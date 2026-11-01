import { useEffect, useId, useMemo, useState, type ReactNode } from "react";
import { RiRefreshLine } from "@remixicon/react";
import { Modal } from "../components/Modal";
import { CodeBlock } from "../components/CodeBlock";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { plural } from "../lib/format";
import type { App, MCPSettingsInput, MCPTool, MCPToolSet, MCPView } from "../lib/types";
import { enabledCount, inputOf, toolOn, useMcp } from "../stores/mcp";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { Textarea } from "@/components/base/textarea/textarea";
import { Switch } from "@/components/base/switch/switch";
import { Chip } from "@/components/base/badges/chip";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { Tab as TabItem, TabList, TabPanel, Tabs } from "@/components/base/tabs/tabs";
import { cx } from "@/utils/cx";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout } from "@/components/acn";

const MAX_INSTRUCTIONS = 8000;
const MAX_KB = 2048;

const SETS: Array<{ id: MCPToolSet; label: string; body: string }> = [
  {
    id: "core",
    label: "Core",
    body: "Lease a key, renew the lease, report how the key did, and call any endpoint of a service through Acnologia. Most agents need this.",
  },
  {
    id: "service_tools",
    label: "Service tools",
    body: "One ready-made tool for each example call in your services. Acnologia builds them live, so new services show up on their own.",
  },
  {
    id: "insights",
    label: "Key insights",
    body: "Read-only view of each key's health, remaining quota and last use. Secrets are never included.",
  },
  {
    id: "manage",
    label: "Key management",
    body: "Run health checks, enable or disable keys, and add new keys to a service.",
  },
];

type Tab = "settings" | "tools" | "connect";
type Client = "claude-code" | "claude-desktop" | "cursor" | "vscode" | "other";

const CLIENTS: Array<{ id: Client; label: string }> = [
  { id: "claude-code", label: "Claude Code" },
  { id: "claude-desktop", label: "Claude Desktop" },
  { id: "cursor", label: "Cursor" },
  { id: "vscode", label: "VS Code" },
  { id: "other", label: "Other clients" },
];

const TOKEN = "cog_...";

/** A config-safe server name, such as cogito-marketing-site. */
export function serverName(app: App): string {
  const slug = app.name
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40)
    .replace(/-+$/, "");
  return `cogito-${slug || app.id}`;
}

function snippets(name: string, endpoint: string) {
  const base = endpoint.replace(/\/mcp\/?$/, "");
  const auth = `Bearer ${TOKEN}`;
  const json = (v: unknown) => JSON.stringify(v, null, 2);
  return {
    "claude-code": [
      {
        label: "terminal",
        code: `claude mcp add --transport http ${name} ${endpoint} --header "Authorization: ${auth}"`,
      },
    ],
    "claude-desktop": [
      {
        label: "claude_desktop_config.json",
        code: json({ mcpServers: { [name]: { command: "/path/to/cogito", args: ["mcp", "--url", base], env: { COGITO_TOKEN: TOKEN } } } }),
      },
    ],
    cursor: [{ label: ".cursor/mcp.json", code: json({ mcpServers: { [name]: { url: endpoint, headers: { Authorization: auth } } } }) }],
    vscode: [{ label: ".vscode/mcp.json", code: json({ servers: { [name]: { type: "http", url: endpoint, headers: { Authorization: auth } } } }) }],
    other: [
      { label: "streamable http", code: `URL     ${endpoint}\nHeader  Authorization: ${auth}` },
      { label: "stdio bridge", code: `COGITO_URL=${base} COGITO_TOKEN=${TOKEN} cogito mcp` },
    ],
  } satisfies Record<Client, Array<{ label: string; code: string }>>;
}

const CLIENT_NOTE: Record<Client, string> = {
  "claude-code": "Run this in your project folder. Add --scope user to use it in every project.",
  "claude-desktop":
    "Claude Desktop starts servers as local programs, so it talks to Acnologia through the cogito binary. Set command to where that binary lives, then restart the app.",
  cursor: "Put this in .cursor/mcp.json in your project, or in ~/.cursor/mcp.json for every project.",
  vscode: "Put this in .vscode/mcp.json in your workspace. VS Code shows a Start button above the server name.",
  other: "Any client that speaks streamable HTTP can connect with the URL and header. Clients that only start local programs can use the stdio bridge.",
};

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-caption-2-semibold uppercase text-text-tertiary">{title}</h3>
      <div className="divide-y divide-separator-border rounded-2xl border border-border-button-default">{children}</div>
    </section>
  );
}

function Row({ children }: { children: ReactNode }) {
  return <div className="px-4 py-3.5">{children}</div>;
}

/** A labelled switch row: title and description on the start side, the switch on the end. */
function SwitchRow({
  checked,
  onChange,
  label,
  description,
  disabled,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: ReactNode;
  description?: ReactNode;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="min-w-0">
        <div id={`${id}-l`} className="text-body-medium text-text-primary">
          {label}
        </div>
        {description && (
          <p id={`${id}-d`} className="mt-0.5 text-caption-1-regular text-text-secondary">
            {description}
          </p>
        )}
      </div>
      <Switch
        isSelected={checked}
        onChange={onChange}
        isDisabled={disabled}
        aria-labelledby={`${id}-l`}
        aria-describedby={description ? `${id}-d` : undefined}
        className="mt-0.5 shrink-0"
      />
    </div>
  );
}

function SettingsTab({ draft, edit, readOnly }: { draft: MCPSettingsInput; edit: (p: Partial<MCPSettingsInput>) => void; readOnly: boolean }) {
  const [kb, setKb] = useState(String(draft.max_result_kb));
  // Follow outside changes, such as Discard or a save. NaN marks the field's own invalid text.
  useEffect(() => {
    if (Number.isFinite(draft.max_result_kb)) setKb((k) => (Number(k) === draft.max_result_kb ? k : String(draft.max_result_kb)));
  }, [draft.max_result_kb]);
  const kbNum = Number(kb);
  const kbErr = kb.trim() === "" || !Number.isInteger(kbNum) || kbNum < 1 || kbNum > MAX_KB ? `Enter a whole number from 1 to ${MAX_KB}.` : null;
  const len = draft.instructions.length;

  return (
    <div className="flex flex-col gap-6">
      <Group title="Tool sets">
        {SETS.map((s) => (
          <Row key={s.id}>
            <SwitchRow checked={draft[s.id]} onChange={(v) => edit({ [s.id]: v })} label={s.label} description={s.body} disabled={readOnly} />
            {s.id === "manage" && (
              <Callout tone="warning" className="mt-3">
                The agent can change your pool. A key it disables stops serving every app until someone turns it back on.
              </Callout>
            )}
          </Row>
        ))}
      </Group>

      <Group title="Context">
        <Row>
          <SwitchRow
            checked={draft.resources}
            onChange={(v) => edit({ resources: v })}
            label="Resources"
            description="Lets the agent read the list of services, how to call each one, and a short guide to Acnologia."
            disabled={readOnly}
          />
        </Row>
        <Row>
          <SwitchRow
            checked={draft.prompts}
            onChange={(v) => edit({ prompts: v })}
            label="Prompts"
            description="Adds ready-made prompts to the client's menu: use a service, and a key health report."
            disabled={readOnly}
          />
        </Row>
      </Group>

      <Group title="Leases">
        <Row>
          <SwitchRow
            checked={draft.exclusive_default}
            onChange={(v) => edit({ exclusive_default: v })}
            label="Reserve keys exclusively by default"
            description="An exclusive lease keeps the key for this agent alone. No one else gets that key until the agent reports back or the lease ends. Use it for long jobs that should not share a key's rate limit. The agent can still ask for a shared key on any call."
            disabled={readOnly}
          />
        </Row>
      </Group>

      <section className="flex flex-col gap-4">
        <h3 className="text-caption-2-semibold uppercase text-text-tertiary">Output</h3>
        <Input
          label="Max result size (KB)"
          inputMode="numeric"
          value={kb}
          isReadOnly={readOnly}
          isInvalid={!readOnly && !!kbErr}
          errorMessage={readOnly ? null : kbErr}
          hint={`Tool results larger than this are cut before they reach the model. 1–${MAX_KB} KB, default 200.`}
          onChange={(v) => {
            setKb(v);
            const n = Number(v);
            const ok = v.trim() !== "" && Number.isInteger(n) && n >= 1 && n <= MAX_KB;
            edit({ max_result_kb: ok ? n : NaN });
          }}
          className="max-w-[240px]"
          inputClassName="font-mono tabular-nums"
        />
        <div className="flex flex-col gap-1">
          <Textarea
            label="Instructions for the agent"
            rows={5}
            maxLength={MAX_INSTRUCTIONS}
            value={draft.instructions}
            isReadOnly={readOnly}
            onChange={(v) => edit({ instructions: v })}
            placeholder={readOnly ? "No extra instructions." : "Prefer the search service. Report every lease, even on success."}
            resize="vertical"
            hint="Sent when the agent connects, after Acnologia's own instructions. Good for house rules, such as which service to try first."
          />
          <span
            className={cx(
              "self-end font-mono text-caption-1-regular tabular-nums",
              len > MAX_INSTRUCTIONS * 0.9 ? "text-status-yellow-text" : "text-text-tertiary",
            )}
          >
            {len.toLocaleString()} / {MAX_INSTRUCTIONS.toLocaleString()}
          </span>
        </div>
      </section>
    </div>
  );
}

function ToolsTab({
  view,
  draft,
  edit,
  readOnly,
  goSettings,
}: {
  view: MCPView;
  draft: MCPSettingsInput;
  edit: (p: Partial<MCPSettingsInput>) => void;
  readOnly: boolean;
  goSettings: () => void;
}) {
  const bySet = useMemo(() => {
    const m: Record<MCPToolSet, MCPTool[]> = { core: [], service_tools: [], insights: [], manage: [] };
    for (const t of view.tools) (m[t.set] ?? (m[t.set] = [])).push(t);
    return m;
  }, [view.tools]);

  const setTool = (name: string, on: boolean) => {
    const rest = draft.disabled_tools.filter((n) => n !== name);
    edit({ disabled_tools: on ? rest : [...rest, name] });
  };

  return (
    <div className="flex flex-col gap-6">
      {SETS.map(({ id, label }) => {
        const tools = bySet[id];
        const setOn = draft[id];
        const savedOn = view.settings[id];
        const on = tools.filter((t) => toolOn(draft, t)).length;
        return (
          <section key={id} className="flex flex-col gap-2">
            <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
              <h3 className="text-body-medium text-text-primary">{label}</h3>
              <span className="font-mono text-caption-1-regular text-text-tertiary">{setOn ? (tools.length ? `${on} of ${tools.length} on` : "") : "set is off"}</span>
            </div>
            {!setOn ? (
              <div className="flex flex-wrap items-center justify-between gap-2 rounded-2xl border border-dashed border-border-button-hover px-4 py-3 text-body-2-regular text-text-secondary">
                <span>Turn on {label} in Settings to give the agent these tools.</span>
                <Button size="small" variant="ghost" onClick={goSettings}>
                  Open Settings
                </Button>
              </div>
            ) : tools.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-border-button-hover px-4 py-3 text-body-2-regular text-text-secondary">
                {!savedOn
                  ? "Save to see the tools in this set."
                  : id === "service_tools"
                    ? "No ready-made tools yet. They appear when a service has example calls."
                    : "No tools in this set."}
              </div>
            ) : (
              <ul className="divide-y divide-separator-border rounded-2xl border border-border-button-default">
                {tools.map((t) => {
                  const checked = toolOn(draft, t);
                  return (
                    <li key={t.name} className="flex items-start gap-3 px-4 py-3">
                      <div className="min-w-0 flex-1">
                        <div className="break-all font-mono text-body-2-regular text-text-primary">{t.name}</div>
                        {t.description && <p className="mt-0.5 text-caption-1-regular text-text-secondary">{t.description}</p>}
                      </div>
                      <Switch
                        size="sm"
                        isSelected={checked}
                        onChange={(v) => setTool(t.name, v)}
                        isDisabled={readOnly}
                        aria-label={`${t.name} ${checked ? "on" : "off"}`}
                        className="mt-0.5 shrink-0"
                      />
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
        );
      })}
    </div>
  );
}

/** Tab strip that scrolls sideways on narrow screens instead of wrapping. */
function ScrollTabs({ children }: { children: ReactNode }) {
  return <div className="-mx-1 overflow-x-auto px-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">{children}</div>;
}

function ConnectTab({ app, endpoint }: { app: App; endpoint: string }) {
  const [client, setClient] = useState<Client>("claude-code");
  const name = serverName(app);
  const all = useMemo(() => snippets(name, endpoint), [name, endpoint]);
  return (
    <div className="flex flex-col gap-4">
      <Callout tone="info">
        Replace <code className="font-mono">{TOKEN}</code> with this app's token, which starts with{" "}
        <code className="break-all font-mono">{app.token_prefix}</code>. Acnologia showed the full token once, when the app was created. Lost it? An admin can
        rotate it from the app's actions to get a new one. The old token stops working right away.
      </Callout>
      <Tabs selectedKey={client} onSelectionChange={(k) => setClient(k as Client)} className="gap-3">
        <ScrollTabs>
          <TabList aria-label="MCP client" className="w-max min-w-full">
            {CLIENTS.map((c) => (
              <TabItem key={c.id} id={c.id}>
                {c.label}
              </TabItem>
            ))}
          </TabList>
        </ScrollTabs>
        {CLIENTS.map((c) => (
          <TabPanel key={c.id} id={c.id} className="flex flex-col gap-3">
            <p className="text-body-2-regular text-text-secondary">{CLIENT_NOTE[c.id]}</p>
            {all[c.id].map((s) => (
              <CodeBlock key={s.label} label={s.label} code={s.code} />
            ))}
          </TabPanel>
        ))}
      </Tabs>
    </div>
  );
}

function LoadingBody() {
  return (
    <div className="flex flex-col gap-3" role="status" aria-live="polite" aria-busy="true">
      <span className="sr-only">Loading MCP settings</span>
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i} className="flex flex-col gap-2 rounded-2xl border border-border-button-default px-4 py-3.5">
          <Skeleton className="h-4 w-32 rounded-md" />
          <Skeleton className="h-3 w-4/5 rounded-md" />
        </div>
      ))}
    </div>
  );
}

export function AppMcpPanel({ app, onClose, canEdit }: { app: App | null; onClose: () => void; canEdit: boolean }) {
  const id = app?.id ?? -1;
  const view = useMcp((s) => s.views[id]);
  const loadErr = useMcp((s) => s.errors[id]);
  const stored = useMcp((s) => s.drafts[id]);
  const { load, setDraft, discard, save } = useMcp();
  const [tab, setTab] = useState<Tab>("settings");
  const [busy, setBusy] = useState(false);
  const [retrying, setRetrying] = useState(false);

  useEffect(() => {
    if (!app) return;
    setTab("settings");
    // Fresh copy on open; the cached view shows meanwhile.
    void load(app.id, true);
  }, [app, load]);

  const readOnly = !canEdit;
  const draft = useMemo(() => stored ?? (view ? inputOf(view.settings) : null), [stored, view]);
  const dirty = !!stored && canEdit;
  const invalid = !!draft && (!Number.isInteger(draft.max_result_kb) || draft.max_result_kb < 1 || draft.max_result_kb > MAX_KB);

  const edit = (p: Partial<MCPSettingsInput>) => {
    if (!draft || readOnly) return;
    setDraft(id, { ...draft, ...p });
  };

  const onSave = async () => {
    if (!app || !dirty || invalid) return;
    setBusy(true);
    try {
      await save(app.id);
      toast.success(`Saved MCP settings for ${app.name}`);
    } catch (e) {
      toast.error(`MCP settings not saved. ${errorMessage(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const retry = async () => {
    if (!app) return;
    setRetrying(true);
    await load(app.id, true);
    setRetrying(false);
  };

  const onCount = view ? enabledCount(view) : null;
  const tabs: Array<{ id: Tab; label: string; count?: number }> = [
    { id: "settings", label: "Settings" },
    { id: "tools", label: "Tools", count: view ? view.tools.length : undefined },
    { id: "connect", label: "Connect a client" },
  ];

  const body = (which: Tab) =>
    !view || !draft ? (
      loadErr ? (
        <Callout
          tone="error"
          action={
            <Button
              size="small"
              variant="secondary"
              leadingIcon={retrying ? ButtonSpinner : RiRefreshLine}
              disabled={retrying}
              onClick={() => void retry()}
            >
              Try again
            </Button>
          }
        >
          Could not load MCP settings. {loadErr}
        </Callout>
      ) : (
        <LoadingBody />
      )
    ) : which === "settings" ? (
      <SettingsTab draft={draft} edit={edit} readOnly={readOnly} />
    ) : which === "tools" ? (
      <ToolsTab view={view} draft={draft} edit={edit} readOnly={readOnly} goSettings={() => setTab("settings")} />
    ) : (
      app && <ConnectTab app={app} endpoint={view.endpoint} />
    );

  return (
    <Modal
      open={!!app}
      onClose={onClose}
      wide
      title={app ? `MCP for ${app.name}` : "MCP"}
      description="Choose what an AI agent using this app's token can see and do."
      headerAction={
        onCount !== null ? (
          <Chip variant="caption" color="purple" className="font-mono">
            {plural(onCount, "tool")} on
          </Chip>
        ) : undefined
      }
      footer={
        canEdit ? (
          <div className="flex w-full flex-wrap items-center justify-end gap-2">
            {dirty && <span className="me-auto text-caption-1-regular text-text-secondary">Unsaved changes</span>}
            {dirty ? (
              <Button variant="ghost" onClick={() => discard(id)} disabled={busy}>
                Discard
              </Button>
            ) : (
              <Button variant="ghost" onClick={onClose}>
                Close
              </Button>
            )}
            <Button variant="primary" onClick={() => void onSave()} leadingIcon={busy ? ButtonSpinner : undefined} disabled={busy || !dirty || invalid}>
              Save changes
            </Button>
          </div>
        ) : (
          <Button variant="secondary" onClick={onClose}>
            Close
          </Button>
        )
      }
    >
      <div data-autofocus tabIndex={-1} className="flex flex-col gap-5 outline-none">
        {readOnly && <Callout tone="info">You can view these settings. Ask an admin to change them.</Callout>}
        <Tabs selectedKey={tab} onSelectionChange={(k) => setTab(k as Tab)} className="gap-5">
          <ScrollTabs>
            <TabList aria-label="MCP sections" className="w-max min-w-full">
              {tabs.map((t) => (
                <TabItem key={t.id} id={t.id} count={t.count}>
                  {t.label}
                </TabItem>
              ))}
            </TabList>
          </ScrollTabs>
          {tabs.map((t) => (
            <TabPanel key={t.id} id={t.id}>
              {body(t.id)}
            </TabPanel>
          ))}
        </Tabs>
        {view && tab !== "connect" && dirty && (
          <p className="text-caption-1-regular text-text-secondary">Some clients keep the old tool list until you reconnect them after saving.</p>
        )}
      </div>
    </Modal>
  );
}
