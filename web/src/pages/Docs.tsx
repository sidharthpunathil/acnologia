import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import {
  RiArrowRightLine,
  RiBellLine,
  RiCoinsLine,
  RiDashboard3Line,
  RiDatabase2Line,
  RiFlashlightLine,
  RiGitBranchLine,
  RiGlobalLine,
  RiKey2Line,
  RiLockLine,
  RiNodeTree,
  RiRefreshLine,
  RiRobot2Line,
  RiShieldCheckLine,
  RiStackLine,
  RiTeamLine,
  RiTerminalBoxLine,
  RiTimerLine,
  RiFileListLine,
} from "@remixicon/react";
import { useTitle } from "@/hooks/useTitle";
import { useAuth } from "@/stores/auth";
import { useTheme } from "@/stores/theme";
import { CodeBlock } from "@/components/CodeBlock";
import type { IconComponent } from "@/components/acn/types";
import { cx } from "@/utils/cx";
import { ButtonLink } from "@/components/base/buttons/button";
import { HeroDemo } from "../demos/HeroDemo";
import { AlertsDemo } from "../demos/AlertsDemo";
import { RecoveryDemo } from "../demos/RecoveryDemo";
import { DocsHeader } from "./docs/DocsHeader";
import { DOCS } from "./docs/manifest";
import { RouterButton } from "./docs/RouterButton";

const SECTIONS = [
  { id: "how", label: "How it works" },
  { id: "calling", label: "Calling it" },
  { id: "failures", label: "When a key fails" },
  { id: "features", label: "Features" },
  { id: "providers", label: "Providers" },
  { id: "integrate", label: "Integrate" },
  { id: "operate", label: "Operate" },
  { id: "security", label: "Security" },
  { id: "guides", label: "Guides" },
];

const STEPS = [
  { title: "Add a service", body: "Pick a provider from the catalog, such as SerpAPI or ElevenLabs, or describe any HTTP API and let the AI builder write its rules." },
  { title: "Paste your keys", body: "One per line, or label,secret. Acnologia encrypts each key with AES-256-GCM and checks it against the provider." },
  { title: "Create an app", body: "Each website, worker or agent gets its own cog_ token, limited to the services and modes you allow." },
  { title: "Call through Acnologia", body: "Your app sends the request to Acnologia. Acnologia picks a key with quota left, attaches it and passes the answer back." },
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
  icon: IconComponent;
  title: string;
  body: ReactNode;
}

const FEATURE_GROUPS: Array<{ title: string; intro: string; items: Feature[]; demo?: boolean }> = [
  {
    title: "Keep requests flowing",
    intro: "What happens between your app and the provider.",
    items: [
      { icon: RiRefreshLine, title: "Rotation strategies", body: "Round robin, fill first by priority, least used today, most quota left, or weighted random. Set per service." },
      { icon: RiTimerLine, title: "Cooldowns that follow the provider", body: "Acnologia reads Retry-After, provider error text and reset times, so a key comes back when the provider says it can." },
      { icon: RiDashboard3Line, title: "Concurrency limits", body: "Cap parallel requests per key (ElevenLabs allows 2 on its free plan). Extra requests wait in a queue for a free slot." },
      { icon: RiFlashlightLine, title: "Circuit breaker", body: "When the provider itself keeps failing, Acnologia answers 503 at once and sends one probe every 30 seconds until it recovers." },
      { icon: RiStackLine, title: "Backup keys", body: "Mark keys as backup and they only serve when every primary key is cooling, busy or out of quota." },
      { icon: RiGitBranchLine, title: "Service groups", body: "Put two accounts or two regions of one API behind a single slug. A request that one member cannot serve moves to the next." },
    ],
  },
  {
    title: "Spend less quota",
    intro: "Every request Acnologia answers itself is a search or a character you keep.",
    items: [
      { icon: RiDatabase2Line, title: "Response cache", body: "Identical GET requests to search APIs come back from memory with X-Cogito-Cache: hit and cost nothing upstream." },
      { icon: RiShieldCheckLine, title: "Idempotency keys", body: "Send Idempotency-Key and a retried POST within 10 minutes replays the first answer instead of paying twice." },
      { icon: RiDashboard3Line, title: "Live quota", body: "Health checks read remaining quota from account endpoints, and providers that send quota headers update it on every response." },
      { icon: RiCoinsLine, title: "Spend and budgets", body: "Each provider has a price per request. Give an app a daily or monthly request cap or USD budget and Acnologia holds it there." },
    ],
  },
  {
    title: "Control who uses what",
    intro: "Keys stay on the server. Apps get access, never secrets, unless you allow it.",
    items: [
      { icon: RiKey2Line, title: "Per app tokens", body: "Each app has its own token, its own list of services and a mode: proxy, lease or both. Rotate a token in one click." },
      { icon: RiLockLine, title: "Hidden and restricted keys", body: "Hidden keys are visible only to superadmins. Restricted keys serve only the apps you name." },
      { icon: RiGlobalLine, title: "Egress proxies", body: "Send a service or a single key through an HTTP, HTTPS or SOCKS5 proxy, for example to call from a specific region." },
      { icon: RiTeamLine, title: "Roles", body: "Members read, admins manage services, keys and apps, superadmins also see hidden keys, users and the audit log." },
    ],
  },
  {
    title: "Know what happened",
    demo: true,
    intro: "Find out about a dead key from Acnologia before your users notice.",
    items: [
      { icon: RiBellLine, title: "Webhooks", body: "Slack, Discord, Telegram or signed JSON when a key goes bad, quota drops under 10%, a pool runs dry, a provider goes down or an app hits its limit." },
      { icon: RiFileListLine, title: "Activity and audit log", body: "Every proxied request, lease and health check with its key, app, status and latency. Every dashboard change and secret reveal is audited." },
      { icon: RiNodeTree, title: "Key insights", body: "Each key shows whether it works, when it last worked, its quota and 14 days of requests and failures." },
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

# tell Acnologia how it went, and get the next key if it failed
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


/** Card surface shared by the landing's panels. */
const CARD = "rounded-3xl border border-border-button-default bg-background-primary-default shadow-xs";

function Heading({ id, eyebrow, title, children }: { id: string; eyebrow: string; title: string; children?: ReactNode }) {
  return (
    <div id={id} className="scroll-mt-24">
      <p className="text-caption-2-semibold text-status-purple-text uppercase">{eyebrow}</p>
      <h2 className="mt-2 text-title-1-semibold text-balance text-text-primary">{title}</h2>
      {children ? <div className="mt-3 max-w-2xl text-headline-regular text-pretty text-text-secondary">{children}</div> : null}
    </div>
  );
}

function FeatureCard({ f }: { f: Feature }) {
  return (
    <div className={cx(CARD, "p-5")}>
      <span className="flex size-9 items-center justify-center rounded-xl border border-border-button-default bg-background-secondary-default text-foreground-icon-secondary">
        <f.icon className="size-[18px]" aria-hidden />
      </span>
      <h4 className="mt-3.5 text-headline-semibold text-text-primary">{f.title}</h4>
      <p className="mt-1.5 text-body-regular text-text-secondary">{f.body}</p>
    </div>
  );
}

function Code({ children }: { children: ReactNode }) {
  return (
    <code className="rounded-md bg-background-tertiary-default px-1.5 py-0.5 font-mono text-body-2-regular text-text-primary [overflow-wrap:anywhere]">
      {children}
    </code>
  );
}

/** An in text link, in the accent. */
function TextLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      className="rounded-sm text-text-primary underline decoration-chart-1 underline-offset-4 outline-none transition-[text-decoration-color] duration-150 hover:decoration-current focus-visible:ring-2 focus-visible:ring-border-focus-ring"
      href={href}
    >
      {children}
    </a>
  );
}

/** The frame a Stageshow demo sits in. The demo itself is untouched. */
function DemoFrame({ children, caption, className }: { children: ReactNode; caption: string; className?: string }) {
  return (
    <figure className={cx("min-w-0", className)}>
      {children}
      <figcaption className="sr-only">{caption}</figcaption>
    </figure>
  );
}

function Section({ children }: { children: ReactNode }) {
  return <section className="border-t border-separator-border py-16">{children}</section>;
}

export function DocsPage() {
  useTitle("Docs");
  const signedIn = useAuth((s) => s.status === "authenticated");
  const theme = useTheme((s) => s.theme);

  return (
    <div className="min-h-dvh bg-background-full text-text-primary">
      <DocsHeader
        nav={
          <nav className="hidden items-center gap-1 lg:flex" aria-label="On this page">
            {SECTIONS.map((s) => (
              <a
                key={s.id}
                href={`#${s.id}`}
                className="rounded-lg px-2 py-1.5 text-body-2-regular text-text-secondary outline-none transition-colors duration-150 hover:bg-background-secondary-default hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
              >
                {s.label}
              </a>
            ))}
          </nav>
        }
      />

      <main className="mx-auto max-w-6xl px-4 pb-24 sm:px-6">
        {/* Hero */}
        <section className="grid items-center gap-x-12 gap-y-10 pt-14 pb-16 sm:pt-20 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
          <div className="min-w-0">
            <p className="text-caption-2-semibold text-status-purple-text uppercase">Self-hosted API key pool</p>
            <h1 className="mt-4 max-w-3xl text-display-4-semibold text-balance text-text-primary sm:text-display-3-semibold lg:text-display-4-semibold">
              One endpoint in front of all your API keys
            </h1>
            <p className="mt-5 max-w-2xl text-headline-regular text-pretty text-text-secondary">
              Acnologia stores many keys per provider and sends each request with one that has quota left. When the provider
              answers rate limited, out of quota or suspended, Acnologia parks that key and retries with the next one. It runs
              as a single Go binary with SQLite, on your own server.
            </p>
            <div className="mt-8 flex flex-wrap gap-3">
              <ButtonLink href="#how" trailingIcon={RiArrowRightLine}>
                See how it works
              </ButtonLink>
              <ButtonLink href="/llms-full.txt" variant="secondary">
                Read the API reference
              </ButtonLink>
            </div>
          </div>
          <DemoFrame caption="A request through Acnologia: the first key answers 429 and starts cooling down, the next key answers 200.">
            <HeroDemo theme={theme} />
          </DemoFrame>
        </section>

        {/* How it works */}
        <Section>
          <Heading id="how" eyebrow="How it works" title="Four steps from a pile of keys to one URL" />
          <ol className="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {STEPS.map((s, i) => (
              <li key={s.title} className={cx(CARD, "p-5")}>
                <span className="font-mono text-caption-1-medium text-status-purple-text">0{i + 1}</span>
                <h3 className="mt-2 text-headline-semibold text-text-primary">{s.title}</h3>
                <p className="mt-2 text-body-regular text-text-secondary">{s.body}</p>
              </li>
            ))}
          </ol>
        </Section>

        {/* Calling */}
        <Section>
          <Heading id="calling" eyebrow="Calling it" title="Proxy the request, or borrow the key">
            Proxy mode is the default. Use a lease when a provider SDK insists on holding the raw key, and report back so
            Acnologia knows whether the key still works.
          </Heading>
          <div className="mt-10 grid gap-6 lg:grid-cols-2">
            <div className="min-w-0">
              <h3 className="text-headline-semibold text-text-primary">Proxy mode</h3>
              <p className="mt-1.5 text-body-regular text-text-secondary">
                Call <Code>/proxy/{"{service}"}/...</Code> exactly as you would call the provider. Your app never sees a
                secret. Responses add <Code>X-Cogito-Key-Id</Code> and <Code>X-Cogito-Attempts</Code> so you can trace
                which key answered.
              </p>
              <div className="mt-4">
                <CodeBlock label="proxy" code={PROXY_EXAMPLE} />
              </div>
            </div>
            <div className="min-w-0">
              <h3 className="text-headline-semibold text-text-primary">Lease mode</h3>
              <p className="mt-1.5 text-body-regular text-text-secondary">
                Ask for a key, call the provider yourself, then report the outcome. With <Code>rotate: true</Code> the
                report hands you the next key in the same response.
              </p>
              <div className="mt-4">
                <CodeBlock label="lease" code={LEASE_EXAMPLE} />
              </div>
            </div>
          </div>
        </Section>

        {/* Failures */}
        <Section>
          <Heading id="failures" eyebrow="When a key fails" title="Six outcomes, each with its own recovery">
            Every provider error is sorted into one of these by the provider's rules. A key that hit an hourly cap is
            back in an hour. A provider outage is never blamed on your keys.
          </Heading>
          <DemoFrame
            className="mt-10"
            caption="One key starts cooling down and another runs out of quota while traffic keeps flowing on the rest; both come back on their own and the pool is whole again."
          >
            <RecoveryDemo theme={theme} />
          </DemoFrame>
          <div className={cx(CARD, "mt-8 overflow-hidden")}>
            {/* Wide screens: BoardUI's table. Phones: the same rows stacked. */}
            <table className="bui-table hidden sm:table">
              <thead>
                <tr>
                  <th scope="col" className="ps-5!">Outcome</th>
                  <th scope="col">When</th>
                  <th scope="col" className="pe-5!">What Acnologia does</th>
                </tr>
              </thead>
              <tbody>
                {OUTCOMES.map((o) => (
                  <tr key={o.name}>
                    <td className="ps-5! align-top! font-mono text-body-2-medium!">{o.name}</td>
                    <td className="align-top! text-body-regular! text-text-secondary!">{o.when}</td>
                    <td className="pe-5! align-top! text-body-regular!">{o.effect}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <ul className="divide-y divide-separator-border sm:hidden">
              {OUTCOMES.map((o) => (
                <li key={o.name} className="px-4 py-3">
                  <div className="font-mono text-body-2-medium text-text-primary">{o.name}</div>
                  <div className="mt-0.5 text-body-2-regular text-text-secondary">{o.when}</div>
                  <div className="mt-1 text-body-regular text-text-primary">{o.effect}</div>
                </li>
              ))}
            </ul>
          </div>
        </Section>

        {/* Features */}
        <Section>
          <Heading id="features" eyebrow="Features" title="Everything Acnologia does" />
          <div className="mt-10 flex flex-col gap-14">
            {FEATURE_GROUPS.map((g) => (
              <div key={g.title}>
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <h3 className="text-title-3-semibold text-text-primary">{g.title}</h3>
                  <p className="text-body-regular text-text-tertiary">{g.intro}</p>
                </div>
                {g.demo ? (
                  // The alerts group pairs its cards with the demo of an alert landing.
                  <div className="mt-5 grid items-start gap-6 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
                    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-1">
                      {g.items.map((f) => (
                        <FeatureCard key={f.title} f={f} />
                      ))}
                    </div>
                    <DemoFrame
                      className="lg:sticky lg:top-20"
                      caption="A key runs out of quota and the alert lands in a Slack channel a moment later, then a second alert when it works again."
                    >
                      <AlertsDemo theme={theme} />
                    </DemoFrame>
                  </div>
                ) : (
                  <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                    {g.items.map((f) => (
                      <FeatureCard key={f.title} f={f} />
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
        </Section>

        {/* Providers */}
        <Section>
          <Heading id="providers" eyebrow="Providers" title="Twelve built in, and a builder for the rest">
            A provider is a JSON spec: where the key goes, which error bodies mean rate limited or out of quota, how to
            read remaining quota, and fixture tests that prove the rules. Publishing a spec takes effect without a
            restart, and every version can be rolled back.
          </Heading>
          <ul className="mt-8 flex flex-wrap gap-2" aria-label="Built in providers">
            {PROVIDERS.map(([name, cat]) => (
              <li
                key={name}
                className="inline-flex items-center gap-2 rounded-full border border-border-button-default bg-background-primary-default px-3 py-1.5 text-body-2-regular"
              >
                <span className="text-text-primary">{name}</span>
                <span className="text-text-tertiary">{cat}</span>
              </li>
            ))}
          </ul>
          <div className={cx(CARD, "mt-8 flex max-w-3xl gap-4 p-5")}>
            <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-status-purple-background text-status-purple-text">
              <RiRobot2Line className="size-5" aria-hidden />
            </span>
            <div className="text-body-regular text-text-secondary">
              <p className="text-headline-semibold text-text-primary">Build a provider with AI</p>
              <p className="mt-1.5">
                Give the builder a name and a docs URL. It reads the official docs, writes the rules, health check and
                tests, runs the tests through the real engine, finds the logo and publishes. A failing test blocks the
                publish. Turn it on by setting <Code>ANTHROPIC_API_KEY</Code>.
              </p>
            </div>
          </div>
        </Section>

        {/* Integrate */}
        <Section>
          <Heading id="integrate" eyebrow="Integrate" title="SDKs, MCP and plain HTTP">
            Any HTTP client works. The SDKs add lease helpers that always report, and a distinct error when every key is
            busy so you know to retry shortly.
          </Heading>
          <div className="mt-10 grid gap-6 lg:grid-cols-2">
            <div className="min-w-0">
              <h3 className="text-headline-semibold text-text-primary">SDKs</h3>
              <p className="mt-1.5 text-body-regular text-text-secondary">Python, TypeScript and JavaScript, Go, PHP and Ruby.</p>
              <div className="mt-4">
                <CodeBlock label="python" code={SDK_EXAMPLE} />
              </div>
            </div>
            <div className="min-w-0">
              <h3 className="text-headline-semibold text-text-primary">MCP for agents</h3>
              <p className="mt-1.5 text-body-regular text-text-secondary">
                <Code>POST /mcp</Code> exposes <Code>list_services</Code>, <Code>pool_status</Code>, <Code>get_key</Code>,{" "}
                <Code>report_key</Code> and <Code>proxy_request</Code>. Agents can also read <Code>/llms.txt</Code>.
              </p>
              <div className="mt-4">
                <CodeBlock label="claude code" code={MCP_EXAMPLE} />
              </div>
            </div>
          </div>
          <div className={cx(CARD, "mt-8 p-5")}>
            <p className="text-headline-semibold text-text-primary">Status codes your app should handle</p>
            <ul className="mt-3 grid gap-2 text-body-regular text-text-secondary sm:grid-cols-2">
              <li>
                <Code>503 no_key_available</Code> every key is cooling or out of quota; wait <Code>retry_after_sec</Code>.
              </li>
              <li>
                <Code>503 all_keys_busy</Code> keys exist but all are at their concurrency limit; retry in a second.
              </li>
              <li>
                <Code>503 service_unavailable</Code> the provider keeps failing and Acnologia paused it.
              </li>
              <li>
                <Code>429 app_limit_reached</Code> your app used its daily or monthly limit; see <Code>reset_at</Code>.
              </li>
            </ul>
          </div>
        </Section>

        {/* Operate */}
        <Section>
          <Heading id="operate" eyebrow="Operate" title="Run it from the terminal too">
            Create an API token under Account, then script Acnologia from CI or your laptop. The same binary that runs the
            server is the client.
          </Heading>
          <div className="mt-10 grid gap-6 lg:grid-cols-2">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <RiTerminalBoxLine className="size-4 text-foreground-icon-secondary" aria-hidden />
                <h3 className="text-headline-semibold text-text-primary">cogito ctl</h3>
              </div>
              <p className="mt-1.5 text-body-regular text-text-secondary">
                Tokens are scoped read, write or admin, never above your own role.
              </p>
              <div className="mt-4">
                <CodeBlock label="terminal" code={CTL_EXAMPLE} />
              </div>
            </div>
            <div className="min-w-0">
              <h3 className="text-headline-semibold text-text-primary">Self-host in a few minutes</h3>
              <p className="mt-1.5 text-body-regular text-text-secondary">
                Back up the data volume and the master key together. Without the master key, stored keys cannot be
                decrypted.
              </p>
              <div className="mt-4">
                <CodeBlock label="docker" code={SELF_HOST} />
              </div>
            </div>
          </div>
        </Section>

        {/* Security */}
        <Section>
          <Heading id="security" eyebrow="Security" title="Built for keys you cannot afford to leak" />
          <ul className="mt-8 grid gap-x-8 gap-y-4 text-body-regular text-text-secondary sm:grid-cols-2">
            {[
              "Keys are encrypted at rest with AES-256-GCM. App and API tokens are stored as hashes and shown once.",
              "Proxied responses have the key scrubbed from headers, run under a sandbox CSP, and redirects are passed back instead of followed with a key attached.",
              "Logo fetches, AI live checks and webhooks cannot reach private networks.",
              "Responses made with hidden or restricted keys, or that echo a key, are never cached for other apps.",
              "Every dashboard change and every secret reveal goes to the audit log.",
              "Search engines and AI crawlers are told to stay out, and pages cannot be framed.",
            ].map((t) => (
              <li key={t} className="flex gap-3">
                <RiShieldCheckLine className="mt-0.5 size-4 shrink-0 text-status-purple-text" aria-hidden />
                <span>{t}</span>
              </li>
            ))}
          </ul>
        </Section>

        {/* Guides */}
        <Section>
          <Heading id="guides" eyebrow="Guides" title="Read the rest of the docs">
            Each guide is also plain Markdown at <Code>/docs/{"{page}"}.md</Code>, listed in{" "}
            <TextLink href="/llms.txt">/llms.txt</TextLink> for agents.
          </Heading>
          <ul className="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {DOCS.map((d) => (
              <li key={d.slug} className="min-w-0">
                <Link
                  to={`/docs/${d.slug}`}
                  className={cx(
                    CARD,
                    "group button-press-motion flex h-full flex-col p-5 outline-none transition-[border-color,background-color] duration-150 ease-out",
                    "hover:border-border-button-hover hover:bg-background-primary-hover focus-visible:ring-2 focus-visible:ring-border-focus-ring",
                  )}
                >
                  <d.icon
                    className="size-[18px] text-foreground-icon-secondary transition-colors duration-150 group-hover:text-status-purple-text"
                    aria-hidden
                  />
                  <span className="mt-3 text-headline-semibold text-text-primary">{d.title}</span>
                  <span className="mt-1.5 text-body-regular text-text-secondary">{d.blurb}</span>
                </Link>
              </li>
            ))}
          </ul>
        </Section>

        {/* Final CTA */}
        <section className={cx(CARD, "px-6 py-10 text-center sm:px-10")}>
          <h2 className="text-title-1-semibold text-balance text-text-primary">Add your first service</h2>
          <p className="mx-auto mt-3 max-w-xl text-headline-regular text-pretty text-text-secondary">
            Pick a provider, paste your keys and point one app at Acnologia. The full API reference lives at{" "}
            <TextLink href="/llms-full.txt">/llms-full.txt</TextLink>.
          </p>
          <div className="mt-6 flex justify-center">
            <RouterButton to="/" trailingIcon={RiArrowRightLine}>
              {signedIn ? "Open the catalog" : "Sign in to Acnologia"}
            </RouterButton>
          </div>
        </section>
      </main>
    </div>
  );
}
