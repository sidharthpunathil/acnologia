package dev.acnologia.kotlin

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
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
    /** Where the service's usage guide is served, `GET /api/v1/services/{slug}/guide`. */
    @SerialName("guide_url") val guideUrl: String = "",
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

/** The token's own access, from `GET /api/v1/me`. */
@Serializable
public data class Me(
    val app: MeApp = MeApp(),
    val limits: AppLimits = AppLimits(),
    val usage: AppUsage = AppUsage(),
    /** The first limit the app has reached, or null. */
    @SerialName("limit_hit") val limitHit: LimitHit? = null,
    val services: List<MeService> = emptyList(),
    /** Null for an ordinary app; set for a share's token, also when the share stopped. */
    val share: MeShare? = null,
)

/** The app behind the token. */
@Serializable
public data class MeApp(val id: Long = 0, val name: String = "")

/** The app's request and budget limits; null means no limit. */
@Serializable
public data class AppLimits(
    @SerialName("daily_requests") val dailyRequests: Long? = null,
    @SerialName("monthly_requests") val monthlyRequests: Long? = null,
    @SerialName("daily_budget") val dailyBudget: Double? = null,
    @SerialName("monthly_budget") val monthlyBudget: Double? = null,
)

/** The app's use so far today and this month. */
@Serializable
public data class AppUsage(
    @SerialName("today_requests") val todayRequests: Long = 0,
    @SerialName("today_cost") val todayCost: Double = 0.0,
    @SerialName("month_requests") val monthRequests: Long = 0,
    @SerialName("month_cost") val monthCost: Double = 0.0,
    /** RFC 3339 time of the last request, or null. */
    @SerialName("last_used_at") val lastUsedAt: String? = null,
    @SerialName("limit_hit") val limitHit: LimitHit? = null,
)

/**
 * A limit the app has reached. [limit] is `daily_requests`, `monthly_requests`, `daily_budget`
 * or `monthly_budget`; [resetAt] is an RFC 3339 time.
 */
@Serializable
public data class LimitHit(val limit: String = "", @SerialName("reset_at") val resetAt: String = "")

/** A service in [Me.services]. [available] counts free keys for a share's token, 1 or 0 for an app. */
@Serializable
public data class MeService(
    val slug: String = "",
    val name: String = "",
    val provider: String = "",
    val category: String = "",
    val available: Int = 0,
    /** The logo as a `data:` URI, or null. */
    @SerialName("logo_url") val logoUrl: String? = null,
)

/** The share behind a share's token. [status] is `active`, `paused`, `expired` or `revoked`. */
@Serializable
public data class MeShare(
    val id: String = "",
    val name: String = "",
    @SerialName("owner_name") val ownerName: String = "",
    val recipient: String = "",
    val label: String = "",
    val status: String = "",
    val mode: String = "",
    /** RFC 3339 end time, or null. */
    @SerialName("ends_at") val endsAt: String? = null,
)

/** A service's usage guide, from `GET /api/v1/services/{slug}/guide`. */
@Serializable
public data class Guide(
    val markdown: String = "",
    /** True when the guide was generated rather than written. */
    val generated: Boolean = false,
    /** The structured guide as JSON. */
    val guide: JsonElement = JsonNull,
)

/** A capability the app may call: one call, served by whichever route can. */
@Serializable
public data class Capability(
    val slug: String = "",
    val name: String = "",
    val description: String = "",
    val strategy: String? = null,
    /** The params the capability takes, as JSON. */
    val params: JsonElement = JsonNull,
    val routes: List<CapabilityRoute> = emptyList(),
    /** The URL to POST the params to. */
    val url: String = "",
)

/** One route of a [Capability]. [mapped] is true when its answer is mapped to a common shape. */
@Serializable
public data class CapabilityRoute(val service: String = "", val mapped: Boolean = false)

/**
 * How to use the forward proxy, from `GET /api/v1/proxy`. Holds no token: [proxyUrlWithToken]
 * and the `HTTPS_PROXY` entry of [env] carry a `<app token>` placeholder, so [toString] is safe
 * to log. Use [Acnologia.forwardProxyUrl] or [Acnologia.forwardProxyEnv] for the real values.
 */
@Serializable
public data class ForwardProxy(
    @SerialName("proxy_url") val proxyUrl: String = "",
    @SerialName("proxy_url_with_token") val proxyUrlWithToken: String = "",
    @SerialName("ca_url") val caUrl: String = "",
    @SerialName("ca_fingerprint") val caFingerprint: String = "",
    /** True when HTTPS to hosts that are no service is tunnelled as is. */
    val passthrough: Boolean = false,
    val hosts: List<ForwardProxyHost> = emptyList(),
    val env: Map<String, String> = emptyMap(),
    /** The request header that picks a service when several match a host. */
    @SerialName("service_header") val serviceHeader: String = "",
)

/** A provider host the forward proxy routes to [service]. */
@Serializable
public data class ForwardProxyHost(
    val host: String = "",
    val service: String = "",
    val name: String = "",
    val path: String = "",
)

/**
 * Per call options for proxy requests.
 *
 * @property idempotencyKey sent as `Idempotency-Key`: a retry with the same key replays the first answer.
 * @property noCache sends `Cache-Control: no-cache` to skip Acnologia's response cache.
 */
public data class ProxyOptions(
    val idempotencyKey: String? = null,
    val noCache: Boolean = false,
)

@Serializable
internal class CapabilitiesWire(val capabilities: List<Capability> = emptyList())

@Serializable
internal class ServicesWire(val services: List<Service> = emptyList())

@Serializable
internal class ReportWire(val ok: Boolean = false, val next: LeaseWire? = null)

@Serializable
internal class RenewWire(val ok: Boolean = false, @SerialName("expires_at") val expiresAt: String = "")
