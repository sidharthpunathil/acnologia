// The public home page. It renders outside the access gate and the sign in
// check (PublicRoutes), so it may only call /api/gate, /api/public/github,
// and /api/auth/state once the gate is open. Copy comes from the README; keep
// the two in step. The look is the landing's own: every class and token lives
// in landing.css under .hm-landing, apart from the dashboard's BoardUI tokens.
import { useEffect, useRef, type ComponentType, type ReactNode } from "react";
import { Link } from "react-aria-components";
import {
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
  RiStarLine,
  RiTimerLine,
  RiUserLine,
} from "@remixicon/react";
import { CodeBlock } from "@/components/acn/code-block";
import { BRAND_NAME, Mark } from "@/components/acn/brand";
import { ThemePicker } from "@/components/acn/theme-picker";
import { useAuth } from "@/stores/auth";
import { useGate } from "@/stores/gate";
import { cx } from "@/utils/cx";
import { CipherField } from "./CipherField";
import { formatStars, useGithubStars } from "./github-stars";
import appsDark from "./screens/apps-dark.webp";
import appsLight from "./screens/apps-light.webp";
import serviceDark from "./screens/service-dark.webp";
import serviceLight from "./screens/service-light.webp";
import shareDark from "./screens/share-dark.webp";
import shareLight from "./screens/share-light.webp";
import "./landing.css";

export const REPO_URL = "https://github.com/sidharthpunathil/acnologia";
const REPO_DOCS_URL = `${REPO_URL}#documentation`;
const RELEASES_URL = `${REPO_URL}/releases`;
export const LANDING_TITLE = "Acnologia: share API keys without giving them away";

// The product video, served from web/public/video (a copy of the repo's
// video/acnologia-web.mp4 and its poster).
const VIDEO_SRC = "/video/acnologia.mp4";
const VIDEO_POSTER = "/video/poster.jpg";

type Icon = ComponentType<{
  className?: string;
  "aria-hidden"?: boolean | "true" | "false";
}>;

const UNIX_SETUP = "curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/setup.sh | sh";
const WIN_SETUP = "irm https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/setup.ps1 | iex";
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
        Its own <code>acn_</code> token for the proxy
      </>
    ),
    sees: "Nothing but responses",
  },
  {
    icon: RiRobot2Line,
    who: "An AI agent",
    gets: (
      <>
        The same token over MCP at <code>/mcp</code>, or plain HTTP
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
  {
    icon: RiHandCoinLine,
    name: "Lease",
    body: "An app borrows a key for a short time.",
  },
  {
    icon: RiServerLine,
    name: "Handover",
    body: "Keys sealed to another server, deleted there when the share ends.",
  },
  { icon: RiEyeLine, name: "Reveal", body: "A person sees the keys once." },
];

const CONTROLS: { icon: Icon; title: string; body: string }[] = [
  {
    icon: RiKey2Line,
    title: "Pick the keys",
    body: "All of a service's keys, up to N of them, or a hand picked list.",
  },
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
  {
    icon: RiPauseCircleLine,
    title: "Pause, resume, revoke",
    body: "Each takes effect on the next request.",
  },
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
  {
    title: "Add a service",
    body: "Pick one from the built in catalog, or describe any HTTP API that takes a key in a header or a query parameter.",
  },
  {
    title: "Add keys",
    body: "Paste them one per line, as label,secret, CSV, JSON or a .env file.",
  },
  {
    title: "Share them",
    body: (
      <>
        Create an app and copy its <code>acn_</code> token (shown once), or open Sharing to send a link to a person or an invite to another
        server.
      </>
    ),
  },
  {
    title: "Call through the proxy",
    body: (
      <>
        Send requests to <code>/proxy/&lt;service&gt;/...</code> with that token.
      </>
    ),
  },
];

/** Opens in a new tab and says so to screen readers. */
function NewTab() {
  return <span className="lp-sr"> (opens in a new tab)</span>;
}

function GithubKey({ primary = true }: { primary?: boolean }) {
  return (
    <a href={REPO_URL} target="_blank" rel="noopener noreferrer" className={cx("lp-key", primary && "lp-key--primary")}>
      <RiGithubLine aria-hidden />
      View on GitHub
      <NewTab />
    </a>
  );
}


/** In app docs when this browser can read them; the README's list on a locked deploy. */
function DocsLink({ className, children }: { className: string; children: ReactNode }) {
  const locked = useGate((s) => s.status === "locked");
  if (locked) {
    return (
      <a href={REPO_DOCS_URL} target="_blank" rel="noopener noreferrer" className={className}>
        {children}
        <NewTab />
      </a>
    );
  }
  return (
    <a href="/docs" className={className}>
      {children}
    </a>
  );
}

/** A dashboard capture in the theme the visitor is using. */
function Shot({
  light,
  dark,
  width,
  height,
  alt,
  slot,
}: {
  light: string;
  dark: string;
  width: number;
  height: number;
  alt: string;
  slot: string;
}) {
  return (
    <div className="lp-shot" data-slot={slot} data-kind="product" data-alt={alt}>
      {/* On a phone the capture keeps a readable size and pans inside its frame. */}
      <div className="lp-shot__pan" tabIndex={0} role="region" aria-label={`${alt} Scroll sideways to see all of it.`}>
        <img src={light} data-theme-shot="light" width={width} height={height} alt={alt} loading="lazy" decoding="async" />
        <img src={dark} data-theme-shot="dark" width={width} height={height} alt={alt} loading="lazy" decoding="async" />
      </div>
    </div>
  );
}

/**
 * The 48 second product video. Nothing but the poster loads until the
 * visitor scrolls near it or presses play: preload is none, and the muted
 * autoplay starts only once it is in view, never under reduced motion.
 */
function ProductVideo() {
  const ref = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    const video = ref.current;
    if (!video || typeof IntersectionObserver === "undefined") return;
    const still = window.matchMedia("(prefers-reduced-motion: reduce)");
    let started = false;
    const seen = new IntersectionObserver(
      ([entry]) => {
        if (!entry) return;
        if (entry.isIntersecting && !started && !still.matches) {
          started = true;
          video.play().catch(() => {
            // Autoplay refused: the poster and the controls stay.
          });
        } else if (!entry.isIntersecting && !video.paused) {
          video.pause();
        }
      },
      { threshold: 0.5 },
    );
    seen.observe(video);
    return () => seen.disconnect();
  }, []);
  return (
    <figure className="lp-video">
      <video
        ref={ref}
        src={VIDEO_SRC}
        poster={VIDEO_POSTER}
        width={1280}
        height={720}
        controls
        muted
        playsInline
        preload="none"
        aria-describedby="video-caption"
      />
      <figcaption id="video-caption" className="lp-caption">
        Acnologia in 48 seconds, with captions.
      </figcaption>
    </figure>
  );
}

/** The signature: a request leaves without a key and gets one on the way out. */
function RequestPath() {
  return (
    <figure className="lp-path-figure">
      <div className="lp-path">
        <ol aria-hidden="true">
          <li className="lp-stop">
            <span className="lp-stop__icon">
              <RiCodeSSlashLine />
            </span>
            <span>
              <span className="lp-stop__name">Your app, agent or teammate</span>
              <br />
              <span className="lp-stop__note">Calls with its own token</span>
            </span>
            <span className="lp-chip">acn_ token</span>
          </li>
          <li className="lp-wire">request, no key</li>
          <li className="lp-stop lp-stop--held">
            <span className="lp-stop__icon">
              <Mark size={40} />
            </span>
            <span>
              <span className="lp-stop__name">{BRAND_NAME}</span>
              <br />
              <span className="lp-stop__note">Attaches the key on the way out</span>
            </span>
            <span className="lp-status">
              <RiLock2Line />
              Key stays here
            </span>
          </li>
          <li className="lp-wire">request + key</li>
          <li className="lp-stop">
            <span className="lp-stop__icon">
              <RiGlobalLine />
            </span>
            <span>
              <span className="lp-stop__name">The API</span>
              <br />
              <span className="lp-stop__note">Answers as usual</span>
            </span>
          </li>
        </ol>
        <p className="lp-path__foot">Revoke a share in one click without touching the key itself. The next request stops.</p>
      </div>
      <figcaption className="lp-caption lp-path__caption">
        You add your keys once. Every app, script, AI agent, teammate or other server gets its own token and calls the API through
        Acnologia, which attaches the key on the way out.
      </figcaption>
    </figure>
  );
}

export function LandingPage() {
  const loadGate = useGate((s) => s.load);
  const gate = useGate((s) => s.status);
  const loadAuth = useAuth((s) => s.load);
  const signedIn = useAuth((st) => st.status === "authenticated");
  const stars = useGithubStars();
  const starText = stars === null ? null : formatStars(stars);

  useEffect(() => {
    document.title = LANDING_TITLE;
    void loadGate();
  }, [loadGate]);

  // Behind a locked gate the session check is refused anyway; past it, ask,
  // so a signed in visitor also gets an "Open dashboard" button. Visitors are
  // not offered a sign in here: /login still works for those who need it.
  useEffect(() => {
    if (gate === "open") void loadAuth();
  }, [gate, loadAuth]);

  return (
    <div className="hm-landing">
      <a href="#main" className="skip-link">
        Skip to content
      </a>

      <header className="lp-nav">
        <div className="lp-nav__pill">
          <span className="lp-wordmark">
            <Mark size={28} />
            {BRAND_NAME}
          </span>
          <nav aria-label="Site" className="lp-nav__links">
            <span className="lp-nav__docs">
              <DocsLink className="lp-navlink">Docs</DocsLink>
            </span>
            <a
              href={REPO_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="lp-key lp-key--small lp-key--primary lp-star"
              aria-label={
                starText
                  ? `Star Acnologia on GitHub, ${starText.long} stars (opens GitHub in a new tab)`
                  : "Star Acnologia on GitHub (opens GitHub in a new tab)"
              }
            >
              <RiStarLine aria-hidden />
              <span className="lp-star__label">Star on GitHub</span>
              <span className="lp-star__short" aria-hidden>
                Star
              </span>
              {starText ? <span className="lp-stars">{starText.short}</span> : null}
            </a>
            {signedIn ? (
              <Link href="/" className="lp-key lp-key--small">
                <RiLoginBoxLine aria-hidden />
                Open dashboard
              </Link>
            ) : null}
          </nav>
        </div>
      </header>

      <main id="main">
        <section aria-labelledby="hero-title" className="lp-hero">
          <CipherField className="lp-field" />
          <div className="lp-container lp-hero__grid">
            <div className="lp-hero__text">
              <h1 id="hero-title">Share your API keys without giving them away.</h1>
              <p className="lp-lead">A self-hosted key server that gives each caller its own token.</p>
              <div className="lp-actions">
                <GithubKey />
                <DocsLink className="lp-key">
                  <RiBookOpenLine aria-hidden />
                  Read the docs
                </DocsLink>
              </div>
            </div>
            <RequestPath />
          </div>
        </section>

        {/* The problem, in the README's words */}
        <section aria-labelledby="why-title" className="lp-section">
          <div className="lp-container">
            <div className="lp-statement">
              <h2 id="why-title">Once a key is out, it stays out</h2>
              <div className="lp-statement__body">
                <p>
                  API keys get passed around in <code>.env</code> files, chat messages and CI settings. Once a key is out, you cannot take
                  it back, you cannot see who is using it, and the only way to stop them is to rotate it for everyone.
                </p>
                <p>
                  With Acnologia you decide which keys each one may use, how much, and for how long, and you can revoke any of them in one
                  click without touching the key itself.
                </p>
              </div>
            </div>
          </div>
        </section>

        <section aria-label="Acnologia in use" className="lp-section">
          <div className="lp-container">
            <ProductVideo />
          </div>
        </section>

        {/* Who you can share with */}
        <section aria-labelledby="who-title" className="lp-band lp-band--who">
          <div className="lp-container">
            <div className="lp-head lp-head--split">
              <h2 id="who-title">Who you can share with</h2>
              <p className="lp-lead">Each one gets its own token and calls through your server.</p>
            </div>
            <table className="lp-table">
              <colgroup>
                <col className="lp-col-who" />
                <col className="lp-col-gets" />
                <col />
              </colgroup>
              <thead>
                <tr>
                  <th scope="col">Who</th>
                  <th scope="col">Gets</th>
                  <th scope="col">Sees of your keys</th>
                </tr>
              </thead>
              <tbody>
                {AUDIENCES.map((a) => (
                  <tr key={a.who}>
                    <th scope="row">
                      <span className="lp-who">
                        <a.icon aria-hidden />
                        {a.who}
                      </span>
                    </th>
                    <td data-label="Gets">{a.gets}</td>
                    <td data-label="Sees of your keys" className="lp-sees">
                      {a.sees}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        {/* Proxy, and the three modes that hand a key over */}
        <section aria-labelledby="raw-title" className="lp-section lp-section--l">
          <div className="lp-container lp-split">
            <div className="lp-split__text">
              <h2 id="raw-title">When the other side needs the raw key</h2>
              <p className="lp-body">
                In proxy mode the key never leaves your server, so revoking a share stops the next request. Three explicit modes cover the
                cases where that is not enough. Acnologia marks every key that left this way, and when such a share ends it lists exactly
                which keys to rotate.
              </p>
              <dl className="lp-defs">
                {RAW_MODES.map((m) => (
                  <div key={m.name}>
                    <dt>
                      <m.icon aria-hidden />
                      {m.name}
                    </dt>
                    <dd>{m.body}</dd>
                  </div>
                ))}
              </dl>
            </div>
            <div className="lp-split__visual">
              <Shot
                light={shareLight}
                dark={shareDark}
                width={1200}
                height={1400}
                slot="share-dialog"
                alt="The New share dialog: share with another server, one person or anyone with a link, then proxy through this server or hand over the keys."
              />
            </div>
          </div>
        </section>

        {/* Control every share */}
        <section aria-labelledby="control-title" className="lp-section">
          <div className="lp-container">
            <div className="lp-head lp-head--split">
              <h2 id="control-title">Control every share</h2>
              <p className="lp-lead">Every change takes effect on the next request, and every one lands in the audit log.</p>
            </div>
            <Shot
              light={appsLight}
              dark={appsDark}
              width={1600}
              height={687}
              slot="apps"
              alt="The Apps page: three apps, each with its own token, the services it may use, its daily request limit and its monthly budget."
            />
            <ul className="lp-rows">
              {CONTROLS.map((c) => (
                <li key={c.title}>
                  <h3>
                    <c.icon aria-hidden />
                    {c.title}
                  </h3>
                  <p>{c.body}</p>
                </li>
              ))}
            </ul>
          </div>
        </section>

        {/* Keys that keep working */}
        <section aria-labelledby="health-title" className="lp-section lp-section--l">
          <div className="lp-container lp-split lp-split--mirror">
            <div className="lp-split__visual">
              <Shot
                light={serviceLight}
                dark={serviceDark}
                width={1600}
                height={1104}
                slot="service"
                alt="A service page: how many keys are available, busy, cooling, out of quota or invalid, and the keys with their health."
              />
            </div>
            <div className="lp-split__text">
              <h2 id="health-title">Keys that keep working</h2>
              <p className="lp-body">Sharing a pool of keys only helps if the pool stays healthy, so Acnologia looks after it.</p>
              <ul className="lp-defs">
                {HEALTH.map((h) => (
                  <li key={h.title}>
                    <h3>
                      <h.icon aria-hidden />
                      {h.title}
                    </h3>
                    <p>{h.body}</p>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </section>

        {/* Run your own */}
        <section aria-labelledby="run-title" className="lp-band">
          <div className="lp-container">
            <div className="lp-head lp-head--split">
              <h2 id="run-title">Run your own</h2>
              <p className="lp-body">
                One Go binary with the dashboard built in, for Linux, macOS and Windows. Run it in a container or build it from source with
                Go 1.26, Node 22 and pnpm 10. SQLite storage, keys encrypted at rest with <span className="lp-nowrap">AES-256-GCM</span>.
              </p>
            </div>
            <div className="lp-platforms">
              <Platform
                title="macOS or Linux"
                setup={UNIX_SETUP}
                clone={UNIX_CLONE}
                container={UNIX_CONTAINER}
                source={UNIX_SOURCE}
                note="One command installs and starts it. Or clone the repository and run it in a container or build it with make."
              />
              <Platform
                title="Windows, in PowerShell"
                setup={WIN_SETUP}
                clone={WIN_CLONE}
                container={WIN_CONTAINER}
                source={WIN_SOURCE}
                note="One command installs and starts it. Or clone the repository and run it in Linux containers or build it without make."
              />
            </div>

            <div className="lp-then">
              <div className="lp-then__text">
                <h3>Then open the dashboard</h3>
                <p>
                  Open the address the server prints when it starts (the installer prints it too). The first visit creates the
                  superadmin.
                </p>
                <p>
                  Release builds for Linux, macOS and Windows (amd64 and arm64) are on the{" "}
                  <a href={RELEASES_URL} target="_blank" rel="noopener noreferrer" className="lp-inline-link">
                    releases page
                    <NewTab />
                  </a>
                  .
                </p>
                <div className="lp-close">
                  <GithubKey />
                  <DocsLink className="lp-link">
                    <RiBookOpenLine aria-hidden />
                    Read the docs
                  </DocsLink>
                </div>
              </div>
              <ol className="lp-steps">
                {STEPS.map((s) => (
                  <li key={s.title}>
                    <h4>{s.title}</h4>
                    <p>{s.body}</p>
                  </li>
                ))}
              </ol>
            </div>
          </div>
        </section>
      </main>

      <footer className="lp-footer">
        <div className="lp-container lp-footer__grid">
          <div className="lp-footer__sign">
            <span className="lp-wordmark">
              <Mark size={28} />
              {BRAND_NAME}
            </span>
            <p className="lp-footer__line">Self-hosted. Open source under the MIT license.</p>
          </div>
          <nav aria-label="Footer" className="lp-footer__links">
            <a href={REPO_URL} target="_blank" rel="noopener noreferrer" className="lp-link">
              <RiGithubLine aria-hidden />
              Source on GitHub
              <NewTab />
            </a>
            <DocsLink className="lp-link">Docs</DocsLink>
          </nav>
          <div className="lp-footer__theme">
            <ThemePicker />
          </div>
        </div>
      </footer>
    </div>
  );
}

function Platform({
  title,
  setup,
  clone,
  container,
  source,
  note,
}: {
  title: string;
  setup: string;
  clone: string;
  container: string;
  source: string;
  note?: string;
}) {
  return (
    <div className="lp-platform">
      <h3>{title}</h3>
      {note ? <p>{note}</p> : null}
      <CodeBlock code={setup} label="install and run" roomyCopy className="lp-code" />
      <CodeBlock code={clone} label="or get the code" roomyCopy className="lp-code" />
      <CodeBlock code={container} label="run in a container" roomyCopy className="lp-code" />
      <CodeBlock code={source} label="or build from source" roomyCopy className="lp-code" />
    </div>
  );
}
