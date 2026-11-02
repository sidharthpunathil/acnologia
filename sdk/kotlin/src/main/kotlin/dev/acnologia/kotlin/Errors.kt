package dev.acnologia.kotlin

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlin.math.max
import kotlin.math.roundToLong
import kotlin.time.Duration
import kotlin.time.Duration.Companion.milliseconds

/**
 * Base class for every exception the SDK throws. Network failures are thrown as
 * a plain [AcnologiaException] with the original error as the cause.
 */
public open class AcnologiaException(message: String, cause: Throwable? = null) : RuntimeException(message, cause)

/**
 * A non-2xx answer from the consumer API that has no more specific type.
 * Errors from the server look like `{"error": {"code": "...", "message": "..."}}`.
 */
public open class ApiException(
    /** The HTTP status code. */
    public val statusCode: Int,
    /** The error code, such as `lease_not_found`, or an empty string. */
    public val code: String,
    /** The error message sent by the server. */
    public val errorMessage: String,
) : AcnologiaException(
    if (code.isNotEmpty()) "acnologia: http $statusCode: $code: $errorMessage" else "acnologia: http $statusCode: $errorMessage",
) {
    public companion object {
        /**
         * Turns an error answer from Acnologia into the matching exception. The
         * SDK calls this for every consumer API call. You can call it yourself
         * on a proxy response that Acnologia produced rather than the provider
         * (one without an `X-Acnologia-Key-Id` header).
         */
        public fun fromResponse(status: Int, body: String?): ApiException {
            var code = ""
            var message = ""
            var retryAfterSec = 0.0
            val text = body ?: ""
            try {
                val root = Json.parseToJsonElement(text)
                if (root is JsonObject) {
                    val err = root["error"] as? JsonObject
                    code = (err?.get("code"))?.jsonPrimitive?.contentOrNull ?: ""
                    message = (err?.get("message"))?.jsonPrimitive?.contentOrNull ?: ""
                    retryAfterSec = root["retry_after_sec"]?.jsonPrimitive?.doubleOrNull ?: 0.0
                }
            } catch (_: Exception) {
                // not JSON, fall through to the raw body
            }
            val retryAfter = (max(retryAfterSec, 0.0) * 1000).roundToLong().milliseconds

            if (code == "no_key_available" || code == "all_keys_busy" || (status == 503 && code.isEmpty())) {
                return if (code == "all_keys_busy") {
                    AllKeysBusyException(message, retryAfter)
                } else {
                    NoKeyAvailableException(status, "no_key_available", message, retryAfter)
                }
            }
            if (code == "service_unavailable") return ServiceUnavailableException(status, message, retryAfter)
            if (message.isEmpty() && code.isEmpty()) {
                message = text.trim().take(200).ifEmpty { "HTTP $status" }
            }
            return when (status) {
                401 -> UnauthenticatedException(code, message)
                403 -> ForbiddenException(code, message)
                else -> ApiException(status, code, message)
            }
        }
    }
}

/** HTTP 401: the app token is missing or wrong. */
public class UnauthenticatedException(code: String, message: String) : ApiException(401, code, message)

/**
 * HTTP 403: the app is disabled, or not allowed this service or mode. The code is one of
 * `unauthenticated`, `service_not_allowed`, `proxy_not_allowed` or `lease_not_allowed`.
 */
public class ForbiddenException(code: String, message: String) : ApiException(403, code, message)

/**
 * HTTP 503: no key can be leased right now. The code is `no_key_available` when every key is
 * cooling down, exhausted or excluded. When every usable key is at its concurrency limit the
 * subclass [AllKeysBusyException] is thrown instead.
 */
public open class NoKeyAvailableException(
    statusCode: Int,
    code: String,
    message: String,
    /** How long to wait before retrying, [Duration.ZERO] if unknown. */
    public val retryAfter: Duration,
) : ApiException(statusCode, code, message) {
    /** True when every usable key is at its concurrency limit (`all_keys_busy`). */
    public val isBusy: Boolean get() = code == "all_keys_busy"
}

/**
 * HTTP 503 `all_keys_busy`: usable keys exist but every one is at its concurrency limit, or at a
 * rate limit its provider publishes. Retry after [retryAfter] (usually one second) or lease with a
 * wait. Extends [NoKeyAvailableException], so existing catches still match.
 */
public class AllKeysBusyException(message: String, retryAfter: Duration) :
    NoKeyAvailableException(503, "all_keys_busy", message, retryAfter)

/**
 * HTTP 503 `service_unavailable`: the provider keeps failing and its circuit breaker is open.
 * Wait [retryAfter]. Acnologia sends this from the proxy; see [ApiException.fromResponse].
 */
public class ServiceUnavailableException(
    statusCode: Int,
    message: String,
    /** How long to wait before retrying, [Duration.ZERO] if unknown. */
    public val retryAfter: Duration,
) : ApiException(statusCode, "service_unavailable", message)

/**
 * Base class for the exceptions that tell [Acnologia.withKey] to report the key and retry with the
 * next one. Throw a subclass from your block, directly or as the cause of another exception.
 */
public sealed class KeyRotationException(
    base: String,
    /** The provider's HTTP status, 0 if unknown. */
    public val statusCode: Int,
    detail: String?,
    cause: Throwable?,
) : AcnologiaException(if (detail.isNullOrEmpty()) base else "$base: $detail", cause) {
    /** The text sent as the report message (the secret is scrubbed first). */
    public val detail: String = detail ?: ""

    /** The outcome reported for the key. */
    public abstract val outcome: Outcome

    internal open fun toReport(secret: String): Report =
        Report(outcome = outcome, statusCode = statusCode, message = scrubMessage(detail, secret))
}

/** The provider rate limited the key. Throw it from a [Acnologia.withKey] block to rotate. */
public class RateLimitedException(
    /** How long the key should cool down, [Duration.ZERO] if unknown. */
    public val retryAfter: Duration = Duration.ZERO,
    statusCode: Int = 0,
    message: String? = null,
    cause: Throwable? = null,
) : KeyRotationException(
    if (retryAfter.isPositive()) "acnologia: rate limited (retry after $retryAfter)" else "acnologia: rate limited",
    statusCode, message, cause,
) {
    override val outcome: Outcome get() = Outcome.RATE_LIMITED
    override fun toReport(secret: String): Report = super.toReport(secret).copy(retryAfter = retryAfter)
}

/** The key has used up its quota. Throw it from a [Acnologia.withKey] block to rotate. */
public class QuotaExhaustedException(statusCode: Int = 0, message: String? = null, cause: Throwable? = null) :
    KeyRotationException("acnologia: quota exhausted", statusCode, message, cause) {
    override val outcome: Outcome get() = Outcome.QUOTA_EXHAUSTED
}

/** The provider rejected the key. Throw it from a [Acnologia.withKey] block to rotate. */
public class InvalidKeyException(statusCode: Int = 0, message: String? = null, cause: Throwable? = null) :
    KeyRotationException("acnologia: invalid key", statusCode, message, cause) {
    override val outcome: Outcome get() = Outcome.INVALID
}
