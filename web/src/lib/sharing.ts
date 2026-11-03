// Types and endpoints for sharing between Acnologia servers: this server's
// identity, shares this server gives (owner side) and peer links to shares
// other servers gave it (receiver side).
import { request } from "./api";
import type { AppLimits, UsageDay } from "./ops";
import type { Service } from "./types";

export interface ServerIdentity {
  id: string;
  name: string;
  url: string;
  /** env (ACNOLOGIA_PUBLIC_URL), saved (set here) or request (guessed). */
  url_source: "env" | "saved" | "request";
  version: string;
  public_key: string;
  fingerprint: string;
}

/** Which keys of one shared service a share may use. A missing entry means all. */
export type KeyScopeMode = "all" | "count" | "pick";

export interface KeyScope {
  mode: KeyScopeMode;
  /** Count mode: at most this many keys, the first still in rotation by priority. */
  count?: number;
  /** Pick mode: only these keys. */
  key_ids?: number[];
}

export type ShareStatus = "pending" | "active" | "paused" | "revoked" | "expired";

export interface Share {
  id: string;
  name: string;
  note: string;
  status: ShareStatus;
  services: string[];
  providers: string[];
  include_hidden: boolean;
  app_id: number | null;
  peer_id: string;
  peer_name: string;
  peer_url: string;
  invite_expires_at: string | null;
  expires_at: string | null;
  created_at: string;
  redeemed_at: string | null;
  revoked_at: string | null;
  left_at: string | null;
  limits: AppLimits;
  /** By service slug; a service without an entry shares every key. */
  key_scopes: Record<string, KeyScope>;
  /** By service slug: keys the scope resolves to now, of the keys the share could use. */
  service_keys?: Record<string, { keys_shared: number; keys_total: number }>;
  usage?: {
    today_requests: number;
    today_cost: number;
    month_requests: number;
    month_cost: number;
    last_used_at: string | null;
    history: UsageDay[];
    per_service?: Record<string, number>;
    limit_hit: { limit: string; reset_at: string } | null;
  };
}

export interface ShareInput {
  name?: string;
  note?: string;
  services?: string[];
  providers?: string[];
  include_hidden?: boolean;
  limits?: AppLimits;
  key_scopes?: Record<string, KeyScope>;
  /** RFC 3339, or "" for no expiry. */
  expires_at?: string;
  invite_valid?: InviteValidity;
}

export type InviteValidity = "1h" | "24h" | "7d";

export type LinkStatus = "active" | "paused" | "expired" | "revoked" | "disconnected";

export interface ManifestService {
  slug: string;
  name: string;
  provider: string;
  category: string;
  description: string;
  available: number;
}

export interface PeerLink {
  id: number;
  peer_id: string;
  peer_name: string;
  peer_url: string;
  remote_share_id: string;
  status: LinkStatus;
  prefix: string;
  trusted: boolean;
  last_sync_at: string | null;
  last_error: string;
  created_at: string;
  services: Record<string, number>;
  providers: string[];
  fingerprint: string;
  local_services: Service[];
  manifest: { name?: string; services?: ManifestService[] };
}

export interface PeerPreview {
  owner: { id: string; name: string; url: string; version: string; public_key: string };
  fingerprint: string;
  url: string;
  prefix: string;
  share: { name: string; services: ManifestService[]; providers: string[]; invite_expires_at: string | null };
}

export const sharing = {
  identity: () => request<ServerIdentity>("GET", "/api/admin/identity"),
  updateIdentity: (body: { name?: string; url?: string }) => request<ServerIdentity>("PATCH", "/api/admin/identity", body),

  listShares: () => request<{ shares: Share[] }>("GET", "/api/admin/shares"),
  getShare: (id: string) => request<Share>("GET", `/api/admin/shares/${id}`),
  createShare: (body: ShareInput) => request<{ share: Share; invite: string }>("POST", "/api/admin/shares", body),
  updateShare: (id: string, body: ShareInput) => request<Share>("PATCH", `/api/admin/shares/${id}`, body),
  pauseShare: (id: string) => request<Share>("POST", `/api/admin/shares/${id}/pause`),
  resumeShare: (id: string) => request<Share>("POST", `/api/admin/shares/${id}/resume`),
  revokeShare: (id: string) => request<Share>("POST", `/api/admin/shares/${id}/revoke`),
  newInvite: (id: string, invite_valid: InviteValidity) =>
    request<{ share: Share; invite: string }>("POST", `/api/admin/shares/${id}/invite`, { invite_valid }),

  listPeers: () => request<{ peers: PeerLink[] }>("GET", "/api/admin/peers"),
  previewPeer: (invite: string, trusted: boolean) => request<PeerPreview>("POST", "/api/admin/peers/preview", { invite, trusted }),
  connectPeer: (body: { invite: string; prefix: string; trusted: boolean; owner_id: string }) =>
    request<PeerLink>("POST", "/api/admin/peers", body),
  syncPeer: (id: number) => request<PeerLink>("POST", `/api/admin/peers/${id}/sync`),
  disconnectPeer: (id: number) => request<PeerLink | { ok: boolean; removed: boolean }>("DELETE", `/api/admin/peers/${id}`),
};

/** The last `days` UTC days of a usage history, oldest first, zero filled. */
export function lastDays(history: UsageDay[] | undefined, days = 14): { day: string; requests: number }[] {
  const by = new Map((history ?? []).map((d) => [d.day, d.requests]));
  const out: { day: string; requests: number }[] = [];
  const now = Date.now();
  for (let i = days - 1; i >= 0; i--) {
    const day = new Date(now - i * 86400000).toISOString().slice(0, 10);
    out.push({ day, requests: by.get(day) ?? 0 });
  }
  return out;
}
