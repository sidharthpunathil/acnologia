import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentType, type FormEvent } from "react";
import { Focusable } from "react-aria-components";
import {
  RiAddLine,
  RiArrowDownLine,
  RiArrowRightSLine,
  RiArrowUpLine,
  RiCloseLine,
  RiPencilLine,
} from "@remixicon/react";
import { AcnServiceGroup } from "@/components/foundations/icons/acn";
import { useTitle } from "../hooks/useTitle";
import { useServices } from "../stores/services";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { plural, slugify } from "../lib/format";
import { ops, STRATEGIES, type RoutingStrategy, type ServiceGroup } from "../lib/ops";
import { Select, SelectItem } from "@/components/base/select/select";
import { Modal } from "../components/Modal";
import { CopyButton } from "../components/CopyButton";
import { ConfirmButton } from "../components/ConfirmButton";
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Input } from "@/components/base/input/input";
import { Textarea } from "@/components/base/textarea/textarea";
import { ComboBox, ComboBoxItem } from "@/components/base/combobox/combobox";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { hintTextClassName } from "@/components/base/input/hint-text";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout, EmptyState, PageHeader, PageSkeleton } from "@/components/acn";
import { cx } from "@/utils/cx";
import { FallbackArt } from "@/components/foundations/illustrations";

const MIN_MEMBERS = 2;
const MAX_MEMBERS = 20;
const SLUG_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

type IconComponent = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;

/** Icon only button with a hover and focus tooltip naming it. */
function TipButton({
  icon,
  label,
  onClick,
  disabled,
  className,
  ...rest
}: {
  icon: IconComponent;
  label: string;
  onClick: () => void;
  disabled?: boolean;
  className?: string;
  "data-move"?: boolean;
}) {
  const button = (
    <IconButton
      {...rest}
      icon={icon}
      size="small"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      className={cx("max-sm:size-11", className)}
    />
  );
  // A disabled button cannot take focus, so it gets no tooltip.
  if (disabled) return button;
  return (
    <TooltipTrigger delay={400}>
      <Focusable>{button}</Focusable>
      <Tooltip>{label}</Tooltip>
    </TooltipTrigger>
  );
}

interface FormState {
  slug: string;
  name: string;
  description: string;
  members: string[];
  strategy: RoutingStrategy;
}

const blank: FormState = { slug: "", name: "", description: "", members: [], strategy: "priority" };

/** Groups take every strategy but weighted: their members carry no weights. */
const GROUP_STRATEGIES = (Object.keys(STRATEGIES) as RoutingStrategy[]).filter((k) => k !== "weighted");

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
  const [query, setQuery] = useState("");
  // The add field remounts after each pick so its list closes, then takes focus back.
  const addBox = useRef<HTMLDivElement>(null);
  const refocusAdd = useRef(false);
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
    if (refocusAdd.current) {
      refocusAdd.current = false;
      addBox.current?.querySelector("input")?.focus();
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
  const q = query.trim().toLowerCase();
  const matches = q ? remaining.filter((s) => s.name.toLowerCase().includes(q) || s.slug.includes(q)) : remaining;
  return (
    <div className="flex flex-col gap-1.5">
      <div id="group-members-label" className="flex items-center gap-0.5 text-body-medium text-text-primary">
        Members
        <span aria-hidden="true" className="text-text-error-primary">
          *
        </span>
      </div>
      {value.length > 0 ? (
        <ol aria-labelledby="group-members-label" className="divide-y divide-separator-border rounded-2xl border border-border-button-default bg-background-primary-default">
          {value.map((slug, i) => (
            <li
              key={slug}
              ref={(el) => {
                if (el) rows.current.set(slug, el);
                else rows.current.delete(slug);
              }}
              className={cx(
                "relative flex items-center gap-2 bg-background-primary-default py-1.5 ps-3 pe-1.5 first:rounded-t-2xl last:rounded-b-2xl",
                fresh(slug) && "fade-up",
              )}
            >
              <span className="w-5 shrink-0 font-mono text-caption-1-regular text-text-tertiary">{i + 1}</span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-body-2-medium text-text-primary">{nameBySlug[slug] ?? slug}</span>
                <span className="block truncate font-mono text-caption-1-regular text-text-tertiary">{slug}</span>
              </span>
              <TipButton data-move icon={RiArrowUpLine} label={`Move ${slug} up`} disabled={i === 0} onClick={() => move(i, -1)} />
              <TipButton data-move icon={RiArrowDownLine} label={`Move ${slug} down`} disabled={i === value.length - 1} onClick={() => move(i, 1)} />
              <TipButton icon={RiCloseLine} label={`Remove ${slug}`} onClick={() => removeAt(slug)} className="hover:text-text-error-primary" />
            </li>
          ))}
        </ol>
      ) : (
        <div className="rounded-2xl border border-dashed border-border-button-hover px-3 py-3 text-caption-1-regular text-text-secondary">
          No members yet. Add services in the order to try them.
        </div>
      )}
      <div ref={addBox} key={value.length}>
        <ComboBox
          aria-label="Add a service"
          aria-describedby="group-members-hint"
          placeholder={full ? `At most ${MAX_MEMBERS} members` : remaining.length ? "Add a service..." : "No more services to add"}
          isDisabled={full || remaining.length === 0}
          items={matches}
          inputValue={query}
          onInputChange={setQuery}
          selectedKey={null}
          emptyMessage="No matching service"
          onSelectionChange={(key) => {
            if (key == null) return;
            snapshot();
            refocusAdd.current = true;
            setQuery("");
            onChange([...value, String(key)]);
          }}
        >
          {(s: { slug: string; name: string }) => (
            <ComboBoxItem id={s.slug} textValue={`${s.name} ${s.slug}`}>
              <span className="truncate">{s.name}</span>
              <span className="truncate font-mono text-caption-1-regular text-text-tertiary">{s.slug}</span>
            </ComboBoxItem>
          )}
        </ComboBox>
      </div>
      {error ? (
        <span id="group-members-hint" role="alert" className={hintTextClassName(true)}>
          {error}
        </span>
      ) : (
        <span id="group-members-hint" className={hintTextClassName(false)}>
          The order for Priority, and the fallback order. {MIN_MEMBERS} to {MAX_MEMBERS} services that accept the same request shape.
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
    setForm(
      initial
        ? { slug: initial.slug, name: initial.name, description: initial.description, members: initial.members ?? [], strategy: initial.strategy ?? "priority" }
        : blank,
    );
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
        await ops.updateGroup(initial.id, { name, description: form.description.trim(), members: form.members, strategy: form.strategy });
        toast.success(`Saved ${name}`);
      } else {
        await ops.createGroup({ slug, name, description: form.description.trim(), members: form.members, strategy: form.strategy });
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
      description={initial ? `/proxy/${initial.slug}/` : "One proxy path that spreads requests across services with the same API and falls back between them."}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="group-form" disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
            {initial ? "Save" : "Create group"}
          </Button>
        </>
      }
    >
      <form id="group-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Input
          label="Name"
          isRequired
          validationBehavior="aria"
          isInvalid={!!errs.name}
          errorMessage={errs.name}
          value={form.name}
          onChange={(name) => {
            setForm({ ...form, name, slug: !initial && !slugTouched ? slugify(name) : form.slug });
            if (errs.name) setErrs({ ...errs, name: undefined });
          }}
          placeholder="chat with fallback"
          autoComplete="off"
        />
        {!initial && (
          <Input
            label="Slug"
            isRequired
            validationBehavior="aria"
            isInvalid={!!errs.slug}
            errorMessage={errs.slug}
            hint="Lowercase letters, digits and hyphens. Shares the namespace with service slugs and cannot be changed later."
            value={form.slug}
            onChange={(v) => {
              setSlugTouched(true);
              setForm({ ...form, slug: v.toLowerCase() });
              if (errs.slug) setErrs({ ...errs, slug: undefined });
            }}
            placeholder="chat"
            autoComplete="off"
            spellCheck="false"
            inputDir="ltr"
            fieldClassName="font-mono"
          />
        )}
        <Textarea label="Description" rows={2} value={form.description} onChange={(description) => setForm({ ...form, description })} />
        <Select
          label="Strategy"
          selectedKey={form.strategy}
          onSelectionChange={(k) => setForm({ ...form, strategy: k as RoutingStrategy })}
          hint={STRATEGIES[form.strategy].hint}
        >
          {GROUP_STRATEGIES.map((k) => (
            <SelectItem key={k} id={k} textValue={STRATEGIES[k].label}>
              {STRATEGIES[k].label}
            </SelectItem>
          ))}
        </Select>
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
            <Button variant="primary" leadingIcon={RiAddLine} onClick={openCreate}>
              Create group
            </Button>
          )
        }
      />
      <Callout tone="neutral" className="mb-6">
        A group puts services with the same API behind one path, such as several accounts of one provider. Its strategy picks which member goes first, and
        a member with no working key, or one that does not answer, hands the same request to the next. Client errors (400, 422) come back as they are. For
        providers with different APIs, use a capability.
      </Callout>

      {error && groups.length === 0 ? (
        <EmptyState title="Service groups are not available" body={error} />
      ) : groups.length === 0 ? (
        <EmptyState
          art={FallbackArt}
          icon={AcnServiceGroup}
          title="No service groups yet"
          body={manage ? "Group services that take the same requests, so a call falls back to the next one when a provider runs dry." : "Ask an admin to create one."}
          action={
            manage ? (
              <Button variant="primary" leadingIcon={RiAddLine} onClick={openCreate}>
                Create your first group
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="flex flex-col gap-3">
          {groups.map((g) => (
            <li key={g.id} className="rounded-3xl border border-border-button-default bg-background-primary-default p-5">
              {/* Actions stay top right at every width, so phones do not get a lone row of icons. */}
              <div className="flex items-start gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                    <span className="min-w-0 wrap-anywhere text-headline-medium text-text-primary">{g.name}</span>
                    <span className="wrap-anywhere font-mono text-caption-1-regular text-text-secondary">{g.slug}</span>
                    <span className="rounded-md bg-background-tertiary-default px-1.5 py-0.5 text-caption-1-medium text-text-secondary">
                      {STRATEGIES[g.strategy ?? "priority"].label}
                    </span>
                  </div>
                  {g.description && <div className="mt-0.5 wrap-anywhere text-body-2-regular text-text-secondary">{g.description}</div>}
                  <ol aria-label="Members, in order" className="mt-3 flex flex-wrap items-center gap-1 font-mono text-caption-1-regular">
                    {g.members.map((m, i) => {
                      const missing = servicesLoaded && !known.has(m);
                      return (
                        <li key={m} className="inline-flex items-center gap-1">
                          {i > 0 && <RiArrowRightSLine className="size-3.5 text-foreground-icon-secondary" aria-label="then" />}
                          <span
                            className={cx(
                              "rounded-md px-1.5 py-0.5",
                              missing ? "bg-status-rose-background text-status-rose-text" : "bg-background-tertiary-default text-text-primary",
                            )}
                            title={missing ? "No service with this slug" : undefined}
                          >
                            {m}
                            {missing && <span className="sr-only"> (no service with this slug)</span>}
                          </span>
                        </li>
                      );
                    })}
                  </ol>
                  <div className="mt-3 flex min-w-0 max-w-md items-center gap-1 rounded-xl border border-border-button-default bg-background-secondary-default py-0.5 ps-2.5 pe-0.5">
                    <code className="min-w-0 flex-1 truncate font-mono text-caption-1-regular text-text-secondary" dir="ltr">
                      /proxy/{g.slug}/...
                    </code>
                    <CopyButton text={`${origin}/proxy/${g.slug}/`} label="Copy URL" />
                  </div>
                </div>
                {manage && (
                  <div className="-me-1.5 -mt-1 flex shrink-0 items-center gap-1">
                    <TooltipTrigger delay={400}>
                      <Focusable>
                        <IconButton
                          icon={RiPencilLine}
                          aria-label={`Edit ${g.name}`}
                          className="max-sm:size-11"
                          onClick={() => {
                            setEditing(g);
                            setFormOpen(true);
                          }}
                        />
                      </Focusable>
                      <Tooltip>Edit group</Tooltip>
                    </TooltipTrigger>
                    <ConfirmButton
                      iconOnly
                      variant="outline"
                      size="medium"
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
