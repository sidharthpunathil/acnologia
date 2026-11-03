import { useCallback, useEffect, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { RiDeleteBinLine, RiEraserLine, RiPencilLine, RiRestartLine } from "@remixicon/react";
import { AcnCircuitBreaker } from "@/components/foundations/icons/acn";
import { useAuth } from "../../stores/auth";
import { can } from "../../lib/perms";
import type { Service, ServiceInput, Strategy } from "../../lib/types";
import { useServices } from "../../stores/services";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { STRATEGY_HELP, STRATEGY_LABEL, countdown, plural, relTime } from "../../lib/format";
import { ops, usd, type BreakerReport, type EgressProxy } from "../../lib/ops";
import { STRATEGIES, ServiceForm, validateService, type ServiceErrors } from "../../components/ServiceForm";
import { LogoEditor } from "../../components/LogoEditor";
import { AiFillButton, AiFillPanel, useAiFill } from "../../components/AiFill";
import { useProviders } from "../../stores/providers";
import { Callout, Section } from "@/components/acn";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { AlertDialog, Modal, ModalBody, ModalFooter, ModalHeader } from "@/components/base/modal/modal";
import { NumberField } from "@/components/base/number-field/number-field";
import { Select, SelectItem } from "@/components/base/select/select";
import { RadioGroup } from "@/components/base/radio/radio";
import { RadioCard } from "@/components/base/radio/radio-card";
import { Label } from "@/components/base/input/label";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
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
    <div className="flex flex-col gap-0.5 border-b border-separator-border py-2.5 last:border-b-0 sm:flex-row sm:gap-4">
      <dt className="font-mono text-caption-2-semibold uppercase text-text-tertiary sm:w-36 sm:shrink-0 sm:pt-0.5">{label}</dt>
      <dd className="wrap-anywhere min-w-0 text-body-2-regular text-text-primary">{value || "-"}</dd>
    </div>
  );
}

/** Save and Discard for a settings card, shown to managers only. */
function SaveRow({ dirty, saving, onDiscard, onSave, label }: { dirty: boolean; saving: boolean; onDiscard: () => void; onSave: () => void; label: string }) {
  return (
    <div className="mt-5 flex justify-end gap-2">
      {dirty && (
        <Button variant="ghost" onClick={onDiscard} disabled={saving}>
          Discard
        </Button>
      )}
      <Button variant="primary" disabled={!dirty || saving} leadingIcon={saving ? ButtonSpinner : undefined} onClick={onSave}>
        {label}
      </Button>
    </div>
  );
}

/** A Select of default or custom, with the custom number beside it. */
function OverrideField({
  label,
  hint,
  mode,
  defaultLabel,
  disabled,
  onMode,
  children,
}: {
  label: string;
  hint?: string;
  mode: Mode;
  defaultLabel: string;
  disabled: boolean;
  onMode: (m: Mode) => void;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <div className="flex items-end gap-2">
        <Select label={label} selectedKey={mode} isDisabled={disabled} onSelectionChange={(k) => onMode(k as Mode)} className="min-w-0 flex-1">
          <SelectItem id="default">{defaultLabel}</SelectItem>
          <SelectItem id="custom">Custom</SelectItem>
        </Select>
        {mode === "custom" && children}
      </div>
      {hint && <p className="text-caption-1-regular text-text-secondary">{hint}</p>}
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
    if (egress !== (service.egress_id ?? 0)) {
      patch.egress_id = egress;
      // A superadmin picking the proxy here confirms where keys go.
      if (isSuper) patch.target_trusted = true;
    }
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

  const egressItems = [
    { id: "0", label: "Direct (no proxy)" },
    ...(proxies ?? []).map((p) => ({ id: String(p.id), label: p.region ? `${p.name} (${p.region})` : p.name })),
  ];
  if (egress > 0 && !proxies?.some((p) => p.id === egress)) egressItems.push({ id: String(egress), label: `Proxy #${egress}` });

  return (
    <Section
      title="Traffic"
      description="Which proxy requests leave through, how long responses are cached, and what each request costs."
      actions={
        canManage && (
          <Button variant="secondary" size="small" leadingIcon={purging ? ButtonSpinner : RiEraserLine} disabled={purging} onClick={purge}>
            Purge cache
          </Button>
        )
      }
    >
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div className="flex min-w-0 flex-col gap-1">
          <Select label="Egress proxy" selectedKey={String(egress)} isDisabled={!canManage} onSelectionChange={(k) => setEgress(Number(k))} className="w-full">
            {egressItems.map((o) => (
              <SelectItem key={o.id} id={o.id}>
                {o.label}
              </SelectItem>
            ))}
          </Select>
          <p className="text-caption-1-regular text-text-secondary">
            {canManage && !isSuper ? "Changing it pauses hidden keys until a superadmin saves the service again." : "Keys can pick their own proxy too."}
          </p>
        </div>
        <OverrideField
          label="Response cache"
          hint={ttlMode === "custom" ? "Seconds to reuse an identical successful response. 0 turns it off." : undefined}
          mode={ttlMode}
          defaultLabel={ttlDefault}
          disabled={!canManage}
          onMode={setTtlMode}
        >
          <NumberField
            aria-label="Cache TTL in seconds"
            minValue={0}
            hideSteppers
            value={ttl.trim() === "" ? NaN : Number(ttl)}
            isDisabled={!canManage}
            onChange={(n) => setTtl(Number.isNaN(n) ? "" : String(n))}
            placeholder="sec"
            className="w-24 shrink-0"
          />
        </OverrideField>
        <OverrideField
          label="Cost per request"
          hint={costMode === "custom" ? "USD, used for spend totals and app budgets." : undefined}
          mode={costMode}
          defaultLabel={costDefault}
          disabled={!canManage}
          onMode={setCostMode}
        >
          <NumberField
            aria-label="Cost per request in USD"
            minValue={0}
            hideSteppers
            formatOptions={{ maximumFractionDigits: 6 }}
            value={cost.trim() === "" ? NaN : Number(cost)}
            isDisabled={!canManage}
            onChange={(n) => setCost(Number.isNaN(n) ? "" : String(n))}
            placeholder="0.002"
            className="w-28 shrink-0"
            fieldClassName="font-mono"
          />
        </OverrideField>
      </div>
      {canManage && <SaveRow dirty={dirty} saving={saving} onDiscard={reset} onSave={save} label="Save traffic" />}
    </Section>
  );
}

const BREAKER_TONE: Record<"closed" | "open" | "half_open", ChipColor> = { closed: "lime", open: "rose", half_open: "yellow" };
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
    <Section
      title={
        <span className="flex flex-wrap items-center gap-2">
          <AcnCircuitBreaker className="size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />
          Circuit breaker
          {st && (
            <Chip variant="caption" color={BREAKER_TONE[st.state]} className="rounded-full px-2 py-0.5 font-mono">
              {BREAKER_LABEL[st.state]}
            </Chip>
          )}
        </span>
      }
      description={settings}
      actions={
        canManage &&
        st &&
        st.state !== "closed" && (
          <Button variant="secondary" size="small" leadingIcon={resetting ? ButtonSpinner : RiRestartLine} disabled={resetting} onClick={resetBreaker}>
            Reset
          </Button>
        )
      }
    >
      {st && (
        <dl>
          <Row label="Recent failures" value={String(st.failures)} />
          {st.opened_at && <Row label="Opened" value={relTime(st.opened_at, now)} />}
          {st.state === "open" && <Row label="Retry in" value={left || "any moment"} />}
          {st.reason && <Row label="Reason" value={st.reason} />}
        </dl>
      )}
    </Section>
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
  const [deleting, setDeleting] = useState(false);

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
      // A superadmin saving the form has seen the target and confirms it;
      // the server never trusts a target without this.
      await update(service.id, isSuper ? { ...rest, target_trusted: true } : rest);
      toast.success("Service updated");
      setEditing(null);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSavingEdit(false);
    }
  };

  const discardRotation = () => {
    setStrategy(service.strategy);
    setCooldown(service.cooldown_sec);
    setAttempts(service.max_attempts);
    setMaxConc(service.max_concurrency ?? 0);
    setQueueSec(service.queue_timeout_sec ?? 0);
  };
  const intOr0 = (n: number) => (Number.isNaN(n) ? 0 : Math.max(0, Math.round(n)));

  return (
    <div className="space-y-5">
      {isSuper && !service.target_trusted && (
        <Callout tone="warning" title="Hidden keys are paused for this service">
          Its base URL, auth or egress proxy changed without a superadmin confirming it, so Acnologia will not send hidden keys to it. Open Edit, check the base URL, then save to confirm it.
        </Callout>
      )}
      <Section
        title="Rotation and concurrency"
        description="How Acnologia picks the next key, how long a rate limited key rests, and how many requests one key can serve at once."
      >
        {/* Cards, not a select: each strategy's rule sits beside its name, so they compare at a glance. */}
        <RadioGroup
          value={strategy}
          isDisabled={!canManage}
          onChange={(v) => setStrategy(v as Strategy)}
          className="gap-1.5"
        >
          <Label>Strategy</Label>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {STRATEGIES.map((st) => (
              <RadioCard
                key={st}
                value={st}
                title={STRATEGY_LABEL[st]}
                // RadioCard truncates its description; this inner span lets the rule wrap instead.
                description={<span className="whitespace-normal text-caption-1-regular">{STRATEGY_HELP[st]}</span>}
                className="items-start"
              />
            ))}
          </div>
        </RadioGroup>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <NumberField
            label="Cooldown (seconds)"
            hint="Doubles with each failure in a row, up to 1 hour."
            minValue={0}
            value={cooldown}
            isDisabled={!canManage}
            onChange={(n) => setCooldown(intOr0(n))}
          />
          <NumberField
            label="Max attempts"
            hint="How many keys the proxy tries before giving up."
            minValue={1}
            maxValue={20}
            value={attempts}
            isDisabled={!canManage}
            onChange={(n) => setAttempts(Number.isNaN(n) ? 1 : n)}
          />
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <NumberField
            label="Max concurrent requests per key"
            hint="0 means no limit. A key at its limit is skipped until one of its requests finishes."
            minValue={0}
            maxValue={1000}
            value={maxConc}
            isDisabled={!canManage}
            onChange={(n) => setMaxConc(intOr0(n))}
          />
          <NumberField
            label="Queue wait (sec)"
            hint="How long a caller waits for a free key when every key is busy. 0 fails right away with all_keys_busy."
            minValue={0}
            maxValue={300}
            value={queueSec}
            isDisabled={!canManage}
            onChange={(n) => setQueueSec(intOr0(n))}
          />
        </div>
        {canManage && <SaveRow dirty={dirty} saving={saving} onDiscard={discardRotation} onSave={saveStrategy} label="Save rotation" />}
      </Section>

      <TrafficCard service={service} canManage={canManage} isSuper={isSuper} />
      <BreakerCard service={service} canManage={canManage} />

      <Section title="Logo" description="Shows in the catalog and on this page.">
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
      </Section>

      <Section
        title="Details"
        actions={
          canManage && (
            <Button
              variant="secondary"
              size="small"
              leadingIcon={RiPencilLine}
              onClick={() => {
                setEditErrors({});
                setEditing(inputOf(service));
              }}
            >
              Edit
            </Button>
          )
        }
      >
        <dl className="-mt-2">
          <Row label="Slug" value={service.slug} />
          <Row label="Provider" value={provider ? `${provider.name} (${service.provider})` : service.provider} />
          <Row label="Base URL" value={service.base_url} />
          <Row label="Auth" value={`${service.auth_type}${service.auth_name ? ` · ${service.auth_name}` : ""}`} />
          <Row label="Category" value={service.category} />
          <Row label="Description" value={service.description} />
          <Row label="Concurrency" value={service.max_concurrency ? `${service.max_concurrency} per key, waits up to ${service.queue_timeout_sec}s for a free key` : "No limit"} />
        </dl>
      </Section>

      {canManage && (
        <Section
          tone="danger"
          title="Delete service"
          description={`Removes the service and all its keys. Apps that call ${service.slug} start failing right away.`}
          actions={
            <Button variant="danger" leadingIcon={RiDeleteBinLine} onClick={() => setDeleting(true)}>
              Delete service
            </Button>
          }
        />
      )}

      <AlertDialog
        isOpen={deleting}
        onOpenChange={setDeleting}
        title={`Delete ${service.name}?`}
        description={`This removes the service and all its keys. Apps that call ${service.slug} start failing right away. This cannot be undone.`}
        confirmLabel="Delete forever"
        pendingLabel="Deleting"
        onConfirm={async () => {
          try {
            await remove(service.id);
            toast.success(`${service.name} deleted`);
            navigate("/");
          } catch (e) {
            toast.error(errorMessage(e));
            throw e;
          }
        }}
      />

      <Modal isOpen={!!editing} onOpenChange={(o) => !o && !savingEdit && setEditing(null)} size="lg">
        <ModalHeader title={`Edit ${service.name}`}>
          <div className="mt-3">
            <AiFillButton ai={ai} />
          </div>
        </ModalHeader>
        <ModalBody>
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
        </ModalBody>
        <ModalFooter>
          <Button variant="secondary" onClick={() => setEditing(null)} disabled={savingEdit}>
            Cancel
          </Button>
          <Button variant="primary" leadingIcon={savingEdit ? ButtonSpinner : undefined} disabled={savingEdit} onClick={saveEdit}>
            {savingEdit ? "Saving" : "Save"}
          </Button>
        </ModalFooter>
      </Modal>
    </div>
  );
}
