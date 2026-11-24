// OAuth sign in for /mcp and /mcp/control: the consent page, an app's
// connections and the signed in person's control MCP connections.
import { request } from "./api";
import type { TokenScope } from "./ops";

export interface OAuthRequest {
  id: string;
  /** app: /mcp, acting as an app. control: /mcp/control, acting as you. */
  kind: "app" | "control";
  client_name: string;
  client_id: string;
  /** Set when the client_id is a metadata document URL: its host. */
  verified_domain: string;
  redirect_uri: string;
  redirect_host: string;
  resource: string;
  scope: string;
  expires_at: string;
}

export interface OAuthRequestInfo {
  request: OAuthRequest;
  apps: { id: number; name: string; description: string; active: boolean }[];
  /** For a control request: the scopes this person may give, lowest first. */
  scopes: TokenScope[];
  can_approve: boolean;
}

export interface OAuthConnection {
  id: string;
  client_id: string;
  client_name: string;
  /** 0 for a control MCP connection. */
  app_id: number;
  user_id: number;
  user_email: string;
  read_only: boolean;
  /** A control MCP connection's scope; empty for an app connection. */
  access: TokenScope | "";
  scope: string;
  resource: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

const rid = (id: string) => encodeURIComponent(id);

export const oauth = {
  request: (id: string) => request<OAuthRequestInfo>("GET", `/api/oauth/requests/${rid(id)}`),
  approve: (id: string, body: { app_id?: number; new_app?: string; read_only?: boolean; scope?: TokenScope }) =>
    request<{ redirect: string }>("POST", `/api/oauth/requests/${rid(id)}/approve`, body),
  deny: (id: string) => request<{ redirect: string }>("POST", `/api/oauth/requests/${rid(id)}/deny`),
  connections: (appId: number) => request<{ connections: OAuthConnection[] }>("GET", `/api/oauth/apps/${appId}/connections`),
  revoke: (appId: number, grantId: string) =>
    request<OAuthConnection>("DELETE", `/api/oauth/apps/${appId}/connections/${encodeURIComponent(grantId)}`),
  /** Control MCP connections: yours, or everyone's for a superadmin. */
  controlConnections: () => request<{ connections: OAuthConnection[] }>("GET", "/api/oauth/control/connections"),
  revokeControl: (grantId: string) => request<OAuthConnection>("DELETE", `/api/oauth/control/connections/${encodeURIComponent(grantId)}`),
};
