import { useCallback, useEffect, useMemo, useState } from "react";
import { RiArrowDownSLine, RiPlugLine, RiRefreshLine } from "@remixicon/react";
import { api, errorMessage } from "../../lib/api";
import { dateTime, plural, relTime } from "../../lib/format";
import type { Service, ServiceMCPTool, ServiceMCPToolAccess, ServiceMCPView } from "../../lib/types";
import { toast } from "../../stores/toast";
import { useNow } from "../../hooks/useNow";
import { Callout, EmptyState, Section } from "@/components/acn";
import { Chip } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { SearchField } from "@/components/base/search-field/search-field";
import { Select, SelectItem } from "@/components/base/select/select";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { cx } from "@/utils/cx";

function SchemaBlock({ label, schema }: { label: string; schema: unknown }) {
  return (
    <div>
      <div className="mb-1 font-mono text-caption-2-semibold uppercase text-text-tertiary">{label}</div>
      <pre className="max-h-64 overflow-auto rounded-xl border border-separator-border bg-background-secondary-default p-3 font-mono text-caption-1-regular text-text-primary">
        {JSON.stringify(schema, null, 2)}
      </pre>
    </div>
  );
}

// The select needs a non-empty key for "follow the server" (access "").
const UPSTREAM_KEY = "upstream";

const ACCESS_OPTIONS: { id: ServiceMCPToolAccess; label: string }[] = [
  { id: "", label: "Follow server" },
  { id: "read", label: "Always read-only" },
  { id: "write", label: "Always write" },
];

function ToolRow({
  tool,
  canManage,
  saving,
  onAccess,
}: {
  tool: ServiceMCPTool;
  canManage: boolean;
  saving: boolean;
  onAccess: (access: ServiceMCPToolAccess) => void;
}) {
  const [open, setOpen] = useState(false);
  const hasSchema = tool.inputSchema != null || tool.outputSchema != null;
  const shown = tool.public_name || tool.name;
  const renamed = tool.public_name && tool.public_name !== tool.name;
  const access = tool.access ?? "";
  const source = access
    ? `Set by an admin. The server says ${tool.upstream_read_only ? "read-only" : "write"}.`
    : "As the server says. Read-only apps and tokens only get read-only tools.";

  return (
    <li className="px-4 py-3.5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="break-all font-mono text-body-2-medium text-text-primary">{shown}</span>
            <Chip variant="caption" color={tool.read_only ? "lime" : "neutral"} className="font-mono" title={source}>
              {tool.read_only ? "read-only" : "write"}
            </Chip>
            {access && (
              <span className="text-caption-1-regular text-text-tertiary" title={source}>
                override
              </span>
            )}
            {renamed && (
              <span className="font-mono text-caption-1-regular text-text-tertiary">upstream: {tool.name}</span>
            )}
          </div>
          {tool.title && tool.title !== tool.name && tool.title !== shown && (
            <div className="mt-0.5 text-caption-1-medium text-text-primary">{tool.title}</div>
          )}
          {tool.description && (
            <p className="mt-1 whitespace-pre-line text-caption-1-regular text-text-secondary">{tool.description}</p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {canManage && (
            <Select
              size="sm"
              aria-label={`Read-only setting for ${shown}`}
              selectedKey={access || UPSTREAM_KEY}
              isDisabled={saving}
              onSelectionChange={(k) => {
                if (k == null) return;
                const next = (String(k) === UPSTREAM_KEY ? "" : String(k)) as ServiceMCPToolAccess;
                if (next !== access) onAccess(next);
              }}
              className="w-40"
            >
              {ACCESS_OPTIONS.map((o) => (
                <SelectItem key={o.id || UPSTREAM_KEY} id={o.id || UPSTREAM_KEY}>
                  {o.label}
                </SelectItem>
              ))}
            </Select>
          )}
          {hasSchema && (
            <Button
              size="small"
              variant="ghost"
              onClick={() => setOpen((o) => !o)}
              aria-expanded={open}
              trailingIcon={RiArrowDownSLine}
              className={cx("shrink-0", open && "[&_svg]:rotate-180")}
            >
              Schema
            </Button>
          )}
        </div>
      </div>
      {open && hasSchema && (
        <div className="fade-up mt-3 flex flex-col gap-3 border-t border-separator-border pt-3">
          {tool.inputSchema != null && <SchemaBlock label="Input schema" schema={tool.inputSchema} />}
          {tool.outputSchema != null && <SchemaBlock label="Output schema" schema={tool.outputSchema} />}
        </div>
      )}
    </li>
  );
}

export function ServiceMcpPanel({
  service,
  canManage,
  onCount,
}: {
  service: Service;
  canManage: boolean;
  onCount?: (n: number) => void;
}) {
  const now = useNow(30000);
  const [view, setView] = useState<ServiceMCPView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [query, setQuery] = useState("");
  const [showInstructions, setShowInstructions] = useState(false);

  const load = useCallback(async () => {
    try {
      const v = await api.getServiceMcp(service.id);
      setView(v);
      setError(null);
      onCount?.(v.tool_count);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [service.id, onCount]);

  useEffect(() => {
    setView(null);
    setError(null);
    void load();
  }, [load, service.mcp_url]);

  const sync = async () => {
    setSyncing(true);
    try {
      const v = await api.syncServiceMcp(service.id);
      setView(v);
      setError(null);
      onCount?.(v.tool_count);
      toast.success(`Synced ${plural(v.tool_count, "MCP tool")} from ${service.name}`);
    } catch (e) {
      toast.error(`MCP sync failed: ${errorMessage(e)}`);
      // Refresh stored view so last_error from the failed sync shows up immediately.
      await load();
    } finally {
      setSyncing(false);
    }
  };

  const filtered = useMemo(() => {
    const tools = view?.tools ?? [];
    const q = query.trim().toLowerCase();
    if (!q) return tools;
    return tools.filter(
      (t) =>
        t.name.toLowerCase().includes(q) ||
        t.public_name.toLowerCase().includes(q) ||
        (t.title ?? "").toLowerCase().includes(q) ||
        (t.description ?? "").toLowerCase().includes(q),
    );
  }, [view?.tools, query]);

  const readOnlyCount = useMemo(() => (view?.tools ?? []).filter((t) => t.read_only).length, [view?.tools]);

  const [savingTool, setSavingTool] = useState<string | null>(null);
  const setAccess = async (tool: ServiceMCPTool, access: ServiceMCPToolAccess) => {
    setSavingTool(tool.name);
    try {
      const v = await api.setServiceMcpToolAccess(service.id, tool.name, access);
      setView(v);
      const shown = tool.public_name || tool.name;
      toast.success(
        access === ""
          ? `${shown} follows the server again`
          : `${shown} is now ${access === "read" ? "read-only" : "a write tool"}`,
      );
    } catch (e) {
      toast.error(`Could not change ${tool.public_name || tool.name}: ${errorMessage(e)}`);
    } finally {
      setSavingTool(null);
    }
  };

  if (!view && !error) {
    return (
      <div className="flex flex-col gap-4" role="status" aria-label="Loading upstream MCP">
        <Skeleton className="h-32 rounded-3xl" />
        <Skeleton className="h-56 rounded-3xl" />
      </div>
    );
  }

  if (error && !view) {
    return (
      <Callout
        tone="error"
        action={
          <Button size="small" variant="secondary" leadingIcon={RiRefreshLine} onClick={() => void load()}>
            Try again
          </Button>
        }
      >
        Could not load upstream MCP details: {error}
      </Callout>
    );
  }

  if (!view) return null;

  return (
    <div className="flex flex-col gap-5">
      <Section
        title="Upstream MCP server"
        description="Acnologia connects to this Streamable HTTP MCP server using your pooled keys, syncs its tools, and exposes them to apps through /mcp."
        actions={
          canManage && view.configured ? (
            <Button
              variant="primary"
              size="small"
              leadingIcon={syncing ? ButtonSpinner : RiRefreshLine}
              disabled={syncing}
              onClick={() => void sync()}
            >
              {syncing ? "Syncing" : "Sync tools"}
            </Button>
          ) : undefined
        }
      >
        <dl className="-mt-2">
          <div className="flex flex-col gap-0.5 border-b border-separator-border py-2.5 sm:flex-row sm:gap-4">
            <dt className="font-mono text-caption-2-semibold uppercase text-text-tertiary sm:w-36 sm:shrink-0 sm:pt-0.5">Endpoint</dt>
            <dd className="flex min-w-0 flex-wrap items-center gap-2 wrap-anywhere font-mono text-body-2-regular text-text-primary" dir="ltr">
              <span>{view.endpoint || "-"}</span>
              {view.from_provider && (
                <Chip variant="caption" color="neutral" className="font-mono">
                  from provider
                </Chip>
              )}
            </dd>
          </div>
          <div className="flex flex-col gap-0.5 border-b border-separator-border py-2.5 sm:flex-row sm:gap-4">
            <dt className="font-mono text-caption-2-semibold uppercase text-text-tertiary sm:w-36 sm:shrink-0 sm:pt-0.5">Transport</dt>
            <dd className="min-w-0 text-body-2-regular text-text-primary">
              {view.transport === "streamable_http" ? "Streamable HTTP" : view.transport || "Streamable HTTP"}
              {view.protocol_version ? ` · MCP ${view.protocol_version}` : ""}
            </dd>
          </div>
          {(view.server_name || view.server_version) && (
            <div className="flex flex-col gap-0.5 border-b border-separator-border py-2.5 sm:flex-row sm:gap-4">
              <dt className="font-mono text-caption-2-semibold uppercase text-text-tertiary sm:w-36 sm:shrink-0 sm:pt-0.5">Server</dt>
              <dd className="min-w-0 font-mono text-body-2-regular text-text-primary">
                {view.server_name}
                {view.server_version ? ` v${view.server_version}` : ""}
              </dd>
            </div>
          )}
          <div className="flex flex-col gap-0.5 border-b border-separator-border py-2.5 last:border-b-0 sm:flex-row sm:gap-4">
            <dt className="font-mono text-caption-2-semibold uppercase text-text-tertiary sm:w-36 sm:shrink-0 sm:pt-0.5">Last synced</dt>
            <dd className="min-w-0 text-body-2-regular text-text-primary" title={view.synced_at ? dateTime(view.synced_at) : undefined}>
              {view.synced_at ? relTime(view.synced_at, now) : "Never"}
            </dd>
          </div>
        </dl>

        {view.last_error && (
          <Callout tone="error" title="Last sync error" className="mt-4">
            {view.last_error}
          </Callout>
        )}

        {view.instructions && (
          <div className="mt-4 rounded-2xl border border-separator-border bg-background-secondary-default px-4 py-3">
            <div className="flex items-center justify-between gap-2">
              <span className="font-mono text-caption-2-semibold uppercase text-text-tertiary">Server instructions</span>
              <Button size="small" variant="ghost" onClick={() => setShowInstructions((v) => !v)}>
                {showInstructions ? "Hide" : "Show"}
              </Button>
            </div>
            {showInstructions && (
              <p className="mt-2 whitespace-pre-wrap text-caption-1-regular text-text-secondary">{view.instructions}</p>
            )}
          </div>
        )}
      </Section>

      <Section
        title="Synced tools"
        description={
          view.tool_count > 0
            ? `${plural(view.tool_count, "tool")} synced (${readOnlyCount} read-only). Apps with access to ${service.slug} see these tools automatically when Service tools is enabled.`
            : "Sync tools after adding at least one active token to this service."
        }
      >
        {view.tools.length === 0 ? (
          <EmptyState
            compact
            icon={RiPlugLine}
            title="No MCP tools synced yet"
            body={
              canManage
                ? "Add a token on the Keys tab if you haven't already, then click Sync tools to discover what this MCP server offers."
                : "Ask an admin to sync tools from this upstream MCP server."
            }
            action={
              canManage && view.configured ? (
                <Button variant="primary" leadingIcon={syncing ? ButtonSpinner : RiRefreshLine} disabled={syncing} onClick={() => void sync()}>
                  {syncing ? "Syncing" : "Sync tools"}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <div className="flex flex-col gap-3">
            {view.tools.length > 5 && (
              <SearchField
                value={query}
                onChange={setQuery}
                placeholder={`Filter ${view.tools.length} tools`}
                aria-label="Filter synced MCP tools"
              />
            )}
            {filtered.length === 0 ? (
              <p className="py-4 text-center text-body-2-regular text-text-secondary">No tools match "{query}".</p>
            ) : (
              <ul className="divide-y divide-separator-border rounded-2xl border border-border-button-default bg-background-primary-default">
                {filtered.map((t) => (
                  <ToolRow
                    key={t.name}
                    tool={t}
                    canManage={canManage}
                    saving={savingTool === t.name}
                    onAccess={(a) => void setAccess(t, a)}
                  />
                ))}
              </ul>
            )}
          </div>
        )}
      </Section>
    </div>
  );
}
