import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Pencil } from "lucide-react";
import type { Service, ServiceInput, Strategy } from "../../lib/types";
import { useServices } from "../../stores/services";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { STRATEGY_HELP } from "../../lib/format";
import { Modal } from "../../components/Modal";
import { ServiceForm, StrategySelect } from "../../components/ServiceForm";
import { ConfirmButton } from "../../components/ConfirmButton";
import { Button, Card, Field, Input } from "../../components/ui";

function inputOf(s: Service): ServiceInput {
  const { slug, name, provider, category, description, base_url, auth_type, auth_name, strategy, cooldown_sec, max_attempts, icon } = s;
  return { slug, name, provider, category, description, base_url, auth_type, auth_name, strategy, cooldown_sec, max_attempts, icon };
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5 py-2 sm:flex-row sm:gap-4">
      <dt className="font-mono text-[11px] uppercase tracking-wide text-faint sm:w-36 sm:shrink-0 sm:pt-0.5">{label}</dt>
      <dd className="min-w-0 break-words text-[13px] text-ink">{value || "-"}</dd>
    </div>
  );
}

export function ServiceSettings({ service, canManage }: { service: Service; canManage: boolean }) {
  const { update, remove } = useServices();
  const navigate = useNavigate();
  const [strategy, setStrategy] = useState<Strategy>(service.strategy);
  const [cooldown, setCooldown] = useState(service.cooldown_sec);
  const [attempts, setAttempts] = useState(service.max_attempts);
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<ServiceInput | null>(null);
  const [savingEdit, setSavingEdit] = useState(false);

  useEffect(() => {
    setStrategy(service.strategy);
    setCooldown(service.cooldown_sec);
    setAttempts(service.max_attempts);
  }, [service]);

  const dirty = strategy !== service.strategy || cooldown !== service.cooldown_sec || attempts !== service.max_attempts;

  const saveStrategy = async () => {
    setSaving(true);
    try {
      await update(service.id, { strategy, cooldown_sec: cooldown, max_attempts: attempts });
      toast.success("Rotation settings saved");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const saveEdit = async () => {
    if (!editing) return;
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
      <Card className="p-5">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h3 className="text-[15px] font-semibold text-ink">Rotation</h3>
            <p className="mt-0.5 text-[13px] text-muted">How Cogito chooses the next key and how long a rate limited key rests.</p>
          </div>
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Field label="Strategy" hint={STRATEGY_HELP[strategy]}>
            <StrategySelect value={strategy} onChange={setStrategy} disabled={!canManage} />
          </Field>
          <Field label="Cooldown (seconds)" hint="Doubles per consecutive failure, capped at 1h.">
            <Input type="number" min={0} value={cooldown} disabled={!canManage} onChange={(e) => setCooldown(Number(e.target.value))} />
          </Field>
          <Field label="Max attempts" hint="Proxy retries before giving up.">
            <Input type="number" min={1} max={20} value={attempts} disabled={!canManage} onChange={(e) => setAttempts(Number(e.target.value))} />
          </Field>
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
        <div className="flex items-start justify-between gap-4">
          <h3 className="text-[15px] font-semibold text-ink">Details</h3>
          {canManage && (
            <Button size="sm" icon={<Pencil className="h-3.5 w-3.5" />} onClick={() => setEditing(inputOf(service))}>
              Edit
            </Button>
          )}
        </div>
        <dl className="mt-2 divide-y divide-line">
          <Row label="Slug" value={service.slug} />
          <Row label="Provider" value={service.provider} />
          <Row label="Base URL" value={service.base_url} />
          <Row label="Auth" value={`${service.auth_type}${service.auth_name ? ` · ${service.auth_name}` : ""}`} />
          <Row label="Category" value={service.category} />
          <Row label="Description" value={service.description} />
        </dl>
      </Card>

      {canManage && (
        <Card className="flex flex-col gap-3 p-5 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h3 className="text-[15px] font-semibold text-ink">Delete service</h3>
            <p className="mt-0.5 text-[13px] text-muted">Removes the service and all of its keys. Apps calling {service.slug} will start failing.</p>
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
        {editing && <ServiceForm value={editing} onChange={setEditing} slugLocked />}
      </Modal>
    </div>
  );
}
