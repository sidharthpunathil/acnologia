import type { ReactNode } from "react";
import {
  RiCheckboxCircleLine,
  RiErrorWarningLine,
  RiInformationLine,
  RiAlertLine,
} from "@remixicon/react";
import { cx } from "@/utils/cx";
import type { IconComponent } from "./types";
import { IconSlot, type IconLike } from "./icon";

export type CalloutTone = "neutral" | "info" | "success" | "warning" | "error";

const TONES: Record<CalloutTone, { box: string; icon: IconComponent }> = {
  neutral: { box: "bg-background-secondary-default text-text-secondary border-border-button-default", icon: RiInformationLine },
  info: { box: "bg-status-blue-background text-status-blue-text border-transparent", icon: RiInformationLine },
  success: { box: "bg-status-lime-background text-status-lime-text border-transparent", icon: RiCheckboxCircleLine },
  warning: { box: "bg-status-yellow-background text-status-yellow-text border-transparent", icon: RiAlertLine },
  error: { box: "bg-status-rose-background text-status-rose-text border-transparent", icon: RiErrorWarningLine },
};

export interface CalloutProps {
  tone?: CalloutTone;
  /** Leading icon; defaults to the tone's icon. `false` hides it. */
  icon?: IconLike | false;
  title?: ReactNode;
  children?: ReactNode;
  /** Trailing control, usually a small Button. */
  action?: ReactNode;
  className?: string;
}

/**
 * Inline notice. Errors are announced as alerts, every other tone politely.
 * The icon carries the meaning too, so it never rides on color alone.
 */
export function Callout({ tone = "neutral", icon, title, children, action, className }: CalloutProps) {
  const t = TONES[tone];
  const shown: IconLike | null = icon === false ? null : (icon ?? t.icon);
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className={cx("flex items-start gap-2.5 rounded-2xl border px-3.5 py-3 text-body-2-regular", t.box, className)}
    >
      {shown != null && (
        <span className="mt-px flex shrink-0 [&>svg]:size-4">
          <IconSlot icon={shown} className="size-4" />
        </span>
      )}
      <div className="min-w-0 flex-1 break-words">
        {title && <div className="text-body-2-semibold">{title}</div>}
        {children}
      </div>
      {action && <div className="-my-1 shrink-0">{action}</div>}
    </div>
  );
}
