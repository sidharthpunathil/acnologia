import { CopyButton } from "./CopyButton";

export function CodeBlock({ code, label }: { code: string; label?: string }) {
  return (
    <div className="overflow-hidden rounded-xl bg-[#1b1b1d]">
      <div className="flex items-center justify-between border-b border-white/10 px-3 py-1.5">
        <span className="font-mono text-[11px] text-white/50">{label ?? ""}</span>
        <CopyButton text={code} dark />
      </div>
      <pre className="overflow-x-auto px-4 py-3 font-mono text-[12.5px] leading-relaxed text-[#ececec]">
        <code>{code}</code>
      </pre>
    </div>
  );
}
