import { useCallback, useEffect, useState } from "react";
import { errorMessage } from "@/lib/api";
import { dateTime, relTime } from "@/lib/format";
import { oauth, type OAuthConnection } from "@/lib/oauth";
import { toast } from "@/stores/toast";
import { useNow } from "@/hooks/useNow";
import { Chip } from "@/components/base/badges/chip";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { cx } from "@/utils/cx";

const META = "font-mono text-caption-1-regular text-text-secondary";

/**
 * The MCP clients signed in to an app through OAuth (claude.ai, ChatGPT,
 * Claude Code, Cursor). Each approval is one connection; revoking it ends
 * its access and refresh tokens. Shows nothing when there are none.
 */
export function OAuthConnections({ appId, canEdit }: { appId: number; canEdit: boolean }) {
  const now = useNow(30000);
  const [list, setList] = useState<OAuthConnection[] | null>(null);

  const load = useCallback(async () => {
    try {
      setList((await oauth.connections(appId)).connections ?? []);
    } catch {
      setList([]);
    }
  }, [appId]);

  useEffect(() => {
    setList(null);
    void load();
  }, [load]);

  if (!list || list.length === 0) return null;

  return (
    <section className="mt-5" aria-label="MCP sign ins">
      <h3 className="text-body-2-medium text-text-primary">MCP sign ins</h3>
      <p className="mt-0.5 text-caption-1-regular text-text-secondary">Clients that connected by signing in. Their tokens renew on their own until revoked.</p>
      <ul className="mt-2 divide-y divide-separator-border overflow-hidden rounded-2xl border border-border-button-default">
        {list.map((c) => {
          const live = !c.revoked_at;
          return (
            <li
              key={c.id}
              className={cx("flex items-start justify-between gap-3 py-3 ps-4 pe-2 sm:items-center sm:gap-4", !live && "bg-background-secondary-default")}
            >
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className={cx("min-w-0 wrap-anywhere text-body-2-medium", live ? "text-text-primary" : "text-text-secondary")}>{c.client_name}</span>
                  {c.read_only && (
                    <Chip variant="caption" color="purple" className="font-mono">
                      read-only
                    </Chip>
                  )}
                  {!live && (
                    <Chip variant="caption" color="neutral" className="font-mono" title={dateTime(c.revoked_at)}>
                      revoked
                    </Chip>
                  )}
                </div>
                <div className={cx(META, "mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 tabular-nums")}>
                  <span className="wrap-anywhere">by {c.user_email || "a deleted user"}</span>
                  <span title={dateTime(c.created_at)}>connected {relTime(c.created_at, now)}</span>
                  <span title={c.last_used_at ? dateTime(c.last_used_at) : undefined}>used {relTime(c.last_used_at, now)}</span>
                </div>
              </div>
              {canEdit && live && (
                <div className="shrink-0">
                  <ConfirmButton
                    label="Revoke"
                    confirmLabel="Revoke"
                    description={`${c.client_name} loses access at once and has to sign in again.`}
                    onConfirm={async () => {
                      try {
                        const g = await oauth.revoke(appId, c.id);
                        setList((xs) => (xs ?? []).map((x) => (x.id === g.id ? g : x)));
                        toast.success(`Revoked ${c.client_name}`);
                      } catch (e) {
                        toast.error(errorMessage(e));
                      }
                    }}
                  />
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
