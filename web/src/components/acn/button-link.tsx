import type { ComponentType, ReactNode } from "react";
import { Link, type LinkProps } from "react-aria-components";
import { buttonStyles } from "@/components/base/buttons/button";
import { cx } from "@/utils/cx";
import { directionalIconClass } from "@/utils/directional-icon";

type IconComponent = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;

export interface RouterButtonLinkProps extends Omit<LinkProps, "className" | "children"> {
  /** App path, such as "/providers/ai". Routes client side through the RouterProvider in main.tsx. */
  href: string;
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "medium" | "small" | "xs";
  iconOnly?: boolean;
  leadingIcon?: IconComponent;
  trailingIcon?: IconComponent;
  className?: string;
  children?: ReactNode;
}

/**
 * BoardUI's Button look on a React Aria Link, so navigation stays in the app
 * (no full reload) and the link keeps link semantics. Icon only links need an
 * `aria-label`.
 */
export function RouterButtonLink({
  variant = "primary",
  size = "medium",
  iconOnly = false,
  leadingIcon: Leading,
  trailingIcon: Trailing,
  className,
  children,
  ...props
}: RouterButtonLinkProps) {
  return (
    <Link
      {...props}
      className={cx(
        buttonStyles.base,
        buttonStyles.size[size],
        buttonStyles.variant[variant],
        iconOnly && buttonStyles.iconOnlySize[size],
        "no-underline",
        className,
      )}
    >
      {Leading ? <Leading className={cx(buttonStyles.icon[size], directionalIconClass(Leading))} aria-hidden /> : null}
      {!iconOnly && children != null && <span className={buttonStyles.label[size]}>{children}</span>}
      {!iconOnly && Trailing ? <Trailing className={cx(buttonStyles.icon[size], directionalIconClass(Trailing))} aria-hidden /> : null}
    </Link>
  );
}
