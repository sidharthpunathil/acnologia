import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { RiArrowLeftLine, RiArrowRightSLine, RiEditLine, RiPlugLine, RiSparkling2Line } from "@remixicon/react";
import { useServices } from "../stores/services";
import { useProviders } from "../stores/providers";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { api, errorMessage } from "../lib/api";
import { plural, slugify } from "../lib/format";
import { parseMcpConnection } from "../lib/mcpParse";
import { STRATEGY_VALUES } from "../lib/providerSpec";
import type { AuthType, Provider, ServiceInput, Strategy } from "../lib/types";
import { ServiceForm, validateService, type ServiceErrors } from "../components/ServiceForm";
import { ProviderLogo } from "../components/EntityLogo";
import { useSheetLayout } from "../components/Modal";
import { AiFillButton, AiFillPanel, useAiFill } from "../components/AiFill";
import { Callout } from "@/components/acn";
import { Chip } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "@/components/base/modal/modal";
import { SearchField } from "@/components/base/search-field/search-field";
import { Select, SelectItem } from "@/components/base/select/select";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Textarea } from "@/components/base/textarea/textarea";
import { cx } from "@/utils/cx";

function uniqueSlug(base: string, taken: Set<string>): string {
  let slug = slugify(base);
  if (!slug) return "";
  if (taken.has(slug)) {
    let i = 2;
    while (taken.has(`${slug}-${i}`)) i++;
    slug = `${slug}-${i}`;
  }
  return slug;
}

function originOf(rawUrl: string): string {
  try {
    const u = new URL(rawUrl);
    return u.origin;
  } catch {
    return "";
  }
}

export function serviceFromProvider(p: Provider, taken: Set<string>): ServiceInput {
  const blank = p.slug === "generic" || p.slug === "mcp";
  const slug = blank ? "" : uniqueSlug(p.slug || p.name, taken);
  const d = p.spec.defaults;
  return {
    slug,
    name: blank ? "" : p.name,
    provider: p.slug,
    category: p.category,
    description: blank ? "" : p.description,
    base_url: p.spec.base_url || p.base_url,
    mcp_url: p.spec.mcp?.url ?? "",
    auth_type: p.spec.auth.type,
    auth_name: p.spec.auth.name,
    strategy: (STRATEGY_VALUES.includes(d.strategy) ? d.strategy : "round_robin") as Strategy,
    cooldown_sec: d.cooldown_sec || 60,
    max_attempts: d.max_attempts || 3,
    max_concurrency: d.max_concurrency ?? 0,
    queue_timeout_sec: d.queue_timeout_sec ?? 0,
    icon: "",
  };
}

const SERVICE_AI_FIELDS = [
  "slug",
  "name",
  "category",
  "description",
  "strategy",
  "cooldown_sec",
  "max_attempts",
  "max_concurrency",
  "queue_timeout_sec",
] as const;

/** Copies only the fields the assist endpoint may set for a service, coercing numbers. */
export function applyServiceFields(form: ServiceInput, fields: Record<string, unknown>): ServiceInput {
  const next = { ...form } as Record<string, unknown>;
  for (const k of SERVICE_AI_FIELDS) {
    if (!(k in fields)) continue;
    const v = fields[k];
    if (["cooldown_sec", "max_attempts", "max_concurrency", "queue_timeout_sec"].includes(k)) {
      const n = Number(v);
      if (Number.isFinite(n)) next[k] = n;
    } else if (k === "slug") next[k] = slugify(String(v ?? ""));
    else if (typeof v === "string") next[k] = v;
  }
  return next as unknown as ServiceInput;
}

interface McpDraft {
  snippet: string;
  parsedSource: string;
  name: string;
  slug: string;
  slugTouched: boolean;
  mcpUrl: string;
  authType: AuthType;
  authName: string;
  token: string;
  tokenLabel: string;
}

const EMPTY_MCP_DRAFT: McpDraft = {
  snippet: "",
  parsedSource: "",
  name: "",
  slug: "",
  slugTouched: false,
  mcpUrl: "",
  authType: "bearer",
  authName: "Authorization",
  token: "",
  tokenLabel: "",
};

const MCP_AUTH_TYPES: Array<{ id: AuthType; label: string }> = [
  { id: "bearer", label: "bearer token" },
  { id: "header", label: "header" },
  { id: "query", label: "query parameter" },
];

function ProviderPicker({
  onPick,
  onConnectMcp,
  onClose,
}: {
  onPick: (p: Provider) => void;
  onConnectMcp: () => void;
  onClose: () => void;
}) {
  const { providers, loaded, error, ensure } = useProviders();
  const role = useAuth((s) => s.user?.role);
  const [q, setQ] = useState("");
  // Wide screens start typing right away; phones keep the keyboard down until asked.
  const sheet = useSheetLayout();

  useEffect(() => {
    ensure().catch(() => undefined);
  }, [ensure]);

  const shown = useMemo(() => {
    const n = q.trim().toLowerCase();
    const list = providers.filter((p) => p.status !== "disabled");
    const hit = n ? list.filter((p) => [p.name, p.slug, p.category, p.description].some((f) => f?.toLowerCase().includes(n))) : list;
    // Generic last: it is the escape hatch, not a first choice.
    return [...hit].sort((a, b) => (a.slug === "generic" ? 1 : b.slug === "generic" ? -1 : a.name.localeCompare(b.name)));
  }, [providers, q]);

  const row =
    "group flex w-full cursor-pointer items-center gap-3 rounded-2xl border border-border-button-default bg-background-primary-default px-4 py-3 text-start outline-none transition-colors duration-150 ease hover:border-border-button-hover hover:bg-background-primary-hover active:bg-background-primary-active focus-visible:ring-2 focus-visible:ring-border-focus-ring";

  return (
    <div className="space-y-3 pb-1">
      <SearchField value={q} onChange={setQ} placeholder="Search providers" aria-label="Search providers" enterKeyHint="search" autoFocus={!sheet} />

      <button
        type="button"
        onClick={onConnectMcp}
        className={cx(row, "border-border-button-hover bg-background-secondary-default/60 hover:bg-background-primary-hover")}
      >
        <span className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-border-button-default bg-background-primary-default text-text-primary shadow-xs">
          <RiPlugLine className="size-5" aria-hidden />
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-2">
            <span className="block text-body-medium text-text-primary">Connect an MCP server</span>
            <Chip variant="caption" color="purple" className="rounded-full px-2 py-0 font-mono">
              MCP
            </Chip>
          </span>
          <span className="block text-caption-1-regular text-text-secondary">
            Paste a <code className="font-mono">claude mcp add</code> command, <code className="font-mono">mcp.json</code>, or Streamable HTTP URL. Pool tokens and expose its tools through Acnologia.
          </span>
        </span>
        <RiArrowRightSLine className="size-4 shrink-0 text-foreground-icon-tertiary transition-transform duration-150 ease-out group-hover:translate-x-0.5 rtl:rotate-180" aria-hidden />
      </button>

      {role !== "member" && (
        <Link to="/providers/ai" onClick={onClose} className={cx(row, "border-accent-line/30 bg-accent-subtle hover:border-accent-line/60 hover:bg-accent-subtle")}>
          <span className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-border-button-default bg-background-primary-default text-accent-fg shadow-xs">
            <RiSparkling2Line className="size-5" aria-hidden />
          </span>
          <span className="min-w-0 flex-1">
            <span className="block text-body-medium text-text-primary">Build a new provider with AI</span>
            <span className="block text-caption-1-regular text-text-secondary">
              Not listed? Give the AI a name or a docs URL. It researches, tests and publishes the provider.
            </span>
          </span>
          <RiArrowRightSLine className="size-4 shrink-0 text-foreground-icon-tertiary transition-transform duration-150 ease-out group-hover:translate-x-0.5 rtl:rotate-180" aria-hidden />
        </Link>
      )}

      {!loaded ? (
        <div className="space-y-2" role="status" aria-label="Loading providers">
          {[0, 1, 2].map((i) => (
            <div key={i} className="flex items-center gap-3 rounded-2xl border border-border-button-default px-4 py-3">
              <Skeleton className="size-10" />
              <div className="flex-1">
                <Skeleton shape="text" className="h-4 w-32" />
                <Skeleton shape="text" className="mt-2 h-3 w-48" />
              </div>
            </div>
          ))}
        </div>
      ) : error && !providers.length ? (
        <Callout tone="error">Could not load providers: {error}</Callout>
      ) : shown.length === 0 ? (
        <p className="py-6 text-center text-body-2-regular text-text-secondary">No provider matches "{q}". Build it with AI above, or pick Generic.</p>
      ) : (
        <ul className="space-y-2">
          {shown.map((p) => (
            <li key={p.slug}>
              <button type="button" onClick={() => (p.slug === "mcp" ? onConnectMcp() : onPick(p))} className={row}>
                <ProviderLogo provider={p} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2">
                    <span className="truncate text-body-medium text-text-primary">{p.name}</span>
                    {p.source === "ai" && (
                      <Chip variant="caption" color="purple" className="rounded-full px-2 py-0 font-mono">
                        ai
                      </Chip>
                    )}
                    {p.spec?.mcp?.url && (
                      <Chip variant="caption" color="purple" className="rounded-full px-2 py-0 font-mono">
                        MCP
                      </Chip>
                    )}
                  </span>
                  <span className="mt-0.5 block truncate font-mono text-caption-1-regular text-text-secondary">
                    {p.category || "other"} · {p.spec.auth.type}
                    {p.spec.base_url ? ` · ${p.spec.base_url.replace(/^https?:\/\//, "")}` : ""}
                  </span>
                </span>
                <RiArrowRightSLine className="size-4 shrink-0 text-foreground-icon-tertiary transition-transform duration-150 ease-out group-hover:translate-x-0.5 rtl:rotate-180" aria-hidden />
              </button>
            </li>
          ))}
        </ul>
      )}
      {role !== "member" && (
        <p className="pt-1 text-center text-caption-1-regular text-text-secondary">
          Prefer writing rules yourself?{" "}
          <Link
            to="/providers/new"
            onClick={onClose}
            className="inline-flex items-center gap-1 rounded-sm text-caption-1-medium text-text-primary underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          >
            <RiEditLine className="size-3" aria-hidden /> Create a provider manually
          </Link>
        </p>
      )}
    </div>
  );
}

export function AddServiceModal({ open, onClose, initialProvider }: { open: boolean; onClose: () => void; initialProvider?: string }) {
  const { create, services } = useServices();
  const providers = useProviders((s) => s.providers);
  const navigate = useNavigate();
  const [provider, setProvider] = useState<Provider | null>(null);
  const [form, setForm] = useState<ServiceInput | null>(null);
  const [mcpMode, setMcpMode] = useState(false);
  const [mcp, setMcp] = useState<McpDraft>(EMPTY_MCP_DRAFT);
  const [mcpErrors, setMcpErrors] = useState<Partial<Record<"name" | "slug" | "mcpUrl", string>>>({});
  const [errors, setErrors] = useState<ServiceErrors>({});
  const [busy, setBusy] = useState(false);

  const takenSlugs = useMemo(() => new Set(services.map((s) => s.slug)), [services]);

  const ai = useAiFill(
    "service",
    () => form ?? {},
    (fields) => setForm((f) => (f ? applyServiceFields(f, fields) : f)),
  );

  const pick = (p: Provider) => {
    setMcpMode(false);
    setProvider(p);
    setErrors({});
    setForm(serviceFromProvider(p, takenSlugs));
  };

  const startMcp = () => {
    setProvider(null);
    setForm(null);
    setErrors({});
    setMcp(EMPTY_MCP_DRAFT);
    setMcpErrors({});
    setMcpMode(true);
  };

  useEffect(() => {
    if (!open) return;
    setProvider(null);
    setForm(null);
    setMcpMode(false);
    setMcp(EMPTY_MCP_DRAFT);
    setMcpErrors({});
    setErrors({});
    ai.setOpen(false);
    if (initialProvider === "mcp") {
      startMcp();
    } else if (initialProvider) {
      const p = providers.find((x) => x.slug === initialProvider);
      if (p) pick(p);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initialProvider]);

  const onSnippetChange = (text: string) => {
    const parsed = parseMcpConnection(text);
    setMcp((prev) => {
      if (!parsed) return { ...prev, snippet: text, parsedSource: "" };
      const nextSlug = prev.slugTouched ? prev.slug : uniqueSlug(parsed.slug || parsed.name, takenSlugs);
      return {
        ...prev,
        snippet: text,
        parsedSource: parsed.source || "connection snippet",
        name: parsed.name || prev.name,
        slug: nextSlug || prev.slug,
        mcpUrl: parsed.mcpUrl || prev.mcpUrl,
        authType: parsed.authType,
        authName: parsed.authName,
        token: parsed.token || prev.token,
      };
    });
    if (parsed && Object.keys(mcpErrors).length) setMcpErrors({});
  };

  const validateMcp = (d: McpDraft) => {
    const e: Partial<Record<"name" | "slug" | "mcpUrl", string>> = {};
    if (!d.name.trim()) e.name = "Give this MCP server a name.";
    if (!d.slug.trim()) e.slug = "Add a slug. Tool names are prefixed with it.";
    else if (!/^[a-z0-9][a-z0-9_-]*$/.test(d.slug)) e.slug = "Use lowercase letters, numbers, dashes or underscores.";
    const u = d.mcpUrl.trim();
    if (!u) e.mcpUrl = "Add the upstream Streamable HTTP MCP URL, such as https://example.com/mcp/.";
    else if (!/^https?:\/\//i.test(u)) e.mcpUrl = "Start with https:// or http://.";
    return e;
  };

  const submitMcp = async (e: FormEvent) => {
    e.preventDefault();
    const errs = validateMcp(mcp);
    setMcpErrors(errs);
    if (Object.keys(errs).length) {
      const first = document.querySelector<HTMLElement>("#connect-mcp-form [aria-invalid=true]");
      first?.focus();
      return;
    }
    setBusy(true);
    try {
      const mcpUrl = mcp.mcpUrl.trim();
      const s = await create({
        slug: mcp.slug.trim(),
        name: mcp.name.trim(),
        provider: "mcp",
        category: "MCP",
        description: "",
        base_url: originOf(mcpUrl),
        mcp_url: mcpUrl,
        auth_type: mcp.authType,
        auth_name: mcp.authName.trim() || (mcp.authType === "query" ? "api_key" : "Authorization"),
        strategy: "round_robin",
        cooldown_sec: 60,
        max_attempts: 3,
        max_concurrency: 0,
        queue_timeout_sec: 0,
        icon: "",
      });

      const tok = mcp.token.trim();
      if (tok) {
        await api.addKeys(s.id, [
          {
            label: mcp.tokenLabel.trim() || "Default",
            secret: tok,
            tier: "primary",
            hidden: false,
            restricted: false,
            weight: 1,
            priority: 0,
            notes: "",
            app_ids: [],
          },
        ]);
      }

      try {
        const view = await api.syncServiceMcp(s.id);
        toast.success(`Connected ${s.name} — synced ${plural(view.tool_count, "MCP tool")}.`);
      } catch (syncErr) {
        toast.info(
          tok
            ? `${s.name} created, but tool sync failed: ${errorMessage(syncErr)}`
            : `${s.name} created. Add a token on the Keys tab, then sync tools on the MCP tab.`,
        );
      }

      onClose();
      navigate(`/services/${s.id}`);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!form) return;
    const errs = validateService(form);
    setErrors(errs);
    if (Object.keys(errs).length) {
      const first = document.querySelector<HTMLElement>("#add-service-form [aria-invalid=true]");
      first?.focus();
      return;
    }
    setBusy(true);
    try {
      const s = await create(form);
      toast.success(s.auth_type === "oauth2" ? `${s.name} is ready. Set up its OAuth app and connect an account next.` : `${s.name} is ready. Add its keys next.`);
      onClose();
      navigate(`/services/${s.id}`);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const title = mcpMode ? "Connect an MCP server" : form ? `New ${provider?.name ?? ""} service` : "Add service";
  const description = mcpMode
    ? "Paste a Claude Code command, mcp.json snippet, or Streamable HTTP URL. Acnologia pools tokens and exposes the server's tools through /mcp."
    : form
      ? "Defaults come from the provider. Change anything you need, then create."
      : "Pick the provider these keys are for.";

  return (
    <Modal isOpen={open} onOpenChange={(o) => !o && !busy && onClose()} size="lg">
      <ModalHeader title={title} description={description}>
        {form && !mcpMode && (
          <div className="mt-3">
            <AiFillButton ai={ai} />
          </div>
        )}
      </ModalHeader>
      <ModalBody>
        {mcpMode ? (
          <form id="connect-mcp-form" onSubmit={submitMcp} noValidate className="flex flex-col gap-5 pb-1">
            <div className="flex flex-col gap-2">
              <Textarea
                label="Quick paste (optional)"
                rows={3}
                value={mcp.snippet}
                onChange={onSnippetChange}
                placeholder={'claude mcp add --transport http scrapebox https://scrapebox.cofount.dev/mcp/ --header "Authorization: Bearer ..."'}
                hint="Paste a claude mcp add command, Cursor/VS Code mcp.json block, or dashboard details to auto-fill below."
                className="font-mono"
              />
              {mcp.parsedSource && (
                <div className="flex items-center gap-2">
                  <Chip variant="caption" color="lime" className="font-mono">
                    Parsed {mcp.parsedSource}
                  </Chip>
                  {mcp.token && (
                    <span className="text-caption-1-regular text-text-secondary">Token extracted — it will be added to this service's key pool.</span>
                  )}
                </div>
              )}
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Input
                label="Name"
                isRequired
                value={mcp.name}
                onChange={(v) =>
                  setMcp((prev) => ({
                    ...prev,
                    name: v,
                    slug: prev.slugTouched ? prev.slug : uniqueSlug(v, takenSlugs),
                  }))
                }
                isInvalid={!!mcpErrors.name}
                errorMessage={mcpErrors.name}
                placeholder="Scrapebox"
                autoComplete="off"
              />
              <Input
                label="Slug"
                isRequired
                value={mcp.slug}
                onChange={(v) =>
                  setMcp((prev) => ({
                    ...prev,
                    slug: v.toLowerCase().replace(/[^a-z0-9_-]/g, ""),
                    slugTouched: true,
                  }))
                }
                isInvalid={!!mcpErrors.slug}
                errorMessage={mcpErrors.slug}
                hint="Used to namespace synced tools in /mcp."
                placeholder="scrapebox"
                autoComplete="off"
                spellCheck="false"
                inputDir="ltr"
                inputClassName="font-mono"
              />
              <Input
                label="Upstream MCP URL"
                isRequired
                type="url"
                inputMode="url"
                value={mcp.mcpUrl}
                onChange={(v) => setMcp((prev) => ({ ...prev, mcpUrl: v }))}
                isInvalid={!!mcpErrors.mcpUrl}
                errorMessage={mcpErrors.mcpUrl}
                placeholder="https://scrapebox.cofount.dev/mcp/"
                hint="Exact Streamable HTTP endpoint (trailing slash is preserved)."
                spellCheck="false"
                inputDir="ltr"
                inputClassName="font-mono"
                className="sm:col-span-2"
              />
              <Select
                label="Auth type"
                selectedKey={mcp.authType}
                onSelectionChange={(k) => {
                  if (k == null) return;
                  const nextType = k as AuthType;
                  setMcp((prev) => ({
                    ...prev,
                    authType: nextType,
                    authName:
                      nextType === "bearer"
                        ? "Authorization"
                        : nextType === "query"
                          ? prev.authName === "Authorization"
                            ? "api_key"
                            : prev.authName
                          : prev.authName === "api_key"
                            ? "X-API-Key"
                            : prev.authName,
                  }));
                }}
                triggerClassName="w-full"
              >
                {MCP_AUTH_TYPES.map((a) => (
                  <SelectItem key={a.id} id={a.id}>
                    {a.label}
                  </SelectItem>
                ))}
              </Select>
              <Input
                label={mcp.authType === "query" ? "Query parameter name" : "Header name"}
                value={mcp.authName}
                onChange={(v) => setMcp((prev) => ({ ...prev, authName: v }))}
                placeholder={mcp.authType === "query" ? "api_key" : mcp.authType === "bearer" ? "Authorization" : "X-API-Key"}
                spellCheck="false"
                inputDir="ltr"
                inputClassName="font-mono"
              />
              <Input
                label="Token / API key (optional)"
                type="password"
                value={mcp.token}
                onChange={(v) => setMcp((prev) => ({ ...prev, token: v }))}
                placeholder="Paste upstream bearer token or API key"
                hint="Stored encrypted in this service's key pool. You can add more tokens later for rotation."
                autoComplete="off"
                spellCheck="false"
                inputDir="ltr"
                inputClassName="font-mono"
                className="sm:col-span-2"
              />
              {mcp.token.trim() && (
                <Input
                  label="Token label"
                  value={mcp.tokenLabel}
                  onChange={(v) => setMcp((prev) => ({ ...prev, tokenLabel: v }))}
                  placeholder="Default"
                  autoComplete="off"
                  className="sm:col-span-2"
                />
              )}
            </div>
          </form>
        ) : !form ? (
          <ProviderPicker onPick={pick} onConnectMcp={startMcp} onClose={onClose} />
        ) : (
          <form id="add-service-form" onSubmit={submit} noValidate className="pb-1">
            {provider && (
              <div className="mb-5 flex items-center gap-3 rounded-2xl bg-background-secondary-default px-3 py-2.5">
                <ProviderLogo provider={provider} size="sm" />
                <div className="min-w-0 font-mono text-caption-1-regular text-text-secondary">
                  provider <span className="text-text-primary">{provider.slug}</span> · v{provider.version}
                </div>
              </div>
            )}
            <AiFillPanel ai={ai} />
            <ServiceForm
              value={form}
              errors={errors}
              flash={ai.flash}
              onChange={(v) => {
                // Keep slug following the name until the user edits the slug directly.
                if (v.name !== form.name && (form.slug === "" || form.slug === slugify(form.name))) v = { ...v, slug: slugify(v.name) };
                setForm(v);
                if (Object.keys(errors).length) setErrors(validateService(v));
              }}
            />
          </form>
        )}
      </ModalBody>
      {(form || mcpMode) && (
        <ModalFooter className="sm:justify-between">
          <Button
            variant="ghost"
            leadingIcon={RiArrowLeftLine}
            onClick={() => {
              setForm(null);
              setMcpMode(false);
            }}
            disabled={busy}
          >
            Providers
          </Button>
          {mcpMode ? (
            <Button variant="primary" type="submit" form="connect-mcp-form" leadingIcon={busy ? ButtonSpinner : undefined} disabled={busy}>
              {busy ? "Connecting" : "Connect & sync tools"}
            </Button>
          ) : (
            <Button variant="primary" type="submit" form="add-service-form" leadingIcon={busy ? ButtonSpinner : undefined} disabled={busy}>
              {busy ? "Creating" : "Create service"}
            </Button>
          )}
        </ModalFooter>
      )}
    </Modal>
  );
}
