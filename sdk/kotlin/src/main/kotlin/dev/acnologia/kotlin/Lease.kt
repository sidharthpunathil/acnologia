package dev.acnologia.kotlin

import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.Request
import java.time.Instant
import java.time.OffsetDateTime
import kotlin.time.Duration

/**
 * A key handed out by the pool. Report how it went with [report]: on services with a
 * concurrency limit an unreported lease holds one of the key's slots until its TTL runs out.
 *
 * [toString] never includes the secret.
 */
public class Lease internal constructor(
    private val client: Acnologia,
    /** The lease id, used to report and renew. */
    public val leaseId: String,
    /** The id of the key behind this lease. */
    public val keyId: Long,
    /** The service slug. */
    public val service: String,
    /** The provider secret. Do not log it. */
    public val secret: String,
    /** Where the provider expects the key. */
    public val auth: Auth,
    /** The provider base URL. */
    public val baseUrl: String,
    /** When the lease expires, or null if the server did not say. */
    public val expiresAt: Instant?,
    /** True when the key is reserved for this lease alone. */
    public val exclusive: Boolean,
) {
    internal companion object {
        fun from(client: Acnologia, w: LeaseWire?): Lease? = w?.let {
            Lease(client, it.leaseId, it.keyId, it.service, it.secret, it.auth, it.baseUrl, parseTime(it.expiresAt), it.exclusive)
        }

        fun parseTime(s: String): Instant? =
            if (s.isEmpty()) null else runCatching { OffsetDateTime.parse(s).toInstant() }.getOrNull()
    }

    /**
     * Sends the outcome of this lease. When [Report.rotate] is true and another key is
     * available, the next lease comes back; otherwise null.
     *
     * @throws ApiException when the server refuses the report (for example 409 when it was already reported).
     */
    public suspend fun report(report: Report): Lease? {
        val body = buildJsonObject {
            put("outcome", report.outcome.wire)
            if (report.statusCode != 0) put("status_code", report.statusCode)
            if (report.retryAfter.isPositive()) put("retry_after_sec", ceilSeconds(report.retryAfter))
            if (report.message.isNotEmpty()) put("message", report.message)
            if (report.rotate) put("rotate", true)
        }
        val out = client.call("POST", "/api/v1/leases/${pathEscape(leaseId)}/report", body, ReportWire.serializer())
        return from(client, out.next)
    }

    /** Shortcut for [report] with the fields spelled out. */
    public suspend fun report(
        outcome: Outcome,
        statusCode: Int = 0,
        retryAfter: Duration = Duration.ZERO,
        message: String = "",
        rotate: Boolean = false,
    ): Lease? = report(Report(outcome, statusCode, retryAfter, message, rotate))

    /**
     * Extends this open lease so a long job keeps its key. A zero [ttl] uses the server
     * default (two minutes). Returns the new expiry.
     *
     * @throws ApiException 404 `lease_not_found` when the lease is no longer open.
     */
    public suspend fun renew(ttl: Duration = Duration.ZERO): Instant? {
        val body = buildJsonObject { if (ttl.isPositive()) put("ttl_sec", ceilSeconds(ttl)) }
        val out = client.call("POST", "/api/v1/leases/${pathEscape(leaseId)}/renew", body, RenewWire.serializer())
        return parseTime(out.expiresAt)
    }

    private val value: String
        get() = if (auth.type == "bearer" && auth.prefix.isEmpty()) "Bearer $secret" else auth.prefix + secret

    private val headerName: String get() = auth.name.ifEmpty { "Authorization" }
    private val queryName: String get() = auth.name.ifEmpty { "api_key" }

    /**
     * Returns a copy of [request] with the secret where the provider expects it: a query
     * parameter for `query`, a header for `header`, or `Authorization: Bearer <secret>` for
     * `bearer`. A non empty [Auth.prefix] goes in front of the secret for every type and, for
     * bearer, replaces `"Bearer "`. Header names default to `Authorization`.
     */
    public fun apply(request: Request): Request =
        if (auth.type == "query") {
            request.newBuilder().url(request.url.newBuilder().setQueryParameter(queryName, value).build()).build()
        } else {
            request.newBuilder().header(headerName, value).build()
        }

    /**
     * Puts the secret into the given maps, for use with any HTTP client: `query` auth adds to
     * [query], the other types add to [headers].
     */
    public fun apply(headers: MutableMap<String, String>, query: MutableMap<String, String>) {
        if (auth.type == "query") query[queryName] = value else headers[headerName] = value
    }

    /** Joins the provider base URL with [path] and [query]. The secret is not added; use [apply]. */
    public fun url(path: String, query: Map<String, Any?> = emptyMap()): String {
        val u = baseUrl.trimEnd('/') + "/" + path.trimStart('/')
        val q = encodeQuery(query)
        return if (q.isEmpty()) u else "$u?$q"
    }

    /** Builds a GET [Request] for [path] on the provider with the key already applied. */
    public fun request(path: String, query: Map<String, Any?> = emptyMap()): Request =
        apply(Request.Builder().url(url(path, query).toHttpUrl()).build())

    override fun toString(): String =
        "Lease(leaseId=$leaseId, keyId=$keyId, service=$service, secret=[redacted], auth=$auth, " +
            "baseUrl=$baseUrl, expiresAt=$expiresAt, exclusive=$exclusive)"
}
