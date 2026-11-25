import { useEffect, useState, type ReactNode } from "react";
import { RiAddLine, RiArrowRightLine, RiCheckLine, RiCloseLine } from "@remixicon/react";
import { api } from "../lib/api";
import type { Checklist, Service } from "../lib/types";
import { Button } from "@/components/base/buttons/button";
import { CodeBlock } from "@/components/acn/code-block";
import { RouterButtonLink } from "@/components/acn/button-link";
import { cx } from "@/utils/cx";
import { CARD } from "./service/parts";
import { DemoData } from "./DemoData";

/** Set once the person closes the card; it then stays closed in this browser. */
const DISMISS_KEY = "acn.getting-started.dismissed";

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
 * app token, connect an agent, make a call. Which steps are done comes from
 * GET /api/admin/checklist, the same answer the control MCP's checklist tool
 * gives, so the dashboard and agents agree. The card hides itself once every
 * step is done, and stays hidden in this browser once dismissed.
 */
export function GettingStarted({ services, onAddService }: { services: Service[]; onAddService: () => void }) {
  const [dismissed, setDismissed] = useState(readDismissed);
  const [checklist, setChecklist] = useState<Checklist | null>(null);
  // A step the person opened ahead of the next one to do.
  const [picked, setPicked] = useState<number | null>(null);
  // Ask again when a service or its keys come or go, so the first step ticks at once.
  const servicesKey = services.map((s) => `${s.id}:${s.key_counts.total}`).join(",");

  useEffect(() => {
    if (dismissed) return;
    let live = true;
    api
      .checklist()
      .then((c) => live && setChecklist(c))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [dismissed, servicesKey]);

  if (dismissed || !checklist) return null;

  const origin = window.location.origin;
  const target = services.find((s) => s.key_counts.total > 0);
  const isDone = (id: string) => checklist.steps.find((s) => s.id === id)?.done ?? false;

  const steps: Step[] = [
    {
      title: "Add your first service and key",
      body: "Pick a provider and paste in a key from an account you own. Add more keys later and Acnologia rotates between them.",
      done: isDone("service"),
      action: (
        <Button variant="primary" size="small" leadingIcon={RiAddLine} onClick={onAddService}>
          Add service
        </Button>
      ),
    },
    {
      title: "Create an app token",
      body: "Each agent or app gets its own token. It can call your services but never sees a key, and you can revoke it any time.",
      done: isDone("app_token"),
      action: (
        <RouterButtonLink href="/apps" variant="secondary" size="small" trailingIcon={RiArrowRightLine}>
          Open Apps
        </RouterButtonLink>
      ),
    },
    {
      title: "Connect your agent",
      body: "Install the acn CLI, sign in, and add Acnologia to Claude Code, Cursor and Codex in one step. Or paste the app token into your agent yourself.",
      done: isDone("agent"),
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
      done: isDone("first_call"),
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
