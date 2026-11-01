import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { buttonStyles } from "@/components/base/buttons/button";
import type { IconComponent } from "@/components/acn/types";
import { cx } from "@/utils/cx";

/**
 * BoardUI's Button look on a react-router Link, for in app navigation from
 * the docs (ButtonLink is a plain anchor and would reload the page).
 */
export function RouterButton({
  to,
  variant = "primary",
  size = "medium",
  leadingIcon: Leading,
  trailingIcon: Trailing,
  className,
  children,
}: {
  to: string;
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "medium" | "small";
  leadingIcon?: IconComponent;
  trailingIcon?: IconComponent;
  className?: string;
  children: ReactNode;
}) {
  return (
    <Link to={to} className={cx(buttonStyles.base, buttonStyles.size[size], buttonStyles.variant[variant], className)}>
      {Leading ? <Leading className={buttonStyles.icon[size]} aria-hidden /> : null}
      <span className={buttonStyles.label[size]}>{children}</span>
      {Trailing ? <Trailing className={buttonStyles.icon[size]} aria-hidden /> : null}
    </Link>
  );
}
