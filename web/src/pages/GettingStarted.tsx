import { useEffect, useState, type ReactNode } from "react";
import { RiAddLine, RiArrowRightLine, RiCheckLine, RiCloseLine } from "@remixicon/react";
import { ops } from "../lib/ops";
import { fetchUsage } from "../lib/usage";
import type { Service } from "../lib/types";
import { useApps } from "../stores/apps";
import { Button } from "@/components/base/buttons/button";
import { CodeBlock } from "@/components/acn/code-block";
import { RouterButtonLink } from "@/components/acn/button-link";
import { cx } from "@/utils/cx";
import { CARD } from "./service/parts";
import { DemoData } from "./DemoData";

/** Set once the person closes the card; it then stays closed in this browser. */
const DISMISS_KEY = "acn.getting-started.dismissed";

/** The app the control MCP makes for itself on first use. It is not one the person made. */
const CONTROL_APP = "Control MCP";

function readDismissed(): boolean {
  try {
    return window.localStorage.getItem(DISMISS_KEY) === "1";
  } catch {
    return false;
  }
}

function saveDismissed() {
  try {
    window.localStorage.setItem(DISMISS_KEY, "1");
  } catch {
    /* storage blocked: the card closes for this visit only */
  }
}

/** What the server says has happened so far, beyond the services the catalog already has. */
interface Signals {
  /** An admin API token (acn login, acn mcp) has been used. */
  cliUsed: boolean;
  /** Requests through the pool in the last 90 days. */
  requests: number;
}

async function loadSignals(): Promise<Signals> {
  const [tokens, usage] = await Promise.allSettled([ops.listTokens(), fetchUsage(90)]);
  return {
    cliUsed: tokens.status === "fulfilled" && (tokens.value.tokens ?? []).some((t) => !!t.last_used_at),
    requests: usage.status === "fulfilled" ? (usage.value.totals?.range?.requests ?? 0) : 0,
  };
}

interface Step {
  title: string;
  body: string;
  done: boolean;
  detail?: ReactNode;
  action?: ReactNode;
}

function StepMark({ n, done, current }: { n: number; done: boolean; current: boolean }) {
  return (
    <span
      className={cx(
        "flex size-7 shrink-0 items-center justify-center rounded-full font-mono text-caption-1-medium",
        done
          ? "bg-status-lime-background text-status-lime-text"
          : current
            ? "border border-accent-line bg-accent-subtle text-accent-fg"
            : "border border-border-button-default text-text-tertiary",
      )}
      aria-hidden
    >
      {done ? <RiCheckLine className="size-4" /> : n}
    </span>
  );
}

/**
 * The first run checklist at the top of the catalog: add a service, make an
 * app token, connect an agent, make a call. Each step reads the server's real
 * state. The card hides itself once every step is done, and stays hidden in
 * this browser once dismissed.
 */
export function GettingStarted({ services, onAddService }: { services: Service[]; onAddService: () => void }) {
  const [dismissed, setDismissed] = useState(readDismissed);
  const { apps, loaded: appsLoaded, fetch: fetchApps } = useApps();
  const [signals, setSignals] = useState<Signals | null>(null);
  // A step the person opened ahead of the next one to do.
  const [picked, setPicked] = useState<number | null>(null);

  useEffect(() => {
    if (dismissed) return;
    let live = true;
    fetchApps().catch(() => undefined);
    loadSignals()
      .then((s) => live && setSignals(s))
      .catch(() => live && setSignals({ cliUsed: false, requests: 0 }));
    return () => {
      live = false;
    };
  }, [dismissed, fetchApps]);

  if (dismissed || !signals || !appsLoaded) return null;

  const origin = window.location.origin;
  const withKeys = services.filter((s) => s.key_counts.total > 0);
  const ownApps = apps.filter((a) => a.name !== CONTROL_APP);
  const appUsed = ownApps.some((a) => !!a.last_used_at);
  const requests = signals.requests + services.reduce((n, s) => n + (s.requests_24h ?? 0), 0);
  const target = withKeys[0];

  const steps: Step[] = [
    {
      title: "Add your first service and key",
      body: "Pick a provider and paste in a key from an account you own. Add more keys later and Acnologia rotates between them.",
      done: withKeys.length > 0,
      action: (
        <Button variant="primary" size="small" leadingIcon={RiAddLine} onClick={onAddService}>
          Add service
        </Button>
      ),
    },
    {
      title: "Create an app token",
      body: "Each agent or app gets its own token. It can call your services but never sees a key, and you can revoke it any time.",
      done: ownApps.length > 0,
      action: (
        <RouterButtonLink href="/apps" variant="secondary" size="small" trailingIcon={RiArrowRightLine}>
          Open Apps
        </RouterButtonLink>
      ),
    },
    {
      title: "Connect your agent",
      body: "Install the acn CLI, sign in, and add Acnologia to Claude Code, Cursor and Codex in one step. Or paste the app token into your agent yourself.",
      done: signals.cliUsed || appUsed,
      detail: (
        <div className="flex flex-col gap-3">
          <CodeBlock
            label="terminal"
            code={`curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.sh | sh\nacn login\nacn mcp install`}
          />
          <CodeBlock
            label="Claude Code, with an app token"
            code={`claude mcp add --transport http acnologia ${origin}/mcp --header "Authorization: Bearer acn_..."`}
          />
        </div>
      ),
      action: (
        <RouterButtonLink href="/docs/cli#set-up-your-agents-in-one-step" variant="ghost" size="small">
          CLI guide
        </RouterButtonLink>
      ),
    },
    {
      title: "Make a first call",
      body: "Ask your agent to use a service, or call one through the proxy. The service's Integrate tab has snippets for every language.",
      done: requests > 0,
      detail: (
        <CodeBlock label="curl" code={`curl "${origin}/proxy/${target?.slug ?? "<service>"}/<path>" \\\n  -H "Authorization: Bearer acn_..."`} />
      ),
      action: target ? (
        <RouterButtonLink href={`/services/${target.id}`} variant="secondary" size="small" trailingIcon={RiArrowRightLine}>
          Open {target.name}
        </RouterButtonLink>
      ) : undefined,
    },
  ];

  const done = steps.filter((s) => s.done).length;
  if (done === steps.length) return null;
  const next = steps.findIndex((s) => !s.done);
  const current = picked !== null && !steps[picked].done ? picked : next;

  const dismiss = () => {
    saveDismissed();
    setDismissed(true);
  };

  return (
    <section aria-labelledby="getting-started-title" className={cx(CARD, "fade-up mt-6 p-5 sm:p-6")}>
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <h2 id="getting-started-title" className="text-headline-semibold text-text-primary">
            Getting started
          </h2>
          <p className="mt-1 text-body-2-regular text-text-secondary">
            {done} of {steps.length} done. Four steps to a first call through your keys.
          </p>
        </div>
        <Button variant="ghost" size="small" iconOnly leadingIcon={RiCloseLine} aria-label="Dismiss getting started" onClick={dismiss} />
      </div>
      <div className="mt-4 h-1 overflow-hidden rounded-full bg-background-tertiary-default" aria-hidden>
        <div className="h-full rounded-full bg-accent-fill transition-[width] duration-300 ease-out" style={{ width: `${(done / steps.length) * 100}%` }} />
      </div>
      <ol className="mt-5 flex flex-col">
        {steps.map((s, i) => {
          const open = i === current;
          return (
            <li key={s.title} className={cx("flex gap-3.5 py-3", i > 0 && "border-t border-separator-border")}>
              <StepMark n={i + 1} done={s.done} current={open} />
              <div className="min-w-0 flex-1 pt-0.5">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                  {s.done || open ? (
                    <span className={cx("min-w-0 flex-1 text-body-medium", s.done ? "text-text-tertiary" : "text-text-primary")}>
                      {s.title}
                      <span className="sr-only">{s.done ? ", done" : ", not done yet"}</span>
                    </span>
                  ) : (
                    <button
                      type="button"
                      aria-expanded={false}
                      onClick={() => setPicked(i)}
                      className="-mx-1 min-w-0 flex-1 cursor-pointer rounded-lg px-1 text-start text-body-medium text-text-primary outline-none transition-colors duration-150 hover:text-accent-fg focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                    >
                      {s.title}
                      <span className="sr-only">, not done yet</span>
                    </button>
                  )}
                  {open && s.action}
                </div>
                {open && (
                  <div id={`getting-started-step-${i}`}>
                    <p className="mt-1 max-w-2xl text-body-2-regular text-text-secondary">{s.body}</p>
                    {s.detail && <div className="mt-3">{s.detail}</div>}
                  </div>
                )}
              </div>
            </li>
          );
        })}
      </ol>
      <DemoData services={services} />
    </section>
  );
}
