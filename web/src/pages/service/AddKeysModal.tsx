import { useEffect, useMemo, useState, type FormEvent } from "react";
import type { App, NewKeyInput, Service, Tier } from "../../lib/types";
import { useKeys } from "../../stores/keys";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { plural } from "../../lib/format";
import { Modal } from "../../components/Modal";
import { MultiSelect } from "../../components/MultiSelect";
import { Button, Field, Input, Select, Textarea, Toggle } from "../../components/ui";

interface Parsed {
  label: string;
  secret: string;
  /** Set when the line was "secret, email". */
  email: string;
}

/**
 * One key per line. "label,secret" sets a label; a bare line is just the secret.
 * "secret,email" sets the account email instead, when the part after the comma has an @.
 * Blank lines and lines starting with # are ignored. Duplicate secrets are dropped.
 */
export function parseKeys(text: string, startIndex: number, slug: string): { keys: Parsed[]; dupes: number } {
  const seen = new Set<string>();
  const keys: Parsed[] = [];
  let dupes = 0;
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    let label = "";
    let secret = line;
    let email = "";
    const comma = line.indexOf(",");
    if (comma > 0) {
      const first = line.slice(0, comma).trim();
      const rest = line.slice(comma + 1).trim();
      if (rest.includes("@")) {
        secret = first;
        email = rest;
      } else {
        label = first;
        secret = rest;
      }
    }
    if (!secret) continue;
    if (seen.has(secret)) {
      dupes++;
      continue;
    }
    seen.add(secret);
    keys.push({ label: label || `${slug} ${startIndex + keys.length + 1}`, secret, email });
  }
  return { keys, dupes };
}

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
  const add = useKeys((s) => s.add);
  const [text, setText] = useState("");
  const [tier, setTier] = useState<Tier>("primary");
  const [hidden, setHidden] = useState(false);
  const [restricted, setRestricted] = useState(false);
  const [appIds, setAppIds] = useState<number[]>([]);
  const [weight, setWeight] = useState(1);
  const [priority, setPriority] = useState(0);
  const [notes, setNotes] = useState("");
  const [account, setAccount] = useState({ name: "", email: "", phone: "", ref: "" });
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setText("");
    setTier("primary");
    setHidden(false);
    setRestricted(false);
    setAppIds([]);
    setWeight(1);
    setPriority(0);
    setNotes("");
    setAccount({ name: "", email: "", phone: "", ref: "" });
  }, [open]);

  const parsed = useMemo(() => parseKeys(text, existingCount, service.slug), [text, existingCount, service.slug]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!parsed.keys.length) {
      toast.error("Paste at least one key.");
      return;
    }
    const batchEmail = account.email.trim();
    if (batchEmail && !batchEmail.includes("@")) {
      toast.error("Account email needs an @.");
      return;
    }
    const payload: NewKeyInput[] = parsed.keys.map((p) => ({
      label: p.label,
      secret: p.secret,
      tier,
      hidden: canHidden ? hidden : false,
      restricted,
      weight,
      priority,
      notes,
      app_ids: restricted ? appIds : [],
      account_name: account.name.trim(),
      // An email on the line wins over the one for the whole batch.
      account_email: p.email || batchEmail,
      account_phone: account.phone.trim(),
      account_ref: account.ref.trim(),
    }));
    setBusy(true);
    try {
      const n = await add(service.id, payload);
      toast.success(`Added ${plural(n, "key")} to ${service.name}`);
      onClose();
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
      wide
      title={`Add keys to ${service.name}`}
      description="Paste one key per line. To name a key, write label,secret. To set its account email, write secret,email."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="add-keys-form" loading={busy} disabled={!parsed.keys.length}>
            {parsed.keys.length ? `Add ${plural(parsed.keys.length, "key")}` : "Add keys"}
          </Button>
        </>
      }
    >
      <form id="add-keys-form" onSubmit={submit} className="space-y-5">
        <div>
          <Textarea
            rows={7}
            value={text}
            onChange={(e) => setText(e.target.value)}
            spellCheck={false}
            autoComplete="off"
            className="font-mono text-[13px]"
            placeholder={"a1b2c3d4e5f6...\nmain account,f6e5d4c3b2a1...\n9f8e7d6c5b4a...,ops@example.com\n# lines starting with # are ignored"}
          />
          <div className="mt-1.5 font-mono text-[11px] text-muted">
            {plural(parsed.keys.length, "key")} detected
            {parsed.dupes > 0 && ` · ${plural(parsed.dupes, "duplicate")} skipped`}
          </div>
          {parsed.keys.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1.5">
              {parsed.keys.slice(0, 12).map((p, i) => (
                <span key={i} className="wrap-anywhere rounded-md bg-canvas px-1.5 py-0.5 font-mono text-[11px] text-muted">
                  {p.label} · ...{p.secret.slice(-4)}
                  {p.email && ` · ${p.email}`}
                </span>
              ))}
              {parsed.keys.length > 12 && <span className="font-mono text-[11px] text-faint">+{parsed.keys.length - 12} more</span>}
            </div>
          )}
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
              Applied to every key in this batch. An email written on a line wins for that key. Only admins can see these details.
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
      </form>
    </Modal>
  );
}
