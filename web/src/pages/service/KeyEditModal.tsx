import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { RiDiceLine } from "@remixicon/react";
import type { App, Key, KeyPatch, KeyStatus, Tier } from "../../lib/types";
import { useKeys } from "../../stores/keys";
import { useInsights } from "../../stores/insights";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { MultiSelect } from "../../components/MultiSelect";
import { ops, type EgressProxy } from "../../lib/ops";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "@/components/base/modal/modal";
import { NumberField } from "@/components/base/number-field/number-field";
import { Select, SelectItem } from "@/components/base/select/select";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { SwitchCard } from "@/components/base/switch/switch-card";
import { Textarea } from "@/components/base/textarea/textarea";
import { cx } from "@/utils/cx";
import { Tip } from "./parts";
import { randomLabel } from "./keyUtil";

/** Egress proxy picker for one key. 0 follows the service's proxy. Loads the proxy list itself. */
function KeyEgressField({ value, onChange, disabled }: { value: number; onChange: (v: number) => void; disabled?: boolean }) {
  const [proxies, setProxies] = useState<EgressProxy[] | null>(null);
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
  const options = [
    { id: "0", label: "Use the service's proxy" },
    ...(proxies ?? []).map((p) => ({ id: String(p.id), label: p.region ? `${p.name} (${p.region})` : p.name })),
  ];
  if (value > 0 && !proxies?.some((p) => p.id === value)) options.push({ id: String(value), label: `Proxy #${value}` });
  return (
    <div className="sm:col-span-2">
      <Select
        label="Egress proxy"
        selectedKey={String(value)}
        isDisabled={disabled}
        onSelectionChange={(k) => onChange(Number(k))}
        className="w-full"
      >
        {options.map((o) => (
          <SelectItem key={o.id} id={o.id}>
            {o.label}
          </SelectItem>
        ))}
      </Select>
      <p className="mt-1 text-caption-1-regular text-text-secondary">
        {disabled ? "Only a superadmin can change the proxy of a hidden key." : "Send this key's traffic through a specific proxy, for example one per account region."}
      </p>
    </div>
  );
}

/** A number field over a string draft, where empty means "not set". */
const numOrNaN = (v: string) => (v.trim() === "" ? NaN : Number(v));
const strOf = (n: number) => (Number.isNaN(n) ? "" : String(n));

/** A labelled group: section title, one line of help, then its fields. */
function Group({ id, title, help, children, className }: { id: string; title: string; help?: string; children: ReactNode; className?: string }) {
  return (
    <div role="group" aria-labelledby={id} className={cx("space-y-3", className)}>
      <div>
        <h3 id={id} className="text-body-2-semibold text-text-primary">
          {title}
        </h3>
        {help && <p className="mt-0.5 text-caption-1-regular text-text-secondary">{help}</p>}
      </div>
      {children}
    </div>
  );
}

interface Draft {
  label: string;
  tier: Tier;
  hidden: boolean;
  restricted: boolean;
  app_ids: number[];
  weight: number;
  priority: number;
  status: KeyStatus;
  notes: string;
  quota_limit: string;
  secret: string;
  /** Empty string means use the service default. */
  max_concurrency: string;
  account_name: string;
  account_email: string;
  account_phone: string;
  account_ref: string;
}

function draftOf(k: Key): Draft {
  return {
    label: k.label,
    tier: k.tier,
    hidden: k.hidden,
    restricted: k.restricted,
    app_ids: k.app_ids ?? [],
    weight: k.weight,
    priority: k.priority,
    status: k.status,
    notes: k.notes,
    quota_limit: k.quota_limit === null ? "" : String(k.quota_limit),
    secret: "",
    max_concurrency: k.max_concurrency === null || k.max_concurrency === undefined ? "" : String(k.max_concurrency),
    account_name: k.account_name ?? "",
    account_email: k.account_email ?? "",
    account_phone: k.account_phone ?? "",
    account_ref: k.account_ref ?? "",
  };
}

/**
 * The key's label with a dice button inside the field that fills a random,
 * readable label nobody else in the service uses. The input keeps focus and
 * the new text is selected, so typing replaces it.
 */
function LabelField({ value, onChange, taken }: { value: string; onChange: (v: string) => void; taken: string[] }) {
  const ref = useRef<HTMLInputElement>(null);
  const roll = () => {
    // The current value counts as taken too, so every click gives a new label.
    onChange(randomLabel([...taken, value]));
    // After React writes the new value, so the selection covers all of it.
    requestAnimationFrame(() => {
      ref.current?.focus();
      ref.current?.select();
    });
  };
  return (
    <Input
      label="Label"
      ref={ref}
      value={value}
      onChange={onChange}
      trailingAddon={
        <Tip label="Random label">
          <button
            type="button"
            aria-label="Random label"
            // Keep focus in the field on a click; keyboard presses move it back in roll().
            onMouseDown={(e) => e.preventDefault()}
            onClick={roll}
            className="-my-1 -me-1 inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-lg text-foreground-icon-secondary outline-none transition-colors duration-150 ease hover:bg-background-tertiary-hover hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          >
            <RiDiceLine className="size-4" aria-hidden />
          </button>
        </Tip>
      }
    />
  );
}

const ACCOUNT_FIELDS = ["account_name", "account_email", "account_phone", "account_ref"] as const;

const sameIds = (a: number[], b: number[]) => a.length === b.length && [...a].sort().join(",") === [...b].sort().join(",");

export function KeyEditModal({
  k,
  apps,
  canHidden,
  serviceMax = 0,
  onClose,
}: {
  k: Key | null;
  apps: App[];
  canHidden: boolean;
  serviceMax?: number;
  onClose: () => void;
}) {
  const update = useKeys((s) => s.update);
  // Labels the random label must avoid: the service's other keys. The Keys page
  // may not have loaded this service's pool, so load it and use the insight rows meanwhile.
  const siblings = useKeys((s) => (k ? s.byService[k.service_id] : undefined));
  const fetchKeys = useKeys((s) => s.fetch);
  const insightRows = useInsights((s) => s.rows);
  const serviceId = k?.service_id;
  const poolLoaded = siblings !== undefined;
  useEffect(() => {
    if (serviceId !== undefined && !poolLoaded) fetchKeys(serviceId).catch(() => undefined);
  }, [serviceId, poolLoaded, fetchKeys]);
  const takenLabels = useMemo(() => {
    if (!k) return [];
    const pool = siblings ?? insightRows.filter((x) => x.service_id === k.service_id);
    return pool.filter((x) => x.id !== k.id).map((x) => x.label);
  }, [k, siblings, insightRows]);
  const [d, setD] = useState<Draft | null>(null);
  const [busy, setBusy] = useState(false);
  // Kept outside Draft so this field stays independent of the other key fields.
  const [egressId, setEgressId] = useState(0);

  // Reset only when a different key is opened, so background refreshes do not wipe edits.
  const keyId = k?.id ?? null;
  useEffect(() => {
    setD(k ? draftOf(k) : null);
    setEgressId(k?.egress_id ?? 0);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keyId]);

  if (!k || !d) return null;

  const set = <K extends keyof Draft>(key: K, v: Draft[K]) => setD({ ...d, [key]: v });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const patch: KeyPatch = {};
    if (d.label !== k.label) patch.label = d.label;
    if (d.tier !== k.tier) patch.tier = d.tier;
    if (canHidden && d.hidden !== k.hidden) patch.hidden = d.hidden;
    if (d.restricted !== k.restricted) patch.restricted = d.restricted;
    if (!sameIds(d.app_ids, k.app_ids ?? [])) patch.app_ids = d.app_ids;
    if (d.weight !== k.weight) patch.weight = d.weight;
    if (d.priority !== k.priority) patch.priority = d.priority;
    if (d.status !== k.status) patch.status = d.status;
    if (d.notes !== k.notes) patch.notes = d.notes;
    const ql = d.quota_limit.trim() === "" ? null : Number(d.quota_limit);
    if (ql !== null && (Number.isNaN(ql) || ql < 0)) {
      toast.error("Quota limit must be 0 or more, or empty.");
      return;
    }
    if (ql !== k.quota_limit) patch.quota_limit = ql;
    if (d.secret.trim()) patch.secret = d.secret.trim();
    const mc = d.max_concurrency.trim() === "" ? null : Number(d.max_concurrency);
    if (mc !== null && (!Number.isInteger(mc) || mc < 0 || mc > 1000)) {
      toast.error("Max concurrent requests must be a whole number from 0 to 1000, or empty.");
      return;
    }
    if (mc !== (k.max_concurrency ?? null)) patch.max_concurrency = mc === null ? -1 : mc;
    const egressLocked = k.hidden && !canHidden;
    if (!egressLocked && egressId !== (k.egress_id ?? 0)) patch.egress_id = egressId;
    const email = d.account_email.trim();
    if (email && !email.includes("@")) {
      toast.error("Account email needs an @.");
      return;
    }
    for (const f of ACCOUNT_FIELDS) {
      const v = d[f].trim();
      if (v !== (k[f] ?? "")) patch[f] = v;
    }

    if (Object.keys(patch).length === 0) {
      onClose();
      return;
    }
    setBusy(true);
    try {
      await update(k, patch);
      toast.success(`Saved ${d.label || k.hint}`);
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const statusOptions: KeyStatus[] = ["active", "disabled"];
  if (k.status === "exhausted" || k.status === "invalid" || k.status === "banned") statusOptions.push(k.status);

  const statusLabel = (st: KeyStatus) => (st === "active" ? "enabled" : st === "exhausted" ? "out of quota" : st);
  const statusHint =
    k.status === "banned"
      ? "The provider suspended this account. Pick enabled only after the account is restored."
      : k.status === "exhausted" || k.status === "invalid"
        ? `Now ${k.status === "exhausted" ? "out of quota" : "invalid"}. Pick enabled to put it back in rotation.`
        : undefined;

  return (
    <Modal isOpen onOpenChange={(o) => !o && !busy && onClose()} size="lg" isKeyboardDismissDisabled={busy}>
      <ModalHeader title={`Edit ${k.label || "key"}`} description={<span className="font-mono">{k.hint}</span>} />
      <ModalBody>
        <form id="edit-key-form" onSubmit={submit} className="space-y-6 pb-1">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <LabelField value={d.label} onChange={(v) => set("label", v)} taken={takenLabels} />
            <Select label="Status" selectedKey={d.status} onSelectionChange={(v) => set("status", v as KeyStatus)} className="w-full">
              {statusOptions.map((st) => (
                <SelectItem key={st} id={st}>
                  {statusLabel(st)}
                </SelectItem>
              ))}
            </Select>
            {statusHint && <p className="-mt-2 text-caption-1-regular text-text-secondary sm:col-span-2">{statusHint}</p>}
            <div>
              <Select label="Tier" selectedKey={d.tier} onSelectionChange={(v) => set("tier", v as Tier)} className="w-full">
                <SelectItem id="primary">primary</SelectItem>
                <SelectItem id="backup">backup</SelectItem>
              </Select>
              <p className="mt-1 text-caption-1-regular text-text-secondary">Backup keys are used only when no primary key is free.</p>
            </div>
            <NumberField
              label="Quota limit"
              hint="Leave empty when the provider has no fixed quota."
              minValue={0}
              value={numOrNaN(d.quota_limit)}
              onChange={(n) => set("quota_limit", strOf(n))}
              placeholder="unlimited"
            />
            <NumberField label="Weight" hint="Higher weight gets picked more often with weighted random." minValue={0} value={d.weight} onChange={(n) => set("weight", Number.isNaN(n) ? 0 : n)} />
            <NumberField label="Priority" hint="With fill first, lower numbers are used first." value={d.priority} onChange={(n) => set("priority", Number.isNaN(n) ? 0 : n)} />
            <NumberField
              label="Max concurrent requests"
              className="sm:col-span-2"
              hint={`Leave empty to use the service default (${serviceMax > 0 ? serviceMax : "no limit"}). 0 means no limit for this key.`}
              minValue={0}
              maxValue={1000}
              formatOptions={{ maximumFractionDigits: 0 }}
              value={numOrNaN(d.max_concurrency)}
              onChange={(n) => set("max_concurrency", strOf(n))}
              placeholder={`Service default (${serviceMax > 0 ? serviceMax : "no limit"})`}
            />
            <KeyEgressField value={egressId} onChange={setEgressId} disabled={k.hidden && !canHidden} />
          </div>

          <Group id="edit-key-access" title="Access">
            {canHidden && (
              <SwitchCard title="Hidden" description="Only superadmins can see or count this key." isSelected={d.hidden} onChange={(v) => set("hidden", v)} />
            )}
            <SwitchCard
              title="Restricted"
              description="Only the apps you pick below can use this key."
              isSelected={d.restricted}
              onChange={(v) => set("restricted", v)}
            />
            {d.restricted && (
              <MultiSelect
                options={apps.map((a) => ({ value: a.id, label: a.name, meta: a.token_prefix }))}
                value={d.app_ids}
                onChange={(v) => set("app_ids", v)}
                empty="No apps yet. Create an app first, then allow it here."
              />
            )}
          </Group>

          <Group id="edit-key-account" title="Account" help="Who this key belongs to. Only admins can see these details.">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Input label="Name" value={d.account_name} maxLength={200} autoComplete="off" onChange={(v) => set("account_name", v)} />
              <Input label="Email" type="email" value={d.account_email} maxLength={254} autoComplete="off" onChange={(v) => set("account_email", v)} />
              <Input label="Phone" type="tel" value={d.account_phone} maxLength={40} autoComplete="off" onChange={(v) => set("account_phone", v)} />
              <Input
                label="Other"
                hint="Username, account ID or anything else that identifies the account"
                value={d.account_ref}
                maxLength={200}
                autoComplete="off"
                onChange={(v) => set("account_ref", v)}
              />
            </div>
          </Group>

          <Textarea label="Notes" rows={2} value={d.notes} onChange={(v) => set("notes", v)} placeholder="Which account it belongs to, who pays, when it renews" />

          <Input
            label="Replace secret"
            hint="Leave empty to keep the current secret. A new one is stored encrypted."
            type="password"
            autoComplete="off"
            value={d.secret}
            onChange={(v) => set("secret", v)}
            placeholder="Paste a new key"
            fieldClassName="font-mono"
          />
        </form>
      </ModalBody>
      <ModalFooter>
        <Button variant="secondary" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button variant="primary" type="submit" form="edit-key-form" leadingIcon={busy ? ButtonSpinner : undefined} disabled={busy}>
          {busy ? "Saving" : "Save changes"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}
