import type { ReactNode } from "react";
import { BRAND_TITLE, TAGLINE, useTitle } from "../hooks/useTitle";
import { BRAND_NAME, Mark } from "../components/Layout";

export function AuthShell({ title, meta, children }: { title: string; meta: string; children: ReactNode }) {
  useTitle(null);
  return (
    <div className="flex min-h-dvh flex-col bg-canvas">
      <main className="mx-auto w-full max-w-[420px] flex-1 px-4 pb-16 pt-[calc(4rem+env(safe-area-inset-top))] sm:pt-24">
        <div className="text-center">
          <Mark size={72} className="mx-auto rounded-[22%] shadow-[0_10px_30px_-8px_rgb(var(--brand)/0.45),0_2px_6px_rgba(0,0,0,0.12)]" />
          <p className="mt-5 font-mono text-2xl font-medium tracking-tight text-ink">{BRAND_NAME}</p>
          <p className="mt-2 font-mono text-[15px] tracking-tight text-ink">{TAGLINE}</p>
          <p className="mt-1 font-mono text-xs text-muted">{meta}</p>
        </div>
        <div className="mt-10 rounded-card bg-surface p-5 shadow-card sm:p-6">
          <h1 className="mb-5 text-[17px] font-semibold text-ink">{title}</h1>
          {children}
        </div>
      </main>
      <footer className="pb-[max(1.5rem,env(safe-area-inset-bottom))] text-center font-mono text-[11px] text-faint">{BRAND_TITLE}</footer>
    </div>
  );
}
