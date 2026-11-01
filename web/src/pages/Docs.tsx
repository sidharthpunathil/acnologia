import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import {
  ArrowRight,
  Bell,
  Bot,
  Boxes,
  Coins,
  DatabaseZap,
  Gauge,
  GitBranch,
  Globe,
  KeyRound,
  Layers,
  LockKeyhole,
  RotateCw,
  ScrollText,
  ShieldCheck,
  Terminal,
  Timer,
  Waypoints,
  Zap,
} from "lucide-react";
import { useTitle } from "../hooks/useTitle";
import { useAuth } from "../stores/auth";
import { Wordmark } from "../components/Layout";
import { CodeBlock } from "../components/CodeBlock";
import { cx } from "../components/ui";

const SECTIONS = [
  { id: "how", label: "How it works" },
  { id: "calling", label: "Calling it" },
  { id: "failures", label: "When a key fails" },
  { id: "features", label: "Features" },
  { id: "providers", label: "Providers" },
  { id: "integrate", label: "Integrate" },
  { id: "operate", label: "Operate" },
  { id: "security", label: "Security" },
];

const STEPS = [
  { title: "Add a service", body: "Pick a provider from the catalog, such as SerpAPI or ElevenLabs, or describe any HTTP API and let the AI builder write its rules." },
  { title: "Paste your keys", body: "One per line, or label,secret. Cogito encrypts each key with AES-256-GCM and checks it against the provider." },
  { title: "Create an app", body: "Each website, worker or agent gets its own cog_ token, limited to the services and modes you allow." },
  { title: "Call through Cogito", body: "Your app sends the request to Cogito. Cogito picks a key with quota left, attaches it and passes the answer back." },
];

const OUTCOMES: Array<{ name: string; when: string; effect: string }> = [
  { name: "rate_limited", when: "429, an hourly cap, a Retry-After header", effect: "Cools down for the time the provider asked for, or a backoff that doubles per failure up to an hour. The next key takes the request." },
  { name: "busy", when: "The account is at its concurrency cap", effect: "Steps aside for about a second, with no backoff. The next key takes the request." },
  { name: "quota_exhausted", when: "Out of searches, characters or credits", effect: "Parked until its quota resets, then returns to rotation on its own." },
  { name: "invalid", when: "The provider rejects the key", effect: "Leaves rotation. A passing health check brings it back." },
  { name: "banned", when: "The provider suspended the account", effect: "Leaves rotation until you reset it. Health checks skip it." },
  { name: "error", when: "5xx, timeouts, refused connections", effect: "The key is not blamed. Ten of these in a minute pause the whole service." },
];

interface Feature {
  icon: typeof Zap;
  title: string;
  body: ReactNode;
}

const FEATURE_GROUPS: Array<{ title: string; intro: string; items: Feature[] }> = [
  {
    title: "Keep requests flowing",
    intro: "What happens between your app and the provider.",
    items: [
      { icon: RotateCw, title: "Rotation strategies", body: "Round robin, fill first by priority, least used today, most quota left, or weighted random. Set per service." },
      { icon: Timer, title: "Cooldowns that follow the provider", body: "Cogito reads Retry-After, provider error text and reset times, so a key comes back when the provider says it can." },
      { icon: Gauge, title: "Concurrency limits", body: "Cap parallel requests per key (ElevenLabs allows 2 on its free plan). Extra requests wait in a queue for a free slot." },
      { icon: Zap, title: "Circuit breaker", body: "When the provider itself keeps failing, Cogito answers 503 at once and sends one probe every 30 seconds until it recovers." },
      { icon: Layers, title: "Backup keys", body: "Mark keys as backup and they only serve when every primary key is cooling, busy or out of quota." },
      { icon: GitBranch, title: "Service groups", body: "Put two accounts or two regions of one API behind a single slug. A request that one member cannot serve moves to the next." },
    ],
  },
  {
    title: "Spend less quota",
    intro: "Every request Cogito answers itself is a search or a character you keep.",
    items: [
      { icon: DatabaseZap, title: "Response cache", body: "Identical GET requests to search APIs come back from memory with X-Cogito-Cache: hit and cost nothing upstream." },
      { icon: ShieldCheck, title: "Idempotency keys", body: "Send Idempotency-Key and a retried POST within 10 minutes replays the first answer instead of paying twice." },
      { icon: Gauge, title: "Live quota", body: "Health checks read remaining quota from account endpoints, and providers that send quota headers update it on every response." },
      { icon: Coins, title: "Spend and budgets", body: "Each provider has a price per request. Give an app a daily or monthly request cap or USD budget and Cogito holds it there." },
    ],
  },
  {
    title: "Control who uses what",
    intro: "Keys stay on the server. Apps get access, never secrets, unless you allow it.",
    items: [
      { icon: KeyRound, title: "Per app tokens", body: "Each app has its own token, its own list of services and a mode: proxy, lease or both. Rotate a token in one click." },
      { icon: LockKeyhole, title: "Hidden and restricted keys", body: "Hidden keys are visible only to superadmins. Restricted keys serve only the apps you name." },
      { icon: Globe, title: "Egress proxies", body: "Send a service or a single key through an HTTP, HTTPS or SOCKS5 proxy, for example to call from a specific region." },
      { icon: Boxes, title: "Roles", body: "Members read, admins manage services, keys and apps, superadmins also see hidden keys, users and the audit log." },
    ],
  },
  {
    title: "Know what happened",
    intro: "Find out about a dead key from Cogito before your users notice.",
    items: [
      { icon: Bell, title: "Webhooks", body: "Slack, Discord, Telegram or signed JSON when a key goes bad, quota drops under 10%, a pool runs dry, a provider goes down or an app hits its limit." },
      { icon: ScrollText, title: "Activity and audit log", body: "Every proxied request, lease and health check with its key, app, status and latency. Every dashboard change and secret reveal is audited." },
      { icon: Waypoints, title: "Key insights", body: "Each key shows whether it works, when it last worked, its quota and 14 days of requests and failures." },
    ],
  },
];

const PROVIDERS = [
  ["SerpAPI", "Search"],
  ["Serper", "Search"],
  ["Brave Search", "Search"],
  ["Tavily", "Search"],
  ["Exa", "Search"],
  ["Firecrawl", "Search"],
  ["Jina Search", "Search"],
  ["SearchApi", "Search"],
  ["Linkup", "Search"],
  ["You.com Search", "Search"],
  ["ElevenLabs", "Voice"],
  ["Custom API", "Any key based HTTP API"],
];

const PROXY_EXAMPLE = `curl "https://cogito.example.com/proxy/serpapi/search.json?q=coffee" \\
  -H "Authorization: Bearer cog_your_app_token"`;

const LEASE_EXAMPLE = `# borrow a key
curl -X POST https://cogito.example.com/api/v1/lease \\
  -H "Authorization: Bearer cog_your_app_token" \\
  -H "Content-Type: application/json" -d '{"service":"serpapi"}'

# tell Cogito how it went, and get the next key if it failed
curl -X POST https://cogito.example.com/api/v1/leases/lse_.../report \\
  -H "Authorization: Bearer cog_your_app_token" \\
  -H "Content-Type: application/json" \\
  -d '{"outcome":"rate_limited","retry_after_sec":60,"rotate":true}'`;

const SDK_EXAMPLE = `from cogito import Cogito

cog = Cogito("https://cogito.example.com", "cog_your_app_token")
resp = cog.proxy("serpapi").get("/search.json", params={"q": "coffee"})
print(resp.json()["organic_results"][0]["title"])`;

const MCP_EXAMPLE = `claude mcp add --transport http cogito https://cogito.example.com/mcp \\
  --header "Authorization: Bearer cog_your_app_token"`;

const CTL_EXAMPLE = `cogito ctl login https://cogito.example.com --token cogadm_...
cogito ctl status
cogito ctl keys add serpapi < keys.txt
cogito ctl events --service serpapi --kind rate_limited`;

const SELF_HOST = `cp .env.example .env
docker run --rm cogito:latest gen-master-key   # paste into COGITO_MASTER_KEY
docker compose up -d --build
open http://localhost:8080                      # the first visit creates the superadmin`;

function Heading({ id, eyebrow, title, children }: { id: string; eyebrow: string; title: string; children?: ReactNode }) {
  return (
    <div id={id} className="scroll-mt-24">
      <p className="font-mono text-[11px] uppercase tracking-[0.14em] text-faint">{eyebrow}</p>
      <h2 className="mt-2 text-balance text-2xl font-semibold tracking-tight text-ink-strong sm:text-[28px]">{title}</h2>
      {children ? <div className="mt-3 max-w-2xl text-pretty text-[15px] leading-relaxed text-muted">{children}</div> : null}
    </div>
  );
}

function Code({ children }: { children: ReactNode }) {
  return <code className="rounded bg-well px-1.5 py-0.5 font-mono text-[12.5px] text-ink">{children}</code>;
}

export function DocsPage() {
  useTitle("Docs");
  const signedIn = useAuth((s) => s.status === "authenticated");

  return (
    <div className="min-h-dvh bg-canvas text-ink">
      <header className="sticky top-0 z-20 border-b border-line bg-canvas/90 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-4 px-4 sm:px-6">
          <Link to="/" className="tap flex items-center gap-3" aria-label="Cogito home">
            <Wordmark className="h-6" />
            <span className="font-mono text-xs text-faint">docs</span>
          </Link>
          <nav className="hidden items-center gap-5 text-[13px] text-muted lg:flex" aria-label="On this page">
            {SECTIONS.map((s) => (
              <a key={s.id} href={`#${s.id}`} className="transition-colors hover:text-ink">
                {s.label}
              </a>
            ))}
          </nav>
          <Link
            to="/"
            className="tap inline-flex h-8 items-center gap-1.5 rounded-lg bg-ink px-3 text-[13px] font-medium text-canvas transition-opacity hover:opacity-90"
          >
            {signedIn ? "Open dashboard" : "Sign in"}
            <ArrowRight className="h-3.5 w-3.5" aria-hidden />
          </Link>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 pb-24 sm:px-6">
        {/* Hero */}
        <section className="pb-16 pt-14 sm:pt-20">
          <p className="font-mono text-xs text-faint">Self-hosted API key pool</p>
          <h1 className="mt-4 max-w-3xl text-balance text-[34px] font-semibold leading-[1.08] tracking-tight text-ink-strong sm:text-5xl">
            One endpoint in front of all your API keys
          </h1>
          <p className="mt-5 max-w-2xl text-pretty text-[17px] leading-relaxed text-muted">
            Cogito stores many keys per provider and sends each request with one that has quota left. When the provider
            answers rate limited, out of quota or suspended, Cogito parks that key and retries with the next one. It runs
            as a single Go binary with SQLite, on your own server.
          </p>
          <div className="mt-8 flex flex-wrap gap-3">
            <a
              href="#how"
              className="tap inline-flex h-10 items-center gap-2 rounded-lg bg-ink px-4 text-sm font-medium text-canvas transition-opacity hover:opacity-90"
            >
              See how it works
              <ArrowRight className="h-4 w-4" aria-hidden />
            </a>
            <a
              href="/llms-full.txt"
              className="tap inline-flex h-10 items-center gap-2 rounded-lg border border-line bg-surface px-4 text-sm font-medium text-ink transition-colors hover:border-line-strong"
            >
              Read the API reference
            </a>
          </div>
          <div className="mt-12 max-w-2xl">
            <CodeBlock label="your app, before and after" code={PROXY_EXAMPLE} />
            <p className="mt-3 text-[13px] text-faint">
              Same path and query you would send to SerpAPI, minus <Code>api_key</Code>. Cogito adds the key.
            </p>
          </div>
        </section>

        {/* How it works */}
        <section className="border-t border-line py-16">
          <Heading id="how" eyebrow="How it works" title="Four steps from a pile of keys to one URL" />
          <ol className="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {STEPS.map((s, i) => (
              <li key={s.title} className="rounded-xl border border-line bg-surface p-5 shadow-card">
                <span className="font-mono text-xs text-faint">0{i + 1}</span>
                <h3 className="mt-2 font-medium text-ink-strong">{s.title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-muted">{s.body}</p>
              </li>
            ))}
          </ol>
        </section>

        {/* Calling */}
        <section className="border-t border-line py-16">
          <Heading id="calling" eyebrow="Calling it" title="Proxy the request, or borrow the key">
            Proxy mode is the default. Use a lease when a provider SDK insists on holding the raw key, and report back so
            Cogito knows whether the key still works.
          </Heading>
          <div className="mt-10 grid gap-6 lg:grid-cols-2">
            <div className="min-w-0">
              <h3 className="font-medium text-ink-strong">Proxy mode</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-muted">
                Call <Code>/proxy/{"{service}"}/...</Code> exactly as you would call the provider. Your app never sees a
                secret. Responses add <Code>X-Cogito-Key-Id</Code> and <Code>X-Cogito-Attempts</Code> so you can trace
                which key answered.
              </p>
              <div className="mt-4">
                <CodeBlock label="proxy" code={PROXY_EXAMPLE} />
              </div>
            </div>
            <div className="min-w-0">
              <h3 className="font-medium text-ink-strong">Lease mode</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-muted">
                Ask for a key, call the provider yourself, then report the outcome. With <Code>rotate: true</Code> the
                report hands you the next key in the same response.
              </p>
              <div className="mt-4">
                <CodeBlock label="lease" code={LEASE_EXAMPLE} />
              </div>
            </div>
          </div>
        </section>

        {/* Failures */}
        <section className="border-t border-line py-16">
          <Heading id="failures" eyebrow="When a key fails" title="Six outcomes, each with its own recovery">
            Every provider error is sorted into one of these by the provider's rules. A key that hit an hourly cap is
            back in an hour. A provider outage is never blamed on your keys.
          </Heading>
          <div className="mt-10 overflow-hidden rounded-xl border border-line bg-surface shadow-card">
            <table className="w-full text-left text-sm">
              <thead className="hidden border-b border-line bg-soft text-[12px] text-faint sm:table-header-group">
                <tr>
                  <th className="px-4 py-2.5 font-medium">Outcome</th>
                  <th className="px-4 py-2.5 font-medium">When</th>
                  <th className="px-4 py-2.5 font-medium">What Cogito does</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {OUTCOMES.map((o) => (
                  <tr key={o.name} className="block px-4 py-3 sm:table-row sm:p-0">
                    <td className="block font-mono text-[13px] text-ink-strong sm:table-cell sm:px-4 sm:py-3 sm:align-top">{o.name}</td>
                    <td className="block text-muted sm:table-cell sm:px-4 sm:py-3 sm:align-top">{o.when}</td>
                    <td className="mt-1 block text-ink sm:mt-0 sm:table-cell sm:px-4 sm:py-3 sm:align-top">{o.effect}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        {/* Features */}
        <section className="border-t border-line py-16">
          <Heading id="features" eyebrow="Features" title="Everything Cogito does" />
          <div className="mt-10 space-y-14">
            {FEATURE_GROUPS.map((g) => (
              <div key={g.title}>
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <h3 className="text-lg font-semibold text-ink-strong">{g.title}</h3>
                  <p className="text-sm text-faint">{g.intro}</p>
                </div>
                <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                  {g.items.map((f) => (
                    <div key={f.title} className="rounded-xl border border-line bg-surface p-5 shadow-card">
                      <f.icon className="h-4 w-4 text-muted" aria-hidden />
                      <h4 className="mt-3 font-medium text-ink-strong">{f.title}</h4>
                      <p className="mt-1.5 text-sm leading-relaxed text-muted">{f.body}</p>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </section>

        {/* Providers */}
        <section className="border-t border-line py-16">
          <Heading id="providers" eyebrow="Providers" title="Twelve built in, and a builder for the rest">
            A provider is a JSON spec: where the key goes, which error bodies mean rate limited or out of quota, how to
            read remaining quota, and fixture tests that prove the rules. Publishing a spec takes effect without a
            restart, and every version can be rolled back.
          </Heading>
          <div className="mt-8 flex flex-wrap gap-2">
            {PROVIDERS.map(([name, cat]) => (
              <span key={name} className="inline-flex items-center gap-2 rounded-full border border-line bg-surface px-3 py-1.5 text-[13px]">
                <span className="text-ink">{name}</span>
                <span className="text-faint">{cat}</span>
              </span>
            ))}
          </div>
          <div className="mt-8 flex max-w-3xl gap-4 rounded-xl border border-line bg-surface p-5 shadow-card">
            <Bot className="mt-0.5 h-5 w-5 shrink-0 text-muted" aria-hidden />
            <div className="text-sm leading-relaxed text-muted">
              <p className="font-medium text-ink-strong">Build a provider with AI</p>
              <p className="mt-1.5">
                Give the builder a name and a docs URL. It reads the official docs, writes the rules, health check and
                tests, runs the tests through the real engine, finds the logo and publishes. A failing test blocks the
                publish. Turn it on by setting <Code>ANTHROPIC_API_KEY</Code>.
              </p>
            </div>
          </div>
        </section>

        {/* Integrate */}
        <section className="border-t border-line py-16">
          <Heading id="integrate" eyebrow="Integrate" title="SDKs, MCP and plain HTTP">
            Any HTTP client works. The SDKs add lease helpers that always report, and a distinct error when every key is
            busy so you know to retry shortly.
          </Heading>
          <div className="mt-10 grid gap-6 lg:grid-cols-2">
            <div className="min-w-0">
              <h3 className="font-medium text-ink-strong">SDKs</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-muted">
                Python, TypeScript and JavaScript, Go, PHP and Ruby.
              </p>
              <div className="mt-4">
                <CodeBlock label="python" code={SDK_EXAMPLE} />
              </div>
            </div>
            <div className="min-w-0">
              <h3 className="font-medium text-ink-strong">MCP for agents</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-muted">
                <Code>POST /mcp</Code> exposes <Code>list_services</Code>, <Code>pool_status</Code>, <Code>get_key</Code>,{" "}
                <Code>report_key</Code> and <Code>proxy_request</Code>. Agents can also read <Code>/llms.txt</Code>.
              </p>
              <div className="mt-4">
                <CodeBlock label="claude code" code={MCP_EXAMPLE} />
              </div>
            </div>
          </div>
          <div className="mt-8 rounded-xl border border-line bg-surface p-5 text-sm shadow-card">
            <p className="font-medium text-ink-strong">Status codes your app should handle</p>
            <ul className="mt-3 grid gap-2 text-muted sm:grid-cols-2">
              <li><Code>503 no_key_available</Code> every key is cooling or out of quota; wait <Code>retry_after_sec</Code>.</li>
              <li><Code>503 all_keys_busy</Code> keys exist but all are at their concurrency limit; retry in a second.</li>
              <li><Code>503 service_unavailable</Code> the provider keeps failing and Cogito paused it.</li>
              <li><Code>429 app_limit_reached</Code> your app used its daily or monthly limit; see <Code>reset_at</Code>.</li>
            </ul>
          </div>
        </section>

        {/* Operate */}
        <section className="border-t border-line py-16">
          <Heading id="operate" eyebrow="Operate" title="Run it from the terminal too">
            Create an API token under Account, then script Cogito from CI or your laptop. The same binary that runs the
            server is the client.
          </Heading>
          <div className="mt-10 grid gap-6 lg:grid-cols-2">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <Terminal className="h-4 w-4 text-muted" aria-hidden />
                <h3 className="font-medium text-ink-strong">cogito ctl</h3>
              </div>
              <p className="mt-1.5 text-sm leading-relaxed text-muted">
                Tokens are scoped read, write or admin, never above your own role.
              </p>
              <div className="mt-4">
                <CodeBlock label="terminal" code={CTL_EXAMPLE} />
              </div>
            </div>
            <div className="min-w-0">
              <h3 className="font-medium text-ink-strong">Self-host in a few minutes</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-muted">
                Back up the data volume and the master key together. Without the master key, stored keys cannot be
                decrypted.
              </p>
              <div className="mt-4">
                <CodeBlock label="docker" code={SELF_HOST} />
              </div>
            </div>
          </div>
        </section>

        {/* Security */}
        <section className="border-t border-line py-16">
          <Heading id="security" eyebrow="Security" title="Built for keys you cannot afford to leak" />
          <ul className="mt-8 grid gap-x-8 gap-y-4 text-sm leading-relaxed text-muted sm:grid-cols-2">
            {[
              "Keys are encrypted at rest with AES-256-GCM. App and API tokens are stored as hashes and shown once.",
              "Proxied responses have the key scrubbed from headers, run under a sandbox CSP, and redirects are passed back instead of followed with a key attached.",
              "Logo fetches, AI live checks and webhooks cannot reach private networks.",
              "Responses made with hidden or restricted keys, or that echo a key, are never cached for other apps.",
              "Every dashboard change and every secret reveal goes to the audit log.",
              "Search engines and AI crawlers are told to stay out, and pages cannot be framed.",
            ].map((t) => (
              <li key={t} className="flex gap-3">
                <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-faint" aria-hidden />
                <span>{t}</span>
              </li>
            ))}
          </ul>
        </section>

        {/* Final CTA */}
        <section className="rounded-2xl border border-line bg-surface px-6 py-10 text-center shadow-card sm:px-10">
          <h2 className="text-balance text-2xl font-semibold tracking-tight text-ink-strong">Add your first service</h2>
          <p className="mx-auto mt-3 max-w-xl text-pretty text-[15px] leading-relaxed text-muted">
            Pick a provider, paste your keys and point one app at Cogito. The full API reference lives at{" "}
            <a className="text-ink underline decoration-line-strong underline-offset-4" href="/llms-full.txt">
              /llms-full.txt
            </a>
            .
          </p>
          <div className="mt-6 flex justify-center">
            <Link
              to="/"
              className={cx(
                "tap inline-flex h-10 items-center gap-2 rounded-lg bg-ink px-4 text-sm font-medium text-canvas transition-opacity hover:opacity-90",
              )}
            >
              {signedIn ? "Open the catalog" : "Sign in to Cogito"}
              <ArrowRight className="h-4 w-4" aria-hidden />
            </Link>
          </div>
        </section>
      </main>
    </div>
  );
}
