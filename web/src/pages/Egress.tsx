import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTitle } from "../hooks/useTitle";
import { Globe, Info, Pencil, Plus, Radar } from "lucide-react";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { dateTime, plural, relTime } from "../lib/format";
import { ops, type EgressInput, type EgressProxy } from "../lib/ops";
import { useNow } from "../hooks/useNow";
import { Modal } from "../components/Modal";
import { ConfirmButton } from "../components/ConfirmButton";
import { Badge, Button, Callout, Empty, Field, IconButton, Input, PageHeader, PageSkeleton, Textarea } from "../components/ui";

const SCHEMES = ["http://", "https://", "socks5://", "socks5h://"];

interface FormState {
  name: string;
  url: string;
  region: string;
  notes: string;
}

const blank: FormState = { name: "", url: "", region: "", notes: "" };

function EgressFormModal({
  open,
  initial,
  onClose,
  onSaved,
}: {
  open: boolean;
  initial: EgressProxy | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState<FormState>(blank);
  const [busy, setBusy] = useState(false);
  const [nameErr, setNameErr] = useState<string | null>(null);
  const [urlErr, setUrlErr] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setNameErr(null);
    setUrlErr(null);
    setForm(initial ? { name: initial.name, url: "", region: initial.region, notes: initial.notes } : blank);
  }, [open, initial]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const name = form.name.trim();
    const url = form.url.trim();
    let bad = false;
    if (!name) {
      setNameErr("Give the proxy a name, such as eu residential.");
      bad = true;
    }
    if (!initial && !url) {
      setUrlErr("Enter the proxy URL.");
      bad = true;
    } else if (url && !SCHEMES.some((s) => url.toLowerCase().startsWith(s))) {
      setUrlErr("Start with http://, https://, socks5:// or socks5h://.");
      bad = true;
    }
    if (bad) return;
    setBusy(true);
    try {
      const body: EgressInput = { name, region: form.region.trim(), notes: form.notes.trim() };
      if (url) body.url = url;
      if (initial) {
        await ops.updateEgress(initial.id, body);
        toast.success(`Saved ${name}`);
      } else {
        await ops.createEgress(body);
        toast.success(`Added ${name}. Run a test to check it works.`);
      }
      onClose();
      onSaved();
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
      title={initial ? `Edit ${initial.name}` : "Add egress proxy"}
      description={initial ? undefined : "Upstream calls through this proxy leave from its address instead of the server's."}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="egress-form" loading={busy}>
            {initial ? "Save" : "Add proxy"}
          </Button>
        </>
      }
    >
      <form id="egress-form" onSubmit={submit} className="space-y-4" noValidate>
        <Field label="Name" required error={nameErr}>
          <Input
            value={form.name}
            onChange={(e) => {
              setForm({ ...form, name: e.target.value });
              if (nameErr) setNameErr(null);
            }}
            placeholder="eu residential"
            autoComplete="off"
          />
        </Field>
        <Field
          label="URL"
          required={!initial}
          error={urlErr}
          hint={
            initial
              ? `Currently ${initial.url_hint}. Leave blank to keep the stored URL and password.`
              : "http://, https://, socks5:// or socks5h://. Include user:password@ if the proxy needs it."
          }
        >
          <Input
            value={form.url}
            onChange={(e) => {
              setForm({ ...form, url: e.target.value });
              if (urlErr) setUrlErr(null);
            }}
            placeholder={initial ? "Leave blank to keep it" : "socks5://user:pass@proxy.example.com:1080"}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
          />
        </Field>
        <Field label="Region" hint="Free text, for your reference.">
          <Input value={form.region} onChange={(e) => setForm({ ...form, region: e.target.value })} placeholder="eu-west" autoComplete="off" />
        </Field>
        <Field label="Notes">
          <Textarea rows={2} value={form.notes} onChange={(e) => setForm({ ...form, notes: e.target.value })} />
        </Field>
      </form>
    </Modal>
  );
}

export function EgressPage() {
  useTitle("Egress proxies");
  const manage = can.manage(useAuth((s) => s.user?.role));
  const now = useNow(30000);
  const [proxies, setProxies] = useState<EgressProxy[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<EgressProxy | null>(null);
  const [testing, setTesting] = useState<number | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await ops.listEgress();
      setProxies([...(res.proxies ?? [])].sort((a, b) => a.name.localeCompare(b.name)));
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const test = async (p: EgressProxy) => {
    setTesting(p.id);
    try {
      const res = await ops.testEgress(p.id);
      if (res.ok) toast.success(`${p.name} works. Public IP ${res.ip}`);
      else toast.error(`${p.name} failed: ${res.error || "no response"}`);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setTesting(null);
      void load();
    }
  };

  const openCreate = () => {
    setEditing(null);
    setFormOpen(true);
  };

  if (!loaded) return <PageSkeleton variant="list" />;

  return (
    <div>
      <PageHeader
        title="Egress proxies"
        meta={plural(proxies.length, "proxy", "proxies")}
        actions={
          manage && (
            <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={openCreate}>
              Add proxy
            </Button>
          )
        }
      />
      <Callout icon={<Info className="h-4 w-4" />} className="mb-6">
        Assign a proxy in a service's settings or on a single key. A key's proxy wins over its service's. Anything without one connects directly, and deleting a proxy sends the services and keys using it direct again.
      </Callout>

      {error && proxies.length === 0 ? (
        <Empty title="Egress proxies are not available" body={error} />
      ) : proxies.length === 0 ? (
        <Empty
          icon={<Globe className="h-5 w-5" />}
          title="No egress proxies yet"
          body={manage ? "Add a proxy to send upstream calls from another address or region." : "Ask an admin to add one."}
          action={
            manage ? (
              <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={openCreate}>
                Add your first proxy
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="space-y-2.5">
          {proxies.map((p) => {
            const used = p.services + p.keys;
            return (
              <li key={p.id} className="rounded-card bg-surface p-4 shadow-card sm:px-5">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="wrap-anywhere min-w-0 text-[15px] font-medium text-ink">{p.name}</span>
                      {p.region && <Badge tone="blue">{p.region}</Badge>}
                      {used === 0 && <Badge>unused</Badge>}
                    </div>
                    <div className="wrap-anywhere mt-1 font-mono text-xs text-muted">{p.url_hint}</div>
                    {p.notes && <div className="wrap-anywhere mt-1 text-[13px] text-muted">{p.notes}</div>}
                    <div className="wrap-anywhere mt-1.5 font-mono text-xs text-muted">
                      {plural(p.services, "service")} · {plural(p.keys, "key")} ·{" "}
                      {p.last_checked_at ? (
                        <span title={dateTime(p.last_checked_at)}>
                          tested {relTime(p.last_checked_at, now)}
                          {p.last_error ? "" : p.last_ip ? `, IP ${p.last_ip}` : ""}
                        </span>
                      ) : (
                        "never tested"
                      )}
                    </div>
                    {p.last_error && <div className="wrap-anywhere mt-1 text-xs text-red-700">{p.last_error}</div>}
                  </div>
                  {manage && (
                    <div className="-mx-1 flex shrink-0 flex-wrap items-center gap-1 border-t border-line pt-2 sm:mx-0 sm:border-0 sm:pt-0">
                      <Button size="sm" variant="ghost" icon={<Radar className="h-3.5 w-3.5" />} loading={testing === p.id} onClick={() => void test(p)}>
                        Test
                      </Button>
                      <IconButton
                        label="Edit proxy"
                        onClick={() => {
                          setEditing(p);
                          setFormOpen(true);
                        }}
                      >
                        <Pencil className="h-4 w-4" />
                      </IconButton>
                      <ConfirmButton
                        iconOnly
                        label="Delete proxy"
                        confirmLabel={used ? "Delete, go direct" : "Delete"}
                        onConfirm={async () => {
                          try {
                            await ops.deleteEgress(p.id);
                            setProxies((list) => list.filter((x) => x.id !== p.id));
                            toast.success(used ? `Deleted ${p.name}. Services and keys that used it now connect directly.` : `Deleted ${p.name}`);
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
          })}
        </ul>
      )}

      <EgressFormModal open={formOpen} initial={editing} onClose={() => setFormOpen(false)} onSaved={() => void load()} />
    </div>
  );
}
