import type {
  App,
  AppInput,
  AppToken,
  AppWithToken,
  AuditEntry,
  AuthState,
  ChatAi,
  ChatAnswer,
  ChatItem,
  ChatReply,
  ChatThread,
  ChatTurn,
  AcnologiaEvent,
  CheckSummary,
  Key,
  KeyInsights,
  KeyPatch,
  KeyRow,
  KeySummary,
  NewKeyInput,
  Overview,
  ProviderTemplate,
  Role,
  Service,
  ServiceInput,
  User,
  AgentRun,
  AgentRunInput,
  AgentStep,
  AiStatus,
  AiRules,
  AssistForm,
  AssistResult,
  LiveReport,
  LogoUpload,
  Provider,
  ProviderDetail,
  ProviderInput,
  ProviderSaveResult,
  ValidateResult,
} from "./types";
import type { MCPSettingsInput, MCPView } from "./types";

export class ApiError extends Error {
  status: number;
  code: string;
  retryAfterSec?: number;
  /** The parsed error body, for endpoints that return extra detail (errors, report). */
  data: unknown;

  constructor(status: number, code: string, message: string, retryAfterSec?: number, data?: unknown) {
    super(message);
    this.status = status;
    this.code = code;
    this.retryAfterSec = retryAfterSec;
    this.data = data ?? null;
  }
}

type UnauthorizedHandler = () => void;
let onUnauthorized: UnauthorizedHandler | null = null;

/** Registered by the auth store so any 401 sends the user back to login. */
export function setUnauthorizedHandler(fn: UnauthorizedHandler) {
  onUnauthorized = fn;
}

/**
 * Fired when the server answers 401 {"error":{"code":"locked"}}: the access
 * gate's cookie is gone or expired. The AccessGate shows the lock screen.
 */
export const GATE_LOCKED_EVENT = "acn:gate-locked";

type Method = "GET" | "POST" | "PATCH" | "PUT" | "DELETE";

export async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: "include",
    headers: { Accept: "application/json" },
  };
  if (method !== "GET") {
    // CSRF guard: the server rejects mutating requests without both headers.
    const h = init.headers as Record<string, string>;
    h["Content-Type"] = "application/json";
    h["X-Requested-With"] = "acnologia";
    init.body = JSON.stringify(body ?? {});
  }

  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    throw new ApiError(0, "network", "Cannot reach the Acnologia server.");
  }

  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }

  if (!res.ok) {
    const payload = (data ?? {}) as {
      error?: { code?: string; message?: string } | string;
      errors?: string[];
      retry_after_sec?: number;
    };
    // The error is usually {code, message}; accept a plain string too.
    const errObj = typeof payload.error === "string" ? { message: payload.error } : payload.error;
    const code = (errObj as { code?: string } | undefined)?.code ?? `http_${res.status}`;
    const message =
      errObj?.message ||
      (payload.errors && payload.errors.length ? payload.errors[0] : "") ||
      res.statusText ||
      `Request failed (${res.status})`;
    // The access gate closed (cookie expired, password changed): show the
    // lock screen rather than the sign-in page.
    if (res.status === 401 && code === "locked") {
      window.dispatchEvent(new Event(GATE_LOCKED_EVENT));
    } else if (res.status === 401 && !path.startsWith("/api/auth/") && !path.startsWith("/api/gate")) {
      // Auth endpoints answer 401 for bad credentials; that is not a session expiry.
      onUnauthorized?.();
    }
    throw new ApiError(res.status, code, message, payload.retry_after_sec, data);
  }

  return data as T;
}

function qs(params: Record<string, string | number | undefined | null>): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

export interface GateState {
  /** An access password is configured on the server. */
  required: boolean;
  /** This browser has not passed the gate yet. */
  locked: boolean;
  /** The access password is the whole login: no email form behind it. */
  password_only?: boolean;
}

export const api = {
  // Access gate (site-wide password, before any account)
  gateState: () => request<GateState>("GET", "/api/gate"),
  gateUnlock: (password: string) => request<GateState>("POST", "/api/gate/unlock", { password }),
  gateLock: () => request<GateState>("POST", "/api/gate/lock"),

  // Auth
  authState: () => request<AuthState>("GET", "/api/auth/state"),
  setup: (body: { email: string; name: string; password: string }) =>
    request<unknown>("POST", "/api/auth/setup", body),
  login: (body: { email: string; password: string }) => request<unknown>("POST", "/api/auth/login", body),
  logout: () => request<unknown>("POST", "/api/auth/logout"),

  // Me
  me: () => request<User>("GET", "/api/admin/me"),
  updateMe: (body: { name?: string; current_password?: string; password?: string }) => request<User>("PATCH", "/api/admin/me", body),

  // Overview
  overview: () => request<Overview>("GET", "/api/admin/overview"),

  // Services
  listServices: () => request<{ services: Service[] }>("GET", "/api/admin/services"),
  getService: (id: number) => request<Service>("GET", `/api/admin/services/${id}`),
  createService: (body: ServiceInput) => request<Service>("POST", "/api/admin/services", body),
  updateService: (id: number, body: Partial<ServiceInput>) =>
    request<Service>("PATCH", `/api/admin/services/${id}`, body),
  deleteService: (id: number) => request<unknown>("DELETE", `/api/admin/services/${id}`),
  setServiceLogo: (id: number, body: LogoUpload) => request<unknown>("PUT", `/api/admin/services/${id}/logo`, body),
  deleteServiceLogo: (id: number) => request<unknown>("DELETE", `/api/admin/services/${id}/logo`),

  // Providers
  listProviders: () =>
    request<{ providers: Array<Provider | ProviderTemplate>; strategies?: string[] }>("GET", "/api/admin/providers"),
  getProvider: (slug: string) => request<ProviderDetail>("GET", `/api/admin/providers/${encodeURIComponent(slug)}`),
  createProvider: (body: ProviderInput) => request<ProviderSaveResult>("POST", "/api/admin/providers", body),
  updateProvider: (slug: string, body: ProviderInput & { note?: string }) =>
    request<ProviderSaveResult>("PUT", `/api/admin/providers/${encodeURIComponent(slug)}`, body),
  deleteProvider: (slug: string) => request<unknown>("DELETE", `/api/admin/providers/${encodeURIComponent(slug)}`),
  validateProvider: (body: ProviderInput) => request<ValidateResult>("POST", "/api/admin/providers/validate", body),
  liveCheck: (slug: string, secret: string) =>
    request<LiveReport>("POST", `/api/admin/providers/${encodeURIComponent(slug)}/live-check`, { secret }),
  rollbackProvider: (slug: string, version: number) =>
    request<Provider>("POST", `/api/admin/providers/${encodeURIComponent(slug)}/rollback`, { version }),
  setProviderLogo: (slug: string, body: LogoUpload) =>
    request<unknown>("PUT", `/api/admin/providers/${encodeURIComponent(slug)}/logo`, body),
  deleteProviderLogo: (slug: string) => request<unknown>("DELETE", `/api/admin/providers/${encodeURIComponent(slug)}/logo`),
  fetchProviderLogo: (slug: string, url?: string) =>
    request<unknown>("POST", `/api/admin/providers/${encodeURIComponent(slug)}/logo/fetch`, url ? { url } : {}),

  // AI builder
  aiStatus: () => request<AiStatus>("GET", "/api/admin/ai/status"),
  listRuns: () => request<{ runs: AgentRun[] }>("GET", "/api/admin/ai/runs"),
  createRun: (body: AgentRunInput) => request<AgentRun>("POST", "/api/admin/ai/runs", body),
  getRun: (id: number, after = 0) =>
    request<{ run: AgentRun; steps: AgentStep[] }>("GET", `/api/admin/ai/runs/${id}${qs({ after })}`),
  deleteRun: (id: number) => request<unknown>("DELETE", `/api/admin/ai/runs/${id}`),
  clearRuns: () => request<{ deleted: number }>("DELETE", "/api/admin/ai/runs"),
  cancelRun: (id: number) => request<unknown>("POST", `/api/admin/ai/runs/${id}/cancel`),
  aiAssist: (form: AssistForm, instruction: string, current: object) =>
    request<AssistResult>("POST", "/api/admin/ai/assist", { form, instruction, current }),
  getAiRules: () => request<AiRules>("GET", "/api/admin/ai/rules"),
  putAiRules: (rules: string) => request<AiRules>("PUT", "/api/admin/ai/rules", { rules }),
  publishRun: (id: number) => request<AgentRun | unknown>("POST", `/api/admin/ai/runs/${id}/publish`),

  // Keys
  listKeys: (serviceId: number) => request<{ keys: Key[] }>("GET", `/api/admin/services/${serviceId}/keys`),
  addKeys: (serviceId: number, keys: NewKeyInput[]) =>
    request<{ keys: Key[] }>("POST", `/api/admin/services/${serviceId}/keys`, { keys }),
  updateKey: (id: number, body: KeyPatch) => request<Key>("PATCH", `/api/admin/keys/${id}`, body),
  deleteKey: (id: number) => request<unknown>("DELETE", `/api/admin/keys/${id}`),
  revealKey: (id: number) => request<{ secret: string }>("POST", `/api/admin/keys/${id}/reveal`),
  checkKey: (id: number) => request<Key>("POST", `/api/admin/keys/${id}/check`),
  resetKey: (id: number) => request<Key>("POST", `/api/admin/keys/${id}/reset`),
  listAllKeys: () => request<{ keys: KeyRow[]; summary: KeySummary }>("GET", "/api/admin/keys"),
  keyInsights: (id: number) => request<KeyInsights>("GET", `/api/admin/keys/${id}/insights`),
  checkAllKeys: () => request<CheckSummary>("POST", "/api/admin/keys/check-all"),
  checkServiceKeys: (serviceId: number) => request<CheckSummary>("POST", `/api/admin/services/${serviceId}/check`),

  // Apps
  listApps: () => request<{ apps: App[] }>("GET", "/api/admin/apps"),
  createApp: (body: AppInput) => request<AppWithToken>("POST", "/api/admin/apps", body),
  updateApp: (id: number, body: Partial<AppInput & { active: boolean }>) =>
    request<App>("PATCH", `/api/admin/apps/${id}`, body),
  deleteApp: (id: number) => request<unknown>("DELETE", `/api/admin/apps/${id}`),
  rotateAppToken: (id: number) => request<AppWithToken>("POST", `/api/admin/apps/${id}/rotate-token`),
  listAppTokens: (id: number) => request<{ tokens: AppToken[] }>("GET", `/api/admin/apps/${id}/tokens`),
  createAppToken: (id: number, body: { name: string; expires_in_days?: number }) =>
    request<{ token: string; app_token: AppToken }>("POST", `/api/admin/apps/${id}/tokens`, body),
  renameAppToken: (id: number, tokenId: number, name: string) =>
    request<AppToken>("PATCH", `/api/admin/apps/${id}/tokens/${tokenId}`, { name }),
  revokeAppToken: (id: number, tokenId: number) => request<AppToken>("DELETE", `/api/admin/apps/${id}/tokens/${tokenId}`),

  // Events
  listEvents: (params: { service_id?: number; key_id?: number; limit?: number } = {}) =>
    request<{ events: AcnologiaEvent[] }>("GET", `/api/admin/events${qs(params)}`),

  // Users
  listUsers: () => request<{ users: User[] }>("GET", "/api/admin/users"),
  createUser: (body: { email: string; name: string; password: string; role: Role }) =>
    request<User>("POST", "/api/admin/users", body),
  updateUser: (id: number, body: Partial<{ name: string; role: Role; disabled: boolean; password: string }>) =>
    request<User>("PATCH", `/api/admin/users/${id}`, body),
  deleteUser: (id: number) => request<unknown>("DELETE", `/api/admin/users/${id}`),

  // Assistant chat
  listChatThreads: () => request<{ threads: ChatThread[]; ai: ChatAi }>("GET", "/api/admin/chat/threads"),
  createChatThread: () => request<{ thread: ChatThread; items: ChatItem[] }>("POST", "/api/admin/chat/threads", {}),
  getChatThread: (id: number) =>
    request<{ thread: ChatThread; items: ChatItem[]; turn?: ChatTurn }>("GET", `/api/admin/chat/threads/${id}`),
  deleteChatThread: (id: number) => request<unknown>("DELETE", `/api/admin/chat/threads/${id}`),
  sendChatMessage: (id: number, text: string) => request<ChatReply>("POST", `/api/admin/chat/threads/${id}/messages`, { text }),
  confirmChat: (id: number, pendingId: string, approve: boolean) =>
    request<ChatReply>("POST", `/api/admin/chat/threads/${id}/confirm`, { pending_id: pendingId, approve }),
  answerChat: (id: number, body: ChatAnswer) => request<ChatReply>("POST", `/api/admin/chat/threads/${id}/answer`, body),

  // Audit
  listAudit: () => request<{ audit: AuditEntry[] }>("GET", "/api/admin/audit"),
};

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return "Something went wrong. Try again.";
}

/** Per app MCP settings. Reading needs any signed in user; saving needs admin. */
export const mcpApi = {
  get: (appId: number) => request<MCPView>("GET", `/api/admin/apps/${appId}/mcp`),
  put: (appId: number, body: MCPSettingsInput) => request<MCPView>("PUT", `/api/admin/apps/${appId}/mcp`, body),
};
