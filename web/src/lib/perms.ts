import type { Role } from "./types";

const rank: Record<Role, number> = { member: 0, admin: 1, superadmin: 2 };

export function atLeast(role: Role | undefined, min: Role): boolean {
  if (!role) return false;
  return rank[role] >= rank[min];
}

export const can = {
  manage: (role?: Role) => atLeast(role, "admin"),
  hidden: (role?: Role) => atLeast(role, "superadmin"),
  reveal: (role?: Role) => atLeast(role, "superadmin"),
  users: (role?: Role) => atLeast(role, "superadmin"),
  audit: (role?: Role) => atLeast(role, "superadmin"),
};

/** Server side minimum password length. */
export const MIN_PASSWORD = 10;
