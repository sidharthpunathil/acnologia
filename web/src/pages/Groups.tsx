import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useTitle } from "../hooks/useTitle";
import { ArrowDown, ArrowUp, ChevronRight, Info, Pencil, Plus, Waypoints, X } from "lucide-react";
import { useServices } from "../stores/services";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { plural, slugify } from "../lib/format";
import { ops, type ServiceGroup } from "../lib/ops";
import { Modal } from "../components/Modal";
import { CopyButton } from "../components/CopyButton";
import { ConfirmButton } from "../components/ConfirmButton";
import { Button, Callout, Empty, Field, IconButton, Input, PageHeader, PageSkeleton, Select, Textarea, cx } from "../components/ui";

const MIN_MEMBERS = 2;
const MAX_MEMBERS = 20;
const SLUG_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

interface FormState {
  slug: string;
  name: string;
  description: string;
  members: string[];
}

const blank: FormState = { slug: "", name: "", description: "", members: [] };

function MemberPicker({
  value,
  onChange,
  options,
  error,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  options: { slug: string; name: string }[];
  error: string | null;
}) {
  const nameBySlug = Object.fromEntries(options.map((s) => [s.slug, s.name]));
  const remaining = options.filter((s) => !value.includes(s.slug));
  const rows = useRef(new Map<string, HTMLLIElement>());
  const tops = useRef(new Map<string, number>());
  // Members that were just added fade in; the ones already there do not.
  const known = useRef(new Set(value));
  // Moving a row re-inserts its DOM node, which drops focus. Put it back.
  const refocus = useRef<{ slug: string; dir: -1 | 1 } | null>(null);

  const snapshot = () => {
    tops.current = new Map([...rows.current].map(([slug, el]) => [slug, el.getBoundingClientRect().top]));
  };

  // FLIP: rows that changed place slide from where they were to where they are.
  useLayoutEffect(() => {
    const before = tops.current;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    for (const [slug, el] of rows.current) {
      const was = before.get(slug);
      if (was === undefined || reduce) continue;
      const dy = was - el.getBoundingClientRect().top;
      if (Math.abs(dy) < 1) continue;
      el.animate([{ transform: `translateY(${dy}px)` }, { transform: "translateY(0)" }], {
        duration: 220,
        easing: "cubic-bezier(0.77, 0, 0.175, 1)",
      });
    }
    tops.current = new Map();
    const f = refocus.current;
    refocus.current = null;
    if (f) {
      const row = rows.current.get(f.slug);
      const btns = row?.querySelectorAll<HTMLButtonElement>("button[data-move]");
      const want = btns?.[f.dir === -1 ? 0 : 1];
      const other = btns?.[f.dir === -1 ? 1 : 0];
      (want && !want.disabled ? want : other)?.focus();
    }
    known.current = new Set(value);
  }, [value]);

  const move = (i: number, d: -1 | 1) => {
    snapshot();
    refocus.current = { slug: value[i], dir: d };
    const next = value.slice();
    [next[i], next[i + d]] = [next[i + d], next[i]];
    onChange(next);
  };
  const removeAt = (slug: string) => {
    snapshot();
    onChange(value.filter((x) => x !== slug));
  };
  // Opening the editor fills the whole list at once; only animate a single added member.
  const fresh = (slug: string) => !known.current.has(slug) && (known.current.size > 0 || value.length === 1);
  const full = value.length >= MAX_MEMBERS;
  return (
    <div>
      <div className="mb-1.5 text-[13px] font-medium text-ink">
        Members<span className="ml-0.5 text-brand" aria-hidden="true">*</span>
      </div>
      {value.length > 0 ? (
        <ol className="divide-y divide-line rounded-xl border border-line bg-surface">
          {value.map((slug, i) => (
            <li
              key={slug}
              ref={(el) => {
                if (el) rows.current.set(slug, el);
                else rows.current.delete(slug);
              }}
              className={cx("relative flex items-center gap-2 bg-surface px-3 py-1.5 first:rounded-t-xl last:rounded-b-xl", fresh(slug) && "fade-up")}
            >
              <span className="w-5 shrink-0 font-mono text-xs text-faint">{i + 1}</span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[13px] text-ink">{nameBySlug[slug] ?? slug}</span>
                <span className="block truncate font-mono text-[11px] text-faint">{slug}</span>
              </span>
              <IconButton data-move label={`Move ${slug} up`} disabled={i === 0} onClick={() => move(i, -1)}>
                <ArrowUp className="h-4 w-4" />
              </IconButton>
              <IconButton data-move label={`Move ${slug} down`} disabled={i === value.length - 1} onClick={() => move(i, 1)}>
                <ArrowDown className="h-4 w-4" />
              </IconButton>
              <IconButton label={`Remove ${slug}`} onClick={() => removeAt(slug)} className="hover:text-red-700">
                <X className="h-4 w-4" />
              </IconButton>
            </li>
          ))}
        </ol>
      ) : (
        <div className="rounded-xl border border-dashed border-line px-3 py-3 text-xs text-muted">No members yet. Add services in the order to try them.</div>
      )}
      <Select
        aria-label="Add a service"
        className="mt-2"
        value=""
        disabled={full || remaining.length === 0}
        onChange={(e) => {
          if (!e.target.value) return;
          snapshot();
          onChange([...value, e.target.value]);
        }}
      >
        <option value="">{full ? `At most ${MAX_MEMBERS} members` : remaining.length ? "Add a service..." : "No more services to add"}</option>
        {remaining.map((s) => (
          <option key={s.slug} value={s.slug}>
            {s.name} ({s.slug})
          </option>
        ))}
      </Select>
      {error ? (
        <span role="alert" className="mt-1 block text-xs text-red-700">
          {error}
        </span>
      ) : (
        <span className="mt-1 block text-xs leading-relaxed text-muted">
          Tried top to bottom. {MIN_MEMBERS} to {MAX_MEMBERS} services that accept the same request shape.
        </span>
      )}
    </div>
  );
}

function GroupFormModal({
  open,
  initial,
  onClose,
  onSaved,
}: {
  open: boolean;
  initial: ServiceGroup | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const services = useServices((s) => s.services);
  const [form, setForm] = useState<FormState>(blank);
  const [slugTouched, setSlugTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [errs, setErrs] = useState<{ slug?: string; name?: string; members?: string }>({});

  useEffect(() => {
    if (!open) return;
    setErrs({});
    setSlugTouched(false);
    setForm(initial ? { slug: initial.slug, name: initial.name, description: initial.description, members: initial.members ?? [] } : blank);
  }, [open, initial]);

  const options = useMemo(() => {
    const list = [...services].sort((a, b) => a.name.localeCompare(b.name)).map((s) => ({ slug: s.slug, name: s.name }));
    // Keep members whose service was deleted visible so they can be removed.
    for (const m of form.members) if (!list.some((s) => s.slug === m)) list.push({ slug: m, name: m });
    return list;
  }, [services, form.members]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const name = form.name.trim();
    const slug = form.slug.trim();
    const next: typeof errs = {};
    if (!initial) {
      if (!SLUG_RE.test(slug)) next.slug = "Use lowercase letters, digits and single hyphens.";
      else if (services.some((s) => s.slug === slug)) next.slug = "A service already uses this slug.";
    }
    if (!name) next.name = "Give the group a name.";
    if (form.members.length < MIN_MEMBERS) next.members = `Add at least ${MIN_MEMBERS} services.`;
    else if (form.members.length > MAX_MEMBERS) next.members = `At most ${MAX_MEMBERS} services.`;
    setErrs(next);
    if (Object.keys(next).length) return;
    setBusy(true);
    try {
      if (initial) {
        await ops.updateGroup(initial.id, { name, description: form.description.trim(), members: form.members });
        toast.success(`Saved ${name}`);
      } else {
        await ops.createGroup({ slug, name, description: form.description.trim(), members: form.members });
        toast.success(`Created ${name}. Call it at /proxy/${slug}/`);
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
      wide
      title={initial ? `Edit ${initial.name}` : "Create service group"}
      description={initial ? `/proxy/${initial.slug}/` : "One proxy path that falls through a list of services in order."}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="group-form" loading={busy}>
            {initial ? "Save" : "Create group"}
          </Button>
        </>
      }
    >
      <form id="group-form" onSubmit={submit} className="space-y-4" noValidate>
        <Field label="Name" required error={errs.name}>
          <Input
            value={form.name}
            onChange={(e) => {
              const name = e.target.value;
              setForm({ ...form, name, slug: !initial && !slugTouched ? slugify(name) : form.slug });
              if (errs.name) setErrs({ ...errs, name: undefined });
            }}
            placeholder="chat with fallback"
            autoComplete="off"
          />
        </Field>
        {!initial && (
          <Field
            label="Slug"
            required
            error={errs.slug}
            hint="Lowercase letters, digits and hyphens. Shares the namespace with service slugs and cannot be changed later."
          >
            <Input
              value={form.slug}
              onChange={(e) => {
                setSlugTouched(true);
                setForm({ ...form, slug: e.target.value.toLowerCase() });
                if (errs.slug) setErrs({ ...errs, slug: undefined });
              }}
              placeholder="chat"
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
          </Field>
        )}
        <Field label="Description">
          <Textarea rows={2} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
        </Field>
        <MemberPicker
          value={form.members}
          options={options}
          error={errs.members ?? null}
          onChange={(members) => {
            setForm({ ...form, members });
            if (errs.members) setErrs({ ...errs, members: undefined });
          }}
        />
      </form>
    </Modal>
  );
}

export function GroupsPage() {
  useTitle("Service groups");
  const manage = can.manage(useAuth((s) => s.user?.role));
  const { services, loaded: servicesLoaded, fetch: fetchServices } = useServices();
  const [groups, setGroups] = useState<ServiceGroup[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<ServiceGroup | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await ops.listGroups();
      setGroups([...(res.groups ?? [])].sort((a, b) => a.slug.localeCompare(b.slug)));
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

  const known = useMemo(() => new Set(services.map((s) => s.slug)), [services]);
  const origin = window.location.origin;

  const openCreate = () => {
    setEditing(null);
    setFormOpen(true);
  };

  if (!loaded) return <PageSkeleton variant="list" />;

  return (
    <div>
      <PageHeader
        title="Service groups"
        meta={plural(groups.length, "group")}
        actions={
          manage && (
            <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={openCreate}>
              Create group
            </Button>
          )
        }
      />
      <Callout icon={<Info className="h-4 w-4" />} className="mb-6">
        Requests go to the first member and fall through to the next when it has no usable key, its circuit breaker is open, it cannot be reached, or every key it
        tried was rate limited, busy, exhausted, invalid or banned. A 400 or 422 is returned as is. Members should accept the same request shape.
      </Callout>

      {error && groups.length === 0 ? (
        <Empty title="Service groups are not available" body={error} />
      ) : groups.length === 0 ? (
        <Empty
          icon={<Waypoints className="h-5 w-5" />}
          title="No service groups yet"
          body={manage ? "Group services that take the same requests, so a call falls back to the next one when a provider runs dry." : "Ask an admin to create one."}
          action={
            manage ? (
              <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={openCreate}>
                Create your first group
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="space-y-2.5">
          {groups.map((g) => (
            <li key={g.id} className="rounded-card bg-surface p-4 shadow-card sm:px-5">
              {/* Actions stay top right at every width, so phones do not get a lone row of icons. */}
              <div className="flex items-start gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                    <span className="wrap-anywhere min-w-0 text-[15px] font-medium text-ink">{g.name}</span>
                    <span className="wrap-anywhere font-mono text-xs text-muted">{g.slug}</span>
                  </div>
                  {g.description && <div className="wrap-anywhere mt-0.5 text-[13px] text-muted">{g.description}</div>}
                  <div className="mt-2 flex flex-wrap items-center gap-1 font-mono text-xs">
                    {g.members.map((m, i) => (
                      <span key={m} className="inline-flex items-center gap-1">
                        {i > 0 && <ChevronRight className="h-3 w-3 text-faint" aria-label="then" />}
                        <span
                          className={servicesLoaded && !known.has(m) ? "rounded-md bg-red-50 px-1.5 py-0.5 text-red-700" : "rounded-md bg-well px-1.5 py-0.5 text-ink"}
                          title={servicesLoaded && !known.has(m) ? "No service with this slug" : undefined}
                        >
                          {m}
                        </span>
                      </span>
                    ))}
                  </div>
                  <div className="mt-3 flex min-w-0 max-w-md items-center gap-1 rounded-lg border border-line bg-canvas py-0.5 pl-2.5 pr-0.5">
                    <code className="min-w-0 flex-1 truncate font-mono text-xs text-muted">/proxy/{g.slug}/...</code>
                    <CopyButton text={`${origin}/proxy/${g.slug}/`} label="Copy URL" />
                  </div>
                </div>
                {manage && (
                  <div className="-mr-1.5 -mt-1 flex shrink-0 items-center gap-0.5">
                    <IconButton
                      label="Edit group"
                      onClick={() => {
                        setEditing(g);
                        setFormOpen(true);
                      }}
                    >
                      <Pencil className="h-4 w-4" />
                    </IconButton>
                    <ConfirmButton
                      iconOnly
                      label="Delete group"
                      confirmLabel="Delete"
                      onConfirm={async () => {
                        try {
                          await ops.deleteGroup(g.id);
                          setGroups((list) => list.filter((x) => x.id !== g.id));
                          toast.success(`Deleted ${g.name}`);
                        } catch (e) {
                          toast.error(errorMessage(e));
                        }
                      }}
                    />
                  </div>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}

      <GroupFormModal open={formOpen} initial={editing} onClose={() => setFormOpen(false)} onSaved={() => void load()} />
    </div>
  );
}
