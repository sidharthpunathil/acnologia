import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { ArrowLeft, ChevronRight, PenLine, Search, Sparkles } from "lucide-react";
import { useServices } from "../stores/services";
import { useProviders } from "../stores/providers";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { slugify } from "../lib/format";
import { STRATEGY_VALUES } from "../lib/providerSpec";
import type { Provider, ServiceInput, Strategy } from "../lib/types";
import { Modal } from "../components/Modal";
import { ServiceForm, validateService, type ServiceErrors } from "../components/ServiceForm";
import { ProviderLogo } from "../components/EntityLogo";
import { AiFillButton, AiFillPanel, useAiFill } from "../components/AiFill";
import { Badge, Button, Callout, Skeleton } from "../components/ui";

export function serviceFromProvider(p: Provider, taken: Set<string>): ServiceInput {
  const generic = p.slug === "generic";
  let slug = generic ? "" : slugify(p.slug || p.name);
  if (slug && taken.has(slug)) {
    let i = 2;
    while (taken.has(`${slug}-${i}`)) i++;
    slug = `${slug}-${i}`;
  }
  const d = p.spec.defaults;
  return {
    slug,
    name: generic ? "" : p.name,
    provider: p.slug,
    category: p.category,
    description: p.description,
    base_url: p.spec.base_url || p.base_url,
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

function ProviderPicker({ onPick, onClose }: { onPick: (p: Provider) => void; onClose: () => void }) {
  const { providers, loaded, error, ensure } = useProviders();
  const role = useAuth((s) => s.user?.role);
  const [q, setQ] = useState("");

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

  return (
    <div className="space-y-3">
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-faint" aria-hidden="true" />
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search providers"
          type="search"
          enterKeyHint="search"
          aria-label="Search providers"
          className="field-input h-10 w-full rounded-xl border border-line bg-surface pl-9 pr-3 text-sm placeholder:text-faint focus:border-ink/60 focus:outline-none focus:ring-2 focus:ring-ink/20"
        />
      </div>

      {role !== "member" && (
        <Link
          to="/providers/ai"
          onClick={onClose}
          className="group flex items-center gap-3 rounded-card border border-brand/20 bg-brand-soft/50 px-4 py-3 transition-colors hover:border-brand/40"
        >
          <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-[11px] bg-surface text-brand shadow-card">
            <Sparkles className="h-4 w-4" />
          </span>
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-medium text-ink">Build a new provider with AI</span>
            <span className="block text-xs text-muted">Not listed? Give the AI a name or a docs URL. It researches, tests and publishes the provider.</span>
          </span>
          <ChevronRight className="h-4 w-4 shrink-0 text-faint transition-transform group-hover:translate-x-0.5" />
        </Link>
      )}

      {!loaded ? (
        <div className="space-y-2">
          {[0, 1, 2].map((i) => (
            <div key={i} className="flex items-center gap-3 rounded-card border border-line px-4 py-3">
              <Skeleton className="h-10 w-10 rounded-[11px]" />
              <div className="flex-1">
                <Skeleton className="h-4 w-32" />
                <Skeleton className="mt-2 h-3 w-48" />
              </div>
            </div>
          ))}
        </div>
      ) : error && !providers.length ? (
        <Callout tone="red">Could not load providers: {error}</Callout>
      ) : shown.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted">No provider matches "{q}". Build it with AI above, or pick Generic.</p>
      ) : (
        <ul className="space-y-2">
          {shown.map((p) => (
            <li key={p.slug}>
              <button
                type="button"
                onClick={() => onPick(p)}
                className="group flex w-full items-center gap-3 rounded-card border border-line bg-surface px-4 py-3 text-left transition-colors hover:border-ink/25 hover:bg-soft"
              >
                <ProviderLogo provider={p} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2">
                    <span className="truncate text-sm font-medium text-ink">{p.name}</span>
                    {p.source === "ai" && <Badge tone="violet">ai</Badge>}
                  </span>
                  <span className="mt-0.5 block truncate font-mono text-[11px] text-muted">
                    {p.category || "other"} · {p.spec.auth.type}
                    {p.spec.base_url ? ` · ${p.spec.base_url.replace(/^https?:\/\//, "")}` : ""}
                  </span>
                </span>
                <ChevronRight className="h-4 w-4 shrink-0 text-faint transition-transform group-hover:translate-x-0.5" />
              </button>
            </li>
          ))}
        </ul>
      )}
      {role !== "member" && (
        <p className="pt-1 text-center text-xs text-muted">
          Prefer writing rules yourself?{" "}
          <Link to="/providers/new" onClick={onClose} className="tap-inline inline-flex items-center gap-1 font-medium text-ink underline-offset-2 hover:underline">
            <PenLine className="h-3 w-3" /> Create a provider manually
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
  const [errors, setErrors] = useState<ServiceErrors>({});
  const [busy, setBusy] = useState(false);

  const ai = useAiFill(
    "service",
    () => form ?? {},
    (fields) => setForm((f) => (f ? applyServiceFields(f, fields) : f)),
  );

  const pick = (p: Provider) => {
    setProvider(p);
    setErrors({});
    setForm(serviceFromProvider(p, new Set(services.map((s) => s.slug))));
  };

  useEffect(() => {
    if (!open) return;
    setProvider(null);
    setForm(null);
    setErrors({});
    ai.setOpen(false);
    if (initialProvider) {
      const p = providers.find((x) => x.slug === initialProvider);
      if (p) pick(p);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initialProvider]);

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
      toast.success(`${s.name} is ready. Add its keys next.`);
      onClose();
      navigate(`/services/${s.id}`);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      wide
      title={form ? `New ${provider?.name ?? ""} service` : "Add service"}
      description={form ? "Defaults come from the provider. Change anything you need, then create." : "Pick the provider these keys are for."}
      headerAction={form ? <AiFillButton ai={ai} /> : undefined}
      footer={
        form ? (
          <>
            <Button variant="ghost" icon={<ArrowLeft className="h-4 w-4" />} onClick={() => setForm(null)} className="mr-auto">
              Providers
            </Button>
            <Button variant="primary" type="submit" form="add-service-form" loading={busy}>
              Create service
            </Button>
          </>
        ) : undefined
      }
    >
      {!form ? (
        <ProviderPicker onPick={pick} onClose={onClose} />
      ) : (
        <form id="add-service-form" onSubmit={submit} noValidate>
          {provider && (
            <div className="mb-5 flex items-center gap-3 rounded-xl bg-soft px-3 py-2.5">
              <ProviderLogo provider={provider} size="sm" />
              <div className="min-w-0 font-mono text-[11px] text-muted">
                provider <span className="text-ink">{provider.slug}</span> · v{provider.version}
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
    </Modal>
  );
}
