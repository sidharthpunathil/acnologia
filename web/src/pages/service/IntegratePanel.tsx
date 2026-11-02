import { useState } from "react";
import { AcnKeyLease, AcnProxy } from "@/components/foundations/icons/acn";
import { moreSnippets, type MoreLang } from "./snippets-more";
import { Link } from "react-router-dom";
import type { ProviderSpec, Service } from "../../lib/types";
import { useProviders } from "../../stores/providers";
import { CodeBlock } from "@/components/acn";
import { RadioGroup } from "@/components/base/radio/radio";
import { RadioCard } from "@/components/base/radio/radio-card";
import { Select, SelectItem } from "@/components/base/select/select";
import { cx } from "@/utils/cx";

type Mode = "proxy" | "lease";
type Lang = "curl" | "python" | "typescript" | "go" | MoreLang;

const LANGS: { id: Lang; label: string }[] = [
  { id: "curl", label: "curl" },
  { id: "python", label: "Python" },
  { id: "typescript", label: "TypeScript" },
  { id: "go", label: "Go" },
  { id: "rust", label: "Rust" },
  { id: "java", label: "Java" },
  { id: "csharp", label: "C#" },
  { id: "cpp", label: "C++" },
  { id: "swift", label: "Swift" },
  { id: "kotlin", label: "Kotlin" },
  { id: "php", label: "PHP" },
  { id: "ruby", label: "Ruby" },
  { id: "dart", label: "Dart" },
];

/** The language picked last, so every service opens in it. */
const LANG_KEY = "acn.integrate.lang";
function savedLang(): Lang {
  try {
    const v = localStorage.getItem(LANG_KEY);
    if (v && LANGS.some((l) => l.id === v)) return v as Lang;
  } catch {
    // Storage can be blocked; curl is a fine default.
  }
  return "curl";
}

const TOKEN = "acn_...";

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

  const more = moreSnippets({ host, slug, path: ex.path, params: ex.params, authType: s.auth_type });

  return {
    proxy: {
      ...more.proxy,
      curl: `curl ${sh(proxyUrl)} \\
  -H "Authorization: Bearer ${TOKEN}"

# Tools that only take a URL can pass the token as a query parameter:
curl ${sh(`${proxyUrl}${q ? "&" : "?"}acn_token=${TOKEN}`)}`,
      python: `from acnologia import Acnologia

c = Acnologia(${lit(host)}, "${TOKEN}")

# Acnologia picks a key, retries on rate limits, and never exposes the secret.
r = c.proxy(${lit(slug)}).get(${lit(ex.path)}, params=${pyDict(ex.params)})
print(r.json())`,
      typescript: `import { Acnologia } from "@acnologia/keys";

const acnologia = new Acnologia({ baseUrl: ${lit(host)}, token: process.env.ACNOLOGIA_TOKEN! });

// Acnologia picks a key, retries on rate limits, and never exposes the secret.
const res = await acnologia.proxy(${lit(slug)}).get(${lit(ex.path)}, ${tsObj(ex.params)});
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
	req.Header.Set("Authorization", "Bearer "+os.Getenv("ACNOLOGIA_TOKEN"))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	fmt.Println(res.Header.Get("X-Acnologia-Key-Id"), string(body))
}`,
    },
    lease: {
      ...more.lease,
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
from acnologia import Acnologia

c = Acnologia(${lit(host)}, "${TOKEN}")

with c.lease(${lit(slug)}) as lease:
    r = requests.get(
        lease.base_url + ${lit(ex.path)},
        ${pyAuth},
    )
    print(r.json())`,
      typescript: `import { Acnologia } from "@acnologia/keys";

const acnologia = new Acnologia({ baseUrl: ${lit(host)}, token: process.env.ACNOLOGIA_TOKEN! });

const data = await acnologia.withKey(${lit(slug)}, async (key) => {
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

	acnologia "github.com/sidharthpunathil/acnologia/sdk/go"
)

func main() {
	ctx := context.Background()
	c := acnologia.New(${lit(host)}, os.Getenv("ACNOLOGIA_TOKEN"))

	err := c.WithKey(ctx, ${lit(slug)}, func(l *acnologia.Lease) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", l.BaseURL+${lit(`${ex.path}${q}`)}, nil)
		l.Apply(req) // puts the key where this service expects it
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			return &acnologia.ErrRateLimited{StatusCode: 429} // WithKey reports it and rotates
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
  const [lang, setLangState] = useState<Lang>(savedLang);
  const setLang = (l: Lang) => {
    setLangState(l);
    try {
      localStorage.setItem(LANG_KEY, l);
    } catch {
      // Not remembered this time; nothing else depends on it.
    }
  };
  const host = window.location.origin;
  const spec = useProviders((st) => st.providers.find((p) => p.slug === service.provider)?.spec);
  const code = snippets(service, host, spec)[mode][lang];

  const modes: { id: Mode; title: string; icon: typeof AcnProxy; body: string }[] = [
    {
      id: "proxy",
      title: "Proxy mode",
      icon: AcnProxy,
      body: `Call Acnologia instead of the provider. It adds a key, retries a rate limit with the next key up to ${service.max_attempts} times, and your app never sees a secret.`,
    },
    {
      id: "lease",
      title: "Lease mode",
      icon: AcnKeyLease,
      body: "Lease a key, call the provider yourself, then report how it went so Acnologia can rest the key and rotate.",
    },
  ];

  return (
    <div className="space-y-5">
      {/* The cards are the mode choice: the whole card selects, arrow keys move between them. */}
      <RadioGroup
        aria-label={`How your app calls ${service.name}`}
        value={mode}
        onChange={(v) => setMode(v as Mode)}
        className="grid grid-cols-1 gap-3 sm:grid-cols-2"
      >
        {modes.map((m) => (
          <RadioCard
            key={m.id}
            value={m.id}
            title={
              <span className="flex items-center gap-2">
                <m.icon className="size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />
                {m.title}
              </span>
            }
            // RadioCard truncates its description; this inner span lets it wrap instead.
            description={<span className="mt-0.5 block whitespace-normal text-body-2-regular">{m.body}</span>}
            className={({ isSelected }) =>
              cx("items-start rounded-3xl p-4 sm:p-5", isSelected && "border-accent-line ring-1 ring-accent-line")
            }
          />
        ))}
      </RadioGroup>

      {/* Thirteen languages: a select reads better than a segmented row that wraps. */}
      <Select
        aria-label="Language"
        selectedKey={lang}
        onSelectionChange={(k) => k && setLang(k as Lang)}
        className="w-full sm:ms-auto sm:w-48"
      >
        {LANGS.map((l) => (
          <SelectItem key={l.id} id={l.id} textValue={l.label}>
            {l.label}
          </SelectItem>
        ))}
      </Select>

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
