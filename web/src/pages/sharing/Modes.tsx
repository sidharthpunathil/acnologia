// Share modes: the proxy or handover choice, the renewal window, the handed
// over keys (owner and receiver side) and the revoke flow for handover.
import { useEffect, useId, useState, type ReactNode } from "react";
import { RiArrowDownSLine, RiExternalLinkLine, RiEyeLine, RiKey2Line, RiLinkM, RiServerLine, RiShieldKeyholeLine, RiUserLine } from "@remixicon/react";
import {
  LEASES,
  handoverKeys,
  personLabel,
  revealedKeys,
  rotateChecklist,
  shareMode,
  type PersonMode,
  type ShareAudience,
  type HandoverKey,
  type Lease,
  type PeerHandoverKey,
  type RevokePush,
  type RotateItem,
  type Share,
  type ShareMode,
} from "../../lib/sharing";
import { compact, dateTime, plural } from "../../lib/format";
import { useServices } from "../../stores/services";
import { useProviders } from "../../stores/providers";
import { Modal } from "../../components/Modal";
import { Button } from "@/components/base/buttons/button";
import { Checkbox } from "@/components/base/checkbox/checkbox";
import { Chip } from "@/components/base/badges/chip";
import { RadioGroup } from "@/components/base/radio/radio";
import { RadioCard } from "@/components/base/radio/radio-card";
import { Select, SelectItem } from "@/components/base/select/select";
import { Callout, CopyButton } from "@/components/acn";
import { cx } from "@/utils/cx";
import { Countdown } from "./Timers";

// ---- choice cards ----

interface Choice<T extends string> {
  id: T;
  title: string;
  body: string;
  icon: typeof RiShieldKeyholeLine;
  recommended?: boolean;
}

/**
 * One option in a share picker: the icon in its own column so the title and
 * the text line up under each other, the radio dot on the right.
 */
function ChoiceCard<T extends string>({ choice, readOnly }: { choice: Choice<T>; readOnly?: boolean }) {
  const Icon = choice.icon;
  return (
    <RadioCard
      value={choice.id}
      title={
        <span className="flex items-start gap-3 whitespace-normal">
          <Icon className="mt-0.5 size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />
          <span className="flex min-w-0 flex-col gap-1">
            <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
              {choice.title}
              {choice.recommended && (
                <Chip variant="caption" color="lime">
                  Recommended
                </Chip>
              )}
            </span>
            <span className="text-body-2-regular text-text-secondary">{choice.body}</span>
          </span>
        </span>
      }
      className={({ isSelected }) =>
        cx("h-full items-start rounded-3xl p-4 sm:p-5", isSelected && "border-accent-line ring-1 ring-accent-line", readOnly && isSelected && "opacity-100")
      }
    />
  );
}

// ---- mode choice ----

const MODE_CARDS: Choice<ShareMode>[] = [
  { id: "proxy", title: "Proxy through this server", body: "Calls go through this server. The other server never sees your keys, and revoking is instant.", icon: RiShieldKeyholeLine, recommended: true },
  // The risk is spelled out in HandoverWarning, shown once this is picked.
  { id: "handover", title: "Hand over the keys", body: "The other server gets the keys and calls the provider itself. Revoking asks it to delete them.", icon: RiKey2Line },
];

export const MODE_LABEL: Record<ShareMode, string> = { proxy: "Proxy", handover: "Handover", reveal: "Keys shown" };

const MODE_ICON: Record<ShareMode, typeof RiKey2Line> = { proxy: RiShieldKeyholeLine, handover: RiKey2Line, reveal: RiEyeLine };

export function ModeChip({ mode }: { mode: ShareMode }) {
  const Icon = MODE_ICON[mode];
  return (
    <Chip variant="caption" color={mode === "proxy" ? "neutral" : "orange"} className="gap-1">
      <Icon className="size-3" aria-hidden />
      {MODE_LABEL[mode]}
    </Chip>
  );
}

// ---- recipient and person modes ----

// How a person uses the link is the next question, so it is not repeated here.
const RECIPIENT_CARDS: Choice<ShareAudience>[] = [
  { id: "server", title: "Another server", body: "Someone running their own Acnologia. They redeem an invite there.", icon: RiServerLine },
  { id: "person", title: "One person", body: "They open a link that works once, in any browser.", icon: RiUserLine },
  { id: "link", title: "Anyone with a link", body: "Post one link for a team or a group. Everyone who opens it gets a token of their own.", icon: RiLinkM },
];

/** "Share with": another server, one person, or anyone with a link. Fixed once the share exists. */
export function RecipientPicker({ value, onChange }: { value: ShareAudience; onChange: (r: ShareAudience) => void }) {
  const labelId = useId();
  return (
    <div className="flex flex-col gap-2">
      <div id={labelId} className="text-body-medium text-text-primary">
        Share with
      </div>
      <RadioGroup aria-labelledby={labelId} value={value} onChange={(v) => onChange(v as ShareAudience)} className="grid grid-cols-1 gap-3">
        {RECIPIENT_CARDS.map((c) => (
          <ChoiceCard key={c.id} choice={c} />
        ))}
      </RadioGroup>
    </div>
  );
}

const PERSON_MODE_CARDS: Choice<PersonMode>[] = [
  { id: "proxy", title: "Through this server", body: "They get a personal token. They never see your keys. Revoke is instant.", icon: RiShieldKeyholeLine, recommended: true },
  { id: "reveal", title: "Show them the keys", body: "They see the keys once. Revoke cannot take them back; rotate keys when the share ends.", icon: RiEyeLine },
];

export const PERSON_MODE_LABEL: Record<PersonMode, string> = { proxy: "Through this server", reveal: "Keys shown" };

/** How a person uses the share: a token through this server, or the keys themselves. */
export function PersonModePicker({ value, onChange, readOnly }: { value: PersonMode; onChange: (m: PersonMode) => void; readOnly?: boolean }) {
  const labelId = useId();
  const hintId = useId();
  return (
    <div className="flex flex-col gap-2">
      <div id={labelId} className="text-body-medium text-text-primary">
        How they use your services
      </div>
      <RadioGroup
        aria-labelledby={labelId}
        aria-describedby={readOnly ? hintId : undefined}
        value={value}
        onChange={(v) => onChange(v as PersonMode)}
        isDisabled={readOnly}
        className="grid grid-cols-1 gap-3"
      >
        {PERSON_MODE_CARDS.map((m) => (
          <ChoiceCard key={m.id} choice={m} readOnly={readOnly} />
        ))}
      </RadioGroup>
      {readOnly && (
        <p id={hintId} className="text-caption-1-regular text-text-secondary">
          The mode is fixed once the link is claimed. Create a new share to change it.
        </p>
      )}
    </div>
  );
}

export function RevealWarning({ hiddenExcluded }: { hiddenExcluded: boolean }) {
  return (
    <Callout tone="warning" title="A key they have seen cannot be taken back">
      Revoke and the timer stop the share, but the person keeps any key they copied. Rotate the keys at the provider when the share ends.
      {hiddenExcluded && " Hidden keys are never shown, even if they are in scope."}
    </Callout>
  );
}

// ---- revealed keys (owner) ----

/** "Keys shown to <label>", collapsed by default. */
export function RevealedKeysList({ share, label }: { share: Share; label: string }) {
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const keys = revealedKeys(share);
  return (
    <div className="mt-3">
      <Button
        size="small"
        variant="ghost"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((o) => !o)}
        trailingIcon={RiArrowDownSLine}
        className={cx("-ms-2 [&_svg:last-child]:transition-transform", open && "[&_svg:last-child]:rotate-180")}
      >
        Keys shown to {label} ({keys.length})
      </Button>
      {open && (
        <div id={panelId} className="mt-2">
          {keys.length === 0 ? (
            <p className="text-caption-1-regular text-text-secondary">
              {share.claimed_at ? "No keys were shown." : `The keys in scope are shown to ${label} once, when they open the link.`}
            </p>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {keys.map((k) => (
                <li key={k.key_id} className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 rounded-xl bg-background-secondary-default px-3 py-2 text-caption-1-regular">
                  <span className="min-w-0 wrap-anywhere text-text-primary">
                    {k.label || `Key ${k.key_id}`}
                    <span className="font-mono text-text-secondary"> · {k.service}</span>
                  </span>
                  {k.revealed_at && <span className="text-text-secondary">shown {dateTime(k.revealed_at)}</span>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

/** Two large cards; a react-aria radio group, so arrow keys move and the choice is announced. */
export function ModePicker({ value, onChange, readOnly }: { value: ShareMode; onChange: (m: ShareMode) => void; readOnly?: boolean }) {
  const labelId = useId();
  const hintId = useId();
  return (
    <div className="flex flex-col gap-2">
      <div id={labelId} className="text-body-medium text-text-primary">
        How the other server uses your keys
      </div>
      <RadioGroup
        aria-labelledby={labelId}
        aria-describedby={readOnly ? hintId : undefined}
        value={value}
        onChange={(v) => onChange(v as ShareMode)}
        isDisabled={readOnly}
        className="grid grid-cols-1 gap-3"
      >
        {MODE_CARDS.map((m) => (
          <ChoiceCard key={m.id} choice={m} readOnly={readOnly} />
        ))}
      </RadioGroup>
      {readOnly && (
        <p id={hintId} className="text-caption-1-regular text-text-secondary">
          The mode is fixed once the invite is redeemed. Create a new share to change it.
        </p>
      )}
    </div>
  );
}

/** The renewal window for handed over keys, with what it means. */
export function LeasePicker({ value, onChange }: { value: Lease; onChange: (l: Lease) => void }) {
  return (
    <Select
      label="Renewal window"
      selectedKey={value}
      onSelectionChange={(k) => k != null && onChange(k as Lease)}
      hint="If this server cannot renew within this time, the keys are wiped there."
      className="w-full sm:max-w-xs"
      popoverClassName="w-(--trigger-width)"
    >
      {LEASES.map((l) => (
        <SelectItem key={l.id} id={l.id} textValue={l.label}>
          {l.label}
          {l.id === "24h" ? " (default)" : ""}
        </SelectItem>
      ))}
    </Select>
  );
}

export function HandoverWarning() {
  return (
    <Callout tone="warning" title="A handed over key cannot be taken back">
      Revoke and the renewal window make a cooperating server delete it. A modified server could keep a copy, so only hand keys to servers you trust, and rotate a key at the provider if you stop trusting one.
    </Callout>
  );
}

// ---- handed over keys (owner) ----

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-caption-1-regular text-text-tertiary">{label}</dt>
      <dd className="mt-0.5 min-w-0 wrap-anywhere text-caption-1-regular text-text-primary">{children}</dd>
    </div>
  );
}

function HandoverKeyItem({ k, live }: { k: HandoverKey; live: boolean }) {
  const wiped = !!k.wiped_at;
  return (
    <li className="rounded-2xl border border-border-button-default p-3">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="min-w-0 wrap-anywhere text-body-2-medium text-text-primary">{k.label || "Untitled key"}</span>
        <Chip variant="caption" color="purple" className="font-mono">
          {k.service}
        </Chip>
        {wiped ? (
          <Chip variant="caption" color="gray">
            wiped
          </Chip>
        ) : (
          live && (
            <Chip variant="caption" color="orange">
              held there
            </Chip>
          )
        )}
        {k.reported_status && k.reported_status !== "active" && (
          <Chip variant="caption" color="rose">
            reported {k.reported_status}
          </Chip>
        )}
      </div>
      <dl className="mt-2 grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3">
        <Field label="Handed at">{dateTime(k.handed_at)}</Field>
        <Field label="Last renewed">{dateTime(k.renewed_at)}</Field>
        <Field label="Valid until">
          {wiped || !k.valid_until ? dateTime(k.valid_until) : <Countdown iso={k.valid_until} prefix="wipes in" ended="lapsed" subject={k.label} />}
        </Field>
        <Field label="Reported requests">{k.reported_requests == null ? "-" : compact(k.reported_requests)}</Field>
        <Field label="Reported status">{k.reported_status || "-"}</Field>
        <Field label="Wiped at">{dateTime(k.wiped_at)}</Field>
      </dl>
    </li>
  );
}

/** "Handed over keys", collapsed by default. Requests and status are what the other server reports. */
export function HandoverKeysList({ share }: { share: Share }) {
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const keys = handoverKeys(share);
  const live = share.status === "active";
  const held = keys.filter((k) => !k.wiped_at).length;
  return (
    <div className="mt-3">
      <Button
        size="small"
        variant="ghost"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((o) => !o)}
        trailingIcon={RiArrowDownSLine}
        className={cx("-ms-2 [&_svg:last-child]:transition-transform", open && "[&_svg:last-child]:rotate-180")}
      >
        Handed over keys ({held ? `${held} held` : plural(keys.length, "key")})
      </Button>
      {open && (
        <div id={panelId} className="mt-2 flex flex-col gap-2">
          {keys.length === 0 ? (
            <p className="text-caption-1-regular text-text-secondary">
              {share.status === "pending" ? "Keys are handed over once the invite is redeemed." : "No keys handed over yet. They go at the other server's next sync."}
            </p>
          ) : (
            <>
              <p className="text-caption-1-regular text-text-secondary">Requests and status are reported by the other server.</p>
              <ul className="flex flex-col gap-2">
                {keys.map((k) => (
                  <HandoverKeyItem key={k.key_id} k={k} live={live} />
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </div>
  );
}

/** When the other server's copy is gone at the latest: the latest valid_until still held. */
export function wipeDeadline(share: Share, leaseMs: number): string | null {
  const times = handoverKeys(share)
    .filter((k) => !k.wiped_at && k.valid_until)
    .map((k) => Date.parse(k.valid_until as string))
    .filter((t) => !Number.isNaN(t));
  if (times.length) return new Date(Math.max(...times)).toISOString();
  return leaseMs ? new Date(Date.now() + leaseMs).toISOString() : null;
}

// ---- revoke result and rotate checklist ----

const ROTATE_KEY = (shareId: string) => `acn.rotate.${shareId}`;

function readRotated(shareId: string): number[] {
  try {
    const raw = window.localStorage.getItem(ROTATE_KEY(shareId));
    const v: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(v) ? v.filter((x): x is number => typeof x === "number") : [];
  } catch {
    return [];
  }
}

function writeRotated(shareId: string, ids: number[]) {
  try {
    window.localStorage.setItem(ROTATE_KEY(shareId), JSON.stringify(ids));
  } catch {
    // Storage blocked: the ticks still work until the page closes.
  }
}

function PushOutcome({ push, deadline }: { push: RevokePush | null | undefined; deadline: string | null }) {
  if (!push) {
    return (
      <Callout tone="info" title="Delete signal sent">
        {deadline ? <>The outcome shows here once the server reports it. Either way, the other server's copy is wiped by {dateTime(deadline)}.</> : "The outcome shows here once the server reports it."}
      </Callout>
    );
  }
  if (push.ok) {
    return (
      <Callout tone="success" title={`Wiped${push.at ? ` at ${dateTime(push.at)}` : ""}`}>
        The other server confirmed it deleted the keys.{push.detail ? ` ${push.detail}` : ""}
      </Callout>
    );
  }
  return (
    <Callout tone="warning" title="Not reached">
      {push.detail ? `${push.detail} ` : ""}
      {deadline ? `Its copy is wiped by ${dateTime(deadline)} when the renewal window ends.` : "Its copy is wiped when the renewal window ends."}
    </Callout>
  );
}

function providerLink(item: RotateItem, serviceProvider: (slug: string) => string | undefined, website: (slug: string) => string | undefined): { href: string; label: string } | null {
  if (item.key_page_url) return { href: item.key_page_url, label: "Open key page" };
  const p = item.provider || serviceProvider(item.service);
  const site = p ? website(p) : undefined;
  return site ? { href: site, label: "Provider site" } : null;
}

/** Shown after revoking a handover share: what happened to the copy, and which keys to rotate. */
export function RevokeResultModal({ share, deadline, onClose }: { share: Share | null; deadline: string | null; onClose: () => void }) {
  const services = useServices((s) => s.services);
  const providers = useProviders((s) => s.providers);
  const [rotated, setRotated] = useState<number[]>([]);
  useEffect(() => {
    if (share) setRotated(readRotated(share.id));
  }, [share]);
  const items = share ? rotateChecklist(share) : [];
  const serviceProvider = (slug: string) => services.find((s) => s.slug === slug)?.provider;
  const website = (slug: string) => providers.find((p) => p.slug === slug)?.website || undefined;
  const toggle = (id: number, on: boolean) => {
    if (!share) return;
    const next = on ? [...new Set([...rotated, id])] : rotated.filter((x) => x !== id);
    setRotated(next);
    writeRotated(share.id, next);
  };
  const list = items
    .map((it) => {
      const link = providerLink(it, serviceProvider, website);
      return `${it.service}: ${it.label || `key ${it.key_id}`}${link && it.key_page_url ? ` (${link.href})` : ""}`;
    })
    .join("\n");
  const done = items.filter((it) => rotated.includes(it.key_id)).length;
  // A reveal share has no copy to wipe: the person saw the keys, so rotating is the only way back.
  const reveal = !!share && shareMode(share) === "reveal";
  const who = share ? personLabel(share) : "";
  const ended = share?.status === "expired" ? "Ended" : "Revoked";

  return (
    <Modal
      open={!!share}
      onClose={onClose}
      wide
      title={`${reveal ? ended : "Revoked"} ${share?.name ?? ""}`}
      description={
        reveal
          ? `The share no longer works. ${who} may still have the keys they were shown, so rotate them at the provider.`
          : "The share is dead on this server. Here is what happened to the keys the other server held."
      }
      footer={
        <Button variant="primary" onClick={onClose}>
          Done
        </Button>
      }
    >
      {share && (
        <div className="flex flex-col gap-4">
          {!reveal && <PushOutcome push={share.handover?.last_revoke_push} deadline={deadline} />}
          <div>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <div className="text-body-medium text-text-primary">Keys to rotate</div>
                <p className="text-caption-1-regular text-text-secondary">
                  {reveal
                    ? `Rotate each key ${who} saw at the provider. Ticks are saved in this browser only.`
                    : "Rotate each key at the provider if you do not trust the other server. Ticks are saved in this browser only."}
                </p>
              </div>
              {items.length > 0 && <CopyButton text={list} label="Copy list" />}
            </div>
            {items.length === 0 ? (
              <p className="mt-3 text-caption-1-regular text-text-secondary">
                {reveal ? "No keys were shown, so there is nothing to rotate." : "No keys were handed over, so there is nothing to rotate."}
              </p>
            ) : (
              <>
                <p className="mt-2 text-caption-1-medium text-text-primary" aria-live="polite">
                  {done} of {plural(items.length, "key")} rotated
                </p>
                <ul className="mt-2 flex flex-col gap-2">
                  {items.map((it) => {
                    const link = providerLink(it, serviceProvider, website);
                    const on = rotated.includes(it.key_id);
                    return (
                      <li key={it.key_id} className="flex flex-wrap items-center justify-between gap-2 rounded-2xl border border-border-button-default p-3">
                        <Checkbox isSelected={on} onChange={(v) => toggle(it.key_id, v)} className="min-w-0 items-start">
                          <span className="flex min-w-0 flex-col">
                            <span className={cx("wrap-anywhere text-body-2-medium", on ? "text-text-secondary line-through" : "text-text-primary")}>{it.label || `Key ${it.key_id}`}</span>
                            <span className="font-mono text-caption-1-regular text-text-secondary">{it.service}</span>
                          </span>
                        </Checkbox>
                        {link && (
                          <a
                            href={link.href}
                            target="_blank"
                            rel="noreferrer noopener"
                            className="inline-flex items-center gap-1 rounded-md text-caption-1-medium text-text-primary underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring pointer-coarse:min-h-11"
                          >
                            {link.label}
                            <RiExternalLinkLine className="size-3.5" aria-hidden />
                            <span className="sr-only">(opens in a new tab)</span>
                          </a>
                        )}
                      </li>
                    );
                  })}
                </ul>
              </>
            )}
          </div>
        </div>
      )}
    </Modal>
  );
}

// ---- handed over keys (receiver) ----

export function PeerHandoverKeys({ keys }: { keys: PeerHandoverKey[] }) {
  return (
    <div className="mt-3 rounded-2xl bg-background-secondary-default p-3">
      <div className="text-body-2-medium text-text-primary">Keys handed to this server</div>
      <p className="mt-0.5 text-caption-1-regular text-text-secondary">
        Your apps call the provider with them directly. They are deleted here when the share ends, is paused or revoked, or when the owner stops renewing them. They cannot be revealed, leased or shared on.
      </p>
      {keys.length === 0 ? (
        <p className="mt-2 text-caption-1-regular text-text-secondary">None held right now. They arrive at the next sync.</p>
      ) : (
        <ul className="mt-2 flex flex-col gap-1.5">
          {keys.map((k, i) => (
            <li key={k.key_id ?? i} className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 text-caption-1-regular">
              <span className="min-w-0 wrap-anywhere text-text-primary">
                {k.label || "Untitled key"}
                {k.service && <span className="font-mono text-text-secondary"> · {k.service}</span>}
              </span>
              <span className="text-text-secondary">
                <Countdown iso={k.valid_until} prefix="wipes in" ended="wiped or due to wipe" subject={k.label} />
                {k.valid_until && Date.parse(k.valid_until) > Date.now() && " unless renewed"}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
