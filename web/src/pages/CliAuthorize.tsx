import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { RiCheckboxCircleLine, RiCloseCircleLine, RiTerminalBoxLine } from "@remixicon/react";
import { useAuth } from "@/stores/auth";
import { errorMessage } from "@/lib/api";
import { dateTime, relTime } from "@/lib/format";
import { ops, type CliDeviceRequest, type TokenScope } from "@/lib/ops";
import type { Role } from "@/lib/types";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { Label } from "@/components/base/input/label";
import { RadioGroup } from "@/components/base/radio/radio";
import { RadioCard } from "@/components/base/radio/radio-card";
import { ButtonSpinner, Spinner } from "@/components/base/spinner/spinner";
import { Callout } from "@/components/acn/callout";
import { AuthShell } from "./AuthShell";

const SCOPES_FOR: Record<Role, TokenScope[]> = {
  member: ["read"],
  admin: ["read", "write"],
  superadmin: ["read", "write", "admin"],
};

const SCOPE_TEXT: Record<TokenScope, string> = {
  read: "Read only, as a member",
  write: "Read and manage, as an admin",
  admin: "Everything, as a superadmin",
};

const CARD =
  "flex w-full max-w-[400px] flex-col rounded-3xl border border-border-button-default bg-background-primary-default p-6 shadow-xs sm:p-8 dark:bg-background-secondary-default";

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
  | { kind: "ask" }
  | { kind: "error"; message: string }
  | { kind: "pending"; req: CliDeviceRequest }
  | { kind: "approved"; scope: TokenScope }
  | { kind: "denied" };

/**
 * /cli/authorize?code=XXXX-XXXX: approve or deny an `acn login`. It sits
 * behind the access gate and sign in like every page, and both bring the
 * browser back here with the code.
 */
export function CliAuthorizePage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const code = (params.get("code") ?? "").trim().toUpperCase();
  const user = useAuth((s) => s.user);
  const scopes = user ? SCOPES_FOR[user.role] : [];
  const [view, setView] = useState<View>({ kind: code ? "loading" : "ask" });
  // Start at write where the role allows it: enough for keys add and
  // checks, short of superadmin powers.
  const [scope, setScope] = useState<TokenScope>(() => (scopes.includes("write") ? "write" : "read"));
  const [busy, setBusy] = useState<"approve" | "deny" | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const [typed, setTyped] = useState("");

  useEffect(() => {
    if (!code) {
      setView({ kind: "ask" });
      return;
    }
    let live = true;
    setView({ kind: "loading" });
    ops
      .cliLookup(code)
      .then(({ request }) => {
        if (!live) return;
        if (request.state === "denied") setView({ kind: "denied" });
        else if (request.state !== "pending") setView({ kind: "error", message: "This sign in was already approved. Go back to your terminal." });
        else setView({ kind: "pending", req: request });
      })
      .catch((e) => live && setView({ kind: "error", message: errorMessage(e) }));
    return () => {
      live = false;
    };
  }, [code]);

  const decide = async (approve: boolean) => {
    if (view.kind !== "pending") return;
    setBusy(approve ? "approve" : "deny");
    setActionErr(null);
    try {
      if (approve) {
        await ops.cliApprove({ user_code: view.req.user_code, scope });
        setView({ kind: "approved", scope });
      } else {
        await ops.cliDeny(view.req.user_code);
        setView({ kind: "denied" });
      }
    } catch (e) {
      setActionErr(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  const submitCode = (e: FormEvent) => {
    e.preventDefault();
    const c = typed.trim().toUpperCase();
    if (c) navigate(`/cli/authorize?code=${encodeURIComponent(c)}`);
  };

  const icon = <RiTerminalBoxLine className="size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />;
  let body: ReactNode;

  if (view.kind === "loading") {
    body = (
      <div className="flex justify-center py-10" role="status" aria-live="polite">
        <Spinner size="lg" aria-label="Loading the sign in request" />
      </div>
    );
  } else if (view.kind === "approved") {
    body = (
      <Card icon={<RiCheckboxCircleLine className="size-5 shrink-0 text-status-lime-text" aria-hidden />} title="CLI authorized">
        <p className="mt-1.5 text-body-regular text-text-secondary" role="status">
          You can close this tab and go back to your terminal.
        </p>
        <p className="mt-4 text-caption-1-regular text-text-secondary">
          The token has the <span className="font-mono">{view.scope}</span> scope and lasts 90 days. Revoke it any time under Account, API tokens.
        </p>
      </Card>
    );
  } else if (view.kind === "denied") {
    body = (
      <Card icon={<RiCloseCircleLine className="size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />} title="Sign in denied">
        <p className="mt-1.5 text-body-regular text-text-secondary" role="status">
          The terminal stops waiting and gets no token. You can close this tab.
        </p>
      </Card>
    );
  } else if (view.kind === "ask" || view.kind === "error") {
    body = (
      <Card icon={icon} title="Authorize the Acnologia CLI">
        <p className="mt-1.5 text-body-regular text-text-secondary">
          Enter the code your terminal shows after <span className="font-mono">acn login</span>.
        </p>
        <form onSubmit={submitCode} className="mt-6 flex flex-col gap-4">
          {view.kind === "error" && <Callout tone="error">{view.message}</Callout>}
          <Input
            label="Code"
            placeholder="XXXX-XXXX"
            autoComplete="off"
            inputDir="ltr"
            value={typed}
            onChange={setTyped}
            autoFocus
          />
          <Button type="submit" className="w-full" disabled={!typed.trim()}>
            Continue
          </Button>
        </form>
      </Card>
    );
  } else {
    const req = view.req;
    body = (
      <Card icon={icon} title="Authorize the Acnologia CLI">
        <p className="mt-1.5 text-body-regular text-text-secondary">
          A terminal wants to sign in as <span className="wrap-anywhere text-text-primary">{user?.email}</span>. Approve only if you just ran{" "}
          <span className="font-mono">acn login</span> there.
        </p>

        <div className="mt-6 rounded-2xl bg-background-tertiary-default px-4 py-3 text-center">
          <div className="text-caption-1-medium text-text-secondary">Check this matches your terminal</div>
          <div className="mt-1 font-mono text-title-1-medium tracking-[0.12em] text-text-primary" data-testid="cli-code">
            {req.user_code}
          </div>
        </div>

        <dl className="mt-4 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-body-regular">
          <dt className="text-text-secondary">Device</dt>
          <dd className="wrap-anywhere font-mono text-text-primary">{req.hostname}</dd>
          <dt className="text-text-secondary">System</dt>
          <dd className="font-mono text-text-primary">{req.os || "unknown"}</dd>
          <dt className="text-text-secondary">Requested</dt>
          <dd className="text-text-primary" title={dateTime(req.created_at)}>
            {relTime(req.created_at)}
          </dd>
        </dl>

        <RadioGroup
          value={scope}
          onChange={(v) => setScope(v as TokenScope)}
          className="mt-6 flex flex-col gap-1.5"
          isDisabled={!!busy}
        >
          <Label>Access</Label>
          {scopes.map((s) => (
            <RadioCard key={s} value={s} title={s} description={SCOPE_TEXT[s]} />
          ))}
        </RadioGroup>
        <p className="mt-2 text-caption-1-regular text-text-secondary">
          The CLI gets a token that lasts 90 days. Revoke it any time under Account, API tokens.
        </p>

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
            disabled={!!busy}
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
    <AuthShell meta="Command line sign in" title="Authorize the CLI">
      {body}
    </AuthShell>
  );
}
