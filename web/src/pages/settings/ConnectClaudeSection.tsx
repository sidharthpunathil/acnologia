import { useMemo, useState, type ReactNode } from "react";
import type { Selection } from "react-aria-components";
import { RiAddLine, RiArrowGoBackLine } from "@remixicon/react";
import { ApiError, errorMessage, mcpApi } from "@/lib/api";
import { plural } from "@/lib/format";
import { ops, type TokenScope } from "@/lib/ops";
import type { ControlMcpView, ControlTool, ControlToolKind, Role } from "@/lib/types";
import { useAuth } from "@/stores/auth";
import { toast } from "@/stores/toast";
import { EXPIRY, SCOPE_HELP, SCOPES_FOR } from "@/components/ApiTokens";
import { Accordion, AccordionItem } from "@/components/base/accordion/accordion";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { Select, SelectItem } from "@/components/base/select/select";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Switch } from "@/components/base/switch/switch";
import { Tab, TabList, TabPanel, Tabs } from "@/components/base/tabs/tabs";
import { Tag, TagGroup } from "@/components/base/tag-group/tag-group";
import { Callout } from "@/components/acn/callout";
import { CodeBlock } from "@/components/acn/code-block";
import { CopyButton, SecretBox } from "@/components/acn/copy-button";
import { EmptyState } from "@/components/acn/empty-state";
import { cx } from "@/utils/cx";
import { useLoad } from "./hooks";
import { ReadOnlyNote, RowsSkeleton, SectionError, SettingRow, SettingsCard, SettingsSection } from "./parts";

/** Stands in for the token in the snippets until one is created here. */
const PLACEHOLDER = "<your acnadm_ token>";
/** The server name clients list it under. */
const SERVER = "acnologia";
const TOKEN_NAME = "Claude MCP";

type Client = "claude-code" | "claude-desktop" | "cursor" | "raw";

const CLIENTS: Array<{ id: Client; label: string; note: ReactNode }> = [
  { id: "claude-code", label: "Claude Code", note: "Run this in a terminal. Add --scope user to use it in every project." },
  {
    id: "claude-desktop",
    label: "Claude Desktop",
    note: (
      <>
        Run <code className="font-mono">acn login</code> first: <code className="font-mono">acn mcp</code> uses the server and token you signed in with.
        Then restart Claude Desktop.
      </>
    ),
  },
  { id: "cursor", label: "Cursor", note: "Put this in .cursor/mcp.json in your project, or in ~/.cursor/mcp.json for every project." },
  { id: "raw", label: "Raw", note: "Any client that speaks streamable HTTP can connect with the URL and header." },
];

const KIND_COLOR: Record<ControlToolKind, ChipColor> = { read: "blue", write: "yellow", destructive: "rose" };

/** Who can use a tool the signed in role cannot. */
const NEEDS: Record<Role, string> = {
  member: "Needs admin or above",
  admin: "Needs superadmin",
  superadmin: "Not available to you",
};

export interface ControlOptions {
  groups: Set<string>;
  readOnly: boolean;
  confirm: boolean;
}

/**
 * The ?groups= value, or null while the pick matches the server's defaults.
 * Every group reads `all`; otherwise the ids go in the server's order.
 */
export function groupsParam(view: ControlMcpView, picked: Set<string>): string | null {
  const ids = view.groups.map((g) => g.id);
  const on = ids.filter((id) => picked.has(id));
  const defaults = view.groups.filter((g) => g.default).map((g) => g.id);
  if (on.length === defaults.length && on.every((id) => defaults.includes(id))) return null;
  if (on.length === ids.length) return "all";
  return on.map(encodeURIComponent).join(",");
}

/** The endpoint URL with the options as query parameters. */
export function controlUrl(view: ControlMcpView, opts: ControlOptions): string {
  const q: string[] = [];
  const groups = groupsParam(view, opts.groups);
  if (groups) q.push(`groups=${groups}`);
  if (opts.readOnly) q.push("read_only=1");
  if (!opts.confirm) q.push("confirm=off");
  if (!q.length) return view.url;
  return `${view.url}${view.url.includes("?") ? "&" : "?"}${q.join("&")}`;
}

/** Ready to paste configs for each client. */
export function controlSnippets(view: ControlMcpView, opts: ControlOptions, token: string): Record<Client, Array<{ label: string; code: string }>> {
  const url = controlUrl(view, opts);
  const auth = `Bearer ${token}`;
  const json = (v: unknown) => JSON.stringify(v, null, 2);
  const args = ["mcp"];
  const groups = groupsParam(view, opts.groups);
  if (groups) args.push("--groups", groups);
  if (opts.readOnly) args.push("--read-only");
  if (!opts.confirm) args.push("--confirm", "off");
  return {
    "claude-code": [{ label: "terminal", code: `claude mcp add --transport http ${SERVER} "${url}" --header "Authorization: ${auth}"` }],
    "claude-desktop": [{ label: "claude_desktop_config.json", code: json({ mcpServers: { [SERVER]: { command: "acn", args } } }) }],
    cursor: [{ label: ".cursor/mcp.json", code: json({ mcpServers: { [SERVER]: { url, headers: { Authorization: auth } } } }) }],
    raw: [{ label: "streamable http", code: `URL     ${url}\nHeader  Authorization: ${auth}` }],
  };
}

/** Whether a client with these options gets the tool. */
function toolOn(t: ControlTool, opts: ControlOptions) {
  return t.available && opts.groups.has(t.group) && (!opts.readOnly || t.kind === "read");
}

/** Why a tool is not in the set, or null when it is. */
function offReason(t: ControlTool, opts: ControlOptions, role: Role): ReactNode {
  if (!t.available) return <ReadOnlyNote className="mt-1">{NEEDS[role]}</ReadOnlyNote>;
  if (!opts.groups.has(t.group)) return "Group is off";
  if (opts.readOnly && t.kind !== "read") return "Off in read only";
  return null;
}

/**
 * The view, or null when this server is older than the control MCP (the
 * route answers 404) so the section can say so quietly.
 */
async function loadControl(): Promise<{ view: ControlMcpView | null }> {
  try {
    const v = await mcpApi.control();
    if (!v || typeof v.url !== "string" || !Array.isArray(v.tools)) return { view: null };
    return { view: { ...v, groups: Array.isArray(v.groups) ? v.groups : [] } };
  } catch (e) {
    if (e instanceof ApiError && (e.status === 404 || e.status === 405)) return { view: null };
    throw e;
  }
}

/** Create the token Claude signs in with. It is shown once, here, and fills the snippets. */
function TokenRow({ role, token, onToken }: { role: Role; token: string | null; onToken: (t: string | null) => void }) {
  const scopes = SCOPES_FOR[role] ?? ["read"];
  const [scope, setScope] = useState<TokenScope>(scopes[scopes.length - 1]);
  const [days, setDays] = useState(90);
  const [busy, setBusy] = useState(false);

  const create = async () => {
    setBusy(true);
    try {
      const res = await ops.createToken({ name: TOKEN_NAME, scope, expires_in_days: days });
      onToken(res.token);
    } catch (e) {
      toast.error(`Token not created. ${errorMessage(e)}`);
    } finally {
      setBusy(false);
    }
  };

  if (token) {
    return (
      <SettingRow label="Token for Claude" description={`Created as ${TOKEN_NAME}. Revoke it any time on your Account page.`} stacked>
        <div className="flex flex-col gap-3">
          <Callout tone="warning">Copy it now. Acnologia stores only a hash, so it cannot show this token again. The snippets below already include it.</Callout>
          <SecretBox value={token} />
          <div>
            <Button variant="secondary" size="small" onClick={() => onToken(null)}>
              I have saved it
            </Button>
          </div>
        </div>
      </SettingRow>
    );
  }

  return (
    <SettingRow
      label="Token for Claude"
      description={`Claude signs in with an admin API token named ${TOKEN_NAME}. It acts as you, limited to the scope you pick.`}
      stacked
    >
      <div className="flex flex-col gap-1.5">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[auto_auto_auto] sm:items-end sm:justify-start">
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
          <Select label="Expires" selectedKey={String(days)} onSelectionChange={(k) => k != null && setDays(Number(k))} triggerClassName="sm:min-w-28">
            {EXPIRY.map((x) => (
              <SelectItem key={x.days} id={String(x.days)}>
                {x.label}
              </SelectItem>
            ))}
          </Select>
          <Button
            variant="primary"
            onClick={() => void create()}
            disabled={busy}
            aria-busy={busy || undefined}
            leadingIcon={busy ? ButtonSpinner : RiAddLine}
            className="w-full sm:w-auto"
          >
            Create token
          </Button>
        </div>
        <p className="text-caption-1-medium text-text-secondary">{SCOPE_HELP[scope]}</p>
      </div>
    </SettingRow>
  );
}

function OptionsCard({
  view,
  opts,
  setOpts,
  isDefault,
  reset,
}: {
  view: ControlMcpView;
  opts: ControlOptions;
  setOpts: (o: ControlOptions) => void;
  isDefault: boolean;
  reset: () => void;
}) {
  const onGroups = (keys: Selection) => {
    if (keys === "all") setOpts({ ...opts, groups: new Set(view.groups.map((g) => g.id)) });
    else setOpts({ ...opts, groups: new Set([...keys].map(String)) });
  };
  return (
    <SettingsCard title="Options">
      {view.groups.length > 0 && (
        <SettingRow label="Tool groups" description="What Claude can reach. The defaults leave out the riskier groups." stacked>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <TagGroup aria-label="Tool groups" selectionMode="multiple" selectedKeys={opts.groups} onSelectionChange={onGroups} disallowEmptySelection>
              {view.groups.map((g) => (
                <Tag key={g.id} id={g.id}>
                  {g.label}
                </Tag>
              ))}
            </TagGroup>
          </div>
        </SettingRow>
      )}
      <SettingRow label="Read only" description="Claude can look but not change anything." labelId="cc-read-only">
        <Switch isSelected={opts.readOnly} onChange={(v) => setOpts({ ...opts, readOnly: v })} aria-labelledby="cc-read-only" />
      </SettingRow>
      <SettingRow
        label="Ask before deleting"
        description="Deletes and revokes do nothing until Claude calls them again to confirm. Turn off for clients that ask you before every tool call."
        labelId="cc-confirm"
      >
        <Switch isSelected={opts.confirm} onChange={(v) => setOpts({ ...opts, confirm: v })} aria-labelledby="cc-confirm" />
      </SettingRow>
      {!isDefault && (
        <div className="flex justify-end px-4 py-3 sm:px-5">
          <Button variant="ghost" size="small" leadingIcon={RiArrowGoBackLine} onClick={reset}>
            Back to defaults
          </Button>
        </div>
      )}
    </SettingsCard>
  );
}

function SnippetsCard({ view, opts, token }: { view: ControlMcpView; opts: ControlOptions; token: string | null }) {
  const [client, setClient] = useState<Client>("claude-code");
  const all = useMemo(() => controlSnippets(view, opts, token ?? PLACEHOLDER), [view, opts, token]);
  return (
    <SettingsCard title="Add to a client">
      <div className="flex flex-col gap-3 px-4 py-4 sm:px-5">
        <Tabs selectedKey={client} onSelectionChange={(k) => setClient(k as Client)} className="gap-3">
          <div className="-mx-1 overflow-x-auto px-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            <TabList aria-label="MCP client" className="w-max min-w-full">
              {CLIENTS.map((c) => (
                <Tab key={c.id} id={c.id}>
                  {c.label}
                </Tab>
              ))}
            </TabList>
          </div>
          {CLIENTS.map((c) => (
            <TabPanel key={c.id} id={c.id} className="flex flex-col gap-3">
              <p className="text-body-2-regular text-text-secondary">{c.note}</p>
              {all[c.id].map((s) => (
                <CodeBlock key={s.label} label={s.label} code={s.code} />
              ))}
            </TabPanel>
          ))}
        </Tabs>
        {!token && client !== "claude-desktop" && (
          <p className="text-caption-1-regular text-text-tertiary">
            Replace <code className="font-mono">{PLACEHOLDER}</code> with a token, or create one above to fill it in.
          </p>
        )}
      </div>
    </SettingsCard>
  );
}

function ToolRow({ tool, opts, role }: { tool: ControlTool; opts: ControlOptions; role: Role }) {
  const on = toolOn(tool, opts);
  const reason = on ? null : offReason(tool, opts, role);
  return (
    <li className="flex items-start gap-3 py-2.5">
      <div className={cx("min-w-0 flex-1", !on && "opacity-60")}>
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span className="text-body-2-medium text-text-primary">{tool.title || tool.name}</span>
          <code dir="ltr" className="font-mono text-caption-1-regular break-all text-text-tertiary">
            {tool.name}
          </code>
        </div>
        {tool.description && (
          <p className="mt-0.5 line-clamp-2 text-caption-1-regular text-text-secondary" title={tool.description}>
            {tool.description}
          </p>
        )}
        {typeof reason === "string" ? <p className="mt-1 text-caption-1-medium text-text-tertiary">{reason}</p> : reason}
      </div>
      <Chip variant="caption" color={on ? KIND_COLOR[tool.kind] : "soft"} className="mt-0.5 shrink-0 font-mono">
        {tool.kind}
      </Chip>
    </li>
  );
}

function ToolsCard({ view, opts, role }: { view: ControlMcpView; opts: ControlOptions; role: Role }) {
  // Groups in the server's order; tools of a group it did not list go last under their id.
  const sections = useMemo(() => {
    const known = new Map(view.groups.map((g) => [g.id, g.label]));
    const by = new Map<string, ControlTool[]>();
    for (const g of view.groups) by.set(g.id, []);
    for (const t of view.tools) {
      if (!by.has(t.group)) by.set(t.group, []);
      by.get(t.group)!.push(t);
    }
    return [...by.entries()].filter(([, tools]) => tools.length > 0).map(([id, tools]) => ({ id, label: known.get(id) ?? id, tools }));
  }, [view]);
  const total = view.tools.length;
  const onCount = view.tools.filter((t) => toolOn(t, opts)).length;

  return (
    <div className="overflow-hidden rounded-3xl border border-border-button-default bg-background-primary-default">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 border-b border-separator-border px-4 pt-4 pb-3 sm:px-5">
        <h3 className="text-caption-1-semibold text-text-tertiary">Tools</h3>
        {total > 0 && <span className="font-mono text-caption-1-regular text-text-tertiary tabular-nums">{onCount} of {total} on</span>}
      </div>
      {total === 0 ? (
        <div className="p-4 sm:p-5">
          <EmptyState compact title="No tools" body="This server lists no control tools for you." />
        </div>
      ) : (
        <Accordion allowsMultipleExpanded className="px-4 sm:px-5">
          {sections.map((s) => {
            const on = s.tools.filter((t) => toolOn(t, opts)).length;
            const groupOn = opts.groups.has(s.id);
            return (
              <AccordionItem
                key={s.id}
                id={s.id}
                title={s.label}
                description={<span className="font-mono text-caption-1-regular tabular-nums">{groupOn ? `${on} of ${plural(s.tools.length, "tool")} on` : `Off · ${plural(s.tools.length, "tool")}`}</span>}
              >
                <ul className="divide-y divide-separator-border">
                  {s.tools.map((t) => (
                    <ToolRow key={t.name} tool={t} opts={opts} role={role} />
                  ))}
                </ul>
              </AccordionItem>
            );
          })}
        </Accordion>
      )}
    </div>
  );
}

/**
 * Connect Claude: the control MCP, which lets Claude Code, Claude Desktop or
 * Cursor run this server as the signed in user. A token made here is shown
 * once and filled into the snippets; the options rewrite the URL live.
 */
export function ConnectClaudeSection() {
  const role: Role = useAuth((s) => s.user?.role) ?? "member";
  const load = useLoad(loadControl);
  const view = load.data?.view ?? null;
  const [picked, setPicked] = useState<ControlOptions | null>(null);
  const [token, setToken] = useState<string | null>(null);

  const defaults = useMemo<ControlOptions | null>(
    () => (view ? { groups: new Set(view.groups.filter((g) => g.default).map((g) => g.id)), readOnly: false, confirm: true } : null),
    [view],
  );
  const opts = picked ?? defaults;
  const isDefault = !!view && !!opts && opts.readOnly === false && opts.confirm && groupsParam(view, opts.groups) === null;

  let body: ReactNode;
  if (load.error) {
    body = <SectionError what="the Claude connection" message={load.error} onRetry={load.reload} />;
  } else if (!load.data) {
    body = (
      <div role="status" aria-busy="true" className="flex flex-col gap-4">
        <span className="sr-only">Loading</span>
        <SettingsCard>
          <RowsSkeleton rows={2} />
        </SettingsCard>
        <SettingsCard>
          <RowsSkeleton rows={3} />
        </SettingsCard>
      </div>
    );
  } else if (!view || !opts) {
    body = (
      <SettingsCard>
        <SettingRow label="Not on this server yet" description="This version of Acnologia has no control MCP. Update the server to connect Claude." />
      </SettingsCard>
    );
  } else {
    const url = controlUrl(view, opts);
    body = (
      <>
        <SettingsCard>
          <SettingRow label="Endpoint" description="Streamable HTTP. The options below add to the query." stacked>
            <div className="flex items-start gap-2 rounded-2lg bg-background-tertiary-default py-1.5 ps-3 pe-1.5">
              <code dir="ltr" className="min-w-0 flex-1 break-all pt-1.5 font-mono text-body-2-regular text-text-primary">
                {url}
              </code>
              <CopyButton text={url} />
            </div>
          </SettingRow>
          <TokenRow role={role} token={token} onToken={setToken} />
        </SettingsCard>
        <OptionsCard view={view} opts={opts} setOpts={setPicked} isDefault={isDefault} reset={() => setPicked(null)} />
        <SnippetsCard view={view} opts={opts} token={token} />
        <ToolsCard view={view} opts={opts} role={role} />
      </>
    );
  }

  return (
    <SettingsSection
      id="connect-claude"
      title="Connect Claude"
      description="Let Claude Code, Claude Desktop or Cursor run this server for you. Claude acts as you, with your role, and every change lands in the audit log."
    >
      {body}
    </SettingsSection>
  );
}
