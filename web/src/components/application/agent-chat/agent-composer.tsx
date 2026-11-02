import { RiArrowUpLine, RiSparklingLine } from "@remixicon/react";
import {
  useLayoutEffect,
  useRef,
  type ClipboardEvent,
  type FormEvent,
  type KeyboardEvent,
  type ReactNode,
  type Ref,
} from "react";
import { TextArea } from "react-aria-components";

import { ComposerLoader } from "@/components/application/composer-loader/composer-loader";
import { SpinnerGlyph } from "@/components/base/spinner/spinner";
import { cx } from "@/utils/cx";

/**
 * The composer, adapted from BoardUI's free Chat Starter.
 *
 * The starter's proportions stay: a rounded field one step lighter than the
 * page, a 36px circular send control sitting flush inside it, and a thin
 * status row beneath. Acnologia changes three things. The single line input
 * is a growing text area (Enter sends, Shift+Enter breaks the line), there is
 * a slot above the text for attachments (pasted or dropped keys), and the
 * status row carries a hint and the model instead of message counts.
 *
 * While a turn runs, ComposerLoader draws its band of light around the rim.
 * The loader paints the pill surface and its shadow, so the form never paints
 * its own: it keeps only the border and focus ring, and the light can fade in
 * and out underneath it. With reduced motion the loader holds a still glow
 * instead of a travelling band.
 */

/**
 * Acnologia: the band rides the --loader-c* tokens (deeper stops in light
 * mode), at full intensity with a stronger bloom and a thicker line, so the
 * working state reads on a white pill.
 */
const LOADER_COLORS: [string, string, string, string] = [
  "var(--loader-c0)",
  "var(--loader-c1)",
  "var(--loader-c2)",
  "var(--loader-c3)",
];

export interface AgentComposerProps {
  id?: string;
  value: string;
  onValueChange: (value: string) => void;
  onSubmit: (event?: FormEvent) => void;
  /** Sending is blocked: the send control spins. */
  busy: boolean;
  /** The agent is working on this chat's turn: the rim lights up. Defaults to `busy`. */
  working?: boolean;
  /** The agent is unavailable: the field is disabled. */
  disabled?: boolean;
  /** Something other than text is ready to send, such as an attachment. */
  hasContent?: boolean;
  placeholder?: string;
  /** Rendered above the text, inside the field. */
  attachment?: ReactNode;
  /** The status row under the field. */
  status?: ReactNode;
  statusId?: string;
  onPaste?: (event: ClipboardEvent<HTMLTextAreaElement>) => void;
  textareaRef?: Ref<HTMLTextAreaElement>;
  className?: string;
}

export function AgentComposer({
  id = "agent-composer-input",
  value,
  onValueChange,
  onSubmit,
  busy,
  working = busy,
  disabled = false,
  hasContent = false,
  placeholder = "Ask me anything",
  attachment,
  status,
  statusId,
  onPaste,
  textareaRef,
  className,
}: AgentComposerProps) {
  const inner = useRef<HTMLTextAreaElement | null>(null);
  const setRef = (el: HTMLTextAreaElement | null) => {
    inner.current = el;
    if (typeof textareaRef === "function") textareaRef(el);
    else if (textareaRef) (textareaRef as { current: HTMLTextAreaElement | null }).current = el;
  };

  // Grow with the text up to a cap (a third of the screen, at most 200px), then scroll inside.
  useLayoutEffect(() => {
    const el = inner.current;
    if (!el) return;
    el.style.height = "auto";
    const cap = Math.min(200, Math.round(window.innerHeight / 3));
    el.style.height = `${Math.min(el.scrollHeight, cap)}px`;
  }, [value]);

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      onSubmit();
    } else if (event.key === "Escape") {
      event.currentTarget.blur();
    }
  };

  const canSend = !busy && !disabled && (!!value.trim() || hasContent);

  return (
    <div className={cx("flex w-full flex-col gap-1.5", className)}>
      <ComposerLoader active={working} radius={24} colors={LOADER_COLORS} intensity={1} bloomStrength={0.45} line={3}>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            onSubmit(event);
          }}
          className={cx(
            "flex w-full flex-col rounded-3xl border border-border-button-default bg-transparent shadow-none transition-[border-color] duration-150 ease-out",
            !disabled && "focus-within:border-border-button-hover",
          )}
        >
          {attachment}
          <div className="flex items-end gap-2 py-2 ps-4 pe-2">
            <label htmlFor={id} className="sr-only">
              Message
            </label>
            <TextArea
              id={id}
              ref={setRef}
              rows={1}
              value={value}
              disabled={disabled}
              onChange={(event) => onValueChange(event.target.value)}
              onKeyDown={onKeyDown}
              onPaste={onPaste}
              aria-describedby={statusId}
              placeholder={placeholder}
              className={cx(
                "block max-h-[200px] min-h-9 w-full flex-1 resize-none overflow-y-auto bg-transparent py-2",
                // 16px on phones keeps iOS from zooming the page on focus.
                "text-headline-regular text-text-primary caret-text-primary placeholder:text-text-placeholder sm:text-body-regular",
                "outline-none disabled:cursor-not-allowed",
              )}
            />
            <button
              type="submit"
              aria-label={busy ? "Working on your last message" : "Send"}
              title={busy ? "Working" : "Send"}
              disabled={!canSend}
              className={cx(
                "button-press-motion flex size-9 shrink-0 cursor-pointer items-center justify-center rounded-full transition-[background-color,color,opacity] duration-150 ease-out",
                "outline-none focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-border-focus-ring",
                busy
                  ? "bg-background-secondary-default text-foreground-icon-secondary"
                  : "bg-button-primary text-text-white disabled:cursor-not-allowed disabled:opacity-40",
              )}
            >
              {busy ? <SpinnerGlyph size="sm" /> : <RiArrowUpLine className="size-5" aria-hidden />}
            </button>
          </div>
        </form>
      </ComposerLoader>
      {status ? (
        <div id={statusId} aria-live="polite" className="flex min-h-[18px] w-full items-center justify-center px-4 text-center">
          {status}
        </div>
      ) : null}
    </div>
  );
}

/** The model the agent answers with, as the status row shows it. */
export function ComposerModel({ model }: { model: string }) {
  const slash = model.lastIndexOf("/");
  return (
    <span className="inline-flex items-center gap-1" title={`Answering with ${model}`}>
      <RiSparklingLine className="size-3.5 shrink-0 text-foreground-icon-tertiary" aria-hidden />
      <span className="font-mono">{slash === -1 ? model : model.slice(slash + 1)}</span>
    </span>
  );
}
