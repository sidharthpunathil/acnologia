import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { RiArrowRightLine } from "@remixicon/react";
import { useAuth } from "@/stores/auth";
import { Brand } from "@/components/acn/brand";
import { RouterButton } from "./RouterButton";

/**
 * The sticky header shared by the docs home and every doc page: brand, a
 * "docs" link back to the docs home, optional center links, and the way into
 * the dashboard.
 */
export function DocsHeader({ nav }: { nav?: ReactNode }) {
  const signedIn = useAuth((s) => s.status === "authenticated");
  return (
    <header className="sticky top-0 z-20 border-b border-separator-border bg-background-full/85 backdrop-blur-sm">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-4 px-4 sm:px-6">
        <div className="flex min-w-0 items-center gap-3">
          <Link
            to="/"
            className="-mx-1.5 flex items-center rounded-xl px-1.5 py-1 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
            aria-label="Acnologia home"
          >
            <Brand />
          </Link>
          <Link
            to="/docs"
            className="inline-flex min-h-8 items-center rounded-md pointer-coarse:min-h-11 px-1.5 font-mono text-caption-1-regular text-text-tertiary outline-none transition-colors duration-150 hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          >
            docs
          </Link>
        </div>
        {nav}
        <RouterButton to="/" size="small" trailingIcon={RiArrowRightLine} className="shrink-0">
          {signedIn ? "Open dashboard" : "Sign in"}
        </RouterButton>
      </div>
    </header>
  );
}
