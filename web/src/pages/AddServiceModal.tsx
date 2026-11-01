import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { RiArrowLeftLine, RiArrowRightSLine, RiEditLine, RiSparkling2Line } from "@remixicon/react";
import { useServices } from "../stores/services";
import { useProviders } from "../stores/providers";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { slugify } from "../lib/format";
import { STRATEGY_VALUES } from "../lib/providerSpec";
import type { Provider, ServiceInput, Strategy } from "../lib/types";
import { ServiceForm, validateService, type ServiceErrors } from "../components/ServiceForm";
import { ProviderLogo } from "../components/EntityLogo";
import { useSheetLayout } from "../components/Modal";
import { AiFillButton, AiFillPanel, useAiFill } from "../components/AiFill";
import { Callout } from "@/components/acn";
import { Chip } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "@/components/base/modal/modal";
import { SearchField } from "@/components/base/search-field/search-field";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { cx } from "@/utils/cx";

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
              <button type="button" onClick={() => onPick(p)} className={row}>
                <ProviderLogo provider={p} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2">
                    <span className="truncate text-body-medium text-text-primary">{p.name}</span>
                    {p.source === "ai" && (
                      <Chip variant="caption" color="purple" className="rounded-full px-2 py-0 font-mono">
                        ai
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
    <Modal isOpen={open} onOpenChange={(o) => !o && !busy && onClose()} size="lg">
      <ModalHeader
        title={form ? `New ${provider?.name ?? ""} service` : "Add service"}
        description={form ? "Defaults come from the provider. Change anything you need, then create." : "Pick the provider these keys are for."}
      >
        {form && (
          <div className="mt-3">
            <AiFillButton ai={ai} />
          </div>
        )}
      </ModalHeader>
      <ModalBody>
        {!form ? (
          <ProviderPicker onPick={pick} onClose={onClose} />
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
      {form && (
        <ModalFooter className="sm:justify-between">
          <Button variant="ghost" leadingIcon={RiArrowLeftLine} onClick={() => setForm(null)} disabled={busy}>
            Providers
          </Button>
          <Button variant="primary" type="submit" form="add-service-form" leadingIcon={busy ? ButtonSpinner : undefined} disabled={busy}>
            {busy ? "Creating" : "Create service"}
          </Button>
        </ModalFooter>
      )}
    </Modal>
  );
}
