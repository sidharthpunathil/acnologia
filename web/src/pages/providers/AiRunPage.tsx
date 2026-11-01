import { useEffect, useRef, useState, type ComponentType, type MouseEvent, type ReactNode } from "react";
import { ShimmerText } from "@/components/application/agent-log/agent-log";
import { Link, useNavigate, useParams } from "react-router-dom";
import { motion, useReducedMotion } from "motion/react";
import {
  RiArrowDownSLine,
  RiBrainLine,
  RiCheckboxCircleLine,
  RiCloseCircleLine,
  RiDownloadLine,
  RiFlagLine,
  RiGlobalLine,
  RiInformationLine,
  RiSearchLine,
  RiSparkling2Line,
  RiStopLine,
  RiToolsLine,
} from "@remixicon/react";
import { AcnAiBuilder } from "@/components/foundations/icons/acn";
import { useTitle } from "../../hooks/useTitle";
import { FadeText, plainText } from "../../components/FadeText";
import { ApiError, errorMessage } from "../../lib/api";
import { can } from "../../lib/perms";
import { compact, dateTime } from "../../lib/format";
import { normalizeSpec } from "../../lib/providerSpec";
import { safeHref } from "../../lib/url";
import type { AgentRun, AgentRunStatus, AgentStep, AgentStepType } from "../../lib/types";
import { ACTIVE_STATUSES, useAi } from "../../stores/ai";
import { useProviders } from "../../stores/providers";
import { useServices } from "../../stores/services";
import { useAuth } from "../../stores/auth";
import { toast } from "../../stores/toast";
import { useNow } from "../../hooks/useNow";
import { CodeBlock } from "../../components/CodeBlock";
import { ProviderLogo } from "../../components/EntityLogo";
import { Breadcrumb, BreadcrumbItem } from "@/components/base/breadcrumb/breadcrumb";
import { Button, ButtonLink } from "@/components/base/buttons/button";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { Badge } from "@/components/base/badges/badge";
import { SpinnerGlyph } from "@/components/base/spinner/spinner";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { cx } from "@/utils/cx";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout, EmptyState, PageSkeleton, Section, StatTile } from "@/components/acn";
import { LiveReportView, TestReportList } from "./ProviderEditor";
import { OutcomeBadge } from "./SpecTabs";
import { LostPathArt } from "@/components/foundations/illustrations";

type IconComponent = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;

const STATUS_META: Record<AgentRunStatus, { label: string; color: ChipColor }> = {
  queued: { label: "queued", color: "neutral" },
  running: { label: "running", color: "blue" },
  testing: { label: "testing", color: "purple" },
  needs_review: { label: "needs review", color: "yellow" },
  published: { label: "published", color: "lime" },
  failed: { label: "failed", color: "rose" },
  cancelled: { label: "cancelled", color: "neutral" },
};

export function RunStatusBadge({ status }: { status: AgentRunStatus }) {
  const m = STATUS_META[status] ?? { label: status, color: "neutral" as const };
  return (
    <Chip variant="caption" color={m.color} className="gap-1 font-mono">
      {ACTIVE_STATUSES.has(status) && <SpinnerGlyph size="xs" track={false} className="text-current" />}
      {m.label}
    </Chip>
  );
}

/**
 * Client side navigation for a ButtonLink until RAC's RouterProvider lands:
 * a plain click routes in app, a modified click keeps the browser default.
 */
export function RouterButtonLink({ to, ...props }: Omit<Parameters<typeof ButtonLink>[0], "href"> & { to: string }) {
  const navigate = useNavigate();
  return (
    <ButtonLink
      {...props}
      href={to}
      onClick={(e: MouseEvent<HTMLAnchorElement>) => {
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        navigate(to);
      }}
    />
  );
}

const STEP_ICON: Record<AgentStepType, { icon: IconComponent; cls: string }> = {
  info: { icon: RiInformationLine, cls: "bg-background-secondary-default text-foreground-icon-secondary" },
  thinking: { icon: RiBrainLine, cls: "bg-status-purple-background text-status-purple-text" },
  search: { icon: RiSearchLine, cls: "bg-status-blue-background text-status-blue-text" },
  fetch: { icon: RiDownloadLine, cls: "bg-status-blue-background text-status-blue-text" },
  tool: { icon: RiToolsLine, cls: "bg-status-yellow-background text-status-yellow-text" },
  result: { icon: RiCheckboxCircleLine, cls: "bg-status-lime-background text-status-lime-text" },
  error: { icon: RiCloseCircleLine, cls: "bg-status-rose-background text-status-rose-text" },
  done: { icon: RiFlagLine, cls: "bg-button-primary text-text-white" },
};

function elapsed(from: string, to: number): string {
  const s = Math.max(0, Math.round((to - Date.parse(from)) / 1000));
  if (Number.isNaN(s)) return "";
  const m = Math.floor(s / 60);
  return m ? `${m}m ${String(s % 60).padStart(2, "0")}s` : `${s}s`;
}

const SOFT_EASE = [0.22, 1, 0.36, 1] as const;

/** A step's detail text: a short inner scroller with the soft top edge. */
function StepDetail({ text }: { text: string }) {
  const fade = useScrollFade();
  return (
    <div className="relative mt-2">
      <pre
        onScroll={fade.onScroll}
        tabIndex={0}
        aria-label="Step detail"
        className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-xl bg-background-tertiary-default p-3 font-mono text-caption-1-regular text-text-secondary outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
      >
        {text}
      </pre>
      <ScrollFade progress={fade.progress} surfaceClassName="from-background-tertiary-default" className="rounded-t-xl" />
    </div>
  );
}

/**
 * One row of the run log, in the agent log look: a glyph column joined by a
 * hairline guide, the title, its time and type, and an optional disclosure.
 * The row still being worked on shows a spinner and a shimmering title.
 */
function StepRow({ step, last, live, enter }: { step: AgentStep; last: boolean; live: boolean; enter: boolean }) {
  const [open, setOpen] = useState(false);
  const reduce = useReducedMotion();
  const meta = STEP_ICON[step.type] ?? STEP_ICON.info;
  const I = meta.icon;
  const working = live && last;
  const hasDetail = !!step.detail?.trim();
  const time = new Date(step.ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  const title = working ? (
    <span className="min-w-0 flex-1 break-words text-body-regular">
      <ShimmerText>{step.title}</ShimmerText>
    </span>
  ) : (
    <span className="min-w-0 flex-1 break-words text-body-regular text-text-primary">{step.title}</span>
  );
  const head = (
    <>
      {title}
      <span className="shrink-0 pt-0.5 font-mono text-caption-1-regular tabular-nums text-text-tertiary">{time}</span>
    </>
  );

  return (
    <motion.li
      className="relative flex gap-3 pb-4"
      initial={enter ? (reduce ? { opacity: 0 } : { opacity: 0, y: 4, filter: "blur(6px)" }) : false}
      animate={reduce ? { opacity: 1 } : { opacity: 1, y: 0, filter: "blur(0px)" }}
      transition={{ duration: reduce ? 0.2 : 0.42, ease: SOFT_EASE }}
    >
      {!last && <span className="absolute start-[13.5px] top-8 h-[calc(100%-28px)] w-px bg-separator-border" aria-hidden />}
      <span className={cx("relative flex size-7 shrink-0 items-center justify-center rounded-full", meta.cls)}>
        {working ? <SpinnerGlyph size="xs" track={false} className="text-current" /> : <I className="size-4" aria-hidden />}
      </span>
      <div className="min-w-0 flex-1 pt-1">
        {hasDetail ? (
          <button
            type="button"
            onClick={() => setOpen(!open)}
            aria-expanded={open}
            className="flex w-full cursor-pointer items-start gap-2 rounded-md text-start outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          >
            {head}
            <RiArrowDownSLine
              className={cx("size-4 shrink-0 text-foreground-icon-tertiary transition-transform duration-200 ease-out motion-reduce:transition-none", open && "rotate-180")}
              aria-hidden
            />
          </button>
        ) : (
          <div className="flex items-start gap-2">{head}</div>
        )}
        <div className="font-mono text-caption-1-regular text-text-tertiary">{step.type}</div>
        {open && hasDetail && <StepDetail text={step.detail} />}
      </div>
    </motion.li>
  );
}

function Eyebrow({ children }: { children: ReactNode }) {
  return <dt className="text-caption-2-semibold uppercase text-text-tertiary">{children}</dt>;
}

function DraftPreview({ run }: { run: AgentRun }) {
  const d = run.draft;
  if (!d) return null;
  const spec = normalizeSpec(d.spec);
  return (
    <Section title="Draft provider" description="What the agent wrote. After publishing, open the provider to change any of it.">
      <div className="flex items-center gap-3">
        <ProviderLogo provider={{ name: d.name || d.slug, color: d.color, logo_url: null }} />
        <div className="min-w-0">
          <div className="truncate text-headline-medium text-text-primary">{d.name}</div>
          <div className="truncate font-mono text-caption-1-regular text-text-secondary">
            {d.slug} · {d.category || "other"} · {spec.base_url}
          </div>
        </div>
      </div>
      {d.description && <p className="mt-3 text-body-2-regular text-text-secondary">{d.description}</p>}
      <dl className="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-4">
        {[
          ["auth", `${spec.auth.type} · ${spec.auth.name}`],
          ["rules", String(spec.rules.length)],
          ["tests", String(spec.tests.length)],
          ["health", spec.health ? `${spec.health.method} ${spec.health.path}` : "none"],
        ].map(([k, v]) => (
          <div key={k} className="min-w-0 rounded-xl bg-background-secondary-default px-3 py-2">
            <Eyebrow>{k}</Eyebrow>
            <dd className="truncate font-mono text-caption-1-regular text-text-primary" title={v}>
              {v}
            </dd>
          </div>
        ))}
      </dl>
      {spec.rules.length > 0 && (
        <ol className="mt-4 flex flex-col gap-1.5">
          {spec.rules.map((r, i) => (
            <li key={i} className="flex flex-wrap items-center gap-2 text-body-2-regular">
              <span className="font-mono text-caption-1-regular tabular-nums text-text-tertiary">{i + 1}</span>
              <span className="text-text-primary">{r.name}</span>
              <OutcomeBadge outcome={r.outcome} />
            </li>
          ))}
        </ol>
      )}
      <details className="group mt-4">
        <summary className="flex w-fit cursor-pointer list-none items-center gap-1 rounded-md py-1 text-body-2-medium text-text-secondary outline-none transition-colors duration-150 ease hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring [&::-webkit-details-marker]:hidden">
          <RiArrowDownSLine className="size-4 transition-transform duration-200 ease-out group-open:rotate-180 motion-reduce:transition-none" aria-hidden />
          Full spec JSON
        </summary>
        <div className="mt-2">
          <CodeBlock code={JSON.stringify(d, null, 2)} label="provider.json" />
        </div>
      </details>
    </Section>
  );
}

export function AiRunPage() {
  const id = Number(useParams().runId);
  const role = useAuth((s) => s.user?.role);
  const manage = can.manage(role);
  const state = useAi((s) => s.byId[id]);
  const { poll, cancel, publish } = useAi();
  const refreshProviders = useProviders((s) => s.fetch);
  const refreshServices = useServices((s) => s.fetch);
  const [notFound, setNotFound] = useState(false);
  const [busy, setBusy] = useState<"cancel" | "publish" | null>(null);
  const logRef = useRef<HTMLDivElement>(null);
  const fade = useScrollFade();
  const now = useNow(1000);
  const run = state?.run;
  const active = !!run && ACTIVE_STATUSES.has(run.status);
  const prevStatus = useRef<string | null>(null);
  // Steps already recorded when the page opens render still; new ones blur in as they stream.
  const firstSeq = useRef<{ run: number; seq: number } | null>(null);
  if (state && firstSeq.current?.run !== id) firstSeq.current = { run: id, seq: state.steps.reduce((m, s) => Math.max(m, s.seq), -1) };
  useTitle(run ? `${plainText(run.title) || run.draft?.name || "Run"} #${run.id}` : "AI run");

  // Poll every 1.5s while the run is active; fetch once otherwise.
  useEffect(() => {
    let stop = false;
    let t: number | undefined;
    const tick = async () => {
      try {
        const s = await poll(id);
        if (stop) return;
        if (ACTIVE_STATUSES.has(s.run.status)) t = window.setTimeout(tick, 1500);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) setNotFound(true);
        else if (!stop) t = window.setTimeout(tick, 4000);
      }
    };
    void tick();
    return () => {
      stop = true;
      window.clearTimeout(t);
    };
  }, [id, poll]);

  // When a run lands, refresh providers and services so the new one shows everywhere.
  useEffect(() => {
    if (!run) return;
    if (prevStatus.current && prevStatus.current !== run.status && !ACTIVE_STATUSES.has(run.status)) {
      refreshProviders().catch(() => undefined);
      refreshServices().catch(() => undefined);
      if (run.status === "published") toast.success(`${run.draft?.name ?? plainText(run.title)} is live`);
    }
    prevStatus.current = run.status;
  }, [run, refreshProviders, refreshServices]);

  // Follow new steps inside the log while live, unless the reader scrolled up in it.
  const stepCount = state?.steps.length ?? 0;
  const pinned = useRef(true);
  useEffect(() => {
    const el = logRef.current;
    if (!active || !el || !pinned.current) return;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    el.scrollTo({ top: el.scrollHeight, behavior: reduce ? "auto" : "smooth" });
  }, [stepCount, active]);

  if (notFound)
    return (
      <EmptyState
        art={LostPathArt}
        title={<h1 className="text-headline-semibold">Run not found</h1>}
        body="It may have been removed, or the link is wrong."
        action={
          <RouterButtonLink to="/providers/ai" variant="primary">
            Back to AI builder
          </RouterButtonLink>
        }
      />
    );
  if (!state || !run) return <PageSkeleton variant="list" tiles={4} />;

  const act = async (kind: "cancel" | "publish") => {
    setBusy(kind);
    try {
      if (kind === "cancel") {
        await cancel(id);
        toast.info("Run cancelled");
      } else {
        await publish(id);
        toast.success("Published. The provider is live.");
        refreshProviders().catch(() => undefined);
        refreshServices().catch(() => undefined);
      }
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  const u = run.usage ?? { input_tokens: 0, output_tokens: 0, web_searches: 0, turns: 0, cost_usd: 0 };
  const end = run.finished_at ? Date.parse(run.finished_at) : now;
  const slug = run.draft?.slug || run.provider_slug;
  const canOpenProvider = run.status === "published" || (run.kind === "update_provider" && !!run.provider_slug);
  const description = plainText(run.description);

  return (
    <div>
      <Breadcrumb className="mb-5">
        <BreadcrumbItem href="/providers">Providers</BreadcrumbItem>
        <BreadcrumbItem href="/providers/ai">AI builder</BreadcrumbItem>
        <BreadcrumbItem current>Run #{run.id}</BreadcrumbItem>
      </Breadcrumb>

      <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="min-w-0 break-words font-mono text-title-1-medium text-text-primary">
              <FadeText text={plainText(run.title) || run.draft?.name || `Run #${run.id}`} />
            </h1>
            <RunStatusBadge status={run.status} />
          </div>
          {description && (
            <div className="mt-1.5 max-w-2xl text-body-regular text-text-secondary">
              <FadeText text={description} />
            </div>
          )}
          <div className="mt-1.5 wrap-anywhere font-mono text-caption-1-regular text-text-secondary">
            #{run.id} · {run.kind === "update_provider" ? `update ${run.provider_slug}` : "new provider"} · {run.created_by} · started {dateTime(run.created_at)} ·{" "}
            <span className="tabular-nums">{elapsed(run.created_at, end)}</span>
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap gap-2">
          {active && manage && (
            <Button variant="secondary" leadingIcon={busy === "cancel" ? ButtonSpinner : RiStopLine} disabled={busy === "cancel"} onClick={() => void act("cancel")}>
              Cancel
            </Button>
          )}
          {run.status === "needs_review" && manage && (
            <Button variant="primary" leadingIcon={busy === "publish" ? ButtonSpinner : RiSparkling2Line} disabled={busy === "publish"} onClick={() => void act("publish")}>
              Publish
            </Button>
          )}
          {canOpenProvider && slug && (
            <RouterButtonLink to={`/providers/${encodeURIComponent(slug)}`} variant="secondary">
              Open provider
            </RouterButtonLink>
          )}
          {run.service_id && (
            <RouterButtonLink to={`/services/${run.service_id}`} variant="primary">
              Open service
            </RouterButtonLink>
          )}
        </div>
      </header>

      <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile label="Tokens" value={compact(u.input_tokens + u.output_tokens)} sub={`${compact(u.input_tokens)} in · ${compact(u.output_tokens)} out`} />
        <StatTile label="Searches" value={u.web_searches} />
        <StatTile label="Turns" value={u.turns} sub="of 40 max" />
        <StatTile label="Cost" value={`$${(u.cost_usd ?? 0).toFixed(2)}`} />
      </div>

      {run.status === "failed" && run.error && (
        <Callout tone="error" title="The run failed" className="mt-6">
          <div className="mt-0.5 break-words">{run.error}</div>
          {manage && (
            <Link
              to={run.provider_slug ? `/providers/ai?provider=${encodeURIComponent(run.provider_slug)}` : "/providers/ai"}
              className="mt-2 inline-block rounded-sm text-body-2-semibold underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
            >
              Try again with more context
            </Link>
          )}
        </Callout>
      )}
      {run.status === "needs_review" && (
        <Callout tone="warning" className="mt-6">
          The draft passed its tests and is waiting for you. Review it below, then publish.
        </Callout>
      )}
      {run.summary && (
        <Callout tone={run.status === "published" ? "success" : "neutral"} icon={AcnAiBuilder} className="mt-6">
          {run.summary}
        </Callout>
      )}

      <div className="mt-8 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
        <section aria-labelledby="steps-h" className="min-w-0">
          <h2 id="steps-h" className="mb-3 flex items-center gap-2 text-headline-semibold text-text-primary">
            Steps <Badge className="font-mono tabular-nums">{state.steps.length}</Badge>
          </h2>
          {state.steps.length === 0 ? (
            <div className="flex items-center gap-3 rounded-3xl border border-border-button-default bg-background-primary-default p-5 text-body-regular text-text-secondary">
              {active ? (
                <>
                  <SpinnerGlyph size="sm" />
                  <ShimmerText>Waiting for the agent to start</ShimmerText>
                </>
              ) : (
                "This run recorded no steps."
              )}
            </div>
          ) : (
            <div className="relative overflow-hidden rounded-3xl border border-border-button-default bg-background-primary-default">
              <div
                ref={logRef}
                tabIndex={0}
                aria-label="Run log"
                onScroll={(e) => {
                  fade.onScroll(e);
                  const el = e.currentTarget;
                  pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
                }}
                className="max-h-[min(640px,70vh)] overflow-y-auto overscroll-contain px-4 pt-5 pb-1 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-border-focus-ring sm:px-5"
              >
                <ol aria-live="polite" aria-relevant="additions">
                  {state.steps.map((s, i) => (
                    <StepRow key={s.seq} step={s} last={i === state.steps.length - 1} live={active} enter={s.seq > (firstSeq.current?.seq ?? -1)} />
                  ))}
                </ol>
              </div>
              <ScrollFade progress={fade.progress} />
            </div>
          )}
        </section>

        <div className="flex min-w-0 flex-col gap-5">
          <Section title="Input">
            <dl className="flex flex-col gap-3 text-body-2-regular">
              {run.input.context && (
                <div>
                  <Eyebrow>Context</Eyebrow>
                  <dd className="mt-0.5 whitespace-pre-wrap break-words text-text-primary">{run.input.context}</dd>
                </div>
              )}
              {run.input.urls?.length > 0 && (
                <div>
                  <Eyebrow>URLs</Eyebrow>
                  {run.input.urls.map((u2) => (
                    <dd key={u2} className="wrap-anywhere" dir="ltr">
                      {safeHref(u2) ? (
                        <a
                          href={safeHref(u2)}
                          target="_blank"
                          rel="noreferrer"
                          className="inline-flex items-center gap-1 rounded-sm font-mono text-caption-1-regular text-text-primary outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                        >
                          <RiGlobalLine className="size-3 shrink-0" aria-hidden /> {u2}
                        </a>
                      ) : (
                        <span className="inline-flex items-center gap-1 font-mono text-caption-1-regular text-text-primary">
                          <RiGlobalLine className="size-3 shrink-0" aria-hidden /> {u2}
                        </span>
                      )}
                    </dd>
                  ))}
                </div>
              )}
              {run.input.github && (
                <div>
                  <Eyebrow>GitHub</Eyebrow>
                  <dd className="font-mono text-caption-1-regular text-text-primary">{run.input.github}</dd>
                </div>
              )}
              <div className="flex flex-wrap gap-1.5 pt-1">
                <Chip variant="caption" color={run.input.auto_publish ? "lime" : "neutral"}>
                  {run.input.auto_publish ? "auto publish" : "review first"}
                </Chip>
                {run.input.create_service && (
                  <Chip variant="caption" color="blue">
                    creates service
                  </Chip>
                )}
                {run.input.has_test_key && (
                  <Chip variant="caption" color="purple">
                    test key
                  </Chip>
                )}
              </div>
            </dl>
          </Section>

          {run.report && (
            <Section title="Tests" description={`${run.report.passed} passed · ${run.report.failed} failed`}>
              <TestReportList report={run.report} />
            </Section>
          )}

          {run.live && (
            <Section title="Live check">
              <LiveReportView live={run.live} />
            </Section>
          )}
        </div>
      </div>

      {run.draft && (
        <div className="mt-6">
          <DraftPreview run={run} />
        </div>
      )}
    </div>
  );
}
