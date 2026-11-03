// Pages for a person a share was made for. They render outside the access
// gate and the signed in shell: no account, no session, only the claim code
// or the person's own token.
import type { ReactNode } from "react";
import { Route, Routes, useParams } from "react-router-dom";
import { Toaster } from "../../components/Toaster";
import { BRAND_NAME, Mark } from "@/components/acn/brand";
import { ThemePicker } from "@/components/acn/theme-picker";
import { ClaimPage } from "./ClaimPage";
import { MePage } from "./MePage";

/** /claim/<code> and /me. */
export function isPublicPath(pathname: string): boolean {
  return /^\/claim\/[^/]+\/?$/.test(pathname) || /^\/me\/?$/.test(pathname);
}

export function PublicRoutes() {
  return (
    <>
      <Routes>
        <Route path="/claim/:code" element={<KeyedClaim />} />
        <Route path="/me" element={<MePage />} />
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
