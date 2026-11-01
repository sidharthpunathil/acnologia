/**
 * Legacy primitives, kept as thin adapters over BoardUI and components/acn so
 * pages that have not migrated keep rendering with the new look. Same exports
 * and props as before. New code imports BoardUI and acn directly; this file
 * goes away once nothing imports it.
 */
import {
  cloneElement,
  forwardRef,
  isValidElement,
  useId,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactElement,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { Button as BoardButton } from "@/components/base/buttons/button";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { Skeleton as BoardSkeleton } from "@/components/base/skeleton/skeleton";
import { Spinner as BoardSpinner, SpinnerGlyph, ButtonSpinner } from "@/components/base/spinner/spinner";
import { Switch } from "@/components/base/switch/switch";
import { SegmentedControl, SegmentedControlItem } from "@/components/base/segmented-control/segmented-control";
import { hintTextClassName } from "@/components/base/input/hint-text";
import { Callout as AcnCallout, type CalloutTone } from "@/components/acn/callout";
import { EmptyState } from "@/components/acn/empty-state";
import { PageHeader as AcnPageHeader } from "@/components/acn/page-header";
import { PageSkeleton as AcnPageSkeleton } from "@/components/acn/page-skeleton";
import { Section as AcnSection } from "@/components/acn/section";
import { StatTile, type StatTone } from "@/components/acn/stat-tile";
import { cx as mergeCx } from "@/utils/cx";

/**
 * Retired: plain class join kept for unmigrated callers. New code uses `cx`
 * from `@/utils/cx`, which also merges conflicting Tailwind classes.
 */
export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  /** Legacy: an icon element. New code passes `leadingIcon` to the BoardUI Button. */
  icon?: ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", size = "md", loading, icon, className, children, disabled, type = "button", ...rest },
  ref,
) {
  const lead = loading ? null : icon;
  return (
    <BoardButton
      ref={ref}
      type={type}
      variant={variant}
      size={size === "sm" ? "small" : "medium"}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      leadingIcon={loading ? ButtonSpinner : undefined}
      className={mergeCx("[&_svg]:shrink-0", className)}
      {...rest}
    >
      {lead ? (
        <span className="inline-flex items-center gap-1.5 [&>svg]:size-4">
          {lead}
          {children}
        </span>
      ) : (
        children
      )}
    </BoardButton>
  );
});

export function IconButton({
  label,
  className,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <BoardButton
      variant="ghost"
      size="small"
      aria-label={label}
      title={label}
      className={mergeCx("size-8 justify-center p-0 text-foreground-icon-secondary hover:text-foreground-icon-primary [&_svg]:size-4", className)}
      {...rest}
    >
      {children}
    </BoardButton>
  );
}

/** BoardUI field look on a native control, so `onChange(e)` callers keep working. */
const fieldBase = [
  "w-full min-w-0 rounded-2lg bg-background-tertiary-default px-3 text-body-regular text-text-primary",
  "ring-1 ring-inset ring-input-idle-ring outline-none transition-[background-color,box-shadow] duration-150 ease",
  "placeholder:text-text-tertiary hover:ring-border-button-hover focus:ring-2 focus:ring-border-button-active",
  "disabled:cursor-not-allowed disabled:bg-input-disabled-background disabled:text-input-disabled-text",
  "aria-invalid:bg-background-tertiary-error aria-invalid:ring-border-error-default",
].join(" ");

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input({ className, ...rest }, ref) {
  return <input ref={ref} className={cx(fieldBase, "h-9", className)} {...rest} />;
});

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(function Textarea(
  { className, ...rest },
  ref,
) {
  return <textarea ref={ref} className={cx(fieldBase, "py-2", className)} {...rest} />;
});

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={cx(fieldBase, "select-chevron h-9 cursor-pointer appearance-none bg-[length:16px] pe-8", className)} {...rest}>
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
  const controlId = control ? ((control.props as { id?: string }).id ?? id) : undefined;
  const wired = control
    ? cloneElement(control, {
        id: controlId,
        "aria-describedby": describedBy,
        "aria-invalid": error ? true : (control.props as { "aria-invalid"?: boolean })["aria-invalid"],
      })
    : children;
  return (
    <div className={cx("flex min-w-0 flex-col gap-1", className)}>
      <label htmlFor={controlId} className="flex items-center gap-0.5 text-body-medium text-text-primary">
        {label}
        {required && (
          <span className="text-text-error-primary" aria-hidden="true">
            *
          </span>
        )}
      </label>
      {wired}
      {error ? (
        <span id={errId} role="alert" className={hintTextClassName(true)}>
          {error}
        </span>
      ) : (
        hint && (
          <span id={hintId} className={hintTextClassName(false)}>
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
  caption,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label?: string;
  description?: string;
  disabled?: boolean;
  /** Accessible name when there is no visible label. */
  ariaLabel?: string;
  /** Short state text beside a bare switch, such as "Active" or "Paused". Pressing it toggles. */
  caption?: ReactNode;
}) {
  if (!label && caption != null)
    return (
      <Switch isSelected={checked} onChange={onChange} isDisabled={disabled} aria-label={ariaLabel} size="sm" className="flex-row-reverse">
        <span aria-hidden={ariaLabel ? true : undefined} className="text-caption-1-regular text-text-secondary">
          {caption}
        </span>
      </Switch>
    );
  if (!label) return <Switch isSelected={checked} onChange={onChange} isDisabled={disabled} aria-label={ariaLabel} size="sm" />;
  return (
    <div className="flex items-start justify-between gap-4">
      <div className={cx("min-w-0", !disabled && "cursor-pointer")} onClick={() => !disabled && onChange(!checked)}>
        <div className="text-body-2-medium text-text-primary">{label}</div>
        {description && <div className="mt-0.5 text-caption-1-regular text-text-secondary">{description}</div>}
      </div>
      <Switch isSelected={checked} onChange={onChange} isDisabled={disabled} aria-label={label} size="sm" />
    </div>
  );
}

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={mergeCx("rounded-3xl border border-border-button-default bg-background-primary-default", className)}>{children}</div>;
}

type BadgeTone = "neutral" | "green" | "amber" | "red" | "blue" | "violet" | "dark";

const BADGE_COLOR: Record<BadgeTone, ChipColor> = {
  neutral: "neutral",
  green: "lime",
  amber: "yellow",
  red: "rose",
  blue: "blue",
  violet: "purple",
  dark: "gray",
};

export function Badge({ tone = "neutral", children, className, title }: { tone?: BadgeTone; children: ReactNode; className?: string; title?: string }) {
  return (
    <Chip variant="caption" color={BADGE_COLOR[tone]} title={title} className={mergeCx("gap-1 py-0.5 font-mono", className)}>
      {children}
    </Chip>
  );
}

export function Meta({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cx("font-mono text-caption-1-regular text-text-secondary", className)}>{children}</div>;
}

export function Empty({
  icon,
  title,
  body,
  action,
  className,
  compact,
}: {
  icon?: ReactNode;
  title: string;
  body?: ReactNode;
  action?: ReactNode;
  className?: string;
  compact?: boolean;
}) {
  return <EmptyState icon={icon} title={title} body={body} action={action} className={className} compact={compact} />;
}

export function Spinner({ className }: { className?: string }) {
  const big = /\bh-(5|6)\b/.test(className ?? "");
  return <SpinnerGlyph size={big ? "md" : "sm"} className={cx("text-text-tertiary", className?.replace(/\b[hw]-[\d.]+\b/g, ""))} />;
}

export function PageLoading({ label = "Loading" }: { label?: string }) {
  return (
    <div className="flex items-center justify-center py-24" role="status" aria-live="polite">
      <BoardSpinner size="md" aria-label={label} />
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <BoardSkeleton className={mergeCx("rounded-lg", className)} />;
}

export const PageSkeleton = AcnPageSkeleton;

export function Section(props: { title: ReactNode; description?: ReactNode; actions?: ReactNode; children?: ReactNode; className?: string }) {
  return <AcnSection {...props} />;
}

const CALLOUT_TONE: Record<"neutral" | "amber" | "red" | "green", CalloutTone> = {
  neutral: "neutral",
  amber: "warning",
  red: "error",
  green: "success",
};

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
  return (
    <AcnCallout tone={CALLOUT_TONE[tone]} icon={icon ?? false} className={className}>
      {children}
    </AcnCallout>
  );
}

export function PageHeader({ title, meta, actions }: { title: ReactNode; meta?: ReactNode; actions?: ReactNode }) {
  return <AcnPageHeader title={title} meta={meta} actions={actions} />;
}

const STAT_TONE: Record<"green" | "amber" | "red" | "neutral" | "violet", StatTone> = {
  green: "success",
  amber: "warning",
  red: "error",
  neutral: "neutral",
  violet: "accent",
};

export function Stat({
  label,
  value,
  tone,
  sub,
  className,
}: {
  label: string;
  value: ReactNode;
  tone?: "green" | "amber" | "red" | "neutral" | "violet";
  sub?: ReactNode;
  className?: string;
}) {
  return <StatTile label={label} value={value} sub={sub} tone={tone ? STAT_TONE[tone] : "neutral"} className={className} />;
}

export interface SegmentItem<T extends string> {
  id: T;
  label: string;
  icon?: ReactNode;
}

/** Segmented control with single selection. Arrow keys move between segments. */
export function Segmented<T extends string>({
  items,
  value,
  onChange,
  label,
  className,
  size = "md",
  block,
}: {
  items: SegmentItem<T>[];
  value: T;
  onChange: (v: T) => void;
  label: string;
  className?: string;
  size?: "sm" | "md";
  /** Fill the row: segments share the full width. */
  block?: boolean;
}) {
  return (
    <SegmentedControl
      aria-label={label}
      selectedKeys={[value]}
      onSelectionChange={(keys) => {
        const next = [...keys][0] as T | undefined;
        if (next !== undefined) onChange(next);
      }}
      className={mergeCx(block ? "flex w-full" : "inline-flex", "max-w-full", className)}
    >
      {items.map((it) => (
        <SegmentedControlItem
          key={it.id}
          id={it.id}
          className={cx("min-w-0 flex-1 gap-1.5 [&>svg]:size-3.5 [&>svg]:shrink-0", size === "sm" && "px-2 py-0.5 text-caption-1-medium")}
        >
          {it.icon}
          {it.label}
        </SegmentedControlItem>
      ))}
    </SegmentedControl>
  );
}
