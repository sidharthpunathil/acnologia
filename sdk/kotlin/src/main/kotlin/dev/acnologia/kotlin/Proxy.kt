package dev.acnologia.kotlin

import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.encodeToJsonElement
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException

/**
 * Sends requests through Acnologia's proxy for one service. Acnologia injects a key, retries on
 * rate limits with the next key, and never returns the secret. Upstream responses come back
 * unchanged: a non-2xx status is not an exception. Responses carry `X-Acnologia-Key-Id` and
 * `X-Acnologia-Attempts` headers. Close every [Response] you get back.
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

    /** Sends any request. [body] null means no body (an empty one for POST, PUT and PATCH). */
    public suspend fun request(
        method: String,
        path: String,
        query: Map<String, Any?> = emptyMap(),
        headers: Map<String, String> = emptyMap(),
        body: RequestBody? = null,
    ): Response {
        val m = method.uppercase()
        val b = body ?: if (m == "POST" || m == "PUT" || m == "PATCH") ByteArray(0).toRequestBody() else null
        val req = Request.Builder().url(url(path, query).toHttpUrl()).method(m, b)
        headers.forEach { (k, v) -> req.header(k, v) }
        return execute(req)
    }

    /** Sends a GET. */
    public suspend fun get(path: String, query: Map<String, Any?> = emptyMap(), headers: Map<String, String> = emptyMap()): Response =
        request("GET", path, query, headers)

    /** Sends a DELETE. */
    public suspend fun delete(path: String, query: Map<String, Any?> = emptyMap(), headers: Map<String, String> = emptyMap()): Response =
        request("DELETE", path, query, headers)

    /** Sends a POST with [body]. */
    public suspend fun post(path: String, body: RequestBody, headers: Map<String, String> = emptyMap()): Response =
        request("POST", path, emptyMap(), headers, body)

    /** POSTs [json] with `Content-Type: application/json`. */
    public suspend fun postJson(path: String, json: JsonElement, headers: Map<String, String> = emptyMap()): Response =
        post(path, json.toString().toRequestBody(JSON_MEDIA), headers)

    /** Encodes [value] with kotlinx.serialization and POSTs it as JSON. */
    public suspend inline fun <reified T> postJson(path: String, value: T): Response =
        postJson(path, kotlinx.serialization.json.Json.encodeToJsonElement(value))

    /**
     * Sends a request you built yourself through the proxy. Only its path and query are used:
     * they are resolved under `/proxy/{service}/`, so a request built against the provider's own
     * URL works as is. A path that already starts with `/proxy/{service}/` is kept. Method,
     * headers and body are copied; the app token replaces any `Authorization` header.
     */
    public suspend fun send(request: Request): Response {
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
        return execute(request.newBuilder().url(target.toHttpUrl()))
    }

    private suspend fun execute(builder: Request.Builder): Response {
        val req = with(client) { builder.auth() }.build()
        return try {
            client.proxyHttp.newCall(req).await()
        } catch (e: IOException) {
            throw AcnologiaException("acnologia: proxy ${req.method} ${req.url.encodedPath}: $e", e)
        }
    }

    override fun toString(): String = "Proxy(service=$service)"
}

