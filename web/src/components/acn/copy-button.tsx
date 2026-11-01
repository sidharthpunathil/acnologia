import { useEffect, useRef, useState } from "react";
import { RiCheckLine, RiFileCopyLine } from "@remixicon/react";
import { Button } from "@/components/base/buttons/button";
import { toast } from "@/components/base/toast/toast";
import { cx } from "@/utils/cx";

/** Copies text, with a fallback for plain http hosts where the Clipboard API is missing. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try {
      ok = document.execCommand("copy");
    } catch {
      ok = false;
    }
    document.body.removeChild(ta);
    return ok;
  }
}

/** Copy state that flips back after a moment. */
export function useCopy(text: string, ms = 1500) {
  const [done, setDone] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const copy = async () => {
    const ok = await copyText(text);
    if (!ok) {
      toast.error("Could not copy. Select the text and copy it yourself.");
      return;
    }
    setDone(true);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setDone(false), ms);
  };
  return { done, copy };
}

export interface CopyButtonProps {
  text: string;
  /** Visible label; the icon only variant uses it as the accessible name. */
  label?: string;
  iconOnly?: boolean;
  size?: "xs" | "small";
  /** Sits on the dark code surface. */
  onCode?: boolean;
  className?: string;
}

/** Copy to clipboard. The icon turns into a check and "Copied" is announced. */
export function CopyButton({ text, label = "Copy", iconOnly, size = "small", onCode, className }: CopyButtonProps) {
  const { done, copy } = useCopy(text);
  return (
    <>
      <Button
        variant="ghost"
        size={size}
        iconOnly={iconOnly}
        aria-label={iconOnly ? (done ? "Copied" : label) : undefined}
        title={iconOnly ? label : undefined}
        leadingIcon={done ? RiCheckLine : RiFileCopyLine}
        onClick={() => void copy()}
        className={cx(
          onCode && "bg-transparent text-code-ink/70 hover:bg-code-ink/10 hover:text-code-ink active:bg-code-ink/15",
          className,
        )}
      >
        {done ? "Copied" : label}
      </Button>
      <span className="sr-only" aria-live="polite">
        {done ? "Copied" : ""}
      </span>
    </>
  );
}

/** A secret shown once: mono value in a read only well, with a copy button. */
export function SecretBox({ value, className }: { value: string; className?: string }) {
  return (
    <div
      className={cx(
        "flex items-center gap-2 rounded-2lg bg-background-tertiary-default py-1.5 ps-3 pe-1.5",
        className,
      )}
    >
      <code dir="ltr" className="min-w-0 flex-1 break-all font-mono text-body-2-regular text-text-primary">
        {value}
      </code>
      <CopyButton text={value} />
    </div>
  );
}
