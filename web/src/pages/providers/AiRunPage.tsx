import { useEffect, useRef, useState } from "react";
import { useTitle } from "../../hooks/useTitle";
import { Link, useParams } from "react-router-dom";
import {
  ArrowLeft,
  Brain,
  CheckCircle2,
  ChevronDown,
  Circle,
  Download,
  Flag,
  Globe,
  Info,
  Loader2,
  Search,
  Sparkles,
  Square,
  Wrench,
  XCircle,
  type LucideIcon,
} from "lucide-react";
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
import { Badge, Button, Callout, Empty, PageSkeleton, Section, Stat, cx } from "../../components/ui";
import { LiveReportView, TestReportList } from "./ProviderEditor";
import { OutcomeBadge } from "./SpecTabs";

const STATUS_META: Record<AgentRunStatus, { label: string; tone: "neutral" | "blue" | "violet" | "amber" | "green" | "red" }> = {
  queued: { label: "queued", tone: "neutral" },
  running: { label: "running", tone: "blue" },
  testing: { label: "testing", tone: "violet" },
  needs_review: { label: "needs review", tone: "amber" },
  published: { label: "published", tone: "green" },
  failed: { label: "failed", tone: "red" },
  cancelled: { label: "cancelled", tone: "neutral" },
};

export function RunStatusBadge({ status }: { status: AgentRunStatus }) {
  const m = STATUS_META[status] ?? { label: status, tone: "neutral" as const };
  return (
    <Badge tone={m.tone}>
      {ACTIVE_STATUSES.has(status) && <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />}
      {m.label}
    </Badge>
  );
}

const STEP_ICON: Record<AgentStepType, { icon: LucideIcon; cls: string }> = {
  info: { icon: Info, cls: "bg-soft text-muted" },
  thinking: { icon: Brain, cls: "bg-violet-50 text-violet-700" },
  search: { icon: Search, cls: "bg-sky-50 text-sky-700" },
  fetch: { icon: Download, cls: "bg-sky-50 text-sky-700" },
  tool: { icon: Wrench, cls: "bg-amber-50 text-amber-800" },
  result: { icon: CheckCircle2, cls: "bg-emerald-50 text-emerald-700" },
  error: { icon: XCircle, cls: "bg-red-50 text-red-700" },
  done: { icon: Flag, cls: "bg-ink text-on-ink" },
};

function elapsed(from: string, to: number): string {
  const s = Math.max(0, Math.round((to - Date.parse(from)) / 1000));
  if (Number.isNaN(s)) return "";
  const m = Math.floor(s / 60);
  return m ? `${m}m ${String(s % 60).padStart(2, "0")}s` : `${s}s`;
}

function StepRow({ step, last, live, enter }: { step: AgentStep; last: boolean; live: boolean; enter: boolean }) {
  const [open, setOpen] = useState(false);
  const meta = STEP_ICON[step.type] ?? STEP_ICON.info;
  const I = meta.icon;
  const hasDetail = !!step.detail?.trim();
  return (
    <li className={cx("relative flex gap-3 pb-4", enter && "fade-up")}>
      {!last && <span className="absolute left-[15px] top-8 h-[calc(100%-24px)] w-px bg-line" aria-hidden="true" />}
      <span className={cx("relative flex h-8 w-8 shrink-0 items-center justify-center rounded-full", meta.cls)}>
        {live && last ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" /> : <I className="h-4 w-4" aria-hidden="true" />}
      </span>
      <div className="min-w-0 flex-1 pt-1">
        {hasDetail ? (
          <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="tap flex w-full items-start gap-2 text-left">
            <span className="min-w-0 flex-1 break-words text-[14px] text-ink">{step.title}</span>
            <span className="shrink-0 font-mono text-[11px] text-faint">{new Date(step.ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</span>
            <ChevronDown className={cx("mt-0.5 h-4 w-4 shrink-0 text-faint transition-transform", open && "rotate-180")} aria-hidden="true" />
          </button>
        ) : (
          <div className="flex items-start gap-2">
            <span className="min-w-0 flex-1 break-words text-[14px] text-ink">{step.title}</span>
            <span className="shrink-0 font-mono text-[11px] text-faint">{new Date(step.ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</span>
          </div>
        )}
        <div className="font-mono text-[11px] text-faint">{step.type}</div>
        {open && hasDetail && (
          <pre className="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-xl bg-soft p-3 font-mono text-[12px] leading-relaxed text-muted">{step.detail}</pre>
        )}
      </div>
    </li>
  );
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
          <div className="truncate text-[15px] font-medium text-ink">{d.name}</div>
          <div className="truncate font-mono text-xs text-muted">
            {d.slug} · {d.category || "other"} · {spec.base_url}
          </div>
        </div>
      </div>
      {d.description && <p className="mt-3 text-[13px] leading-relaxed text-muted">{d.description}</p>}
      <dl className="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-4">
        {[
          ["auth", `${spec.auth.type} · ${spec.auth.name}`],
          ["rules", String(spec.rules.length)],
          ["tests", String(spec.tests.length)],
          ["health", spec.health ? `${spec.health.method} ${spec.health.path}` : "none"],
        ].map(([k, v]) => (
          <div key={k} className="min-w-0 rounded-xl bg-soft px-3 py-2">
            <dt className="font-mono text-[10px] uppercase tracking-wide text-faint">{k}</dt>
            <dd className="truncate font-mono text-xs text-ink" title={v}>
              {v}
            </dd>
          </div>
        ))}
      </dl>
      {spec.rules.length > 0 && (
        <ol className="mt-4 space-y-1.5">
          {spec.rules.map((r, i) => (
            <li key={i} className="flex flex-wrap items-center gap-2 text-[13px]">
              <span className="font-mono text-[11px] text-faint">{i + 1}</span>
              <span className="text-ink">{r.name}</span>
              <OutcomeBadge outcome={r.outcome} />
            </li>
          ))}
        </ol>
      )}
      <details className="mt-4">
        <summary className="tap flex cursor-pointer items-center py-1 text-[13px] font-medium text-muted hover:text-ink">Full spec JSON</summary>
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
  const endRef = useRef<HTMLDivElement>(null);
  const now = useNow(1000);
  const run = state?.run;
  const active = !!run && ACTIVE_STATUSES.has(run.status);
  const prevStatus = useRef<string | null>(null);
  // Steps already recorded when the page opens render still; new ones fade up as they stream in.
  const firstSeq = useRef<{ run: number; seq: number } | null>(null);
  if (state && firstSeq.current?.run !== id) firstSeq.current = { run: id, seq: state.steps.reduce((m, s) => Math.max(m, s.seq), -1) };
  useTitle(run ? `${run.title || run.draft?.name || "Run"} #${run.id}` : "AI run");

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
      if (run.status === "published") toast.success(`${run.draft?.name ?? run.title} is live`);
    }
    prevStatus.current = run.status;
  }, [run, refreshProviders, refreshServices]);

  // Follow new steps while live, without hijacking scroll if the user scrolled up.
  const stepCount = state?.steps.length ?? 0;
  useEffect(() => {
    if (!active || !endRef.current) return;
    const r = endRef.current.getBoundingClientRect();
    if (r.top < window.innerHeight + 200) {
      const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      endRef.current.scrollIntoView({ block: "end", behavior: reduce ? "auto" : "smooth" });
    }
  }, [stepCount, active]);

  if (notFound)
    return (
      <Empty
        title="Run not found"
        body="It may have been removed, or the link is wrong."
        action={
          <Link to="/providers/ai" className="tap inline-flex h-10 items-center rounded-full bg-ink px-4 text-sm font-medium text-on-ink">
            Back to AI builder
          </Link>
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

  return (
    <div>
      <Link to="/providers/ai" className="tap mb-5 inline-flex items-center gap-1.5 font-mono text-xs text-muted hover:text-ink">
        <ArrowLeft className="h-3.5 w-3.5" /> AI builder
      </Link>

      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="min-w-0 break-words font-mono text-[24px] leading-tight tracking-tight text-ink sm:text-[28px]">{run.title || run.draft?.name || `Run #${run.id}`}</h1>
            <RunStatusBadge status={run.status} />
          </div>
          <div className="wrap-anywhere mt-1.5 font-mono text-xs text-muted">
            #{run.id} · {run.kind === "update_provider" ? `update ${run.provider_slug}` : "new provider"} · {run.created_by} · started {dateTime(run.created_at)} ·{" "}
            <span className="tabular-nums">{elapsed(run.created_at, end)}</span>
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap gap-2">
          {active && manage && (
            <Button icon={<Square className="h-3.5 w-3.5" />} loading={busy === "cancel"} onClick={() => void act("cancel")}>
              Cancel
            </Button>
          )}
          {run.status === "needs_review" && manage && (
            <Button variant="primary" icon={<Sparkles className="h-4 w-4" />} loading={busy === "publish"} onClick={() => void act("publish")}>
              Publish
            </Button>
          )}
          {canOpenProvider && slug && (
            <Link to={`/providers/${encodeURIComponent(slug)}`} className="tap inline-flex h-10 items-center rounded-full bg-surface px-4 text-sm font-medium text-ink shadow-card hover:bg-soft">
              Open provider
            </Link>
          )}
          {run.service_id && (
            <Link to={`/services/${run.service_id}`} className="tap inline-flex h-10 items-center rounded-full bg-ink px-4 text-sm font-medium text-on-ink hover:bg-ink-strong">
              Open service
            </Link>
          )}
        </div>
      </div>

      <div className="mt-6 grid grid-cols-2 gap-2.5 sm:grid-cols-4">
        <Stat label="Tokens" value={compact(u.input_tokens + u.output_tokens)} sub={`${compact(u.input_tokens)} in · ${compact(u.output_tokens)} out`} />
        <Stat label="Searches" value={u.web_searches} />
        <Stat label="Turns" value={u.turns} sub="of 40 max" />
        <Stat label="Cost" value={`$${(u.cost_usd ?? 0).toFixed(2)}`} />
      </div>

      {run.status === "failed" && run.error && (
        <Callout tone="red" icon={<XCircle className="h-4 w-4" />} className="mt-6">
          <div className="font-medium">The run failed</div>
          <div className="mt-0.5 break-words">{run.error}</div>
          {manage && (
            <Link to={run.provider_slug ? `/providers/ai?provider=${encodeURIComponent(run.provider_slug)}` : "/providers/ai"} className="mt-2 inline-block font-medium underline underline-offset-2">
              Try again with more context
            </Link>
          )}
        </Callout>
      )}
      {run.status === "needs_review" && (
        <Callout tone="amber" className="mt-6">
          The draft passed its tests and is waiting for you. Review it below, then publish.
        </Callout>
      )}
      {run.summary && (
        <Callout tone={run.status === "published" ? "green" : "neutral"} icon={<Sparkles className="h-4 w-4" />} className="mt-6">
          {run.summary}
        </Callout>
      )}

      <div className="mt-8 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
        <section aria-labelledby="steps-h" className="min-w-0">
          <h2 id="steps-h" className="mb-4 flex items-center gap-2 text-[15px] font-semibold text-ink">
            Steps <span className="font-mono text-xs font-normal text-faint">{state.steps.length}</span>
          </h2>
          {state.steps.length === 0 ? (
            <div className="flex items-center gap-3 rounded-card bg-surface p-4 text-sm text-muted shadow-card">
              {active ? <Loader2 className="h-4 w-4 animate-spin" /> : <Circle className="h-4 w-4" />}
              {active ? "Waiting for the agent to start" : "This run recorded no steps."}
            </div>
          ) : (
            <ol className="rounded-card bg-surface p-4 shadow-card sm:p-5" aria-live="polite" aria-relevant="additions">
              {state.steps.map((s, i) => (
                <StepRow key={s.seq} step={s} last={i === state.steps.length - 1} live={active} enter={s.seq > (firstSeq.current?.seq ?? -1)} />
              ))}
            </ol>
          )}
          <div ref={endRef} />
        </section>

        <div className="space-y-5">
          <Section title="Input">
            <dl className="space-y-3 text-[13px]">
              {run.input.context && (
                <div>
                  <dt className="font-mono text-[11px] uppercase tracking-wide text-faint">Context</dt>
                  <dd className="mt-0.5 whitespace-pre-wrap break-words text-ink">{run.input.context}</dd>
                </div>
              )}
              {run.input.urls?.length > 0 && (
                <div>
                  <dt className="font-mono text-[11px] uppercase tracking-wide text-faint">URLs</dt>
                  {run.input.urls.map((u2) => (
                    <dd key={u2} className="wrap-anywhere">
                      {safeHref(u2) ? (
                        <a href={safeHref(u2)} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 font-mono text-xs text-ink hover:underline">
                          <Globe className="h-3 w-3 shrink-0" /> {u2}
                        </a>
                      ) : (
                        <span className="inline-flex items-center gap-1 font-mono text-xs text-ink">
                          <Globe className="h-3 w-3 shrink-0" /> {u2}
                        </span>
                      )}
                    </dd>
                  ))}
                </div>
              )}
              {run.input.github && (
                <div>
                  <dt className="font-mono text-[11px] uppercase tracking-wide text-faint">GitHub</dt>
                  <dd className="font-mono text-xs text-ink">{run.input.github}</dd>
                </div>
              )}
              <div className="flex flex-wrap gap-1.5 pt-1">
                <Badge tone={run.input.auto_publish ? "green" : "neutral"}>{run.input.auto_publish ? "auto publish" : "review first"}</Badge>
                {run.input.create_service && <Badge tone="blue">creates service</Badge>}
                {run.input.has_test_key && <Badge tone="violet">test key</Badge>}
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
