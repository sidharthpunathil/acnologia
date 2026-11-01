import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Focusable } from "react-aria-components";
import { RiAddLine, RiPencilLine, RiRadarLine } from "@remixicon/react";
import { AcnEgress } from "@/components/foundations/icons/acn";
import { useTitle } from "../hooks/useTitle";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { dateTime, plural, relTime } from "../lib/format";
import { ops, type EgressInput, type EgressProxy } from "../lib/ops";
import { useNow } from "../hooks/useNow";
import { Modal } from "../components/Modal";
import { ConfirmButton } from "../components/ConfirmButton";
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Input } from "@/components/base/input/input";
import { Textarea } from "@/components/base/textarea/textarea";
import { Chip } from "@/components/base/badges/chip";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout, EmptyState, PageHeader, PageSkeleton } from "@/components/acn";
import { UnpluggedArt } from "@/components/foundations/illustrations";

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
          <Button variant="primary" type="submit" form="egress-form" disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
            {initial ? "Save" : "Add proxy"}
          </Button>
        </>
      }
    >
      <form id="egress-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Input
          label="Name"
          isRequired
          validationBehavior="aria"
          isInvalid={!!nameErr}
          errorMessage={nameErr}
          value={form.name}
          onChange={(name) => {
            setForm({ ...form, name });
            if (nameErr) setNameErr(null);
          }}
          placeholder="eu residential"
          autoComplete="off"
        />
        <Input
          label="URL"
          isRequired={!initial}
          validationBehavior="aria"
          isInvalid={!!urlErr}
          errorMessage={urlErr}
          hint={
            initial
              ? `Currently ${initial.url_hint}. Leave blank to keep the stored URL and password.`
              : "http://, https://, socks5:// or socks5h://. Include user:password@ if the proxy needs it."
          }
          value={form.url}
          onChange={(url) => {
            setForm({ ...form, url });
            if (urlErr) setUrlErr(null);
          }}
          placeholder={initial ? "Leave blank to keep it" : "socks5://user:pass@proxy.example.com:1080"}
          autoComplete="off"
          spellCheck="false"
          inputDir="ltr"
          fieldClassName="font-mono"
        />
        <Input
          label="Region"
          hint="Free text, for your reference."
          value={form.region}
          onChange={(region) => setForm({ ...form, region })}
          placeholder="eu-west"
          autoComplete="off"
        />
        <Textarea label="Notes" rows={2} value={form.notes} onChange={(notes) => setForm({ ...form, notes })} />
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
            <Button variant="primary" leadingIcon={RiAddLine} onClick={openCreate}>
              Add proxy
            </Button>
          )
        }
      />
      <Callout tone="neutral" className="mb-6">
        Assign a proxy in a service's settings or on a single key. A key's proxy wins over its service's. Anything without one connects directly, and deleting a proxy sends the services and keys using it direct again.
      </Callout>

      {error && proxies.length === 0 ? (
        <EmptyState title="Egress proxies are not available" body={error} />
      ) : proxies.length === 0 ? (
        <EmptyState
          art={UnpluggedArt}
          icon={AcnEgress}
          title="No egress proxies yet"
          body={manage ? "Add a proxy to send upstream calls from another address or region." : "Ask an admin to add one."}
          action={
            manage ? (
              <Button variant="primary" leadingIcon={RiAddLine} onClick={openCreate}>
                Add your first proxy
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="flex flex-col gap-3">
          {proxies.map((p) => {
            const used = p.services + p.keys;
            return (
              <li key={p.id} className="rounded-3xl border border-border-button-default bg-background-primary-default p-5">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="min-w-0 wrap-anywhere text-headline-medium text-text-primary">{p.name}</span>
                      {p.region && (
                        <Chip variant="caption" color="blue" className="font-mono">
                          {p.region}
                        </Chip>
                      )}
                      {used === 0 && (
                        <Chip variant="caption" color="neutral">
                          unused
                        </Chip>
                      )}
                    </div>
                    <div className="mt-1 wrap-anywhere font-mono text-caption-1-regular text-text-secondary" dir="ltr">
                      {p.url_hint}
                    </div>
                    {p.notes && <div className="mt-1 wrap-anywhere text-body-2-regular text-text-secondary">{p.notes}</div>}
                    <div className="mt-2 wrap-anywhere font-mono text-caption-1-regular text-text-secondary">
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
                    {p.last_error && <div className="mt-1 wrap-anywhere text-caption-1-regular text-text-error-primary">{p.last_error}</div>}
                  </div>
                  {manage && (
                    <div className="flex shrink-0 flex-wrap items-center gap-1 border-t border-separator-border pt-3 sm:border-0 sm:pt-0">
                      <Button
                        size="small"
                        variant="ghost"
                        leadingIcon={testing === p.id ? ButtonSpinner : RiRadarLine}
                        disabled={testing === p.id}
                        aria-busy={testing === p.id || undefined}
                        onClick={() => void test(p)}
                        className="max-sm:h-11"
                      >
                        Test
                      </Button>
                      <TooltipTrigger delay={400}>
                        <Focusable>
                          <IconButton
                            icon={RiPencilLine}
                            aria-label={`Edit ${p.name}`}
                            className="max-sm:size-11"
                            onClick={() => {
                              setEditing(p);
                              setFormOpen(true);
                            }}
                          />
                        </Focusable>
                        <Tooltip>Edit proxy</Tooltip>
                      </TooltipTrigger>
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
