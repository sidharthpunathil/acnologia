// The public home page. It renders outside the access gate and the sign in
// check (PublicRoutes), so it may only call /api/gate, and /api/auth/state
// once the gate is open. Copy comes from the README; keep the two in step.
import { useEffect, type ComponentType, type ReactNode } from "react";
import {
  RiArrowRightLine,
  RiArrowRightUpLine,
  RiBookOpenLine,
  RiCodeSSlashLine,
  RiDatabase2Line,
  RiEyeLine,
  RiGithubLine,
  RiGlobalLine,
  RiHandCoinLine,
  RiKey2Line,
  RiLineChartLine,
  RiLock2Line,
  RiLoginBoxLine,
  RiPauseCircleLine,
  RiRefreshLine,
  RiRobot2Line,
  RiServerLine,
  RiShuffleLine,
  RiSpeedUpLine,
  RiTimerLine,
  RiUserLine,
} from "@remixicon/react";
import { ButtonLink } from "@/components/base/buttons/button";
import { RouterButtonLink } from "@/components/acn/button-link";
import { CodeBlock } from "@/components/acn/code-block";
import { BRAND_NAME, Mark } from "@/components/acn/brand";
import { ThemePicker } from "@/components/acn/theme-picker";
import { useAuth } from "@/stores/auth";
import { useGate } from "@/stores/gate";
import { cx } from "@/utils/cx";

export const REPO_URL = "https://github.com/sidharthpunathil/acnologia";
const REPO_DOCS_URL = `${REPO_URL}#documentation`;
const RELEASES_URL = `${REPO_URL}/releases`;
export const LANDING_TITLE = "Acnologia: share API keys without giving them away";

type Icon = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;

const UNIX_CLONE = "git clone https://github.com/sidharthpunathil/acnologia && cd acnologia";
const UNIX_CONTAINER = "cp .env.example .env\ndocker compose up -d --build";
const UNIX_SOURCE = "make build\nACNOLOGIA_DATA_DIR=./data ./bin/acnologia";
const WIN_CLONE = "git clone https://github.com/sidharthpunathil/acnologia; cd acnologia";
const WIN_CONTAINER = "Copy-Item .env.example .env\ndocker compose up -d --build";
const WIN_SOURCE = ".\\scripts\\build.ps1\n.\\bin\\acnologia.exe    # data goes to %LocalAppData%\\Acnologia";

const AUDIENCES: { icon: Icon; who: string; gets: ReactNode; sees: string }[] = [
  {
    icon: RiCodeSSlashLine,
    who: "An app or script",
    gets: (
      <>
        Its own <code className="font-mono">acn_</code> token for the proxy
      </>
    ),
    sees: "Nothing but responses",
  },
  {
    icon: RiRobot2Line,
    who: "An AI agent",
    gets: (
      <>
        The same token over MCP at <code className="font-mono">/mcp</code>, or plain HTTP
      </>
    ),
    sees: "Nothing but responses",
  },
  {
    icon: RiUserLine,
    who: "A person who does not run Acnologia",
    gets: "A one time link, then a personal token, ready made snippets and a My access page",
    sees: "Nothing but responses",
  },
  {
    icon: RiServerLine,
    who: "Another Acnologia server",
    gets: "An invite bound to that server's identity",
    sees: "Nothing, in proxy mode",
  },
];

const RAW_MODES: { icon: Icon; name: string; body: string }[] = [
  { icon: RiHandCoinLine, name: "Lease", body: "An app borrows a key for a short time." },
  { icon: RiServerLine, name: "Handover", body: "Keys sealed to another server, deleted there when the share ends." },
  { icon: RiEyeLine, name: "Reveal", body: "A person sees the keys once." },
];

const CONTROLS: { icon: Icon; title: string; body: string }[] = [
  { icon: RiKey2Line, title: "Pick the keys", body: "All of a service's keys, up to N of them, or a hand picked list." },
  {
    icon: RiSpeedUpLine,
    title: "Set limits",
    body: "Daily or monthly request caps and a USD budget per app or share, reserved before each call so a burst cannot overshoot them.",
  },
  {
    icon: RiTimerLine,
    title: "Set a timer",
    body: "End a share after an hour, a day, a week or at a set time, and choose whether it expires (you can extend it) or is revoked for good.",
  },
  { icon: RiPauseCircleLine, title: "Pause, resume, revoke", body: "Each takes effect on the next request." },
  {
    icon: RiLineChartLine,
    title: "See the use",
    body: "Requests, spend and last use per share, and an audit log of every change and every reveal.",
  },
];

const HEALTH: { icon: Icon; title: string; body: string }[] = [
  {
    icon: RiRefreshLine,
    title: "Keys rest and come back",
    body: "A rate limited key rests for as long as the provider asked, and a key out of quota waits for its reset. A revoked or suspended key leaves rotation once a health check confirms it. An outage at the provider opens a circuit breaker instead of blaming your keys.",
  },
  {
    icon: RiShuffleLine,
    title: "You choose how keys are picked",
    body: "Round robin, fill first, least used, most quota left or weighted random, with backup keys and per key concurrency limits.",
  },
  {
    icon: RiDatabase2Line,
    title: "Repeat calls cost less",
    body: "Identical GET requests can be answered from a response cache, and retried POSTs with an Idempotency-Key are replayed instead of sent twice.",
  },
];

const STEPS: { title: string; body: ReactNode }[] = [
  { title: "Add a service", body: "Pick one from the built in catalog, or describe any HTTP API that takes a key in a header or a query parameter." },
  { title: "Add keys", body: "Paste them one per line, as label,secret, CSV, JSON or a .env file." },
  {
    title: "Share them",
    body: (
      <>
        Create an app and copy its <code className="font-mono">acn_</code> token (shown once), or open Sharing to send a link to a person
        or an invite to another server.
      </>
    ),
  },
  {
    title: "Call through the proxy",
    body: (
      <>
        Send requests to <code className="font-mono break-all">/proxy/&lt;service&gt;/...</code> with that token.
      </>
    ),
  },
];

const focusRing = "outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring";
const tall = "h-11 px-4";

/** Opens in a new tab and says so to screen readers. */
function NewTab() {
  return <span className="sr-only"> (opens in a new tab)</span>;
}

function GithubButton({ variant = "primary", className }: { variant?: "primary" | "secondary"; className?: string }) {
  return (
    <ButtonLink
      href={REPO_URL}
      target="_blank"
      rel="noopener noreferrer"
      variant={variant}
      leadingIcon={RiGithubLine}
      trailingIcon={RiArrowRightUpLine}
      className={cx(tall, className)}
    >
      View on GitHub
      <NewTab />
    </ButtonLink>
  );
}

/**
 * Where "Sign in" goes. /login runs the existing flow: the lock screen on a
 * locked deploy, first run setup on a fresh server, the sign in page
 * otherwise. Someone already signed in goes straight to the dashboard.
 */
function useSignIn(): { href: string; label: string } {
  const signedIn = useAuth((s) => s.status === "authenticated");
  return signedIn ? { href: "/", label: "Open dashboard" } : { href: "/login", label: "Sign in" };
}

/** In app docs when this browser can read them; the README's list on a locked deploy. */
function DocsLink({ className, children }: { className?: string; children: ReactNode }) {
  const locked = useGate((s) => s.status === "locked");
  const cls = cx(
    "tap-inline inline-flex items-center gap-1 rounded-md text-body-medium text-text-primary underline decoration-border-button-hover underline-offset-4 hover:decoration-text-primary",
    focusRing,
    className,
  );
  if (locked) {
    return (
      <a href={REPO_DOCS_URL} target="_blank" rel="noopener noreferrer" className={cls}>
        {children}
        <RiArrowRightUpLine className="size-4" aria-hidden />
        <NewTab />
      </a>
    );
  }
  return (
    <a href="/docs" className={cls}>
      {children}
      <RiArrowRightLine className="size-4" aria-hidden />
    </a>
  );
}

function SectionHead({ index, id, title, children }: { index: string; id: string; title: string; children?: ReactNode }) {
  return (
    <div className="flex max-w-2xl flex-col gap-3">
      <p className="font-mono text-caption-1-medium text-accent-fg" aria-hidden>
        {index}
      </p>
      <h2 id={id} className="text-display-4-semibold text-balance text-text-primary">
        {title}
      </h2>
      {children ? <div className="text-body-regular text-pretty text-text-secondary">{children}</div> : null}
    </div>
  );
}

function IconChip({ icon: I }: { icon: Icon }) {
  return (
    <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-accent-subtle text-accent-fg">
      <I className="size-5" aria-hidden />
    </span>
  );
}

/** The one picture on the page: a request leaves without a key and gets one on the way out. */
function RequestPath() {
  return (
    <figure className="relative rounded-3xl border border-border-button-default bg-background-primary-default p-4 shadow-lift sm:p-6">
      <figcaption className="sr-only">
        A request goes from your app, agent or teammate to Acnologia with an acn_ token. Acnologia attaches the key and sends the request
        on to the API. The key stays on your server.
      </figcaption>
      <ol className="flex flex-col gap-0" aria-hidden>
        <PathStop icon={RiCodeSSlashLine} title="Your app, agent or teammate" note="Calls with its own token" chip="acn_ token" />
        <PathLink label="request, no key" />
        <li className="relative flex items-center gap-3 rounded-2xl border border-accent-line bg-accent-subtle p-3">
          <Mark size={40} className="rounded-xl" />
          <div className="min-w-0 flex-1">
            <p className="font-mono text-body-medium text-text-primary">{BRAND_NAME}</p>
            <p className="text-caption-1-regular text-text-secondary">Attaches the key on the way out</p>
          </div>
          <span className="flex shrink-0 items-center gap-1 rounded-full bg-background-primary-default px-2.5 py-1 font-mono text-caption-2-medium text-accent-fg">
            <RiLock2Line className="size-3.5" />
            key stays here
          </span>
        </li>
        <PathLink label="request + key" />
        <PathStop icon={RiGlobalLine} title="The API" note="Answers as usual" />
      </ol>
      <p className="mt-4 border-t border-separator-border pt-4 text-caption-1-regular text-text-secondary">
        Revoke a share in one click without touching the key itself. The next request stops.
      </p>
    </figure>
  );
}

function PathStop({ icon: I, title, note, chip }: { icon: Icon; title: string; note: string; chip?: string }) {
  return (
    <li className="flex items-center gap-3 rounded-2xl border border-border-button-default bg-background-secondary-default p-3">
      <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-background-tertiary-default text-foreground-icon-secondary">
        <I className="size-5" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="text-body-medium text-text-primary">{title}</p>
        <p className="text-caption-1-regular text-text-secondary">{note}</p>
      </div>
      {chip ? (
        <span className="shrink-0 rounded-full border border-border-button-default bg-background-primary-default px-2.5 py-1 font-mono text-caption-2-medium text-text-secondary">
          {chip}
        </span>
      ) : null}
    </li>
  );
}

function PathLink({ label }: { label: string }) {
  return (
    <li className="flex items-center gap-3 py-1 ps-8">
      <span className="landing-wire h-8 w-px" />
      <span className="font-mono text-caption-2-regular text-text-tertiary">{label}</span>
    </li>
  );
}

export function LandingPage() {
  const loadGate = useGate((s) => s.load);
  const gate = useGate((s) => s.status);
  const loadAuth = useAuth((s) => s.load);
  const signIn = useSignIn();

  useEffect(() => {
    document.title = LANDING_TITLE;
    void loadGate();
  }, [loadGate]);

  // Behind a locked gate the sign in state is refused anyway; past it, ask,
  // so a signed in visitor sees "Open dashboard" instead of "Sign in".
  useEffect(() => {
    if (gate === "open") void loadAuth();
  }, [gate, loadAuth]);

  return (
    <div className="landing relative flex min-h-dvh flex-col overflow-x-clip bg-background-full">
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <div className="landing-grid pointer-events-none absolute inset-x-0 top-0 h-[720px]" aria-hidden />

      <header className="relative mx-auto flex w-full max-w-6xl items-center gap-2 px-4 pt-[calc(1rem+env(safe-area-inset-top))] pb-2 sm:px-6">
        <span className="flex min-w-0 flex-1 items-center gap-2">
          <Mark size={28} />
          <span className="font-mono text-headline-medium text-text-primary">{BRAND_NAME}</span>
        </span>
        <nav aria-label="Site" className="flex items-center gap-1">
          <span className="hidden sm:inline-flex">
            <DocsLink className="px-2 no-underline hover:underline">Docs</DocsLink>
          </span>
          <ButtonLink
            href={REPO_URL}
            target="_blank"
            rel="noopener noreferrer"
            variant="ghost"
            iconOnly
            leadingIcon={RiGithubLine}
            aria-label="Acnologia on GitHub (opens in a new tab)"
            className="size-11"
          />
          <RouterButtonLink href={signIn.href} variant="secondary" leadingIcon={RiLoginBoxLine} className={tall}>
            {signIn.label}
          </RouterButtonLink>
        </nav>
      </header>

      <main id="main" className="relative flex flex-1 flex-col">
        {/* Hero */}
        <section
          aria-labelledby="hero-title"
          className="mx-auto grid w-full max-w-6xl gap-10 px-4 pt-10 pb-16 sm:px-6 sm:pt-16 lg:grid-cols-[1.1fr_1fr] lg:items-center lg:gap-14 lg:pt-24 lg:pb-24"
        >
          <div className="flex flex-col gap-6">
            <p className="fade-up inline-flex w-fit items-center gap-2 rounded-full border border-border-button-default bg-background-primary-default py-1 ps-1 pe-3 font-mono text-caption-1-medium text-text-secondary">
              <span className="rounded-full bg-accent-subtle px-2 py-0.5 text-accent-fg">self-hosted</span>
              API key server
            </p>
            <h1
              id="hero-title"
              className="fade-up text-display-3-semibold text-balance text-text-primary [animation-delay:60ms] sm:text-display-2-semibold lg:text-display-1-semibold"
            >
              Share your API keys <span className="text-accent-fg">without giving them away.</span>
            </h1>
            <p className="fade-up max-w-xl text-headline-regular text-pretty text-text-secondary [animation-delay:120ms]">
              You add your keys once. Every app, script, AI agent, teammate or other server gets its own token and calls the API through
              Acnologia, which attaches the key on the way out.
            </p>
            <div className="fade-up flex flex-col gap-3 [animation-delay:180ms] sm:flex-row">
              <GithubButton />
              <RouterButtonLink href={signIn.href} variant="secondary" leadingIcon={RiLoginBoxLine} className={tall}>
                {signIn.label}
              </RouterButtonLink>
            </div>
            <p className="fade-up font-mono text-caption-1-regular text-text-tertiary [animation-delay:240ms]">
              One Go binary with the dashboard built in. SQLite storage, keys encrypted at rest with AES-256-GCM.
            </p>
          </div>
          <div className="fade-up [animation-delay:200ms]">
            <RequestPath />
          </div>
        </section>

        {/* The problem, in the README's words */}
        <section aria-labelledby="why-title" className="border-y border-separator-border bg-background-primary-default">
          <div className="mx-auto grid w-full max-w-6xl gap-6 px-4 py-12 sm:px-6 lg:grid-cols-[1fr_2fr] lg:gap-14 lg:py-16">
            <h2 id="why-title" className="text-title-1-semibold text-text-primary">
              Once a key is out, it stays out
            </h2>
            <div className="flex flex-col gap-4 text-body-regular text-pretty text-text-secondary">
              <p>
                API keys get passed around in <code className="font-mono text-text-primary">.env</code> files, chat messages and CI
                settings. Once a key is out, you cannot take it back, you cannot see who is using it, and the only way to stop them is to
                rotate it for everyone.
              </p>
              <p>
                With Acnologia you decide which keys each one may use, how much, and for how long, and you can revoke any of them in one
                click without touching the key itself.
              </p>
            </div>
          </div>
        </section>

        {/* Who you can share with */}
        <section aria-labelledby="who-title" className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-4 py-16 sm:px-6 lg:py-24">
          <SectionHead index="01 / share" id="who-title" title="Who you can share with">
            <p>Each one gets its own token and calls through your server.</p>
          </SectionHead>
          <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {AUDIENCES.map((a) => (
              <li
                key={a.who}
                className="flex flex-col gap-4 rounded-3xl border border-border-button-default bg-background-primary-default p-5 shadow-card"
              >
                <IconChip icon={a.icon} />
                <h3 className="text-headline-semibold text-text-primary">{a.who}</h3>
                <dl className="mt-auto flex flex-col gap-3">
                  <div className="flex flex-col gap-1">
                    <dt className="font-mono text-caption-2-medium text-text-tertiary uppercase">Gets</dt>
                    <dd className="text-body-2-regular text-text-primary">{a.gets}</dd>
                  </div>
                  <div className="flex flex-col gap-1 border-t border-separator-border pt-3">
                    <dt className="font-mono text-caption-2-medium text-text-tertiary uppercase">Sees</dt>
                    <dd className="text-body-2-medium text-accent-fg">{a.sees}</dd>
                  </div>
                </dl>
              </li>
            ))}
          </ul>

          <div className="grid gap-6 rounded-3xl border border-border-button-default bg-background-secondary-default p-5 sm:p-8 lg:grid-cols-[1fr_1.4fr] lg:gap-10">
            <div className="flex flex-col gap-3">
              <h3 className="text-title-2-semibold text-text-primary">When the other side needs the raw key</h3>
              <p className="text-body-regular text-pretty text-text-secondary">
                In proxy mode the key never leaves your server, so revoking a share stops the next request. Three explicit modes cover the
                cases where that is not enough. Acnologia marks every key that left this way, and when such a share ends it lists exactly
                which keys to rotate.
              </p>
            </div>
            <ul className="grid gap-3 sm:grid-cols-3 lg:grid-cols-1">
              {RAW_MODES.map((m) => (
                <li
                  key={m.name}
                  className="flex items-start gap-3 rounded-2xl border border-border-button-default bg-background-primary-default p-4"
                >
                  <m.icon className="mt-0.5 size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />
                  <p className="text-body-2-regular text-text-secondary">
                    <span className="font-mono text-body-2-medium text-text-primary">{m.name}.</span> {m.body}
                  </p>
                </li>
              ))}
            </ul>
          </div>
        </section>

        {/* Control every share */}
        <section aria-labelledby="control-title" className="border-t border-separator-border">
          <div className="mx-auto grid w-full max-w-6xl gap-10 px-4 py-16 sm:px-6 lg:grid-cols-[1fr_1.6fr] lg:gap-14 lg:py-24">
            <div className="lg:sticky lg:top-8 lg:self-start">
              <SectionHead index="02 / control" id="control-title" title="Control every share">
                <p>Every change takes effect on the next request, and every one lands in the audit log.</p>
              </SectionHead>
            </div>
            <ul className="flex flex-col">
              {CONTROLS.map((c, i) => (
                <li key={c.title} className={cx("flex gap-4 py-5", i > 0 && "border-t border-separator-border")}>
                  <IconChip icon={c.icon} />
                  <div className="flex flex-col gap-1">
                    <h3 className="text-headline-semibold text-text-primary">{c.title}</h3>
                    <p className="text-body-regular text-pretty text-text-secondary">{c.body}</p>
                  </div>
                </li>
              ))}
            </ul>
          </div>
        </section>

        {/* Keys that keep working */}
        <section aria-labelledby="health-title" className="border-t border-separator-border bg-background-primary-default">
          <div className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-4 py-16 sm:px-6 lg:py-24">
            <SectionHead index="03 / pool" id="health-title" title="Keys that keep working">
              <p>Sharing a pool of keys only helps if the pool stays healthy, so Acnologia looks after it.</p>
            </SectionHead>
            <ul className="grid gap-4 md:grid-cols-3">
              {HEALTH.map((h) => (
                <li key={h.title} className="flex flex-col gap-3 rounded-3xl border border-border-button-default bg-background-secondary-default p-5">
                  <h3 className="flex items-center gap-2 text-headline-semibold text-text-primary">
                    <h.icon className="size-5 shrink-0 text-accent-fg" aria-hidden />
                    {h.title}
                  </h3>
                  <p className="text-body-2-regular text-pretty text-text-secondary">{h.body}</p>
                </li>
              ))}
            </ul>
          </div>
        </section>

        {/* Run your own */}
        <section aria-labelledby="run-title" className="border-t border-separator-border">
          <div className="mx-auto flex w-full max-w-6xl flex-col gap-10 px-4 py-16 sm:px-6 lg:py-24">
            <SectionHead index="04 / run" id="run-title" title="Run your own">
              <p>
                One Go binary with the dashboard built in, for Linux, macOS and Windows. Run it in a container or build it from source with
                Go 1.26, Node 22 and pnpm 10.
              </p>
            </SectionHead>
            <div className="grid min-w-0 gap-8 lg:grid-cols-2">
              <Platform
                title="macOS or Linux"
                clone={UNIX_CLONE}
                container={UNIX_CONTAINER}
                source={UNIX_SOURCE}
                note="Run it in a container, or build from source with make."
              />
              <Platform
                title="Windows, in PowerShell"
                clone={WIN_CLONE}
                container={WIN_CONTAINER}
                source={WIN_SOURCE}
                note="Run it in Linux containers, or build from source with no make needed."
              />
            </div>

            <div className="grid gap-6 rounded-3xl border border-border-button-default bg-background-primary-default p-5 sm:p-8 lg:grid-cols-[1fr_2fr] lg:gap-10">
              <div className="flex flex-col gap-3">
                <h3 className="text-title-2-semibold text-text-primary">Then open the dashboard</h3>
                <p className="text-body-regular text-text-secondary">
                  Open <code className="font-mono text-text-primary">http://localhost:8080</code>. The first visit creates the superadmin.
                </p>
                <p className="text-body-regular text-text-secondary">
                  Release builds for Linux, macOS and Windows (amd64 and arm64) are on the{" "}
                  <a
                    href={RELEASES_URL}
                    target="_blank"
                    rel="noopener noreferrer"
                    className={cx("rounded-sm text-text-primary underline underline-offset-4", focusRing)}
                  >
                    releases page
                    <NewTab />
                  </a>
                  .
                </p>
              </div>
              <ol className="grid gap-4 sm:grid-cols-2">
                {STEPS.map((s, i) => (
                  <li key={s.title} className="flex gap-3">
                    <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-accent-subtle font-mono text-caption-1-semibold text-accent-fg">
                      {i + 1}
                    </span>
                    <div className="flex min-w-0 flex-col gap-1">
                      <h4 className="text-body-semibold text-text-primary">{s.title}</h4>
                      <p className="text-body-2-regular text-pretty text-text-secondary">{s.body}</p>
                    </div>
                  </li>
                ))}
              </ol>
            </div>

            <div className="flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:gap-6">
              <GithubButton />
              <DocsLink>
                <RiBookOpenLine className="size-4" aria-hidden />
                Read the docs
              </DocsLink>
            </div>
          </div>
        </section>
      </main>

      <footer className="border-t border-separator-border bg-background-primary-default">
        <div className="mx-auto flex w-full max-w-6xl flex-col gap-6 px-4 pt-8 pb-[max(2rem,env(safe-area-inset-bottom))] sm:px-6 md:flex-row md:items-center md:justify-between">
          <div className="flex flex-col gap-1">
            <span className="flex items-center gap-2">
              <Mark size={24} />
              <span className="font-mono text-body-medium text-text-primary">{BRAND_NAME}</span>
            </span>
            <p className="font-mono text-caption-1-regular text-text-tertiary">Self-hosted. Open source under the MIT license.</p>
          </div>
          <nav aria-label="Footer" className="flex flex-wrap items-center gap-x-6 gap-y-2">
            <a
              href={REPO_URL}
              target="_blank"
              rel="noopener noreferrer"
              className={cx("tap-inline inline-flex items-center gap-1.5 rounded-md text-body-medium text-text-primary", focusRing)}
            >
              <RiGithubLine className="size-4" aria-hidden />
              Source on GitHub
              <NewTab />
            </a>
            <DocsLink className="no-underline">Docs</DocsLink>
          </nav>
          <div className="w-full max-w-xs">
            <ThemePicker />
          </div>
        </div>
      </footer>
    </div>
  );
}

function Platform({ title, clone, container, source, note }: { title: string; clone: string; container: string; source: string; note?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <h3 className="text-headline-semibold text-text-primary">{title}</h3>
      {note ? <p className="text-body-2-regular text-text-secondary">{note}</p> : null}
      <CodeBlock code={clone} label="get the code" roomyCopy />
      <CodeBlock code={container} label="run in a container" roomyCopy />
      <CodeBlock code={source} label="or build from source" roomyCopy />
    </div>
  );
}
