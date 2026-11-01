import { cx } from "@/utils/cx";
import { CopyButton } from "./copy-button";

export interface CodeBlockProps {
  code: string;
  /** Small mono caption in the header row, such as "curl" or a file name. */
  label?: string;
  className?: string;
}

/** Dark code panel with a label row and a copy button. Scrolls sideways, never the page. */
export function CodeBlock({ code, label, className }: CodeBlockProps) {
  return (
    <div className={cx("min-w-0 max-w-full overflow-hidden rounded-2xl bg-code-bg", className)}>
      <div className="flex items-center justify-between gap-2 border-b border-code-ink/10 py-1 ps-4 pe-1">
        <span className="truncate font-mono text-caption-1-regular text-code-ink/60">{label ?? ""}</span>
        <CopyButton text={code} size="xs" onCode />
      </div>
      <pre dir="ltr" className="overflow-x-auto px-4 py-3 font-mono text-body-2-regular text-code-ink">
        <code>{code}</code>
      </pre>
    </div>
  );
}
