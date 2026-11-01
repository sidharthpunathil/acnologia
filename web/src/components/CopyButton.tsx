import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { toast } from "../stores/toast";
import { cx } from "./ui";

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Fallback for non secure contexts (plain http on a LAN host).
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

export function CopyButton({ text, label = "Copy", className, dark }: { text: string; label?: string; className?: string; dark?: boolean }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        const ok = await copyText(text);
        if (ok) {
          setDone(true);
          window.setTimeout(() => setDone(false), 1500);
        } else {
          toast.error("Could not copy. Select the text and copy it yourself.");
        }
      }}
      className={cx(
        "tap inline-flex shrink-0 items-center gap-1.5 rounded-lg px-2 py-1 text-xs font-medium transition-colors",
        dark ? "text-white/70 hover:bg-white/10 hover:text-white" : "text-muted hover:bg-ink/[0.06] hover:text-ink",
        className,
      )}
    >
      {done ? (
        <span key="done" className="swap-in inline-flex items-center gap-1.5">
          <Check className="h-3.5 w-3.5" />
          Copied
        </span>
      ) : (
        <>
          <Copy className="h-3.5 w-3.5" />
          {label}
        </>
      )}
    </button>
  );
}

export function SecretBox({ value }: { value: string }) {
  return (
    <div className="flex items-center gap-2 rounded-xl border border-line bg-canvas px-3 py-2.5">
      <code className="min-w-0 flex-1 break-all font-mono text-[13px] text-ink">{value}</code>
      <CopyButton text={value} />
    </div>
  );
}
