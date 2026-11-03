import { Button, Disclosure, DisclosureGroup, DisclosurePanel, Heading } from "react-aria-components";
import { NavLink, useLocation } from "react-router-dom";
import { RiArrowDownSLine, RiLockLine, RiLogoutBoxRLine } from "@remixicon/react";
import { Drawer, DrawerBody, DrawerHeader } from "@/components/base/drawer/drawer";
import { useAuth } from "@/stores/auth";
import { useGate } from "@/stores/gate";
import { cx } from "@/utils/cx";
import { ACCOUNT_ITEM, NAV_GROUP_ICON, SIGN_OUT_ROW, activeGroup, grouped, isActivePath, menuRowKeys, useSignOut, type NavItem } from "./nav";
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
          <item.icon className={cx("size-5 shrink-0", isActive ? "text-accent-fg" : "text-foreground-icon-secondary")} aria-hidden />
          <span className="min-w-0 flex-1 truncate">{item.label}</span>
          {isActive && <span className="size-1.5 shrink-0 rounded-full bg-accent-fill" aria-hidden />}
        </>
      )}
    </NavLink>
  );
}

/**
 * Phone overflow sheet, in the account menu's order: the groups of pages,
 * theme, your account, then sign out, within thumb reach. Flyouts have no
 * room here, so each group is an accordion row that expands its pages in
 * place, one at a time. The open page's group starts expanded.
 */
export function MoreSheet({ open, onClose, items }: { open: boolean; onClose: () => void; items: NavItem[] }) {
  const user = useAuth((s) => s.user);
  const signOut = useSignOut();
  const gateOn = useGate((s) => s.required);
  const passwordOnly = useGate((s) => s.passwordOnly);
  const lockBrowser = useGate((s) => s.lock);
  const { pathname } = useLocation();
  const groups = grouped(items);
  const openGroup = activeGroup(items, pathname);
  return (
    <Drawer placement="bottom" isOpen={open} onOpenChange={(o) => !o && onClose()} className="pb-[max(1.5rem,env(safe-area-inset-bottom))]">
      <DrawerHeader title="More" description={user ? `${user.name || user.email} · ${user.role}` : undefined} />
      <DrawerBody className="pb-1">
        <nav aria-label="More" onKeyDown={menuRowKeys}>
          <DisclosureGroup defaultExpandedKeys={openGroup ? [openGroup] : []} className="-mx-2 flex flex-col gap-1">
            {groups.map(([group, list]) => {
              const Icon = NAV_GROUP_ICON[group];
              const current = list.find((l) => isActivePath(pathname, l.to));
              return (
                <Disclosure key={group} id={group} className="group/disclosure">
                  <Heading level={3} className="flex">
                    <Button
                      slot="trigger"
                      data-menu-row=""
                      className={cx(ROW, "w-full cursor-pointer text-start text-text-primary hover:bg-background-secondary-hover")}
                    >
                      <Icon className="size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />
                      <span className="flex min-w-0 flex-1 flex-col">
                        <span className="truncate">{group}</span>
                        <span className="truncate text-caption-1-regular text-text-tertiary">
                          {current ? (
                            <>
                              {current.label}
                              <span className="sr-only">, current page</span>
                            </>
                          ) : (
                            list.map((l) => l.label).join(", ")
                          )}
                        </span>
                      </span>
                      {current && <span className="size-1.5 shrink-0 rounded-full bg-accent-fill" aria-hidden />}
                      <RiArrowDownSLine
                        className={cx(
                          "size-5 shrink-0 text-foreground-icon-tertiary transition-transform duration-150 ease-out motion-reduce:transition-none",
                          "group-data-[expanded]/disclosure:rotate-180",
                        )}
                        aria-hidden
                      />
                    </Button>
                  </Heading>
                  {/* React Aria measures the panel into --disclosure-panel-height; ease it open in 150ms. */}
                  <DisclosurePanel className="h-(--disclosure-panel-height) overflow-clip transition-[height] duration-150 ease-out motion-reduce:transition-none">
                    <ul className="flex flex-col ps-4">
                      {list.map((l) => (
                        <li key={l.to}>
                          <SheetLink item={l} onClose={onClose} />
                        </li>
                      ))}
                    </ul>
                  </DisclosurePanel>
                </Disclosure>
              );
            })}
          </DisclosureGroup>
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
          {gateOn && !passwordOnly && (
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
