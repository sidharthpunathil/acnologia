import { useCallback, useEffect, useState, type FormEvent } from "react";
import { RiAddLine, RiKey2Line } from "@remixicon/react";
import { useAuth } from "@/stores/auth";
import { toast } from "@/stores/toast";
import { errorMessage } from "@/lib/api";
import { dateTime, relTime } from "@/lib/format";
import { ops, type AdminToken, type TokenScope } from "@/lib/ops";
import type { Role } from "@/lib/types";
import { useNow } from "@/hooks/useNow";
import { Button } from "@/components/base/buttons/button";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { Input } from "@/components/base/input/input";
import { Select, SelectItem } from "@/components/base/select/select";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Table, TableBody, TableCell, TableColumn, TableHeader, TableRow } from "@/components/base/table/table";
import { Callout } from "@/components/acn/callout";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { CopyButton, SecretBox } from "@/components/acn/copy-button";
import { EmptyState } from "@/components/acn/empty-state";
import { Section } from "@/components/acn/section";
import { cx } from "@/utils/cx";
import { Modal } from "./Modal";

/** The scopes a role may give a token, lowest first. */
export const SCOPES_FOR: Record<Role, TokenScope[]> = {
  member: ["read"],
  admin: ["read", "write"],
  superadmin: ["read", "write", "admin"],
};

export const SCOPE_HELP: Record<TokenScope, string> = {
  read: "Acts as a member: reads services, keys, apps and activity.",
  write: "Acts as an admin: also manages services, keys, apps and providers.",
  admin: "Acts as a superadmin: also hidden keys, secret reveals, users and the audit log.",
};

const SCOPE_COLOR: Record<TokenScope, ChipColor> = { read: "neutral", write: "blue", admin: "purple" };

/** Table cells carry body type from the table styles; meta text sets its own. */
const META = "font-mono text-caption-1-regular text-text-secondary";

export const EXPIRY: { days: number; label: string }[] = [
  { days: 0, label: "Never" },
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 365, label: "1 year" },
];

function NewTokenModal({ result, onClose }: { result: { token: string; info: AdminToken } | null; onClose: () => void }) {
  const cmd = result ? `acnologia ctl login ${window.location.origin} --token ${result.token}` : "";
  return (
    <Modal
      open={!!result}
      onClose={onClose}
      title={`${result?.info.name ?? "Token"} created`}
      footer={
        <Button variant="primary" onClick={onClose}>
          I have saved it
        </Button>
      }
    >
      {result && (
        <div className="flex flex-col gap-4">
          <Callout tone="warning">Copy this token now. Acnologia stores only a hash, so it cannot show the token again.</Callout>
          <SecretBox value={result.token} />
          <div>
            <div className="mb-1.5 text-body-medium text-text-primary">Use it with the CLI</div>
            <div className="flex items-start gap-2 rounded-2lg bg-background-tertiary-default py-1.5 ps-3 pe-1.5">
              <code dir="ltr" className="min-w-0 flex-1 break-all pt-1.5 font-mono text-caption-1-regular text-text-primary">
                {cmd}
              </code>
              <CopyButton text={cmd} />
            </div>
          </div>
          <p className="font-mono text-caption-1-regular wrap-anywhere text-text-secondary">
            Or send it as Authorization: Bearer {result.info.token_prefix}...
          </p>
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
  // The token made in this visit fades into the list instead of appearing at once.
  const [freshId, setFreshId] = useState<number | null>(null);

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
      setFreshId(res.info.id);
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
      description="For scripts and the acnologia CLI. A token acts as you, limited to the scope you pick. Tokens cannot manage tokens or change your account."
    >
      <form onSubmit={submit} noValidate className="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_auto_auto_auto] sm:items-start">
        <Input
          label="Name"
          value={name}
          onChange={(v) => {
            setName(v);
            if (nameErr) setNameErr(null);
          }}
          isInvalid={!!nameErr}
          errorMessage={nameErr}
          placeholder="laptop cli"
          autoComplete="off"
        />
        <Select
          label="Scope"
          selectedKey={scope}
          onSelectionChange={(k) => k != null && setScope(k as TokenScope)}
          isDisabled={scopes.length < 2}
          triggerClassName="sm:min-w-28"
        >
          {scopes.map((s) => (
            <SelectItem key={s} id={s}>
              {s}
            </SelectItem>
          ))}
        </Select>
        <Select
          label="Expires"
          selectedKey={String(days)}
          onSelectionChange={(k) => k != null && setDays(Number(k))}
          triggerClassName="sm:min-w-28"
        >
          {EXPIRY.map((x) => (
            <SelectItem key={x.days} id={String(x.days)}>
              {x.label}
            </SelectItem>
          ))}
        </Select>
        <div className="sm:pt-6">
          <Button
            type="submit"
            variant="primary"
            disabled={busy}
            leadingIcon={busy ? ButtonSpinner : RiAddLine}
            className="w-full sm:w-auto"
          >
            Create token
          </Button>
        </div>
      </form>
      <p className="mt-1.5 text-caption-1-medium text-text-secondary">{SCOPE_HELP[scope]}</p>

      <div className="mt-5">
        {!loaded ? (
          <div className="flex flex-col gap-2" role="status" aria-busy="true">
            <span className="sr-only">Loading</span>
            <Skeleton className="h-12 rounded-xl" />
            <Skeleton className="h-12 rounded-xl" />
          </div>
        ) : error && tokens.length === 0 ? (
          <Callout tone="error">Could not load tokens: {error}</Callout>
        ) : tokens.length === 0 ? (
          <EmptyState compact icon={RiKey2Line} title="No tokens yet" body="Create one above for a script or the CLI." />
        ) : (
          <div className="overflow-hidden rounded-2xl border border-border-button-default">
            <Table aria-label="API tokens" size="sm" className="[&_th:first-child]:border-t-0 [&_th]:border-t-0">
              <TableHeader>
                <TableColumn isRowHeader>Token</TableColumn>
                <TableColumn className="hidden md:table-cell">Created</TableColumn>
                <TableColumn className="hidden md:table-cell">Last used</TableColumn>
                <TableColumn className="hidden sm:table-cell">Expires</TableColumn>
                <TableColumn className="w-0">
                  <span className="sr-only">Actions</span>
                </TableColumn>
              </TableHeader>
              <TableBody>
                {tokens.map((t) => {
                  const expired = !!t.expires_at && Date.parse(t.expires_at) <= now;
                  return (
                    <TableRow key={t.id} id={t.id} className={cx(t.id === freshId && "fade-up")}>
                      <TableCell>
                        <div className="flex min-w-0 flex-wrap items-center gap-2">
                          <span className="min-w-0 wrap-anywhere text-body-2-medium text-text-primary">{t.name}</span>
                          <Chip variant="caption" color={SCOPE_COLOR[t.scope]} className="font-mono">
                            {t.scope}
                          </Chip>
                          {expired && (
                            <Chip variant="caption" color="rose" className="font-mono">
                              expired
                            </Chip>
                          )}
                        </div>
                        <div className="mt-0.5 font-mono text-caption-1-regular text-text-secondary">
                          {t.token_prefix}...
                          <span className="md:hidden">
                            {" "}
                            · used {relTime(t.last_used_at, now)}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className="hidden whitespace-nowrap md:table-cell">
                        <span className={META} title={dateTime(t.created_at)}>{relTime(t.created_at, now)}</span>
                      </TableCell>
                      <TableCell className="hidden whitespace-nowrap md:table-cell">
                        <span className={META} title={dateTime(t.last_used_at)}>{relTime(t.last_used_at, now)}</span>
                      </TableCell>
                      <TableCell className="hidden whitespace-nowrap sm:table-cell">
                        {t.expires_at ? (
                          <span className={META} title={dateTime(t.expires_at)}>
                            {expired ? "expired" : "expires"} {relTime(t.expires_at, now)}
                          </span>
                        ) : (
                          <span className={META}>never</span>
                        )}
                      </TableCell>
                      <TableCell className="text-end">
                        <ConfirmButton
                          label="Revoke"
                          confirmLabel="Revoke"
                          description={`Scripts using ${t.name} stop working at once.`}
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
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      <NewTokenModal result={created} onClose={() => setCreated(null)} />
    </Section>
  );
}
