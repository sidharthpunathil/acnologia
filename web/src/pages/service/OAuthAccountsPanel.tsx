import { useCallback, useEffect, useRef, useState } from "react";
import { RiAddLine, RiExternalLinkLine, RiLoopLeftLine, RiPencilLine } from "@remixicon/react";
import { errorMessage, oauthApi } from "../../lib/api";
import type { OAuthConnectState, OAuthSettings, Service } from "../../lib/types";
import { toast } from "../../stores/toast";
import { Button } from "@/components/base/buttons/button";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Input } from "@/components/base/input/input";
import { Callout, CopyButton } from "@/components/acn";
import { CARD } from "./parts";

/**
 * Accounts of a service that signs in with OAuth (auth type oauth2): the
 * OAuth client, the redirect URI to register with the provider, the
 * connected accounts and Connect account. Each account is a key in the
 * pool; its tokens never reach the browser.
 */

/** Fired by the header's Connect account button; the panel runs the sign in. */
const CONNECT_EVENT = "acn:oauth-connect";

const POLL_MS = 2000;
const POLL_LIMIT_MS = 15 * 60 * 1000;

/** Panels listening now, and a request made while none was (another tab was open). */
let listening = 0;
let requested = false;

/** The page header's button. It asks the panel on the Keys tab to start a sign in. */
export function OAuthConnectButton({ className, onClick }: { className?: string; onClick?: () => void }) {
  return (
    <Button
      variant="primary"
      leadingIcon={RiAddLine}
      className={className}
      onClick={() => {
        onClick?.();
        if (listening > 0) window.dispatchEvent(new Event(CONNECT_EVENT));
        else requested = true;
      }}
    >
      Connect account
    </Button>
  );
}

/** The settings with the tenant, for a provider whose OAuth URLs take one (Microsoft Graph). */
type TenantSettings = OAuthSettings & { takes_tenant?: boolean; tenant?: string; default_tenant?: string };

function expiresIn(iso?: string): string {
  if (!iso) return "no expiry";
  const ms = new Date(iso).getTime() - Date.now();
  if (ms <= 0) return "token expired, renews on next use";
  const min = Math.round(ms / 60000);
  return min < 1 ? "token renews within a minute" : `token renews in ${min} min`;
}

export function OAuthAccountsPanel({ service, canManage, onChange }: { service: Service; canManage: boolean; onChange: () => void }) {
  const [settings, setSettings] = useState<TenantSettings | null>(null);
  const [editing, setEditing] = useState(false);
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [scopes, setScopes] = useState("");
  const [tenant, setTenant] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [pending, setPending] = useState<OAuthConnectState | null>(null);
  const [starting, setStarting] = useState(false);
  const panelRef = useRef<HTMLElement>(null);
  const timer = useRef<number | undefined>(undefined);

  const load = useCallback(async () => {
    try {
      const s: TenantSettings = await oauthApi.settings(service.id);
      setSettings(s);
      setClientId(s.client_id);
      setScopes(s.scopes.join(" "));
      setTenant(s.tenant ?? "");
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }, [service.id]);

  useEffect(() => {
    void load();
    return () => window.clearTimeout(timer.current);
  }, [load]);

  const poll = useCallback(
    (st: OAuthConnectState, until: number) => {
      timer.current = window.setTimeout(async () => {
        try {
          const cur = await oauthApi.status(st.connect_id);
          if (cur.status === "done") {
            setPending(null);
            toast.success(cur.account ? `Connected ${cur.account}.` : "Account connected.");
            onChange();
            void load();
            return;
          }
          if (cur.status === "failed" || cur.status === "expired") {
            setPending(null);
            toast.error(cur.status === "expired" ? "The sign in link expired. Try again." : cur.error || "The sign in failed.");
            void load();
            return;
          }
        } catch {
          // A dropped poll is retried on the next tick.
        }
        if (Date.now() < until) poll(st, until);
        else setPending(null);
      }, POLL_MS);
    },
    [load, onChange],
  );

  /** Starts a sign in. The tab opens now, inside the click, so it is not blocked as a popup. */
  const connect = useCallback(
    async (keyId?: number) => {
      if (!settings) return;
      const missing = settings.fields.find((f) => !f.optional && !fields[f.key]?.trim());
      if (!keyId && missing) {
        toast.error(`${missing.label} is required.`);
        panelRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
        return;
      }
      const win = window.open("", "_blank");
      if (win) win.opener = null;
      setStarting(true);
      try {
        const st = keyId ? await oauthApi.reconnect(keyId) : await oauthApi.connect(service.id, fields);
        if (win && st.url) win.location.href = st.url;
        setPending(st);
        window.clearTimeout(timer.current);
        poll(st, Date.now() + POLL_LIMIT_MS);
      } catch (e) {
        win?.close();
        toast.error(errorMessage(e));
      } finally {
        setStarting(false);
      }
    },
    [settings, fields, service.id, poll],
  );

  useEffect(() => {
    const onEvent = () => {
      if (settings && !settings.configured) {
        setEditing(true);
        panelRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
        return;
      }
      void connect();
    };
    window.addEventListener(CONNECT_EVENT, onEvent);
    listening++;
    if (requested && settings) {
      requested = false;
      onEvent();
    }
    return () => {
      listening--;
      window.removeEventListener(CONNECT_EVENT, onEvent);
    };
  }, [connect, settings]);

  const save = async () => {
    setSaving(true);
    try {
      const body: { client_id: string; client_secret?: string; scopes: string[]; tenant?: string } = {
        client_id: clientId.trim(),
        scopes: scopes.split(/[\s,]+/).filter(Boolean),
      };
      if (clientSecret.trim()) body.client_secret = clientSecret.trim();
      if (settings?.takes_tenant) body.tenant = tenant.trim();
      const s: TenantSettings = await oauthApi.setClient(service.id, body);
      setSettings(s);
      setTenant(s.tenant ?? "");
      setClientSecret("");
      setEditing(false);
      toast.success("OAuth client saved.");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  if (!settings) return null;
  const showForm = canManage && (editing || !settings.configured);

  return (
    <section ref={panelRef} aria-labelledby="oauth-accounts-title" className={`${CARD} mb-4 scroll-mt-4 p-4 sm:p-5`}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 id="oauth-accounts-title" className="text-body-1-semibold text-text-primary">
            Signed in accounts
          </h2>
          <p className="mt-0.5 text-body-2-regular text-text-secondary">
            {settings.configured
              ? "Each connected account is a key in this pool. Tokens renew on their own."
              : "This provider signs in with OAuth instead of API keys. Set up its OAuth app first."}
          </p>
        </div>
        {canManage && settings.configured && (
          <div className="flex flex-wrap gap-2">
            {!editing && (
              <Button variant="secondary" size="small" leadingIcon={RiPencilLine} onClick={() => setEditing(true)}>
                OAuth app
              </Button>
            )}
            <Button variant="primary" size="small" leadingIcon={starting ? ButtonSpinner : RiAddLine} disabled={starting} onClick={() => void connect()}>
              Connect account
            </Button>
          </div>
        )}
      </div>

      {showForm && (
        <div className="mt-4 space-y-3">
          <div className="rounded-2xl bg-background-secondary-default p-3">
            <p className="text-body-2-medium text-text-primary">Redirect URI</p>
            <p className="mt-0.5 text-body-2-regular text-text-secondary">Register this exact URI in the OAuth app you create with the provider.</p>
            <div className="mt-2 flex items-center gap-2">
              <code className="wrap-anywhere min-w-0 flex-1 text-body-2-regular text-text-primary">{settings.redirect_uri}</code>
              <CopyButton text={settings.redirect_uri} label="Copy redirect URI" iconOnly />
            </div>
          </div>
          {!settings.public_url_set && (
            <Callout tone="warning">Set ACNOLOGIA_PUBLIC_URL so this URI stays the same behind a proxy.</Callout>
          )}
          <div className="grid gap-3 sm:grid-cols-2">
            <Input label="Client ID" value={clientId} onChange={setClientId} autoComplete="off" />
            <Input
              label="Client secret"
              type="password"
              revealable
              value={clientSecret}
              onChange={setClientSecret}
              autoComplete="off"
              placeholder={settings.has_client_secret ? "Saved; leave empty to keep it" : ""}
            />
          </div>
          <Input
            label="Scopes"
            value={scopes}
            onChange={setScopes}
            autoComplete="off"
            hint={settings.default_scopes.length ? `Leave empty for the provider's defaults: ${settings.default_scopes.join(" ")}` : "Separate scopes with spaces."}
          />
          {settings.takes_tenant && (
            <Input
              label="Tenant"
              value={tenant}
              onChange={setTenant}
              autoComplete="off"
              placeholder={settings.default_tenant}
              hint={`Leave empty for ${settings.default_tenant || "the provider's default"}. Use organizations, consumers, or your tenant ID or domain for an app registered for one organization.`}
            />
          )}
          <div className="flex flex-wrap justify-end gap-2">
            {settings.configured && (
              <Button variant="ghost" size="small" onClick={() => setEditing(false)}>
                Cancel
              </Button>
            )}
            <Button variant="primary" size="small" leadingIcon={saving ? ButtonSpinner : undefined} disabled={saving || !clientId.trim()} onClick={() => void save()}>
              Save
            </Button>
          </div>
        </div>
      )}

      {canManage && settings.configured && settings.fields.length > 0 && (
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          {settings.fields.map((f) => (
            <Input
              key={f.key}
              label={f.optional ? `${f.label} (optional)` : f.label}
              type={f.public ? "text" : "password"}
              revealable={!f.public}
              value={fields[f.key] ?? ""}
              onChange={(v) => setFields((cur) => ({ ...cur, [f.key]: v }))}
              autoComplete="off"
            />
          ))}
          <p className="text-caption-1-regular text-text-secondary sm:col-span-2">Sent with each request of the next account you connect.</p>
        </div>
      )}

      {pending && (
        <Callout
          tone="info"
          className="mt-4"
          title="Waiting for the sign in"
          action={
            pending.url ? (
              <Button variant="secondary" size="small" leadingIcon={RiExternalLinkLine} onClick={() => window.open(pending.url, "_blank", "noopener")}>
                Open
              </Button>
            ) : undefined
          }
        >
          Approve access in the tab that opened, or send the link to the account's owner. It works once, for 15 minutes.
          {pending.url && (
            <span className="mt-2 flex items-center gap-2">
              <span className="wrap-anywhere min-w-0 flex-1 text-caption-1-regular">{pending.url}</span>
              <CopyButton text={pending.url} label="Copy sign in link" iconOnly />
            </span>
          )}
        </Callout>
      )}

      {settings.connections.length > 0 && (
        <ul className="mt-4 divide-y divide-separator-border rounded-2xl border border-border-button-default">
          {settings.connections.map((c) => (
            <li key={c.key_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <p className="wrap-anywhere text-body-2-medium text-text-primary">{c.account || c.label}</p>
                <p className="text-caption-1-regular text-text-secondary">
                  {c.needs_reconnect ? "Access was revoked or expired. Reconnect to use it again." : c.last_error || expiresIn(c.expires_at)}
                </p>
              </div>
              {canManage && (
                <Button
                  variant={c.needs_reconnect ? "primary" : "ghost"}
                  size="small"
                  leadingIcon={RiLoopLeftLine}
                  disabled={starting}
                  onClick={() => void connect(c.key_id)}
                >
                  Reconnect
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
