import { useCallback, useEffect, useState, type FormEvent } from "react";
import { RiAddLine } from "@remixicon/react";
import { AcnAppToken } from "@/components/foundations/icons/acn";
import { Modal } from "../components/Modal";
import { api, errorMessage } from "../lib/api";
import { dateTime, plural, relTime } from "../lib/format";
import type { App, AppToken } from "../lib/types";
import { useApps } from "../stores/apps";
import { toast } from "../stores/toast";
import { useNow } from "../hooks/useNow";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { Input } from "@/components/base/input/input";
import { Select, SelectItem } from "@/components/base/select/select";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Switch } from "@/components/base/switch/switch";
import { Callout, EmptyState } from "@/components/acn";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { SecretBox } from "@/components/acn/copy-button";
import { cx } from "@/utils/cx";
import { OAuthConnections } from "@/components/OAuthConnections";
import {
  BLANK_LIMITS_DRAFT,
  LIMIT_FIELDS,
  LIMIT_FIELD_LABEL,
  anyLimit,
  limitsDraft,
  limitsText,
  parseLimitsDraft,
  parseTags,
  tagsText,
  type LimitsDraft,
} from "../lib/accessControls";
import type { TokenLimits } from "../lib/types";

/** A token's own limits and pinned tags, as typed in the form. */
function TokenControlsFields({
  limits,
  setLimits,
  tags,
  setTags,
  error,
}: {
  limits: LimitsDraft;
  setLimits: (d: LimitsDraft) => void;
  tags: string;
  setTags: (v: string) => void;
  error: string | null;
}) {
  return (
    <fieldset className="flex flex-col gap-3">
      <legend className="mb-1 text-body-medium text-text-primary">Limits for this token</legend>
      <p className="text-caption-1-regular text-text-secondary">Counted on top of the app's limits. Leave a field empty to use the app's per-token default.</p>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {LIMIT_FIELDS.map((k) => (
          <Input
            key={k}
            label={LIMIT_FIELD_LABEL[k]}
            inputMode={k.endsWith("_budget") ? "decimal" : "numeric"}
            value={limits[k]}
            onChange={(v) => setLimits({ ...limits, [k]: v })}
            placeholder="default"
            inputClassName="font-mono tabular-nums"
          />
        ))}
      </div>
      <Input
        label="Pinned tags"
        value={tags}
        onChange={setTags}
        placeholder="customer=cust_A"
        hint="Every call with this token carries these tags, and cannot send other values for them."
        inputClassName="font-mono"
        autoComplete="off"
      />
      {error && <Callout tone="error">{error}</Callout>}
    </fieldset>
  );
}

/** Parses the controls fields, or returns what is wrong. */
function parseControls(limits: LimitsDraft, tags: string): { limits: TokenLimits; pinned: Record<string, string> } | string {
  const l = parseLimitsDraft(limits);
  if (typeof l === "string") return l;
  const t = parseTags(tags);
  if (typeof t === "string") return t;
  return { limits: l, pinned: t };
}

const EXPIRY: { days: number; label: string }[] = [
  { days: 0, label: "Never" },
  { days: 7, label: "7 days" },
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 365, label: "1 year" },
];

type Status = "active" | "revoked" | "expired";

function statusOf(t: AppToken, now: number): Status {
  if (t.revoked_at) return "revoked";
  if (t.expires_at && Date.parse(t.expires_at) <= now) return "expired";
  return "active";
}

const META = "font-mono text-caption-1-regular text-text-secondary";

/** One token: name and status, then prefix, created, last use and expiry. */
function TokenRow({
  app,
  t,
  now,
  canEdit,
  onRevoked,
  onEdit,
}: {
  app: App;
  t: AppToken;
  now: number;
  canEdit: boolean;
  onRevoked: (t: AppToken) => void;
  onEdit: (t: AppToken) => void;
}) {
  const status = statusOf(t, now);
  const live = status === "active";
  return (
    <li
      className={cx("flex items-start justify-between gap-3 py-3 ps-4 pe-2 sm:items-center sm:gap-4", !live && "bg-background-secondary-default")}
      aria-label={`Token ${t.name}`}
    >
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className={cx("min-w-0 wrap-anywhere text-body-2-medium", live ? "text-text-primary" : "text-text-secondary")}>{t.name}</span>
          {t.read_only && (
            <Chip variant="caption" color="purple" className="font-mono">
              read-only
            </Chip>
          )}
          {status === "revoked" && (
            <Chip variant="caption" color="neutral" className="font-mono" title={dateTime(t.revoked_at)}>
              revoked
            </Chip>
          )}
          {status === "expired" && (
            <Chip variant="caption" color="rose" className="font-mono">
              expired
            </Chip>
          )}
          {anyLimit(t.limits) && (
            <Chip variant="caption" color="neutral" className="font-mono" title="This token's own limits">
              {limitsText(t.limits)}
            </Chip>
          )}
          {Object.keys(t.pinned_tags ?? {}).length > 0 && (
            <Chip variant="caption" color="neutral" className="font-mono wrap-anywhere" title="Pinned tags">
              {tagsText(t.pinned_tags)}
            </Chip>
          )}
        </div>
        <div className={cx(META, "mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 tabular-nums")}>
          <span dir="ltr">{t.prefix}</span>
          <span title={dateTime(t.created_at)}>created {relTime(t.created_at, now)}</span>
          <span title={t.last_used_at ? dateTime(t.last_used_at) : undefined}>used {relTime(t.last_used_at, now)}</span>
          {status !== "revoked" && (
            <span title={t.expires_at ? dateTime(t.expires_at) : undefined}>
              {t.expires_at ? `${status === "expired" ? "expired" : "expires"} ${relTime(t.expires_at, now)}` : "no expiry"}
            </span>
          )}
        </div>
      </div>
      {canEdit && live && (
        <div className="flex shrink-0 items-center gap-1">
          <Button size="small" variant="ghost" onClick={() => onEdit(t)} aria-label={`Limits and tags for ${t.name}`}>
            Limits
          </Button>
          <ConfirmButton
            label="Revoke"
            confirmLabel="Revoke"
            description={`Anything using ${t.name} stops working at once. ${app.name}'s other tokens keep working.`}
            onConfirm={async () => {
              try {
                onRevoked(await api.revokeAppToken(app.id, t.id));
                toast.success(`Revoked ${t.name}`);
              } catch (e) {
                toast.error(errorMessage(e));
              }
            }}
          />
        </div>
      )}
    </li>
  );
}

/**
 * An app's tokens: list, add and revoke. Each token works on its own, so a
 * leaked or retired one can go without breaking the rest. Requests and
 * spend count toward the app; a token may also have limits of its own and
 * pinned caller tags.
 */
export function AppTokensPanel({ app, canEdit, onClose }: { app: App | null; canEdit: boolean; onClose: () => void }) {
  const now = useNow(30000);
  const refreshApps = useApps((s) => s.fetch);
  const [tokens, setTokens] = useState<AppToken[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState<"list" | "new" | "edit">("list");
  const [name, setName] = useState("");
  const [nameErr, setNameErr] = useState<string | null>(null);
  const [days, setDays] = useState(0);
  const [readOnly, setReadOnly] = useState(false);
  const [limits, setLimits] = useState<LimitsDraft>(BLANK_LIMITS_DRAFT);
  const [tags, setTags] = useState("");
  const [controlsErr, setControlsErr] = useState<string | null>(null);
  const [editing, setEditing] = useState<AppToken | null>(null);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<{ token: string; info: AppToken } | null>(null);
  const id = app?.id;

  const load = useCallback(async (appId: number) => {
    try {
      const res = await api.listAppTokens(appId);
      setTokens(res.tokens ?? []);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, []);

  useEffect(() => {
    if (id === undefined) return;
    setTokens(null);
    setError(null);
    setView("list");
    setCreated(null);
    void load(id);
  }, [id, load]);

  const startNew = () => {
    setName("");
    setNameErr(null);
    setDays(0);
    setReadOnly(false);
    setLimits(BLANK_LIMITS_DRAFT);
    setTags("");
    setControlsErr(null);
    setCreated(null);
    setView("new");
  };

  const startEdit = (t: AppToken) => {
    setEditing(t);
    setLimits(limitsDraft(t.limits));
    setTags(tagsText(t.pinned_tags));
    setControlsErr(null);
    setView("edit");
  };

  const saveEdit = async (e: FormEvent) => {
    e.preventDefault();
    if (!app || !editing) return;
    const c = parseControls(limits, tags);
    if (typeof c === "string") {
      setControlsErr(c);
      return;
    }
    setBusy(true);
    try {
      const t = await api.updateAppToken(app.id, editing.id, {
        limits: anyLimit(c.limits) ? c.limits : null,
        pinned_tags: Object.keys(c.pinned).length ? c.pinned : null,
      });
      setTokens((list) => (list ?? []).map((x) => (x.id === t.id ? t : x)));
      toast.success(`Saved ${t.name}`);
      setView("list");
    } catch (err) {
      setControlsErr(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!app) return;
    const n = name.trim();
    if (!n) {
      setNameErr("Name the token after where it will live, such as staging or ci.");
      return;
    }
    const c = parseControls(limits, tags);
    if (typeof c === "string") {
      setControlsErr(c);
      return;
    }
    setBusy(true);
    try {
      const body: Parameters<typeof api.createAppToken>[1] = { name: n };
      if (days) body.expires_in_days = days;
      if (readOnly) body.read_only = true;
      if (anyLimit(c.limits)) body.limits = c.limits;
      if (Object.keys(c.pinned).length) body.pinned_tags = c.pinned;
      const res = await api.createAppToken(app.id, body);
      setCreated({ token: res.token, info: res.app_token });
      setTokens((list) => [res.app_token, ...(list ?? [])]);
      void refreshApps().catch(() => undefined);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const onRevoked = (t: AppToken) => {
    setTokens((list) => (list ?? []).map((x) => (x.id === t.id ? t : x)));
    void refreshApps().catch(() => undefined);
  };

  const live = (tokens ?? []).filter((t) => statusOf(t, now) === "active").length;
  const appName = app?.name ?? "";

  const creating = view === "new";
  const newFooter = created ? (
    <Button variant="primary" onClick={() => setView("list")}>
      I have saved it
    </Button>
  ) : (
    <>
      <Button variant="ghost" onClick={() => setView("list")}>
        Back
      </Button>
      <Button variant="primary" type="submit" form="app-token-form" disabled={busy} leadingIcon={busy ? ButtonSpinner : undefined}>
        Create token
      </Button>
    </>
  );
  const listFooter = (
    <div className="flex w-full flex-wrap items-center justify-end gap-2">
      <Button variant="ghost" onClick={onClose}>
        Close
      </Button>
      {canEdit && (
        <Button variant="primary" leadingIcon={RiAddLine} onClick={startNew}>
          New token
        </Button>
      )}
    </div>
  );

  const newBody = created ? (
    <div className="flex flex-col gap-3">
      <Callout tone="warning">Copy this token now. You will not see it again: Acnologia stores only a hash.</Callout>
      <SecretBox value={created.token} />
      <div className="flex flex-wrap items-center gap-2 font-mono text-caption-1-regular wrap-anywhere text-text-secondary">
        <span>Send it as Authorization: Bearer {created.info.prefix}</span>
        {created.info.read_only && (
          <Chip variant="caption" color="purple" className="font-mono">
            read-only
          </Chip>
        )}
      </div>
    </div>
  ) : (
    <form id="app-token-form" onSubmit={submit} noValidate className="flex flex-col gap-4">
      <Input
        label="Token name"
        isRequired
        value={name}
        onChange={(v) => {
          setName(v);
          if (nameErr) setNameErr(null);
        }}
        isInvalid={!!nameErr}
        errorMessage={nameErr}
        placeholder="staging"
        autoComplete="off"
        maxLength={64}
      />
      <Select label="Expires" selectedKey={String(days)} onSelectionChange={(k) => k != null && setDays(Number(k))} className="w-full">
        {EXPIRY.map((x) => (
          <SelectItem key={x.days} id={String(x.days)}>
            {x.label}
          </SelectItem>
        ))}
      </Select>
      <div className="rounded-2xl border border-border-button-default px-4 py-3.5">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="text-body-medium text-text-primary">Read-only token</div>
            <p className="mt-0.5 text-caption-1-regular text-text-secondary">
              Only allows GET/HEAD proxy requests and read-only MCP tools. Blocks raw key leasing and any write or mutating tool.
            </p>
          </div>
          <Switch isSelected={readOnly} onChange={setReadOnly} aria-label="Read-only token" className="mt-0.5 shrink-0" />
        </div>
      </div>
      <TokenControlsFields
        limits={limits}
        setLimits={(d) => {
          setLimits(d);
          setControlsErr(null);
        }}
        tags={tags}
        setTags={(v) => {
          setTags(v);
          setControlsErr(null);
        }}
        error={controlsErr}
      />
    </form>
  );

  const editBody = (
    <form id="app-token-edit-form" onSubmit={saveEdit} noValidate className="flex flex-col gap-4">
      <TokenControlsFields
        limits={limits}
        setLimits={(d) => {
          setLimits(d);
          setControlsErr(null);
        }}
        tags={tags}
        setTags={(v) => {
          setTags(v);
          setControlsErr(null);
        }}
        error={controlsErr}
      />
    </form>
  );
  const editFooter = (
    <>
      <Button variant="ghost" onClick={() => setView("list")}>
        Back
      </Button>
      <Button variant="primary" type="submit" form="app-token-edit-form" disabled={busy} leadingIcon={busy ? ButtonSpinner : undefined}>
        Save
      </Button>
    </>
  );

  const listBody = (
    <div data-autofocus tabIndex={-1} className="outline-none">
      {!tokens && !error ? (
        <div className="flex flex-col gap-2" role="status" aria-busy="true">
          <span className="sr-only">Loading</span>
          <Skeleton className="h-14 rounded-xl" />
          <Skeleton className="h-14 rounded-xl" />
        </div>
      ) : error && !tokens ? (
        <Callout tone="error">Could not load tokens: {error}</Callout>
      ) : tokens && tokens.length === 0 ? (
        <EmptyState compact icon={AcnAppToken} title="No tokens" body="This app has no tokens. Create one to let it call Acnologia." />
      ) : (
        <ul aria-label={`Tokens for ${appName}`} className="divide-y divide-separator-border overflow-hidden rounded-2xl border border-border-button-default">
          {(tokens ?? []).map((t) => (
            <TokenRow key={t.id} app={app!} t={t} now={now} canEdit={canEdit} onRevoked={onRevoked} onEdit={startEdit} />
          ))}
        </ul>
      )}
      {app && <OAuthConnections appId={app.id} canEdit={canEdit} />}
      {!canEdit && <p className="mt-3 text-caption-1-regular text-text-secondary">Ask an admin to add or revoke tokens.</p>}
    </div>
  );

  const editingView = view === "edit" && !!editing;
  return (
    <Modal
      open={!!app}
      onClose={onClose}
      wide
      title={
        editingView
          ? `Limits for ${editing.name}`
          : creating
            ? created
              ? `Token ${created.info.name} created`
              : `New token for ${appName}`
            : `Tokens for ${appName}`
      }
      description={
        editingView
          ? "Calls with this token count toward these limits and the app's."
          : creating
            ? created
              ? undefined
              : "The app's other tokens keep working. Requests and spend still count toward its limits."
            : "Give each place that calls this app its own token, so you can revoke one without breaking the rest."
      }
      headerAction={
        !creating && !editingView && tokens ? (
          <Chip variant="caption" color="neutral" className="font-mono">
            {plural(live, "active token")}
          </Chip>
        ) : undefined
      }
      footer={editingView ? editFooter : creating ? newFooter : listFooter}
    >
      {editingView ? editBody : creating ? newBody : listBody}
    </Modal>
  );
}
