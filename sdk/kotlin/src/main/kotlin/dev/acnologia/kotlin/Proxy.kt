package dev.acnologia.kotlin

import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.encodeToJsonElement
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response

/**
 * Sends requests through Acnologia's proxy for one service. Acnologia injects a key, retries on
 * rate limits with the next key, and never returns the secret. Upstream responses come back
 * unchanged: a non-2xx status is not an exception. Read Acnologia's answer headers with
 * [Response.acnologia]. Close every [Response] you get back.
 *
 * Redirects are never followed, so the app token is not replayed to another host.
 */
public class Proxy internal constructor(private val client: Acnologia, public val service: String) {
    private val prefixPath: String get() = "/proxy/" + pathEscape(service)

    /** The full proxy URL for a provider [path] and [query]. */
    public fun url(path: String, query: Map<String, Any?> = emptyMap()): String {
        val u = client.baseUrl + prefixPath + "/" + path.trimStart('/')
        val q = encodeQuery(query)
        return if (q.isEmpty()) u else "$u?$q"
    }

    /**
     * Sends any request. [body] null means no body (an empty one for POST, PUT and PATCH).
     * [options] sets `Idempotency-Key` and `Cache-Control: no-cache`.
     */
    public suspend fun request(
        method: String,
        path: String,
        query: Map<String, Any?> = emptyMap(),
        headers: Map<String, String> = emptyMap(),
        body: RequestBody? = null,
        options: ProxyOptions = ProxyOptions(),
    ): Response {
        val m = method.uppercase()
        val b = body ?: if (m == "POST" || m == "PUT" || m == "PATCH") ByteArray(0).toRequestBody() else null
        val req = Request.Builder().url(url(path, query).toHttpUrl()).method(m, b)
        headers.forEach { (k, v) -> req.header(k, v) }
        return client.executeProxy(req, options)
    }

    /** Sends a GET. */
    public suspend fun get(
        path: String,
        query: Map<String, Any?> = emptyMap(),
        headers: Map<String, String> = emptyMap(),
        options: ProxyOptions = ProxyOptions(),
    ): Response = request("GET", path, query, headers, options = options)

    /** Sends a DELETE. */
    public suspend fun delete(
        path: String,
        query: Map<String, Any?> = emptyMap(),
        headers: Map<String, String> = emptyMap(),
        options: ProxyOptions = ProxyOptions(),
    ): Response = request("DELETE", path, query, headers, options = options)

    /** Sends a POST with [body]. */
    public suspend fun post(
        path: String,
        body: RequestBody,
        headers: Map<String, String> = emptyMap(),
        options: ProxyOptions = ProxyOptions(),
    ): Response = request("POST", path, emptyMap(), headers, body, options)

    /** POSTs [json] with `Content-Type: application/json`. */
    public suspend fun postJson(
        path: String,
        json: JsonElement,
        headers: Map<String, String> = emptyMap(),
        options: ProxyOptions = ProxyOptions(),
    ): Response = post(path, json.toString().toRequestBody(JSON_MEDIA), headers, options)

    /** Encodes [value] with kotlinx.serialization and POSTs it as JSON. */
    public suspend inline fun <reified T> postJson(path: String, value: T): Response =
        postJson(path, kotlinx.serialization.json.Json.encodeToJsonElement(value))

    /**
     * Sends a request you built yourself through the proxy. Only its path and query are used:
     * they are resolved under `/proxy/{service}/`, so a request built against the provider's own
     * URL works as is. A path that already starts with `/proxy/{service}/` is kept. Method,
     * headers and body are copied; the app token replaces any `Authorization` header.
     */
    public suspend fun send(request: Request, options: ProxyOptions = ProxyOptions()): Response {
        var path = request.url.encodedPath
        val svc = prefixPath
        val basePath = client.baseUrl.toHttpUrl().encodedPath.trimEnd('/')
        for (p in listOf(basePath + svc, svc)) {
            if (path == p || path.startsWith("$p/")) {
                path = path.substring(p.length)
                break
            }
        }
        var target = client.baseUrl + svc + "/" + path.trimStart('/')
        request.url.encodedQuery?.let { target += "?$it" }
        return client.executeProxy(request.newBuilder().url(target.toHttpUrl()), options)
    }

    override fun toString(): String = "Proxy(service=$service)"
}


/**
 * What Acnologia says about a proxied answer, read from its `X-Acnologia-*` headers. Get it with
 * [Response.acnologia].
 *
 * @property keyId the pool key that served the call (`X-Acnologia-Key-Id`), null for a share's token.
 * @property attempts how many keys were tried (`X-Acnologia-Attempts`).
 * @property service the service that answered (`X-Acnologia-Service`).
 * @property cache `hit`, `miss` or another cache state (`X-Acnologia-Cache`).
 * @property replayed true when an earlier answer was replayed for the same `Idempotency-Key`.
 * @property capability the capability that ran (`X-Acnologia-Capability`).
 * @property tried the routes a capability tried, in order (`X-Acnologia-Tried`).
 */
public data class ProxyInfo(
    val keyId: Long?,
    val attempts: Int?,
    val service: String?,
    val cache: String?,
    val replayed: Boolean,
    val capability: String?,
    val tried: List<TriedRoute>,
)

/** One route a capability tried: the [service] and the HTTP [status] it gave (0 when it failed first). */
public data class TriedRoute(val service: String, val status: Int)

/** Acnologia's answer headers on a proxy response. */
public val Response.acnologia: ProxyInfo
    get() = ProxyInfo(
        keyId = header("X-Acnologia-Key-Id")?.trim()?.toLongOrNull(),
        attempts = header("X-Acnologia-Attempts")?.trim()?.toIntOrNull(),
        service = header("X-Acnologia-Service")?.ifEmpty { null },
        cache = header("X-Acnologia-Cache")?.ifEmpty { null },
        replayed = header("Idempotent-Replayed").equals("true", ignoreCase = true),
        capability = header("X-Acnologia-Capability")?.ifEmpty { null },
        tried = parseTried(header("X-Acnologia-Tried")),
    )

internal fun parseTried(v: String?): List<TriedRoute> {
    if (v.isNullOrBlank()) return emptyList()
    return v.split(',').mapNotNull { part ->
        val p = part.trim()
        if (p.isEmpty()) return@mapNotNull null
        val i = p.lastIndexOf('=')
        if (i < 0) TriedRoute(p, 0) else TriedRoute(p.substring(0, i), p.substring(i + 1).trim().toIntOrNull() ?: 0)
    }
}
