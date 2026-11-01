import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Pencil, TriangleAlert } from "lucide-react";
import { useAuth } from "../../stores/auth";
import { can } from "../../lib/perms";
import type { Service, ServiceInput, Strategy } from "../../lib/types";
import { useServices } from "../../stores/services";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { STRATEGY_HELP } from "../../lib/format";
import { Modal } from "../../components/Modal";
import { ConcurrencyFields, ServiceForm, StrategySelect, validateService, type ServiceErrors } from "../../components/ServiceForm";
import { ConfirmButton } from "../../components/ConfirmButton";
import { LogoEditor } from "../../components/LogoEditor";
import { AiFillButton, AiFillPanel, useAiFill } from "../../components/AiFill";
import { useProviders } from "../../stores/providers";
import { Button, Callout, Card, Field, Input } from "../../components/ui";
import { applyServiceFields } from "../AddServiceModal";

function inputOf(s: Service): ServiceInput {
  const { slug, name, provider, category, description, base_url, auth_type, auth_name, strategy, cooldown_sec, max_attempts, icon } = s;
  return {
    slug,
    name,
    provider,
    category,
    description,
    base_url,
    auth_type,
    auth_name,
    strategy,
    cooldown_sec,
    max_attempts,
    icon,
    max_concurrency: s.max_concurrency ?? 0,
    queue_timeout_sec: s.queue_timeout_sec ?? 0,
  };
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5 py-2 sm:flex-row sm:gap-4">
      <dt className="font-mono text-[11px] uppercase tracking-wide text-faint sm:w-36 sm:shrink-0 sm:pt-0.5">{label}</dt>
      <dd className="wrap-anywhere min-w-0 text-[13px] text-ink">{value || "-"}</dd>
    </div>
  );
}

export function ServiceSettings({ service, canManage }: { service: Service; canManage: boolean }) {
  const isSuper = can.hidden(useAuth((s) => s.user?.role));
  const { update, remove, setLogo, removeLogo } = useServices();
  const provider = useProviders((s) => s.providers.find((p) => p.slug === service.provider));
  const navigate = useNavigate();
  const [strategy, setStrategy] = useState<Strategy>(service.strategy);
  const [cooldown, setCooldown] = useState(service.cooldown_sec);
  const [attempts, setAttempts] = useState(service.max_attempts);
  const [maxConc, setMaxConc] = useState(service.max_concurrency ?? 0);
  const [queueSec, setQueueSec] = useState(service.queue_timeout_sec ?? 0);
  const [editErrors, setEditErrors] = useState<ServiceErrors>({});
  const ai = useAiFill(
    "service",
    () => editing ?? inputOf(service),
    (fields) => setEditing((f) => applyServiceFields(f ?? inputOf(service), fields)),
  );
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<ServiceInput | null>(null);
  const [savingEdit, setSavingEdit] = useState(false);

  useEffect(() => {
    setStrategy(service.strategy);
    setCooldown(service.cooldown_sec);
    setAttempts(service.max_attempts);
    setMaxConc(service.max_concurrency ?? 0);
    setQueueSec(service.queue_timeout_sec ?? 0);
  }, [service]);

  const dirty =
    strategy !== service.strategy ||
    cooldown !== service.cooldown_sec ||
    attempts !== service.max_attempts ||
    maxConc !== (service.max_concurrency ?? 0) ||
    queueSec !== (service.queue_timeout_sec ?? 0);

  const saveStrategy = async () => {
    setSaving(true);
    try {
      await update(service.id, {
        strategy,
        cooldown_sec: cooldown,
        max_attempts: attempts,
        max_concurrency: maxConc,
        queue_timeout_sec: queueSec,
      });
      toast.success("Rotation settings saved");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const saveEdit = async () => {
    if (!editing) return;
    const errs = validateService(editing);
    setEditErrors(errs);
    if (Object.keys(errs).length) return;
    setSavingEdit(true);
    try {
      const { slug: _slug, ...rest } = editing;
      await update(service.id, rest);
      toast.success("Service updated");
      setEditing(null);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSavingEdit(false);
    }
  };

  return (
    <div className="space-y-5">
      {isSuper && !service.target_trusted && (
        <Callout tone="amber" icon={<TriangleAlert className="h-4 w-4" />}>
          <div className="font-medium">Hidden keys are paused for this service</div>
          <div className="mt-0.5">
            Someone other than a superadmin set its base URL or auth, so Cogito will not send hidden keys to it. Open Edit, check the
            base URL, then save to confirm it.
          </div>
        </Callout>
      )}
      <Card className="p-5">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h3 className="text-[15px] font-semibold text-ink">Rotation and concurrency</h3>
            <p className="mt-0.5 text-[13px] text-muted">How Cogito picks the next key, how long a rate limited key rests, and how many requests one key can serve at once.</p>
          </div>
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Field label="Strategy" hint={STRATEGY_HELP[strategy]}>
            <StrategySelect value={strategy} onChange={setStrategy} disabled={!canManage} />
          </Field>
          <Field label="Cooldown (seconds)" hint="Doubles with each failure in a row, up to 1 hour.">
            <Input type="number" min={0} value={cooldown} disabled={!canManage} onChange={(e) => setCooldown(Number(e.target.value))} />
          </Field>
          <Field label="Max attempts" hint="How many keys the proxy tries before giving up.">
            <Input type="number" min={1} max={20} value={attempts} disabled={!canManage} onChange={(e) => setAttempts(Number(e.target.value))} />
          </Field>
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <ConcurrencyFields
            maxConcurrency={maxConc}
            queueSec={queueSec}
            disabled={!canManage}
            onChange={(p) => {
              if (p.max_concurrency !== undefined) setMaxConc(p.max_concurrency);
              if (p.queue_timeout_sec !== undefined) setQueueSec(p.queue_timeout_sec);
            }}
          />
        </div>
        {canManage && (
          <div className="mt-4 flex justify-end gap-2">
            {dirty && (
              <Button
                variant="ghost"
                onClick={() => {
                  setStrategy(service.strategy);
                  setCooldown(service.cooldown_sec);
                  setAttempts(service.max_attempts);
                  setMaxConc(service.max_concurrency ?? 0);
                  setQueueSec(service.queue_timeout_sec ?? 0);
                }}
              >
                Discard
              </Button>
            )}
            <Button variant="primary" disabled={!dirty} loading={saving} onClick={saveStrategy}>
              Save rotation
            </Button>
          </div>
        )}
      </Card>

      <Card className="p-5">
        <h3 className="text-[15px] font-semibold text-ink">Logo</h3>
        <p className="mb-4 mt-0.5 text-[13px] text-muted">Shows in the catalog and on this page.</p>
        <LogoEditor
          src={service.logo_url ?? provider?.logo_url}
          name={service.name}
          color={provider?.color}
          // Own logos are served from /api/logos/services/{id}; anything else is inherited.
          hasOwn={!!service.logo_url && service.logo_url.includes("/logos/services/")}
          disabled={!canManage}
          onUpload={(logo) => setLogo(service.id, logo)}
          onRemove={() => removeLogo(service.id)}
          inheritedNote={provider ? `Without its own logo, the service uses the provider logo.` : undefined}
        />
      </Card>

      <Card className="p-5">
        <div className="flex items-start justify-between gap-4">
          <h3 className="text-[15px] font-semibold text-ink">Details</h3>
          {canManage && (
            <Button
              size="sm"
              icon={<Pencil className="h-3.5 w-3.5" />}
              onClick={() => {
                setEditErrors({});
                setEditing(inputOf(service));
              }}
            >
              Edit
            </Button>
          )}
        </div>
        <dl className="mt-2 divide-y divide-line">
          <Row label="Slug" value={service.slug} />
          <Row label="Provider" value={provider ? `${provider.name} (${service.provider})` : service.provider} />
          <Row label="Base URL" value={service.base_url} />
          <Row label="Auth" value={`${service.auth_type}${service.auth_name ? ` · ${service.auth_name}` : ""}`} />
          <Row label="Category" value={service.category} />
          <Row label="Description" value={service.description} />
          <Row label="Concurrency" value={service.max_concurrency ? `${service.max_concurrency} per key, waits up to ${service.queue_timeout_sec}s for a free key` : "No limit"} />
        </dl>
      </Card>

      {canManage && (
        <Card className="flex flex-col gap-3 p-5 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h3 className="text-[15px] font-semibold text-ink">Delete service</h3>
            <p className="mt-0.5 text-[13px] text-muted">Removes the service and all its keys. Apps that call {service.slug} start failing right away.</p>
          </div>
          <ConfirmButton
            label="Delete service"
            confirmLabel="Delete forever"
            onConfirm={async () => {
              try {
                await remove(service.id);
                toast.success(`${service.name} deleted`);
                navigate("/");
              } catch (e) {
                toast.error(errorMessage(e));
              }
            }}
          />
        </Card>
      )}

      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        wide
        title={`Edit ${service.name}`}
        headerAction={<AiFillButton ai={ai} />}
        footer={
          <>
            <Button variant="ghost" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button variant="primary" loading={savingEdit} onClick={saveEdit}>
              Save
            </Button>
          </>
        }
      >
        {editing && (
          <>
            <AiFillPanel ai={ai} />
            <ServiceForm
              value={editing}
              errors={editErrors}
              flash={ai.flash}
              onChange={(v) => {
                setEditing(v);
                if (Object.keys(editErrors).length) setEditErrors(validateService(v));
              }}
              slugLocked
            />
          </>
        )}
      </Modal>
    </div>
  );
}
