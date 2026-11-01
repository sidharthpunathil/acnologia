import { useState } from "react";
import { Link } from "react-router-dom";
import type { ProviderSpec, Service } from "../../lib/types";
import { useProviders } from "../../stores/providers";
import { CodeBlock } from "../../components/CodeBlock";
import { Card, cx } from "../../components/ui";

type Mode = "proxy" | "lease";
type Lang = "curl" | "python" | "typescript" | "go";

const LANGS: { id: Lang; label: string }[] = [
  { id: "curl", label: "curl" },
  { id: "python", label: "Python" },
  { id: "typescript", label: "TypeScript" },
  { id: "go", label: "Go" },
];

const TOKEN = "cog_...";

/** A representative upstream call, taken from the provider spec, so snippets are runnable. */
function example(_s: Service, spec?: ProviderSpec): { path: string; params: [string, string][] } {
  // Prefer the provider's own first GET example so new providers get runnable snippets too.
  const ex = spec?.examples?.find((e) => e.method === "GET") ?? null;
  if (ex) return { path: ex.path || "/", params: Object.entries(ex.query ?? {}) };
  return { path: "/", params: [] };
}

function qsOf(params: [string, string][]): string {
  return params.length ? `?${params.map(([k, v]) => `${k}=${encodeURIComponent(v)}`).join("&")}` : "";
}

function pyDict(params: [string, string][]): string {
  return `{${params.map(([k, v]) => `"${k}": "${v}"`).join(", ")}}`;
}

function tsObj(params: [string, string][]): string {
  return params.length ? `{ ${params.map(([k, v]) => `${k}: "${v}"`).join(", ")} }` : "{}";
}

/** How the leased secret must be attached upstream, per language. */
function authFor(s: Service) {
  const name = s.auth_name || (s.auth_type === "query" ? "api_key" : "Authorization");
  return {
    name,
    curl: s.auth_type === "query" ? "" : s.auth_type === "bearer" ? `-H "Authorization: Bearer $SECRET"` : `-H "${name}: $SECRET"`,
    curlQuery: s.auth_type === "query" ? `${name}=$SECRET` : "",
  };
}

function snippets(s: Service, host: string, spec?: ProviderSpec): Record<Mode, Record<Lang, string>> {
  const ex = example(s, spec);
  const slug = s.slug;
  const a = authFor(s);
  const q = qsOf(ex.params);
  const upstream = `${s.base_url.replace(/\/$/, "")}${ex.path}`;

  const leaseQuery = a.curlQuery ? (q ? `${q}&${a.curlQuery}` : `?${a.curlQuery}`) : q;

  const pyAuth =
    s.auth_type === "query"
      ? `params={**${pyDict(ex.params)}, "${a.name}": lease.secret}`
      : s.auth_type === "bearer"
        ? `params=${pyDict(ex.params)},\n        headers={"Authorization": f"Bearer {lease.secret}"}`
        : `params=${pyDict(ex.params)},\n        headers={"${a.name}": lease.secret}`;

  const tsUrl =
    s.auth_type === "query"
      ? `\`\${key.base_url}${ex.path}?${new URLSearchParams([...ex.params]).toString()}${ex.params.length ? "&" : ""}${a.name}=\${key.secret}\``
      : `\`\${key.base_url}${ex.path}${q}\``;
  const tsHeaders =
    s.auth_type === "query"
      ? ""
      : s.auth_type === "bearer"
        ? `, {\n    headers: { Authorization: \`Bearer \${key.secret}\` },\n  }`
        : `, {\n    headers: { "${a.name}": key.secret },\n  }`;


  return {
    proxy: {
      curl: `curl "${host}/proxy/${slug}${ex.path}${q}" \\
  -H "Authorization: Bearer ${TOKEN}"

# Tools that only take a URL can pass the token as a query parameter:
curl "${host}/proxy/${slug}${ex.path}${q ? q + "&" : "?"}cogito_token=${TOKEN}"`,
      python: `from cogito import Cogito

c = Cogito("${host}", "${TOKEN}")

# Cogito picks a key, retries on rate limits, and never exposes the secret.
r = c.proxy("${slug}").get("${ex.path}", params=${pyDict(ex.params)})
print(r.json())`,
      typescript: `import { Cogito } from "@cogito/keys";

const cogito = new Cogito({ baseUrl: "${host}", token: process.env.COGITO_TOKEN! });

// Cogito picks a key, retries on rate limits, and never exposes the secret.
const res = await cogito.proxy("${slug}").get("${ex.path}", ${tsObj(ex.params)});
console.log(await res.json());`,
      go: `package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	req, _ := http.NewRequest("GET", "${host}/proxy/${slug}${ex.path}${q}", nil)
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
  -d '{"service": "${slug}", "ttl_sec": 300}'
# returns {"lease_id", "key_id", "secret", "auth", "base_url", "expires_at"}

# 2. Call the provider yourself
curl "${upstream}${leaseQuery}"${a.curl ? ` \\\n  ${a.curl}` : ""}

# 3. Report the outcome (rotate: true returns the next lease on failure)
curl -X POST "${host}/api/v1/leases/$LEASE_ID/report" \\
  -H "Authorization: Bearer ${TOKEN}" \\
  -H "Content-Type: application/json" \\
  -d '{"outcome": "ok", "status_code": 200}'`,
      python: `import requests
from cogito import Cogito

c = Cogito("${host}", "${TOKEN}")

with c.lease("${slug}") as lease:
    r = requests.get(
        f"{lease.base_url}${ex.path}",
        ${pyAuth},
    )
    print(r.json())`,
      typescript: `import { Cogito } from "@cogito/keys";

const cogito = new Cogito({ baseUrl: "${host}", token: process.env.COGITO_TOKEN! });

const data = await cogito.withKey("${slug}", async (key) => {
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
	c := cogito.New("${host}", os.Getenv("COGITO_TOKEN"))

	err := c.WithKey(ctx, "${slug}", func(l *cogito.Lease) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", l.BaseURL+"${ex.path}${q}", nil)
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

function Seg<T extends string>({ items, value, onChange }: { items: { id: T; label: string }[]; value: T; onChange: (v: T) => void }) {
  return (
    <div className="inline-flex rounded-full bg-[#ebeae4] p-0.5">
      {items.map((it) => (
        <button
          key={it.id}
          type="button"
          onClick={() => onChange(it.id)}
          className={cx(
            "rounded-full px-3 py-1 text-[13px] transition-colors",
            value === it.id ? "bg-white font-medium text-ink shadow-card" : "text-muted hover:text-ink",
          )}
        >
          {it.label}
        </button>
      ))}
    </div>
  );
}

export function IntegratePanel({ service }: { service: Service }) {
  const [mode, setMode] = useState<Mode>("proxy");
  const [lang, setLang] = useState<Lang>("curl");
  const host = window.location.origin;
  const spec = useProviders((st) => st.providers.find((p) => p.slug === service.provider)?.spec);
  const code = snippets(service, host, spec)[mode][lang];

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Card className={cx("p-4", mode === "proxy" && "ring-1 ring-ink/20")}>
          <div className="text-sm font-medium text-ink">Proxy mode</div>
          <p className="mt-1 text-[13px] text-muted">
            Call Cogito instead of the provider. It injects a key, retries rate limits with the next key up to {service.max_attempts} times, and your app never sees a secret.
          </p>
        </Card>
        <Card className={cx("p-4", mode === "lease" && "ring-1 ring-ink/20")}>
          <div className="text-sm font-medium text-ink">Lease mode</div>
          <p className="mt-1 text-[13px] text-muted">
            Ask for a key, call the provider yourself, then report the outcome so Cogito can cool the key down and rotate.
          </p>
        </Card>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <Seg items={[{ id: "proxy" as Mode, label: "Proxy" }, { id: "lease" as Mode, label: "Lease" }]} value={mode} onChange={setMode} />
        <Seg items={LANGS} value={lang} onChange={setLang} />
      </div>

      <CodeBlock code={code} label={`${mode} · ${LANGS.find((l) => l.id === lang)?.label}`} />

      <p className="font-mono text-xs text-muted">
        Replace {TOKEN} with an app token from <Link to="/apps" className="text-ink underline underline-offset-2">Apps</Link>. The app must allow mode {mode} and service {service.slug}.
      </p>
    </div>
  );
}
