import { useState } from "react";
import { Link, NavLink, Outlet } from "react-router-dom";
import { Menu, X } from "lucide-react";
import { useAuth } from "../stores/auth";
import { can } from "../lib/perms";
import { cx } from "./ui";

export function Logo() {
  return (
    <Link to="/" className="flex items-center gap-2.5">
      <span className="relative block h-7 w-7 rounded-[7px] bg-ink">
        <span className="absolute left-[7px] top-[7px] h-[7px] w-[7px] bg-white" />
        <span className="absolute left-[14px] top-[14px] h-[7px] w-[7px] bg-white" />
      </span>
      <span className="font-mono text-[15px] font-medium tracking-tight text-ink">cogito</span>
    </Link>
  );
}

export function Layout() {
  const user = useAuth((s) => s.user);
  const role = user?.role;
  const [open, setOpen] = useState(false);

  const links = [
    { to: "/", label: "Catalog", show: true, end: true },
    { to: "/apps", label: "Apps", show: true },
    { to: "/activity", label: "Activity", show: true },
    { to: "/users", label: "Users", show: can.users(role) },
    { to: "/audit", label: "Audit", show: can.audit(role) },
  ].filter((l) => l.show);

  const linkCls = ({ isActive }: { isActive: boolean }) =>
    cx("text-[14px] transition-colors", isActive ? "font-medium text-ink" : "text-muted hover:text-ink");

  return (
    <div className="min-h-screen bg-canvas">
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <header className="sticky top-0 z-40 border-b border-line bg-canvas/90 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-[1240px] items-center justify-between px-4 sm:px-6">
          <Logo />
          <nav className="hidden items-center gap-6 md:flex">
            {links.map((l) => (
              <NavLink key={l.to} to={l.to} end={l.end} className={linkCls}>
                {l.label}
              </NavLink>
            ))}
            <NavLink
              to="/account"
              className="inline-flex h-9 items-center rounded-full bg-ink px-4 text-[13px] font-medium text-white hover:bg-black"
            >
              {user?.name || "Account"}
            </NavLink>
          </nav>
          <button
            type="button"
            className="rounded-lg p-2 text-ink md:hidden"
            aria-label={open ? "Close menu" : "Open menu"}
            onClick={() => setOpen(!open)}
          >
            {open ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
          </button>
        </div>
        {open && (
          <nav className="border-t border-line px-4 py-3 md:hidden">
            {[...links, { to: "/account", label: "Account", end: false }].map((l) => (
              <NavLink
                key={l.to}
                to={l.to}
                end={l.end}
                onClick={() => setOpen(false)}
                className={({ isActive }) =>
                  cx("block rounded-lg px-2 py-2.5 text-[15px]", isActive ? "font-medium text-ink" : "text-muted")
                }
              >
                {l.label}
              </NavLink>
            ))}
          </nav>
        )}
      </header>
      <main id="main" tabIndex={-1} className="mx-auto max-w-[1076px] focus:outline-none px-4 pb-24 pt-10 sm:px-6 sm:pt-14">
        <Outlet />
      </main>
    </div>
  );
}
