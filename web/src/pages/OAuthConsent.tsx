import { useEffect, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { RiCloseCircleLine, RiPlugLine } from "@remixicon/react";
import { useAuth } from "@/stores/auth";
import { errorMessage } from "@/lib/api";
import { oauth, type OAuthRequestInfo } from "@/lib/oauth";
import type { TokenScope } from "@/lib/ops";
import { SCOPE_HELP } from "@/components/ApiTokens";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { Select, SelectItem } from "@/components/base/select/select";
import { ButtonSpinner, Spinner } from "@/components/base/spinner/spinner";
import { Switch } from "@/components/base/switch/switch";
import { Callout } from "@/components/acn/callout";
import { AuthShell } from "./AuthShell";

const CARD =
  "flex w-full max-w-[400px] flex-col rounded-3xl border border-border-button-default bg-background-primary-default p-6 shadow-xs sm:p-8 dark:bg-background-secondary-default";

const NEW_APP = "new";

function Card({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <div className={CARD}>
      <h1 className="flex items-center gap-2 text-title-2-medium text-text-primary">
        {icon}
        {title}
      </h1>
      {children}
    </div>
  );
}

type View =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ask"; info: OAuthRequestInfo }
  | { kind: "leaving"; to: string; approved: boolean };

/** What a control MCP connection can do at each scope, in plain words. */
const CONTROL_RISK: Record<TokenScope, string> = {
  read: "It can see your services, keys, apps and activity, but not change them.",
  write: "This connection can change your server: add and delete services, keys and apps, and change providers.",
  admin: "This connection can change your server and everyone's access: hidden keys, secret reveals, users and the audit log.",
};

/**
 * /oauth/consent?request=<id>: an MCP client (claude.ai, ChatGPT, Claude
 * Code, Cursor) asks to connect. For /mcp the person picks the app it acts
 * as and whether it is read only. For the control MCP (/mcp/control) it acts
 * as the person, and they pick the scope, read by default. Then the browser
 * goes back to the client.
 */
export function OAuthConsentPage() {
  const [params] = useSearchParams();
  const id = params.get("request") ?? "";
  const user = useAuth((s) => s.user);
  const [view, setView] = useState<View>({ kind: id ? "loading" : "error", message: "This link is missing its request. Start the connection again from your app." });
  const [choice, setChoice] = useState<string>("");
  const [newName, setNewName] = useState("");
  const [nameErr, setNameErr] = useState<string | null>(null);
  const [readOnly, setReadOnly] = useState(false);
  const [scope, setScope] = useState<TokenScope>("read");
  const [busy, setBusy] = useState<"approve" | "deny" | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    let live = true;
    oauth
      .request(id)
      .then((info) => {
        if (!live) return;
        setView({ kind: "ask", info });
        setChoice(info.apps.length ? String(info.apps[0].id) : NEW_APP);
        setNewName(info.request.client_name);
      })
      .catch((e) => live && setView({ kind: "error", message: errorMessage(e) }));
    return () => {
      live = false;
    };
  }, [id]);

  const leave = (to: string, approved: boolean) => {
    setView({ kind: "leaving", to, approved });
    window.location.assign(to);
  };

  const decide = async (approve: boolean) => {
    if (view.kind !== "ask") return;
    setActionErr(null);
    const control = view.info.request.kind === "control";
    if (approve && !control && choice === NEW_APP && !newName.trim()) {
      setNameErr("Name the new app.");
      return;
    }
    setBusy(approve ? "approve" : "deny");
    try {
      if (approve) {
        const body = control
          ? { scope }
          : choice === NEW_APP
            ? { new_app: newName.trim(), read_only: readOnly }
            : { app_id: Number(choice), read_only: readOnly };
        leave((await oauth.approve(id, body)).redirect, true);
      } else {
        leave((await oauth.deny(id)).redirect, false);
      }
    } catch (e) {
      setActionErr(errorMessage(e));
      setBusy(null);
    }
  };

  const icon = <RiPlugLine className="size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />;
  let body: ReactNode;

  if (view.kind === "loading") {
    body = (
      <div className="flex justify-center py-10" role="status" aria-live="polite">
        <Spinner size="lg" aria-label="Loading the connection request" />
      </div>
    );
  } else if (view.kind === "error") {
    body = (
      <Card icon={<RiCloseCircleLine className="size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />} title="Cannot connect">
        <div className="mt-4">
          <Callout tone="error">{view.message}</Callout>
        </div>
      </Card>
    );
  } else if (view.kind === "leaving") {
    body = (
      <Card icon={icon} title={view.approved ? "Connected" : "Request denied"}>
        <p className="mt-1.5 text-body-regular text-text-secondary" role="status">
          Taking you back to the app.{" "}
          <a href={view.to} className="text-text-primary underline underline-offset-2">
            Continue
          </a>{" "}
          if nothing happens.
        </p>
      </Card>
    );
  } else {
    const { request: req, apps, scopes, can_approve } = view.info;
    const control = req.kind === "control";
    body = (
      <Card icon={icon} title={control ? `Let ${req.client_name} manage this server` : `Connect ${req.client_name}`}>
        {control ? (
          <p className="mt-1.5 text-body-regular text-text-secondary">
            <span className="wrap-anywhere text-text-primary">{req.client_name}</span> wants the control MCP: the dashboard's tools, acting as you.
            Approve only if you just started this from that app.
          </p>
        ) : (
          <p className="mt-1.5 text-body-regular text-text-secondary">
            <span className="wrap-anywhere text-text-primary">{req.client_name}</span> wants to use this server's MCP tools as one of your apps.
            Approve only if you just started this from that app.
          </p>
        )}

        <dl className="mt-5 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-body-regular">
          {req.verified_domain && (
            <>
              <dt className="text-text-secondary">Client</dt>
              <dd className="wrap-anywhere font-mono text-text-primary">{req.verified_domain}</dd>
            </>
          )}
          <dt className="text-text-secondary">Returns to</dt>
          <dd className="wrap-anywhere font-mono text-text-primary" title={req.redirect_uri}>
            {req.redirect_host}
          </dd>
          <dt className="text-text-secondary">Signed in as</dt>
          <dd className="wrap-anywhere text-text-primary">{user?.email}</dd>
        </dl>

        {control && can_approve ? (
          <div className="mt-6 flex flex-col gap-4">
            <Select
              label="Access"
              selectedKey={scope}
              onSelectionChange={(k) => k != null && setScope(k as TokenScope)}
              className="w-full"
              isDisabled={!!busy || scopes.length < 2}
            >
              {scopes.map((s) => (
                <SelectItem key={s} id={s}>
                  {s}
                </SelectItem>
              ))}
            </Select>
            <p className="-mt-2 text-caption-1-regular text-text-secondary">{SCOPE_HELP[scope]}</p>
            <Callout tone={scope === "read" ? "info" : "warning"}>{CONTROL_RISK[scope]}</Callout>
            <p className="text-caption-1-regular text-text-secondary">
              It can never do more than your own role, and stops if your role drops below this scope. Revoke it any time under API tokens on your
              account page.
            </p>
          </div>
        ) : control ? (
          <div className="mt-6">
            <Callout tone="warning">Sign in to the dashboard yourself to approve this, or deny it.</Callout>
          </div>
        ) : can_approve ? (
          <div className="mt-6 flex flex-col gap-4">
            <Select
              label="Acts as app"
              selectedKey={choice}
              onSelectionChange={(k) => k != null && setChoice(String(k))}
              className="w-full"
              isDisabled={!!busy}
            >
              {[
                ...apps.map((a) => (
                  <SelectItem key={a.id} id={String(a.id)}>
                    {a.active ? a.name : `${a.name} (paused)`}
                  </SelectItem>
                )),
                <SelectItem key={NEW_APP} id={NEW_APP}>
                  New app
                </SelectItem>,
              ]}
            </Select>
            {choice === NEW_APP && (
              <Input
                label="New app name"
                value={newName}
                onChange={(v) => {
                  setNewName(v);
                  if (nameErr) setNameErr(null);
                }}
                isInvalid={!!nameErr}
                errorMessage={nameErr}
                maxLength={100}
                autoComplete="off"
                isDisabled={!!busy}
              />
            )}
            <div className="rounded-2xl border border-border-button-default px-4 py-3.5">
              <div className="flex items-start justify-between gap-4">
                <div className="min-w-0">
                  <div className="text-body-medium text-text-primary">Read only</div>
                  <p className="mt-0.5 text-caption-1-regular text-text-secondary">Only read only tools. No raw keys and nothing that changes data.</p>
                </div>
                <Switch isSelected={readOnly} onChange={setReadOnly} aria-label="Read only" className="mt-0.5 shrink-0" isDisabled={!!busy} />
              </div>
            </div>
            <p className="text-caption-1-regular text-text-secondary">
              The app's rules and limits apply. Revoke the connection any time from the app's tokens.
            </p>
          </div>
        ) : (
          <div className="mt-6">
            <Callout tone="warning">Only an admin can connect an app. Ask one to approve this, or deny it.</Callout>
          </div>
        )}

        {actionErr && (
          <div className="mt-4">
            <Callout tone="error">{actionErr}</Callout>
          </div>
        )}

        <div className="mt-6 grid grid-cols-2 gap-3">
          <Button
            variant="secondary"
            onClick={() => void decide(false)}
            disabled={!!busy}
            aria-busy={busy === "deny" || undefined}
            leadingIcon={busy === "deny" ? ButtonSpinner : undefined}
          >
            Deny
          </Button>
          <Button
            variant="primary"
            onClick={() => void decide(true)}
            disabled={!!busy || !can_approve}
            aria-busy={busy === "approve" || undefined}
            leadingIcon={busy === "approve" ? ButtonSpinner : undefined}
          >
            Approve
          </Button>
        </div>
      </Card>
    );
  }

  return (
    <AuthShell meta="MCP sign in" title="Connect an app">
      {body}
    </AuthShell>
  );
}
