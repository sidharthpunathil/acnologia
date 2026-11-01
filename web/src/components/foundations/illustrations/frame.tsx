import type { CSSProperties, ReactNode } from "react";
import { cx } from "@/utils/cx";

/** Props every spot illustration takes. They are decorative: the empty state's title carries the meaning. */
export interface IllustrationProps {
  className?: string;
}

/** A spot illustration, passed by reference (`KeyRingArt`, not `<KeyRingArt />`). */
export type Illustration = (props: IllustrationProps) => ReactNode;

/*
 * Palette. Everything resolves through theme tokens so a piece reads the same
 * in light and dark mode: a muted ink line, a softer secondary line, the
 * surface color for anything that sits in front of something else, and the
 * violet accent for the one highlighted element.
 */
const INK = "color-mix(in srgb, var(--color-text-tertiary) 75%, transparent)";
const SOFT = "var(--color-border-button-hover)";
const PAPER = "var(--color-background-primary-default)";
const ACCENT = "var(--color-accent-fg)";
const TINT = "color-mix(in srgb, var(--color-accent-fill) 16%, var(--color-background-primary-default))";

type Tone = "ink" | "soft" | "accent";
const STROKE: Record<Tone, string> = { ink: INK, soft: SOFT, accent: ACCENT };

/**
 * Inline style for one shape. Styles (not presentation attributes) so the
 * CSS variables and color-mix resolve in every browser.
 */
export function paint(stroke: Tone | "none", fill: "paper" | "tint" | "accent" | "ink" | "soft" | "none" = "none", extra?: CSSProperties): CSSProperties {
  const fills = { paper: PAPER, tint: TINT, accent: ACCENT, ink: INK, soft: SOFT, none: "none" };
  return { stroke: stroke === "none" ? "none" : STROKE[stroke], fill: fills[fill], ...extra };
}

/** Dashed variant, for things that are missing. */
export const dashed: CSSProperties = { strokeDasharray: "3 3.5" };

/** 120 by 96 artboard with a faint ground shadow. Scales with its CSS size. */
export function Frame({ className, children, ground = true }: { className?: string; children: ReactNode; ground?: boolean }) {
  return (
    <svg
      viewBox="0 0 120 96"
      width={120}
      height={96}
      fill="none"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      className={cx("shrink-0 overflow-visible", className)}
    >
      {ground && <ellipse cx="60" cy="87" rx="36" ry="3" style={{ fill: "var(--color-text-primary)", fillOpacity: 0.05 }} />}
      {children}
    </svg>
  );
}
