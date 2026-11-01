import { isValidElement, type ReactNode } from "react";
import type { IconComponent } from "./types";

/** An icon given either as a Remix component ref (preferred) or, for legacy callers, as an element. */
export type IconLike = IconComponent | ReactNode;

export function isIconComponent(icon: unknown): icon is IconComponent {
  return typeof icon === "function" || (typeof icon === "object" && icon !== null && "$$typeof" in icon && !isValidElement(icon));
}

/** Draws an IconLike at the given class; elements render as they are. */
export function IconSlot({ icon, className }: { icon: IconLike; className?: string }) {
  if (!icon) return null;
  if (isIconComponent(icon)) {
    const I = icon;
    return <I className={className} aria-hidden />;
  }
  return <>{icon}</>;
}
