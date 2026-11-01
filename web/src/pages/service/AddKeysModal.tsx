import { useEffect, useState, type FormEvent } from "react";
import { CheckCircle2 } from "lucide-react";
import type { App, NewKeyInput, Service, Tier } from "../../lib/types";
import { useKeys } from "../../stores/keys";
import { toast } from "../../stores/toast";
import { api, errorMessage } from "../../lib/api";
import { num, plural } from "../../lib/format";
import { Modal } from "../../components/Modal";
import { MultiSelect } from "../../components/MultiSelect";
import { KeyPasteArea, KeyPreview, useKeyInput } from "../../components/KeyPaste";
import { Button, Callout, Field, Input, Select, Toggle } from "../../components/ui";

/** The server takes up to 1000 keys a request; smaller chunks give steadier progress. */
const CHUNK = 500;

const PLACEHOLDER = `Paste keys in any format, or drop files here. For example:
sk_live_a1b2c3d4e5f6a7b8c9d0
prod-eu, f6e5d4c3b2a1f6e5d4c3
OPENAI_KEY=9f8e7d6c5b4a9f8e7d6c
# lines starting with # are ignored`;

type Phase =
  | { kind: "edit" }
  | { kind: "adding"; done: number; total: number }
  | { kind: "done"; added: number; skipped: number; total: number; error?: string };

export function AddKeysModal({
  open,
  onClose,
  service,
  apps,
  existingCount,
  canHidden,
}: {
  open: boolean;
  onClose: () => void;
  service: Service;
  apps: App[];
  existingCount: number;
  canHidden: boolean;
}) {
  const fetchKeys = useKeys((s) => s.fetch);
  const input = useKeyInput();
  const { parsed, reset } = input;
  const [tier, setTier] = useState<Tier>("primary");
  const [hidden, setHidden] = useState(false);
  const [restricted, setRestricted] = useState(false);
  const [appIds, setAppIds] = useState<number[]>([]);
  const [weight, setWeight] = useState(1);
  const [priority, setPriority] = useState(0);
  const [notes, setNotes] = useState("");
  const [account, setAccount] = useState({ name: "", email: "", phone: "", ref: "" });
  const [phase, setPhase] = useState<Phase>({ kind: "edit" });

  useEffect(() => {
    if (!open) return;
    reset();
    setTier("primary");
    setHidden(false);
    setRestricted(false);
    setAppIds([]);
    setWeight(1);
    setPriority(0);
    setNotes("");
    setAccount({ name: "", email: "", phone: "", ref: "" });
    setPhase({ kind: "edit" });
  }, [open, reset]);

  const fallbackLabel = (i: number) => `${service.slug} ${existingCount + i + 1}`;
  const busy = phase.kind === "adding";
  const count = parsed.keys.length;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    if (!count) {
      toast.error("Paste or drop at least one key.");
      document.getElementById("add-keys-text")?.focus();
      return;
    }
    const batchEmail = account.email.trim();
    if (batchEmail && !batchEmail.includes("@")) {
      toast.error("Account email needs an @.");
      return;
    }
    const payload: NewKeyInput[] = parsed.keys.map((p, i) => ({
      label: p.label || fallbackLabel(i),
      secret: p.secret,
      tier,
      hidden: canHidden ? hidden : false,
      restricted,
      weight,
      priority,
      notes,
      app_ids: restricted ? appIds : [],
      account_name: account.name.trim(),
      account_email: batchEmail,
      account_phone: account.phone.trim(),
      account_ref: account.ref.trim(),
    }));
    let added = 0;
    let skipped = 0;
    let error: string | undefined;
    setPhase({ kind: "adding", done: 0, total: payload.length });
    for (let i = 0; i < payload.length; i += CHUNK) {
      const chunk = payload.slice(i, i + CHUNK);
      try {
        const res = await api.addKeys(service.id, chunk);
        const made = res?.keys?.length ?? 0;
        added += made;
        skipped += chunk.length - made;
      } catch (err) {
        error = errorMessage(err);
        break;
      }
      setPhase({ kind: "adding", done: Math.min(i + CHUNK, payload.length), total: payload.length });
    }
    fetchKeys(service.id).catch(() => undefined);
    setPhase({ kind: "done", added, skipped, total: payload.length, error });
    // The result view says what happened; a toast on top would repeat it.
    if (error) toast.error(error);
  };

  if (phase.kind === "done") {
    const notSent = phase.total - phase.added - phase.skipped;
    return (
      <Modal
        open={open}
        onClose={onClose}
        title={phase.error ? "Some keys were not added" : `Added to ${service.name}`}
        footer={
          <>
            {phase.error && (
              <Button variant="ghost" onClick={() => setPhase({ kind: "edit" })}>
                Back to the list
              </Button>
            )}
            <Button variant="primary" onClick={onClose}>
              Done
            </Button>
          </>
        }
      >
        <div className="space-y-4" aria-live="polite">
          <div className="flex items-start gap-3">
            <CheckCircle2 className={phase.added ? "mt-0.5 h-5 w-5 shrink-0 text-emerald-600" : "mt-0.5 h-5 w-5 shrink-0 text-faint"} aria-hidden="true" />
            <div>
              <p className="text-[15px] font-medium text-ink">{plural(phase.added, "key")} added</p>
              <p className="mt-0.5 text-[13px] leading-relaxed text-muted">
                {phase.skipped > 0 ? `${plural(phase.skipped, "key")} skipped because ${phase.skipped === 1 ? "it was" : "they were"} already in this service.` : "None were already in this service."}
                {phase.added > 0 && " New keys are checked in the background, so quotas show up on their own."}
              </p>
            </div>
          </div>
          <dl className="grid grid-cols-3 gap-2 font-mono">
            {[
              ["added", phase.added],
              ["already there", phase.skipped],
              ["not sent", notSent],
            ].map(([k, v]) => (
              <div key={k} className="rounded-xl bg-soft px-3 py-2.5">
                <dt className="text-[11px] text-muted">{k}</dt>
                <dd className="mt-0.5 text-[18px] tabular-nums text-ink">{num(v as number)}</dd>
              </div>
            ))}
          </dl>
          {phase.error && (
            <Callout tone="red">
              Stopped after {num(phase.added + phase.skipped)} of {num(phase.total)}: {phase.error}
            </Callout>
          )}
        </div>
      </Modal>
    );
  }

  return (
    <Modal
      open={open}
      onClose={busy ? () => undefined : onClose}
      wide
      title={`Add keys to ${service.name}`}
      description="Paste keys in any shape: one per line, comma or space separated, label then key, an env file, CSV with a header, or JSON. You see what was found before anything is saved."
      footer={
        <>
          {busy && (
            <div className="mr-auto flex min-w-0 flex-1 items-center gap-3" role="status">
              <div className="h-1.5 min-w-[60px] flex-1 overflow-hidden rounded-full bg-well">
                <div
                  className="h-full rounded-full bg-brand transition-[width] duration-300 ease-[var(--ease-out)]"
                  style={{ width: `${Math.max(4, (phase.done / phase.total) * 100)}%` }}
                />
              </div>
              <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted">
                {num(phase.done)} / {num(phase.total)}
              </span>
            </div>
          )}
          {!busy && (
            <Button variant="ghost" onClick={onClose}>
              Cancel
            </Button>
          )}
          <Button variant="primary" type="submit" form="add-keys-form" loading={busy} disabled={!count}>
            {busy ? "Adding" : count ? `Add ${num(count)} ${count === 1 ? "key" : "keys"}` : "Add keys"}
          </Button>
        </>
      }
    >
      <form id="add-keys-form" onSubmit={submit} className="space-y-5" {...input.dropProps}>
        <fieldset disabled={busy} className="min-w-0 space-y-5">
          <div className="space-y-3">
            <KeyPasteArea input={input} id="add-keys-text" label="Keys" rows={8} placeholder={PLACEHOLDER} />
            <KeyPreview parsed={parsed} stale={input.stale} fallbackLabel={fallbackLabel} />
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <Field label="Tier">
              <Select value={tier} onChange={(e) => setTier(e.target.value as Tier)}>
                <option value="primary">primary</option>
                <option value="backup">backup</option>
              </Select>
            </Field>
            <Field label="Weight">
              <Input type="number" min={0} value={weight} onChange={(e) => setWeight(Number(e.target.value))} />
            </Field>
            <Field label="Priority">
              <Input type="number" value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
            </Field>
          </div>

          <div className="space-y-4 rounded-xl border border-line p-4">
            {canHidden && (
              <Toggle label="Hidden" description="Only superadmins can see or count these keys." checked={hidden} onChange={setHidden} />
            )}
            <Toggle
              label="Restricted"
              description="Only the apps you pick can use these keys."
              checked={restricted}
              onChange={setRestricted}
            />
            {restricted && (
              <MultiSelect
                options={apps.map((a) => ({ value: a.id, label: a.name, meta: a.token_prefix }))}
                value={appIds}
                onChange={setAppIds}
                empty="No apps yet. Create an app first, then allow it here."
              />
            )}
          </div>

          <div role="group" aria-labelledby="add-keys-account" className="space-y-4 rounded-xl border border-line p-4">
            <div>
              <h3 id="add-keys-account" className="text-[13px] font-medium text-ink">Account (optional)</h3>
              <p className="mt-0.5 text-xs leading-relaxed text-muted">
                Applied to every key in this batch. Only admins can see these details.
              </p>
            </div>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="Name">
                <Input value={account.name} maxLength={200} autoComplete="off" onChange={(e) => setAccount({ ...account, name: e.target.value })} />
              </Field>
              <Field label="Email">
                <Input type="email" value={account.email} maxLength={254} autoComplete="off" onChange={(e) => setAccount({ ...account, email: e.target.value })} />
              </Field>
              <Field label="Phone">
                <Input type="tel" value={account.phone} maxLength={40} autoComplete="off" onChange={(e) => setAccount({ ...account, phone: e.target.value })} />
              </Field>
              <Field label="Other" hint="Username, account ID or anything else that identifies the account">
                <Input value={account.ref} maxLength={200} autoComplete="off" onChange={(e) => setAccount({ ...account, ref: e.target.value })} />
              </Field>
            </div>
          </div>

          <Field label="Notes" hint="Applied to every key in this batch.">
            <Input value={notes} onChange={(e) => setNotes(e.target.value)} />
          </Field>
        </fieldset>
      </form>
    </Modal>
  );
}
