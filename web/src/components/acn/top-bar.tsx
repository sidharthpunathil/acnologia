import { NavLink, Link } from "react-router-dom";
import { useAuth } from "@/stores/auth";
import { cx } from "@/utils/cx";
import { AccountMenu } from "./account-menu";
import { Logo } from "./brand";
import type { NavItem } from "./nav";

/** Sticky top bar: brand, the three daily pages and the account menu. Phones get an account chip instead. */
export function TopBar({ primary, secondary }: { primary: NavItem[]; secondary: NavItem[] }) {
  const user = useAuth((s) => s.user);
  return (
    <header className="sticky top-0 z-40 border-b border-separator-border bg-background-full/80 pt-[env(safe-area-inset-top)] backdrop-blur-sm">
      {/* Same width as main, so the logo and account pill line up with the page column. */}
      <div className="mx-auto flex h-14 max-w-[1076px] items-center justify-between gap-4 px-4 sm:px-6">
        <Logo />
        <nav aria-label="Main" className="hidden items-center gap-1 lg:flex">
          {primary.map((l) => (
            <NavLink
              key={l.to}
              to={l.to}
              end={l.end}
              className={({ isActive }) =>
                cx(
                  "inline-flex h-9 items-center rounded-full px-3.5 text-body-medium outline-none",
                  "transition-colors duration-150 ease-out focus-visible:ring-2 focus-visible:ring-border-focus-ring",
                  isActive
                    ? "bg-background-tertiary-default text-text-primary"
                    : "text-text-secondary hover:bg-background-secondary-hover hover:text-text-primary",
                )
              }
            >
              {l.label}
            </NavLink>
          ))}
          <AccountMenu items={secondary} />
        </nav>
        <Link
          to="/account"
          aria-label={`Your account: ${user?.name || user?.email || ""}`}
          className={cx(
            "inline-flex h-9 max-w-[45vw] items-center rounded-full border border-border-button-default bg-background-primary-default px-3.5 text-body-2-medium text-text-primary shadow-xs",
            "outline-none transition-colors duration-150 hover:border-border-button-hover focus-visible:ring-2 focus-visible:ring-border-focus-ring lg:hidden",
          )}
        >
          <span className="truncate">{user?.name || "Account"}</span>
        </Link>
      </div>
    </header>
  );
}
