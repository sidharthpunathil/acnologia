import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link as RouterLink } from "react-router-dom";
import { RiCheckLine, RiCloseLine, RiPlayLine } from "@remixicon/react";
import { errorMessage } from "@/lib/api";
import { dateTime, plural, relTime } from "@/lib/format";
import {
  PRICES_TARGET,
  refreshApi,
  targetName,
  usd,
  type ItemStatus,
  type RefreshApply,
  type RefreshItem,
  type RefreshReview,
  type RefreshRun,
  type RefreshSchedule,
  type RefreshScope,
  type RefreshSettings,
  type RefreshState,
} from "@/lib/refresh";
import { usePoll } from "@/hooks/usePoll";
import { useProviders } from "@/stores/providers";
import { toast } from "@/stores/toast";
import { Accordion, AccordionItem } from "@/components/base/accordion/accordion";
import { Button } from "@/components/base/buttons/button";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { NumberField } from "@/components/base/number-field/number-field";
import { Select, SelectItem } from "@/components/base/select/select";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Switch } from "@/components/base/switch/switch";
import { Callout } from "@/components/acn/callout";
import { MultiSelect } from "@/components/MultiSelect";
import { RefreshDiff } from "@/components/RefreshDiff";
import { SAVE_SHORTCUT, useLoad, useSaveShortcut } from "./hooks";
import { ReadOnlyNote, RowsSkeleton, SaveBar, SectionError, SettingRow, SettingsCard, SettingsSection } from "./parts";

const SCHEDULES: { id: RefreshSchedule; label: string }[] = [
  { id: "off", label: "Off" },
  { id: "weekly", label: "Weekly" },
  { id: "biweekly", label: "Every 2 weeks" },
  { id: "monthly", label: "Monthly, on the 1st" },
  { id: "days", label: "Every N days" },
];

const SCOPES: { id: RefreshScope; label: string; hint: string }[] = [
  { id: "with_keys", label: "Providers with keys", hint: "Behind at least one service with a usable key: active, cooling or out of quota." },
  { id: "with_services", label: "Providers with services", hint: "Behind at least one service, with or without keys." },
  { id: "all", label: "Every provider", hint: "Every provider in the catalog. Costs the most." },
];

const APPLY: { id: RefreshApply; label: string; hint: string }[] = [
  {
    id: "auto",
    label: "Apply sourced changes",
    hint: "A small change publishes a new version on its own: prices moving within 4x or looser limits, each citing a page on the provider's own site. Anything else waits for you.",
  },
  { id: "review", label: "Always review", hint: "Every change waits here until a superadmin applies or dismisses it." },
];

const HOURS = Array.from({ length: 24 }, (_, h) => h);
const hourLabel = (h: number) => `${String(h).padStart(2, "0")}:00 UTC`;

const ITEM_CHIP: Record<ItemStatus, { label: string; color: ChipColor }> = {
  pending: { label: "Waiting", color: "soft" },
  running: { label: "Checking", color: "blue" },
  unchanged: { label: "No change", color: "neutral" },
  applied: { label: "Applied", color: "lime" },
  review: { label: "In review", color: "yellow" },
  failed: { label: "Failed", color: "rose" },
  skipped: { label: "Not reached", color: "soft" },
};

const RUN_TRIGGER: Record<RefreshRun["trigger"], string> = {
  schedule: "Scheduled",
  manual: "By hand",
  provider: "Check now",
};

function same(a: RefreshSettings, b: RefreshSettings) {
  const keys: (keyof RefreshSettings)[] = [
    "schedule",
    "every_days",
    "hour",
    "scope",
    "include",
    "exclude",
    "include_peer",
    "claude_prices",
    "apply",
    "max_run_usd",
    "max_month_usd",
    "max_providers",
    "stale_days",
  ];
  return keys.every((k) => JSON.stringify(a[k]) === JSON.stringify(b[k]));
}

/** The schedule, scope, apply policy and budget. A superadmin edits; admins read. */
function SettingsForm({ state, onSaved, onDirty }: { state: RefreshState; onSaved: (s: RefreshState) => void; onDirty: (d: boolean) => void }) {
  const saved = state.settings;
  const [draft, setDraft] = useState<RefreshSettings>(saved);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const form = useRef<HTMLFormElement>(null);
  const providers = useProviders((s) => s.providers);
  const ensure = useProviders((s) => s.ensure);
  useEffect(() => {
    ensure().catch(() => undefined);
  }, [ensure]);
  useEffect(() => setDraft(saved), [saved]);

  const canEdit = state.can_edit;
  const dirty = !same(draft, saved);
  useEffect(() => {
    onDirty(canEdit && dirty);
    return () => onDirty(false);
  }, [canEdit, dirty, onDirty]);

  const options = useMemo(
    () => providers.filter((p) => p.slug !== "acnologia-peer").map((p) => ({ value: p.slug, label: p.name, meta: p.slug })),
    [providers],
  );
  const set = <K extends keyof RefreshSettings>(k: K, v: RefreshSettings[K]) => setDraft((d) => ({ ...d, [k]: v }));

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!dirty || saving) return;
    setSaving(true);
    setError(null);
    try {
      const r = await refreshApi.save(draft);
      onSaved(r);
      toast.success("Refresh settings saved.");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };
  useSaveShortcut(form, canEdit && dirty, () => void save());

  const ro = !canEdit;
  const lists = draft.include.length + draft.exclude.length;
  const updated = saved.updated_at ? (
    <span title={dateTime(saved.updated_at)}>
      Saved {relTime(saved.updated_at)} by {saved.updated_by || "someone"}
    </span>
  ) : (
    "Using the defaults"
  );

  const body = (
    <SettingsCard>
      <SettingRow labelId="rf-schedule" label="Schedule" description="How often the refresh runs on its own, and at which hour (UTC) so it lands at a quiet time.">
        <div className="flex flex-wrap gap-2 sm:justify-end">
          <Select
            aria-labelledby="rf-schedule"
            selectedKey={draft.schedule}
            onSelectionChange={(k) => k != null && set("schedule", k as RefreshSchedule)}
            isDisabled={ro}
            className="w-full sm:w-48"
          >
            {SCHEDULES.map((s) => (
              <SelectItem key={s.id} id={s.id} textValue={s.label}>
                {s.label}
              </SelectItem>
            ))}
          </Select>
          {draft.schedule === "days" && (
            <NumberField
              aria-label="Days between runs"
              value={draft.every_days}
              onChange={(v) => set("every_days", Number.isFinite(v) ? v : 1)}
              minValue={1}
              maxValue={365}
              isReadOnly={ro}
              hideSteppers={ro}
              className="w-28"
            />
          )}
          {draft.schedule !== "off" && (
            <Select
              aria-label="Hour, UTC"
              selectedKey={String(draft.hour)}
              onSelectionChange={(k) => k != null && set("hour", Number(k))}
              isDisabled={ro}
              className="w-full sm:w-36"
            >
              {HOURS.map((h) => (
                <SelectItem key={h} id={String(h)} textValue={hourLabel(h)}>
                  {hourLabel(h)}
                </SelectItem>
              ))}
            </Select>
          )}
        </div>
      </SettingRow>
      <SettingRow labelId="rf-scope" label="Which providers" description={SCOPES.find((s) => s.id === draft.scope)?.hint}>
        <Select
          aria-labelledby="rf-scope"
          selectedKey={draft.scope}
          onSelectionChange={(k) => k != null && set("scope", k as RefreshScope)}
          isDisabled={ro}
          className="w-full sm:w-56"
        >
          {SCOPES.map((s) => (
            <SelectItem key={s.id} id={s.id} textValue={s.label}>
              {s.label}
            </SelectItem>
          ))}
        </Select>
      </SettingRow>
      <div className="px-4 sm:px-5">
        <Accordion defaultExpandedKeys={lists > 0 ? ["lists"] : []}>
          <AccordionItem
            id="lists"
            title={<span className="text-body-medium">Always or never check...</span>}
            description={
              <span className="text-body-2-regular">
                {lists > 0 ? `${plural(draft.include.length, "always")}, ${plural(draft.exclude.length, "never", "never")}` : "No exceptions to the scope"}
              </span>
            }
          >
            <div className="flex flex-col gap-5 pb-2">
              <div>
                <div className="mb-2 text-body-2-medium text-text-primary">Always check</div>
                <MultiSelect options={options} value={draft.include} onChange={(v) => set("include", v)} disabled={ro} searchLabel="providers" />
              </div>
              <div>
                <div className="mb-2 text-body-2-medium text-text-primary">Never check</div>
                <p className="mb-2 text-caption-1-regular text-text-secondary">Wins over the scope and over Always check.</p>
                <MultiSelect options={options} value={draft.exclude} onChange={(v) => set("exclude", v)} disabled={ro} searchLabel="providers" />
              </div>
            </div>
          </AccordionItem>
        </Accordion>
      </div>
      <SettingRow
        label="Providers copied from another server"
        description="Off by default: the server that shared them keeps their offers current, and your copy follows theirs."
      >
        <Switch isSelected={draft.include_peer} onChange={(v) => set("include_peer", v)} isReadOnly={ro} aria-label="Check providers copied from another server" />
      </SettingRow>
      <SettingRow
        label="Claude model prices"
        description="Also re-read Anthropic's pricing page, so the cost of Acnologia's own AI features stays right. Same apply rule and budget."
      >
        <Switch isSelected={draft.claude_prices} onChange={(v) => set("claude_prices", v)} isReadOnly={ro} aria-label="Re-read Claude model prices" />
      </SettingRow>
      <SettingRow labelId="rf-apply" label="When something changed" description={APPLY.find((a) => a.id === draft.apply)?.hint}>
        <Select
          aria-labelledby="rf-apply"
          selectedKey={draft.apply}
          onSelectionChange={(k) => k != null && set("apply", k as RefreshApply)}
          isDisabled={ro}
          className="w-full sm:w-56"
        >
          {APPLY.map((a) => (
            <SelectItem key={a.id} id={a.id} textValue={a.label}>
              {a.label}
            </SelectItem>
          ))}
        </Select>
      </SettingRow>
      <SettingRow label="Budget" description="The refresh model's spend. A run stops starting new providers once either cap is reached.">
        <div className="flex flex-wrap gap-2 sm:justify-end">
          <NumberField
            label="Per run"
            value={draft.max_run_usd}
            onChange={(v) => set("max_run_usd", Number.isFinite(v) ? v : 0.01)}
            minValue={0.01}
            maxValue={100}
            step={0.01}
            formatOptions={{ style: "currency", currency: "USD" }}
            isReadOnly={ro}
            hideSteppers={ro}
            className="w-32"
          />
          <NumberField
            label="Per month"
            value={draft.max_month_usd}
            onChange={(v) => set("max_month_usd", Number.isFinite(v) ? v : 0.01)}
            minValue={0.01}
            maxValue={1000}
            step={0.01}
            formatOptions={{ style: "currency", currency: "USD" }}
            isReadOnly={ro}
            hideSteppers={ro}
            className="w-32"
          />
        </div>
      </SettingRow>
      <SettingRow label="Pace" description="At most this many providers per run, oldest checked first. Providers checked recently are skipped.">
        <div className="flex flex-wrap gap-2 sm:justify-end">
          <NumberField
            label="Providers per run"
            value={draft.max_providers}
            onChange={(v) => set("max_providers", Number.isFinite(v) ? v : 1)}
            minValue={1}
            maxValue={100}
            isReadOnly={ro}
            hideSteppers={ro}
            className="w-36"
          />
          <NumberField
            label="Skip if checked within (days)"
            value={draft.stale_days}
            onChange={(v) => set("stale_days", Number.isFinite(v) ? v : 0)}
            minValue={0}
            maxValue={365}
            isReadOnly={ro}
            hideSteppers={ro}
            className="w-52"
          />
        </div>
      </SettingRow>
      {error && (
        <div className="px-4 py-3 sm:px-5">
          <Callout tone="error" title="Settings not saved">
            <span className="wrap-anywhere">{error}</span>
          </Callout>
        </div>
      )}
      {canEdit ? (
        <SaveBar
          dirty={dirty}
          saving={saving}
          onDiscard={() => {
            setDraft(saved);
            setError(null);
          }}
          saveLabel="Save refresh settings"
          status={updated}
          shortcut={SAVE_SHORTCUT}
        />
      ) : (
        <div className="px-4 py-3 sm:px-5">
          <ReadOnlyNote>Only a superadmin can change the refresh.</ReadOnlyNote>
        </div>
      )}
    </SettingsCard>
  );
  return canEdit ? (
    <form ref={form} onSubmit={(e) => void save(e)} aria-label="Provider refresh settings">
      {body}
    </form>
  ) : (
    body
  );
}

function Stat({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-caption-1-regular text-text-tertiary">{label}</dt>
      <dd className="mt-0.5 text-body-2-medium text-text-primary tabular-nums wrap-anywhere">{children}</dd>
    </div>
  );
}

/** How the refresh stands: the model, the last and next run, spend, and Run now. */
function StatusCard({ state, onStarted }: { state: RefreshState; onStarted: () => void }) {
  const [starting, setStarting] = useState(false);
  const s = state.settings;
  const last = state.last_run;
  const running = state.running;
  const m = state.model;

  const runNow = async () => {
    setStarting(true);
    try {
      const r = await refreshApi.runNow();
      toast.success(`Refresh started: ${plural(r.run.providers, "target")} to check.`);
      onStarted();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setStarting(false);
    }
  };

  const price = m.pricing_known ? `${usd(m.input_price)} / ${usd(m.output_price)} per M tokens in/out` : "price unknown";
  const pricey = m.pricing_known && m.input_price > 1;

  return (
    <SettingsCard>
      {!state.ai_enabled && (
        <div className="px-4 py-4 sm:px-5">
          <Callout tone="warning" title="AI is off">
            The refresh runs once ANTHROPIC_API_KEY is set and Acnologia restarts. Settings saved now apply then.
          </Callout>
        </div>
      )}
      <SettingRow
        label="Model"
        description={
          <>
            The refresh slot in{" "}
            <RouterLink
              to="/settings#ai"
              className="rounded-sm text-text-primary underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
            >
              Models
            </RouterLink>
            . A small model such as Haiku reads pricing pages well and costs little.
          </>
        }
      >
        <div className="flex flex-col gap-1 sm:items-end">
          <span dir="ltr" className="font-mono text-body-2-regular wrap-anywhere text-text-primary">
            {m.model}
          </span>
          <span className={pricey ? "text-caption-1-regular text-status-yellow-text" : "text-caption-1-regular text-text-tertiary"}>
            {pricey ? `${price}: consider a smaller model` : price}
          </span>
        </div>
      </SettingRow>
      <div className="px-4 py-4 sm:px-5">
        <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-4">
          <Stat label="Last run">
            {last ? (
              <span title={dateTime(last.started_at)}>
                {relTime(last.started_at)}
                <span className="block text-caption-1-regular text-text-secondary">
                  {last.checked} checked · {last.changed} changed · {last.failed} failed
                </span>
              </span>
            ) : (
              "Never"
            )}
          </Stat>
          <Stat label="Next run">
            {s.schedule === "off" ? "Schedule off" : state.next_run ? <span title={dateTime(state.next_run)}>{nextLabel(state.next_run)}</span> : "When AI is on"}
          </Stat>
          <Stat label="This month">
            {usd(state.month_spend_usd)} <span className="text-text-tertiary">of {usd(s.max_month_usd)}</span>
          </Stat>
          <Stat label="Waiting for review">{state.pending_reviews}</Stat>
        </dl>
      </div>
      {running && (
        <div className="px-4 py-3 sm:px-5" role="status">
          <p className="flex items-center gap-2 text-body-2-regular text-text-primary">
            <ButtonSpinner className="size-4" />
            Running: {running.providers - running.pending} of {running.providers} done, {usd(running.cost_usd)} so far.
          </p>
        </div>
      )}
      {state.can_edit && (
        <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-3 sm:px-5">
          <p className="text-caption-1-regular text-text-secondary">Checks everything in scope now, with the same rules, budget and staleness.</p>
          <Button
            variant="secondary"
            size="small"
            leadingIcon={starting ? ButtonSpinner : RiPlayLine}
            disabled={starting || !!running || !state.ai_enabled}
            onClick={() => void runNow()}
          >
            Run refresh now
          </Button>
        </div>
      )}
    </SettingsCard>
  );
}

function nextLabel(iso: string) {
  const t = new Date(iso).getTime();
  if (t <= Date.now()) return "Due now";
  return new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

/** Proposals waiting for a superadmin, with the diff and Apply or Dismiss. */
function ReviewsCard({ canEdit, names, onDecided, nonce }: { canEdit: boolean; names: Record<string, string>; onDecided: () => void; nonce: number }) {
  const [reviews, setReviews] = useState<RefreshReview[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const load = useCallback(() => {
    refreshApi
      .reviews()
      .then((r) => {
        setReviews(r.reviews);
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));
  }, []);
  useEffect(load, [load, nonce]);

  const decide = async (rv: RefreshReview, apply: boolean) => {
    setBusy(rv.id);
    try {
      if (apply) await refreshApi.apply(rv.id);
      else await refreshApi.dismiss(rv.id);
      toast.success(apply ? `Applied the change to ${targetName(rv.slug, names)}.` : "Dismissed.");
      load();
      onDecided();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  if (error) return <SectionError what="the reviews" message={error} onRetry={load} />;
  if (!reviews) return <Skeleton className="h-24 rounded-3xl" />;
  if (reviews.length === 0) return null;
  return (
    <SettingsCard title={`Waiting for review (${reviews.length})`}>
      {reviews.map((rv) => (
        <div key={rv.id} className="flex flex-col gap-3 px-4 py-4 sm:px-5">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="text-body-medium text-text-primary">
                {rv.slug === PRICES_TARGET ? (
                  targetName(rv.slug)
                ) : (
                  <RouterLink
                    to={`/providers/${encodeURIComponent(rv.slug)}`}
                    className="rounded-sm underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                  >
                    {targetName(rv.slug, names)}
                  </RouterLink>
                )}
              </div>
              <div className="text-caption-1-regular text-text-secondary">
                Found {relTime(rv.created_at)} by run {rv.run_id}
                {rv.slug !== PRICES_TARGET && <> against v{rv.base_version}</>}. {rv.reason}
              </div>
            </div>
            {canEdit && (
              <div className="flex shrink-0 gap-2">
                <Button variant="ghost" size="small" leadingIcon={RiCloseLine} disabled={busy !== null} onClick={() => void decide(rv, false)}>
                  Dismiss
                </Button>
                <Button
                  variant="primary"
                  size="small"
                  leadingIcon={busy === rv.id ? ButtonSpinner : RiCheckLine}
                  disabled={busy !== null}
                  onClick={() => void decide(rv, true)}
                >
                  Apply
                </Button>
              </div>
            )}
          </div>
          {rv.summary && <p className="text-body-2-regular text-text-secondary">{rv.summary}</p>}
          <RefreshDiff diff={rv.diff} />
        </div>
      ))}
    </SettingsCard>
  );
}

function RunItems({ id, names }: { id: number; names: Record<string, string> }) {
  const [data, setData] = useState<{ run: RefreshRun; items: RefreshItem[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    refreshApi
      .run(id)
      .then(setData)
      .catch((e) => setError(errorMessage(e)));
  }, [id]);
  if (error) return <p className="text-caption-1-regular text-text-error-primary">{error}</p>;
  if (!data) return <Skeleton className="h-16 rounded-xl" />;
  return (
    <div className="flex flex-col gap-3 pb-2">
      {data.run.stop_reason && <p className="text-caption-1-regular text-text-secondary">{data.run.stop_reason}</p>}
      <ul className="divide-y divide-separator-border">
        {data.items.map((it) => (
          <li key={it.id} className="flex flex-col gap-1.5 py-2.5">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-body-2-medium text-text-primary">{targetName(it.slug, names)}</span>
              <Chip variant="caption" color={ITEM_CHIP[it.status].color}>
                {ITEM_CHIP[it.status].label}
              </Chip>
              {it.version ? <span className="text-caption-1-regular text-text-secondary">v{it.version}</span> : null}
              <span className="ms-auto text-caption-1-regular text-text-tertiary tabular-nums">{usd(it.cost_usd)}</span>
            </div>
            {it.detail && <p className="text-caption-1-regular text-text-secondary wrap-anywhere">{it.detail}</p>}
            {(it.status === "applied" || it.status === "review") && <RefreshDiff diff={it.diff} />}
          </li>
        ))}
      </ul>
      {data.run.skipped.length > 0 && (
        <div>
          <div className="text-caption-1-semibold text-text-tertiary">Skipped</div>
          <ul className="mt-1 flex flex-col gap-0.5">
            {data.run.skipped.map((s) => (
              <li key={s.slug} className="text-caption-1-regular text-text-secondary">
                {targetName(s.slug, names)}: {s.reason}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

/** Recent runs; open one for its per provider outcome and cost. */
function HistoryCard({ names, nonce }: { names: Record<string, string>; nonce: number }) {
  const runs = useLoad(refreshApi.runs);
  const reload = runs.reload;
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    reload();
  }, [nonce, reload]);
  if (runs.error) return <SectionError what="the refresh history" message={runs.error} onRetry={runs.reload} />;
  if (!runs.data)
    return (
      <SettingsCard>
        <RowsSkeleton rows={2} />
      </SettingsCard>
    );
  const list = runs.data.runs;
  return (
    <SettingsCard title="Recent runs">
      {list.length === 0 ? (
        <p className="px-4 py-4 text-body-2-regular text-text-secondary sm:px-5">No runs yet.</p>
      ) : (
        <div className="px-4 sm:px-5">
          <Accordion>
            {list.map((r) => (
              <AccordionItem
                key={r.id}
                id={String(r.id)}
                title={
                  <span className="flex flex-wrap items-baseline gap-x-2 text-body-medium">
                    <span title={dateTime(r.started_at)}>{relTime(r.started_at)}</span>
                    <span className="text-caption-1-regular text-text-tertiary">
                      {RUN_TRIGGER[r.trigger]}
                      {r.started_by && r.started_by !== "system" ? ` by ${r.started_by}` : ""}
                    </span>
                  </span>
                }
                description={
                  <span className="text-body-2-regular tabular-nums">
                    {r.status === "running"
                      ? `Running: ${r.providers - r.pending} of ${r.providers}`
                      : `${r.checked} checked · ${r.changed} applied · ${r.reviews} in review · ${r.failed} failed · ${usd(r.cost_usd)}`}
                    {r.status === "stopped" ? " · stopped" : ""}
                  </span>
                }
              >
                <RunItems id={r.id} names={names} />
              </AccordionItem>
            ))}
          </Accordion>
        </div>
      )}
    </SettingsCard>
  );
}

/**
 * Provider refresh: when and how providers' offers (and Claude's own model
 * prices) are re-read, what it costs, what changed and what waits for review.
 */
export function RefreshSection({ isAdmin, onDirty }: { isAdmin: boolean; onDirty: (d: boolean) => void }) {
  const state = useLoad(refreshApi.state, isAdmin);
  const providers = useProviders((s) => s.providers);
  const names = useMemo(() => Object.fromEntries(providers.map((p) => [p.slug, p.name])), [providers]);
  const [nonce, setNonce] = useState(0);
  const set = state.set;
  const refreshAll = useCallback(() => {
    refreshApi
      .state()
      .then(set)
      .catch(() => undefined);
    setNonce((n) => n + 1);
  }, [set]);
  const running = !!state.data?.running;
  usePoll(refreshAll, 3000, running);
  // When a run ends, reload the history and reviews once more.
  const wasRunning = useRef(false);
  useEffect(() => {
    if (wasRunning.current && !running) setNonce((n) => n + 1);
    wasRunning.current = running;
  }, [running]);

  const description =
    "Re-reads the pricing pages of the providers you use with the refresh model, on a schedule you set, and updates their offers, free tiers, prices and limits. Never auth, base URLs or rules.";

  if (!isAdmin) {
    return (
      <SettingsSection id="provider-refresh" title="Provider refresh" description={description}>
        <SettingsCard>
          <div className="px-4 py-4 sm:px-5">
            <ReadOnlyNote>Admins and superadmins see and manage the provider refresh.</ReadOnlyNote>
          </div>
        </SettingsCard>
      </SettingsSection>
    );
  }
  return (
    <SettingsSection id="provider-refresh" title="Provider refresh" description={description}>
      {state.error ? (
        <SectionError what="the provider refresh" message={state.error} onRetry={state.reload} />
      ) : !state.data ? (
        <SettingsCard>
          <RowsSkeleton rows={3} />
        </SettingsCard>
      ) : (
        <>
          <StatusCard state={state.data} onStarted={refreshAll} />
          <ReviewsCard canEdit={state.data.can_edit} names={names} onDecided={refreshAll} nonce={nonce} />
          <SettingsForm state={state.data} onSaved={(s) => state.set(s)} onDirty={onDirty} />
          <HistoryCard names={names} nonce={nonce} />
        </>
      )}
    </SettingsSection>
  );
}
