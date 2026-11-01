import { useCallback, useEffect, useState, type FormEvent } from "react";
import { KeyRound, Plus, TriangleAlert } from "lucide-react";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { dateTime, relTime } from "../lib/format";
import { ops, type AdminToken, type TokenScope } from "../lib/ops";
import type { Role } from "../lib/types";
import { useNow } from "../hooks/useNow";
import { Modal } from "./Modal";
import { CopyButton, SecretBox } from "./CopyButton";
import { ConfirmButton } from "./ConfirmButton";
import { Badge, Button, Field, Input, Section, Select, Skeleton } from "./ui";

const SCOPES_FOR: Record<Role, TokenScope[]> = {
  member: ["read"],
  admin: ["read", "write"],
  superadmin: ["read", "write", "admin"],
};

const SCOPE_HELP: Record<TokenScope, string> = {
  read: "Acts as a member: reads services, keys, apps and activity.",
  write: "Acts as an admin: also manages services, keys, apps and providers.",
  admin: "Acts as a superadmin: also hidden keys, secret reveals, users and the audit log.",
};

const SCOPE_TONE: Record<TokenScope, "neutral" | "blue" | "violet"> = { read: "neutral", write: "blue", admin: "violet" };

const EXPIRY: { days: number; label: string }[] = [
  { days: 0, label: "Never" },
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 365, label: "1 year" },
];

function NewTokenModal({ result, onClose }: { result: { token: string; info: AdminToken } | null; onClose: () => void }) {
  const cmd = result ? `cogito ctl login ${window.location.origin} --token ${result.token}` : "";
  return (
    <Modal
      open={!!result}
      onClose={onClose}
      title={`${result?.info.name} created`}
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
            <span>Copy this token now. Cogito stores only a hash, so it cannot show the token again.</span>
          </div>
          <SecretBox value={result.token} />
          <div>
            <div className="mb-1.5 text-[13px] font-medium text-ink">Use it with the CLI</div>
            <div className="flex items-start gap-2 rounded-xl border border-line bg-canvas px-3 py-2.5">
              <code className="min-w-0 flex-1 break-all font-mono text-[12px] text-ink">{cmd}</code>
              <CopyButton text={cmd} />
            </div>
          </div>
          <p className="wrap-anywhere font-mono text-[11px] text-muted">Or send it as Authorization: Bearer {result.info.token_prefix}...</p>
        </div>
      )}
    </Modal>
  );
}

/** The signed in user's admin API tokens: list, create and revoke. */
export function ApiTokens() {
  const role = useAuth((s) => s.user?.role);
  const scopes = role ? SCOPES_FOR[role] : [];
  const now = useNow(30000);
  const [tokens, setTokens] = useState<AdminToken[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [nameErr, setNameErr] = useState<string | null>(null);
  const [scope, setScope] = useState<TokenScope>("read");
  const [days, setDays] = useState(90);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<{ token: string; info: AdminToken } | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await ops.listTokens();
      setTokens([...(res.tokens ?? [])].sort((a, b) => b.created_at.localeCompare(a.created_at)));
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

  // Keep the chosen scope within what the role allows.
  useEffect(() => {
    if (scopes.length && !scopes.includes(scope)) setScope(scopes[0]);
  }, [scopes, scope]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const n = name.trim();
    if (!n) {
      setNameErr("Name the token after where it will live, such as laptop cli.");
      return;
    }
    setBusy(true);
    try {
      const res = await ops.createToken({ name: n, scope, expires_in_days: days });
      setCreated(res);
      setName("");
      setTokens((list) => [res.info, ...list.filter((t) => t.id !== res.info.id)]);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section
      title="API tokens"
      description="For scripts and the cogito CLI. A token acts as you, limited to the scope you pick. Tokens cannot manage tokens or change your account."
    >
      <form onSubmit={submit} noValidate className="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_auto_auto_auto] sm:items-start">
        <Field label="Name" error={nameErr}>
          <Input
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              if (nameErr) setNameErr(null);
            }}
            placeholder="laptop cli"
            autoComplete="off"
          />
        </Field>
        <Field label="Scope">
          <Select value={scope} onChange={(e) => setScope(e.target.value as TokenScope)} disabled={scopes.length < 2}>
            {scopes.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Expires">
          <Select value={days} onChange={(e) => setDays(Number(e.target.value))}>
            {EXPIRY.map((x) => (
              <option key={x.days} value={x.days}>
                {x.label}
              </option>
            ))}
          </Select>
        </Field>
        <div className="sm:pt-[26px]">
          <Button type="submit" variant="primary" loading={busy} icon={<Plus className="h-4 w-4" />} className="w-full sm:w-auto">
            Create token
          </Button>
        </div>
      </form>
      <p className="mt-1.5 text-xs text-muted">{SCOPE_HELP[scope]}</p>

      <div className="mt-5">
        {!loaded ? (
          <div className="space-y-2" role="status" aria-busy="true">
            <span className="sr-only">Loading</span>
            <Skeleton className="h-12" />
            <Skeleton className="h-12" />
          </div>
        ) : error && tokens.length === 0 ? (
          <div className="text-[13px] text-red-700">Could not load tokens: {error}</div>
        ) : tokens.length === 0 ? (
          <div className="flex items-center gap-2 rounded-xl border border-dashed border-line px-3 py-3 text-xs text-muted">
            <KeyRound className="h-4 w-4 shrink-0" />
            No tokens yet.
          </div>
        ) : (
          <ul className="divide-y divide-line rounded-xl border border-line">
            {tokens.map((t) => {
              const expired = !!t.expires_at && Date.parse(t.expires_at) <= now;
              return (
                <li key={t.id} className="flex flex-col gap-2 px-3 py-2.5 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-[13px] font-medium text-ink">{t.name}</span>
                      <Badge tone={SCOPE_TONE[t.scope]}>{t.scope}</Badge>
                      {expired && <Badge tone="red">expired</Badge>}
                    </div>
                    <div className="wrap-anywhere mt-0.5 font-mono text-xs text-muted">
                      {t.token_prefix}... · <span title={dateTime(t.created_at)}>created {relTime(t.created_at, now)}</span> ·{" "}
                      <span title={dateTime(t.last_used_at)}>used {relTime(t.last_used_at, now)}</span> ·{" "}
                      {t.expires_at ? (
                        <span title={dateTime(t.expires_at)}>
                          {expired ? "expired" : "expires"} {relTime(t.expires_at, now)}
                        </span>
                      ) : (
                        "never expires"
                      )}
                    </div>
                  </div>
                  <div className="shrink-0">
                    <ConfirmButton
                      label="Revoke"
                      confirmLabel="Revoke"
                      onConfirm={async () => {
                        try {
                          await ops.deleteToken(t.id);
                          setTokens((list) => list.filter((x) => x.id !== t.id));
                          toast.success(`Revoked ${t.name}`);
                        } catch (e) {
                          toast.error(errorMessage(e));
                        }
                      }}
                    />
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      <NewTokenModal result={created} onClose={() => setCreated(null)} />
    </Section>
  );
}
