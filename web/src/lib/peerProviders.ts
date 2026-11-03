// Provider specs imported from another Acnologia server, and which provider
// describes a service. A service shared by another server keeps the
// acnologia-peer provider (it classifies the share token's answers), but its
// name, logo, docs and examples come from the owner's provider.
import { request } from "./api";
import type { Service } from "./types";

/** The provider slug whose name, logo, docs and examples describe a service. */
export function docProviderSlug(s: Pick<Service, "provider" | "peer_provider">): string {
  return s.peer_provider || s.provider;
}

export interface PeerProvider {
  link_id: number;
  provider: string;
  /** shared: the owner shared it on purpose; dependency: a shared service needed it. */
  kind: "shared" | "dependency";
  /** The owner's service slugs that needed it. */
  for_services: string[];
  /** The same, mapped to the local service ids. */
  services: Record<string, number>;
  imported_version: number;
  remote_version: number;
  local_version: number;
  imported_at: string;
  name: string;
  edited_here: boolean;
  update_available: boolean;
  base_url: string;
  remote_base_url: string;
  missing: boolean;
}

export const peerProviders = {
  list: (linkId: number) => request<{ providers: PeerProvider[] }>("GET", `/api/admin/peers/${linkId}/providers`),
  pull: (linkId: number, slug: string, allowBaseUrlChange = false) =>
    request<{ providers: PeerProvider[] }>("POST", `/api/admin/peers/${linkId}/providers/${encodeURIComponent(slug)}/pull`, {
      allow_base_url_change: allowBaseUrlChange,
    }),
};
