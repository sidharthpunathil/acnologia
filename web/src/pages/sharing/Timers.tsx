// Share timers: when a share ends, what happens then, a live countdown and
// the Extend popover. Shared by both share modes.
import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { Radio as AriaRadio, RadioGroup as AriaRadioGroup } from "react-aria-components";
import { RiCheckLine, RiTimerLine } from "@remixicon/react";
import type { EndAction } from "../../lib/sharing";
import { dateTime } from "../../lib/format";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { RadioGroup } from "@/components/base/radio/radio";
import { RadioCard } from "@/components/base/radio/radio-card";
import { Popover, PopoverFooter, PopoverHeader, PopoverTrigger } from "@/components/base/popover/popover";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { cx } from "@/utils/cx";

// ---- reduced motion ----

const REDUCE = "(prefers-reduced-motion: reduce)";

function subscribeReduce(cb: () => void) {
  const mq = window.matchMedia(REDUCE);
  mq.addEventListener("change", cb);
  return () => mq.removeEventListener("change", cb);
}

/** True when the viewer asked for less motion. Countdowns then tick once a minute. */
export function usePrefersReducedMotion(): boolean {
  return useSyncExternalStore(
    subscribeReduce,
    () => window.matchMedia(REDUCE).matches,
    () => false,
  );
}

// ---- countdown ----

const HOUR = 3600e3;
const DAY = 86400e3;

/** "3d 4h", "5h 12m", "12m 05s" (or "13m" without seconds). */
export function formatLeft(ms: number, seconds: boolean): string {
  if (ms >= DAY) return `${Math.floor(ms / DAY)}d ${Math.floor((ms % DAY) / HOUR)}h`;
  if (ms >= HOUR) return `${Math.floor(ms / HOUR)}h ${String(Math.floor((ms % HOUR) / 60e3)).padStart(2, "0")}m`;
  if (seconds) {
    const s = Math.ceil(ms / 1000);
    return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
  }
  return `${Math.max(1, Math.ceil(ms / 60e3))}m`;
}

/**
 * The current time for a countdown to `target`: ticks every minute, every
 * second in the last hour (every minute still under reduced motion), and
 * stops once the target has passed.
 */
export function useCountdownNow(target: number | null): number {
  const reduce = usePrefersReducedMotion();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (target === null) return;
    let t = 0;
    const tick = () => {
      const n = Date.now();
      setNow(n);
      const left = target - n;
      if (left <= 0) return;
      const step = left < HOUR && !reduce ? 1000 - (n % 1000) : 60e3 - (n % 60e3);
      t = window.setTimeout(tick, Math.max(50, Math.min(step, left + 50)));
    };
    tick();
    return () => window.clearTimeout(t);
  }, [target, reduce]);
  return now;
}

/**
 * "ends in 5h 12m", ticking in place. The number sits in a fixed width,
 * tabular box so nothing around it shifts. Screen readers hear one polite
 * status when it ends, never each tick.
 */
export function Countdown({
  iso,
  prefix = "ends in",
  ended = "ended",
  subject,
  className,
}: {
  iso: string | null | undefined;
  prefix?: string;
  ended?: string;
  /** Names the thing for the ended announcement, such as the share name. */
  subject?: string;
  className?: string;
}) {
  const reduce = usePrefersReducedMotion();
  const t = iso ? Date.parse(iso) : NaN;
  const target = Number.isNaN(t) ? null : t;
  const now = useCountdownNow(target);
  const left = target === null ? 0 : target - now;
  const done = target !== null && left <= 0;
  // Announce only a change to ended that happens while the row is on screen.
  const wasDone = useRef(done);
  const [announce, setAnnounce] = useState("");
  useEffect(() => {
    if (done && !wasDone.current) setAnnounce(`${subject ? `${subject} ` : ""}${ended}`);
    wasDone.current = done;
  }, [done, ended, subject]);

  if (target === null) return null;
  return (
    <span className={cx("inline-flex items-baseline gap-1", className)} title={dateTime(iso)}>
      {done ? (
        <span>{ended}</span>
      ) : (
        <>
          <span>{prefix}</span>
          <span className="inline-block min-w-16 font-mono tabular-nums">{formatLeft(left, !reduce)}</span>
        </>
      )}
      <span role="status" aria-atomic="true" className="sr-only">
        {announce}
      </span>
    </span>
  );
}

// ---- ends picker ----

export type EndsPreset = "never" | "1h" | "24h" | "7d" | "30d" | "custom";

const PRESETS: { id: EndsPreset; label: string; ms?: number }[] = [
  { id: "never", label: "Never" },
  { id: "1h", label: "1 hour", ms: HOUR },
  { id: "24h", label: "24 hours", ms: DAY },
  { id: "7d", label: "7 days", ms: 7 * DAY },
  { id: "30d", label: "30 days", ms: 30 * DAY },
  { id: "custom", label: "Custom" },
];

export interface EndsDraft {
  preset: EndsPreset;
  /** Custom: local date "YYYY-MM-DD" and time "HH:MM". */
  date: string;
  time: string;
}

function pad(n: number) {
  return String(n).padStart(2, "0");
}

function localParts(ms: number): { date: string; time: string } {
  const d = new Date(ms);
  return { date: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`, time: `${pad(d.getHours())}:${pad(d.getMinutes())}` };
}

/** A draft for an existing end time: Never, or Custom filled with it. */
export function endsDraftOf(iso: string | null | undefined): EndsDraft {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return { preset: "never", ...localParts(Date.now() + DAY) };
  return { preset: "custom", ...localParts(t) };
}

/** RFC 3339, "" for never, or an error message. `from` is where quick choices count from. */
export function resolveEnds(d: EndsDraft, from = Date.now()): { iso: string } | { error: string } {
  if (d.preset === "never") return { iso: "" };
  if (d.preset !== "custom") return { iso: new Date(from + (PRESETS.find((p) => p.id === d.preset)?.ms ?? 0)).toISOString() };
  if (!d.date) return { error: "Pick the date the share ends." };
  const t = new Date(`${d.date}T${d.time || "23:59"}`).getTime();
  if (Number.isNaN(t)) return { error: "That end date and time is not valid." };
  if (t <= Date.now()) return { error: "Pick an end time in the future." };
  return { iso: new Date(t).toISOString() };
}

/** A row of single choice pills, a radio group underneath (arrow keys move, Space selects). */
export function ChoicePills<T extends string>({
  label,
  value,
  options,
  onChange,
  isDisabled,
}: {
  label: string;
  value: T;
  options: { id: T; label: string }[];
  onChange: (v: T) => void;
  isDisabled?: boolean;
}) {
  return (
    <AriaRadioGroup aria-label={label} orientation="horizontal" value={value} onChange={(v) => onChange(v as T)} isDisabled={isDisabled} className="flex flex-wrap gap-1.5">
      {options.map((o) => (
        <AriaRadio
          key={o.id}
          value={o.id}
          className={({ isSelected, isFocusVisible, isDisabled: off }) =>
            cx(
              "inline-flex min-h-8 cursor-pointer items-center gap-1 rounded-full border px-3 py-1 text-caption-1-medium outline-none select-none",
              "transition-colors duration-150 ease-out pointer-coarse:min-h-11",
              isFocusVisible && "ring-2 ring-border-focus-ring",
              off && "cursor-not-allowed opacity-60",
              isSelected
                ? "border-accent-line bg-accent-subtle text-accent-fg"
                : "border-border-button-default bg-background-primary-default text-text-primary hover:border-border-button-hover",
            )
          }
        >
          {({ isSelected }) => (
            <>
              {isSelected && <RiCheckLine className="size-3.5" aria-hidden />}
              {o.label}
            </>
          )}
        </AriaRadio>
      ))}
    </AriaRadioGroup>
  );
}

/** Quick choices for when a share ends, with a custom date and time. */
export function EndsPicker({
  value,
  onChange,
  label = "Ends",
  from,
  compact,
}: {
  value: EndsDraft;
  onChange: (d: EndsDraft) => void;
  label?: string;
  /** Quick choices count from here (now by default). */
  from?: number;
  /** Tighter layout for a popover. */
  compact?: boolean;
}) {
  const r = resolveEnds(value, from);
  let preview: ReactNode = null;
  if (value.preset === "never") preview = "Keeps working until you pause or revoke it.";
  else if ("iso" in r && r.iso) preview = <>Ends {dateTime(r.iso)}, your time.</>;
  else if ("error" in r && value.preset === "custom") preview = <span className="text-text-error-primary">{r.error}</span>;
  return (
    <div className="flex flex-col gap-2">
      <div className="text-body-medium text-text-primary">{label}</div>
      <ChoicePills label={label} value={value.preset} options={PRESETS} onChange={(preset) => onChange({ ...value, preset })} />
      {value.preset === "custom" && (
        <div className={cx("grid grid-cols-2 gap-2", !compact && "sm:max-w-sm")}>
          <Input label="Date" type="date" size="small" value={value.date} onChange={(date) => onChange({ ...value, date })} />
          <Input label="Time" type="time" size="small" value={value.time} onChange={(time) => onChange({ ...value, time })} />
        </div>
      )}
      {preview && (
        <p aria-live="polite" className="text-caption-1-regular text-text-secondary">
          {preview}
        </p>
      )}
    </div>
  );
}

// ---- end action ----

export const END_ACTION_LABEL: Record<EndAction, string> = { expire: "expires", revoke: "revokes for good" };

export function EndActionPicker({
  value,
  onChange,
  handover,
  person,
}: {
  value: EndAction;
  onChange: (v: EndAction) => void;
  handover: boolean;
  /** A person share: a claim link and a personal token instead of an invite. */
  person?: boolean;
}) {
  const cards: { id: EndAction; title: string; body: string }[] = [
    { id: "expire", title: "Expire", body: "The share stops working. You can extend it later to turn it back on." },
    {
      id: "revoke",
      title: "Revoke for good",
      body: handover
        ? "The invite and token die, the handed over keys are wiped and a delete signal is sent."
        : person
          ? "The link and their token die. It cannot be extended or resumed."
          : "The invite and token die. It cannot be extended or resumed.",
    },
  ];
  return (
    <div className="flex flex-col gap-2">
      <div id="share-end-action-label" className="text-body-medium text-text-primary">
        When it ends
      </div>
      <RadioGroup aria-labelledby="share-end-action-label" value={value} onChange={(v) => onChange(v as EndAction)} className="grid grid-cols-1 gap-2">
        {cards.map((c) => (
          <RadioCard
            key={c.id}
            value={c.id}
            title={c.title}
            description={<span className="mt-0.5 block whitespace-normal text-body-2-regular">{c.body}</span>}
            className={({ isSelected }) => cx("items-start rounded-2xl", isSelected && "border-accent-line ring-1 ring-accent-line")}
          />
        ))}
      </RadioGroup>
    </div>
  );
}

// ---- extend ----

/** Extend: a small popover with the same quick choices, counted from the later of now and the current end. */
export function ExtendButton({ name, endsAt, onExtend }: { name: string; endsAt: string | null; onExtend: (iso: string) => Promise<void> }) {
  const [open, setOpen] = useState(false);
  const [d, setD] = useState<EndsDraft>(() => ({ ...endsDraftOf(null), preset: "24h" }));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const cur = endsAt ? Date.parse(endsAt) : NaN;
  const from = Number.isNaN(cur) ? Date.now() : Math.max(Date.now(), cur);

  const openChange = (o: boolean) => {
    if (busy) return;
    if (o) {
      setD({ ...endsDraftOf(null), preset: "24h" });
      setErr(null);
    }
    setOpen(o);
  };

  const run = async () => {
    const r = resolveEnds(d, from);
    if ("error" in r) return setErr(r.error);
    setBusy(true);
    setErr(null);
    try {
      await onExtend(r.iso);
      setOpen(false);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <PopoverTrigger isOpen={open} onOpenChange={openChange}>
      <Button size="small" variant="ghost" leadingIcon={RiTimerLine}>
        Extend
      </Button>
      <Popover placement="bottom end" className="w-88">
        <PopoverHeader
          title={`Extend ${name}`}
          description={Number.isNaN(cur) || cur <= Date.now() ? "Counted from now. An expired share turns back on." : "Counted from its current end."}
        />
        <EndsPicker label="New end" value={d} onChange={setD} from={from} compact />
        {err && <p className="text-caption-1-regular text-text-error-primary">{err}</p>}
        <PopoverFooter>
          <Button variant="secondary" size="small" onClick={() => setOpen(false)} disabled={busy}>
            Cancel
          </Button>
          <Button variant="primary" size="small" onClick={() => void run()} disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
            Extend
          </Button>
        </PopoverFooter>
      </Popover>
    </PopoverTrigger>
  );
}
