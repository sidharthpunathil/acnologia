/** Error returned by the Cogito consumer API (any non-2xx response). */
export class CogitoError extends Error {
  readonly status: number;
  readonly code: string;
  readonly body: unknown;

  constructor(message: string, status: number, code: string, body?: unknown) {
    super(message);
    this.name = "CogitoError";
    this.status = status;
    this.code = code;
    this.body = body;
  }
}

/**
 * Thrown when no key can be leased right now (HTTP 503). `code` is `no_key_available`
 * when every key is cooling down, exhausted or excluded, and `all_keys_busy` when every
 * usable key is at its concurrency limit (see `busy` and `AllKeysBusyError`).
 */
export class NoKeyAvailableError extends CogitoError {
  /** Seconds to wait before retrying, when the server knows it. */
  readonly retryAfter: number | null;

  constructor(message: string, retryAfter: number | null, body?: unknown, code: string = "no_key_available") {
    super(message, 503, code, body);
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
