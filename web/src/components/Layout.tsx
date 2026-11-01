import { useEffect, useRef, useState } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import {
  Activity,
  Blocks,
  Boxes,
  ChevronDown,
  KeyRound,
  LayoutGrid,
  LogOut,
  MoreHorizontal,
  Settings,
  ShieldCheck,
  UserRound,
  Users,
  type LucideIcon,
} from "lucide-react";
import { useAuth } from "../stores/auth";
import { can } from "../lib/perms";
import { BRAND_TITLE, TAGLINE } from "../hooks/useTitle";
import { Modal } from "./Modal";
import { ThemePicker } from "./ThemePicker";
import { cx } from "./ui";

/** The red Cogito wordmark, tuned per theme so it reads on light and dark. */
export function Wordmark({ className }: { className?: string }) {
  return <span role="img" aria-label="Cogito" className={cx("wordmark", className)} />;
}

export function Logo() {
  return (
    <Link to="/" className="tap flex items-center" aria-label="Cogito home">
      <Wordmark className="h-7" />
    </Link>
  );
}

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
}

function useSignOut() {
  const logout = useAuth((s) => s.logout);
  const navigate = useNavigate();
  return async () => {
    await logout();
    navigate("/login", { replace: true });
  };
}

/** Desktop account menu: who you are, theme, account and sign out. */
function AccountMenu() {
  const user = useAuth((s) => s.user);
  const signOut = useSignOut();
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const location = useLocation();

  useEffect(() => setOpen(false), [location.pathname]);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (!wrap.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        trigger.current?.focus();
      }
    };
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div ref={wrap} className="relative ml-2">
      <button
        ref={trigger}
        type="button"
        aria-haspopup="true"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="tap inline-flex h-9 max-w-[200px] items-center gap-1.5 rounded-full bg-ink pl-4 pr-3 text-[13px] font-medium text-on-ink transition-[background-color,transform] duration-150 ease-out hover:bg-ink-strong active:scale-[0.98]"
      >
        <span className="truncate">{user?.name || "Account"}</span>
        <ChevronDown className={cx("h-3.5 w-3.5 shrink-0 transition-transform duration-150", open && "rotate-180")} aria-hidden="true" />
      </button>
      {open && (
        <div className="pop-in absolute right-0 top-11 z-50 w-72 rounded-2xl bg-surface p-2 shadow-pop">
          <div className="px-3 pb-3 pt-2">
            <div className="truncate text-[14px] font-medium text-ink">{user?.name}</div>
            <div className="truncate font-mono text-xs text-muted">
              {user?.email} · {user?.role}
            </div>
          </div>
          <div className="border-t border-line px-3 py-3">
            <div className="mb-2 text-xs font-medium text-muted">Theme</div>
            <ThemePicker size="sm" className="w-full" />
          </div>
          <div className="border-t border-line pt-1">
            <Link to="/account" className="tap flex h-10 items-center gap-2.5 rounded-lg px-3 text-[14px] text-ink hover:bg-soft">
              <UserRound className="h-4 w-4 text-muted" aria-hidden="true" /> Your account
            </Link>
            <button type="button" onClick={() => void signOut()} className="tap flex h-10 w-full items-center gap-2.5 rounded-lg px-3 text-left text-[14px] text-ink hover:bg-soft">
              <LogOut className="h-4 w-4 text-muted" aria-hidden="true" /> Sign out
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

/** Phone overflow sheet: the rest of the nav, theme and sign out, all within thumb reach. */
function MoreSheet({ open, onClose, items }: { open: boolean; onClose: () => void; items: NavItem[] }) {
  const user = useAuth((s) => s.user);
  const signOut = useSignOut();
  return (
    <Modal open={open} onClose={onClose} title="More" description={user ? `${user.name || user.email} · ${user.role}` : undefined}>
      <nav aria-label="More">
        <ul className="-mx-2">
          {items.map((l) => (
            <li key={l.to}>
              <NavLink
                to={l.to}
                onClick={onClose}
                className={({ isActive }) =>
                  cx("flex min-h-[48px] items-center gap-3 rounded-xl px-3 text-[15px]", isActive ? "bg-ink/[0.06] font-medium text-ink" : "text-ink")
                }
              >
                <l.icon className="h-[18px] w-[18px] text-muted" aria-hidden="true" />
                {l.label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
      <div className="mt-4 border-t border-line pt-4">
        <div className="mb-2 text-xs font-medium text-muted">Theme</div>
        <ThemePicker className="w-full" />
      </div>
      <button
        type="button"
        onClick={() => {
          onClose();
          void signOut();
        }}
        className="tap mt-4 flex min-h-[48px] w-full items-center gap-3 rounded-xl px-3 text-left text-[15px] text-red-700 hover:bg-red-50"
      >
        <LogOut className="h-[18px] w-[18px]" aria-hidden="true" /> Sign out
      </button>
    </Modal>
  );
}

export function Layout() {
  const user = useAuth((s) => s.user);
  const role = user?.role;
  const [more, setMore] = useState(false);
  const location = useLocation();
  const main = useRef<HTMLElement>(null);
  const first = useRef(true);

  const all: Array<NavItem & { show: boolean }> = [
    { to: "/", label: "Catalog", icon: LayoutGrid, show: true, end: true },
    { to: "/keys", label: "Keys", icon: KeyRound, show: true },
    { to: "/providers", label: "Providers", icon: Blocks, show: true },
    { to: "/apps", label: "Apps", icon: Boxes, show: true },
    { to: "/activity", label: "Activity", icon: Activity, show: true },
    { to: "/settings", label: "Settings", icon: Settings, show: true },
    { to: "/users", label: "Users", icon: Users, show: can.users(role) },
    { to: "/audit", label: "Audit", icon: ShieldCheck, show: can.audit(role) },
  ];
  const links = all.filter((l) => l.show);
  // Four destinations people use most sit in the tab bar; the rest live under More.
  const TAB_PATHS = ["/", "/keys", "/providers", "/activity"];
  const tabs = links.filter((l) => TAB_PATHS.includes(l.to));
  const overflow: NavItem[] = [...links.filter((l) => !TAB_PATHS.includes(l.to)), { to: "/account", label: "Your account", icon: UserRound }];
  const moreActive = overflow.some((l) => location.pathname.startsWith(l.to));

  // While a text field has focus the keyboard is up: hide the tab bar so it does not
  // cover the field (CSS keys off data-typing, on touch screens only).
  useEffect(() => {
    const isText = (el: Element | null) =>
      !!el && (el.tagName === "TEXTAREA" || (el.tagName === "INPUT" && !/^(checkbox|radio|button|submit|reset|file|color|range)$/i.test((el as HTMLInputElement).type)));
    const root = document.documentElement;
    const onIn = (e: FocusEvent) => {
      if (isText(e.target as Element)) root.setAttribute("data-typing", "");
    };
    // Focus may move straight to another field, so check after it settles.
    const onOut = () => window.setTimeout(() => !isText(document.activeElement) && root.removeAttribute("data-typing"), 0);
    document.addEventListener("focusin", onIn);
    document.addEventListener("focusout", onOut);
    return () => {
      document.removeEventListener("focusin", onIn);
      document.removeEventListener("focusout", onOut);
      root.removeAttribute("data-typing");
    };
  }, []);

  // Close the sheet on navigation, and move focus to the new page for screen readers.
  useEffect(() => {
    setMore(false);
    if (first.current) {
      first.current = false;
      return;
    }
    window.scrollTo(0, 0);
    main.current?.focus({ preventScroll: true });
  }, [location.pathname]);

  const linkCls = ({ isActive }: { isActive: boolean }) =>
    cx(
      "tap relative inline-flex h-9 items-center rounded-full px-3 text-[14px] transition-colors",
      isActive ? "bg-ink/[0.06] font-medium text-ink" : "text-muted hover:text-ink",
    );

  const tabCls = (active: boolean) =>
    cx(
      "flex min-h-[52px] flex-1 flex-col items-center justify-center gap-0.5 rounded-xl text-[11px] transition-colors active:bg-ink/[0.06]",
      active ? "font-medium text-ink" : "text-muted",
    );

  return (
    <div className="flex min-h-dvh flex-col bg-canvas pb-[var(--tabbar-h)]">
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <header className="sticky top-0 z-40 border-b border-line bg-canvas/90 pt-[env(safe-area-inset-top)] backdrop-blur supports-[backdrop-filter]:bg-canvas/75">
        <div className="mx-auto flex h-14 max-w-[1240px] items-center justify-between gap-4 px-4 sm:px-6">
          <Logo />
          <nav aria-label="Main" className="hidden items-center gap-1 lg:flex">
            {links.map((l) => (
              <NavLink key={l.to} to={l.to} end={l.end} className={linkCls}>
                {l.label}
              </NavLink>
            ))}
            <AccountMenu />
          </nav>
          <Link
            to="/account"
            className="tap inline-flex h-9 max-w-[45vw] items-center truncate rounded-full bg-ink/[0.06] px-3.5 text-[13px] font-medium text-ink lg:hidden"
            aria-label={`Your account: ${user?.name || user?.email || ""}`}
          >
            <span className="truncate">{user?.name || "Account"}</span>
          </Link>
        </div>
      </header>
      <main id="main" ref={main} tabIndex={-1} className="mx-auto w-full max-w-[1076px] flex-1 px-4 pb-24 pt-8 focus:outline-none sm:px-6 sm:pt-14">
        <Outlet />
      </main>
      <footer className="border-t border-line">
        <div className="mx-auto flex max-w-[1076px] flex-col gap-1 px-4 py-6 font-mono text-[11px] text-faint sm:flex-row sm:items-center sm:justify-between sm:px-6">
          <span title={BRAND_TITLE}>
            <span className="text-brand">Cogito</span> · {TAGLINE}
          </span>
          <span>Self-hosted API key pool</span>
        </div>
      </footer>

      {/* Phone tab bar: the main destinations stay under the thumb. */}
      <nav
        aria-label="Main"
        className="tabbar fixed inset-x-0 bottom-0 z-40 border-t border-line bg-canvas/95 px-2 pb-[env(safe-area-inset-bottom)] pt-1 backdrop-blur supports-[backdrop-filter]:bg-canvas/85 lg:hidden"
      >
        <ul className="mx-auto flex max-w-md items-stretch gap-1">
          {tabs.map((l) => (
            <li key={l.to} className="flex flex-1">
              <NavLink to={l.to} end={l.end} className={({ isActive }) => tabCls(isActive)}>
                {({ isActive }) => (
                  <>
                    <l.icon className="h-5 w-5" strokeWidth={isActive ? 2.25 : 1.75} aria-hidden="true" />
                    {l.label}
                  </>
                )}
              </NavLink>
            </li>
          ))}
          <li className="flex flex-1">
            <button type="button" onClick={() => setMore(true)} aria-haspopup="dialog" className={tabCls(moreActive)}>
              <MoreHorizontal className="h-5 w-5" strokeWidth={moreActive ? 2.25 : 1.75} aria-hidden="true" />
              More
            </button>
          </li>
        </ul>
      </nav>
      <MoreSheet open={more} onClose={() => setMore(false)} items={overflow} />
    </div>
  );
}
