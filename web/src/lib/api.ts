import type {
  App,
  AppInput,
  AppWithToken,
  AuditEntry,
  AuthState,
  CogitoEvent,
  Key,
  KeyPatch,
  NewKeyInput,
  Overview,
  ProviderTemplate,
  Role,
  Service,
  ServiceInput,
  User,
} from "./types";

export class ApiError extends Error {
  status: number;
  code: string;
  retryAfterSec?: number;

  constructor(status: number, code: string, message: string, retryAfterSec?: number) {
    super(message);
    this.status = status;
    this.code = code;
    this.retryAfterSec = retryAfterSec;
  }
}

type UnauthorizedHandler = () => void;
let onUnauthorized: UnauthorizedHandler | null = null;

/** Registered by the auth store so any 401 sends the user back to login. */
export function setUnauthorizedHandler(fn: UnauthorizedHandler) {
  onUnauthorized = fn;
}

type Method = "GET" | "POST" | "PATCH" | "DELETE";

async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: "include",
    headers: { Accept: "application/json" },
  };
  if (method !== "GET") {
    // CSRF guard: the server rejects mutating requests without both headers.
    const h = init.headers as Record<string, string>;
    h["Content-Type"] = "application/json";
    h["X-Requested-With"] = "cogito";
    init.body = JSON.stringify(body ?? {});
  }

  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    throw new ApiError(0, "network", "Cannot reach the Cogito server");
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
      error?: { code?: string; message?: string };
      retry_after_sec?: number;
    };
    const code = payload.error?.code ?? `http_${res.status}`;
    const message = payload.error?.message ?? (res.statusText || `Request failed (${res.status})`);
    // Auth endpoints answer 401 for bad credentials; that is not a session expiry.
    if (res.status === 401 && !path.startsWith("/api/auth/")) {
      onUnauthorized?.();
    }
    throw new ApiError(res.status, code, message, payload.retry_after_sec);
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

export const api = {
  // Auth
  authState: () => request<AuthState>("GET", "/api/auth/state"),
  setup: (body: { email: string; name: string; password: string }) =>
    request<unknown>("POST", "/api/auth/setup", body),
  login: (body: { email: string; password: string }) => request<unknown>("POST", "/api/auth/login", body),
  logout: () => request<unknown>("POST", "/api/auth/logout"),

  // Me
  me: () => request<User>("GET", "/api/admin/me"),
  updateMe: (body: { name?: string; current_password: string; password?: string }) => request<User>("PATCH", "/api/admin/me", body),

  // Overview
  overview: () => request<Overview>("GET", "/api/admin/overview"),

  // Services
  listServices: () => request<{ services: Service[] }>("GET", "/api/admin/services"),
  getService: (id: number) => request<Service>("GET", `/api/admin/services/${id}`),
  createService: (body: ServiceInput) => request<Service>("POST", "/api/admin/services", body),
  updateService: (id: number, body: Partial<ServiceInput>) =>
    request<Service>("PATCH", `/api/admin/services/${id}`, body),
  deleteService: (id: number) => request<unknown>("DELETE", `/api/admin/services/${id}`),
  listProviders: () => request<{ providers: ProviderTemplate[] }>("GET", "/api/admin/providers"),

  // Keys
  listKeys: (serviceId: number) => request<{ keys: Key[] }>("GET", `/api/admin/services/${serviceId}/keys`),
  addKeys: (serviceId: number, keys: NewKeyInput[]) =>
    request<{ keys: Key[] }>("POST", `/api/admin/services/${serviceId}/keys`, { keys }),
  updateKey: (id: number, body: KeyPatch) => request<Key>("PATCH", `/api/admin/keys/${id}`, body),
  deleteKey: (id: number) => request<unknown>("DELETE", `/api/admin/keys/${id}`),
  revealKey: (id: number) => request<{ secret: string }>("POST", `/api/admin/keys/${id}/reveal`),
  checkKey: (id: number) => request<Key>("POST", `/api/admin/keys/${id}/check`),
  resetKey: (id: number) => request<Key>("POST", `/api/admin/keys/${id}/reset`),

  // Apps
  listApps: () => request<{ apps: App[] }>("GET", "/api/admin/apps"),
  createApp: (body: AppInput) => request<AppWithToken>("POST", "/api/admin/apps", body),
  updateApp: (id: number, body: Partial<AppInput & { active: boolean }>) =>
    request<App>("PATCH", `/api/admin/apps/${id}`, body),
  deleteApp: (id: number) => request<unknown>("DELETE", `/api/admin/apps/${id}`),
  rotateAppToken: (id: number) => request<AppWithToken>("POST", `/api/admin/apps/${id}/rotate-token`),

  // Events
  listEvents: (params: { service_id?: number; key_id?: number; limit?: number } = {}) =>
    request<{ events: CogitoEvent[] }>("GET", `/api/admin/events${qs(params)}`),

  // Users
  listUsers: () => request<{ users: User[] }>("GET", "/api/admin/users"),
  createUser: (body: { email: string; name: string; password: string; role: Role }) =>
    request<User>("POST", "/api/admin/users", body),
  updateUser: (id: number, body: Partial<{ name: string; role: Role; disabled: boolean; password: string }>) =>
    request<User>("PATCH", `/api/admin/users/${id}`, body),
  deleteUser: (id: number) => request<unknown>("DELETE", `/api/admin/users/${id}`),

  // Audit
  listAudit: () => request<{ audit: AuditEntry[] }>("GET", "/api/admin/audit"),
};

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return "Something went wrong";
}
