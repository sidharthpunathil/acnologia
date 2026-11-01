import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router-dom";
import {
  RiArrowRightLine,
  RiBox3Line,
  RiCheckLine,
  RiCloseLine,
  RiFlaskLine,
  RiKey2Line,
  RiSparkling2Line,
  RiTimeLine,
} from "@remixicon/react";
import { api } from "@/lib/api";
import type { ChatAnswer, ChatCard, ChatEntity, ChatItem, Key, Service } from "@/lib/types";
import { useNow } from "@/hooks/useNow";
import { EntityLogo } from "@/components/EntityLogo";
import { StatusPill } from "@/components/StatusPill";
import { Chip } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { ProgressTrack } from "@/components/base/progress/progress";
import { ButtonSpinner, SpinnerGlyph } from "@/components/base/spinner/spinner";
import { cx } from "@/utils/cx";

/** The card surface every tool result in the transcript shares. */
const CARD = "rounded-3xl border border-border-button-default bg-background-primary-default shadow-xs";

// ---- entity chips ----

/** Where an entity lives in the dashboard. Only ids and slugs from tool results are used. */
export function entityPath(e: ChatEntity): string | null {
  switch (e.type) {
    case "service":
      return e.id ? `/services/${e.id}` : null;
    case "provider":
      return e.slug ? `/providers/${encodeURIComponent(e.slug)}` : null;
    case "app":
      return "/apps";
    case "key":
      return e.name ? `/keys?q=${encodeURIComponent(e.name)}` : null;
  }
  return null;
}

/** A name from a tool result, as a subtle violet chip that links to its page. */
export function EntityChip({ e, children }: { e: ChatEntity; children?: ReactNode }) {
  const to = entityPath(e);
  const label = children ?? e.name;
  if (!to) return <>{label}</>;
  return (
    <Link
      to={to}
      title={`Open ${e.type} ${e.name}`}
      className="group/chip mx-px inline-flex max-w-full rounded-md align-baseline outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
    >
      <Chip
        variant="caption"
        color="purple"
        className="max-w-full truncate px-1.5 py-0 text-body-medium leading-[inherit] transition-[filter] duration-150 ease-out group-hover/chip:brightness-95"
      >
        {label}
      </Chip>
    </Link>
  );
}

/** Every entity tool results in this thread named, by name. Names under 3 characters are skipped. */
export function useEntities(items: ChatItem[]): Map<string, ChatEntity> {
  return useMemo(() => {
    const m = new Map<string, ChatEntity>();
    for (const it of items) {
      for (const e of it.entities ?? []) {
        if (e.name && e.name.length >= 3 && entityPath(e) && !m.has(e.name)) m.set(e.name, e);
        if (e.slug && e.slug.length >= 3 && e.slug !== e.name && entityPath(e) && !m.has(e.slug)) m.set(e.slug, e);
      }
    }
    return m;
  }, [items]);
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/**
 * Splits text around entity names and turns each into a chip. Matches whole
 * words only, longest name first, so "Gemini" never eats "Gemini Pro".
 */
export function linkify(
  text: string,
  entities: Map<string, ChatEntity> | undefined,
  render: (s: string, key: number) => ReactNode,
): ReactNode {
  if (!entities || entities.size === 0 || !text) return render(text, 0);
  const names = [...entities.keys()].sort((a, b) => b.length - a.length).map(escapeRe);
  const re = new RegExp(`(?<![\\w/.-])(${names.join("|")})(?![\\w-])`, "g");
  const out: ReactNode[] = [];
  let last = 0;
  let n = 0;
  for (const m of text.matchAll(re)) {
    const at = m.index ?? 0;
    if (at > last) out.push(render(text.slice(last, at), n++));
    const e = entities.get(m[1]);
    out.push(
      e ? (
        <EntityChip key={`e${n++}`} e={e}>
          {m[1]}
        </EntityChip>
      ) : (
        render(m[1], n++)
      ),
    );
    last = at + m[1].length;
  }
  if (last < text.length) out.push(render(text.slice(last), n++));
  return <>{out}</>;
}

// ---- elapsed time ----

function elapsed(from: string | undefined, to: number): string {
  const a = Date.parse(from ?? "");
  if (Number.isNaN(a)) return "";
  const s = Math.max(0, Math.floor((to - a) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
}

export function Elapsed({ from, to }: { from?: string; to?: string }) {
  const now = useNow(1000);
  const end = to ? Date.parse(to) : now;
  return (
    <span className="font-mono text-caption-1-regular text-text-tertiary tabular-nums">
      {elapsed(from, Number.isNaN(end) ? now : end)}
    </span>
  );
}

/** Text that crossfades when it changes. Reduced motion swaps it plainly (see assistant.css). */
export function SwapText({ text, className }: { text: string; className?: string }) {
  return (
    <span key={text} className={cx("chat-swap inline-block", className)}>
      {text}
    </span>
  );
}

function OpenRun({ to, label = "Open run" }: { to: string; label?: string }) {
  return (
    <Link
      to={to}
      className="inline-flex h-8 shrink-0 items-center gap-1 rounded-lg px-2 text-body-2-medium text-text-primary outline-none transition-colors duration-150 hover:bg-button-ghost-hover focus-visible:ring-2 focus-visible:ring-border-focus-ring"
    >
      {label}
      <RiArrowRightLine className="size-4" aria-hidden />
    </Link>
  );
}

/** A small round state glyph: a check, or a cross for a failure. */
function Tick({ failed }: { failed?: boolean }) {
  return (
    <span
      className={cx(
        "flex size-5 shrink-0 items-center justify-center rounded-full",
        failed ? "bg-status-rose-background text-status-rose-text" : "bg-status-lime-background text-status-lime-text",
      )}
      aria-hidden
    >
      {failed ? <RiCloseLine className="size-3.5" /> : <RiCheckLine className="size-3.5" />}
    </span>
  );
}

// ---- provider build ----

const PHASES = ["Research", "Spec", "Tests", "Live check", "Publish"];
/** Shorter labels for phone widths. */
const PHASES_SHORT = ["Research", "Spec", "Tests", "Live", "Publish"];

/**
 * A provider build the assistant started. While it runs: the phase rail,
 * the live step and the elapsed time, refreshed by the thread poll (the
 * build finishes itself after a reload). Once done: one line with the result.
 */
export function BuildCard({ item }: { item: ChatItem }) {
  const b = item.build;
  if (!b) return null;
  const link = item.link ?? `/providers/ai/${b.run_id}`;
  const done = b.status === "published" || b.status === "needs_review";
  const failed = b.status === "failed" || b.status === "cancelled";
  if (done || failed) {
    const p = item.card?.provider;
    return (
      <div className={cx(CARD, "chat-reveal flex flex-wrap items-center gap-x-2.5 gap-y-1 px-4 py-2.5 text-body-2-regular")}>
        <Tick failed={failed} />
        <span className="min-w-0 flex-1">
          {b.status === "published" && p ? (
            <>
              <span className="text-body-2-medium text-text-primary">
                Published <EntityChip e={{ type: "provider", slug: p.slug, name: p.name }} />
              </span>
              <span className="text-text-secondary">
                {" "}
                v{p.version || 1}
                <span className="mx-1.5 text-text-tertiary" aria-hidden>
                  ·
                </span>
                <RiFlaskLine className="me-1 inline size-3.5 align-[-2px] text-foreground-icon-tertiary" aria-hidden />
                {p.tests_passed}/{p.tests_passed + p.tests_failed} tests pass
              </span>
              {item.card?.service && (
                <span className="text-text-secondary">
                  <span className="mx-1.5 text-text-tertiary" aria-hidden>
                    ·
                  </span>
                  service <EntityChip e={item.card.service} />
                </span>
              )}
            </>
          ) : b.status === "needs_review" ? (
            <span className="text-body-2-medium text-text-primary">{b.title}: ready for your review</span>
          ) : (
            <>
              <span className="text-body-2-medium text-text-error-primary">
                {b.title}: build {b.status}
              </span>
              {b.error && <span className="wrap-anywhere text-text-secondary"> {b.error}</span>}
            </>
          )}
          <span className="ms-2">
            <Elapsed from={b.started_at} to={b.finished_at} />
          </span>
        </span>
        <OpenRun to={link} label={b.status === "needs_review" ? "Review" : "Open run"} />
      </div>
    );
  }
  const phase = Math.max(0, Math.min(4, b.phase));
  return (
    <div role="group" aria-label={`Building ${b.title}`} className={cx(CARD, "px-4 pt-3.5 pb-3")}>
      <div className="flex items-center gap-2.5">
        <span className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-status-purple-background text-status-purple-text">
          <RiBox3Line className="size-4" aria-hidden />
        </span>
        <span className="min-w-0 flex-1 truncate text-body-medium text-text-primary">Building {b.title}</span>
        <Elapsed from={b.started_at} />
      </div>
      <div
        className="mt-3"
        role="progressbar"
        aria-label={`Building ${b.title}`}
        aria-valuemin={0}
        aria-valuemax={5}
        aria-valuenow={phase + 1}
        aria-valuetext={PHASES[phase]}
      >
        <ProgressTrack percentage={((phase + 1) / PHASES.length) * 100} size="sm" />
      </div>
      <ol className="mt-2 grid grid-cols-5 gap-1 font-mono text-caption-2-medium uppercase" aria-hidden>
        {PHASES.map((p, i) => (
          <li
            key={p}
            className={cx(
              "truncate transition-colors duration-300",
              i < phase ? "text-text-secondary" : i === phase ? "text-text-primary" : "text-text-tertiary",
              i === 4 && "text-end",
              i > 0 && i < 4 && "text-center",
            )}
          >
            <span className="sm:hidden">{PHASES_SHORT[i]}</span>
            <span className="hidden sm:inline">{p}</span>
          </li>
        ))}
      </ol>
      <div className="mt-2.5 flex items-center gap-3 text-body-2-regular">
        <span className="min-w-0 flex-1 truncate text-text-secondary" aria-live="polite">
          <SwapText text={b.step || "Working"} />
        </span>
        <OpenRun to={link} />
      </div>
    </div>
  );
}

// ---- ask_user ----

/**
 * A question the assistant asked with ask_user: a names form (one field per
 * pasted key) or a row of choices. Disabled while a turn is busy; answered
 * and expired questions fold into one line.
 */
export function AskCard({
  item,
  busy,
  onAnswer,
}: {
  item: ChatItem;
  busy: boolean;
  onAnswer: (body: ChatAnswer) => Promise<void>;
}) {
  const a = item.ask;
  const [names, setNames] = useState<Record<string, string>>(() =>
    Object.fromEntries((a?.rows ?? []).map((r) => [r.ref, r.suggestion])),
  );
  const [which, setWhich] = useState<string | null>(null);
  if (!a) return null;
  const waiting = a.status === "waiting";
  const run = async (key: string, body: ChatAnswer) => {
    setWhich(key);
    try {
      await onAnswer(body);
    } finally {
      setWhich(null);
    }
  };
  if (!waiting) {
    return (
      <div className={cx(CARD, "chat-reveal flex items-center gap-2.5 px-4 py-2.5 text-body-2-regular text-text-secondary")}>
        {a.status === "answered" ? (
          <RiCheckLine className="size-4 shrink-0 text-foreground-icon-primary" aria-hidden />
        ) : (
          <RiTimeLine className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
        )}
        <span className="min-w-0 flex-1 truncate">
          {a.question}
          <span className="text-text-tertiary"> · </span>
          {a.status === "answered" ? <span className="text-text-primary">{a.answer}</span> : "Expired"}
        </span>
      </div>
    );
  }
  const submit = (e: FormEvent) => {
    e.preventDefault();
    void run("names", { ask_id: a.id, names });
  };
  return (
    <div role="group" aria-label="Question" className={CARD}>
      <div className="px-4 pt-3.5 pb-2">
        <div className="text-caption-2-semibold text-text-tertiary uppercase">Your call</div>
        <div className="mt-1.5 text-body-regular text-text-primary">{a.question}</div>
      </div>
      {a.kind === "names" ? (
        <form onSubmit={submit} className="px-4 pb-3.5">
          <ul className="flex max-h-72 flex-col gap-2 overflow-y-auto py-1">
            {(a.rows ?? []).map((r) => (
              <li key={r.ref} className="flex items-center gap-2.5">
                <RiKey2Line className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
                <span className="w-16 shrink-0 font-mono text-caption-1-regular text-text-secondary" title={`Key ${r.ref}`}>
                  {r.hint.replace(/^\.\.\./, "…")}
                </span>
                <Input
                  size="small"
                  className="min-w-0 flex-1"
                  aria-label={`Name for the key ending ${r.hint.slice(-4)}`}
                  value={names[r.ref] ?? ""}
                  maxLength={64}
                  placeholder="Name"
                  isDisabled={busy}
                  onChange={(v) => setNames({ ...names, [r.ref]: v })}
                />
              </li>
            ))}
          </ul>
          <div className="mt-3 flex flex-col-reverse gap-2 sm:flex-row sm:items-center sm:justify-end">
            <Button
              size="small"
              variant="secondary"
              type="button"
              leadingIcon={which === "auto" ? ButtonSpinner : RiSparkling2Line}
              disabled={busy}
              onClick={() => void run("auto", { ask_id: a.id, auto: true })}
            >
              Pick names for me
            </Button>
            <Button
              size="small"
              type="submit"
              variant="primary"
              leadingIcon={which === "names" ? ButtonSpinner : undefined}
              disabled={busy || !Object.values(names).some((n) => n.trim())}
            >
              Use these names
            </Button>
          </div>
        </form>
      ) : (
        <div className="flex flex-wrap gap-2 px-4 pt-1 pb-3.5">
          {(a.choices ?? []).map((c) => (
            <Button
              key={c}
              size="small"
              variant="secondary"
              leadingIcon={which === c ? ButtonSpinner : undefined}
              disabled={busy}
              onClick={() => void run(c, { ask_id: a.id, choice: c })}
            >
              {c}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
}

// ---- tool result cards ----

const FRESH_MS = 2 * 60_000;

/** Keys a tool just added, with live status pills while their first health checks run. */
function KeysCard({ card, ts }: { card: ChatCard; ts: string }) {
  const svc = card.service;
  const ids = useMemo(() => new Set((card.keys ?? []).map((k) => k.id)), [card.keys]);
  const [live, setLive] = useState<Map<number, Key>>(new Map());
  const [checking, setChecking] = useState(false);
  const now = useNow(1000);
  useEffect(() => {
    if (!svc?.id || !ids.size) return;
    const started = Date.parse(ts);
    // Only a fresh card refreshes, every 4s for up to a minute after the keys were added.
    if (Number.isNaN(started) || Date.now() - started > FRESH_MS) {
      api
        .listKeys(svc.id)
        .then((r) => setLive(new Map(r.keys.filter((k) => ids.has(k.id)).map((k) => [k.id, k]))))
        .catch(() => {});
      return;
    }
    let stop = false;
    let timer = 0;
    const until = Math.max(Date.now(), started) + 60_000;
    const tick = async () => {
      try {
        const r = await api.listKeys(svc.id as number);
        if (stop) return;
        const mine = r.keys.filter((k) => ids.has(k.id));
        setLive(new Map(mine.map((k) => [k.id, k])));
        const pending = mine.some((k) => k.validity === "unknown" && !k.last_checked_at);
        setChecking(pending && Date.now() < until);
        if (!pending || Date.now() >= until) return;
      } catch {
        if (stop) return;
      }
      timer = window.setTimeout(tick, 4000);
    };
    setChecking(true);
    void tick();
    return () => {
      stop = true;
      window.clearTimeout(timer);
    };
  }, [svc?.id, ids, ts]);
  const shown = (card.keys ?? []).slice(0, 8);
  const more = (card.key_total ?? shown.length) - shown.length;
  return (
    <div className={cx(CARD, "overflow-hidden")}>
      <div className="flex items-center gap-2.5 px-4 pt-3 pb-2.5">
        <span className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-status-purple-background text-status-purple-text">
          <RiKey2Line className="size-4" aria-hidden />
        </span>
        <span className="min-w-0 flex-1 text-body-2-regular text-text-primary">
          <span className="text-body-2-medium">
            {card.key_total ?? 0} {card.key_total === 1 ? "key" : "keys"} added
          </span>
          {svc && (
            <>
              {" "}
              to <EntityChip e={svc} />
            </>
          )}
          {!!card.skipped && <span className="text-text-secondary">, {card.skipped} already there</span>}
        </span>
        {checking && (
          <span role="status" className="inline-flex items-center gap-1.5 font-mono text-caption-1-regular text-text-tertiary">
            <SpinnerGlyph size="xs" />
            checking
          </span>
        )}
      </div>
      {shown.length > 0 && (
        <ul className="divide-y divide-separator-border border-t border-separator-border">
          {shown.map((k) => {
            const lk = live.get(k.id);
            return (
              <li key={k.id} className="flex items-center gap-3 px-4 py-2 text-body-2-regular">
                <Link
                  to={`/keys?q=${encodeURIComponent(k.label)}`}
                  className="min-w-0 flex-1 truncate rounded-sm text-body-2-medium text-text-primary underline-offset-4 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                >
                  {k.label}
                </Link>
                <span className="shrink-0 font-mono text-caption-1-regular text-text-secondary">{k.hint.replace(/^\.\.\./, "…")}</span>
                <span className="flex w-28 shrink-0 justify-end" aria-live="polite">
                  {lk ? (
                    <span key={`${lk.effective_status}-${lk.validity}`} className="chat-swap inline-flex">
                      {lk.validity === "invalid" ? (
                        <Chip variant="caption" color="rose" className="py-0.5 font-mono">
                          invalid
                        </Chip>
                      ) : (
                        <StatusPill k={lk} now={now} />
                      )}
                    </span>
                  ) : (
                    <span className="font-mono text-caption-1-regular text-text-tertiary">{k.status || "added"}</span>
                  )}
                </span>
              </li>
            );
          })}
        </ul>
      )}
      {more > 0 && svc?.id && (
        <div className="border-t border-separator-border px-4 py-2 text-body-2-regular text-text-secondary">
          and {more} more.{" "}
          <Link
            to={`/services/${svc.id}`}
            className="rounded-sm text-body-2-medium text-text-primary underline-offset-4 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          >
            See all in {svc.name}
          </Link>
        </div>
      )}
    </div>
  );
}

function ServiceCard({ card }: { card: ChatCard }) {
  const svc = card.service;
  const [full, setFull] = useState<Service | null>(null);
  useEffect(() => {
    if (!svc?.id) return;
    api.getService(svc.id).then(setFull).catch(() => {});
  }, [svc?.id]);
  if (!svc) return null;
  const counts = full?.key_counts;
  return (
    <div className={cx(CARD, "flex items-center gap-3 px-4 py-3")}>
      <EntityLogo src={full?.logo_url} name={svc.name} size="md" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-body-medium text-text-primary">{svc.name}</div>
        <div className="truncate font-mono text-caption-1-regular text-text-secondary">
          /proxy/{svc.slug}
          {counts && (
            <>
              <span className="mx-1.5 text-text-tertiary" aria-hidden>
                ·
              </span>
              {counts.total} {counts.total === 1 ? "key" : "keys"}
              {counts.total > 0 && `, ${counts.available} available`}
            </>
          )}
        </div>
      </div>
      {svc.id && <OpenRun to={`/services/${svc.id}`} label="Open" />}
    </div>
  );
}

/** Cards for the tool rows of a turn that have one. */
export function ToolCards({ items }: { items: ChatItem[] }) {
  const cards = items.filter((it) => it.card && (it.card.type === "keys" || it.card.type === "service"));
  if (!cards.length) return null;
  return (
    <div className="mt-2.5 flex flex-col gap-2.5">
      {cards.map((it) => (
        <div key={it.id} className="fade-up">
          {it.card?.type === "keys" ? <KeysCard card={it.card} ts={it.ts} /> : <ServiceCard card={it.card as ChatCard} />}
        </div>
      ))}
    </div>
  );
}

// ---- suggestions ----

/** Follow ups the assistant offered at the end of a turn. Picking one sends it. */
export function Suggestions({
  items,
  disabled,
  onPick,
}: {
  items: string[];
  disabled: boolean;
  onPick: (text: string) => void;
}) {
  if (!items.length) return null;
  return (
    <div className={cx("mt-3 flex flex-wrap gap-2", "ps-9")} role="group" aria-label="Suggested follow ups">
      {items.map((s, i) => (
        <Button
          key={s}
          size="small"
          variant="secondary"
          trailingIcon={RiArrowRightLine}
          disabled={disabled}
          onClick={() => onPick(s)}
          className="fade-up h-auto min-h-8 rounded-full px-3 whitespace-normal text-start"
          style={{ animationDelay: `${120 + i * 60}ms` }}
        >
          {s}
        </Button>
      ))}
    </div>
  );
}
