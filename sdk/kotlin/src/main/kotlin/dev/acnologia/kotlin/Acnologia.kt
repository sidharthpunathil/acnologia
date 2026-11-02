package dev.acnologia.kotlin

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.isActive
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.serialization.DeserializationStrategy
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import java.net.URLEncoder
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds

/**
 * Coroutine client for an Acnologia key pool, authenticated with an app token (`acn_...`;
 * tokens with the older `cog_` prefix still work).
 *
 * ```
 * val acn = Acnologia.fromEnv() // ACNOLOGIA_URL, ACNOLOGIA_TOKEN
 * val body = acn.withKey("serpapi") { lease ->
 *     http.newCall(lease.request("/search.json", mapOf("q" to "coffee"))).await().use { res ->
 *         if (res.code == 429) throw RateLimitedException(1.hours, 429)
 *         res.body!!.string()
 *     }
 * }
 * ```
 *
 * Instances are thread safe. [toString] never includes the token.
 *
 * @param baseUrl the Acnologia server, for example `http://localhost:8080`.
 * @param token the app token.
 * @param httpClient the OkHttp client for API calls. The default has a 75 second call and read
 *   timeout, longer than [MAX_WAIT]. Proxy calls use a copy that never follows redirects.
 * @param userAgent sent as `User-Agent`, `acnologia-kotlin/<version>` by default.
 */
public class Acnologia(
    baseUrl: String,
    private val token: String,
    httpClient: OkHttpClient? = null,
    public val userAgent: String = "acnologia-kotlin/$VERSION",
) {
    /** The server base URL, without a trailing slash. */
    public val baseUrl: String = baseUrl.trimEnd('/')

    internal val http: OkHttpClient
    internal val proxyHttp: OkHttpClient

    init {
        require(this.baseUrl.isNotEmpty()) { "acnologia: base URL is required (or set ACNOLOGIA_URL)" }
        require(token.isNotEmpty()) { "acnologia: app token is required (or set ACNOLOGIA_TOKEN)" }
        val base = httpClient ?: OkHttpClient.Builder()
            .callTimeout(DEFAULT_TIMEOUT.inWholeMilliseconds, TimeUnit.MILLISECONDS)
            .readTimeout(DEFAULT_TIMEOUT.inWholeMilliseconds, TimeUnit.MILLISECONDS)
            .build()
        http = base
        // Never follow redirects on the proxy, so the app token is not replayed to another host.
        // The default call timeout is dropped because proxied bodies may stream for longer.
        proxyHttp = base.newBuilder().followRedirects(false).followSslRedirects(false).apply {
            if (httpClient == null) callTimeout(0, TimeUnit.MILLISECONDS)
        }.build()
    }

    public companion object {
        /** The SDK version. */
        public const val VERSION: String = "0.1.0"

        /** Default timeout for API calls; longer than [MAX_WAIT]. */
        public val DEFAULT_TIMEOUT: Duration = 75.seconds

        /** The longest lease wait the server accepts. */
        public val MAX_WAIT: Duration = 60.seconds

        internal val json = Json { ignoreUnknownKeys = true; coerceInputValues = true }

        /**
         * Creates a client from the `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN` environment variables.
         *
         * @throws IllegalArgumentException when either is missing.
         */
        public fun fromEnv(httpClient: OkHttpClient? = null): Acnologia = fromEnv(System.getenv(), httpClient)

        internal fun fromEnv(env: Map<String, String>, httpClient: OkHttpClient? = null): Acnologia =
            Acnologia(env["ACNOLOGIA_URL"] ?: "", env["ACNOLOGIA_TOKEN"] ?: "", httpClient)
    }

    override fun toString(): String = "Acnologia(baseUrl=$baseUrl, token=[redacted])"

    /** Lists the services this app token may use. */
    public suspend fun services(): List<Service> =
        call("GET", "/api/v1/services", null, ServicesWire.serializer()).services

    /**
     * Asks the pool for a key. Always [Lease.report] the lease: on services with a concurrency
     * limit it holds one of the key's slots until it is reported or its TTL expires.
     *
     * @throws NoKeyAvailableException when every key is cooling down, exhausted or excluded.
     * @throws AllKeysBusyException when every usable key is at its concurrency limit and the wait ran out.
     * @throws IllegalArgumentException when [LeaseOptions.wait] is negative or over [MAX_WAIT].
     */
    public suspend fun lease(service: String, options: LeaseOptions = LeaseOptions()): Lease {
        require(!options.wait.isNegative() && options.wait <= MAX_WAIT) {
            "acnologia: lease wait must be between 0 and $MAX_WAIT"
        }
        val body = buildJsonObject {
            put("service", service)
            if (options.excludeKeyIds.isNotEmpty()) putJsonArray("exclude_key_ids") { options.excludeKeyIds.forEach { add(it) } }
            if (options.ttl.isPositive()) put("ttl_sec", ceilSeconds(options.ttl))
            if (options.wait.isPositive()) put("wait_sec", ceilSeconds(options.wait))
            if (options.exclusive) put("exclusive", true)
        }
        return Lease.from(this, call("POST", "/api/v1/lease", body, LeaseWire.serializer()))!!
    }

    /**
     * Leases a key for [service] and runs [block] with it.
     *
     * - [block] returns: the lease is reported `ok` and the value is returned. A failed report is thrown.
     * - [block] throws [RateLimitedException], [QuotaExhaustedException] or [InvalidKeyException]
     *   (directly or as a cause): the outcome is reported with rotate on and [block] runs again
     *   with the next key, up to [maxAttempts]. When attempts run out, the last one is reported
     *   without rotate and its exception is thrown.
     * - [block] throws anything else: the lease is reported `error` and the exception is rethrown.
     *
     * When the pool has no further key, [NoKeyAvailableException] is thrown ([AllKeysBusyException]
     * when every usable key is at its concurrency limit). Every lease is reported, also when the
     * coroutine is cancelled while [block] runs (the report is sent in [NonCancellable]). Report
     * messages are built from the exception with the secret scrubbed out.
     *
     * @param options used for the first lease; ttl, wait and exclusive apply to every lease.
     */
    public suspend fun <T> withKey(
        service: String,
        maxAttempts: Int = 5,
        options: LeaseOptions = LeaseOptions(),
        block: suspend (Lease) -> T,
    ): T {
        val max = maxAttempts.coerceAtLeast(1)
        val tried = options.excludeKeyIds.toMutableList()
        var lease = this.lease(service, options)
        var last: Throwable? = null
        for (attempt in 1..max) {
            val result = try {
                block(lease)
            } catch (t: Throwable) {
                val rot = rotationCause(t)
                if (rot == null) {
                    reportQuietly(lease, Report(Outcome.ERROR, message = scrubMessage(t.toString(), lease.secret)))
                    throw t
                }
                last = t
                tried += lease.keyId
                val report = rot.toReport(lease.secret)
                val active = currentCoroutineContext().isActive
                if (attempt == max || !active) {
                    reportQuietly(lease, report)
                    if (!active) currentCoroutineContext().ensureActive()
                    break
                }
                lease = lease.report(report.copy(rotate = true))
                    ?: this.lease(service, options.copy(excludeKeyIds = tried.toList()))
                continue
            }
            withContext(NonCancellable) { lease.report(Report(Outcome.OK)) }
            return result
        }
        throw last!!
    }

    /** Returns a helper whose requests go to `/proxy/{service}/...`. */
    public fun proxy(service: String): Proxy = Proxy(this, service)

    private suspend fun reportQuietly(lease: Lease, report: Report) {
        withContext(NonCancellable) {
            try {
                lease.report(report)
            } catch (_: Exception) {
                // keep the caller's error
            }
        }
    }

    internal fun Request.Builder.auth(): Request.Builder {
        header("Authorization", "Bearer $token")
        if (userAgent.isNotEmpty()) header("User-Agent", userAgent)
        return this
    }

    internal suspend fun <T> call(method: String, path: String, body: JsonObject?, de: DeserializationStrategy<T>): T {
        val rb = body?.toString()?.toRequestBody(JSON_MEDIA)
        val req = Request.Builder().url(baseUrl + path).auth()
            .header("Accept", "application/json")
            .method(method, rb)
            .build()
        val res = try {
            http.newCall(req).await()
        } catch (e: IOException) {
            throw AcnologiaException("acnologia: $method $path: $e", e)
        }
        val text = withContext(Dispatchers.IO) { res.use { it.body?.string() ?: "" } }
        if (!res.isSuccessful) throw ApiException.fromResponse(res.code, text)
        return try {
            json.decodeFromString(de, text.ifEmpty { "{}" })
        } catch (e: Exception) {
            throw AcnologiaException("acnologia: decode response: ${e.message}", e)
        }
    }
}

internal val JSON_MEDIA = "application/json".toMediaType()

/**
 * Runs this OkHttp call without blocking a thread and cancels it when the coroutine is
 * cancelled. Close the returned response.
 */
public suspend fun Call.await(): Response = suspendCancellableCoroutine { cont ->
    cont.invokeOnCancellation { runCatching { cancel() } }
    enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) {
            if (!cont.isCancelled) cont.resumeWithException(e)
        }

        override fun onResponse(call: Call, response: Response) {
            if (cont.isActive) cont.resume(response) else response.close()
        }
    })
}

internal fun rotationCause(t: Throwable): KeyRotationException? {
    var cur: Throwable? = t
    var depth = 0
    while (cur != null && depth < 32) {
        if (cur is KeyRotationException) return cur
        cur = cur.cause
        depth++
    }
    return null
}

internal fun ceilSeconds(d: Duration): Long {
    val whole = d.inWholeSeconds
    return if (d > whole.seconds) whole + 1 else whole
}

internal fun queryEscape(s: String): String = URLEncoder.encode(s, Charsets.UTF_8)

/** Percent encodes one path segment (RFC 3986 unreserved characters stay). */
internal fun pathEscape(s: String): String = buildString {
    for (b in s.toByteArray(Charsets.UTF_8)) {
        val c = (b.toInt() and 0xff).toChar()
        if (c in 'a'..'z' || c in 'A'..'Z' || c in '0'..'9' || c == '-' || c == '.' || c == '_' || c == '~') {
            append(c)
        } else {
            append('%').append(String.format("%02X", b.toInt() and 0xff))
        }
    }
}

internal fun encodeQuery(query: Map<String, Any?>): String = buildList {
    for ((k, v) in query) {
        when (v) {
            null -> {}
            is Iterable<*> -> v.forEach { if (it != null) add(queryEscape(k) + "=" + queryEscape(it.toString())) }
            else -> add(queryEscape(k) + "=" + queryEscape(v.toString()))
        }
    }
}.joinToString("&")

private const val MAX_REPORT_MESSAGE = 500

/**
 * Removes [secret] (raw and URL escaped) from [message] and truncates it to 500 characters.
 * Errors from query auth services often carry the full request URL, key included, and a report
 * must not copy it to the server.
 */
public fun scrubMessage(message: String?, secret: String): String {
    var out = message ?: ""
    if (secret.isNotEmpty()) {
        out = out.replace(secret, "[key]").replace(queryEscape(secret), "[key]").replace(pathEscape(secret), "[key]")
    }
    if (out.length > MAX_REPORT_MESSAGE) {
        var end = MAX_REPORT_MESSAGE
        if (out[end - 1].isHighSurrogate()) end--
        out = out.substring(0, end)
    }
    return out
}
