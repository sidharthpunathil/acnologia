import { useEffect, useState, type FormEvent } from "react";
import type { App, Key, KeyPatch, KeyStatus, Tier } from "../../lib/types";
import { useKeys } from "../../stores/keys";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { Modal } from "../../components/Modal";
import { MultiSelect } from "../../components/MultiSelect";
import { Button, Field, Input, Select, Textarea, Toggle } from "../../components/ui";
import { ops, type EgressProxy } from "../../lib/ops";

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
  return (
    <Field
      label="Egress proxy"
      className="sm:col-span-2"
      hint={disabled ? "Only a superadmin can change the proxy of a hidden key." : "Send this key's traffic through a specific proxy, for example one per account region."}
    >
      <Select value={value} disabled={disabled} onChange={(e) => onChange(Number(e.target.value))}>
        <option value={0}>Use the service's proxy</option>
        {(proxies ?? []).map((p) => (
          <option key={p.id} value={p.id}>
            {p.region ? `${p.name} (${p.region})` : p.name}
          </option>
        ))}
        {value > 0 && !proxies?.some((p) => p.id === value) && <option value={value}>{`Proxy #${value}`}</option>}
      </Select>
    </Field>
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
  };
}

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

  if (!k || !d) return <Modal open={false} onClose={onClose} title="">{null}</Modal>;

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

  return (
    <Modal
      open
      onClose={onClose}
      wide
      title={`Edit ${k.label || "key"}`}
      description={<span className="font-mono">{k.hint}</span>}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="edit-key-form" loading={busy}>
            Save changes
          </Button>
        </>
      }
    >
      <form id="edit-key-form" onSubmit={submit} className="space-y-5">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Label">
            <Input value={d.label} onChange={(e) => set("label", e.target.value)} />
          </Field>
          <Field
            label="Status"
            hint={
              k.status === "banned"
                ? "The provider suspended this account. Pick enabled only after the account is restored."
                : k.status === "exhausted" || k.status === "invalid"
                  ? `Now ${k.status === "exhausted" ? "out of quota" : "invalid"}. Pick enabled to put it back in rotation.`
                  : undefined
            }
          >
            <Select value={d.status} onChange={(e) => set("status", e.target.value as KeyStatus)}>
              {statusOptions.map((s) => (
                <option key={s} value={s}>
                  {s === "active" ? "enabled" : s === "exhausted" ? "out of quota" : s}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Tier" hint="Backup keys are used only when no primary key is free.">
            <Select value={d.tier} onChange={(e) => set("tier", e.target.value as Tier)}>
              <option value="primary">primary</option>
              <option value="backup">backup</option>
            </Select>
          </Field>
          <Field label="Quota limit" hint="Leave empty when the provider has no fixed quota.">
            <Input type="number" min={0} value={d.quota_limit} onChange={(e) => set("quota_limit", e.target.value)} placeholder="unlimited" />
          </Field>
          <Field label="Weight" hint="Higher weight gets picked more often with weighted random.">
            <Input type="number" min={0} value={d.weight} onChange={(e) => set("weight", Number(e.target.value))} />
          </Field>
          <Field label="Priority" hint="With fill first, lower numbers are used first.">
            <Input type="number" value={d.priority} onChange={(e) => set("priority", Number(e.target.value))} />
          </Field>
          <Field
            label="Max concurrent requests"
            className="sm:col-span-2"
            hint={`Leave empty to use the service default (${serviceMax > 0 ? serviceMax : "no limit"}). 0 means no limit for this key.`}
          >
            <Input
              type="number"
              inputMode="numeric"
              min={0}
              max={1000}
              value={d.max_concurrency}
              onChange={(e) => set("max_concurrency", e.target.value)}
              placeholder={`Service default (${serviceMax > 0 ? serviceMax : "no limit"})`}
            />
          </Field>
          <KeyEgressField value={egressId} onChange={setEgressId} disabled={k.hidden && !canHidden} />
        </div>

        <div className="space-y-4 rounded-xl border border-line p-4">
          {canHidden && (
            <Toggle
              label="Hidden"
              description="Only superadmins can see or count this key."
              checked={d.hidden}
              onChange={(v) => set("hidden", v)}
            />
          )}
          <Toggle
            label="Restricted"
            description="Only the apps you pick below can use this key."
            checked={d.restricted}
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
        </div>

        <Field label="Notes">
          <Textarea rows={2} value={d.notes} onChange={(e) => set("notes", e.target.value)} placeholder="Which account it belongs to, who pays, when it renews" />
        </Field>

        <Field label="Replace secret" hint="Leave empty to keep the current secret. A new one is stored encrypted.">
          <Input type="password" autoComplete="off" value={d.secret} onChange={(e) => set("secret", e.target.value)} placeholder="Paste a new key" className="font-mono" />
        </Field>
      </form>
    </Modal>
  );
}
