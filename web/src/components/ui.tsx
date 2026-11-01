import { cloneElement, forwardRef, type KeyboardEvent, isValidElement, useId, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactElement, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { Loader2 } from "lucide-react";

export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md";

const variants: Record<Variant, string> = {
  primary: "bg-ink text-on-ink hover:bg-ink-strong disabled:bg-well disabled:text-faint",
  secondary: "bg-surface text-ink shadow-card hover:bg-soft disabled:text-faint",
  ghost: "text-muted hover:text-ink hover:bg-ink/[0.05] disabled:text-faint",
  danger: "bg-red-600 text-on-ink hover:bg-red-600/90 disabled:bg-well disabled:text-faint",
};

const sizes: Record<Size, string> = {
  sm: "h-8 px-3 text-[13px] gap-1.5",
  md: "h-10 px-4 text-sm gap-2",
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  icon?: ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", size = "md", loading, icon, className, children, disabled, type = "button", ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      disabled={disabled || loading}
      className={cx(
        "tap inline-flex shrink-0 cursor-pointer select-none items-center justify-center whitespace-nowrap rounded-full font-medium transition-[background-color,color,box-shadow,transform] duration-150 ease-out active:scale-[0.98] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ink focus-visible:ring-offset-2 focus-visible:ring-offset-canvas disabled:cursor-not-allowed disabled:active:scale-100",
        variants[variant],
        sizes[size],
        className,
      )}
      {...rest}
    >
      {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : icon}
      {children}
    </button>
  );
});

export function IconButton({
  label,
  className,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      className={cx(
        "icon-btn inline-flex h-8 w-8 items-center justify-center rounded-lg text-muted transition-[background-color,color,transform] duration-150 ease-out hover:bg-ink/[0.06] hover:text-ink active:scale-[0.94] disabled:cursor-not-allowed disabled:opacity-40 disabled:active:scale-100",
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  );
}

const fieldBase =
  "field-input w-full rounded-xl border border-line bg-surface px-3 text-sm text-ink placeholder:text-faint transition-colors focus:border-ink/60 focus:outline-none focus:ring-2 focus:ring-ink/20 disabled:bg-canvas disabled:text-muted";

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input(
  { className, ...rest },
  ref,
) {
  return (
    <input
      ref={ref}
      className={cx(fieldBase, "h-10", rest["aria-invalid"] ? "border-red-400 focus:border-red-500 focus:ring-red-500/15" : "", className)}
      {...rest}
    />
  );
});

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(function Textarea(
  { className, ...rest },
  ref,
) {
  return <textarea ref={ref} className={cx(fieldBase, "py-2.5", className)} {...rest} />;
});

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={cx(fieldBase, "h-10 appearance-none bg-[length:16px] pr-8", "select-chevron", className)} {...rest}>
      {children}
    </select>
  );
}

export function Field({
  label,
  hint,
  error,
  required,
  children,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: string | null;
  required?: boolean;
  children: ReactNode;
  className?: string;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errId = `${id}-err`;
  const describedBy = [hint ? hintId : "", error ? errId : ""].filter(Boolean).join(" ") || undefined;
  // Wire the control to its label, hint and error without each caller passing ids.
  const control = isValidElement(children) ? (children as ReactElement<Record<string, unknown>>) : null;
  const wired = control
    ? cloneElement(control, {
        id: (control.props as { id?: string }).id ?? id,
        "aria-describedby": describedBy,
        "aria-invalid": error ? true : (control.props as { "aria-invalid"?: boolean })["aria-invalid"],
      })
    : children;
  return (
    <div className={cx("block min-w-0", className)}>
      <label htmlFor={control ? ((control.props as { id?: string }).id ?? id) : undefined} className="mb-1.5 block text-[13px] font-medium text-ink">
        {label}
        {required && (
          <span className="ml-0.5 text-brand" aria-hidden="true">
            *
          </span>
        )}
      </label>
      {wired}
      {error ? (
        <span id={errId} role="alert" className="mt-1 block text-xs text-red-700">
          {error}
        </span>
      ) : (
        hint && (
          <span id={hintId} className="mt-1 block text-xs leading-relaxed text-muted">
            {hint}
          </span>
        )
      )}
    </div>
  );
}

export function Toggle({
  checked,
  onChange,
  label,
  description,
  disabled,
  ariaLabel,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label?: string;
  description?: string;
  disabled?: boolean;
  /** Accessible name when there is no visible label. */
  ariaLabel?: string;
}) {
  const sw = (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label ?? ariaLabel}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cx(
        "switch-hit relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full transition-colors disabled:cursor-not-allowed disabled:opacity-50",
        checked ? "bg-ink" : "bg-line-strong ring-1 ring-inset ring-faint",
      )}
    >
      <span
        className={cx(
          "inline-block h-4 w-4 rounded-full shadow transition-transform",
          // The knob contrasts with the track in both themes: surface on ink when on, white on gray when off.
          checked ? "bg-surface" : "bg-white",
          checked ? "translate-x-[18px]" : "translate-x-0.5",
        )}
      />
    </button>
  );
  if (!label) return sw;
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="min-w-0 cursor-pointer" onClick={() => !disabled && onChange(!checked)}>
        <div className="text-[13px] font-medium text-ink">{label}</div>
        {description && <div className="mt-0.5 text-xs leading-relaxed text-muted">{description}</div>}
      </div>
      {sw}
    </div>
  );
}

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cx("rounded-card bg-surface shadow-card", className)}>{children}</div>;
}

type BadgeTone = "neutral" | "green" | "amber" | "red" | "blue" | "violet" | "dark";

const tones: Record<BadgeTone, string> = {
  neutral: "bg-well text-muted",
  green: "bg-emerald-50 text-emerald-700",
  amber: "bg-amber-50 text-amber-700",
  red: "bg-red-50 text-red-700",
  blue: "bg-sky-50 text-sky-700",
  violet: "bg-violet-50 text-violet-700",
  dark: "bg-ink/[0.12] font-medium text-ink",
};

export function Badge({
  tone = "neutral",
  children,
  className,
  title,
}: {
  tone?: BadgeTone;
  children: ReactNode;
  className?: string;
  title?: string;
}) {
  return (
    <span
      title={title}
      className={cx(
        "inline-flex items-center gap-1 whitespace-nowrap rounded-md px-1.5 py-0.5 font-mono text-[11px] leading-4",
        tones[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

export function Meta({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cx("font-mono text-xs text-muted", className)}>{children}</div>;
}

export function Empty({
  icon,
  title,
  body,
  action,
  className,
}: {
  icon?: ReactNode;
  title: string;
  body?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("flex flex-col items-center rounded-card border border-dashed border-line-strong px-6 py-12 text-center", className)}>
      {icon && <div className="mb-3 flex h-10 w-10 items-center justify-center rounded-xl bg-surface text-muted shadow-card">{icon}</div>}
      <div className="text-[15px] font-medium text-ink">{title}</div>
      {body && <div className="mt-1 max-w-md text-sm text-muted">{body}</div>}
      {action && <div className="mt-5 flex flex-wrap justify-center gap-2">{action}</div>}
    </div>
  );
}

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cx("h-4 w-4 animate-spin text-muted", className)} />;
}

export function PageLoading({ label = "Loading" }: { label?: string }) {
  return (
    <div className="flex items-center justify-center py-24" role="status" aria-live="polite">
      <Spinner className="h-5 w-5" />
      <span className="sr-only">{label}</span>
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden="true" className={cx("skeleton rounded-lg", className)} />;
}

/** Placeholder for a page: header line, a row of tiles and a list or grid. */
export function PageSkeleton({ variant = "grid", tiles = 0, bare }: { variant?: "grid" | "list"; tiles?: number; bare?: boolean }) {
  return (
    <div role="status" aria-live="polite" aria-busy="true" className={bare ? "-mt-8" : undefined}>
      <span className="sr-only">Loading</span>
      {!bare && (
        <>
          <Skeleton className="h-8 w-64 max-w-full" />
          <Skeleton className="mt-3 h-3 w-48" />
        </>
      )}
      {tiles > 0 && (
        <div className="mt-8 grid grid-cols-2 gap-2.5 sm:grid-cols-3 lg:grid-cols-6">
          {Array.from({ length: tiles }).map((_, i) => (
            <Skeleton key={i} className="h-[66px] rounded-card" />
          ))}
        </div>
      )}
      {variant === "grid" ? (
        <div className="mt-10 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3.5 rounded-card bg-surface px-4 py-4 shadow-card">
              <Skeleton className="h-10 w-10 rounded-[11px]" />
              <div className="flex-1">
                <Skeleton className="h-4 w-1/2" />
                <Skeleton className="mt-2 h-3 w-4/5" />
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className="mt-8 space-y-2.5">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="rounded-card bg-surface p-4 shadow-card">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="mt-2 h-3 w-3/4" />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** A titled card section with optional actions, used for settings and editors. */
export function Section({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <section className={cx("rounded-card bg-surface p-5 shadow-card sm:p-6", className)}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <h2 className="text-[15px] font-semibold text-ink">{title}</h2>
          {description && <p className="mt-0.5 max-w-2xl text-[13px] leading-relaxed text-muted">{description}</p>}
        </div>
        {actions && <div className="flex shrink-0 flex-wrap gap-2">{actions}</div>}
      </div>
      {children && <div className="mt-5">{children}</div>}
    </section>
  );
}

export function Callout({
  tone = "neutral",
  icon,
  children,
  className,
}: {
  tone?: "neutral" | "amber" | "red" | "green";
  icon?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  const t = {
    neutral: "bg-soft text-muted border-line",
    amber: "bg-amber-50 text-amber-900 border-amber-200/70",
    red: "bg-red-50 text-red-800 border-red-200/70",
    green: "bg-emerald-50 text-emerald-900 border-emerald-200/70",
  }[tone];
  return (
    <div className={cx("flex items-start gap-2.5 rounded-xl border px-3.5 py-3 text-[13px] leading-relaxed", t, className)}>
      {icon && <span className="mt-0.5 shrink-0">{icon}</span>}
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}

export function PageHeader({
  title,
  meta,
  actions,
}: {
  title: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-8 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <h1 className="font-mono text-[26px] leading-tight tracking-tight text-ink sm:text-[30px]">{title}</h1>
        {meta && <Meta className="mt-2">{meta}</Meta>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

export function Stat({
  label,
  value,
  tone,
  sub,
}: {
  label: string;
  value: ReactNode;
  tone?: "green" | "amber" | "red" | "neutral" | "violet";
  sub?: ReactNode;
}) {
  const color =
    tone === "green"
      ? "text-emerald-700"
      : tone === "amber"
        ? "text-amber-700"
        : tone === "red"
          ? "text-red-700"
          : tone === "violet"
            ? "text-violet-700"
            : "text-ink";
  return (
    <div className="min-w-0 rounded-card bg-surface px-4 py-3 shadow-card">
      <div className="truncate font-mono text-[11px] uppercase tracking-wide text-faint">{label}</div>
      <div className={cx("mt-1 font-mono text-xl tabular-nums", color)}>{value}</div>
      {sub && <div className="mt-0.5 truncate font-mono text-[11px] text-faint">{sub}</div>}
    </div>
  );
}

export interface SegmentItem<T extends string> {
  id: T;
  label: string;
  icon?: ReactNode;
}

/** Segmented control with radio semantics. Arrow keys move the selection. */
export function Segmented<T extends string>({
  items,
  value,
  onChange,
  label,
  className,
  size = "md",
}: {
  items: SegmentItem<T>[];
  value: T;
  onChange: (v: T) => void;
  label: string;
  className?: string;
  size?: "sm" | "md";
}) {
  const onKey = (e: KeyboardEvent<HTMLButtonElement>, i: number) => {
    let j = -1;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") j = (i + 1) % items.length;
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp") j = (i - 1 + items.length) % items.length;
    if (j < 0) return;
    e.preventDefault();
    onChange(items[j].id);
    const group = e.currentTarget.parentElement;
    (group?.children[j] as HTMLElement | undefined)?.focus();
  };
  return (
    <div role="radiogroup" aria-label={label} className={cx("inline-flex max-w-full rounded-full bg-well p-0.5", className)}>
      {items.map((it, i) => {
        const on = it.id === value;
        return (
          <button
            key={it.id}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={on ? 0 : -1}
            onClick={() => onChange(it.id)}
            onKeyDown={(e) => onKey(e, i)}
            className={cx(
              "tap inline-flex min-w-0 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-full transition-[background-color,color,box-shadow] duration-150",
              size === "sm" ? "px-2.5 py-1 text-[12px]" : "px-3 py-1.5 text-[13px]",
              on ? "bg-surface font-medium text-ink shadow-card" : "text-muted hover:text-ink",
            )}
          >
            {it.icon}
            {it.label}
          </button>
        );
      })}
    </div>
  );
}
