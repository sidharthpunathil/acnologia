import { Link } from "react-router-dom";
import { cx } from "@/utils/cx";

export const BRAND_NAME = "Acnologia";

/** The Acnologia dragon mark. Corners are baked into the artwork. Decorative: pair it with the name. */
export function Mark({ size, className }: { size: number; className?: string }) {
  return (
    <img
      src="/mark.png"
      alt=""
      aria-hidden="true"
      width={size}
      height={size}
      draggable={false}
      className={cx("shrink-0 select-none", className)}
    />
  );
}

/** Mark plus name, as shown in the top bar and the docs header. */
export function Brand({ className, size = 28 }: { className?: string; size?: number }) {
  return (
    <span className={cx("flex items-center gap-2", className)}>
      <Mark size={size} />
      <span className="font-mono text-headline-medium text-text-primary">{BRAND_NAME}</span>
    </span>
  );
}

/** Brand that links home. */
export function Logo({ className }: { className?: string }) {
  return (
    <Link
      to="/"
      aria-label={`${BRAND_NAME} home`}
      className={cx(
        "-mx-1.5 flex items-center rounded-xl px-1.5 py-1 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring",
        className,
      )}
    >
      <Brand />
    </Link>
  );
}
