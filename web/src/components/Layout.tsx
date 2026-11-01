import { useEffect, useRef, useState } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router-dom";
import { Menu, X } from "lucide-react";
import { useAuth } from "../stores/auth";
import { can } from "../lib/perms";
import { BRAND_TITLE, TAGLINE } from "../hooks/useTitle";
import { cx } from "./ui";

export function Logo() {
  return (
    <Link to="/" className="tap flex items-center" aria-label="Cogito home">
      {/* 64px tall source, shown at 28px for sharp rendering on retina screens */}
      <img src="/logo.png" alt="Cogito" width={52} height={28} className="h-7 w-auto select-none" draggable={false} />
    </Link>
  );
}

export function Layout() {
  const user = useAuth((s) => s.user);
  const role = user?.role;
  const [open, setOpen] = useState(false);
  const location = useLocation();
  const main = useRef<HTMLElement>(null);
  const first = useRef(true);

  const links = [
    { to: "/", label: "Catalog", show: true, end: true },
    { to: "/providers", label: "Providers", show: true },
    { to: "/apps", label: "Apps", show: true },
    { to: "/activity", label: "Activity", show: true },
    { to: "/settings", label: "Settings", show: true },
    { to: "/users", label: "Users", show: can.users(role) },
    { to: "/audit", label: "Audit", show: can.audit(role) },
  ].filter((l) => l.show);

  // Close the mobile menu on navigation, and move focus to the new page for screen readers.
  useEffect(() => {
    setOpen(false);
    if (first.current) {
      first.current = false;
      return;
    }
    window.scrollTo(0, 0);
    main.current?.focus({ preventScroll: true });
  }, [location.pathname]);

  useEffect(() => {
    if (!open) return;
    const h = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("keydown", h);
    return () => document.removeEventListener("keydown", h);
  }, [open]);

  const linkCls = ({ isActive }: { isActive: boolean }) =>
    cx(
      "tap relative inline-flex h-9 items-center rounded-full px-3 text-[14px] transition-colors",
      isActive ? "bg-black/[0.05] font-medium text-ink" : "text-muted hover:text-ink",
    );

  return (
    <div className="flex min-h-dvh flex-col bg-canvas">
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <header className="sticky top-0 z-40 border-b border-line bg-canvas/90 backdrop-blur supports-[backdrop-filter]:bg-canvas/75">
        <div className="mx-auto flex h-14 max-w-[1240px] items-center justify-between gap-4 px-4 sm:px-6">
          <Logo />
          <nav aria-label="Main" className="hidden items-center gap-1 lg:flex">
            {links.map((l) => (
              <NavLink key={l.to} to={l.to} end={l.end} className={linkCls}>
                {l.label}
              </NavLink>
            ))}
            <NavLink
              to="/account"
              className="tap ml-2 inline-flex h-9 max-w-[180px] items-center truncate rounded-full bg-ink px-4 text-[13px] font-medium text-white hover:bg-black"
              title={user ? `${user.email} · ${user.role}` : undefined}
            >
              {user?.name || "Account"}
            </NavLink>
          </nav>
          <button
            type="button"
            className="icon-btn inline-flex h-10 w-10 items-center justify-center rounded-lg text-ink hover:bg-black/[0.05] lg:hidden"
            aria-label={open ? "Close menu" : "Open menu"}
            aria-expanded={open}
            aria-controls="mobile-nav"
            onClick={() => setOpen(!open)}
          >
            {open ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
          </button>
        </div>
        {open && (
          <nav id="mobile-nav" aria-label="Main" className="fade-up border-t border-line px-4 py-2 lg:hidden">
            {[...links, { to: "/account", label: user?.name ? `Account · ${user.name}` : "Account", end: false }].map((l) => (
              <NavLink
                key={l.to}
                to={l.to}
                end={l.end}
                className={({ isActive }) =>
                  cx("flex min-h-[44px] items-center rounded-lg px-2 text-[15px]", isActive ? "bg-black/[0.04] font-medium text-ink" : "text-muted")
                }
              >
                {l.label}
              </NavLink>
            ))}
          </nav>
        )}
      </header>
      <main id="main" ref={main} tabIndex={-1} className="mx-auto w-full max-w-[1076px] flex-1 px-4 pb-24 pt-10 focus:outline-none sm:px-6 sm:pt-14">
        <Outlet />
      </main>
      <footer className="border-t border-line">
        <div className="mx-auto flex max-w-[1076px] flex-col gap-1 px-4 py-6 font-mono text-[11px] text-faint sm:flex-row sm:items-center sm:justify-between sm:px-6">
          <span title={BRAND_TITLE}>
            <span className="text-brand">Cogito</span> · {TAGLINE}
          </span>
          <span>self-hosted API key pool</span>
        </div>
      </footer>
    </div>
  );
}
