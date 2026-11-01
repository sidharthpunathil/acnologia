import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Eraser, Pencil, RotateCcw, TriangleAlert } from "lucide-react";
import { useAuth } from "../../stores/auth";
import { can } from "../../lib/perms";
import type { Service, ServiceInput, Strategy } from "../../lib/types";
import { useServices } from "../../stores/services";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { STRATEGY_HELP, countdown, plural, relTime } from "../../lib/format";
import { ops, usd, type BreakerReport, type EgressProxy } from "../../lib/ops";
import { Modal } from "../../components/Modal";
import { ConcurrencyFields, ServiceForm, StrategySelect, validateService, type ServiceErrors } from "../../components/ServiceForm";
import { ConfirmButton } from "../../components/ConfirmButton";
import { LogoEditor } from "../../components/LogoEditor";
import { AiFillButton, AiFillPanel, useAiFill } from "../../components/AiFill";
import { useProviders } from "../../stores/providers";
import { Badge, Button, Callout, Card, Field, Input, Select } from "../../components/ui";
import { applyServiceFields } from "../AddServiceModal";

function inputOf(s: Service): ServiceInput {
  const { slug, name, provider, category, description, base_url, auth_type, auth_name, strategy, cooldown_sec, max_attempts, icon } = s;
  return {
    slug,
    name,
    provider,
    category,
    description,
    base_url,
    auth_type,
    auth_name,
    strategy,
    cooldown_sec,
    max_attempts,
    icon,
    max_concurrency: s.max_concurrency ?? 0,
    queue_timeout_sec: s.queue_timeout_sec ?? 0,
  };
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5 py-2 sm:flex-row sm:gap-4">
      <dt className="font-mono text-[11px] uppercase tracking-wide text-faint sm:w-36 sm:shrink-0 sm:pt-0.5">{label}</dt>
      <dd className="wrap-anywhere min-w-0 text-[13px] text-ink">{value || "-"}</dd>
    </div>
  );
}

type Mode = "default" | "custom";

/** Proxy, response cache and cost overrides. Each override is null on the service when it follows the provider. */
function TrafficCard({ service, canManage, isSuper }: { service: Service; canManage: boolean; isSuper: boolean }) {
  const { update } = useServices();
  const provider = useProviders((s) => s.providers.find((p) => p.slug === service.provider));
  const specTtl = provider?.spec?.defaults?.cache_ttl_sec;
  const specCost = provider?.spec?.quota?.cost_per_request;
  const ttlDefault = specTtl === undefined ? "Provider default" : specTtl > 0 ? `Provider default, ${specTtl}s` : "Provider default, off";
  const costDefault = specCost === undefined ? "Provider default" : `Provider default, ${usd(specCost, 4)}`;

  const [proxies, setProxies] = useState<EgressProxy[] | null>(null);
  const [egress, setEgress] = useState(service.egress_id ?? 0);
  const [ttlMode, setTtlMode] = useState<Mode>(service.cache_ttl_sec == null ? "default" : "custom");
  const [ttl, setTtl] = useState(service.cache_ttl_sec == null ? "" : String(service.cache_ttl_sec));
  const [costMode, setCostMode] = useState<Mode>(service.cost_per_request == null ? "default" : "custom");
  const [cost, setCost] = useState(service.cost_per_request == null ? "" : String(service.cost_per_request));
  const [saving, setSaving] = useState(false);
  const [purging, setPurging] = useState(false);

  const reset = useCallback(() => {
    setEgress(service.egress_id ?? 0);
    setTtlMode(service.cache_ttl_sec == null ? "default" : "custom");
    setTtl(service.cache_ttl_sec == null ? "" : String(service.cache_ttl_sec));
    setCostMode(service.cost_per_request == null ? "default" : "custom");
    setCost(service.cost_per_request == null ? "" : String(service.cost_per_request));
  }, [service.egress_id, service.cache_ttl_sec, service.cost_per_request]);
  useEffect(reset, [reset]);

  useEffect(() => {
    let live = true;
    ops
      .listEgress()
      .then((r) => live && setProxies(r.proxies))
      .catch(() => live && setProxies([]));
    return () => {
      live = false;
    };
  }, []);

  const ttlValue = ttlMode === "default" ? null : ttl.trim() === "" ? NaN : Number(ttl);
  const costValue = costMode === "default" ? null : cost.trim() === "" ? NaN : Number(cost);
  const dirty =
    egress !== (service.egress_id ?? 0) || ttlValue !== (service.cache_ttl_sec ?? null) || costValue !== (service.cost_per_request ?? null);

  const save = async () => {
    if (ttlValue !== null && (!Number.isInteger(ttlValue) || ttlValue < 0)) {
      toast.error("Cache TTL must be a whole number of seconds, 0 or more.");
      return;
    }
    if (costValue !== null && (!Number.isFinite(costValue) || costValue < 0)) {
      toast.error("Cost per request must be 0 or more.");
      return;
    }
    const patch: Partial<ServiceInput> = {};
    if (egress !== (service.egress_id ?? 0)) patch.egress_id = egress;
    // -1 tells the server to drop the override and follow the provider again.
    if (ttlValue !== (service.cache_ttl_sec ?? null)) patch.cache_ttl_sec = ttlValue ?? -1;
    if (costValue !== (service.cost_per_request ?? null)) patch.cost_per_request = costValue ?? -1;
    setSaving(true);
    try {
      await update(service.id, patch);
      toast.success("Traffic settings saved");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const purge = async () => {
    setPurging(true);
    try {
      const r = await ops.purgeCache(service.id);
      toast.success(`Purged ${plural(r.purged, "cached response")}`);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setPurging(false);
    }
  };

  return (
    <Card className="p-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <h3 className="text-[15px] font-semibold text-ink">Traffic</h3>
          <p className="mt-0.5 text-[13px] text-muted">Which proxy requests leave through, how long responses are cached, and what each request costs.</p>
        </div>
        {canManage && (
          <Button size="sm" icon={<Eraser className="h-3.5 w-3.5" />} loading={purging} onClick={purge}>
            Purge cache
          </Button>
        )}
      </div>
      <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Field
          label="Egress proxy"
          hint={
            canManage && !isSuper
              ? "Changing it pauses hidden keys until a superadmin saves the service again."
              : "Keys can pick their own proxy too."
          }
        >
          <Select value={egress} disabled={!canManage} onChange={(e) => setEgress(Number(e.target.value))}>
            <option value={0}>Direct (no proxy)</option>
            {(proxies ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.region ? `${p.name} (${p.region})` : p.name}
              </option>
            ))}
            {egress > 0 && !proxies?.some((p) => p.id === egress) && <option value={egress}>{`Proxy #${egress}`}</option>}
          </Select>
        </Field>
        <Field label="Response cache" hint={ttlMode === "custom" ? "Seconds to reuse an identical successful response. 0 turns it off." : undefined}>
          <div className="flex gap-2">
            <Select value={ttlMode} disabled={!canManage} onChange={(e) => setTtlMode(e.target.value as Mode)} className="min-w-0 flex-1">
              <option value="default">{ttlDefault}</option>
              <option value="custom">Custom</option>
            </Select>
            {ttlMode === "custom" && (
              <Input
                type="number"
                inputMode="numeric"
                min={0}
                aria-label="Cache TTL in seconds"
                value={ttl}
                disabled={!canManage}
                onChange={(e) => setTtl(e.target.value)}
                placeholder="sec"
                className="w-24 shrink-0"
              />
            )}
          </div>
        </Field>
        <Field label="Cost per request" hint={costMode === "custom" ? "USD, used for spend totals and app budgets." : undefined}>
          <div className="flex gap-2">
            <Select value={costMode} disabled={!canManage} onChange={(e) => setCostMode(e.target.value as Mode)} className="min-w-0 flex-1">
              <option value="default">{costDefault}</option>
              <option value="custom">Custom</option>
            </Select>
            {costMode === "custom" && (
              <Input
                type="number"
                inputMode="decimal"
                min={0}
                step="any"
                aria-label="Cost per request in USD"
                value={cost}
                disabled={!canManage}
                onChange={(e) => setCost(e.target.value)}
                placeholder="0.002"
                className="w-24 shrink-0 font-mono"
              />
            )}
          </div>
        </Field>
      </div>
      {canManage && (
        <div className="mt-4 flex justify-end gap-2">
          {dirty && (
            <Button variant="ghost" onClick={reset}>
              Discard
            </Button>
          )}
          <Button variant="primary" disabled={!dirty} loading={saving} onClick={save}>
            Save traffic
          </Button>
        </div>
      )}
    </Card>
  );
}

const BREAKER_TONE = { closed: "green", open: "red", half_open: "amber" } as const;
const BREAKER_LABEL = { closed: "closed", open: "open", half_open: "half open" } as const;

/** Circuit breaker state, polled while the settings tab is open. */
function BreakerCard({ service, canManage }: { service: Service; canManage: boolean }) {
  const [report, setReport] = useState<BreakerReport | null>(null);
  const [retryAt, setRetryAt] = useState<string | null>(null);
  const [now, setNow] = useState(Date.now());
  const [resetting, setResetting] = useState(false);
  const [failed, setFailed] = useState(false);

  const apply = useCallback((r: BreakerReport) => {
    setReport(r);
    setFailed(false);
    setRetryAt(r.status.retry_after_sec > 0 ? new Date(Date.now() + r.status.retry_after_sec * 1000).toISOString() : null);
  }, []);

  const load = useCallback(() => {
    ops
      .breaker(service.id)
      .then(apply)
      .catch(() => setFailed(true));
  }, [service.id, apply]);

  useEffect(() => {
    load();
    const t = window.setInterval(load, 15000);
    return () => window.clearInterval(t);
  }, [load]);

  useEffect(() => {
    if (!retryAt) return;
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, [retryAt]);

  const resetBreaker = async () => {
    setResetting(true);
    try {
      apply(await ops.resetBreaker(service.id));
      toast.success("Circuit breaker closed");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setResetting(false);
    }
  };

  // Older servers have no breaker endpoint; hide the card rather than show an error.
  if (failed && !report) return null;
  const st = report?.status;
  const left = countdown(retryAt, now);
  const settings = !report
    ? "Pauses the service when the provider keeps failing."
    : report.threshold < 0
      ? "Off for this service."
      : `Opens after ${plural(report.threshold, "error")} in ${report.window_sec}s, then pauses ${report.reset_sec}s before trying again.`;

  return (
    <Card className="p-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-[15px] font-semibold text-ink">Circuit breaker</h3>
            {st && <Badge tone={BREAKER_TONE[st.state]}>{BREAKER_LABEL[st.state]}</Badge>}
          </div>
          <p className="mt-0.5 text-[13px] text-muted">{settings}</p>
        </div>
        {canManage && st && st.state !== "closed" && (
          <Button size="sm" icon={<RotateCcw className="h-3.5 w-3.5" />} loading={resetting} onClick={resetBreaker}>
            Reset
          </Button>
        )}
      </div>
      {st && (
        <dl className="mt-3 divide-y divide-line">
          <Row label="Recent failures" value={String(st.failures)} />
          {st.opened_at && <Row label="Opened" value={relTime(st.opened_at, now)} />}
          {st.state === "open" && <Row label="Retry in" value={left || "any moment"} />}
          {st.reason && <Row label="Reason" value={st.reason} />}
        </dl>
      )}
    </Card>
  );
}

export function ServiceSettings({ service, canManage }: { service: Service; canManage: boolean }) {
  const isSuper = can.hidden(useAuth((s) => s.user?.role));
  const { update, remove, setLogo, removeLogo } = useServices();
  const provider = useProviders((s) => s.providers.find((p) => p.slug === service.provider));
  const navigate = useNavigate();
  const [strategy, setStrategy] = useState<Strategy>(service.strategy);
  const [cooldown, setCooldown] = useState(service.cooldown_sec);
  const [attempts, setAttempts] = useState(service.max_attempts);
  const [maxConc, setMaxConc] = useState(service.max_concurrency ?? 0);
  const [queueSec, setQueueSec] = useState(service.queue_timeout_sec ?? 0);
  const [editErrors, setEditErrors] = useState<ServiceErrors>({});
  const ai = useAiFill(
    "service",
    () => editing ?? inputOf(service),
    (fields) => setEditing((f) => applyServiceFields(f ?? inputOf(service), fields)),
  );
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<ServiceInput | null>(null);
  const [savingEdit, setSavingEdit] = useState(false);

  useEffect(() => {
    setStrategy(service.strategy);
    setCooldown(service.cooldown_sec);
    setAttempts(service.max_attempts);
    setMaxConc(service.max_concurrency ?? 0);
    setQueueSec(service.queue_timeout_sec ?? 0);
  }, [service]);

  const dirty =
    strategy !== service.strategy ||
    cooldown !== service.cooldown_sec ||
    attempts !== service.max_attempts ||
    maxConc !== (service.max_concurrency ?? 0) ||
    queueSec !== (service.queue_timeout_sec ?? 0);

  const saveStrategy = async () => {
    setSaving(true);
    try {
      await update(service.id, {
        strategy,
        cooldown_sec: cooldown,
        max_attempts: attempts,
        max_concurrency: maxConc,
        queue_timeout_sec: queueSec,
      });
      toast.success("Rotation settings saved");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const saveEdit = async () => {
    if (!editing) return;
    const errs = validateService(editing);
    setEditErrors(errs);
    if (Object.keys(errs).length) return;
    setSavingEdit(true);
    try {
      const { slug: _slug, ...rest } = editing;
      await update(service.id, rest);
      toast.success("Service updated");
      setEditing(null);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSavingEdit(false);
    }
  };

  return (
    <div className="space-y-5">
      {isSuper && !service.target_trusted && (
        <Callout tone="amber" icon={<TriangleAlert className="h-4 w-4" />}>
          <div className="font-medium">Hidden keys are paused for this service</div>
          <div className="mt-0.5">
            Someone other than a superadmin set its base URL or auth, so Cogito will not send hidden keys to it. Open Edit, check the
            base URL, then save to confirm it.
          </div>
        </Callout>
      )}
      <Card className="p-5">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h3 className="text-[15px] font-semibold text-ink">Rotation and concurrency</h3>
            <p className="mt-0.5 text-[13px] text-muted">How Cogito picks the next key, how long a rate limited key rests, and how many requests one key can serve at once.</p>
          </div>
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Field label="Strategy" hint={STRATEGY_HELP[strategy]}>
            <StrategySelect value={strategy} onChange={setStrategy} disabled={!canManage} />
          </Field>
          <Field label="Cooldown (seconds)" hint="Doubles with each failure in a row, up to 1 hour.">
            <Input type="number" min={0} value={cooldown} disabled={!canManage} onChange={(e) => setCooldown(Number(e.target.value))} />
          </Field>
          <Field label="Max attempts" hint="How many keys the proxy tries before giving up.">
            <Input type="number" min={1} max={20} value={attempts} disabled={!canManage} onChange={(e) => setAttempts(Number(e.target.value))} />
          </Field>
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <ConcurrencyFields
            maxConcurrency={maxConc}
            queueSec={queueSec}
            disabled={!canManage}
            onChange={(p) => {
              if (p.max_concurrency !== undefined) setMaxConc(p.max_concurrency);
              if (p.queue_timeout_sec !== undefined) setQueueSec(p.queue_timeout_sec);
            }}
          />
        </div>
        {canManage && (
          <div className="mt-4 flex justify-end gap-2">
            {dirty && (
              <Button
                variant="ghost"
                onClick={() => {
                  setStrategy(service.strategy);
                  setCooldown(service.cooldown_sec);
                  setAttempts(service.max_attempts);
                  setMaxConc(service.max_concurrency ?? 0);
                  setQueueSec(service.queue_timeout_sec ?? 0);
                }}
              >
                Discard
              </Button>
            )}
            <Button variant="primary" disabled={!dirty} loading={saving} onClick={saveStrategy}>
              Save rotation
            </Button>
          </div>
        )}
      </Card>

      <TrafficCard service={service} canManage={canManage} isSuper={isSuper} />
      <BreakerCard service={service} canManage={canManage} />

      <Card className="p-5">
        <h3 className="text-[15px] font-semibold text-ink">Logo</h3>
        <p className="mb-4 mt-0.5 text-[13px] text-muted">Shows in the catalog and on this page.</p>
        <LogoEditor
          src={service.logo_url ?? provider?.logo_url}
          name={service.name}
          color={provider?.color}
          // Own logos are served from /api/logos/services/{id}; anything else is inherited.
          hasOwn={!!service.logo_url && service.logo_url.includes("/logos/services/")}
          disabled={!canManage}
          onUpload={(logo) => setLogo(service.id, logo)}
          onRemove={() => removeLogo(service.id)}
          inheritedNote={provider ? `Without its own logo, the service uses the provider logo.` : undefined}
        />
      </Card>

      <Card className="p-5">
        <div className="flex items-start justify-between gap-4">
          <h3 className="text-[15px] font-semibold text-ink">Details</h3>
          {canManage && (
            <Button
              size="sm"
              icon={<Pencil className="h-3.5 w-3.5" />}
              onClick={() => {
                setEditErrors({});
                setEditing(inputOf(service));
              }}
            >
              Edit
            </Button>
          )}
        </div>
        <dl className="mt-2 divide-y divide-line">
          <Row label="Slug" value={service.slug} />
          <Row label="Provider" value={provider ? `${provider.name} (${service.provider})` : service.provider} />
          <Row label="Base URL" value={service.base_url} />
          <Row label="Auth" value={`${service.auth_type}${service.auth_name ? ` · ${service.auth_name}` : ""}`} />
          <Row label="Category" value={service.category} />
          <Row label="Description" value={service.description} />
          <Row label="Concurrency" value={service.max_concurrency ? `${service.max_concurrency} per key, waits up to ${service.queue_timeout_sec}s for a free key` : "No limit"} />
        </dl>
      </Card>

      {canManage && (
        <Card className="flex flex-col gap-3 p-5 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h3 className="text-[15px] font-semibold text-ink">Delete service</h3>
            <p className="mt-0.5 text-[13px] text-muted">Removes the service and all its keys. Apps that call {service.slug} start failing right away.</p>
          </div>
          <ConfirmButton
            label="Delete service"
            confirmLabel="Delete forever"
            onConfirm={async () => {
              try {
                await remove(service.id);
                toast.success(`${service.name} deleted`);
                navigate("/");
              } catch (e) {
                toast.error(errorMessage(e));
              }
            }}
          />
        </Card>
      )}

      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        wide
        title={`Edit ${service.name}`}
        headerAction={<AiFillButton ai={ai} />}
        footer={
          <>
            <Button variant="ghost" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button variant="primary" loading={savingEdit} onClick={saveEdit}>
              Save
            </Button>
          </>
        }
      >
        {editing && (
          <>
            <AiFillPanel ai={ai} />
            <ServiceForm
              value={editing}
              errors={editErrors}
              flash={ai.flash}
              onChange={(v) => {
                setEditing(v);
                if (Object.keys(editErrors).length) setEditErrors(validateService(v));
              }}
              slugLocked
            />
          </>
        )}
      </Modal>
    </div>
  );
}
