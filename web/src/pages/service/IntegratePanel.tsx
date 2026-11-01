import { useState } from "react";
import { Link } from "react-router-dom";
import type { ProviderSpec, Service } from "../../lib/types";
import { useProviders } from "../../stores/providers";
import { CodeBlock } from "@/components/acn";
import { SegmentedControl, SegmentedControlItem } from "@/components/base/segmented-control/segmented-control";
import { cx } from "@/utils/cx";

type Mode = "proxy" | "lease";
type Lang = "curl" | "python" | "typescript" | "go";

const LANGS: { id: Lang; label: string }[] = [
  { id: "curl", label: "curl" },
  { id: "python", label: "Python" },
  { id: "typescript", label: "TypeScript" },
  { id: "go", label: "Go" },
];

const TOKEN = "cog_...";

/*
 * Every value below can come from the server (provider spec, service config),
 * so each one is escaped for the language it lands in before it is pasted into
 * a snippet. Normal inputs render exactly as a hand written example would.
 */

/** A string literal for Python, TypeScript or Go. JSON string syntax is valid in all three. */
const lit = (v: string) => JSON.stringify(v);

/** Single quoted shell word: nothing inside is expanded. */
const shQ = (v: string) => `'${v.replace(/'/g, `'\\''`)}'`;

/**
 * A shell word for `literal` followed by the expansion of `$variable` (if any).
 * Plain text stays in readable double quotes; anything the shell would expand
 * inside double quotes is single quoted instead.
 */
function sh(literal: string, variable?: string): string {
  if (!/[$`\\"!]/.test(literal)) return `"${literal}${variable ? `$${variable}` : ""}"`;
  return shQ(literal) + (variable ? `"$${variable}"` : "");
}

/** Text that is safe inside a JS template literal. */
const tpl = (v: string) => v.replace(/[\\`]/g, (c) => `\\${c}`).replace(/\$\{/g, "\\${");

/** An object key for TypeScript: bare when it is a plain identifier. */
const tsKey = (k: string) => (/^[A-Za-z_$][\w$]*$/.test(k) ? k : lit(k));

/** A representative upstream call, taken from the provider spec, so snippets are runnable. */
function example(_s: Service, spec?: ProviderSpec): { path: string; params: [string, string][] } {
  // Prefer the provider's own first GET example so new providers get runnable snippets too.
  const ex = spec?.examples?.find((e) => e.method === "GET") ?? null;
  if (ex) return { path: ex.path || "/", params: Object.entries(ex.query ?? {}) };
  return { path: "/", params: [] };
}

function qsOf(params: [string, string][]): string {
  return params.length ? `?${params.map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`).join("&")}` : "";
}

function pyDict(params: [string, string][]): string {
  return `{${params.map(([k, v]) => `${lit(k)}: ${lit(v)}`).join(", ")}}`;
}

function tsObj(params: [string, string][]): string {
  return params.length ? `{ ${params.map(([k, v]) => `${tsKey(k)}: ${lit(v)}`).join(", ")} }` : "{}";
}

/** How the leased secret must be attached upstream, per language. */
function authFor(s: Service) {
  const name = s.auth_name || (s.auth_type === "query" ? "api_key" : "Authorization");
  return {
    name,
    curl: s.auth_type === "query" ? "" : s.auth_type === "bearer" ? `-H "Authorization: Bearer $SECRET"` : `-H ${sh(`${name}: `, "SECRET")}`,
    /** The query string key for query auth, already URL encoded; the value is $SECRET. */
    queryKey: s.auth_type === "query" ? `${encodeURIComponent(name)}=` : "",
  };
}

function snippets(s: Service, host: string, spec?: ProviderSpec): Record<Mode, Record<Lang, string>> {
  const ex = example(s, spec);
  const slug = s.slug;
  const a = authFor(s);
  const q = qsOf(ex.params);
  const upstream = `${s.base_url.replace(/\/$/, "")}${ex.path}`;
  const proxyUrl = `${host}/proxy/${slug}${ex.path}${q}`;

  const leaseCurl = a.queryKey ? sh(`${upstream}${q ? `${q}&` : "?"}${a.queryKey}`, "SECRET") : sh(`${upstream}${q}`);

  const pyAuth =
    s.auth_type === "query"
      ? `params={**${pyDict(ex.params)}, ${lit(a.name)}: lease.secret}`
      : s.auth_type === "bearer"
        ? `params=${pyDict(ex.params)},\n        headers={"Authorization": f"Bearer {lease.secret}"}`
        : `params=${pyDict(ex.params)},\n        headers={${lit(a.name)}: lease.secret}`;

  const tsUrl =
    s.auth_type === "query"
      ? `\`\${key.base_url}${tpl(`${ex.path}?${new URLSearchParams([...ex.params]).toString()}${ex.params.length ? "&" : ""}${a.queryKey}`)}\${key.secret}\``
      : `\`\${key.base_url}${tpl(`${ex.path}${q}`)}\``;
  const tsHeaders =
    s.auth_type === "query"
      ? ""
      : s.auth_type === "bearer"
        ? `, {\n    headers: { Authorization: \`Bearer \${key.secret}\` },\n  }`
        : `, {\n    headers: { ${lit(a.name)}: key.secret },\n  }`;

  const leaseBody = `{"service": ${lit(slug)}, "ttl_sec": 300}`;

  return {
    proxy: {
      curl: `curl ${sh(proxyUrl)} \\
  -H "Authorization: Bearer ${TOKEN}"

# Tools that only take a URL can pass the token as a query parameter:
curl ${sh(`${proxyUrl}${q ? "&" : "?"}cogito_token=${TOKEN}`)}`,
      python: `from cogito import Cogito

c = Cogito(${lit(host)}, "${TOKEN}")

# Acnologia picks a key, retries on rate limits, and never exposes the secret.
r = c.proxy(${lit(slug)}).get(${lit(ex.path)}, params=${pyDict(ex.params)})
print(r.json())`,
      typescript: `import { Cogito } from "@cogito/keys";

const cogito = new Cogito({ baseUrl: ${lit(host)}, token: process.env.COGITO_TOKEN! });

// Acnologia picks a key, retries on rate limits, and never exposes the secret.
const res = await cogito.proxy(${lit(slug)}).get(${lit(ex.path)}, ${tsObj(ex.params)});
console.log(await res.json());`,
      go: `package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	req, _ := http.NewRequest("GET", ${lit(proxyUrl)}, nil)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("COGITO_TOKEN"))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	fmt.Println(res.Header.Get("X-Cogito-Key-Id"), string(body))
}`,
    },
    lease: {
      curl: `# 1. Lease a key
curl -X POST "${host}/api/v1/lease" \\
  -H "Authorization: Bearer ${TOKEN}" \\
  -H "Content-Type: application/json" \\
  -d ${shQ(leaseBody)}
# returns {"lease_id", "key_id", "secret", "auth", "base_url", "expires_at"}

# 2. Call the provider yourself
curl ${leaseCurl}${a.curl ? ` \\\n  ${a.curl}` : ""}

# 3. Report the outcome (rotate: true returns the next lease on failure)
curl -X POST "${host}/api/v1/leases/$LEASE_ID/report" \\
  -H "Authorization: Bearer ${TOKEN}" \\
  -H "Content-Type: application/json" \\
  -d '{"outcome": "ok", "status_code": 200}'`,
      python: `import requests
from cogito import Cogito

c = Cogito(${lit(host)}, "${TOKEN}")

with c.lease(${lit(slug)}) as lease:
    r = requests.get(
        lease.base_url + ${lit(ex.path)},
        ${pyAuth},
    )
    print(r.json())`,
      typescript: `import { Cogito } from "@cogito/keys";

const cogito = new Cogito({ baseUrl: ${lit(host)}, token: process.env.COGITO_TOKEN! });

const data = await cogito.withKey(${lit(slug)}, async (key) => {
  const res = await fetch(${tsUrl}${tsHeaders});
  return res.json();
});`,
      go: `package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	cogito "github.com/cogito-keys/cogito-go"
)

func main() {
	ctx := context.Background()
	c := cogito.New(${lit(host)}, os.Getenv("COGITO_TOKEN"))

	err := c.WithKey(ctx, ${lit(slug)}, func(l *cogito.Lease) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", l.BaseURL+${lit(`${ex.path}${q}`)}, nil)
		l.Apply(req) // puts the key where this service expects it
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			return &cogito.ErrRateLimited{StatusCode: 429} // WithKey reports it and rotates
		}
		body, _ := io.ReadAll(res.Body)
		fmt.Println(string(body))
		return nil
	})
	if err != nil {
		panic(err)
	}
}`,
    },
  };
}

export function IntegratePanel({ service }: { service: Service }) {
  const [mode, setMode] = useState<Mode>("proxy");
  const [lang, setLang] = useState<Lang>("curl");
  const host = window.location.origin;
  const spec = useProviders((st) => st.providers.find((p) => p.slug === service.provider)?.spec);
  const code = snippets(service, host, spec)[mode][lang];

  const modeCard = (on: boolean) =>
    cx(
      "rounded-3xl border bg-background-primary-default p-4 transition-[border-color,box-shadow] duration-150 ease sm:p-5",
      on ? "border-accent-line ring-1 ring-accent-line" : "border-border-button-default",
    );

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className={modeCard(mode === "proxy")}>
          <h3 className="text-body-medium text-text-primary">Proxy mode</h3>
          <p className="mt-1 text-body-2-regular text-text-secondary">
            Call Acnologia instead of the provider. It adds a key, retries a rate limit with the next key up to {service.max_attempts} times, and your app never sees a secret.
          </p>
        </div>
        <div className={modeCard(mode === "lease")}>
          <h3 className="text-body-medium text-text-primary">Lease mode</h3>
          <p className="mt-1 text-body-2-regular text-text-secondary">
            Lease a key, call the provider yourself, then report how it went so Acnologia can rest the key and rotate.
          </p>
        </div>
      </div>

      {/* Phones: the language picker drops to its own full width row. */}
      <div className="flex flex-col items-start gap-2 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between sm:gap-3">
        <SegmentedControl
          aria-label="Mode"
          selectedKeys={[mode]}
          onSelectionChange={(keys) => {
            const next = [...keys][0];
            if (next) setMode(next as Mode);
          }}
        >
          <SegmentedControlItem id="proxy">Proxy</SegmentedControlItem>
          <SegmentedControlItem id="lease">Lease</SegmentedControlItem>
        </SegmentedControl>
        <SegmentedControl
          aria-label="Language"
          selectedKeys={[lang]}
          onSelectionChange={(keys) => {
            const next = [...keys][0];
            if (next) setLang(next as Lang);
          }}
          className="w-full sm:w-auto [&>*]:flex-1 sm:[&>*]:flex-none"
        >
          {LANGS.map((l) => (
            <SegmentedControlItem key={l.id} id={l.id}>
              {l.label}
            </SegmentedControlItem>
          ))}
        </SegmentedControl>
      </div>

      <CodeBlock code={code} label={`${mode} · ${LANGS.find((l) => l.id === lang)?.label}`} />

      <p className="wrap-anywhere font-mono text-caption-1-regular text-text-secondary">
        Replace {TOKEN} with an app token from{" "}
        <Link
          to="/apps"
          className="rounded-sm text-text-primary underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
        >
          Apps
        </Link>
        . The app must allow {mode} mode and the {service.slug} service.
      </p>
    </div>
  );
}
