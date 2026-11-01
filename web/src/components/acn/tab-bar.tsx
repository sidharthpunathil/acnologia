import { NavLink } from "react-router-dom";
import { RiMoreLine } from "@remixicon/react";
import { cx } from "@/utils/cx";
import type { NavItem } from "./nav";

const CELL =
  "flex min-h-[52px] flex-1 flex-col items-center justify-center gap-0.5 rounded-xl text-caption-2-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-border-focus-ring active:bg-background-tertiary-default";

/**
 * Phone tab bar: the daily pages and More stay under the thumb. It steps
 * aside while a text field has focus (index.css keys off data-typing).
 */
export function TabBar({ tabs, moreActive, onMore }: { tabs: NavItem[]; moreActive: boolean; onMore: () => void }) {
  return (
    <nav
      aria-label="Main"
      className="tabbar fixed inset-x-0 bottom-0 z-40 border-t border-separator-border bg-background-full/90 px-2 pt-1 pb-[env(safe-area-inset-bottom)] backdrop-blur-sm lg:hidden"
    >
      <ul className="mx-auto flex max-w-md items-stretch gap-1">
        {tabs.map((l) => (
          <li key={l.to} className="flex flex-1">
            <NavLink
              to={l.to}
              end={l.end}
              className={({ isActive }) => cx(CELL, isActive ? "text-text-primary" : "text-text-secondary")}
            >
              {({ isActive }) => (
                <>
                  <l.icon className={cx("size-5", isActive && "text-accent-fg")} aria-hidden />
                  {l.label}
                </>
              )}
            </NavLink>
          </li>
        ))}
        <li className="flex flex-1">
          <button
            type="button"
            onClick={onMore}
            aria-haspopup="dialog"
            className={cx(CELL, moreActive ? "text-text-primary" : "text-text-secondary")}
          >
            <RiMoreLine className={cx("size-5", moreActive && "text-accent-fg")} aria-hidden />
            More
          </button>
        </li>
      </ul>
    </nav>
  );
}
