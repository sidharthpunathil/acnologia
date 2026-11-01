import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTitle } from "../hooks/useTitle";
import { Check, Pencil, Plus, Send, TriangleAlert, Webhook as WebhookIcon } from "lucide-react";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { dateTime, plural, relTime } from "../lib/format";
import { ops, type Webhook, type WebhookEventType, type WebhookFormat, type WebhookInput } from "../lib/ops";
import { useNow } from "../hooks/useNow";
import { Modal } from "../components/Modal";
import { SecretBox } from "../components/CopyButton";
import { ConfirmButton } from "../components/ConfirmButton";
import { Badge, Button, Empty, Field, IconButton, Input, PageHeader, PageSkeleton, Select, Toggle, cx } from "../components/ui";

const FORMATS: WebhookFormat[] = ["json", "slack", "discord", "telegram"];

const FORMAT_HELP: Record<WebhookFormat, string> = {
  json: "Signed JSON body for your own receiver. You get a signing secret once, after creating it.",
  slack: "A Slack incoming webhook URL.",
  discord: "A Discord channel webhook URL.",
  telegram: "Like https://api.telegram.org/bot<token>/sendMessage?chat_id=<id>",
};

const FORMAT_PLACEHOLDER: Record<WebhookFormat, string> = {
  json: "https://example.com/hooks/cogito",
  slack: "https://hooks.slack.com/services/...",
  discord: "https://discord.com/api/webhooks/...",
  telegram: "https://api.telegram.org/bot<token>/sendMessage?chat_id=<id>",
};

interface FormState {
  name: string;
  url: string;
  format: WebhookFormat;
  events: string[];
  active: boolean;
}

const blank: FormState = { name: "", url: "", format: "json", events: [], active: true };

function EventPicker({ types, value, onChange }: { types: WebhookEventType[]; value: string[]; onChange: (v: string[]) => void }) {
  if (!types.length) return <div className="rounded-xl border border-dashed border-line px-3 py-3 text-xs text-muted">No event types reported by the server.</div>;
  const set = new Set(value);
  return (
    <div className="max-h-56 divide-y divide-line overflow-y-auto rounded-xl border border-line bg-surface">
      {types.map((t) => {
        const on = set.has(t.type);
        return (
          <button
            key={t.type}
            type="button"
            role="checkbox"
            aria-checked={on}
            onClick={() => onChange(on ? value.filter((v) => v !== t.type) : [...value, t.type])}
            className="tap flex w-full items-start gap-2.5 px-3 py-2.5 text-left transition-colors hover:bg-soft"
          >
            <span
              className={cx(
                "mt-0.5 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border transition-colors",
                on ? "border-ink bg-ink text-on-ink" : "border-line-strong bg-surface",
              )}
            >
              {on && <Check className="h-3 w-3" />}
            </span>
            <span className="min-w-0">
              <span className="wrap-anywhere block font-mono text-xs text-ink">{t.type}</span>
              {t.description && <span className="mt-0.5 block text-xs leading-relaxed text-muted">{t.description}</span>}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function WebhookFormModal({
  open,
  initial,
  eventTypes,
  formats,
  onClose,
  onSaved,
}: {
  open: boolean;
  initial: Webhook | null;
  eventTypes: WebhookEventType[];
  formats: WebhookFormat[];
  onClose: () => void;
  onSaved: (secret: { name: string; secret: string } | null) => void;
}) {
  const [form, setForm] = useState<FormState>(blank);
  const [busy, setBusy] = useState(false);
  const [nameErr, setNameErr] = useState<string | null>(null);
  const [urlErr, setUrlErr] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setNameErr(null);
    setUrlErr(null);
    setForm(
      initial
        ? { name: initial.name, url: "", format: initial.format, events: initial.events ?? [], active: initial.active }
        : blank,
    );
  }, [open, initial]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const name = form.name.trim();
    const url = form.url.trim();
    let bad = false;
    if (!name) {
      setNameErr("Give the webhook a name, such as ops channel.");
      bad = true;
    }
    if (!initial && !url) {
      setUrlErr("Enter the URL to deliver to.");
      bad = true;
    }
    if (bad) return;
    setBusy(true);
    try {
      const body: WebhookInput = { name, format: form.format, events: form.events, active: form.active };
      if (url) body.url = url;
      if (initial) {
        await ops.updateWebhook(initial.id, body);
        toast.success(`Saved ${name}`);
        onClose();
        onSaved(null);
      } else {
        const res = await ops.createWebhook(body);
        onClose();
        onSaved(res.signing_secret ? { name: res.webhook.name, secret: res.signing_secret } : null);
        if (!res.signing_secret) toast.success(`Created ${res.webhook.name}`);
      }
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const options = formats.length ? formats : FORMATS;

  return (
    <Modal
      open={open}
      onClose={onClose}
      wide
      title={initial ? `Edit ${initial.name}` : "Add webhook"}
      description={initial ? undefined : "Acnologia posts a message to this URL when a subscribed event happens."}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="webhook-form" loading={busy}>
            {initial ? "Save" : "Add webhook"}
          </Button>
        </>
      }
    >
      <form id="webhook-form" onSubmit={submit} className="space-y-4" noValidate>
        <Field label="Name" required error={nameErr}>
          <Input
            value={form.name}
            onChange={(e) => {
              setForm({ ...form, name: e.target.value });
              if (nameErr) setNameErr(null);
            }}
            placeholder="ops channel"
            autoComplete="off"
          />
        </Field>
        <Field label="Format" hint={FORMAT_HELP[form.format]}>
          <Select value={form.format} onChange={(e) => setForm({ ...form, format: e.target.value as WebhookFormat })}>
            {options.map((f) => (
              <option key={f} value={f}>
                {f}
              </option>
            ))}
          </Select>
        </Field>
        <Field
          label="URL"
          required={!initial}
          error={urlErr}
          hint={initial ? `Currently ${initial.url_hint}. Leave blank to keep the stored URL.` : "Private network addresses are refused unless the server allows them."}
        >
          <Input
            type="url"
            value={form.url}
            onChange={(e) => {
              setForm({ ...form, url: e.target.value });
              if (urlErr) setUrlErr(null);
            }}
            placeholder={initial ? "Leave blank to keep it" : FORMAT_PLACEHOLDER[form.format]}
            autoComplete="off"
            className="font-mono"
          />
        </Field>
        <div>
          <div className="mb-1.5 text-[13px] font-medium text-ink">Events</div>
          <EventPicker types={eventTypes} value={form.events} onChange={(v) => setForm({ ...form, events: v })} />
          <div className="mt-1 text-xs text-muted">
            {form.events.length ? `Subscribed to ${plural(form.events.length, "event")}.` : "Pick none to receive every event."}
          </div>
        </div>
        <Toggle checked={form.active} onChange={(v) => setForm({ ...form, active: v })} label="Active" description="Paused webhooks receive nothing." />
      </form>
    </Modal>
  );
}

function SecretModal({ result, onClose }: { result: { name: string; secret: string } | null; onClose: () => void }) {
  return (
    <Modal
      open={!!result}
      onClose={onClose}
      title={`${result?.name} created`}
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
            <span>Copy this signing secret now. Acnologia cannot show it again.</span>
          </div>
          <SecretBox value={result.secret} />
          <p className="text-[13px] leading-relaxed text-muted">
            Each delivery carries <code className="font-mono text-xs text-ink">X-Cogito-Timestamp</code> and{" "}
            <code className="font-mono text-xs text-ink">X-Cogito-Signature</code>. Verify it on your receiver:
          </p>
          <pre className="wrap-anywhere whitespace-pre-wrap rounded-xl border border-line bg-canvas px-3 py-2.5 font-mono text-[12px] text-ink">
            X-Cogito-Signature: sha256=HMAC_SHA256(secret, "&lt;X-Cogito-Timestamp&gt;.&lt;body&gt;")
          </pre>
          <p className="text-xs text-muted">Compare in constant time and reject old timestamps.</p>
        </div>
      )}
    </Modal>
  );
}

export function WebhooksPage() {
  useTitle("Webhooks");
  const manage = can.manage(useAuth((s) => s.user?.role));
  const now = useNow(30000);
  const [hooks, setHooks] = useState<Webhook[]>([]);
  const [eventTypes, setEventTypes] = useState<WebhookEventType[]>([]);
  const [formats, setFormats] = useState<WebhookFormat[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Webhook | null>(null);
  const [secret, setSecret] = useState<{ name: string; secret: string } | null>(null);
  const [testing, setTesting] = useState<number | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await ops.listWebhooks();
      setHooks([...(res.webhooks ?? [])].sort((a, b) => a.name.localeCompare(b.name)));
      setEventTypes(res.event_types ?? []);
      setFormats(res.formats ?? []);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const toggleActive = async (h: Webhook, v: boolean) => {
    try {
      await ops.updateWebhook(h.id, { active: v });
      setHooks((list) => list.map((x) => (x.id === h.id ? { ...x, active: v } : x)));
      toast.success(v ? `${h.name} is active` : `${h.name} is paused`);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  const test = async (h: Webhook) => {
    setTesting(h.id);
    try {
      const res = await ops.testWebhook(h.id);
      if (res.ok) toast.success(`Test delivered to ${h.name}${res.status ? ` (HTTP ${res.status})` : ""}`);
      else toast.error(`Test failed: ${res.error || `HTTP ${res.status}`}`);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setTesting(null);
      void load();
    }
  };

  const openCreate = () => {
    setEditing(null);
    setFormOpen(true);
  };

  if (!loaded) return <PageSkeleton variant="list" />;

  const active = hooks.filter((h) => h.active).length;

  return (
    <div>
      <PageHeader
        title="Webhooks"
        meta={`${plural(hooks.length, "webhook")} · ${active} active`}
        actions={
          manage && (
            <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={openCreate}>
              Add webhook
            </Button>
          )
        }
      />
      <p className="-mt-4 mb-6 max-w-2xl text-[13px] leading-relaxed text-muted">
        Post Acnologia events to your own endpoint, Slack, Discord or Telegram. URLs on a private network are refused unless the server sets{" "}
        <code className="font-mono text-xs text-ink">COGITO_WEBHOOK_ALLOW_PRIVATE</code>.
      </p>

      {error && hooks.length === 0 ? (
        <Empty title="Webhooks are not available" body={error} />
      ) : hooks.length === 0 ? (
        <Empty
          icon={<WebhookIcon className="h-5 w-5" />}
          title="No webhooks yet"
          body={manage ? "Add one to hear about exhausted keys, open breakers and other events as they happen." : "Ask an admin to add one."}
          action={
            manage ? (
              <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={openCreate}>
                Add your first webhook
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="space-y-2.5">
          {hooks.map((h) => (
            <li key={h.id} className="rounded-card bg-surface p-4 shadow-card sm:px-5">
              <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-[15px] font-medium text-ink">{h.name}</span>
                    <Badge tone={h.format === "json" ? "violet" : "blue"}>{h.format}</Badge>
                    {!h.active && <Badge>paused</Badge>}
                  </div>
                  <div className="wrap-anywhere mt-1 font-mono text-xs text-muted">{h.url_hint}</div>
                  <div className="mt-1.5 flex flex-wrap gap-1">
                    {h.events?.length ? (
                      h.events.map((ev) => <Badge key={ev}>{ev}</Badge>)
                    ) : (
                      <Badge tone="neutral">All events</Badge>
                    )}
                  </div>
                  <div className="wrap-anywhere mt-1.5 font-mono text-xs text-muted">
                    <span title={dateTime(h.last_sent_at)}>last sent {relTime(h.last_sent_at, now)}</span>
                    {h.last_sent_at && h.last_status > 0 && (
                      <span className={h.last_status >= 200 && h.last_status < 300 ? undefined : "text-red-700"}> · HTTP {h.last_status}</span>
                    )}
                  </div>
                  {h.last_error && <div className="wrap-anywhere mt-1 text-xs text-red-700">{h.last_error}</div>}
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-1">
                  <Toggle checked={h.active} onChange={(v) => void toggleActive(h, v)} disabled={!manage} ariaLabel={h.active ? `${h.name} is active` : `${h.name} is paused`} />
                  {manage && (
                    <>
                      <span className="mx-1 h-5 w-px bg-line" />
                      <Button size="sm" variant="ghost" icon={<Send className="h-3.5 w-3.5" />} loading={testing === h.id} onClick={() => void test(h)}>
                        Send test
                      </Button>
                      <IconButton
                        label="Edit webhook"
                        onClick={() => {
                          setEditing(h);
                          setFormOpen(true);
                        }}
                      >
                        <Pencil className="h-4 w-4" />
                      </IconButton>
                      <ConfirmButton
                        iconOnly
                        label="Delete webhook"
                        confirmLabel="Delete"
                        onConfirm={async () => {
                          try {
                            await ops.deleteWebhook(h.id);
                            setHooks((list) => list.filter((x) => x.id !== h.id));
                            toast.success(`Deleted ${h.name}`);
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

      <WebhookFormModal
        open={formOpen}
        initial={editing}
        eventTypes={eventTypes}
        formats={formats}
        onClose={() => setFormOpen(false)}
        onSaved={(s) => {
          if (s) setSecret(s);
          void load();
        }}
      />
      <SecretModal result={secret} onClose={() => setSecret(null)} />
    </div>
  );
}
