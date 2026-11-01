import type { ReactNode } from "react";
import { cx } from "@/utils/cx";

export interface SectionProps {
  title: ReactNode;
  description?: ReactNode;
  /** Controls on the header's trailing side. */
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
  /** Tone for a danger zone: a rose hairline instead of the neutral one. */
  tone?: "default" | "danger";
  id?: string;
}

/** A titled rounded-3xl card used for settings, editors and grouped content. */
export function Section({ title, description, actions, children, className, tone = "default", id }: SectionProps) {
  return (
    <section
      id={id}
      className={cx(
        "rounded-3xl border bg-background-primary-default p-5 sm:p-6",
        tone === "danger" ? "border-border-error-default" : "border-border-button-default",
        className,
      )}
    >
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <h2 className="text-headline-semibold text-text-primary">{title}</h2>
          {description && <p className="mt-1 max-w-2xl text-body-2-regular text-text-secondary">{description}</p>}
        </div>
        {actions && <div className="flex shrink-0 flex-wrap gap-2">{actions}</div>}
      </div>
      {children && <div className="mt-5">{children}</div>}
    </section>
  );
}
