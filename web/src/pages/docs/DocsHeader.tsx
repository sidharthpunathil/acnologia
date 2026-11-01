import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowRight } from "lucide-react";
import { useAuth } from "../../stores/auth";
import { Brand } from "../../components/Layout";

/**
 * The sticky header shared by the docs home and every doc page: brand, a
 * "docs" link back to the docs home, optional center links, and the way into
 * the dashboard.
 */
export function DocsHeader({ nav }: { nav?: ReactNode }) {
  const signedIn = useAuth((s) => s.status === "authenticated");
  return (
    <header className="sticky top-0 z-20 border-b border-line bg-canvas/90 backdrop-blur-sm">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-4 px-4 sm:px-6">
        <div className="flex min-w-0 items-center gap-3">
          <Link to="/" className="tap flex items-center" aria-label="Acnologia home">
            <Brand />
          </Link>
          <Link
            to="/docs"
            className="tap inline-flex items-center rounded-md px-1 font-mono text-xs text-faint transition-colors hover:text-ink focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ink"
          >
            docs
          </Link>
        </div>
        {nav}
        <Link
          to="/"
          className="tap inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg bg-ink px-3 text-[13px] font-medium text-canvas transition-[opacity,transform] duration-150 ease-out hover:opacity-90 active:scale-[0.97]"
        >
          {signedIn ? "Open dashboard" : "Sign in"}
          <ArrowRight className="h-3.5 w-3.5" aria-hidden />
        </Link>
      </div>
    </header>
  );
}
