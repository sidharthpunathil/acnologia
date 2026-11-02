package dev.acnologia.kotlin

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlin.time.Duration

/** How a leased key behaved. [wire] is the value sent to the server. */
public enum class Outcome(public val wire: String) {
    /** The call worked. */
    OK("ok"),

    /** The provider rate limited the key; it cools down for the retry after. */
    RATE_LIMITED("rate_limited"),

    /** The key used up its quota. */
    QUOTA_EXHAUSTED("quota_exhausted"),

    /** The provider rejected the key. */
    INVALID("invalid"),

    /** Something else went wrong. */
    ERROR("error"),

    /** The account is at its concurrency cap; the key steps aside briefly. */
    BUSY("busy"),

    /** The provider suspended the account; the key leaves rotation. */
    BANNED("banned");

    override fun toString(): String = wire
}

/**
 * Where a provider expects the key. [type] is `query`, `header` or `bearer`; [name] is the query
 * parameter or header name. [prefix], when not empty, goes in front of the secret for every type
 * (for bearer it replaces the default `"Bearer "`).
 */
@Serializable
public data class Auth(
    val type: String = "header",
    val name: String = "",
    val prefix: String = "",
)

/** A service this app token may use, from `GET /api/v1/services`. */
@Serializable
public data class Service(
    val slug: String = "",
    val name: String = "",
    val provider: String = "",
    val category: String = "",
    /** True when a key is free to lease right now. */
    val available: Boolean = false,
    @SerialName("base_url") val baseUrl: String = "",
    val auth: Auth = Auth(),
)

/**
 * Optional parameters for [Acnologia.lease].
 *
 * @property excludeKeyIds keys the server must not hand out, for example keys you already tried.
 * @property ttl lease lifetime, sent as `ttl_sec` rounded up. Zero uses the server default
 *   (120 seconds on services with a concurrency limit, 300 otherwise).
 * @property wait how long the server may wait (at most [Acnologia.MAX_WAIT]) for a free slot when
 *   every usable key is at its concurrency limit. Zero fails fast with [AllKeysBusyException].
 * @property exclusive keep the key from every other caller until this lease ends.
 */
public data class LeaseOptions(
    val excludeKeyIds: List<Long> = emptyList(),
    val ttl: Duration = Duration.ZERO,
    val wait: Duration = Duration.ZERO,
    val exclusive: Boolean = false,
)

/**
 * How a leased key behaved. Set [rotate] to receive the next key in the same round trip.
 *
 * @property statusCode the provider's HTTP status, 0 to omit.
 * @property retryAfter how long the key should cool down, sent as `retry_after_sec` rounded up.
 * @property message a short note shown on the dashboard. Never include the secret.
 */
public data class Report(
    val outcome: Outcome = Outcome.OK,
    val statusCode: Int = 0,
    val retryAfter: Duration = Duration.ZERO,
    val message: String = "",
    val rotate: Boolean = false,
)

@Serializable
internal class LeaseWire(
    @SerialName("lease_id") val leaseId: String = "",
    @SerialName("key_id") val keyId: Long = 0,
    val service: String = "",
    val secret: String = "",
    val auth: Auth = Auth(),
    @SerialName("base_url") val baseUrl: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    val exclusive: Boolean = false,
)

@Serializable
internal class ServicesWire(val services: List<Service> = emptyList())

@Serializable
internal class ReportWire(val ok: Boolean = false, val next: LeaseWire? = null)

@Serializable
internal class RenewWire(val ok: Boolean = false, @SerialName("expires_at") val expiresAt: String = "")
