import { useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowLeft, ExternalLink } from "lucide-react";
import { useServices } from "../stores/services";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { slugify } from "../lib/format";
import type { AuthType, Provider, ProviderTemplate, ServiceInput } from "../lib/types";
import { Modal } from "../components/Modal";
import { ServiceForm } from "../components/ServiceForm";
import { ServiceIcon } from "../components/ServiceIcon";
import { Button, Spinner } from "../components/ui";

const FALLBACK: ProviderTemplate[] = [
  { provider: "serpapi", name: "SerpAPI", category: "Search", description: "Google and other search engine results as JSON.", base_url: "https://serpapi.com", auth_type: "query", auth_name: "api_key", icon: "serpapi", default_cooldown_sec: 3600, docs_url: "https://serpapi.com/search-api" },
  { provider: "elevenlabs", name: "ElevenLabs", category: "Audio", description: "Text to speech and voice AI.", base_url: "https://api.elevenlabs.io", auth_type: "header", auth_name: "xi-api-key", icon: "elevenlabs", default_cooldown_sec: 5, docs_url: "https://elevenlabs.io/docs/api-reference" },
  { provider: "generic", name: "Generic", category: "Other", description: "Any API key based HTTP service.", base_url: "", auth_type: "bearer", auth_name: "Authorization", icon: "generic", default_cooldown_sec: 60, docs_url: "" },
];

function fromTemplate(t: ProviderTemplate, taken: Set<string>): ServiceInput {
  const provider = (["serpapi", "elevenlabs", "generic"].includes(t.provider) ? t.provider : "generic") as Provider;
  let slug = provider === "generic" ? "" : slugify(t.name);
  if (slug && taken.has(slug)) {
    let i = 2;
    while (taken.has(`${slug}-${i}`)) i++;
    slug = `${slug}-${i}`;
  }
  return {
    slug,
    name: provider === "generic" ? "" : t.name,
    provider,
    category: t.category,
    description: t.description,
    base_url: t.base_url,
    auth_type: (["query", "header", "bearer"].includes(t.auth_type) ? t.auth_type : "header") as AuthType,
    auth_name: t.auth_name,
    strategy: "round_robin",
    cooldown_sec: t.default_cooldown_sec || 60,
    max_attempts: 3,
    icon: t.icon,
  };
}

export function AddServiceModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { fetchProviders, create, services } = useServices();
  const navigate = useNavigate();
  const [providers, setProviders] = useState<ProviderTemplate[] | null>(null);
  const [template, setTemplate] = useState<ProviderTemplate | null>(null);
  const [form, setForm] = useState<ServiceInput | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setTemplate(null);
    setForm(null);
    fetchProviders()
      .then((p) => setProviders(p.length ? p : FALLBACK))
      .catch(() => setProviders(FALLBACK));
  }, [open, fetchProviders]);

  const pick = (t: ProviderTemplate) => {
    setTemplate(t);
    setForm(fromTemplate(t, new Set(services.map((s) => s.slug))));
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!form) return;
    if (!form.slug || !form.name || !form.base_url) {
      toast.error("Name, slug and base URL are required.");
      return;
    }
    setBusy(true);
    try {
      const s = await create(form);
      toast.success(`${s.name} added. Now add some keys.`);
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
      title={form ? `New ${template?.name ?? ""} service` : "Add service"}
      description={form ? "Review the defaults from the template and adjust anything you need." : "Start from a provider template."}
      footer={
        form ? (
          <>
            <Button variant="ghost" icon={<ArrowLeft className="h-4 w-4" />} onClick={() => setForm(null)}>
              Templates
            </Button>
            <Button variant="primary" type="submit" form="add-service-form" loading={busy}>
              Create service
            </Button>
          </>
        ) : undefined
      }
    >
      {!form ? (
        providers === null ? (
          <div className="flex justify-center py-10">
            <Spinner />
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-3">
            {providers.map((p) => (
              <button
                key={p.provider + p.name}
                type="button"
                onClick={() => pick(p)}
                className="flex flex-col items-start gap-3 rounded-card border border-line bg-white p-4 text-left transition-colors hover:border-ink/30"
              >
                <ServiceIcon icon={p.icon} provider={p.provider} name={p.name} />
                <div>
                  <div className="text-sm font-medium text-ink">{p.name}</div>
                  <div className="mt-0.5 font-mono text-[11px] text-muted">
                    {p.category || "other"} · {p.auth_type}
                  </div>
                  {p.description && <div className="mt-2 text-xs text-muted">{p.description}</div>}
                </div>
                {p.docs_url && (
                  <a
                    href={p.docs_url}
                    target="_blank"
                    rel="noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    className="inline-flex items-center gap-1 text-[11px] text-muted underline-offset-2 hover:text-ink hover:underline"
                  >
                    docs <ExternalLink className="h-3 w-3" />
                  </a>
                )}
              </button>
            ))}
          </div>
        )
      ) : (
        <form id="add-service-form" onSubmit={submit}>
          <ServiceForm
            value={form}
            onChange={(v) => {
              // Keep slug following the name until the user edits the slug directly.
              if (v.name !== form.name && (form.slug === "" || form.slug === slugify(form.name))) v = { ...v, slug: slugify(v.name) };
              setForm(v);
            }}
          />
        </form>
      )}
    </Modal>
  );
}
