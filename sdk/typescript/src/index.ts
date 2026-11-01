export { Cogito, Lease, Proxy, VERSION } from "./client.js";
export {
  CogitoError,
  NoKeyAvailableError,
  KeyRotationError,
  RateLimitedError,
  QuotaExhaustedError,
  InvalidKeyError,
} from "./errors.js";
export type { RotationErrorOptions } from "./errors.js";
export type {
  AuthSpec,
  AuthType,
  CogitoOptions,
  FetchLike,
  HeadersLike,
  LeaseJSON,
  LeaseOptions,
  Outcome,
  QueryInit,
  ReportOptions,
  Service,
  WithKeyOptions,
} from "./types.js";
