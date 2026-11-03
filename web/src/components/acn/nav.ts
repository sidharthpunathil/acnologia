import {
  RiBarChartBoxLine,
  RiBookOpenLine,
  RiChat3Line,
  RiEqualizerLine,
  RiLayoutGridLine,
  RiQuestionLine,
  RiPulseLine,
  RiShareForwardLine,
  RiSettings3Line,
  RiShieldCheckLine,
  RiShieldUserLine,
  RiTeamLine,
  RiUser3Line,
  RiWebhookLine,
} from "@remixicon/react";
import { AcnApiKey, AcnAppToken, AcnEgress, AcnKeyPool, AcnProviderSpec, AcnServiceGroup } from "@/components/foundations/icons/acn";
import type { KeyboardEvent } from "react";
import { useNavigate } from "react-router-dom";
import { can } from "@/lib/perms";
import type { Role } from "@/lib/types";
import { useAuth } from "@/stores/auth";
import type { IconComponent } from "./types";

export type NavGroup = "Workspace" | "Manage" | "Admin" | "Help";
export const NAV_GROUPS: NavGroup[] = ["Workspace", "Manage", "Admin", "Help"];

/** The icon on each group's row in the account menu and the More sheet. */
export const NAV_GROUP_ICON: Record<NavGroup, IconComponent> = {
  Workspace: RiLayoutGridLine,
  Manage: RiEqualizerLine,
  Admin: RiShieldUserLine,
  Help: RiQuestionLine,
};

export interface NavItem {
  to: string;
  label: string;
  icon: IconComponent;
  /** Match the path exactly (the catalog at "/"). */
  end?: boolean;
  /** Pages with a group live in the account menu (desktop) and the More sheet (phone). */
  group?: NavGroup;
}

/** Sign out reads the same on the account menu and the phone More sheet. */
export const SIGN_OUT_ROW = "text-text-primary";

export const ACCOUNT_ITEM: NavItem = { to: "/account", label: "Your account", icon: RiUser3Line };

/** Every destination this role can open, in display order. */
export function navItems(role: Role | undefined): NavItem[] {
  const all: Array<NavItem & { show: boolean }> = [
    // The top bar (and the phone tab bar) keeps the three pages used every day.
    { to: "/", label: "Catalog", icon: AcnKeyPool, show: true, end: true },
    { to: "/keys", label: "Keys", icon: AcnApiKey, show: true },
    { to: "/assistant", label: "Assistant", icon: RiChat3Line, show: true },
    // Everything else lives in the account menu, grouped.
    { to: "/providers", label: "Providers", icon: AcnProviderSpec, show: true, group: "Workspace" },
    { to: "/apps", label: "Apps", icon: AcnAppToken, show: true, group: "Workspace" },
    { to: "/activity", label: "Activity", icon: RiPulseLine, show: true, group: "Workspace" },
    { to: "/usage", label: "Usage", icon: RiBarChartBoxLine, show: true, group: "Workspace" },
    { to: "/groups", label: "Service groups", icon: AcnServiceGroup, show: true, group: "Manage" },
    { to: "/webhooks", label: "Webhooks", icon: RiWebhookLine, show: true, group: "Manage" },
    { to: "/egress", label: "Egress proxies", icon: AcnEgress, show: true, group: "Manage" },
    { to: "/sharing", label: "Sharing", icon: RiShareForwardLine, show: can.manage(role), group: "Manage" },
    { to: "/settings", label: "Settings", icon: RiSettings3Line, show: true, group: "Admin" },
    { to: "/users", label: "Users", icon: RiTeamLine, show: can.users(role), group: "Admin" },
    { to: "/audit", label: "Audit", icon: RiShieldCheckLine, show: can.audit(role), group: "Admin" },
    { to: "/docs", label: "Docs", icon: RiBookOpenLine, show: true, group: "Help" },
  ];
  return all.filter((l) => l.show);
}

/** Groups the menu items in NAV_GROUPS order, skipping empty groups. */
export function grouped(items: NavItem[]): Array<[NavGroup, NavItem[]]> {
  return NAV_GROUPS.map((g) => [g, items.filter((i) => i.group === g)] as [NavGroup, NavItem[]]).filter(([, l]) => l.length > 0);
}

export function isActivePath(pathname: string, to: string) {
  return to === "/" ? pathname === "/" : pathname === to || pathname.startsWith(to + "/");
}

export function useSignOut() {
  const logout = useAuth((s) => s.logout);
  const navigate = useNavigate();
  return async () => {
    await logout();
    navigate("/login", { replace: true });
  };
}

/** The group holding the open page, if any. */
export function activeGroup(items: NavItem[], pathname: string): NavGroup | undefined {
  return items.find((i) => i.group && isActivePath(pathname, i.to))?.group;
}

/**
 * Arrow key movement between the rows of a menu panel. Rows carry
 * `data-menu-row`; rows inside a collapsed panel are skipped. Home and End
 * jump to the ends.
 */
export function menuRowKeys(e: KeyboardEvent<HTMLElement>) {
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(e.key)) return;
  const rows = [...e.currentTarget.querySelectorAll<HTMLElement>("[data-menu-row]")].filter((r) => r.getClientRects().length > 0);
  if (!rows.length) return;
  e.preventDefault();
  const i = rows.indexOf(document.activeElement as HTMLElement);
  let j = 0;
  if (e.key === "ArrowDown") j = i < 0 ? 0 : (i + 1) % rows.length;
  else if (e.key === "ArrowUp") j = i < 0 ? rows.length - 1 : (i - 1 + rows.length) % rows.length;
  else if (e.key === "End") j = rows.length - 1;
  rows[j]?.focus();
}
