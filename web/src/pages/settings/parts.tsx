import type { ReactNode } from "react";
import { Link } from "react-aria-components";
import { RiArrowRightSLine, RiLockLine, RiRefreshLine } from "@remixicon/react";
import type { EnvSetting } from "@/lib/settings";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout } from "@/components/acn/callout";
import type { IconComponent } from "@/components/acn/types";
import { cx } from "@/utils/cx";

const FOCUS = "outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring";

/**
 * One settings section: a heading the in page nav links to, a one line
 * description, then its cards. Sections take focus when the nav jumps to them
 * so screen readers start reading at the heading. The scroll margin keeps the
 * heading clear of the sticky top bar (and the phone section bar).
 */
export function SettingsSection({
  id,
  title,
  description,
  children,
}: {
  id: string;
  title: string;
  description: ReactNode;
  children: ReactNode;
}) {
  return (
    <section id={id} aria-labelledby={`${id}-title`} tabIndex={-1} className="scroll-mt-36 outline-none lg:scroll-mt-24">
      <header className="mb-4">
        <h2 id={`${id}-title`} className="text-title-3-semibold text-text-primary">
          {title}
        </h2>
        <p className="mt-1 max-w-2xl text-body-2-regular text-text-secondary">{description}</p>
      </header>
      <div className="flex flex-col gap-4">{children}</div>
    </section>
  );
}

/** A card of rows. Rows divide themselves with hairlines. */
export function SettingsCard({ title, children, className }: { title?: string; children: ReactNode; className?: string }) {
  return (
    <div className={cx("overflow-hidden rounded-3xl border border-border-button-default bg-background-primary-default", className)}>
      {title && (
        <h3 className="border-b border-separator-border px-4 pt-4 pb-3 text-caption-1-semibold text-text-tertiary sm:px-5">{title}</h3>
      )}
      <div className="divide-y divide-separator-border">{children}</div>
    </div>
  );
}

/**
 * Label and description on the start side, the control on the end side. On
 * phones (or with `stacked`) the control drops under the label.
 */
export function SettingRow({
  label,
  description,
  children,
  stacked = false,
  labelId,
  className,
}: {
  label: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  stacked?: boolean;
  /** Id for the label, so a control can point at it with aria-labelledby. */
  labelId?: string;
  className?: string;
}) {
  return (
    <div
      className={cx(
        "flex flex-col gap-3 px-4 py-4 sm:px-5",
        !stacked && "sm:flex-row sm:items-center sm:justify-between sm:gap-6",
        className,
      )}
    >
      <div className="min-w-0 sm:max-w-md">
        <div id={labelId} className="text-body-medium text-text-primary">
          {label}
        </div>
        {description && <div className="mt-0.5 text-body-2-regular text-pretty text-text-secondary">{description}</div>}
      </div>
      {children != null && <div className={cx("min-w-0", !stacked && "sm:max-w-[55%] sm:shrink-0 sm:text-end")}>{children}</div>}
    </div>
  );
}

/**
 * A LinkRow summary from a load: the words once loaded, `undefined` (a
 * placeholder) while loading, and nothing when it failed, since the page it
 * links to shows the error.
 */
export function countSummary<T>(load: { data: T | null; error: string | null }, words: (data: T) => string): string | undefined {
  if (load.error) return "";
  return load.data ? words(load.data) : undefined;
}

/** A row that opens another page, with a short live summary on the end side. */
export function LinkRow({
  href,
  icon: Icon,
  label,
  description,
  summary,
}: {
  href: string;
  icon: IconComponent;
  label: string;
  description: string;
  /** "3 webhooks". `undefined` while loading draws a placeholder of the same height. */
  summary?: ReactNode;
}) {
  return (
    <Link
      href={href}
      className={cx(
        "group flex items-center gap-3 px-4 py-3.5 transition-colors duration-150 hover:bg-background-secondary-hover sm:px-5",
        "pointer-coarse:min-h-14 focus-visible:ring-inset",
        FOCUS,
      )}
    >
      <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-background-secondary-default">
        <Icon className="size-[18px] text-foreground-icon-secondary" aria-hidden />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex flex-wrap items-baseline gap-x-2">
          <span className="text-body-medium text-text-primary">{label}</span>
          {/* Phones: the summary rides next to the label, so the description keeps the width. */}
          {summary !== "" && (
            <span className="text-caption-1-regular text-text-tertiary tabular-nums sm:hidden">
              {summary === undefined ? <Skeleton shape="text" className="inline-block h-3 w-12 align-middle" /> : summary}
            </span>
          )}
        </span>
        <span className="block text-body-2-regular text-text-secondary">{description}</span>
      </span>
      <span className="hidden shrink-0 text-body-2-regular text-text-secondary tabular-nums sm:block">
        {summary === undefined ? <Skeleton shape="text" className="h-3.5 w-16" /> : summary}
      </span>
      <RiArrowRightSLine
        className="size-5 shrink-0 text-foreground-icon-tertiary transition-transform duration-150 ease-out group-hover:translate-x-0.5 motion-reduce:transition-none rtl:-scale-x-100"
        aria-hidden
      />
    </Link>
  );
}

/** "Set by ACNOLOGIA_X" or "ACNOLOGIA_X not set", with the value in effect. Secrets show only set or not set. */
export function EnvValue({
  env,
  format = (v) => v,
  unsetLabel,
}: {
  env: EnvSetting | null;
  /** Turns the raw value into words, such as "30 days". */
  format?: (value: string) => ReactNode;
  /** What the value means when the variable is unset, such as "Default: 30 days". */
  unsetLabel?: ReactNode;
}) {
  if (!env) return <EnvValueSkeleton />;
  const value = env.value ?? "";
  return (
    <div className="flex flex-col gap-1 sm:items-end">
      {env.secret ? (
        <Chip variant="caption" color={env.set ? "lime" : "soft"} className="w-fit">
          {env.set ? "Set" : "Not set"}
        </Chip>
      ) : (
        <span className="font-mono text-body-2-regular wrap-anywhere text-text-primary">
          {value === "" ? <span className="text-text-tertiary">empty</span> : format(value)}
        </span>
      )}
      <span className="text-caption-1-regular text-text-tertiary">
        {env.set ? (
          <>
            Set by <code className="font-mono wrap-anywhere">{env.name}</code>
          </>
        ) : (
          <>
            <code className="font-mono wrap-anywhere">{env.name}</code> not set
            {unsetLabel ? <>. {unsetLabel}</> : null}
          </>
        )}
      </span>
    </div>
  );
}

export function EnvValueSkeleton() {
  return (
    <div className="flex flex-col gap-1.5 sm:items-end" aria-hidden>
      <Skeleton shape="text" className="h-4 w-24" />
      <Skeleton shape="text" className="h-3 w-44" />
    </div>
  );
}

/**
 * The footer of an editable card: what state the edits are in, Discard and
 * Save. Both buttons are always there (disabled when there is nothing to do),
 * so the bar never changes height or shifts as you type.
 */
export function SaveBar({
  dirty,
  saving,
  onDiscard,
  saveLabel = "Save",
  status,
  shortcut,
}: {
  dirty: boolean;
  saving: boolean;
  onDiscard: () => void;
  saveLabel?: string;
  /** Shown while there is nothing unsaved, such as "Saved 2 minutes ago". */
  status?: ReactNode;
  /** The save shortcut as it reads on this platform. */
  shortcut?: string;
}) {
  return (
    <div className="flex flex-wrap items-center justify-end gap-x-2 gap-y-3 border-t border-separator-border px-4 py-3 sm:px-5">
      <p role="status" className="me-auto flex min-w-0 items-center gap-1.5 text-caption-1-regular text-text-secondary">
        {dirty ? (
          <>
            <span className="size-1.5 shrink-0 rounded-full bg-accent-500" aria-hidden />
            Unsaved changes
            {shortcut && <span className="hidden text-text-tertiary sm:inline">· {shortcut} to save</span>}
          </>
        ) : (
          status
        )}
      </p>
      <Button type="button" variant="ghost" size="small" disabled={!dirty || saving} onClick={onDiscard}>
        Discard
      </Button>
      <Button
        type="submit"
        variant="primary"
        size="small"
        disabled={!dirty || saving}
        aria-busy={saving || undefined}
        leadingIcon={saving ? ButtonSpinner : undefined}
      >
        {saveLabel}
      </Button>
    </div>
  );
}

/** Why this part is read only for you. */
export function ReadOnlyNote({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <p className={cx("flex items-start gap-1.5 text-caption-1-regular text-text-secondary", className)}>
      <RiLockLine className="mt-px size-3.5 shrink-0 text-foreground-icon-tertiary" aria-hidden />
      <span>{children}</span>
    </p>
  );
}

/** A section that failed to load, with a way to try again. */
export function SectionError({ what, message, onRetry }: { what: string; message: string; onRetry: () => void }) {
  return (
    <Callout
      tone="error"
      title={`Could not load ${what}`}
      action={
        <Button variant="secondary" size="small" leadingIcon={RiRefreshLine} onClick={onRetry}>
          Retry
        </Button>
      }
    >
      <span className="wrap-anywhere">{message}</span>
    </Callout>
  );
}

/** Rows of label and value placeholders, as tall as the rows they stand in for. */
export function RowsSkeleton({ rows }: { rows: number }) {
  return (
    <>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between sm:px-5" aria-hidden>
          <div className="flex flex-col gap-1.5">
            <Skeleton shape="text" className="h-4 w-32" />
            <Skeleton shape="text" className="h-3 w-56 max-w-full" />
          </div>
          <EnvValueSkeleton />
        </div>
      ))}
    </>
  );
}
