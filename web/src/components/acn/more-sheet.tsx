import { NavLink } from "react-router-dom";
import { RiLogoutBoxRLine } from "@remixicon/react";
import { Drawer, DrawerBody, DrawerHeader } from "@/components/base/drawer/drawer";
import { useAuth } from "@/stores/auth";
import { cx } from "@/utils/cx";
import { grouped, menuRowKeys, useSignOut, type NavItem } from "./nav";
import { ThemePicker } from "./theme-picker";

/** Phone overflow sheet: the rest of the nav, theme, account and sign out, within thumb reach. */
export function MoreSheet({ open, onClose, items }: { open: boolean; onClose: () => void; items: NavItem[] }) {
  const user = useAuth((s) => s.user);
  const signOut = useSignOut();
  return (
    <Drawer placement="bottom" isOpen={open} onOpenChange={(o) => !o && onClose()} className="pb-[max(1.5rem,env(safe-area-inset-bottom))]">
      <DrawerHeader title="More" description={user ? `${user.name || user.email} · ${user.role}` : undefined} />
      <DrawerBody className="pb-1">
        <nav aria-label="More" className="flex flex-col gap-4" onKeyDown={menuRowKeys}>
          {grouped(items).map(([group, list]) => (
            <div key={group}>
              <div className="mb-1 text-caption-2-semibold uppercase tracking-wider text-text-tertiary">{group}</div>
              <ul className="-mx-2 flex flex-col">
                {list.map((l) => (
                  <li key={l.to}>
                    <NavLink
                      to={l.to}
                      end={l.end}
                      data-menu-row=""
                      onClick={onClose}
                      className={({ isActive }) =>
                        cx(
                          "flex min-h-12 items-center gap-3 rounded-xl px-3 text-body-medium outline-none transition-colors duration-150",
                          "focus-visible:ring-2 focus-visible:ring-border-focus-ring active:bg-background-tertiary-default",
                          isActive ? "bg-background-tertiary-default text-text-primary" : "text-text-primary",
                        )
                      }
                    >
                      {({ isActive }) => (
                        <>
                          <l.icon className={cx("size-5", isActive ? "text-accent-fg" : "text-foreground-icon-secondary")} aria-hidden />
                          {l.label}
                        </>
                      )}
                    </NavLink>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
        <div className="mt-4 border-t border-separator-border pt-4">
          <div className="mb-2 text-caption-1-medium text-text-secondary">Theme</div>
          <ThemePicker />
        </div>
        <button
          type="button"
          onClick={() => {
            onClose();
            void signOut();
          }}
          className={cx(
            "mt-4 -mx-2 flex min-h-12 w-[calc(100%+1rem)] items-center gap-3 rounded-xl px-3 text-start text-body-medium text-text-error-primary",
            "outline-none transition-colors duration-150 hover:bg-status-rose-background focus-visible:ring-2 focus-visible:ring-border-focus-ring active:bg-status-rose-background",
          )}
        >
          <RiLogoutBoxRLine className="size-5" aria-hidden />
          Sign out
        </button>
      </DrawerBody>
    </Drawer>
  );
}
