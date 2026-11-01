import type { ReactNode } from "react";
import { Link } from "react-aria-components";
import { RiArrowLeftLine } from "@remixicon/react";
import { cx } from "@/utils/cx";

export interface PageHeaderProps {
  /** The page's one h1. Rendered in the mono voice. */
  title: ReactNode;
  /** Small mono line under the title: counts, ids, last checked. */
  meta?: ReactNode;
  /** Buttons on the trailing side; they wrap under the title on phones. */
  actions?: ReactNode;
  /** Optional back link above the title. */
  back?: { href: string; label: string };
  /** Leading visual beside the title, such as an EntityLogo. */
  leading?: ReactNode;
  className?: string;
}

/** Page title row: optional back link, mono h1, meta line and actions. */
export function PageHeader({ title, meta, actions, back, leading, className }: PageHeaderProps) {
  return (
    <header className={cx("mb-8 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between", className)}>
      <div className="flex min-w-0 flex-col gap-3">
        {back && (
          <Link
            href={back.href}
            className={cx(
              "-ms-1 inline-flex w-fit items-center gap-1 rounded-md px-1 py-0.5 text-body-2-medium text-text-secondary",
              "outline-none transition-colors duration-150 hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring",
            )}
          >
            <RiArrowLeftLine className="size-4 rtl:-scale-x-100" aria-hidden />
            {back.label}
          </Link>
        )}
        <div className="flex min-w-0 items-center gap-3">
          {leading}
          <div className="min-w-0">
            <h1 className="break-words font-mono text-title-1-medium text-text-primary sm:text-display-4-medium">{title}</h1>
            {meta && <div className="mt-1.5 font-mono text-caption-1-regular text-text-secondary">{meta}</div>}
          </div>
        </div>
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  );
}
