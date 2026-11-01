import { motion, useReducedMotion } from "motion/react";
import { memo, useMemo, type ComponentType, type ReactNode } from "react";

import { Button } from "@/components/base/buttons/button";
import { cx } from "@/utils/cx";

/**
 * One turn in the transcript, adapted from BoardUI's free Chat Starter.
 *
 * Acnologia keeps the starter's anatomy (a user bubble on the right, an
 * assistant reply as plain text on the page, an action row under it) and
 * swaps the starter's own text renderer for children, so the page can render
 * entity chips, inline code and lists inside a reply. The reply sits beside
 * an avatar slot, which the Assistant fills with the dragon mark.
 *
 * The action row is mounted at all times and revealed on hover or focus with
 * a mouse, and always shown on touch screens. Rendering it only on hover
 * would make it unreachable by keyboard and shift the layout under the
 * pointer.
 */

export interface AgentMessageProps {
  role: "user" | "assistant";
  children: ReactNode;
  /** Assistant only: the avatar beside the reply. */
  avatar?: ReactNode;
  /** Controls under the message: Copy, Retry, Edit. */
  actions?: ReactNode;
  className?: string;
}

/** Hidden with a mouse until the message is hovered or a control has focus; always visible on touch. */
const ACTIONS_ROW =
  "flex flex-wrap items-center gap-1 transition-opacity duration-150 ease-out [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:focus-within:opacity-100";

export function AgentMessage({ role, children, avatar, actions, className }: AgentMessageProps) {
  if (role === "user") {
    return (
      <div className={cx("group/message flex flex-col items-end gap-1", className)}>
        <div className="max-w-[85%] rounded-2xl bg-background-primary-default px-3.5 py-2.5 text-start text-body-regular break-words whitespace-pre-wrap text-text-primary shadow-card sm:max-w-[75%]">
          {children}
        </div>
        {actions ? (
          <div className={cx(ACTIONS_ROW, "-me-1 [@media(hover:hover)]:group-hover/message:opacity-100")}>{actions}</div>
        ) : null}
      </div>
    );
  }

  return (
    <div className={cx("group/message flex gap-3", className)}>
      {avatar ? <div className="shrink-0 pt-px">{avatar}</div> : null}
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="min-w-0 text-body-regular break-words text-text-primary">{children}</div>
        {actions ? (
          <div className={cx(ACTIONS_ROW, "-ms-2 [@media(hover:hover)]:group-hover/message:opacity-100")}>{actions}</div>
        ) : null}
      </div>
    </div>
  );
}

type IconComponent = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;

/**
 * BoardUI's ghost button is accent tinted; actions under a message stay
 * quieter than that, on the neutral hover surface.
 */
export const QUIET_BUTTON =
  "bg-transparent text-text-secondary hover:bg-background-secondary-hover hover:text-text-primary active:bg-background-tertiary-default";

/** A quiet text action under a message. */
export function MessageAction({
  icon,
  label,
  onClick,
  "aria-label": ariaLabel,
}: {
  icon: IconComponent;
  label: ReactNode;
  onClick: () => void;
  "aria-label"?: string;
}) {
  return (
    <Button
      size="xs"
      variant="ghost"
      leadingIcon={icon}
      onClick={onClick}
      aria-label={ariaLabel}
      className={cx(QUIET_BUTTON, "pointer-coarse:h-10")}
    >
      {label}
    </Button>
  );
}

/**
 * The starter's line entrance: each block softens in out of a 5px blur with
 * a 4px lift over 0.42s. Kept at module scope so the identities are stable.
 * Reduced motion skips it (the caller passes `animate={false}`).
 */
export const LINE_HIDDEN = { opacity: 0, filter: "blur(5px)", y: 4 };
export const LINE_SHOWN = { opacity: 1, filter: "blur(0px)", y: 0 };
export const LINE_TRANSITION = { duration: 0.42, ease: [0.22, 0.61, 0.36, 1] } as const;

/**
 * Plain paragraphs, each softening in once as it first appears. The
 * animation lives at the line level rather than the word level: a word level
 * blur on a fragment that then mutates reads as a stutter.
 */
export function StreamedText({ text, paragraphs = false }: { text: string; paragraphs?: boolean }) {
  const reduceMotion = useReducedMotion();
  const lines = useMemo(
    () => text.split(paragraphs ? /\n\s*\n/ : "\n").filter((line) => line.trim() !== ""),
    [text, paragraphs],
  );
  return (
    <div className="flex flex-col gap-3">
      {lines.map((line, index) => (
        <Line key={index} text={line} animate={!reduceMotion} />
      ))}
    </div>
  );
}

const Line = memo(function Line({ text, animate }: { text: string; animate: boolean }) {
  return (
    <motion.p
      initial={animate ? LINE_HIDDEN : false}
      animate={LINE_SHOWN}
      transition={LINE_TRANSITION}
      className="text-body-regular break-words text-text-primary"
    >
      {text}
    </motion.p>
  );
});
