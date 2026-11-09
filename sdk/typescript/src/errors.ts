/**
 * Error returned by the Acnologia consumer API (any non-2xx response). Codes worth checking
 * here include `empty_group`, `bad_params`, `no_route`, `no_route_worked`,
 * `proxy_not_allowed`, `lease_not_allowed` and `loop_detected`.
 */
export class AcnologiaError extends Error {
  readonly status: number;
  readonly code: string;
  readonly body: unknown;
  /** Seconds to wait before retrying (`retry_after_sec` or `Retry-After`), when the server gave one. */
  readonly retryAfter: number | null;

  constructor(message: string, status: number, code: string, body?: unknown, retryAfter: number | null = null) {
    super(message);
    this.name = "AcnologiaError";
    this.status = status;
    this.code = code;
    this.body = body;
    this.retryAfter = retryAfter;
  }
}

/**
 * The app used up a daily or monthly request or budget limit (HTTP 429, code
 * `app_limit_reached`). `limit` is `daily_requests`, `monthly_requests`, `daily_budget` or
 * `monthly_budget`; `resetAt` (RFC 3339) is when it resets.
 */
export class AppLimitReachedError extends AcnologiaError {
  readonly limit: string;
  readonly resetAt: string | null;

  constructor(
    message: string,
    opts: { limit?: string; resetAt?: string | null; retryAfter?: number | null; status?: number } = {},
    body?: unknown,
  ) {
    super(message, opts.status ?? 429, "app_limit_reached", body, opts.retryAfter ?? null);
    this.name = "AppLimitReachedError";
    this.limit = opts.limit ?? "";
    this.resetAt = opts.resetAt ?? null;
  }
}

/**
 * The share behind this token was revoked, paused or expired (HTTP 403, code `share_revoked`,
 * `share_paused` or `share_expired`). `shareStatus` is `revoked`, `paused` or `expired` (also
 * sent as `X-Acnologia-Share`); `status` stays the HTTP status.
 */
export class ShareUnavailableError extends AcnologiaError {
  readonly shareStatus: string;

  constructor(message: string, shareStatus: string, code: string, body?: unknown, status: number = 403) {
    super(message, status, code, body);
    this.name = "ShareUnavailableError";
    this.shareStatus = shareStatus;
  }
}

/**
 * Thrown when no key can be leased right now (HTTP 503). `code` is `no_key_available`
 * when every key is cooling down, exhausted or excluded, and `all_keys_busy` when every
 * usable key is at its concurrency limit (see `busy` and `AllKeysBusyError`).
 */
export class NoKeyAvailableError extends AcnologiaError {
  /** Seconds to wait before retrying, when the server knows it. */
  readonly retryAfter: number | null;

  constructor(message: string, retryAfter: number | null, body?: unknown, code: string = "no_key_available") {
    super(message, 503, code, body, retryAfter);
    this.name = "NoKeyAvailableError";
    this.retryAfter = retryAfter;
  }

  /** True when every usable key is at its concurrency limit (`all_keys_busy`). Clears as soon as another caller reports. */
  get busy(): boolean {
    return this.code === "all_keys_busy";
  }
}

/**
 * Every usable key is at its concurrency limit (HTTP 503, code `all_keys_busy`).
 * Extends `NoKeyAvailableError`, so existing `instanceof NoKeyAvailableError` checks still match.
 * Retry after `retryAfter` seconds (usually 1) or lease with `waitSec`.
 */
export class AllKeysBusyError extends NoKeyAvailableError {
  constructor(message: string, retryAfter: number | null, body?: unknown) {
    super(message, retryAfter, body, "all_keys_busy");
    this.name = "AllKeysBusyError";
  }
}

/**
 * The provider keeps failing and its circuit breaker is open (HTTP 503, code
 * `service_unavailable`). Acnologia paused traffic to it; wait `retryAfter` seconds before
 * retrying. This is not a `NoKeyAvailableError`.
 */
export class ServiceUnavailableError extends AcnologiaError {
  /** Seconds to wait before retrying, when the server knows it. */
  readonly retryAfter: number | null;

  constructor(message: string, retryAfter: number | null, body?: unknown, status: number = 503) {
    super(message, status, "service_unavailable", body, retryAfter);
    this.name = "ServiceUnavailableError";
    this.retryAfter = retryAfter;
  }
}

export interface RotationErrorOptions {
  statusCode?: number;
  message?: string;
}

/** Base class for errors that tell `withKey` to report the key and rotate. */
export abstract class KeyRotationError extends Error {
  readonly statusCode: number | undefined;
  abstract readonly outcome: "rate_limited" | "quota_exhausted" | "invalid";

  constructor(defaultMessage: string, opts: RotationErrorOptions = {}) {
    super(opts.message ?? defaultMessage);
    this.statusCode = opts.statusCode;
  }
}

/** Throw from a `withKey` callback when the provider rate limited the key. */
export class RateLimitedError extends KeyRotationError {
  readonly outcome = "rate_limited" as const;
  /** Seconds the key should cool down, if the provider said so. */
  readonly retryAfter: number | undefined;

  constructor(retryAfter?: number | null, opts: RotationErrorOptions = {}) {
    super("key was rate limited", opts);
    this.name = "RateLimitedError";
    this.retryAfter = retryAfter ?? undefined;
  }
}

/** Throw from a `withKey` callback when the key's quota is used up. */
export class QuotaExhaustedError extends KeyRotationError {
  readonly outcome = "quota_exhausted" as const;

  constructor(opts: RotationErrorOptions = {}) {
    super("key quota exhausted", opts);
    this.name = "QuotaExhaustedError";
  }
}

/** Throw from a `withKey` callback when the provider rejected the key. */
export class InvalidKeyError extends KeyRotationError {
  readonly outcome = "invalid" as const;

  constructor(opts: RotationErrorOptions = {}) {
    super("key is invalid", opts);
    this.name = "InvalidKeyError";
  }
}
