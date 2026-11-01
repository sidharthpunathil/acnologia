import { useEffect, useState, type FormEvent } from "react";
import { RiCheckboxCircleLine, RiKey2Line } from "@remixicon/react";
import type { App, NewKeyInput, Service, Tier } from "../../lib/types";
import { useKeys } from "../../stores/keys";
import { toast } from "../../stores/toast";
import { api, errorMessage } from "../../lib/api";
import { num, plural } from "../../lib/format";
import { MultiSelect } from "../../components/MultiSelect";
import { KeyPasteArea, KeyPreview, useKeyInput } from "../../components/KeyPaste";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "@/components/base/modal/modal";
import { NumberField } from "@/components/base/number-field/number-field";
import { ProgressTrack } from "@/components/base/progress/progress";
import { Select, SelectItem } from "@/components/base/select/select";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { SwitchCard } from "@/components/base/switch/switch-card";
import { cx } from "@/utils/cx";
import { Callout } from "@/components/acn";

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
      <Modal isOpen={open} onOpenChange={(o) => !o && onClose()} size="md">
        <ModalHeader
          title={phase.error ? "Some keys were not added" : `Added to ${service.name}`}
          icon={RiCheckboxCircleLine}
          tone={phase.error ? "danger" : "default"}
        />
        <ModalBody>
          <div className="space-y-4" aria-live="polite">
            <div>
              <p className="text-body-medium text-text-primary">{plural(phase.added, "key")} added</p>
              <p className="mt-0.5 text-body-2-regular text-text-secondary">
                {phase.skipped > 0 ? `${plural(phase.skipped, "key")} skipped because ${phase.skipped === 1 ? "it was" : "they were"} already in this service.` : "None were already in this service."}
                {phase.added > 0 && " New keys are checked in the background, so quotas show up on their own."}
              </p>
            </div>
            <dl className="grid grid-cols-3 gap-2">
              {[
                ["added", phase.added],
                ["already there", phase.skipped],
                ["not sent", notSent],
              ].map(([k, v]) => (
                <div key={k} className="rounded-2xl bg-background-secondary-default px-3 py-2.5">
                  <dt className="text-caption-1-regular text-text-secondary">{k}</dt>
                  <dd className="mt-0.5 font-mono text-title-3-medium tabular-nums text-text-primary">{num(v as number)}</dd>
                </div>
              ))}
            </dl>
            {phase.error && (
              <Callout tone="error">
                Stopped after {num(phase.added + phase.skipped)} of {num(phase.total)}: {phase.error}
              </Callout>
            )}
          </div>
        </ModalBody>
        <ModalFooter>
          {phase.error && (
            <Button variant="secondary" onClick={() => setPhase({ kind: "edit" })}>
              Back to the list
            </Button>
          )}
          <Button variant="primary" onClick={onClose}>
            Done
          </Button>
        </ModalFooter>
      </Modal>
    );
  }

  return (
    <Modal
      isOpen={open}
      onOpenChange={(o) => !o && !busy && onClose()}
      isDismissable={!busy}
      isKeyboardDismissDisabled={busy}
      size="lg"
    >
      <ModalHeader
        title={`Add keys to ${service.name}`}
        description="Paste keys in any shape: one per line, comma or space separated, label then key, an env file, CSV with a header, or JSON. You see what was found before anything is saved."
        icon={RiKey2Line}
        showCloseButton={!busy}
      />
      <ModalBody>
        <form id="add-keys-form" onSubmit={submit} className="pb-1" {...input.dropProps}>
          <fieldset disabled={busy} className="min-w-0 space-y-6">
            <div className="space-y-3">
              <KeyPasteArea input={input} id="add-keys-text" label="Keys" rows={8} placeholder={PLACEHOLDER} />
              <KeyPreview parsed={parsed} stale={input.stale} fallbackLabel={fallbackLabel} />
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
              <Select label="Tier" selectedKey={tier} onSelectionChange={(v) => setTier(v as Tier)} className="w-full">
                <SelectItem id="primary">primary</SelectItem>
                <SelectItem id="backup">backup</SelectItem>
              </Select>
              <NumberField label="Weight" minValue={0} value={weight} onChange={(n) => setWeight(Number.isNaN(n) ? 0 : n)} />
              <NumberField label="Priority" value={priority} onChange={(n) => setPriority(Number.isNaN(n) ? 0 : n)} />
            </div>

            <div role="group" aria-labelledby="add-keys-access" className="space-y-3">
              <h3 id="add-keys-access" className="text-body-2-semibold text-text-primary">
                Access
              </h3>
              {canHidden && <SwitchCard title="Hidden" description="Only superadmins can see or count these keys." isSelected={hidden} onChange={setHidden} />}
              <SwitchCard title="Restricted" description="Only the apps you pick can use these keys." isSelected={restricted} onChange={setRestricted} />
              {restricted && (
                <MultiSelect
                  options={apps.map((a) => ({ value: a.id, label: a.name, meta: a.token_prefix }))}
                  value={appIds}
                  onChange={setAppIds}
                  empty="No apps yet. Create an app first, then allow it here."
                />
              )}
            </div>

            <div role="group" aria-labelledby="add-keys-account" className="space-y-3">
              <div>
                <h3 id="add-keys-account" className="text-body-2-semibold text-text-primary">
                  Account (optional)
                </h3>
                <p className="mt-0.5 text-caption-1-regular text-text-secondary">Applied to every key in this batch. Only admins can see these details.</p>
              </div>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <Input label="Name" value={account.name} maxLength={200} autoComplete="off" onChange={(v) => setAccount({ ...account, name: v })} />
                <Input label="Email" type="email" value={account.email} maxLength={254} autoComplete="off" onChange={(v) => setAccount({ ...account, email: v })} />
                <Input label="Phone" type="tel" value={account.phone} maxLength={40} autoComplete="off" onChange={(v) => setAccount({ ...account, phone: v })} />
                <Input
                  label="Other"
                  hint="Username, account ID or anything else that identifies the account"
                  value={account.ref}
                  maxLength={200}
                  autoComplete="off"
                  onChange={(v) => setAccount({ ...account, ref: v })}
                />
              </div>
            </div>

            <Input label="Notes" hint="Applied to every key in this batch." value={notes} onChange={setNotes} />
          </fieldset>
        </form>
      </ModalBody>
      <ModalFooter className={cx(busy && "sm:justify-between")}>
        {busy && (
          <div className="flex min-w-0 flex-1 items-center gap-3" role="status">
            <ProgressTrack percentage={Math.max(4, (phase.done / phase.total) * 100)} size="sm" className="min-w-[60px] flex-1" />
            <span className="shrink-0 font-mono text-caption-1-regular tabular-nums text-text-secondary">
              {num(phase.done)} / {num(phase.total)}
            </span>
          </div>
        )}
        {!busy && (
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
        )}
        <Button variant="primary" type="submit" form="add-keys-form" leadingIcon={busy ? ButtonSpinner : undefined} disabled={busy || !count}>
          {busy ? "Adding" : count ? `Add ${num(count)} ${count === 1 ? "key" : "keys"}` : "Add keys"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}
