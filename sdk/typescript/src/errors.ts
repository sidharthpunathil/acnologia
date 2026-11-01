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

/** Thrown when the pool has no usable key for a service (HTTP 503, code `no_key_available`). */
export class NoKeyAvailableError extends CogitoError {
  /** Seconds until the earliest cooldown ends, when the server knows it. */
  readonly retryAfter: number | null;

  constructor(message: string, retryAfter: number | null, body?: unknown) {
    super(message, 503, "no_key_available", body);
    this.name = "NoKeyAvailableError";
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
