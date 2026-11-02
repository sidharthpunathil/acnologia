import { useEffect, useState } from "react";
import { Link } from "react-aria-components";
import { useLocation } from "react-router-dom";
import { RiArrowDownSLine, RiLogoutBoxRLine } from "@remixicon/react";
import { Dropdown, DropdownPopover, DropdownTrigger } from "@/components/base/dropdown/dropdown";
import { MENU_ITEM, MENU_ITEM_ACTIVE, MENU_ITEM_INTERACTIVE } from "@/components/base/dropdown/menu-styles";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { useAuth } from "@/stores/auth";
import { cx } from "@/utils/cx";
import { ACCOUNT_ITEM, SIGN_OUT_ROW, grouped, isActivePath, menuRowKeys, useSignOut, type NavItem } from "./nav";
import { ThemePicker } from "./theme-picker";

const ROW = cx(MENU_ITEM, "text-body-medium");
const ROW_ICON = "size-[18px] shrink-0";

/** One navigation row in a menu panel. */
function MenuLink({ item, active }: { item: NavItem; active: boolean }) {
  return (
    <Link
      href={item.to}
      data-menu-row=""
      aria-current={active ? "page" : undefined}
      className={cx(ROW, active ? MENU_ITEM_ACTIVE : MENU_ITEM_INTERACTIVE, "focus-visible:ring-2 focus-visible:ring-border-focus-ring")}
    >
      <item.icon className={cx(ROW_ICON, active ? "text-foreground-icon-primary" : "text-foreground-icon-secondary")} aria-hidden />
      <span className={cx("min-w-0 flex-1 truncate", active && "text-text-primary")}>{item.label}</span>
      {active && <span className="size-1.5 shrink-0 rounded-full bg-accent-fill" aria-hidden />}
    </Link>
  );
}

/**
 * Desktop account menu: who you are, the secondary pages grouped, theme,
 * your account and sign out. While one of its pages is open, the pill names
 * that page before your name, since the top bar has no tab for it.
 */
export function AccountMenu({ items }: { items: NavItem[] }) {
  const user = useAuth((s) => s.user);
  const { pathname } = useLocation();
  const current = [...items, ACCOUNT_ITEM].find((l) => isActivePath(pathname, l.to));
  const who = user?.name || user?.email || "Account";
  const signOut = useSignOut();
  const [open, setOpen] = useState(false);
  const fade = useScrollFade();

  // A row was followed: close.
  useEffect(() => setOpen(false), [pathname]);

  // Opening moves focus to the first row, so arrow keys work at once, as in a menu.
  useEffect(() => {
    if (!open) return;
    const t = window.setTimeout(() => {
      document.querySelector<HTMLElement>('[aria-label="Account menu"][role="dialog"] [data-menu-row]')?.focus({ preventScroll: true });
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
        className="flex w-[300px] flex-col overflow-hidden p-0"
        dialogClassName="min-h-0 flex-1 gap-0"
      >
        <div className="border-b border-separator-border px-4 pt-3.5 pb-3">
          <div className="truncate text-body-medium text-text-primary">{user?.name}</div>
          <div className="truncate font-mono text-caption-1-regular text-text-secondary">
            {user?.email} · {user?.role}
          </div>
        </div>
        <div className="relative flex min-h-0 flex-1 flex-col">
          <div onScroll={fade.onScroll} onKeyDown={menuRowKeys} className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-2">
            <nav aria-label="Menu" className="flex flex-col">
              {grouped(items).map(([group, list], gi) => (
                <div key={group} className={cx("flex flex-col gap-0.5", gi > 0 && "mt-2 border-t border-separator-border pt-2")}>
                  <div className="px-2 pt-1 pb-1 text-caption-2-semibold uppercase tracking-wider text-text-tertiary">{group}</div>
                  {list.map((l) => (
                    <MenuLink key={l.to} item={l} active={isActivePath(pathname, l.to)} />
                  ))}
                </div>
              ))}
            </nav>
            <div className="mt-2 border-t border-separator-border px-2 pt-3 pb-2">
              <div className="mb-2 text-caption-1-medium text-text-secondary">Theme</div>
              <ThemePicker />
            </div>
            <div className="mt-1 flex flex-col gap-0.5 border-t border-separator-border pt-2">
              <MenuLink item={ACCOUNT_ITEM} active={isActivePath(pathname, ACCOUNT_ITEM.to)} />
            </div>
            <div className="mt-2 flex flex-col gap-0.5 border-t border-separator-border pt-2">
              <button
                type="button"
                data-menu-row=""
                onClick={() => {
                  setOpen(false);
                  void signOut();
                }}
                className={cx(ROW, MENU_ITEM_INTERACTIVE, SIGN_OUT_ROW, "focus-visible:ring-2 focus-visible:ring-border-focus-ring")}
              >
                <RiLogoutBoxRLine className={cx(ROW_ICON, "text-foreground-icon-secondary")} aria-hidden />
                Sign out
              </button>
            </div>
          </div>
          <ScrollFade progress={fade.progress} />
        </div>
      </DropdownPopover>
    </Dropdown>
  );
}
