import { useState } from "react";
import { AcnKeyLease, AcnProxy } from "@/components/foundations/icons/acn";
import { RiDownload2Line, RiGlobalLine } from "@remixicon/react";
import { moreSnippets, type MoreLang } from "./snippets-more";
import { forwardSnippets } from "./snippets-forward";
import { Link } from "react-router-dom";
import type { Service } from "../../lib/types";
import { useProviders } from "../../stores/providers";
import { docProviderSlug } from "../../lib/peerProviders";
import { CodeBlock } from "@/components/acn";
import { api } from "../../lib/api";
import { GuideView } from "../../components/GuideView";
import { RadioGroup } from "@/components/base/radio/radio";
import { RadioCard } from "@/components/base/radio/radio-card";
import { Select, SelectItem } from "@/components/base/select/select";
import { cx } from "@/utils/cx";
import type { IconComponent } from "@/components/acn/types";
import { can } from "@/lib/perms";
import { useAuth } from "@/stores/auth";
import { CA_PATH, fallbackProxyUrl, proxyWithToken, useForwardProxy } from "@/lib/forwardProxy";

type Mode = "proxy" | "lease" | "forward";
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

const PLACEHOLDER = "acn_...";
const TOKEN = PLACEHOLDER;

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
function example(spec?: SpecExamples): { path: string; params: [string, string][] } {
  // Prefer the provider's own first GET example so new providers get runnable snippets too.
  const ex = spec?.examples?.find((e) => (e.method ?? "GET").toUpperCase() === "GET") ?? null;
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
/** The parts of a service the snippets read. */
type SnippetService = Pick<Service, "slug" | "base_url" | "auth_type" | "auth_name">;

/** The parts of a provider spec the snippets read: its example calls. */
interface SpecExamples {
  examples?: { method?: string; path?: string; query?: Record<string, string> | null }[] | null;
}

function authFor(s: SnippetService) {
  const name = s.auth_name || (s.auth_type === "query" ? "api_key" : "Authorization");
  return {
    name,
    curl: s.auth_type === "query" ? "" : s.auth_type === "bearer" ? `-H "Authorization: Bearer $SECRET"` : `-H ${sh(`${name}: `, "SECRET")}`,
    /** The query string key for query auth, already URL encoded; the value is $SECRET. */
    queryKey: s.auth_type === "query" ? `${encodeURIComponent(name)}=` : "",
  };
}

function snippets(s: SnippetService, host: string, spec?: SpecExamples, TOKEN = PLACEHOLDER): Record<"proxy" | "lease", Record<Lang, string>> {
  const ex = example(spec);
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

/**
 * Proxy mode snippets for one service, for pages outside the dashboard (the
 * claim page). `token` fills the curl and Python examples; TypeScript reads
 * ACNOLOGIA_TOKEN from the environment.
 */
export function proxySnippets(
  s: SnippetService,
  host: string,
  examples: SpecExamples["examples"],
  token: string,
): Record<"curl" | "python" | "typescript", string> {
  const all = snippets(s, host, { examples }, token).proxy;
  return { curl: all.curl, python: all.python, typescript: all.typescript };
}

/** Forward proxy snippets: the provider's own URL, through `proxyUrl` with the token as its password. */
function forwardCode(s: SnippetService, host: string, proxyUrl: string, spec: SpecExamples | undefined, lang: Lang): string {
  const ex = example(spec);
  const base = `${s.base_url.replace(/\/$/, "")}${ex.path}`;
  return forwardSnippets({ host, proxy: proxyWithToken(proxyUrl, TOKEN), url: `${base}${qsOf(ex.params)}`, base, params: ex.params })[lang];
}

export function IntegratePanel({ service }: { service: Service }) {
  const [picked, setMode] = useState<Mode>("proxy");
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
  const slug = docProviderSlug(service);
  const spec = useProviders((st) => st.providers.find((p) => p.slug === slug)?.spec);
  // A shared service is proxy only: a lease would hand out the share token.
  const proxyOnly = !!service.peer_link_id;
  const mode: Mode = proxyOnly ? "proxy" : picked;
  // The forward proxy's address comes from the server (admins); anyone else gets this page's origin.
  const isAdmin = can.manage(useAuth((st) => st.user?.role));
  const fwd = useForwardProxy(mode === "forward" && isAdmin);
  const fwdUrl = fwd.setup?.proxy_url ?? fallbackProxyUrl();
  const code = mode === "forward" ? forwardCode(service, host, fwdUrl, spec, lang) : snippets(service, host, spec)[mode][lang];

  const allModes: { id: Mode; title: string; icon: IconComponent; body: string }[] = [
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
    {
      id: "forward",
      title: "Forward proxy",
      icon: RiGlobalLine,
      body: "Set HTTPS_PROXY and call the provider's own URL. Acnologia adds a key to every call to this service's host.",
    },
  ];
  const modes = proxyOnly ? allModes.filter((m) => m.id === "proxy") : allModes;

  return (
    <div className="space-y-5">
      {/* The cards are the mode choice: the whole card selects, arrow keys move between them. */}
      <RadioGroup
        aria-label={`How your app calls ${service.name}`}
        value={mode}
        onChange={(v) => setMode(v as Mode)}
        className={cx("grid grid-cols-1 gap-3", !proxyOnly && "sm:grid-cols-2 lg:grid-cols-3")}
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

      {mode === "forward" && <ForwardNote service={service} proxyUrl={fwdUrl} known={!!fwd.setup} />}

      <p className="wrap-anywhere font-mono text-caption-1-regular text-text-secondary">
        Replace {TOKEN} with an app token from{" "}
        <Link
          to="/apps"
          className="rounded-sm text-text-primary underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
        >
          Apps
        </Link>
        . The app must allow {mode === "forward" ? "proxy" : mode} mode and the {service.slug} service.
        {proxyOnly &&
          ` ${service.peer_name || "Another server"} shares this service, so it is proxy only: requests go through that server, which adds its own key.`}
      </p>

      <GuideView
        description={`How to call ${service.name} through this server's proxy: endpoints, examples, limits and errors. Copy for LLM copies it as Markdown for any model or agent.`}
        fetcher={() => api.serviceGuide(service.id)}
        fetchKey={`${service.id}:${service.slug}`}
      />
    </div>
  );
}

const LINK = "rounded-sm text-text-primary underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring";

/** What the forward proxy does with a call, where the CA comes from, and how to pick a service on a shared host. */
function ForwardNote({ service, proxyUrl, known }: { service: Service; proxyUrl: string; known: boolean }) {
  let host = "";
  try {
    host = new URL(service.base_url).host;
  } catch {
    // A base URL that does not parse has no host to name.
  }
  return (
    <div className="flex flex-col gap-2 rounded-2xl border border-border-button-default bg-background-secondary-default p-4 text-body-2-regular text-text-secondary">
      <p className="text-pretty">
        Calls to {host ? <code className="font-mono wrap-anywhere text-text-primary">{host}</code> : "this service's host"} get a key from the pool, with the
        same rotation, limits and logs as Proxy mode. Any key the app sends is replaced. HTTPS needs the Acnologia CA trusted: Node takes{" "}
        <code className="font-mono">NODE_EXTRA_CA_CERTS</code> and <code className="font-mono">--use-env-proxy</code>, Python{" "}
        <code className="font-mono">REQUESTS_CA_BUNDLE</code>, curl <code className="font-mono">--cacert</code>.
      </p>
      <p className="text-pretty">
        When several services share a host, send <code className="font-mono wrap-anywhere">X-Acnologia-Service: {service.slug}</code> to pick this one (or a group).
      </p>
      <p className="wrap-anywhere">
        Proxy: <code className="font-mono text-text-primary">{proxyUrl}</code>
        {!known && " (this page's address; an admin can see the forward proxy's own address in Settings)"}
      </p>
      <a href={CA_PATH} download="acnologia-ca.pem" className={cx(LINK, "inline-flex w-fit items-center gap-1.5 text-body-2-medium")}>
        <RiDownload2Line className="size-4 shrink-0 text-foreground-icon-secondary" aria-hidden />
        Download CA certificate
      </a>
    </div>
  );
}
