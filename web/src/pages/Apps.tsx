import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Boxes, Pencil, Plus, RefreshCw, TriangleAlert } from "lucide-react";
import { useApps } from "../stores/apps";
import { useServices } from "../stores/services";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { dateTime, plural, relTime } from "../lib/format";
import type { App, AppInput, AppMode, AppWithToken } from "../lib/types";
import { useNow } from "../hooks/useNow";
import { Modal } from "../components/Modal";
import { MultiSelect } from "../components/MultiSelect";
import { SecretBox } from "../components/CopyButton";
import { ConfirmButton } from "../components/ConfirmButton";
import { Badge, Button, Empty, Field, IconButton, Input, PageHeader, PageLoading, Select, Textarea, Toggle } from "../components/ui";

const MODE_HELP: Record<AppMode, string> = {
  proxy: "Calls go through /proxy. The app never sees a raw key.",
  lease: "The app leases raw keys and calls the provider itself.",
  both: "Allows both proxy calls and leases.",
};

const blank: AppInput = { name: "", description: "", mode: "proxy", allowed_services: [] };

function AppFormModal({
  open,
  initial,
  onClose,
  onCreated,
}: {
  open: boolean;
  initial: App | null;
  onClose: () => void;
  onCreated: (r: AppWithToken) => void;
}) {
  const { create, update } = useApps();
  const services = useServices((s) => s.services);
  const [form, setForm] = useState<AppInput>(blank);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setForm(
      initial
        ? { name: initial.name, description: initial.description, mode: initial.mode, allowed_services: initial.allowed_services ?? [] }
        : blank,
    );
  }, [open, initial]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!form.name.trim()) {
      toast.error("Give the app a name.");
      return;
    }
    setBusy(true);
    try {
      if (initial) {
        await update(initial.id, { ...form, name: form.name.trim() });
        toast.success(`Saved ${form.name}`);
        onClose();
      } else {
        const res = await create({ ...form, name: form.name.trim() });
        onClose();
        onCreated(res);
      }
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
      title={initial ? `Edit ${initial.name}` : "Create app"}
      description={initial ? undefined : "An app is a consumer such as a website or an agent. It gets its own token."}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="app-form" loading={busy}>
            {initial ? "Save" : "Create app"}
          </Button>
        </>
      }
    >
      <form id="app-form" onSubmit={submit} className="space-y-4">
        <Field label="Name">
          <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="marketing site" required />
        </Field>
        <Field label="Description">
          <Textarea rows={2} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
        </Field>
        <Field label="Mode" hint={MODE_HELP[form.mode]}>
          <Select value={form.mode} onChange={(e) => setForm({ ...form, mode: e.target.value as AppMode })}>
            <option value="proxy">proxy</option>
            <option value="lease">lease</option>
            <option value="both">both</option>
          </Select>
        </Field>
        <div>
          <div className="mb-1.5 text-[13px] font-medium text-ink">Allowed services</div>
          <MultiSelect
            options={services.map((s) => ({ value: s.slug, label: s.name, meta: s.slug }))}
            value={form.allowed_services}
            onChange={(v) => setForm({ ...form, allowed_services: v })}
            empty="No services yet. Add one from the catalog first."
          />
          <div className="mt-1 text-xs text-muted">
            {form.allowed_services.length ? `Limited to ${plural(form.allowed_services.length, "service")}.` : "None selected means the app can use every service."}
          </div>
        </div>
      </form>
    </Modal>
  );
}

function TokenModal({ result, rotated, onClose }: { result: AppWithToken | null; rotated: boolean; onClose: () => void }) {
  return (
    <Modal
      open={!!result}
      onClose={onClose}
      title={rotated ? `New token for ${result?.app.name}` : `${result?.app.name} created`}
      footer={
        <Button variant="primary" onClick={onClose}>
          I have saved it
        </Button>
      }
    >
      {result && (
        <div className="space-y-3">
          <div className="flex items-start gap-2 rounded-xl bg-amber-50 px-3 py-2.5 text-xs text-amber-800">
            <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" />
            <span>
              Copy this token now. It is shown once and Cogito only stores a hash of it.
              {rotated && " The previous token stopped working immediately."}
            </span>
          </div>
          <SecretBox value={result.token} />
          <p className="font-mono text-[11px] text-muted">Send it as Authorization: Bearer {result.app.token_prefix}...</p>
        </div>
      )}
    </Modal>
  );
}

export function AppsPage() {
  const { apps, loaded, error, fetch, update, remove, rotate } = useApps();
  const { services, loaded: servicesLoaded, fetch: fetchServices } = useServices();
  const role = useAuth((s) => s.user?.role);
  const manage = can.manage(role);
  const now = useNow(30000);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<App | null>(null);
  const [token, setToken] = useState<{ result: AppWithToken; rotated: boolean } | null>(null);
  const [busyId, setBusyId] = useState<number | null>(null);

  useEffect(() => {
    fetch().catch(() => undefined);
    if (!servicesLoaded) fetchServices().catch(() => undefined);
  }, [fetch, fetchServices, servicesLoaded]);

  const nameBySlug = useMemo(() => Object.fromEntries(services.map((s) => [s.slug, s.name])), [services]);
  const sorted = useMemo(() => [...apps].sort((a, b) => a.name.localeCompare(b.name)), [apps]);

  const doRotate = async (a: App) => {
    setBusyId(a.id);
    try {
      const res = await rotate(a.id);
      setToken({ result: res, rotated: true });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusyId(null);
    }
  };

  const toggleActive = async (a: App, v: boolean) => {
    try {
      await update(a.id, { active: v });
      toast.success(`${a.name} ${v ? "activated" : "paused"}`);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  if (!loaded) return <PageLoading />;

  const active = apps.filter((a) => a.active).length;

  return (
    <div>
      <PageHeader
        title="Apps"
        meta={`${plural(apps.length, "app")} · ${active} active`}
        actions={
          manage && (
            <Button
              variant="primary"
              icon={<Plus className="h-4 w-4" />}
              onClick={() => {
                setEditing(null);
                setFormOpen(true);
              }}
            >
              Create app
            </Button>
          )
        }
      />

      {error && apps.length === 0 ? (
        <Empty title="Apps are not available" body={error} />
      ) : sorted.length === 0 ? (
        <Empty
          icon={<Boxes className="h-5 w-5" />}
          title="No apps yet"
          body={
            manage
              ? "Create an app for each website, agent or script that needs keys. Each one gets a cog_ token for proxy or lease calls."
              : "An admin has not created any apps yet."
          }
          action={
            manage ? (
              <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setFormOpen(true)}>
                Create your first app
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="space-y-2.5">
          {sorted.map((a) => (
            <li key={a.id} className="rounded-card bg-white p-4 shadow-card sm:px-5">
              <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-[15px] font-medium text-ink">{a.name}</span>
                    <Badge tone={a.mode === "lease" ? "blue" : a.mode === "proxy" ? "violet" : "neutral"}>{a.mode}</Badge>
                    {!a.active && <Badge>paused</Badge>}
                  </div>
                  {a.description && <div className="mt-0.5 text-[13px] text-muted">{a.description}</div>}
                  <div className="mt-1.5 font-mono text-xs text-muted">
                    {a.token_prefix}... · {a.allowed_services?.length ? a.allowed_services.map((s) => nameBySlug[s] ?? s).join(", ") : "all services"} ·{" "}
                    <span title={dateTime(a.last_used_at)}>used {relTime(a.last_used_at, now)}</span>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Toggle checked={a.active} onChange={(v) => void toggleActive(a, v)} disabled={!manage} label={undefined} />
                  {manage && (
                    <>
                      <span className="mx-1 h-5 w-px bg-line" />
                      <IconButton label="Rotate token" onClick={() => void doRotate(a)} disabled={busyId === a.id}>
                        <RefreshCw className="h-4 w-4" />
                      </IconButton>
                      <IconButton
                        label="Edit app"
                        onClick={() => {
                          setEditing(a);
                          setFormOpen(true);
                        }}
                      >
                        <Pencil className="h-4 w-4" />
                      </IconButton>
                      <ConfirmButton
                        iconOnly
                        label="Delete app"
                        confirmLabel="Delete"
                        onConfirm={async () => {
                          try {
                            await remove(a.id);
                            toast.success(`Deleted ${a.name}`);
                          } catch (e) {
                            toast.error(errorMessage(e));
                          }
                        }}
                      />
                    </>
                  )}
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}

      <AppFormModal
        open={formOpen}
        initial={editing}
        onClose={() => setFormOpen(false)}
        onCreated={(r) => setToken({ result: r, rotated: false })}
      />
      <TokenModal result={token?.result ?? null} rotated={token?.rotated ?? false} onClose={() => setToken(null)} />
    </div>
  );
}
