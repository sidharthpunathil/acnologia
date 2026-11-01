import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router-dom";
import {
  ArrowRight,
  Boxes,
  Check,
  CircleDashed,
  Clock,
  FlaskConical,
  KeyRound,
  Sparkles,
  X,
} from "lucide-react";
import { api } from "../../lib/api";
import type { ChatAnswer, ChatCard, ChatEntity, ChatItem, Key, Service } from "../../lib/types";
import { useNow } from "../../hooks/useNow";
import { EntityLogo } from "../../components/EntityLogo";
import { StatusPill } from "../../components/StatusPill";
import { Button, cx } from "../../components/ui";

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

export function EntityChip({ e, children }: { e: ChatEntity; children?: ReactNode }) {
  const to = entityPath(e);
  const label = children ?? e.name;
  if (!to) return <>{label}</>;
  return (
    <Link
      to={to}
      title={`Open ${e.type} ${e.name}`}
      className="chat-chip press mx-px inline-flex max-w-full items-baseline gap-1 rounded-md bg-ink/[0.06] px-1.5 align-baseline font-medium text-ink transition-[background-color,transform] duration-150 hover:bg-ink/[0.11] focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ink/30"
    >
      <span className="truncate">{label}</span>
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
export function linkify(text: string, entities: Map<string, ChatEntity> | undefined, render: (s: string, key: number) => ReactNode): ReactNode {
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
    out.push(e ? <EntityChip key={`e${n++}`} e={e}>{m[1]}</EntityChip> : render(m[1], n++));
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
  return <span className="font-mono text-[12px] tabular-nums text-faint">{elapsed(from, Number.isNaN(end) ? now : end)}</span>;
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
      className="press tap-inline inline-flex shrink-0 items-center gap-1 rounded-md text-[12.5px] font-medium text-ink underline-offset-4 hover:underline"
    >
      {label} <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
    </Link>
  );
}

// ---- provider build ----

const PHASES = ["Research", "Spec", "Tests", "Live check", "Publish"];
/** Shorter labels for phone widths. */
const PHASES_SHORT = ["Research", "Spec", "Tests", "Live", "Publish"];

export function BuildCard({ item }: { item: ChatItem }) {
  const b = item.build;
  if (!b) return null;
  const link = item.link ?? `/providers/ai/${b.run_id}`;
  const done = b.status === "published" || b.status === "needs_review";
  const failed = b.status === "failed" || b.status === "cancelled";
  if (done || failed) {
    const p = item.card?.provider;
    return (
      <div className="chat-reveal flex flex-wrap items-center gap-x-2 gap-y-1 rounded-xl border border-line bg-surface px-3.5 py-2.5 text-[13px] shadow-card">
        <span
          className={cx(
            "flex h-4 w-4 shrink-0 items-center justify-center rounded-full",
            failed ? "bg-red-600 text-white" : "bg-ink text-on-ink",
          )}
          aria-hidden="true"
        >
          {failed ? <X className="h-2.5 w-2.5" strokeWidth={3} /> : <Check className="h-2.5 w-2.5" strokeWidth={3} />}
        </span>
        <span className="min-w-0 flex-1">
          {b.status === "published" && p ? (
            <>
              <span className="font-medium text-ink">
                Published <EntityChip e={{ type: "provider", slug: p.slug, name: p.name }} />
              </span>
              <span className="text-muted">
                {" "}
                v{p.version || 1}
                <span className="mx-1.5 text-faint" aria-hidden="true">·</span>
                <FlaskConical className="mr-1 inline h-3.5 w-3.5 align-[-2px] text-faint" aria-hidden="true" />
                {p.tests_passed}/{p.tests_passed + p.tests_failed} tests pass
              </span>
              {item.card?.service && (
                <span className="text-muted">
                  <span className="mx-1.5 text-faint" aria-hidden="true">·</span>
                  service <EntityChip e={item.card.service} />
                </span>
              )}
            </>
          ) : b.status === "needs_review" ? (
            <span className="font-medium text-ink">{b.title}: ready for your review</span>
          ) : (
            <>
              <span className="font-medium text-red-700">{b.title}: build {b.status}</span>
              {b.error && <span className="wrap-anywhere text-muted"> {b.error}</span>}
            </>
          )}
          <span className="ml-2">
            <Elapsed from={b.started_at} to={b.finished_at} />
          </span>
        </span>
        <OpenRun to={link} label={b.status === "needs_review" ? "Review" : "Open run"} />
      </div>
    );
  }
  const phase = Math.max(0, Math.min(4, b.phase));
  return (
    <div
      role="group"
      aria-label={`Building ${b.title}`}
      className="rounded-2xl border border-line bg-surface px-4 pb-3 pt-3.5 shadow-card"
    >
      <div className="flex items-center gap-2">
        <Boxes className="h-4 w-4 shrink-0 text-brand" aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate text-[13.5px] font-medium text-ink">Building {b.title}</span>
        <Elapsed from={b.started_at} />
      </div>
      <div
        className="relative mt-3 h-[3px] overflow-hidden rounded-full bg-well"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={5}
        aria-valuenow={phase + 1}
        aria-valuetext={PHASES[phase]}
      >
        <div
          className="chat-progress absolute inset-y-0 left-0 rounded-full bg-ink"
          style={{ width: `${((phase + 1) / PHASES.length) * 100}%` }}
        />
      </div>
      <ol className="mt-1.5 grid grid-cols-5 gap-1 font-mono text-[10.5px] uppercase tracking-wide">
        {PHASES.map((p, i) => (
          <li
            key={p}
            className={cx(
              "truncate transition-colors duration-300",
              i < phase ? "text-muted" : i === phase ? "text-ink" : "text-faint",
              i === 4 && "text-right",
              i > 0 && i < 4 && "text-center",
            )}
          >
            <span className="sm:hidden">{PHASES_SHORT[i]}</span>
            <span className="hidden sm:inline">{p}</span>
          </li>
        ))}
      </ol>
      <div className="mt-2.5 flex items-center gap-3 text-[13px]">
        <span className="min-w-0 flex-1 truncate text-muted" aria-live="polite">
          <SwapText text={b.step || "Working"} />
        </span>
        <OpenRun to={link} />
      </div>
    </div>
  );
}

// ---- ask_user ----

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
  const first = useRef<HTMLInputElement>(null);
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
      <div className="chat-reveal flex items-center gap-2 rounded-xl border border-line bg-surface px-3.5 py-2.5 text-[13px] text-muted shadow-card">
        {a.status === "answered" ? (
          <Check className="h-3.5 w-3.5 shrink-0 text-ink" aria-hidden="true" />
        ) : (
          <Clock className="h-3.5 w-3.5 shrink-0 text-faint" aria-hidden="true" />
        )}
        <span className="min-w-0 flex-1 truncate">
          {a.question}
          <span className="text-faint"> · </span>
          {a.status === "answered" ? <span className="text-ink">{a.answer}</span> : "Expired"}
        </span>
      </div>
    );
  }
  const submit = (e: FormEvent) => {
    e.preventDefault();
    void run("names", { ask_id: a.id, names });
  };
  return (
    <div role="group" aria-label="Question" className="rounded-2xl border border-line bg-surface shadow-card">
      <div className="px-4 pb-2 pt-3.5">
        <div className="font-mono text-[11px] uppercase tracking-wide text-faint">Your call</div>
        <div className="mt-1.5 text-[14px] leading-relaxed text-ink">{a.question}</div>
      </div>
      {a.kind === "names" ? (
        <form onSubmit={submit} className="px-4 pb-3.5">
          <ul className="max-h-72 space-y-1.5 overflow-y-auto py-1">
            {(a.rows ?? []).map((r, i) => (
              <li key={r.ref} className="flex items-center gap-2.5">
                <KeyRound className="h-3.5 w-3.5 shrink-0 text-faint" aria-hidden="true" />
                <span className="w-16 shrink-0 font-mono text-[12px] text-muted" title={`Key ${r.ref}`}>
                  {r.hint.replace(/^\.\.\./, "…")}
                </span>
                <label className="sr-only" htmlFor={`ask-${a.id}-${r.ref}`}>
                  Name for the key ending {r.hint.slice(-4)}
                </label>
                <input
                  id={`ask-${a.id}-${r.ref}`}
                  ref={i === 0 ? first : undefined}
                  value={names[r.ref] ?? ""}
                  maxLength={64}
                  placeholder="Name"
                  onChange={(e) => setNames({ ...names, [r.ref]: e.target.value })}
                  className="h-8 min-w-0 flex-1 rounded-lg border border-line bg-canvas px-2.5 text-[13.5px] text-ink transition-[border-color] duration-150 placeholder:text-faint focus:border-line-strong focus:outline-hidden"
                />
              </li>
            ))}
          </ul>
          <div className="mt-3 flex flex-wrap items-center justify-end gap-2">
            <Button
              size="sm"
              variant="ghost"
              type="button"
              className="press flex-1 sm:flex-none"
              icon={<Sparkles className="h-3.5 w-3.5" aria-hidden="true" />}
              loading={which === "auto"}
              disabled={busy}
              onClick={() => void run("auto", { ask_id: a.id, auto: true })}
            >
              Pick names for me
            </Button>
            <Button
              size="sm"
              type="submit"
              variant="primary"
              className="press flex-1 sm:flex-none"
              loading={which === "names"}
              disabled={busy || !Object.values(names).some((n) => n.trim())}
            >
              Use these names
            </Button>
          </div>
        </form>
      ) : (
        <div className="flex flex-wrap gap-2 px-4 pb-3.5 pt-1">
          {(a.choices ?? []).map((c) => (
            <Button
              key={c}
              size="sm"
              variant="secondary"
              className="press"
              loading={which === c}
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
      api.listKeys(svc.id).then((r) => setLive(new Map(r.keys.filter((k) => ids.has(k.id)).map((k) => [k.id, k])))).catch(() => {});
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
    <div className="rounded-2xl border border-line bg-surface shadow-card">
      <div className="flex items-center gap-2 px-4 pb-2 pt-3">
        <KeyRound className="h-4 w-4 shrink-0 text-brand" aria-hidden="true" />
        <span className="min-w-0 flex-1 text-[13.5px] text-ink">
          <span className="font-medium">
            {card.key_total ?? 0} {card.key_total === 1 ? "key" : "keys"} added
          </span>
          {svc && (
            <>
              {" "}
              to <EntityChip e={svc} />
            </>
          )}
          {!!card.skipped && <span className="text-muted"> · {card.skipped} already there</span>}
        </span>
        {checking && (
          <span className="inline-flex items-center gap-1 font-mono text-[11px] text-faint">
            <CircleDashed className="chat-spin h-3 w-3" aria-hidden="true" />
            checking
          </span>
        )}
      </div>
      {shown.length > 0 && (
        <ul className="divide-y divide-line border-t border-line">
          {shown.map((k) => {
            const lk = live.get(k.id);
            return (
              <li key={k.id} className="flex items-center gap-3 px-4 py-2 text-[13px]">
                <Link
                  to={`/keys?q=${encodeURIComponent(k.label)}`}
                  className="min-w-0 flex-1 truncate font-medium text-ink underline-offset-4 hover:underline"
                >
                  {k.label}
                </Link>
                <span className="shrink-0 font-mono text-[12px] text-muted">{k.hint.replace(/^\.\.\./, "…")}</span>
                <span className="w-28 shrink-0 text-right">
                  {lk ? (
                    <span key={`${lk.effective_status}-${lk.validity}`} className="chat-swap inline-flex">
                      {lk.validity === "invalid" ? (
                        <span className="inline-flex items-center gap-1.5 rounded-full bg-red-50 px-2 py-0.5 font-mono text-[11px] text-red-700">
                          <span className="h-1.5 w-1.5 rounded-full bg-red-500" aria-hidden="true" />
                          invalid
                        </span>
                      ) : (
                        <StatusPill k={lk} now={now} />
                      )}
                    </span>
                  ) : (
                    <span className="font-mono text-[11px] text-faint">{k.status || "added"}</span>
                  )}
                </span>
              </li>
            );
          })}
        </ul>
      )}
      {more > 0 && svc?.id && (
        <div className="border-t border-line px-4 py-2 text-[12.5px] text-muted">
          and {more} more.{" "}
          <Link to={`/services/${svc.id}`} className="font-medium text-ink underline-offset-4 hover:underline">
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
    <div className="flex items-center gap-3 rounded-2xl border border-line bg-surface px-4 py-3 shadow-card">
      <EntityLogo src={full?.logo_url} name={svc.name} size="md" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-[13.5px] font-medium text-ink">{svc.name}</div>
        <div className="truncate font-mono text-[11.5px] text-muted">
          /proxy/{svc.slug}
          {counts && (
            <>
              <span className="mx-1.5 text-faint" aria-hidden="true">·</span>
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
    <div className="mt-2.5 space-y-2.5">
      {cards.map((it) =>
        it.card?.type === "keys" ? (
          <div key={it.id} className="fade-up">
            <KeysCard card={it.card} ts={it.ts} />
          </div>
        ) : (
          <div key={it.id} className="fade-up">
            <ServiceCard card={it.card as ChatCard} />
          </div>
        ),
      )}
    </div>
  );
}

// ---- suggestions ----

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
    <div className="mt-3 flex flex-wrap gap-2 pl-9" role="group" aria-label="Suggested follow ups">
      {items.map((s, i) => (
        <button
          key={s}
          type="button"
          disabled={disabled}
          onClick={() => onPick(s)}
          className="press fade-up tap inline-flex items-center gap-1.5 rounded-full border border-line bg-surface px-3 py-1.5 text-[13px] text-ink shadow-card transition-[border-color,background-color,transform] duration-150 ease-out hover:border-line-strong hover:bg-soft disabled:cursor-not-allowed disabled:opacity-50"
          style={{ animationDelay: `${120 + i * 60}ms` }}
        >
          {s}
          <ArrowRight className="h-3.5 w-3.5 text-faint" aria-hidden="true" />
        </button>
      ))}
    </div>
  );
}

