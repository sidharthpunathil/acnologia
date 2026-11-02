import { NavLink } from "react-router-dom";
import { RiLockLine, RiLogoutBoxRLine } from "@remixicon/react";
import { Drawer, DrawerBody, DrawerHeader } from "@/components/base/drawer/drawer";
import { useAuth } from "@/stores/auth";
import { useGate } from "@/stores/gate";
import { cx } from "@/utils/cx";
import { ACCOUNT_ITEM, SIGN_OUT_ROW, grouped, menuRowKeys, useSignOut, type NavItem } from "./nav";
import { ThemePicker } from "./theme-picker";

const ROW = cx(
  "flex min-h-12 items-center gap-3 rounded-xl px-3 text-body-medium outline-none transition-colors duration-150",
  "focus-visible:ring-2 focus-visible:ring-border-focus-ring active:bg-background-tertiary-default",
);

/** One page row in the sheet. */
function SheetLink({ item, onClose }: { item: NavItem; onClose: () => void }) {
  return (
    <NavLink
      to={item.to}
      end={item.end}
      data-menu-row=""
      onClick={onClose}
      className={({ isActive }) => cx(ROW, "text-text-primary", isActive && "bg-background-tertiary-default")}
    >
      {({ isActive }) => (
        <>
          <item.icon className={cx("size-5", isActive ? "text-accent-fg" : "text-foreground-icon-secondary")} aria-hidden />
          {item.label}
        </>
      )}
    </NavLink>
  );
}

/**
 * Phone overflow sheet, in the account menu's order: the rest of the nav,
 * theme, your account, then sign out, within thumb reach.
 */
export function MoreSheet({ open, onClose, items }: { open: boolean; onClose: () => void; items: NavItem[] }) {
  const user = useAuth((s) => s.user);
  const signOut = useSignOut();
  const gateOn = useGate((s) => s.required);
  const lockBrowser = useGate((s) => s.lock);
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
                    <SheetLink item={l} onClose={onClose} />
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
        <div className="mt-4 border-t border-separator-border pt-2">
          <div className="-mx-2">
            <SheetLink item={ACCOUNT_ITEM} onClose={onClose} />
          </div>
        </div>
        <div className="mt-2 border-t border-separator-border pt-2">
          <button
            type="button"
            onClick={() => {
              onClose();
              void signOut();
            }}
            className={cx(ROW, SIGN_OUT_ROW, "-mx-2 w-[calc(100%+1rem)] text-start hover:bg-background-tertiary-default")}
          >
            <RiLogoutBoxRLine className="size-5 text-foreground-icon-secondary" aria-hidden />
            Sign out
          </button>
          {gateOn && (
            <button
              type="button"
              onClick={() => {
                onClose();
                void lockBrowser();
              }}
              className={cx(ROW, SIGN_OUT_ROW, "-mx-2 w-[calc(100%+1rem)] text-start hover:bg-background-tertiary-default")}
            >
              <RiLockLine className="size-5 text-foreground-icon-secondary" aria-hidden />
              Lock this browser
            </button>
          )}
        </div>
      </DrawerBody>
    </Drawer>
  );
}
