import { useId, type ReactNode, type SVGProps } from "react";

/**
 * Props shared by every Acnologia icon. The shape matches a Remix icon
 * (`size`, `color`, `className` and the rest of the SVG props), so an Acn icon
 * drops into any slot that takes a Remix component (`leadingIcon`, `IconLike`).
 */
export interface AcnIconProps extends Omit<SVGProps<SVGSVGElement>, "color" | "fill" | "viewBox" | "children" | "width" | "height"> {
  /** Width and height. Defaults to 24, like Remix. Tailwind `size-*` classes win over it. */
  size?: number | string;
  /** Stroke color. Defaults to currentColor; prefer a text-* token class instead. */
  color?: string;
  /** Gives the icon a text alternative: it becomes role="img" with this <title>. */
  title?: string;
}

export type AcnIcon = ((props: AcnIconProps) => ReactNode) & { displayName?: string };

/** The one stroke weight of the set. 1.75 sits optically level with Remix line icons. */
export const ACN_STROKE = 1.75;

/**
 * Builds an icon from its drawing. Drawings use a 24 grid, stroke only, round
 * caps and joins, and keep 2px clear of every edge. A `fill="currentColor"`
 * on a child marks the few solid dots.
 */
export function createIcon(displayName: string, drawing: ReactNode): AcnIcon {
  function Icon({ size = 24, color = "currentColor", title, strokeWidth = ACN_STROKE, ...rest }: AcnIconProps) {
    const id = useId();
    const a11y = title ? { role: "img", "aria-labelledby": id } : { "aria-hidden": true };
    return (
      <svg
        xmlns="http://www.w3.org/2000/svg"
        viewBox="0 0 24 24"
        width={size}
        height={size}
        fill="none"
        stroke={color}
        strokeWidth={strokeWidth}
        strokeLinecap="round"
        strokeLinejoin="round"
        data-acn-icon={displayName}
        {...a11y}
        {...rest}
      >
        {title && <title id={id}>{title}</title>}
        {drawing}
      </svg>
    );
  }
  Icon.displayName = displayName;
  return Icon;
}
