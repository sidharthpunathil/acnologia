import { useEffect, useId, useState, type KeyboardEvent, type ReactNode } from "react";
import { Menu, MenuItem, Popover, SubmenuTrigger, type MenuItemProps } from "react-aria-components";
import { useLocation } from "react-router-dom";
import { RiArrowDownSLine, RiArrowLeftSLine, RiLockLine, RiLogoutBoxRLine } from "@remixicon/react";
import { Dropdown, DropdownPopover, DropdownTrigger, useDropdownLayer } from "@/components/base/dropdown/dropdown";
import { MENU_ITEM, MENU_ITEM_ACTIVE, MENU_POPOVER_SURFACE } from "@/components/base/dropdown/menu-styles";
import { useDirection } from "@/components/foundations/direction/direction";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { useAuth } from "@/stores/auth";
import { useGate } from "@/stores/gate";
import { cx } from "@/utils/cx";
import { ACCOUNT_ITEM, NAV_GROUP_ICON, SIGN_OUT_ROW, grouped, isActivePath, useSignOut, type NavGroup, type NavItem } from "./nav";
import { ThemePicker } from "./theme-picker";
import type { IconComponent } from "./types";

const ROW_ICON = "size-[18px] shrink-0";
const MENU_LIST = "flex flex-col gap-0.5 outline-none";

/** How long the pointer rests on a group row before its pages fly out. */
const SUBMENU_DELAY_MS = 150;

/** Row classes from React Aria's state: hover, keyboard focus and an open submenu share the highlight. */
function rowClass(extra?: string) {
  return ({ isHovered, isFocusVisible, isOpen }: { isHovered: boolean; isFocusVisible: boolean; isOpen?: boolean }) =>
    cx(
      MENU_ITEM,
      "text-body-medium",
      (isHovered || isFocusVisible || isOpen) && MENU_ITEM_ACTIVE,
      isFocusVisible && "ring-2 ring-border-focus-ring",
      extra,
    );
}

const ActiveDot = () => <span className="size-1.5 shrink-0 rounded-full bg-accent-fill" aria-hidden />;

/** A menu row: icon, label, and the dot on the open page. */
function Row({
  icon: Icon,
  active = false,
  children,
  className,
  ...props
}: Omit<MenuItemProps, "children" | "className"> & { icon: IconComponent; active?: boolean; className?: string; children: ReactNode }) {
  return (
    // React Aria's MenuItem drops aria-current, so the open page says so in text.
    <MenuItem {...props} className={rowClass(className)}>
      <Icon className={cx(ROW_ICON, active ? "text-foreground-icon-primary" : "text-foreground-icon-secondary")} aria-hidden />
      <span className="min-w-0 flex-1 truncate">
        {children}
        {active && <span className="sr-only">, current page</span>}
      </span>
      {active && <ActiveDot />}
    </MenuItem>
  );
}

/**
 * One group's row and the flyout with its pages. The flyout opens towards
 * the start (left in LTR, since the menu sits at the end of the top bar) and
 * flips to the end when the start has no room. React Aria opens it on hover
 * after a short rest, on a press, and on Enter, Space or the arrow towards
 * the end; it keeps it open while the pointer crosses towards it.
 */
function GroupSubmenu({ group, items, pathname }: { group: NavGroup; items: NavItem[]; pathname: string }) {
  const direction = useDirection();
  const layer = useDropdownLayer();
  const current = items.find((l) => isActivePath(pathname, l.to));
  const Icon = NAV_GROUP_ICON[group];
  // The flyout is named by the group alone, not the whole row with its summary.
  const labelId = useId();
  return (
    <SubmenuTrigger delay={SUBMENU_DELAY_MS}>
      <MenuItem id={group} textValue={group} className={rowClass("py-1.5")}>
        <Icon className={cx(ROW_ICON, current ? "text-foreground-icon-primary" : "text-foreground-icon-secondary")} aria-hidden />
        <span className="flex min-w-0 flex-1 flex-col">
          <span id={labelId} className="truncate text-text-primary">{group}</span>
          {/* What is inside, or the open page when it is one of them. */}
          <span className="truncate text-caption-1-regular text-text-tertiary">
            {current ? (
              <>
                {current.label}
                <span className="sr-only">, current page</span>
              </>
            ) : (
              items.map((l) => l.label).join(", ")
            )}
          </span>
        </span>
        {current && <ActiveDot />}
        <RiArrowLeftSLine className="-me-1 size-5 shrink-0 text-foreground-icon-tertiary rtl:-scale-x-100" aria-hidden />
      </MenuItem>
      {/* offset 13 = the panel's 8px padding, its 1px border and a 4px gap;
          crossOffset -7 lines the first page up with the group's row. */}
      <Popover
        {...layer}
        aria-labelledby={labelId}
        dir={direction}
        placement="start top"
        offset={13}
        crossOffset={-7}
        className={cx(MENU_POPOVER_SURFACE, "w-[224px] p-1.5 motion-reduce:transition-none")}
      >
        <Menu aria-labelledby={labelId} className={MENU_LIST}>
          {items.map((l) => {
            const active = isActivePath(pathname, l.to);
            return (
              <Row key={l.to} id={l.to} href={l.to} textValue={l.label} icon={l.icon} active={active}>
                <span className={cx(active && "text-text-primary")}>{l.label}</span>
              </Row>
            );
          })}
        </Menu>
      </Popover>
    </SubmenuTrigger>
  );
}

/**
 * The pages menu and the account menu are two React Aria menus with the
 * theme control between them. ArrowDown on the last row of one moves to the
 * first row of the next (and ArrowUp back), so the panel reads as one list.
 * Keys from a submenu (portalled, outside this container) are left alone.
 */
function bridgeMenus(e: KeyboardEvent<HTMLElement>) {
  if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
  const item = (e.target as HTMLElement).closest<HTMLElement>('[role="menuitem"]');
  const menu = item?.closest('[role="menu"]');
  const menus = [...e.currentTarget.querySelectorAll<HTMLElement>('[role="menu"]')];
  const i = menu ? menus.indexOf(menu as HTMLElement) : -1;
  if (!item || i < 0) return;
  const rows = [...menus[i].querySelectorAll<HTMLElement>('[role="menuitem"]')];
  const down = e.key === "ArrowDown";
  const next = down ? menus[i + 1] : menus[i - 1];
  if (!next || item !== (down ? rows[rows.length - 1] : rows[0])) return;
  const nextRows = next.querySelectorAll<HTMLElement>('[role="menuitem"]');
  const target = down ? nextRows[0] : nextRows[nextRows.length - 1];
  if (!target) return;
  e.preventDefault();
  e.stopPropagation();
  target.focus();
}

/**
 * Desktop account menu: who you are, one row per group of secondary pages
 * (each opens its pages in a flyout), theme, your account and sign out.
 * While one of its pages is open, the pill names that page before your
 * name, since the top bar has no tab for it.
 */
export function AccountMenu({ items }: { items: NavItem[] }) {
  const user = useAuth((s) => s.user);
  const { pathname } = useLocation();
  const current = [...items, ACCOUNT_ITEM].find((l) => isActivePath(pathname, l.to));
  const who = user?.name || user?.email || "Account";
  const signOut = useSignOut();
  const gateOn = useGate((s) => s.required);
  const passwordOnly = useGate((s) => s.passwordOnly);
  const lockBrowser = useGate((s) => s.lock);
  const [open, setOpen] = useState(false);
  const fade = useScrollFade();

  // A row was followed: close.
  useEffect(() => setOpen(false), [pathname]);

  // Opening moves focus to the first row, so arrow keys work at once, as in a menu.
  useEffect(() => {
    if (!open) return;
    const t = window.setTimeout(() => {
      document
        .querySelector<HTMLElement>('[aria-label="Account menu"][role="dialog"] [role="menuitem"]')
        ?.focus({ preventScroll: true });
    }, 0);
    return () => window.clearTimeout(t);
  }, [open]);

  return (
    <Dropdown isOpen={open} onOpenChange={setOpen}>
      <DropdownTrigger
        aria-label={`Account menu: ${who}${current ? `, current page ${current.label}` : ""}`}
        className={cx(
          "ms-2 inline-flex h-9 max-w-[320px] items-center gap-1.5 rounded-full pe-3 text-body-2-medium",
          current ? "ps-3" : "ps-4",
          "bg-text-primary text-background-full transition-colors duration-150 ease-out hover:bg-foreground-icon-hover",
          "focus-visible:ring-offset-2 focus-visible:ring-offset-background-full",
        )}
      >
        {/* A page from this menu is open: name it, since no top bar tab does. */}
        {current && (
          <>
            <current.icon className="size-4 shrink-0" aria-hidden />
            <span className="shrink-0 whitespace-nowrap">{current.label}</span>
            <span className="mx-1 h-4 w-px shrink-0 bg-background-full/35" aria-hidden />
          </>
        )}
        <span className={cx("truncate", current && "opacity-80")}>{user?.name || "Account"}</span>
        <RiArrowDownSLine
          className={cx("size-4 shrink-0 transition-transform duration-200 ease-out motion-reduce:transition-none", open && "rotate-180")}
          aria-hidden
        />
      </DropdownTrigger>
      <DropdownPopover
        aria-label="Account menu"
        placement="bottom end"
        offset={8}
        className="flex w-[300px] flex-col overflow-hidden p-0 motion-reduce:transition-none"
        dialogClassName="min-h-0 flex-1 gap-0"
      >
        <div className="border-b border-separator-border px-4 pt-3.5 pb-3">
          <div className="truncate text-body-medium text-text-primary">{user?.name}</div>
          <div className="truncate font-mono text-caption-1-regular text-text-secondary">
            {user?.email} · {user?.role}
          </div>
        </div>
        <div className="relative flex min-h-0 flex-1 flex-col">
          <div onScroll={fade.onScroll} onKeyDownCapture={bridgeMenus} className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-2">
            <Menu aria-label="Pages" className={MENU_LIST}>
              {grouped(items).map(([group, list]) => (
                <GroupSubmenu key={group} group={group} items={list} pathname={pathname} />
              ))}
            </Menu>
            <div className="mt-2 border-t border-separator-border px-2 pt-3 pb-2">
              <div className="mb-2 text-caption-1-medium text-text-secondary">Theme</div>
              <ThemePicker />
            </div>
            <Menu aria-label="Account" className={cx(MENU_LIST, "mt-1 border-t border-separator-border pt-2")}>
              <Row id="account" href={ACCOUNT_ITEM.to} textValue={ACCOUNT_ITEM.label} icon={ACCOUNT_ITEM.icon} active={isActivePath(pathname, ACCOUNT_ITEM.to)}>
                {ACCOUNT_ITEM.label}
              </Row>
              <Row
                id="sign-out"
                icon={RiLogoutBoxRLine}
                className={SIGN_OUT_ROW}
                onAction={() => {
                  setOpen(false);
                  void signOut();
                }}
              >
                Sign out
              </Row>
              {gateOn && !passwordOnly && (
                <Row
                  id="lock"
                  icon={RiLockLine}
                  className={SIGN_OUT_ROW}
                  onAction={() => {
                    setOpen(false);
                    void lockBrowser();
                  }}
                >
                  Lock this browser
                </Row>
              )}
            </Menu>
          </div>
          <ScrollFade progress={fade.progress} />
        </div>
      </DropdownPopover>
    </Dropdown>
  );
}
