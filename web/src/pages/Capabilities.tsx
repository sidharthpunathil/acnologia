import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { RiAddLine, RiArrowRightSLine, RiCodeSSlashLine, RiSparklingLine } from "@remixicon/react";
import { AcnServiceGroup } from "@/components/foundations/icons/acn";
import { useTitle } from "../hooks/useTitle";
import { useServices } from "../stores/services";
import { useAuth } from "../stores/auth";
import { NEW_DRAFT, useAssistant } from "../stores/assistant";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { plural } from "../lib/format";
import { ops, STRATEGIES, type Capability, type CapabilityInput } from "../lib/ops";
import { Modal } from "../components/Modal";
import { CopyButton } from "../components/CopyButton";
import { ConfirmButton } from "../components/ConfirmButton";
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Textarea } from "@/components/base/textarea/textarea";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout, EmptyState, PageHeader, PageSkeleton } from "@/components/acn";
import { FallbackArt } from "@/components/foundations/illustrations";
import { cx } from "@/utils/cx";

/** What a new capability starts from: the shape, with two of your services filled in. */
function template(services: string[]): CapabilityInput {
  const [a = "service-a", b = "service-b"] = services;
  return {
    slug: "maps-search",
    name: "Search places",
    description: "Find businesses by keyword and location.",
    strategy: "priority",
    params: [
      { name: "query", type: "string", required: true, description: "What and where, such as cafes in Kochi" },
      { name: "limit", type: "integer", default: 20 },
    ],
    routes: [
      {
        service: a,
        method: "GET",
        path: "/search",
        query: { query: "{{query}}", limit: "{{limit}}" },
        output: { items: "results", fields: { name: "name", rating: "rating", address: "address" } },
      },
      {
        service: b,
        method: "GET",
        path: "/search.json",
        query: { engine: "google_maps", q: "{{query}}" },
        output: { items: "local_results", fields: { name: "title", rating: "rating", address: "address" } },
      },
    ],
  };
}

const ASSISTANT_PROMPT =
  "Set up a capability so my apps can call one name for the same job across my providers: ";

function CapabilityEditor({
  open,
  initial,
  onClose,
  onSaved,
}: {
  open: boolean;
  initial: Capability | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const services = useServices((s) => s.services);
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setError(null);
    const start: CapabilityInput = initial
      ? { slug: initial.slug, name: initial.name, description: initial.description, strategy: initial.strategy, params: initial.params, routes: initial.routes }
      : template(services.map((s) => s.slug));
    setText(JSON.stringify(start, null, 2));
  }, [open, initial, services]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    let body: CapabilityInput;
    try {
      body = JSON.parse(text) as CapabilityInput;
    } catch (err) {
      setError(`Not valid JSON: ${(err as Error).message}`);
      return;
    }
    setBusy(true);
    try {
      if (initial) {
        const { slug: _slug, ...rest } = body;
        await ops.updateCapability(initial.id, rest);
        toast.success(`Saved ${body.name ?? initial.name}`);
      } else {
        const made = await ops.createCapability(body);
        toast.success(`Created ${made.name}. Call it at /proxy/${made.slug}`);
      }
      onClose();
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      wide
      title={initial ? `Edit ${initial.name}` : "Create capability"}
      description={
        initial
          ? `/proxy/${initial.slug}. The slug stays as it is.`
          : "Params are what apps send. Each route maps them into one service's request with {{param}}, and output maps the answer to shared fields."
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="capability-form" disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
            {initial ? "Save" : "Create capability"}
          </Button>
        </>
      }
    >
      <form id="capability-form" onSubmit={submit} className="flex flex-col gap-3" noValidate>
        <Textarea
          label="Capability as JSON"
          rows={22}
          value={text}
          onChange={(v) => {
            setText(v);
            if (error) setError(null);
          }}
          isInvalid={!!error}
          spellCheck="false"
          fieldClassName="font-mono text-caption-1-regular"
          hint={error ?? "strategy: priority, round_robin, random, weighted, cheapest or most_keys. A route can set weight, disabled, method and body."}
        />
      </form>
    </Modal>
  );
}

function CapabilityCard({
  c,
  manage,
  known,
  onEdit,
  onDeleted,
}: {
  c: Capability;
  manage: boolean;
  known: Set<string>;
  onEdit: () => void;
  onDeleted: () => void;
}) {
  const origin = window.location.origin;
  const example = c.params.filter((p) => p.required).map((p) => `${p.name}=...`).join("&");
  return (
    <li className="rounded-3xl border border-border-button-default bg-background-primary-default p-5">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span className="min-w-0 wrap-anywhere text-headline-medium text-text-primary">{c.name}</span>
            <span className="wrap-anywhere font-mono text-caption-1-regular text-text-secondary">{c.slug}</span>
            <span
              className="rounded-md bg-background-tertiary-default px-1.5 py-0.5 text-caption-1-medium text-text-secondary"
              title={STRATEGIES[c.strategy]?.hint}
            >
              {STRATEGIES[c.strategy]?.label ?? c.strategy}
            </span>
          </div>
          {c.description && <p className="mt-0.5 wrap-anywhere text-body-2-regular text-text-secondary">{c.description}</p>}

          {c.params.length > 0 && (
            <ul aria-label="Params" className="mt-3 flex flex-wrap gap-1 font-mono text-caption-1-regular">
              {c.params.map((p) => (
                <li key={p.name} className="rounded-md border border-border-button-default px-1.5 py-0.5 text-text-primary" title={p.description}>
                  {p.name}
                  <span className="text-text-tertiary">: {p.type ?? "string"}</span>
                  {p.required && <span className="text-text-error-primary" aria-label="required"> *</span>}
                </li>
              ))}
            </ul>
          )}

          <ol aria-label="Routes, in priority order" className="mt-3 flex flex-col gap-1">
            {c.routes.map((r, i) => {
              const missing = !known.has(r.service);
              return (
                <li
                  key={`${r.service}-${i}`}
                  className={cx("flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-caption-1-regular", r.disabled && "opacity-50")}
                >
                  <span className="w-4 shrink-0 text-end tabular-nums text-text-tertiary">{i + 1}</span>
                  <span
                    className={cx(
                      "rounded-md px-1.5 py-0.5 font-mono",
                      missing ? "bg-status-rose-background text-status-rose-text" : "bg-background-tertiary-default text-text-primary",
                    )}
                    title={missing ? "No service with this slug" : undefined}
                  >
                    {r.service}
                  </span>
                  <RiArrowRightSLine className="size-3.5 shrink-0 text-foreground-icon-tertiary" aria-hidden />
                  <code className="min-w-0 truncate font-mono text-text-secondary" dir="ltr">
                    {(r.method ?? "GET").toUpperCase()} {r.path}
                  </code>
                  <span className="text-text-tertiary">
                    {r.output ? "mapped" : "as is"}
                    {r.disabled ? ", off" : ""}
                    {c.strategy === "weighted" ? `, weight ${r.weight || 1}` : ""}
                  </span>
                </li>
              );
            })}
          </ol>

          <div className="mt-3 flex min-w-0 max-w-md items-center gap-1 rounded-xl border border-border-button-default bg-background-secondary-default py-0.5 ps-2.5 pe-0.5">
            <code className="min-w-0 flex-1 truncate font-mono text-caption-1-regular text-text-secondary" dir="ltr">
              /proxy/{c.slug}
              {example ? `?${example}` : ""}
            </code>
            <CopyButton text={`${origin}/proxy/${c.slug}`} label="Copy URL" />
          </div>
        </div>
        {manage && (
          <div className="-me-1.5 -mt-1 flex shrink-0 items-center gap-1">
            <IconButton icon={RiCodeSSlashLine} aria-label={`Edit ${c.name}`} className="max-sm:size-11" onClick={onEdit} />
            <ConfirmButton
              iconOnly
              variant="outline"
              size="medium"
              label="Delete capability"
              confirmLabel="Delete"
              onConfirm={async () => {
                try {
                  await ops.deleteCapability(c.id);
                  toast.success(`Deleted ${c.name}`);
                  onDeleted();
                } catch (e) {
                  toast.error(errorMessage(e));
                }
              }}
            />
          </div>
        )}
      </div>
    </li>
  );
}

export function CapabilitiesPage() {
  useTitle("Capabilities");
  const navigate = useNavigate();
  const manage = can.manage(useAuth((s) => s.user?.role));
  const { services, loaded: servicesLoaded, fetch: fetchServices } = useServices();
  const [caps, setCaps] = useState<Capability[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<Capability | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await ops.listCapabilities();
      setCaps(res.capabilities ?? []);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => {
    void load();
    if (!servicesLoaded) fetchServices().catch(() => undefined);
  }, [load, fetchServices, servicesLoaded]);

  const known = new Set(services.map((s) => s.slug));
  const openCreate = () => {
    setEditing(null);
    setEditorOpen(true);
  };
  const askAssistant = () => {
    useAssistant.getState().setDraftText(NEW_DRAFT, ASSISTANT_PROMPT);
    navigate("/assistant", { state: { fresh: true } });
  };

  if (!loaded) return <PageSkeleton variant="list" />;

  const actions = manage && (
    <div className="flex flex-wrap gap-2">
      <Button variant="secondary" leadingIcon={RiSparklingLine} onClick={askAssistant}>
        Build with the Assistant
      </Button>
      <Button variant="primary" leadingIcon={RiAddLine} onClick={openCreate}>
        Create capability
      </Button>
    </div>
  );

  return (
    <div>
      <PageHeader title="Capabilities" meta={plural(caps.length, "capability", "capabilities")} actions={actions} />
      <Callout tone="neutral" className="mb-6">
        A capability is one job, such as searching Google Maps, that several providers can do with APIs of their own. Apps call{" "}
        <code className="font-mono">/proxy/&lt;slug&gt;</code> with its params, or use it as one MCP tool. Acnologia picks a provider by the strategy,
        rotates that provider's keys, and moves to the next provider when one is out of keys, quota or working order. Mapped routes answer in one shape
        whichever provider served; add <code className="font-mono">_raw=1</code> for the provider's own body.
      </Callout>

      {error && caps.length === 0 ? (
        <EmptyState title="Capabilities are not available" body={error} />
      ) : caps.length === 0 ? (
        <EmptyState
          art={FallbackArt}
          icon={AcnServiceGroup}
          title="No capabilities yet"
          body={
            manage
              ? "When two providers can do the same job, put them behind one name. The Assistant can read their guides and write the mapping for you."
              : "Ask an admin to create one."
          }
          action={actions || undefined}
        />
      ) : (
        <ul className="flex flex-col gap-3">
          {caps.map((c) => (
            <CapabilityCard
              key={c.id}
              c={c}
              manage={manage}
              known={known}
              onEdit={() => {
                setEditing(c);
                setEditorOpen(true);
              }}
              onDeleted={() => setCaps((list) => list.filter((x) => x.id !== c.id))}
            />
          ))}
        </ul>
      )}

      <CapabilityEditor open={editorOpen} initial={editing} onClose={() => setEditorOpen(false)} onSaved={() => void load()} />
    </div>
  );
}
