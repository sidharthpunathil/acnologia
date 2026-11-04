// Pages for a person a share was made for, and the public home page. They
// render outside the access gate and the signed in shell: no account, no
// session, only the claim code or the person's own token.
import type { ReactNode } from "react";
import { Route, Routes, useParams } from "react-router-dom";
import { Toaster } from "../../components/Toaster";
import { BRAND_NAME, Mark } from "@/components/acn/brand";
import { ThemePicker } from "@/components/acn/theme-picker";
import { lazyPage, preloadRoute, type PageRoute } from "@/lazyPage";
// The same hashed file landing.css asks for: Vite names assets by content.
import landingHeadlineFont from "@fontsource-variable/familjen-grotesk/files/familjen-grotesk-latin-wght-normal.woff2?url";

/**
 * Starts the landing headline's font with the landing chunk. Left to the
 * stylesheet, the browser finds it only after the chunk, its CSS and the
 * first layout, one more round trip before the largest text can paint.
 */
function preloadLandingFont() {
  if (document.querySelector(`link[rel="preload"][href="${landingHeadlineFont}"]`)) return;
  const link = document.createElement("link");
  link.rel = "preload";
  link.as = "font";
  link.type = "font/woff2";
  link.crossOrigin = "anonymous";
  link.href = landingHeadlineFont;
  document.head.appendChild(link);
}

// Each its own chunk: a visitor to the landing page never downloads the
// dashboard, and a claim link never downloads the landing page.
const ClaimPage = lazyPage(() => import("./ClaimPage").then((m) => m.ClaimPage));
const MePage = lazyPage(() => import("./MePage").then((m) => m.MePage));
const LandingPage = lazyPage(() => {
  preloadLandingFont();
  return import("../landing/Landing").then((m) => m.LandingPage);
});

const PAGES: readonly PageRoute[] = [
  ["/claim/:code", ClaimPage],
  ["/me", MePage],
  ["/landing", LandingPage],
];

/** /claim/<code>, /me and the public home page, /landing. */
export function isPublicPath(pathname: string): boolean {
  return /^\/claim\/[^/]+\/?$/.test(pathname) || /^\/me\/?$/.test(pathname) || /^\/landing\/?$/.test(pathname);
}

/** Fetches the chunk of the public page at `pathname` so the first render does not wait on it. */
export function preloadPublicPage(pathname: string): Promise<void> {
  return preloadRoute(PAGES, pathname);
}

export function PublicRoutes() {
  return (
    <>
      <Routes>
        <Route path="/claim/:code" element={<KeyedClaim />} />
        <Route path="/me" element={<MePage />} />
        <Route path="/landing" element={<LandingPage />} />
      </Routes>
      <Toaster />
    </>
  );
}

/** A new code starts a fresh page, so nothing shown for one link carries over to another. */
function KeyedClaim() {
  const { code = "" } = useParams();
  return <ClaimPage key={code} />;
}

/** A narrow page with the brand on top and the theme choice at the bottom. */
export function PublicShell({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col bg-background-full">
      <header className="mx-auto flex w-full max-w-2xl items-center gap-2 px-4 pt-[calc(1.25rem+env(safe-area-inset-top))] pb-2">
        <Mark size={28} />
        <span className="font-mono text-body-medium text-text-primary">{BRAND_NAME}</span>
      </header>
      <main className="mx-auto flex w-full max-w-2xl flex-1 flex-col gap-4 px-4 pt-4 pb-10">{children}</main>
      <footer className="mx-auto w-full max-w-2xl px-4 pb-[max(1.5rem,env(safe-area-inset-bottom))]">
        <div className="mx-auto max-w-xs">
          <ThemePicker />
        </div>
      </footer>
    </div>
  );
}
