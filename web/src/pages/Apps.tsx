import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useTitle } from "../hooks/useTitle";
import { Boxes, Pencil, Plus, RefreshCw, TriangleAlert } from "lucide-react";
import { useApps } from "../stores/apps";
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
import { Badge, Button, Empty, Field, IconButton, Input, PageHeader, PageSkeleton, Select, Textarea, Toggle } from "../components/ui";

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
      description={initial ? undefined : "An app is anything that calls Cogito, such as a website, an agent or a script. Each gets its own token."}
      headerAction={<AiFillButton ai={ai} />}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="app-form" loading={busy}>
            {initial ? "Save" : "Create app"}
          </Button>
        </>
      }
    >
      <form id="app-form" onSubmit={submit} className="space-y-4" noValidate>
        <AiFillPanel ai={ai} />
        <Field label="Name" required error={nameErr} className={ai.flash("name")}>
          <Input
            value={form.name}
            onChange={(e) => {
              setForm({ ...form, name: e.target.value });
              if (nameErr) setNameErr(null);
            }}
            placeholder="marketing site"
            autoComplete="off"
          />
        </Field>
        <Field label="Description" className={ai.flash("description")}>
          <Textarea rows={2} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
        </Field>
        <Field label="Mode" hint={MODE_HELP[form.mode]} className={ai.flash("mode")}>
          <Select value={form.mode} onChange={(e) => setForm({ ...form, mode: e.target.value as AppMode })}>
            <option value="proxy">proxy</option>
            <option value="lease">lease</option>
            <option value="both">both</option>
          </Select>
        </Field>
        <div className={ai.flash("allowed_services")}>
          <div className="mb-1.5 text-[13px] font-medium text-ink">Allowed services</div>
          <MultiSelect
            options={services.map((s) => ({ value: s.slug, label: s.name, meta: s.slug }))}
            value={form.allowed_services}
            onChange={(v) => setForm({ ...form, allowed_services: v })}
            empty="No services yet. Add one from the catalog first."
          />
          <div className="mt-1 text-xs text-muted">
            {form.allowed_services.length ? `Limited to ${plural(form.allowed_services.length, "service")}.` : "Pick none to allow every service."}
          </div>
        </div>
        <fieldset className="space-y-2">
          <legend className="mb-1.5 text-[13px] font-medium text-ink">Limits</legend>
          <p className="text-xs leading-relaxed text-muted">Leave a field empty for no limit. Calls over a limit are refused until it resets.</p>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {LIMIT_KEYS.map((k) => {
              const money = k.endsWith("_budget");
              return (
                <Field key={k} label={money ? `${LIMIT_LABEL[k]} (USD)` : LIMIT_LABEL[k]}>
                  <Input
                    type="number"
                    inputMode={money ? "decimal" : "numeric"}
                    min={0}
                    step={money ? "any" : 1}
                    value={limits[k]}
                    onChange={(e) => setLimits({ ...limits, [k]: e.target.value })}
                    placeholder="no limit"
                    className={money ? "font-mono" : undefined}
                  />
                </Field>
              );
            })}
          </div>
        </fieldset>
        {isSuper && (
          <Toggle
            checked={!!form.lease_hidden}
            onChange={(v) => setForm({ ...form, lease_hidden: v })}
            label="Lease hidden keys"
            description="Lets this app use hidden keys. Leases and some upstream responses reveal the plain key, so turn this on only for apps you trust. Rotating the token as an admin turns it off."
          />
        )}
      </form>
    </Modal>
  );
}

function TokenModal({ result, rotated, onClose }: { result: AppWithToken | null; rotated: boolean; onClose: () => void }) {
  return (
    <Modal
      open={!!result}
      onClose={onClose}
      title={rotated ? `New token for ${result?.app.name}` : `${result?.app.name} created`}
      footer={
        <Button variant="primary" onClick={onClose}>
          I have saved it
        </Button>
      }
    >
      {result && (
        <div className="space-y-3">
          <div className="flex items-start gap-2 rounded-xl bg-amber-50 px-3 py-2.5 text-xs text-amber-800">
            <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" />
            <span>
              Copy this token now. Cogito stores only a hash, so it cannot show the token again.
              {rotated && " The old token has stopped working."}
            </span>
          </div>
          <SecretBox value={result.token} />
          <p className="wrap-anywhere font-mono text-[11px] text-muted">Send it as Authorization: Bearer {result.app.token_prefix}...</p>
        </div>
      )}
    </Modal>
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
  return (
    <div className="mt-1.5 flex flex-col gap-1.5">
      {hit && (
        <div>
          <Badge tone="red" title={dateTime(hit.reset_at)}>
            {hitLabel} limit reached, resets {relTime(hit.reset_at, now)}
          </Badge>
        </div>
      )}
      <div className="flex flex-wrap gap-x-4 gap-y-0.5 font-mono text-xs text-muted">
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
    </div>
  );
}

export function AppsPage() {
  useTitle("Apps");
  const { apps, loaded, error, fetch, update, remove, rotate } = useApps();
  const { services, loaded: servicesLoaded, fetch: fetchServices } = useServices();
  const role = useAuth((s) => s.user?.role);
  const manage = can.manage(role);
  const now = useNow(30000);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<App | null>(null);
  const [token, setToken] = useState<{ result: AppWithToken; rotated: boolean } | null>(null);
  const [busyId, setBusyId] = useState<number | null>(null);

  useEffect(() => {
    fetch().catch(() => undefined);
    if (!servicesLoaded) fetchServices().catch(() => undefined);
  }, [fetch, fetchServices, servicesLoaded]);

  const nameBySlug = useMemo(() => Object.fromEntries(services.map((s) => [s.slug, s.name])), [services]);
  const sorted = useMemo(() => [...apps].sort((a, b) => a.name.localeCompare(b.name)), [apps]);

  const doRotate = async (a: App) => {
    setBusyId(a.id);
    try {
      const res = await rotate(a.id);
      setToken({ result: res, rotated: true });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusyId(null);
    }
  };

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

  return (
    <div>
      <PageHeader
        title="Apps"
        meta={`${plural(apps.length, "app")} · ${active} active`}
        actions={
          manage && (
            <Button
              variant="primary"
              icon={<Plus className="h-4 w-4" />}
              onClick={() => {
                setEditing(null);
                setFormOpen(true);
              }}
            >
              Create app
            </Button>
          )
        }
      />

      {error && apps.length === 0 ? (
        <Empty title="Apps are not available" body={error} />
      ) : sorted.length === 0 ? (
        <Empty
          icon={<Boxes className="h-5 w-5" />}
          title="No apps yet"
          body={
            manage
              ? "Create one app per website, agent or script. Each gets its own token, so you can pause or rotate it without touching the others."
              : "No apps yet. Ask an admin to create one."
          }
          action={
            manage ? (
              <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setFormOpen(true)}>
                Create your first app
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="space-y-2.5">
          {sorted.map((a) => (
            <li key={a.id} className="rounded-card bg-surface p-4 shadow-card sm:px-5">
              <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-[15px] font-medium text-ink">{a.name}</span>
                    <Badge tone={a.mode === "lease" ? "blue" : a.mode === "proxy" ? "violet" : "neutral"}>{a.mode}</Badge>
                    {!a.active && <Badge>paused</Badge>}
                  </div>
                  {a.description && <div className="mt-0.5 text-[13px] text-muted">{a.description}</div>}
                  <div className="wrap-anywhere mt-1.5 font-mono text-xs text-muted">
                    {a.token_prefix}... · {a.allowed_services?.length ? a.allowed_services.map((s) => nameBySlug[s] ?? s).join(", ") : "all services"} ·{" "}
                    <span title={dateTime(a.last_used_at)}>used {relTime(a.last_used_at, now)}</span>
                  </div>
                  <AppUsageLine app={a} now={now} />
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-1">
                  <Toggle checked={a.active} onChange={(v) => void toggleActive(a, v)} disabled={!manage} ariaLabel={a.active ? `${a.name} is active` : `${a.name} is paused`} />
                  {manage && (
                    <>
                      <span className="mx-1 h-5 w-px bg-line" />
                      <IconButton label="Rotate token" onClick={() => void doRotate(a)} disabled={busyId === a.id}>
                        <RefreshCw className="h-4 w-4" />
                      </IconButton>
                      <IconButton
                        label="Edit app"
                        onClick={() => {
                          setEditing(a);
                          setFormOpen(true);
                        }}
                      >
                        <Pencil className="h-4 w-4" />
                      </IconButton>
                      <ConfirmButton
                        iconOnly
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
            </li>
          ))}
        </ul>
      )}

      <AppFormModal
        open={formOpen}
        initial={editing}
        onClose={() => setFormOpen(false)}
        onCreated={(r) => setToken({ result: r, rotated: false })}
      />
      <TokenModal result={token?.result ?? null} rotated={token?.rotated ?? false} onClose={() => setToken(null)} />
    </div>
  );
}
