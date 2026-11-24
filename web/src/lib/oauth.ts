// OAuth sign in for /mcp: the consent page and an app's connections.
import { request } from "./api";

export interface OAuthRequest {
  id: string;
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
  can_approve: boolean;
}

export interface OAuthConnection {
  id: string;
  client_id: string;
  client_name: string;
  app_id: number;
  user_id: number;
  user_email: string;
  read_only: boolean;
  scope: string;
  resource: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

const rid = (id: string) => encodeURIComponent(id);

export const oauth = {
  request: (id: string) => request<OAuthRequestInfo>("GET", `/api/oauth/requests/${rid(id)}`),
  approve: (id: string, body: { app_id?: number; new_app?: string; read_only: boolean }) =>
    request<{ redirect: string; app: { id: number; name: string } }>("POST", `/api/oauth/requests/${rid(id)}/approve`, body),
  deny: (id: string) => request<{ redirect: string }>("POST", `/api/oauth/requests/${rid(id)}/deny`),
  connections: (appId: number) => request<{ connections: OAuthConnection[] }>("GET", `/api/oauth/apps/${appId}/connections`),
  revoke: (appId: number, grantId: string) =>
    request<OAuthConnection>("DELETE", `/api/oauth/apps/${appId}/connections/${encodeURIComponent(grantId)}`),
};
