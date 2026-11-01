import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Focusable } from "react-aria-components";
import { RiAddLine, RiPencilLine, RiSendPlaneLine, RiWebhookLine } from "@remixicon/react";
import { useTitle } from "../hooks/useTitle";
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
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Input } from "@/components/base/input/input";
import { hintTextClassName } from "@/components/base/input/hint-text";
import { Select, SelectItem } from "@/components/base/select/select";
import { Switch } from "@/components/base/switch/switch";
import { Checkbox } from "@/components/base/checkbox/checkbox";
import { Chip } from "@/components/base/badges/chip";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout, EmptyState, PageHeader, PageSkeleton } from "@/components/acn";
import { cx } from "@/utils/cx";
import { UnpluggedArt } from "@/components/foundations/illustrations";

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
  const fade = useScrollFade();
  if (!types.length)
    return (
      <div className="rounded-2xl border border-dashed border-border-button-hover px-3 py-3 text-caption-1-regular text-text-secondary">
        No event types reported by the server.
      </div>
    );
  const set = new Set(value);
  return (
    <div className="relative overflow-hidden rounded-2xl border border-border-button-default bg-background-primary-default">
      <div
        role="group"
        aria-labelledby="webhook-events-label"
        aria-describedby="webhook-events-hint"
        onScroll={fade.onScroll}
        className="max-h-56 divide-y divide-separator-border overflow-y-auto overscroll-contain"
      >
        {types.map((t) => {
          const on = set.has(t.type);
          return (
            <Checkbox
              key={t.type}
              isSelected={on}
              onChange={(next) => onChange(next ? [...value, t.type] : value.filter((v) => v !== t.type))}
              aria-label={t.type}
              className="flex w-full items-start px-3 py-2.5 transition-colors duration-150 ease hover:bg-background-secondary-default"
            >
              <span className="flex min-w-0 flex-col">
                <span className="wrap-anywhere font-mono text-caption-1-medium text-text-primary">{t.type}</span>
                {t.description && <span className="mt-0.5 text-caption-1-regular text-text-secondary">{t.description}</span>}
              </span>
            </Checkbox>
          );
        })}
      </div>
      <ScrollFade progress={fade.progress} />
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
          <Button variant="primary" type="submit" form="webhook-form" disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
            {initial ? "Save" : "Add webhook"}
          </Button>
        </>
      }
    >
      <form id="webhook-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Input
          label="Name"
          isRequired
          validationBehavior="aria"
          isInvalid={!!nameErr}
          errorMessage={nameErr}
          value={form.name}
          onChange={(name) => {
            setForm({ ...form, name });
            if (nameErr) setNameErr(null);
          }}
          placeholder="ops channel"
          autoComplete="off"
        />
        <div className="flex flex-col gap-1">
          <Select
            label="Format"
            aria-describedby="webhook-format-hint"
            selectedKey={form.format}
            onSelectionChange={(k) => k != null && setForm({ ...form, format: k as WebhookFormat })}
            className="w-full"
            popoverClassName="w-(--trigger-width)"
          >
            {options.map((f) => (
              <SelectItem key={f} id={f} textValue={f}>
                <span className="font-mono">{f}</span>
              </SelectItem>
            ))}
          </Select>
          <p id="webhook-format-hint" className={cx(hintTextClassName(false), "wrap-anywhere")}>
            {FORMAT_HELP[form.format]}
          </p>
        </div>
        <Input
          label="URL"
          type="url"
          isRequired={!initial}
          validationBehavior="aria"
          isInvalid={!!urlErr}
          errorMessage={urlErr}
          hint={initial ? `Currently ${initial.url_hint}. Leave blank to keep the stored URL.` : "Private network addresses are refused unless the server allows them."}
          value={form.url}
          onChange={(url) => {
            setForm({ ...form, url });
            if (urlErr) setUrlErr(null);
          }}
          placeholder={initial ? "Leave blank to keep it" : FORMAT_PLACEHOLDER[form.format]}
          autoComplete="off"
          fieldClassName="font-mono"
        />
        <div className="flex flex-col gap-1.5">
          <div id="webhook-events-label" className="text-body-medium text-text-primary">
            Events
          </div>
          <EventPicker types={eventTypes} value={form.events} onChange={(v) => setForm({ ...form, events: v })} />
          <p id="webhook-events-hint" className={hintTextClassName(false)}>
            {form.events.length ? `Subscribed to ${plural(form.events.length, "event")}.` : "Pick none to receive every event."}
          </p>
        </div>
        <div className="flex items-center justify-between gap-4 rounded-2xl border border-border-button-default p-3.5">
          <div className="min-w-0">
            <div id="webhook-active-label" className="text-body-medium text-text-primary">
              Active
            </div>
            <div id="webhook-active-hint" className="text-caption-1-regular text-text-secondary">
              Paused webhooks receive nothing.
            </div>
          </div>
          <Switch
            aria-labelledby="webhook-active-label"
            aria-describedby="webhook-active-hint"
            isSelected={form.active}
            onChange={(active) => setForm({ ...form, active })}
          />
        </div>
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
        <div className="flex flex-col gap-3">
          <Callout tone="warning">Copy this signing secret now. Acnologia cannot show it again.</Callout>
          <SecretBox value={result.secret} />
          <p className="text-body-2-regular text-text-secondary">
            Each delivery carries <code className="font-mono text-caption-1-regular text-text-primary">X-Cogito-Timestamp</code> and{" "}
            <code className="font-mono text-caption-1-regular text-text-primary">X-Cogito-Signature</code>. Verify it on your receiver:
          </p>
          <pre
            dir="ltr"
            className="wrap-anywhere whitespace-pre-wrap rounded-2xl bg-code-bg px-3.5 py-3 font-mono text-caption-1-regular text-code-ink"
          >
            X-Cogito-Signature: sha256=HMAC_SHA256(secret, "&lt;X-Cogito-Timestamp&gt;.&lt;body&gt;")
          </pre>
          <p className="text-caption-1-regular text-text-secondary">Compare in constant time and reject old timestamps.</p>
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
        className="mb-4"
        actions={
          manage && (
            <Button variant="primary" leadingIcon={RiAddLine} onClick={openCreate}>
              Add webhook
            </Button>
          )
        }
      />
      <p className="mb-6 max-w-2xl text-body-2-regular text-text-secondary">
        Post Acnologia events to your own endpoint, Slack, Discord or Telegram. URLs on a private network are refused unless the server sets{" "}
        <code className="font-mono text-caption-1-regular text-text-primary">COGITO_WEBHOOK_ALLOW_PRIVATE</code>.
      </p>

      {error && hooks.length === 0 ? (
        <EmptyState title="Webhooks are not available" body={error} />
      ) : hooks.length === 0 ? (
        <EmptyState
          art={UnpluggedArt}
          icon={RiWebhookLine}
          title="No webhooks yet"
          body={manage ? "Add one to hear about exhausted keys, open breakers and other events as they happen." : "Ask an admin to add one."}
          action={
            manage ? (
              <Button variant="primary" leadingIcon={RiAddLine} onClick={openCreate}>
                Add your first webhook
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="flex flex-col gap-3">
          {hooks.map((h) => {
            const failed = !!h.last_sent_at && h.last_status > 0 && (h.last_status < 200 || h.last_status >= 300);
            return (
              <li key={h.id} className="rounded-3xl border border-border-button-default bg-background-primary-default p-5">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className={cx("min-w-0 wrap-anywhere text-headline-medium", h.active ? "text-text-primary" : "text-text-secondary")}>{h.name}</span>
                      <Chip variant="caption" color={h.format === "json" ? "purple" : "blue"} className="font-mono">
                        {h.format}
                      </Chip>
                      {!h.active && (
                        <Chip variant="caption" color="neutral">
                          paused
                        </Chip>
                      )}
                    </div>
                    <div className="mt-1 wrap-anywhere font-mono text-caption-1-regular text-text-secondary" dir="ltr">
                      {h.url_hint}
                    </div>
                    <div className="mt-2 flex flex-wrap gap-1">
                      {h.events?.length ? (
                        h.events.map((ev) => (
                          <Chip key={ev} variant="caption" color="neutral" className="font-mono">
                            {ev}
                          </Chip>
                        ))
                      ) : (
                        <Chip variant="caption" color="neutral">
                          All events
                        </Chip>
                      )}
                    </div>
                    <div className="mt-2 wrap-anywhere font-mono text-caption-1-regular text-text-secondary">
                      <span title={dateTime(h.last_sent_at)}>last sent {relTime(h.last_sent_at, now)}</span>
                      {h.last_sent_at && h.last_status > 0 && (
                        <span className={failed ? "text-text-error-primary" : undefined}> · HTTP {h.last_status}</span>
                      )}
                    </div>
                    {h.last_error && <div className="mt-1 wrap-anywhere text-caption-1-regular text-text-error-primary">{h.last_error}</div>}
                  </div>
                  <div className="flex shrink-0 flex-wrap items-center gap-1 border-t border-separator-border pt-3 sm:border-0 sm:pt-0">
                    <Switch
                      size="sm"
                      isSelected={h.active}
                      onChange={(v) => void toggleActive(h, v)}
                      isDisabled={!manage}
                      aria-label={`${h.name} active`}
                      className="min-h-11 pe-1 sm:min-h-0"
                    >
                      <span className="text-body-2-regular text-text-secondary">{h.active ? "Active" : "Paused"}</span>
                    </Switch>
                    {manage && (
                      <>
                        <span aria-hidden className="mx-1.5 h-5 w-px bg-separator-border" />
                        <Button
                          size="small"
                          variant="ghost"
                          leadingIcon={testing === h.id ? ButtonSpinner : RiSendPlaneLine}
                          disabled={testing === h.id}
                          aria-busy={testing === h.id || undefined}
                          onClick={() => void test(h)}
                          className="max-sm:h-11"
                        >
                          Send test
                        </Button>
                        <TooltipTrigger delay={400}>
                          <Focusable>
                            <IconButton
                              icon={RiPencilLine}
                              aria-label={`Edit ${h.name}`}
                              className="max-sm:size-11"
                              onClick={() => {
                                setEditing(h);
                                setFormOpen(true);
                              }}
                            />
                          </Focusable>
                          <Tooltip>Edit webhook</Tooltip>
                        </TooltipTrigger>
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
            );
          })}
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
