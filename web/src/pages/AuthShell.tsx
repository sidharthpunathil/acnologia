import type { ReactNode } from "react";
import { Logo } from "../components/Layout";

export function AuthShell({ title, meta, children }: { title: string; meta: string; children: ReactNode }) {
  return (
    <div className="min-h-screen bg-canvas">
      <header className="border-b border-line">
        <div className="mx-auto flex h-14 max-w-[1240px] items-center px-4 sm:px-6">
          <Logo />
        </div>
      </header>
      <main className="mx-auto max-w-[420px] px-4 pt-16 sm:pt-24">
        <h1 className="font-mono text-[26px] tracking-tight text-ink">{title}</h1>
        <p className="mt-2 font-mono text-xs text-muted">{meta}</p>
        <div className="mt-8 rounded-card bg-white p-5 shadow-card sm:p-6">{children}</div>
      </main>
    </div>
  );
}
