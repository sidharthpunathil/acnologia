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

/**
 * proxy: requests go through this server. handover: the keys go to the other
 * server. reveal: a person sees the scoped keys once on the claim page.
 */
export type ShareMode = "proxy" | "handover" | "reveal";

/** Who a share is for: another Acnologia server, or a person with a claim link. */
export type ShareRecipient = "server" | "person";

/** The modes a person share can have. */
export type PersonMode = "proxy" | "reveal";

/** A key shown to a person on the claim page (no secret). */
export interface RevealedKey {
  key_id: number;
  service: string;
  label: string;
  revealed_at?: string | null;
}

/** What happens when ends_at passes: expire (can be extended) or revoke for good. */
export type EndAction = "expire" | "revoke";

/** Renewal window for handed over keys. */
export type Lease = "1h" | "6h" | "24h" | "72h" | "7d";

/** One key handed over to the other server (no secret). */
export interface HandoverKey {
  key_id: number;
  service: string;
  label: string;
  handed_at?: string | null;
  renewed_at?: string | null;
  valid_until?: string | null;
  wiped_at?: string | null;
  /** Counted by the other server and reported on sync. */
  reported_requests?: number | null;
  /** Pool status the other server reported, such as "invalid" or "banned". */
  reported_status?: string | null;
}

/** Outcome of the signed revoke sent to the other server. */
export interface RevokePush {
  at?: string | null;
  ok: boolean;
  detail?: string;
}

/** A key to rotate at the provider after a handover share is revoked. */
export interface RotateItem {
  key_id: number;
  service: string;
  label: string;
  /** Optional: the provider spec slug, and the provider's key page when the spec knows it. */
  provider?: string;
  key_page_url?: string;
}

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
  // Share modes and timers. Optional so the page still works against a
  // server that does not send them yet: read them through the helpers below.
  mode?: ShareMode;
  /** Handover only. A duration such as "24h"; a number is read as seconds. */
  lease?: Lease | string | number | null;
  /** RFC 3339 or null. Maps to expires_at on the server. */
  ends_at?: string | null;
  end_action?: EndAction;
  handover?: { keys?: HandoverKey[] | null; last_revoke_push?: RevokePush | null } | null;
  /** Set once a handover or reveal share ends. */
  rotate_checklist?: RotateItem[] | null;
  // Sharing with people. Optional: a missing recipient means a server share.
  recipient?: ShareRecipient;
  /** The person's name, for person shares. */
  label?: string;
  /** Only a label; nothing is ever sent to it. */
  email?: string;
  claimed_at?: string | null;
  claim_expires_at?: string | null;
  /** Reveal shares: the keys the person was shown (no secrets). */
  revealed_keys?: RevealedKey[] | null;
}

export interface ShareInput {
  name?: string;
  note?: string;
  services?: string[];
  providers?: string[];
  include_hidden?: boolean;
  limits?: AppLimits;
  key_scopes?: Record<string, KeyScope>;
  /** RFC 3339, or "" for no expiry. Sent alongside ends_at for older servers. */
  expires_at?: string;
  invite_valid?: InviteValidity;
  mode?: ShareMode;
  lease?: Lease;
  /** RFC 3339, or "" for never. */
  ends_at?: string;
  end_action?: EndAction;
  recipient?: ShareRecipient;
  label?: string;
  email?: string;
  /** Person shares: how long the claim link works. Sent with invite_valid, which the server may read instead. */
  claim_valid?: InviteValidity;
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
  /** Optional until the server sends it; missing means proxy. */
  mode?: ShareMode;
  /** Keys this server holds for the link (no secrets). Read through linkHandoverKeys. */
  handover?: { keys?: PeerHandoverKey[] | null } | null;
  /** Flat spelling, also accepted. */
  handover_keys?: PeerHandoverKey[] | null;
}

/** A key the other server handed to this one, as the link reports it. */
export interface PeerHandoverKey {
  key_id?: number;
  /** Local service slug the key lives in. */
  service?: string;
  label?: string;
  valid_until?: string | null;
  renewed_at?: string | null;
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
  /** Server shares answer with `invite`, person shares with `claim_url`. */
  createShare: (body: ShareInput) => request<{ share: Share; invite?: string; claim_url?: string }>("POST", "/api/admin/shares", body),
  updateShare: (id: string, body: ShareInput) => request<Share>("PATCH", `/api/admin/shares/${id}`, body),
  pauseShare: (id: string) => request<Share>("POST", `/api/admin/shares/${id}/pause`),
  resumeShare: (id: string) => request<Share>("POST", `/api/admin/shares/${id}/resume`),
  revokeShare: (id: string) => request<Share>("POST", `/api/admin/shares/${id}/revoke`),
  /** ends_at is RFC 3339, or "" for never. */
  extendShare: (id: string, ends_at: string) => request<Share>("POST", `/api/admin/shares/${id}/extend`, { ends_at }),
  newInvite: (id: string, invite_valid: InviteValidity) =>
    request<{ share: Share; invite: string }>("POST", `/api/admin/shares/${id}/invite`, { invite_valid }),
  /** A new claim link for a person share that is not claimed yet. The old link stops working. */
  newClaimLink: (id: string, valid: InviteValidity) =>
    request<{ share?: Share; claim_url: string; claim_expires_at?: string | null }>("POST", `/api/admin/shares/${id}/claim-link`, { valid }),

  listPeers: () => request<{ peers: PeerLink[] }>("GET", "/api/admin/peers"),
  previewPeer: (invite: string, trusted: boolean) => request<PeerPreview>("POST", "/api/admin/peers/preview", { invite, trusted }),
  connectPeer: (body: { invite: string; prefix: string; trusted: boolean; owner_id: string }) =>
    request<PeerLink>("POST", "/api/admin/peers", body),
  syncPeer: (id: number) => request<PeerLink>("POST", `/api/admin/peers/${id}/sync`),
  disconnectPeer: (id: number) => request<PeerLink | { ok: boolean; removed: boolean }>("DELETE", `/api/admin/peers/${id}`),
};

export function shareMode(s: { mode?: ShareMode | null }): ShareMode {
  return s.mode === "handover" || s.mode === "reveal" ? s.mode : "proxy";
}

export function shareRecipient(s: { recipient?: ShareRecipient | null }): ShareRecipient {
  return s.recipient === "person" ? "person" : "server";
}

/** The person's name on a person share, falling back to the share name. */
export function personLabel(s: Share): string {
  return s.label?.trim() || s.name || "this person";
}

export function revealedKeys(s: Share): RevealedKey[] {
  return s.revealed_keys ?? [];
}

/** When the share ends: ends_at, or the older expires_at. */
export function shareEndsAt(s: Share): string | null {
  return s.ends_at || s.expires_at || null;
}

export function shareEndAction(s: Share): EndAction {
  return s.end_action === "revoke" ? "revoke" : "expire";
}

export const LEASES: { id: Lease; label: string; ms: number }[] = [
  { id: "1h", label: "1 hour", ms: 3600e3 },
  { id: "6h", label: "6 hours", ms: 6 * 3600e3 },
  { id: "24h", label: "24 hours", ms: 24 * 3600e3 },
  { id: "72h", label: "72 hours", ms: 72 * 3600e3 },
  { id: "7d", label: "7 days", ms: 7 * 86400e3 },
];

/** The share's lease as one of the choices, tolerating seconds or an hours spelling. */
export function shareLease(s: Share): Lease {
  const v = s.lease;
  if (typeof v === "number") return LEASES.find((l) => l.ms === v * 1000)?.id ?? "24h";
  if (typeof v === "string") {
    const hit = LEASES.find((l) => l.id === v);
    if (hit) return hit.id;
    if (v === "168h") return "7d";
  }
  return "24h";
}

export function handoverKeys(s: Share): HandoverKey[] {
  return s.handover?.keys ?? [];
}

export function linkHandoverKeys(l: PeerLink): PeerHandoverKey[] {
  return l.handover?.keys ?? l.handover_keys ?? [];
}

/** The rotate checklist after revoke, or else every key that was handed over or revealed. */
export function rotateChecklist(s: Share): RotateItem[] {
  if (s.rotate_checklist?.length) return s.rotate_checklist;
  if (shareMode(s) === "reveal") return revealedKeys(s).map((k) => ({ key_id: k.key_id, service: k.service, label: k.label }));
  return handoverKeys(s).map((k) => ({ key_id: k.key_id, service: k.service, label: k.label }));
}

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
