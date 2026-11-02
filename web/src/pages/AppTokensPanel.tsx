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
import { Callout, EmptyState } from "@/components/acn";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { SecretBox } from "@/components/acn/copy-button";
import { cx } from "@/utils/cx";

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
function TokenRow({ app, t, now, canEdit, onRevoked }: { app: App; t: AppToken; now: number; canEdit: boolean; onRevoked: (t: AppToken) => void }) {
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
        <div className="shrink-0">
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
 * leaked or retired one can go without breaking the rest. Requests, spend
 * and limits are still counted per app.
 */
export function AppTokensPanel({ app, canEdit, onClose }: { app: App | null; canEdit: boolean; onClose: () => void }) {
  const now = useNow(30000);
  const refreshApps = useApps((s) => s.fetch);
  const [tokens, setTokens] = useState<AppToken[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState<"list" | "new">("list");
  const [name, setName] = useState("");
  const [nameErr, setNameErr] = useState<string | null>(null);
  const [days, setDays] = useState(0);
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
    setCreated(null);
    setView("new");
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!app) return;
    const n = name.trim();
    if (!n) {
      setNameErr("Name the token after where it will live, such as staging or ci.");
      return;
    }
    setBusy(true);
    try {
      const res = await api.createAppToken(app.id, days ? { name: n, expires_in_days: days } : { name: n });
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
      <div className="font-mono text-caption-1-regular wrap-anywhere text-text-secondary">Send it as Authorization: Bearer {created.info.prefix}</div>
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
    </form>
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
            <TokenRow key={t.id} app={app!} t={t} now={now} canEdit={canEdit} onRevoked={onRevoked} />
          ))}
        </ul>
      )}
      {!canEdit && <p className="mt-3 text-caption-1-regular text-text-secondary">Ask an admin to add or revoke tokens.</p>}
    </div>
  );

  return (
    <Modal
      open={!!app}
      onClose={onClose}
      wide
      title={creating ? (created ? `Token ${created.info.name} created` : `New token for ${appName}`) : `Tokens for ${appName}`}
      description={
        creating
          ? created
            ? undefined
            : "The app's other tokens keep working. Requests and spend still count toward its limits."
          : "Give each place that calls this app its own token, so you can revoke one without breaking the rest."
      }
      headerAction={
        !creating && tokens ? (
          <Chip variant="caption" color="neutral" className="font-mono">
            {plural(live, "active token")}
          </Chip>
        ) : undefined
      }
      footer={creating ? newFooter : listFooter}
    >
      {creating ? newBody : listBody}
    </Modal>
  );
}
