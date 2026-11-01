import { useEffect, useMemo, useState } from "react";
import { RiRefreshLine, RiShieldCheckLine } from "@remixicon/react";
import { useTitle } from "@/hooks/useTitle";
import { useNow } from "@/hooks/useNow";
import { useUsers } from "@/stores/users";
import { toast } from "@/stores/toast";
import { errorMessage } from "@/lib/api";
import { dateTime, plural, relTime } from "@/lib/format";
import { Button } from "@/components/base/buttons/button";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { SearchField } from "@/components/base/search-field/search-field";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Table, TableBody, TableCell, TableColumn, TableHeader, TableRow } from "@/components/base/table/table";
import { EmptyState } from "@/components/acn/empty-state";
import { PageHeader } from "@/components/acn/page-header";
import { PageSkeleton } from "@/components/acn/page-skeleton";

function actionColor(action: string): ChipColor {
  if (action.includes("reveal")) return "yellow";
  if (action.includes("delete")) return "rose";
  if (action.includes("create")) return "lime";
  return "neutral";
}

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
          <Button variant="secondary" disabled={busy} leadingIcon={busy ? ButtonSpinner : RiRefreshLine} onClick={() => void load()}>
            Refresh
          </Button>
        }
      />
      <SearchField
        value={q}
        onChange={setQ}
        placeholder="Filter by person, action or target"
        aria-label="Filter the audit log"
        enterKeyHint="search"
        className="mb-5 max-w-md"
      />
      <p className="sr-only" aria-live="polite">
        {q.trim() ? `${plural(shown.length, "entry", "entries")} match` : ""}
      </p>
      {shown.length === 0 ? (
        <EmptyState
          icon={RiShieldCheckLine}
          title={audit.length ? "No entries match" : "Nothing audited yet"}
          body={audit.length ? "Try another filter." : "Changes to services, keys, apps and people, plus every secret reveal, are recorded here."}
        />
      ) : (
        <div className="overflow-hidden rounded-3xl border border-border-button-default bg-background-primary-default">
          <Table aria-label="Audit log" size="sm" className="table-fixed [&_th]:border-t-0">
            <TableHeader>
              <TableColumn className="w-28 sm:w-44">Action</TableColumn>
              <TableColumn isRowHeader>What</TableColumn>
              <TableColumn className="hidden w-28 text-end sm:table-cell">When</TableColumn>
            </TableHeader>
            <TableBody>
              {shown.map((a) => (
                <TableRow key={a.id} id={a.id}>
                  <TableCell className="align-top">
                    <div className="flex flex-col items-start gap-1">
                      <Chip variant="caption" color={actionColor(a.action)} className="max-w-full truncate font-mono">
                        {a.action}
                      </Chip>
                      {/* On phones the time sits under the action, so each entry is one line shorter. */}
                      <span className="font-mono text-caption-2-regular text-text-tertiary sm:hidden" title={dateTime(a.ts)}>
                        {relTime(a.ts, now)}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell className="align-top">
                    <div className="min-w-0 text-body-2-regular text-text-primary">
                      <span className="text-body-2-medium wrap-anywhere">{a.user_email || "system"}</span>
                      {a.target && <span className="font-mono text-caption-1-regular wrap-anywhere text-text-secondary"> · {a.target}</span>}
                    </div>
                    {a.detail && (
                      <div className="line-clamp-2 text-caption-1-regular wrap-anywhere text-text-secondary" title={a.detail}>
                        {a.detail}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="hidden whitespace-nowrap text-end align-top sm:table-cell">
                    <span className="font-mono text-caption-1-regular text-text-tertiary" title={dateTime(a.ts)}>{relTime(a.ts, now)}</span>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
