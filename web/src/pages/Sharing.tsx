import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link } from "react-aria-components";
import {
  RiAddLine,
  RiLinkM,
  RiPauseLine,
  RiPencilLine,
  RiPlayLine,
  RiRefreshLine,
  RiShareForwardLine,
  RiTicketLine,
} from "@remixicon/react";
import { useTitle } from "../hooks/useTitle";
import { useNow } from "../hooks/useNow";
import { useAuth } from "../stores/auth";
import { useServices } from "../stores/services";
import { useProviders } from "../stores/providers";
import { toast } from "../stores/toast";
import { ApiError, errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { api } from "../lib/api";
import { compact, dateTime, plural, relTime } from "../lib/format";
import type { AppLimits } from "../lib/ops";
import {
  LEASES,
  lastDays,
  linkHandoverKeys,
  shareEndAction,
  shareEndsAt,
  shareLease,
  shareMode,
  sharing,
  type EndAction,
  type InviteValidity,
  type KeyScope,
  type Lease,
  type LinkStatus,
  type PeerLink,
  type PeerPreview,
  type ServerIdentity,
  type Share,
  type ShareInput,
  type ShareMode,
  type ShareStatus,
} from "../lib/sharing";
import { Modal } from "../components/Modal";
import { MultiSelect } from "../components/MultiSelect";
import { ConfirmButton } from "../components/ConfirmButton";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { Label } from "@/components/base/input/label";
import { HintText } from "@/components/base/input/hint-text";
import { Textarea } from "@/components/base/textarea/textarea";
import { Select, SelectItem } from "@/components/base/select/select";
import { Switch } from "@/components/base/switch/switch";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout, CopyButton, EmptyState, PageHeader, PageSkeleton, Section, SecretBox } from "@/components/acn";
import { UnpluggedArt } from "@/components/foundations/illustrations";
import { cx } from "@/utils/cx";
import { KeyScopes, visibleKeys, type KeyLists } from "./sharing/KeyScopes";
import { LimitMeters, limitSummary } from "./sharing/Limits";
import { END_ACTION_LABEL, Countdown, EndActionPicker, EndsPicker, ExtendButton, endsDraftOf, resolveEnds, type EndsDraft } from "./sharing/Timers";
import { HandoverKeysList, HandoverWarning, LeasePicker, ModeChip, ModePicker, PeerHandoverKeys, RevokeResultModal, wipeDeadline } from "./sharing/Modes";

// Only true for proxy shares; handover shares give the keys away, so say which.
const PROMISE = "In proxy mode, keys never leave the server that owns them.";

const SHARE_COLOR: Record<ShareStatus, ChipColor> = { pending: "yellow", active: "lime", paused: "neutral", revoked: "rose", expired: "gray" };
const LINK_COLOR: Record<LinkStatus, ChipColor> = { active: "lime", paused: "neutral", expired: "gray", revoked: "rose", disconnected: "gray" };

const LIMIT_KEYS = ["daily_requests", "monthly_requests", "daily_budget", "monthly_budget"] as const;
type LimitKey = (typeof LIMIT_KEYS)[number];
const LIMIT_LABEL: Record<LimitKey, string> = {
  daily_requests: "Daily requests",
  monthly_requests: "Monthly requests",
  daily_budget: "Daily budget (USD)",
  monthly_budget: "Monthly budget (USD)",
};
const NO_LIMITS: AppLimits = { daily_requests: null, monthly_requests: null, daily_budget: null, monthly_budget: null };

const VALIDITY: { id: InviteValidity; label: string }[] = [
  { id: "1h", label: "1 hour" },
  { id: "24h", label: "24 hours" },
  { id: "7d", label: "7 days" },
];

function Mono({ children, className }: { children: string; className?: string }) {
  return (
    <code dir="ltr" className={cx("wrap-anywhere font-mono text-caption-1-regular text-text-primary", className)}>
      {children}
    </code>
  );
}

// ---- This server ----

function ThisServer({ identity, isSuper, onSaved }: { identity: ServerIdentity; isSuper: boolean; onSaved: (i: ServerIdentity) => void }) {
  const [name, setName] = useState(identity.name);
  const [url, setUrl] = useState(identity.url_source === "saved" ? identity.url : "");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setName(identity.name);
    setUrl(identity.url_source === "saved" ? identity.url : "");
  }, [identity]);

  const dirty = name.trim() !== identity.name || (identity.url_source !== "env" && url.trim() !== (identity.url_source === "saved" ? identity.url : ""));

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const body: { name?: string; url?: string } = { name: name.trim() };
      if (identity.url_source !== "env") body.url = url.trim();
      onSaved(await sharing.updateIdentity(body));
      toast.success("Saved this server");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const urlHint =
    identity.url_source === "env"
      ? "Set by ACNOLOGIA_PUBLIC_URL."
      : identity.url_source === "saved"
        ? "Saved here. Other servers reach this one at this URL."
        : "Guessed from this page's address. Save the URL other servers should use.";

  return (
    <Section title="This server" description="Other servers see this identity when you share with them or connect to them.">
      <dl className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="min-w-0">
          <dt className="text-caption-1-regular text-text-secondary">Server id</dt>
          <dd className="mt-1 flex items-center gap-1">
            <Mono className="text-body-2-regular">{identity.id}</Mono>
            <CopyButton text={identity.id} label="Copy server id" iconOnly size="xs" />
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-caption-1-regular text-text-secondary">Key fingerprint</dt>
          <dd className="mt-1">
            <Mono className="text-body-2-regular">{identity.fingerprint}</Mono>
          </dd>
        </div>
      </dl>
      {isSuper ? (
        <form onSubmit={save} className="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Input label="Name" value={name} onChange={setName} maxLength={80} autoComplete="off" />
          <Input
            label="Public URL"
            type="url"
            value={identity.url_source === "env" ? identity.url : url}
            onChange={setUrl}
            isDisabled={identity.url_source === "env"}
            placeholder={identity.url}
            hint={urlHint}
            fieldClassName="font-mono"
            autoComplete="off"
          />
          <div className="sm:col-span-2">
            <Button type="submit" variant="secondary" disabled={!dirty || busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
              Save
            </Button>
          </div>
        </form>
      ) : (
        <dl className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <dt className="text-caption-1-regular text-text-secondary">Name</dt>
            <dd className="mt-1 text-body-2-regular text-text-primary">{identity.name}</dd>
          </div>
          <div className="min-w-0">
            <dt className="text-caption-1-regular text-text-secondary">Public URL</dt>
            <dd className="mt-1">
              <Mono className="text-body-2-regular">{identity.url}</Mono>
            </dd>
          </div>
        </dl>
      )}
    </Section>
  );
}

// ---- Shared by you ----

function UsageBars({ share }: { share: Share }) {
  const days = lastDays(share.usage?.history);
  const top = Math.max(1, ...days.map((d) => d.requests));
  const total = days.reduce((n, d) => n + d.requests, 0);
  return (
    <div className="flex h-8 items-end gap-0.5" role="img" aria-label={`${plural(total, "request")} in the last 14 days`}>
      {days.map((d) => (
        <span
          key={d.day}
          title={`${d.day}: ${plural(d.requests, "request")}`}
          className={cx("w-1.5 rounded-xs", d.requests ? "bg-chart-2" : "bg-background-tertiary-default")}
          style={{ height: `${Math.max(12, (d.requests / top) * 100)}%` }}
        />
      ))}
    </div>
  );
}

interface ShareDraft {
  name: string;
  note: string;
  services: string[];
  providers: string[];
  include_hidden: boolean;
  key_scopes: Record<string, KeyScope>;
  limits: Record<LimitKey, string>;
  invite_valid: InviteValidity;
  mode: ShareMode;
  lease: Lease;
  ends: EndsDraft;
  end_action: EndAction;
}

function draftOf(s: Share | null): ShareDraft {
  const limits = { daily_requests: "", monthly_requests: "", daily_budget: "", monthly_budget: "" };
  for (const k of LIMIT_KEYS) {
    const v = s?.limits?.[k];
    limits[k] = v === null || v === undefined ? "" : String(v);
  }
  return {
    name: s?.name ?? "",
    note: s?.note ?? "",
    services: s?.services ?? [],
    providers: s?.providers ?? [],
    include_hidden: s?.include_hidden ?? false,
    key_scopes: { ...(s?.key_scopes ?? {}) },
    limits,
    invite_valid: "24h",
    mode: s ? shareMode(s) : "proxy",
    lease: s ? shareLease(s) : "24h",
    ends: endsDraftOf(s ? shareEndsAt(s) : null),
    end_action: s ? shareEndAction(s) : "expire",
  };
}

function parseDraftLimits(d: ShareDraft): AppLimits | string {
  const out: AppLimits = { ...NO_LIMITS };
  for (const k of LIMIT_KEYS) {
    const t = d.limits[k].trim();
    if (!t) continue;
    const n = Number(t);
    const whole = k.endsWith("_requests");
    if (!Number.isFinite(n) || n < 0 || (whole && !Number.isInteger(n))) return `${LIMIT_LABEL[k]} must be ${whole ? "a whole number" : "an amount"}, 0 or more.`;
    out[k] = n;
  }
  return out;
}

function ShareFormModal({
  open,
  initial,
  isSuper,
  onClose,
  onSaved,
}: {
  open: boolean;
  initial: Share | null;
  isSuper: boolean;
  onClose: () => void;
  onSaved: (r: { share: Share; invite?: string }) => void;
}) {
  const services = useServices((s) => s.services);
  const providers = useProviders((s) => s.providers);
  const [d, setD] = useState<ShareDraft>(draftOf(null));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [keys, setKeys] = useState<KeyLists>({});
  const requested = useRef(new Set<number>());
  const now = useNow(30000);

  useEffect(() => {
    if (open) {
      setD(draftOf(initial));
      setErr(null);
      setKeys({});
      requested.current = new Set();
    }
  }, [open, initial]);

  // Load the keys of each selected service once, for the key scope rows.
  useEffect(() => {
    if (!open) return;
    for (const slug of d.services) {
      const sv = services.find((s) => s.slug === slug);
      if (!sv || requested.current.has(sv.id)) continue;
      requested.current.add(sv.id);
      api.listKeys(sv.id).then(
        (r) => setKeys((k) => ({ ...k, [sv.id]: r.keys ?? [] })),
        (e) => setKeys((k) => ({ ...k, [sv.id]: errorMessage(e) })),
      );
    }
  }, [open, d.services, services]);

  // Mode is chosen at creation and fixed once the invite is redeemed.
  const modeLocked = !!initial && (!!initial.redeemed_at || initial.status !== "pending");

  // A service that came from another server cannot be shared onward.
  const serviceOptions = services
    .filter((s) => s.provider !== "acnologia-peer" && !s.peer_link_id)
    .map((s) => ({ value: s.slug, label: s.name, meta: s.slug }));
  const providerOptions = providers.filter((p) => p.slug !== "acnologia-peer").map((p) => ({ value: p.slug, label: p.name, meta: p.slug }));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!d.name.trim()) return setErr("Give the share a name, such as the server it is for.");
    if (!d.services.length && !d.providers.length) return setErr("Pick at least one service or provider to share.");
    const handover = d.mode === "handover";
    // The other server runs handover requests, so this one cannot hold it to limits.
    const limits = handover ? { ...NO_LIMITS } : parseDraftLimits(d);
    if (typeof limits === "string") return setErr(limits);
    // An untouched end time is sent back as it was, even if it already passed.
    const keptEnd = initial && JSON.stringify(d.ends) === JSON.stringify(endsDraftOf(shareEndsAt(initial)));
    const ends = keptEnd ? { iso: shareEndsAt(initial) ?? "" } : resolveEnds(d.ends);
    if ("error" in ends) return setErr(ends.error);
    const key_scopes: Record<string, KeyScope> = {};
    for (const slug of d.services) {
      const sc = d.key_scopes[slug];
      if (!sc || sc.mode === "all") continue;
      if (sc.mode === "count") {
        key_scopes[slug] = { mode: "count", count: Math.max(1, sc.count ?? 1) };
        continue;
      }
      // Only keys still listed: a deleted key drops out, and so does a hidden one once hidden keys are off.
      const sv = services.find((s) => s.slug === slug);
      const loaded = sv ? keys[sv.id] : undefined;
      let ids = sc.key_ids ?? [];
      if (Array.isArray(loaded)) {
        const ok = new Set(visibleKeys(loaded, d.include_hidden, isSuper).map((k) => k.id));
        ids = ids.filter((id) => ok.has(id));
      }
      if (!ids.length) return setErr(`Pick at least one key of ${sv?.name ?? slug}, or share all its keys.`);
      key_scopes[slug] = { mode: "pick", key_ids: ids };
    }
    const body: ShareInput = {
      name: d.name.trim(),
      note: d.note,
      services: d.services,
      providers: d.providers,
      key_scopes,
      limits,
      ends_at: ends.iso,
      // Same moment for servers that only know expires_at.
      expires_at: ends.iso,
      end_action: d.end_action,
    };
    if (!modeLocked) body.mode = d.mode;
    if (handover) body.lease = d.lease;
    if (isSuper) body.include_hidden = d.include_hidden;
    if (!initial) body.invite_valid = d.invite_valid;
    setBusy(true);
    try {
      if (initial) {
        onSaved({ share: await sharing.updateShare(initial.id, body) });
        toast.success(`Saved ${body.name}`);
      } else {
        onSaved(await sharing.createShare(body));
      }
      onClose();
    } catch (e2) {
      setErr(errorMessage(e2));
    } finally {
      setBusy(false);
    }
  };

  const handover = d.mode === "handover";
  const parsedLimits = parseDraftLimits(d);
  const limitLine = handover || typeof parsedLimits === "string" ? "" : limitSummary(parsedLimits);

  return (
    <Modal
      open={open}
      onClose={onClose}
      wide
      variant="drawer"
      title={initial ? `Edit ${initial.name}` : "New share"}
      description={initial ? "Changes reach the other server at its next sync." : "Give one other server use of some services or provider specs."}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="share-form" disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
            {initial ? "Save" : "Create and get invite"}
          </Button>
        </>
      }
    >
      <form id="share-form" onSubmit={submit} className="flex flex-col gap-5" noValidate>
        {err && <Callout tone="error">{err}</Callout>}
        <ModePicker value={d.mode} onChange={(mode) => setD({ ...d, mode })} readOnly={modeLocked} />
        {handover && (
          <div className="flex flex-col gap-3">
            <LeasePicker value={d.lease} onChange={(lease) => setD({ ...d, lease })} />
            <HandoverWarning />
          </div>
        )}
        <Input label="Name" isRequired value={d.name} onChange={(name) => setD({ ...d, name })} placeholder="Railway deploy" autoComplete="off" />
        <div className="flex flex-col gap-1.5">
          <Label id="share-services-label">Services</Label>
          <div role="group" aria-labelledby="share-services-label">
            <MultiSelect
              options={serviceOptions}
              value={d.services}
              onChange={(services) => setD({ ...d, services })}
              empty="No services to share yet."
              searchLabel="services"
            />
          </div>
          <HintText>
            {handover
              ? "The keys in scope go to the other server, sealed to it. Keys from another server are never handed on."
              : "The other server proxies through this one. It never sees a key and cannot lease one."}
          </HintText>
          {d.services.length > 0 && (
            <div className="mt-2 flex flex-col gap-2">
              <div className="text-body-medium text-text-primary">Keys to share</div>
              <KeyScopes
                services={services}
                selected={d.services}
                scopes={d.key_scopes}
                keys={keys}
                includeHidden={d.include_hidden}
                isSuper={isSuper}
                now={now}
                onChange={(slug, sc) => setD((cur) => ({ ...cur, key_scopes: { ...cur.key_scopes, [slug]: sc } }))}
              />
            </div>
          )}
        </div>
        <div className="flex flex-col gap-1.5">
          <Label id="share-providers-label">Provider specs</Label>
          <div role="group" aria-labelledby="share-providers-label">
            <MultiSelect options={providerOptions} value={d.providers} onChange={(providers) => setD({ ...d, providers })} searchLabel="provider specs" />
          </div>
          <HintText>Definitions only, no keys. The other server can add its own keys to them.</HintText>
        </div>
        <fieldset className="flex flex-col gap-3" aria-describedby="share-limits-hint">
          <legend className="mb-1 text-body-medium text-text-primary">Limits</legend>
          {limitLine && (
            <p aria-live="polite" className="text-caption-1-medium text-text-primary">
              {limitLine}
            </p>
          )}
          <p id="share-limits-hint" className="text-caption-1-regular text-text-secondary">
            {handover
              ? "Not available when handing over keys: requests run on the other server, so this one cannot count or stop them. The timer and renewal window still apply."
              : "Leave a field empty for no limit. Calls over a limit are refused until it resets."}
          </p>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {LIMIT_KEYS.map((k) => (
              <Input
                key={k}
                label={LIMIT_LABEL[k]}
                inputMode={k.endsWith("_budget") ? "decimal" : "numeric"}
                value={handover ? "" : d.limits[k]}
                onChange={(v) => setD({ ...d, limits: { ...d.limits, [k]: v } })}
                placeholder={handover ? "not available" : "no limit"}
                isDisabled={handover}
                inputClassName="font-mono tabular-nums"
              />
            ))}
          </div>
        </fieldset>
        <fieldset className="flex flex-col gap-4">
          <legend className="mb-1 text-body-medium text-text-primary">Timer</legend>
          <EndsPicker value={d.ends} onChange={(ends) => setD({ ...d, ends })} />
          <EndActionPicker value={d.end_action} onChange={(end_action) => setD({ ...d, end_action })} handover={handover} />
        </fieldset>
        {!initial && (
          <Select
            label="Invite valid for"
            selectedKey={d.invite_valid}
            onSelectionChange={(k) => k != null && setD({ ...d, invite_valid: k as InviteValidity })}
            className="w-full sm:max-w-xs"
            popoverClassName="w-(--trigger-width)"
          >
            {VALIDITY.map((v) => (
              <SelectItem key={v.id} id={v.id} textValue={v.label}>
                {v.label}
              </SelectItem>
            ))}
          </Select>
        )}
        <Textarea label="Note" value={d.note} onChange={(note) => setD({ ...d, note })} rows={2} placeholder="Who this is for, optional" maxLength={500} />
        {isSuper && (
          <div className="flex items-start justify-between gap-4 rounded-2xl bg-background-secondary-default p-4">
            <div className="min-w-0">
              <div id="share-hidden-label" className="text-body-medium text-text-primary">
                Include hidden keys
              </div>
              <p id="share-hidden-hint" className="mt-0.5 text-caption-1-regular text-text-secondary">
                {handover
                  ? "Hands over hidden keys too. Turn this on only for servers you trust with those keys."
                  : "Lets the other server's requests use hidden keys too. Some upstream answers can echo a key, so turn this on only for servers you trust."}
              </p>
            </div>
            <Switch isSelected={d.include_hidden} onChange={(v) => setD({ ...d, include_hidden: v })} aria-labelledby="share-hidden-label" aria-describedby="share-hidden-hint" />
          </div>
        )}
      </form>
    </Modal>
  );
}

function InviteModal({ invite, onClose }: { invite: { name: string; code: string } | null; onClose: () => void }) {
  return (
    <Modal
      open={!!invite}
      onClose={onClose}
      title={`Invite for ${invite?.name ?? ""}`}
      footer={
        <Button variant="primary" onClick={onClose}>
          I have copied it
        </Button>
      }
    >
      {invite && (
        <div className="flex flex-col gap-3">
          <Callout tone="warning">This invite is shown once and works once. Send it to the other server's superadmin over a private channel.</Callout>
          <SecretBox value={invite.code} />
          <p className="text-body-2-regular text-text-secondary">On the other server, open Sharing, choose Connect a server and paste it.</p>
        </div>
      )}
    </Modal>
  );
}

function ShareRow({
  share,
  isSuper,
  now,
  onChanged,
  onEdit,
  onInvite,
  onRevoked,
}: {
  share: Share;
  isSuper: boolean;
  now: number;
  onChanged: () => void;
  onEdit: () => void;
  onInvite: (code: string) => void;
  onRevoked: (share: Share, deadline: string | null) => void;
}) {
  const [busy, setBusy] = useState<string | null>(null);
  const mode = shareMode(share);
  const handover = mode === "handover";
  const endsAt = shareEndsAt(share);
  const endAction = shareEndAction(share);
  const leaseMs = LEASES.find((l) => l.id === shareLease(share))?.ms ?? 0;
  const deadline = handover ? wipeDeadline(share, leaseMs) : null;
  const revoke = async () => {
    setBusy("revoke");
    try {
      const after = await sharing.revokeShare(share.id);
      if (handover) onRevoked({ ...share, ...after }, deadline);
      else toast.success(`Revoked ${share.name}`);
      onChanged();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };
  const extend = async (iso: string) => {
    try {
      await sharing.extendShare(share.id, iso);
    } catch (e) {
      // A server without the extend endpoint still takes a new end time on edit.
      if (!(e instanceof ApiError) || (e.status !== 404 && e.status !== 405)) throw new Error(errorMessage(e));
      await sharing.updateShare(share.id, { ends_at: iso, expires_at: iso });
    }
    toast.success(iso ? `Extended ${share.name} to ${dateTime(iso)}` : `${share.name} no longer ends on its own`);
    onChanged();
  };
  const act = async (what: string, fn: () => Promise<unknown>, done: string) => {
    setBusy(what);
    try {
      await fn();
      toast.success(done);
      onChanged();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };
  const live = share.status !== "revoked";
  return (
    <li className="rounded-3xl border border-border-button-default bg-background-primary-default p-5">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className={cx("min-w-0 wrap-anywhere text-headline-medium", live ? "text-text-primary" : "text-text-secondary")}>{share.name}</span>
            <Chip variant="caption" color={SHARE_COLOR[share.status]}>
              {share.left_at ? "left by peer" : share.status}
            </Chip>
            <ModeChip mode={mode} />
          </div>
          <div className="mt-1 text-caption-1-regular text-text-secondary">
            {share.peer_id ? (
              <>
                <span className="text-text-primary">{share.peer_name || "Unnamed server"}</span> · <Mono className="text-text-secondary">{share.peer_id}</Mono>
              </>
            ) : share.status === "pending" ? (
              <>Waiting for the invite{share.invite_expires_at ? `, valid until ${dateTime(share.invite_expires_at)}` : ""}</>
            ) : (
              "Never redeemed"
            )}
          </div>
          <div className="mt-2 flex flex-wrap gap-1">
            {share.services.map((s) => {
              const k = share.service_keys?.[s];
              const scoped = !!share.key_scopes?.[s] && share.key_scopes[s].mode !== "all";
              return (
                <Chip key={`s-${s}`} variant="caption" color="purple" className="font-mono">
                  {s}
                  {k && <> · {scoped ? `${k.keys_shared} of ${plural(k.keys_total, "key")}` : plural(k.keys_total, "key")}</>}
                </Chip>
              );
            })}
            {share.providers.map((p) => (
              <Chip key={`p-${p}`} variant="caption" color="blue" className="font-mono">
                spec: {p}
              </Chip>
            ))}
            {share.include_hidden && (
              <Chip variant="caption" color="yellow">
                hidden keys
              </Chip>
            )}
          </div>
          <div className="mt-2 font-mono text-caption-1-regular text-text-secondary">
            {compact(share.usage?.today_requests ?? 0)} today{handover && " (reported)"} · last used {relTime(share.usage?.last_used_at, now)}
            {share.usage?.limit_hit && <span className="text-text-error-primary"> · limit reached</span>}
          </div>
          <div className="mt-1 flex flex-wrap items-baseline gap-x-1.5 text-caption-1-regular text-text-secondary">
            {share.status === "revoked" ? (
              <span>Revoked{share.revoked_at ? ` ${dateTime(share.revoked_at)}` : ""}</span>
            ) : share.status === "expired" ? (
              <span>
                Expired{endsAt ? ` ${dateTime(endsAt)}` : ""}
                {isSuper ? ". Extend it to turn it back on." : ""}
              </span>
            ) : endsAt ? (
              <>
                <Countdown iso={endsAt} subject={share.name} className="text-text-primary" />
                <span>
                  then {END_ACTION_LABEL[endAction]}
                  {endAction === "expire" && " (can be extended)"}
                </span>
              </>
            ) : (
              <span>No end time</span>
            )}
          </div>
          {!handover && (
            <div className="mt-3">
              <LimitMeters share={share} />
            </div>
          )}
          {handover && share.status !== "pending" && <HandoverKeysList share={share} />}
        </div>
        <div className="flex shrink-0 flex-col gap-3 lg:items-end">
          <UsageBars share={share} />
          {isSuper && live && (
            <div className="flex flex-wrap items-center gap-1">
              {share.status === "pending" && (
                <Button
                  size="small"
                  variant="ghost"
                  leadingIcon={busy === "invite" ? ButtonSpinner : RiTicketLine}
                  disabled={!!busy}
                  onClick={async () => {
                    setBusy("invite");
                    try {
                      const r = await sharing.newInvite(share.id, "24h");
                      onInvite(r.invite);
                      onChanged();
                    } catch (e) {
                      toast.error(errorMessage(e));
                    } finally {
                      setBusy(null);
                    }
                  }}
                >
                  New invite
                </Button>
              )}
              {share.status === "active" && (
                <Button size="small" variant="ghost" leadingIcon={busy === "pause" ? ButtonSpinner : RiPauseLine} disabled={!!busy} onClick={() => void act("pause", () => sharing.pauseShare(share.id), `Paused ${share.name}`)}>
                  Pause
                </Button>
              )}
              {share.status === "paused" && (
                <Button size="small" variant="ghost" leadingIcon={busy === "resume" ? ButtonSpinner : RiPlayLine} disabled={!!busy} onClick={() => void act("resume", () => sharing.resumeShare(share.id), `Resumed ${share.name}`)}>
                  Resume
                </Button>
              )}
              {(share.status === "active" || share.status === "expired") && <ExtendButton name={share.name} endsAt={endsAt} onExtend={extend} />}
              <Button size="small" variant="ghost" leadingIcon={RiPencilLine} disabled={!!busy} onClick={onEdit}>
                Edit
              </Button>
              <ConfirmButton
                label="Revoke"
                confirmLabel={handover ? "Revoke and wipe" : "Revoke for good"}
                variant="outline"
                description={
                  handover
                    ? `Sends a delete signal now. If the other server cannot be reached, its copy is wiped by ${deadline ? dateTime(deadline) : "the end of the renewal window"}. This cannot be undone.`
                    : "The other server stops at once. This cannot be undone."
                }
                onConfirm={revoke}
              />
            </div>
          )}
          {isSuper && share.status === "revoked" && handover && (
            <Button size="small" variant="ghost" onClick={() => onRevoked(share, wipeDeadline(share, 0))}>
              Keys to rotate
            </Button>
          )}
        </div>
      </div>
    </li>
  );
}

// ---- Shared with you ----

function ConnectModal({ open, onClose, onConnected }: { open: boolean; onClose: () => void; onConnected: () => void }) {
  const [invite, setInvite] = useState("");
  const [trusted, setTrusted] = useState(false);
  const [preview, setPreview] = useState<PeerPreview | null>(null);
  const [prefix, setPrefix] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setInvite("");
      setTrusted(false);
      setPreview(null);
      setPrefix("");
      setErr(null);
    }
  }, [open]);

  const look = async (e: FormEvent) => {
    e.preventDefault();
    if (!invite.trim()) return setErr("Paste the invite code you were sent.");
    setBusy(true);
    setErr(null);
    try {
      const p = await sharing.previewPeer(invite.trim(), trusted);
      setPreview(p);
      setPrefix(p.prefix);
    } catch (e2) {
      setErr(errorMessage(e2));
    } finally {
      setBusy(false);
    }
  };

  const connect = async () => {
    if (!preview) return;
    setBusy(true);
    setErr(null);
    try {
      const link = await sharing.connectPeer({ invite: invite.trim(), prefix: prefix.trim(), trusted, owner_id: preview.owner.id });
      toast.success(`Connected to ${link.peer_name}`);
      if (link.last_error) toast.error(link.last_error);
      onConnected();
      onClose();
    } catch (e2) {
      setErr(errorMessage(e2));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      wide
      title="Connect a server"
      description="Use services another Acnologia shares with you."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          {preview ? (
            <Button variant="primary" onClick={() => void connect()} disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : RiLinkM}>
              Connect
            </Button>
          ) : (
            <Button variant="primary" type="submit" form="connect-form" disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
              Check invite
            </Button>
          )}
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {err && <Callout tone="error">{err}</Callout>}
        {!preview ? (
          <form id="connect-form" onSubmit={look} className="flex flex-col gap-4" noValidate>
            <Textarea label="Invite code" value={invite} onChange={setInvite} rows={3} placeholder="acninv_..." fieldClassName="font-mono" />
            <div className="flex items-start justify-between gap-4 rounded-2xl bg-background-secondary-default p-4">
              <div className="min-w-0">
                <div id="connect-private-label" className="text-body-medium text-text-primary">
                  This server is on my private network
                </div>
                <p id="connect-private-hint" className="mt-0.5 text-caption-1-regular text-text-secondary">
                  Allows a private or local address, such as two servers on one machine. Leave off for servers on the internet.
                </p>
              </div>
              <Switch isSelected={trusted} onChange={setTrusted} aria-labelledby="connect-private-label" aria-describedby="connect-private-hint" />
            </div>
          </form>
        ) : (
          <>
            <div className="rounded-2xl border border-border-button-default p-4">
              <div className="text-headline-medium text-text-primary">{preview.owner.name}</div>
              <dl className="mt-2 grid grid-cols-1 gap-2 text-caption-1-regular">
                <div>
                  <dt className="text-text-secondary">Server id</dt>
                  <dd>
                    <Mono>{preview.owner.id}</Mono>
                  </dd>
                </div>
                <div>
                  <dt className="text-text-secondary">Key fingerprint</dt>
                  <dd>
                    <Mono>{preview.fingerprint}</Mono>
                  </dd>
                </div>
                <div>
                  <dt className="text-text-secondary">URL</dt>
                  <dd>
                    <Mono>{preview.url}</Mono>
                  </dd>
                </div>
              </dl>
              <p className="mt-3 text-caption-1-regular text-text-secondary">Check the fingerprint with the person who sent the invite before you connect.</p>
            </div>
            <div>
              <div className="text-body-medium text-text-primary">{preview.share.name}</div>
              <div className="mt-2 flex flex-wrap gap-1">
                {preview.share.services.map((s) => (
                  <Chip key={s.slug} variant="caption" color="purple">
                    {s.name}
                  </Chip>
                ))}
                {preview.share.providers.map((p) => (
                  <Chip key={p} variant="caption" color="blue" className="font-mono">
                    spec: {p}
                  </Chip>
                ))}
              </div>
            </div>
            <Input
              label="Service slug prefix"
              value={prefix}
              onChange={setPrefix}
              fieldClassName="font-mono"
              hint={preview.share.services[0] ? `Services appear here as ${prefix || "prefix"}-${preview.share.services[0].slug}.` : "Used for the services this share adds."}
              autoComplete="off"
            />
          </>
        )}
      </div>
    </Modal>
  );
}

function PeerRow({ link, isSuper, now, onChanged }: { link: PeerLink; isSuper: boolean; now: number; onChanged: () => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  const gone = link.status === "revoked" || link.status === "disconnected";
  return (
    <li className="rounded-3xl border border-border-button-default bg-background-primary-default p-5">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className={cx("min-w-0 wrap-anywhere text-headline-medium", gone ? "text-text-secondary" : "text-text-primary")}>{link.peer_name}</span>
            <Chip variant="caption" color={LINK_COLOR[link.status]}>
              {link.status}
            </Chip>
            <ModeChip mode={shareMode(link)} />
            {link.trusted && (
              <Chip variant="caption" color="neutral">
                private network
              </Chip>
            )}
          </div>
          <div className="mt-1">
            <Mono className="text-text-secondary">{link.peer_id}</Mono>
          </div>
          <div className="mt-2 flex flex-wrap gap-1">
            {link.local_services.map((s) => (
              <Link
                key={s.id}
                href={`/services/${s.id}`}
                className="rounded-full outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
              >
                <Chip variant="caption" color="purple" className="font-mono">
                  {s.slug}
                </Chip>
              </Link>
            ))}
            {link.providers.map((p) => (
              <Link key={p} href={`/providers/${p}`} className="rounded-full outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring">
                <Chip variant="caption" color="blue" className="font-mono">
                  spec: {p}
                </Chip>
              </Link>
            ))}
          </div>
          <div className="mt-2 font-mono text-caption-1-regular text-text-secondary" title={dateTime(link.last_sync_at)}>
            last sync {relTime(link.last_sync_at, now)}
          </div>
          {link.last_error && <div className="mt-1 wrap-anywhere text-caption-1-regular text-text-error-primary">{link.last_error}</div>}
          {shareMode(link) === "handover" && !gone && <PeerHandoverKeys keys={linkHandoverKeys(link)} />}
        </div>
        {isSuper && (
          <div className="flex shrink-0 flex-wrap items-center gap-1">
            {!gone && (
              <Button
                size="small"
                variant="ghost"
                leadingIcon={busy === "sync" ? ButtonSpinner : RiRefreshLine}
                disabled={!!busy}
                onClick={async () => {
                  setBusy("sync");
                  try {
                    const l = await sharing.syncPeer(link.id);
                    if (l.last_error) toast.error(l.last_error);
                    else toast.success(`Synced ${link.peer_name}`);
                    onChanged();
                  } catch (e) {
                    toast.error(errorMessage(e));
                  } finally {
                    setBusy(null);
                  }
                }}
              >
                Sync now
              </Button>
            )}
            <ConfirmButton
              label={gone ? "Remove" : "Disconnect"}
              confirmLabel={gone ? "Remove from list" : "Disconnect for good"}
              variant="outline"
              onConfirm={async () => {
                try {
                  await sharing.disconnectPeer(link.id);
                  toast.success(gone ? `Removed ${link.peer_name}` : `Disconnected from ${link.peer_name}`);
                  onChanged();
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

// ---- page ----

export function SharingPage() {
  useTitle("Sharing");
  const isSuper = can.hidden(useAuth((s) => s.user?.role));
  const now = useNow(30000);
  const fetchServices = useServices((s) => s.fetch);
  const ensureProviders = useProviders((s) => s.ensure);
  const [identity, setIdentity] = useState<ServerIdentity | null>(null);
  const [shares, setShares] = useState<Share[]>([]);
  const [peers, setPeers] = useState<PeerLink[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Share | null>(null);
  const [invite, setInvite] = useState<{ name: string; code: string } | null>(null);
  const [connectOpen, setConnectOpen] = useState(false);
  const [revoked, setRevoked] = useState<{ share: Share; deadline: string | null } | null>(null);

  const load = useCallback(async () => {
    try {
      const [id, sh, pl] = await Promise.all([sharing.identity(), sharing.listShares(), sharing.listPeers()]);
      setIdentity(id);
      setShares(sh.shares ?? []);
      setPeers(pl.peers ?? []);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => {
    void load();
    void fetchServices();
    void ensureProviders();
  }, [load, fetchServices, ensureProviders]);

  const activeShares = useMemo(() => shares.filter((s) => s.status === "active").length, [shares]);

  if (!loaded) return <PageSkeleton variant="list" />;
  if (error && !identity) return <EmptyState title="Sharing is not available" body={error} />;

  const openNew = () => {
    setEditing(null);
    setFormOpen(true);
  };

  return (
    <div className="flex flex-col gap-8">
      <div>
        <PageHeader title="Sharing" meta={`${plural(shares.length, "share")} · ${activeShares} active · ${plural(peers.length, "connection")}`} className="mb-4" />
        <p className="max-w-2xl text-body-2-regular text-text-secondary">
          Let another Acnologia use some of your services, or use services another one shares with you. {PROMISE} Handover shares give the keys to the other server, which deletes them when the share ends. A share can be timed, paused and revoked at any time.
        </p>
      </div>

      {identity && <ThisServer identity={identity} isSuper={isSuper} onSaved={setIdentity} />}

      <section aria-labelledby="shared-by-you">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 id="shared-by-you" className="text-title-3-semibold text-text-primary">
            Shared by you
          </h2>
          {isSuper && (
            <Button variant="primary" leadingIcon={RiAddLine} onClick={openNew}>
              New share
            </Button>
          )}
        </div>
        {shares.length === 0 ? (
          <EmptyState
            art={UnpluggedArt}
            icon={RiShareForwardLine}
            title="Nothing shared yet"
            body="Share a service by proxy and the other server never sees a key, or hand the keys over so it calls the provider directly."
            action={
              isSuper ? (
                <Button variant="primary" leadingIcon={RiAddLine} onClick={openNew}>
                  Share with a server
                </Button>
              ) : undefined
            }
          />
        ) : (
          <ul className="flex flex-col gap-3">
            {shares.map((s) => (
              <ShareRow
                key={s.id}
                share={s}
                isSuper={isSuper}
                now={now}
                onChanged={() => void load()}
                onEdit={() => {
                  setEditing(s);
                  setFormOpen(true);
                }}
                onInvite={(code) => setInvite({ name: s.name, code })}
                onRevoked={(share, deadline) => setRevoked({ share, deadline })}
              />
            ))}
          </ul>
        )}
      </section>

      <section aria-labelledby="shared-with-you">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 id="shared-with-you" className="text-title-3-semibold text-text-primary">
            Shared with you
          </h2>
          {isSuper && (
            <Button variant="secondary" leadingIcon={RiLinkM} onClick={() => setConnectOpen(true)}>
              Connect a server
            </Button>
          )}
        </div>
        {peers.length === 0 ? (
          <EmptyState
            icon={RiLinkM}
            title="No connections yet"
            body="Paste an invite from another Acnologia to use what it shares."
            action={
              isSuper ? (
                <Button variant="secondary" leadingIcon={RiLinkM} onClick={() => setConnectOpen(true)}>
                  Connect a server
                </Button>
              ) : undefined
            }
          />
        ) : (
          <ul className="flex flex-col gap-3">
            {peers.map((l) => (
              <PeerRow
                key={l.id}
                link={l}
                isSuper={isSuper}
                now={now}
                onChanged={() => {
                  void load();
                  void fetchServices();
                }}
              />
            ))}
          </ul>
        )}
      </section>

      <ShareFormModal
        open={formOpen}
        initial={editing}
        isSuper={isSuper}
        onClose={() => setFormOpen(false)}
        onSaved={(r) => {
          if (r.invite) setInvite({ name: r.share.name, code: r.invite });
          void load();
        }}
      />
      <InviteModal invite={invite} onClose={() => setInvite(null)} />
      <RevokeResultModal share={revoked?.share ?? null} deadline={revoked?.deadline ?? null} onClose={() => setRevoked(null)} />
      <ConnectModal
        open={connectOpen}
        onClose={() => setConnectOpen(false)}
        onConnected={() => {
          void load();
          void fetchServices();
          void ensureProviders();
        }}
      />
    </div>
  );
}
