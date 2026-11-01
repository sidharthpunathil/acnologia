import { CopyButton } from "./CopyButton";

export function CodeBlock({ code, label }: { code: string; label?: string }) {
  return (
    <div className="min-w-0 max-w-full overflow-hidden rounded-xl bg-code-bg ring-1 ring-inset ring-white/[0.06]">
      <div className="flex items-center justify-between border-b border-white/10 px-3 py-1.5">
        <span className="font-mono text-[11px] text-white/60">{label ?? ""}</span>
        <CopyButton text={code} dark />
      </div>
      <pre className="overflow-x-auto px-4 py-3 font-mono text-[12.5px] leading-relaxed text-code-ink">
        <code>{code}</code>
      </pre>
    </div>
  );
}
