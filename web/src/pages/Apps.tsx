import { useEffect, useMemo, useState, type ComponentType, type FormEvent } from "react";
import { RiAddLine, RiPencilLine, RiPlugLine } from "@remixicon/react";
import { AcnAppToken } from "@/components/foundations/icons/acn";
import { Focusable } from "react-aria-components";
import { useTitle } from "../hooks/useTitle";
import { useApps } from "../stores/apps";
import { enabledCount, useMcp } from "../stores/mcp";
import { AppMcpPanel } from "./AppMcpPanel";
import { AppTokensPanel } from "./AppTokensPanel";
import { useServices } from "../stores/services";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { compact, dateTime, plural, relTime } from "../lib/format";
import type { App, AppInput, AppMode, AppWithToken } from "../lib/types";
import { ops, usd, type AppLimits, type AppUsageReport } from "../lib/ops";
import { useNow } from "../hooks/useNow";
import { Modal } from "../components/Modal";
import { MultiSelect } from "../components/MultiSelect";
import { SecretBox } from "../components/CopyButton";
import { ConfirmButton } from "../components/ConfirmButton";
import { AiFillButton, AiFillPanel, useAiFill } from "../components/AiFill";
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Input } from "@/components/base/input/input";
import { Label } from "@/components/base/input/label";
import { HintText } from "@/components/base/input/hint-text";
import { Textarea } from "@/components/base/textarea/textarea";
import { Select, SelectItem } from "@/components/base/select/select";
import { Switch } from "@/components/base/switch/switch";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { Meter, type MeterTone } from "@/components/base/meter/meter";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { cx } from "@/utils/cx";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout, EmptyState, PageHeader, PageSkeleton } from "@/components/acn";
import { TokenCardArt } from "@/components/foundations/illustrations";

type IconComponent = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;

/** Icon only action with a hover and focus tooltip naming it. */
function TipButton({ icon, label, onClick, disabled }: { icon: IconComponent; label: string; onClick: () => void; disabled?: boolean }) {
  return (
    <TooltipTrigger delay={300}>
      <Focusable>
        {/* aria-disabled rather than disabled, so focus stays put and returns here after a dialog it opened. */}
        <IconButton icon={icon} size="small" className="pointer-coarse:size-11" aria-label={label} onClick={disabled ? undefined : onClick} aria-disabled={disabled || undefined} />
      </Focusable>
      <Tooltip>{label}</Tooltip>
    </TooltipTrigger>
  );
}

const MODE_COLOR: Record<AppMode, ChipColor> = { proxy: "purple", lease: "blue", both: "neutral" };

const MODE_HELP: Record<AppMode, string> = {
  proxy: "Calls go through /proxy. The app never sees a raw key.",
  lease: "The app leases raw keys and calls the provider itself.",
  both: "Allows both proxy calls and leases.",
};

const blank: AppInput = { name: "", description: "", mode: "proxy", allowed_services: [] };

const LIMIT_KEYS = ["daily_requests", "monthly_requests", "daily_budget", "monthly_budget"] as const;
type LimitKey = (typeof LIMIT_KEYS)[number];
/** Raw text per limit; empty means no limit. */
type LimitDraft = Record<LimitKey, string>;

const LIMIT_LABEL: Record<LimitKey, string> = {
  daily_requests: "Daily requests",
  monthly_requests: "Monthly requests",
  daily_budget: "Daily budget",
  monthly_budget: "Monthly budget",
};

const NO_LIMITS: AppLimits = { daily_requests: null, monthly_requests: null, daily_budget: null, monthly_budget: null };
const blankLimits: LimitDraft = { daily_requests: "", monthly_requests: "", daily_budget: "", monthly_budget: "" };

function limitDraftOf(l: AppLimits | null | undefined): LimitDraft {
  const out = { ...blankLimits };
  for (const k of LIMIT_KEYS) out[k] = l?.[k] === null || l?.[k] === undefined ? "" : String(l[k]);
  return out;
}

/** Parses the draft; returns an error message when a value is not a valid number. */
function parseLimits(d: LimitDraft): AppLimits | string {
  const out: AppLimits = { ...NO_LIMITS };
  for (const k of LIMIT_KEYS) {
    const t = d[k].trim();
    if (t === "") continue;
    const n = Number(t);
    const whole = k.endsWith("_requests");
    if (!Number.isFinite(n) || n < 0 || (whole && !Number.isInteger(n))) {
      return `${LIMIT_LABEL[k]} must be ${whole ? "a whole number" : "an amount"}, 0 or more, or empty for no limit.`;
    }
    out[k] = n;
  }
  return out;
}

const sameLimits = (a: AppLimits, b: AppLimits) => LIMIT_KEYS.every((k) => a[k] === b[k]);

function AppFormModal({
  open,
  initial,
  onClose,
  onCreated,
}: {
  open: boolean;
  initial: App | null;
  onClose: () => void;
  onCreated: (r: AppWithToken) => void;
}) {
  const { create, update } = useApps();
  const services = useServices((s) => s.services);
  const isSuper = can.hidden(useAuth((s) => s.user?.role));
  const [form, setForm] = useState<AppInput>(blank);
  const [busy, setBusy] = useState(false);
  const [nameErr, setNameErr] = useState<string | null>(null);
  const [limits, setLimits] = useState<LimitDraft>(blankLimits);
  // What the server holds now; null until known, so an unknown value is never overwritten by accident.
  const [savedLimits, setSavedLimits] = useState<AppLimits | null>(null);
  const ai = useAiFill(
    "app",
    () => form,
    (fields) =>
      setForm((f) => {
        const next = { ...f };
        if (typeof fields.name === "string") next.name = fields.name;
        if (typeof fields.description === "string") next.description = fields.description;
        if (fields.mode === "proxy" || fields.mode === "lease" || fields.mode === "both") next.mode = fields.mode;
        if (Array.isArray(fields.allowed_services)) {
          const known = new Set(services.map((s) => s.slug));
          next.allowed_services = fields.allowed_services.map(String).filter((x) => known.has(x));
        }
        return next;
      }),
  );

  useEffect(() => {
    if (!open) return;
    setNameErr(null);
    ai.setOpen(false);
    setForm(
      initial
        ? {
            name: initial.name,
            description: initial.description,
            mode: initial.mode,
            allowed_services: initial.allowed_services ?? [],
            lease_hidden: initial.lease_hidden,
          }
        : blank,
    );
    if (!initial) {
      setLimits(blankLimits);
      setSavedLimits(NO_LIMITS);
    } else if (initial.limits) {
      setLimits(limitDraftOf(initial.limits));
      setSavedLimits(initial.limits);
    } else {
      // The list may not carry limits; read them from the usage report instead.
      setLimits(blankLimits);
      setSavedLimits(null);
      let live = true;
      ops
        .appUsage(initial.id, 1)
        .then((r) => {
          if (!live) return;
          setLimits(limitDraftOf(r.limits));
          setSavedLimits(r.limits ?? NO_LIMITS);
        })
        .catch(() => undefined);
      return () => {
        live = false;
      };
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initial]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!form.name.trim()) {
      setNameErr("Give the app a name, such as marketing site.");
      return;
    }
    const parsed = parseLimits(limits);
    if (typeof parsed === "string") {
      toast.error(parsed);
      return;
    }
    setBusy(true);
    try {
      // Only a superadmin may send lease_hidden; the server refuses it from anyone else.
      const { lease_hidden, ...rest } = form;
      const base: AppInput = isSuper ? { ...rest, lease_hidden: !!lease_hidden } : rest;
      // Limits replace all four at once, so send them only when they changed from a known value.
      const changed = savedLimits ? !sameLimits(parsed, savedLimits) : LIMIT_KEYS.some((k) => parsed[k] !== null);
      const payload = (changed ? { ...base, limits: parsed } : base) as AppInput;
      if (initial) {
        await update(initial.id, { ...payload, name: form.name.trim() });
        toast.success(`Saved ${form.name}`);
        onClose();
      } else {
        const res = await create({ ...payload, name: form.name.trim() });
        onClose();
        onCreated(res);
      }
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={initial ? `Edit ${initial.name}` : "Create app"}
      description={initial ? undefined : "An app is anything that calls Acnologia, such as a website, an agent or a script. Each gets its own token."}
      headerAction={<AiFillButton ai={ai} />}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="app-form" disabled={busy} leadingIcon={busy ? ButtonSpinner : undefined}>
            {initial ? "Save" : "Create app"}
          </Button>
        </>
      }
    >
      <form id="app-form" onSubmit={submit} className="flex flex-col gap-5" noValidate>
        <AiFillPanel ai={ai} />
        <Input
          label="Name"
          isRequired
          isInvalid={!!nameErr}
          errorMessage={nameErr}
          className={ai.flash("name")}
          value={form.name}
          onChange={(v) => {
            setForm({ ...form, name: v });
            if (nameErr) setNameErr(null);
          }}
          placeholder="marketing site"
          autoComplete="off"
        />
        <Textarea
          label="Description"
          className={ai.flash("description")}
          rows={2}
          autoResize
          value={form.description}
          onChange={(v) => setForm({ ...form, description: v })}
        />
        <div className={cx("flex flex-col gap-1", ai.flash("mode"))}>
          <Select
            label="Mode"
            selectedKey={form.mode}
            onSelectionChange={(k) => k != null && setForm({ ...form, mode: k as AppMode })}
            className="w-full"
            aria-describedby="app-mode-hint"
          >
            <SelectItem id="proxy">proxy</SelectItem>
            <SelectItem id="lease">lease</SelectItem>
            <SelectItem id="both">both</SelectItem>
          </Select>
          <HintText id="app-mode-hint">{MODE_HELP[form.mode]}</HintText>
        </div>
        <div className={cx("flex flex-col gap-1.5", ai.flash("allowed_services"))}>
          <Label id="app-services-label">Allowed services</Label>
          <div role="group" aria-labelledby="app-services-label">
            <MultiSelect
              options={services.map((s) => ({ value: s.slug, label: s.name, meta: s.slug }))}
              value={form.allowed_services}
              onChange={(v) => setForm({ ...form, allowed_services: v })}
              empty="No services yet. Add one from the catalog first."
              searchLabel="services"
            />
          </div>
          <HintText>
            {form.allowed_services.length ? `Limited to ${plural(form.allowed_services.length, "service")}.` : "Leave all unselected to allow every service, including ones you add later."}
          </HintText>
        </div>
        <fieldset className="flex flex-col gap-3">
          <legend className="mb-1 text-body-medium text-text-primary">Limits</legend>
          <p className="text-caption-1-regular text-text-secondary">Leave a field empty for no limit. Calls over a limit are refused until it resets.</p>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {LIMIT_KEYS.map((k) => {
              const money = k.endsWith("_budget");
              return (
                <Input
                  key={k}
                  label={money ? `${LIMIT_LABEL[k]} (USD)` : LIMIT_LABEL[k]}
                  inputMode={money ? "decimal" : "numeric"}
                  value={limits[k]}
                  onChange={(v) => setLimits({ ...limits, [k]: v })}
                  placeholder="no limit"
                  inputClassName="font-mono tabular-nums"
                />
              );
            })}
          </div>
        </fieldset>
        {isSuper && (
          <div className="flex items-start justify-between gap-4 rounded-2xl bg-background-secondary-default p-4">
            <div className="min-w-0">
              <div id="lease-hidden-label" className="text-body-medium text-text-primary">
                Lease hidden keys
              </div>
              <p id="lease-hidden-hint" className="mt-0.5 text-caption-1-regular text-text-secondary">
                Lets this app use hidden keys. Leases and some upstream responses reveal the plain key, so turn this on only for apps you trust. A new token made by an admin turns it off.
              </p>
            </div>
            <Switch
              isSelected={!!form.lease_hidden}
              onChange={(v) => setForm({ ...form, lease_hidden: v })}
              aria-labelledby="lease-hidden-label"
              aria-describedby="lease-hidden-hint"
            />
          </div>
        )}
      </form>
    </Modal>
  );
}

function TokenModal({ result, onClose }: { result: AppWithToken | null; onClose: () => void }) {
  return (
    <Modal
      open={!!result}
      onClose={onClose}
      title={`${result?.app.name} created`}
      footer={
        <Button variant="primary" onClick={onClose}>
          I have saved it
        </Button>
      }
    >
      {result && (
        <div className="flex flex-col gap-3">
          <Callout tone="warning">
            Copy this token now. You will not see it again: Acnologia stores only a hash. You can add more tokens later from Tokens.
          </Callout>
          <SecretBox value={result.token} />
          <div className="font-mono text-caption-1-regular text-text-secondary wrap-anywhere text-text-tertiary">Send it as Authorization: Bearer {result.app.token_prefix}...</div>
        </div>
      )}
    </Modal>
  );
}

/** How full a limit is: amber from 80%, red once it is reached. */
function limitTone(pct: number): MeterTone {
  return pct >= 100 ? "error" : pct >= 80 ? "warning" : "accent";
}

/** One capped limit as a small labelled meter. The usage line above carries the exact numbers. */
function LimitMeter({ label, used, limit, text }: { label: string; used: number; limit: number; text: string }) {
  // A limit of 0 refuses every call, so it reads as full.
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : 100;
  return (
    <div className="min-w-0">
      <div className="flex items-baseline justify-between gap-3 font-mono text-caption-1-regular text-text-secondary tabular-nums">
        <span className="truncate">{label}</span>
        <span className="shrink-0">{Math.floor(pct)}%</span>
      </div>
      <Meter aria-label={`${label} used`} value={pct} valueLabel={text} tone={limitTone(pct)} size="sm" className="mt-1" />
    </div>
  );
}

/** Today's and this month's requests and spend for one app, loaded when its row renders. */
function AppUsageLine({ app, now }: { app: App; now: number }) {
  const [r, setR] = useState<AppUsageReport | null>(null);
  useEffect(() => {
    let live = true;
    ops
      .appUsage(app.id, 1)
      .then((x) => live && setR(x))
      .catch(() => undefined);
    return () => {
      live = false;
    };
    // Refetch when the app object is replaced, such as after an edit.
  }, [app]);
  if (!r) return null;
  const { usage: u, limits: l, limit_hit: hit } = r;
  const of = (n: number | null | undefined, fmt: (v: number) => string) => (n === null || n === undefined ? "" : ` of ${fmt(n)}`);
  const hitKey = hit?.limit as LimitKey | undefined;
  const hitLabel = hitKey && LIMIT_LABEL[hitKey] ? LIMIT_LABEL[hitKey] : "Usage";
  const used: Record<LimitKey, { n: number; fmt: (v: number) => string }> = {
    daily_requests: { n: u.today_requests, fmt: compact },
    monthly_requests: { n: u.month_requests, fmt: compact },
    daily_budget: { n: u.today_cost, fmt: (v) => usd(v) },
    monthly_budget: { n: u.month_cost, fmt: (v) => usd(v) },
  };
  const capped = LIMIT_KEYS.filter((k) => l?.[k] !== null && l?.[k] !== undefined);
  return (
    <div className="mt-2 flex flex-col gap-1.5">
      {hit && (
        <div>
          <Chip variant="caption" color="rose" title={dateTime(hit.reset_at)}>
            {hitLabel} limit reached, resets {relTime(hit.reset_at, now)}
          </Chip>
        </div>
      )}
      <div className="flex flex-wrap gap-x-4 gap-y-0.5 font-mono text-caption-1-regular text-text-secondary tabular-nums">
        <span>
          today {compact(u.today_requests)}
          {of(l?.daily_requests, compact)} req, {usd(u.today_cost)}
          {of(l?.daily_budget, (v) => usd(v))}
        </span>
        <span>
          month {compact(u.month_requests)}
          {of(l?.monthly_requests, compact)} req, {usd(u.month_cost)}
          {of(l?.monthly_budget, (v) => usd(v))}
        </span>
      </div>
      {capped.length > 0 && (
        <div className="mt-1 grid max-w-md grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-2">
          {capped.map((k) => {
            const limit = l[k] as number;
            const { n, fmt } = used[k];
            return <LimitMeter key={k} label={LIMIT_LABEL[k]} used={n} limit={limit} text={`${fmt(n)} of ${fmt(limit)}`} />;
          })}
        </div>
      )}
    </div>
  );
}

/** "MCP: 11 tools" once the app's MCP view has loaded. Shares the panel's cache. */
function McpSummary({ app }: { app: App }) {
  const view = useMcp((s) => s.views[app.id]);
  const load = useMcp((s) => s.load);
  useEffect(() => {
    void load(app.id);
  }, [app.id, load]);
  if (!view) return null;
  return <> · MCP: {plural(enabledCount(view), "tool")}</>;
}

export function AppsPage() {
  useTitle("Apps");
  const { apps, loaded, error, fetch, update, remove } = useApps();
  const { services, loaded: servicesLoaded, fetch: fetchServices } = useServices();
  const role = useAuth((s) => s.user?.role);
  const manage = can.manage(role);
  const now = useNow(30000);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<App | null>(null);
  const [token, setToken] = useState<AppWithToken | null>(null);
  const [mcpApp, setMcpApp] = useState<App | null>(null);
  const [tokensApp, setTokensApp] = useState<App | null>(null);

  useEffect(() => {
    fetch().catch(() => undefined);
    if (!servicesLoaded) fetchServices().catch(() => undefined);
  }, [fetch, fetchServices, servicesLoaded]);

  const nameBySlug = useMemo(() => Object.fromEntries(services.map((s) => [s.slug, s.name])), [services]);
  const sorted = useMemo(() => [...apps].sort((a, b) => a.name.localeCompare(b.name)), [apps]);

  const toggleActive = async (a: App, v: boolean) => {
    try {
      await update(a.id, { active: v });
      toast.success(v ? `${a.name} is active again` : `${a.name} is paused. Its token stops working until you turn it back on.`);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  if (!loaded) return <PageSkeleton variant="list" />;

  const active = apps.filter((a) => a.active).length;
  const openCreate = () => {
    setEditing(null);
    setFormOpen(true);
  };

  return (
    <div>
      <PageHeader
        title="Apps"
        meta={`${plural(apps.length, "app")} · ${active} active`}
        actions={
          manage && (
            <Button variant="primary" leadingIcon={RiAddLine} onClick={openCreate}>
              Create app
            </Button>
          )
        }
      />

      {error && apps.length === 0 ? (
        <EmptyState title="Apps are not available" body={error} />
      ) : sorted.length === 0 ? (
        <EmptyState
          art={TokenCardArt}
          icon={AcnAppToken}
          title="No apps yet"
          body={
            manage
              ? "Create one app per website, agent or script. Each gets its own tokens, so you can pause an app or revoke a token without touching the others."
              : "No apps yet. Ask an admin to create one."
          }
          action={
            manage ? (
              <Button variant="primary" leadingIcon={RiAddLine} onClick={openCreate}>
                Create your first app
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="flex flex-col gap-3">
          {sorted.map((a) => (
            <li key={a.id} className="rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:px-5">
              <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className={cx("wrap-anywhere min-w-0 text-headline-medium", a.active ? "text-text-primary" : "text-text-secondary")}>{a.name}</span>
                    <Chip variant="caption" color={MODE_COLOR[a.mode]} className="font-mono">
                      {a.mode}
                    </Chip>
                    {!a.active && (
                      <Chip variant="caption" color="neutral">
                        paused
                      </Chip>
                    )}
                  </div>
                  {a.description && <div className="wrap-anywhere mt-0.5 text-body-2-regular text-text-secondary">{a.description}</div>}
                  <div className="font-mono text-caption-1-regular text-text-secondary wrap-anywhere mt-1.5">
                    {plural(a.token_count ?? 0, "active token")} · {a.allowed_services?.length ? a.allowed_services.map((s) => nameBySlug[s] ?? s).join(", ") : "all services"} ·{" "}
                    <span title={dateTime(a.last_used_at)}>used {relTime(a.last_used_at, now)}</span>
                    <McpSummary app={a} />
                  </div>
                  <AppUsageLine app={a} now={now} />
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-separator-border pt-3 sm:border-0 sm:pt-0">
                  <Button size="small" variant="ghost" leadingIcon={RiPlugLine} onClick={() => setMcpApp(a)} aria-label={`MCP settings for ${a.name}`}>
                    MCP
                  </Button>
                  <Button size="small" variant="ghost" leadingIcon={AcnAppToken} onClick={() => setTokensApp(a)} aria-label={`Tokens for ${a.name}`}>
                    Tokens
                  </Button>
                  {/* Kept together so a narrow row wraps as a group, not one icon at a time. */}
                  <div className="flex items-center gap-1.5">
                    <Switch
                      size="sm"
                      isSelected={a.active}
                      onChange={(v) => void toggleActive(a, v)}
                      isDisabled={!manage}
                      aria-label={a.active ? `${a.name} is active` : `${a.name} is paused`}
                      className="min-h-8 pe-1"
                    >
                      <span className="text-body-2-regular text-text-secondary">{a.active ? "Active" : "Paused"}</span>
                    </Switch>
                    {manage && (
                      <>
                        <span aria-hidden className="mx-1 h-5 w-px bg-separator-border" />
                        <TipButton
                          icon={RiPencilLine}
                          label="Edit app"
                          onClick={() => {
                            setEditing(a);
                            setFormOpen(true);
                          }}
                        />
                        <ConfirmButton
                          iconOnly
                          variant="outline"
                          label="Delete app"
                          confirmLabel="Delete"
                          onConfirm={async () => {
                            try {
                              await remove(a.id);
                              toast.success(`Deleted ${a.name}`);
                            } catch (e) {
                              toast.error(errorMessage(e));
                            }
                          }}
                        />
                      </>
                    )}
                  </div>
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}

      <AppFormModal
        open={formOpen}
        initial={editing}
        onClose={() => setFormOpen(false)}
        onCreated={(r) => setToken(r)}
      />
      <AppMcpPanel app={mcpApp} canEdit={manage} onClose={() => setMcpApp(null)} />
      <AppTokensPanel app={tokensApp} canEdit={manage} onClose={() => setTokensApp(null)} />
      <TokenModal result={token} onClose={() => setToken(null)} />
    </div>
  );
}
