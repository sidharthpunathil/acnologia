import type { ReactNode } from "react";
import { BRAND_TITLE, TAGLINE, useTitle } from "@/hooks/useTitle";
import { BRAND_NAME, Mark } from "@/components/acn/brand";

/** Centered auth page: the dragon, the name and tagline, then the card. */
export function AuthShell({ meta, title, children }: { meta: string; title?: string; children: ReactNode }) {
  useTitle(title ?? null);
  return (
    <div className="flex min-h-dvh flex-col bg-background-full">
      <main className="fade-up mx-auto flex w-full max-w-[440px] flex-1 flex-col items-center px-4 pt-[calc(3.5rem+env(safe-area-inset-top))] pb-16 sm:pt-20">
        <div className="text-center">
          <Mark size={72} className="mx-auto shadow-[0_10px_30px_-8px_color-mix(in_srgb,var(--color-accent-fill)_45%,transparent),0_2px_6px_rgb(0_0_0/0.12)]" />
          <p className="mt-5 font-mono text-title-1-medium text-text-primary">{BRAND_NAME}</p>
          <p className="mt-1.5 font-mono text-body-regular text-text-primary">{TAGLINE}</p>
          <p className="mt-1 font-mono text-caption-1-regular text-text-secondary">{meta}</p>
        </div>
        <div className="mt-10 flex w-full justify-center">{children}</div>
      </main>
      <footer className="pb-[max(1.5rem,env(safe-area-inset-bottom))] text-center font-mono text-caption-2-regular text-text-tertiary">
        {BRAND_TITLE}
      </footer>
    </div>
  );
}
