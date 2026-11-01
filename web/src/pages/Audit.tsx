import { useEffect, useMemo, useState } from "react";
import { useTitle } from "../hooks/useTitle";
import { RefreshCw, Search, ShieldCheck } from "lucide-react";
import { useUsers } from "../stores/users";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { dateTime, plural, relTime } from "../lib/format";
import { useNow } from "../hooks/useNow";
import { Badge, Button, Empty, Input, PageHeader, PageSkeleton } from "../components/ui";

export function AuditPage() {
  useTitle("Audit");
  const { audit, auditLoaded, fetchAudit } = useUsers();
  const now = useNow(30000);
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState(false);

  const load = async () => {
    setBusy(true);
    try {
      await fetchAudit();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const shown = useMemo(() => {
    const n = q.trim().toLowerCase();
    if (!n) return audit;
    return audit.filter((a) => [a.user_email, a.action, a.target, a.detail].some((f) => f?.toLowerCase().includes(n)));
  }, [audit, q]);

  if (!auditLoaded) return <PageSkeleton variant="list" />;

  return (
    <div>
      <PageHeader
        title="Audit log"
        meta={`${plural(audit.length, "entry", "entries")} · who changed what, and when`}
        actions={
          <Button icon={<RefreshCw className="h-4 w-4" />} loading={busy} onClick={() => void load()}>
            Refresh
          </Button>
        }
      />
      <div className="relative mb-5 max-w-md">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
        <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter by person, action or target" aria-label="Filter the audit log" enterKeyHint="search" className="pl-9" />
      </div>
      {shown.length === 0 ? (
        <Empty
          icon={<ShieldCheck className="h-5 w-5" />}
          title={audit.length ? "No entries match" : "Nothing audited yet"}
          body={audit.length ? "Try another filter." : "Changes to services, keys, apps and people, plus every secret reveal, are recorded here."}
        />
      ) : (
        <ul className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card">
          {shown.map((a) => (
            <li key={a.id} className="flex flex-col gap-1 px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
              {/* On phones the time sits beside the action, so each entry is one line shorter. */}
              <div className="flex items-center justify-between gap-3 sm:w-40 sm:shrink-0">
                <Badge tone={a.action.includes("reveal") ? "amber" : a.action.includes("delete") ? "red" : "neutral"}>{a.action}</Badge>
                <span className="shrink-0 font-mono text-[11px] text-faint sm:hidden" title={dateTime(a.ts)}>
                  {relTime(a.ts, now)}
                </span>
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-[13px] text-ink">
                  <span className="font-medium">{a.user_email || "system"}</span>
                  {a.target && <span className="font-mono text-xs text-muted"> · {a.target}</span>}
                </div>
                {a.detail && <div className="wrap-anywhere line-clamp-2 text-xs text-muted" title={a.detail}>{a.detail}</div>}
              </div>
              <div className="hidden shrink-0 font-mono text-[11px] text-faint sm:block" title={dateTime(a.ts)}>
                {relTime(a.ts, now)}
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
