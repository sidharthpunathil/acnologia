import { useCallback, useEffect, useState } from "react";
import { errorMessage } from "@/lib/api";
import { dateTime, relTime } from "@/lib/format";
import { oauth, type OAuthConnection } from "@/lib/oauth";
import { useAuth } from "@/stores/auth";
import { toast } from "@/stores/toast";
import { useNow } from "@/hooks/useNow";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { Section } from "@/components/acn/section";
import { cx } from "@/utils/cx";

const META = "font-mono text-caption-1-regular text-text-secondary";
const SCOPE_COLOR: Record<string, ChipColor> = { read: "neutral", write: "blue", admin: "purple" };

/**
 * MCP clients signed in to the control MCP (/mcp/control) with OAuth: each
 * acts as the person who approved it, like an admin API token with the
 * scope they picked. Yours, or everyone's for a superadmin. Shows nothing
 * when there are none.
 */
export function ControlConnections() {
  const now = useNow(30000);
  const isSuper = useAuth((s) => s.user?.role === "superadmin");
  const [list, setList] = useState<OAuthConnection[] | null>(null);

  const load = useCallback(async () => {
    try {
      setList((await oauth.controlConnections()).connections ?? []);
    } catch {
      setList([]);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!list || list.length === 0) return null;

  return (
    <Section
      title="Control MCP sign ins"
      description={
        isSuper
          ? "MCP clients that signed in to the control MCP. Each acts as the person who approved it and can change this server within its scope."
          : "MCP clients you signed in to the control MCP. Each acts as you and can change this server within its scope."
      }
    >
      <ul className="divide-y divide-separator-border overflow-hidden rounded-2xl border border-border-button-default">
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
                  {c.access && (
                    <Chip variant="caption" color={SCOPE_COLOR[c.access] ?? "neutral"} className="font-mono">
                      {c.access}
                    </Chip>
                  )}
                  {!live && (
                    <Chip variant="caption" color="neutral" className="font-mono" title={dateTime(c.revoked_at)}>
                      revoked
                    </Chip>
                  )}
                </div>
                <div className={cx(META, "mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 tabular-nums")}>
                  {isSuper && <span className="wrap-anywhere">by {c.user_email || "a deleted user"}</span>}
                  <span title={dateTime(c.created_at)}>connected {relTime(c.created_at, now)}</span>
                  <span title={c.last_used_at ? dateTime(c.last_used_at) : undefined}>used {relTime(c.last_used_at, now)}</span>
                </div>
              </div>
              {live && (
                <div className="shrink-0">
                  <ConfirmButton
                    label="Revoke"
                    confirmLabel="Revoke"
                    description={`${c.client_name} loses access to the control MCP at once and has to sign in again.`}
                    onConfirm={async () => {
                      try {
                        const g = await oauth.revokeControl(c.id);
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
    </Section>
  );
}
