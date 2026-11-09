package dev.acnologia.kotlin

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import kotlinx.serialization.json.put
import okhttp3.Request
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import java.io.IOException
import java.time.Instant
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertSame
import kotlin.test.assertTrue
import kotlin.time.Duration
import kotlin.time.Duration.Companion.hours
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.seconds

class AcnologiaTest {
    private lateinit var server: MockWebServer
    private lateinit var c: Acnologia
    private val keys = ArrayDeque<Long>()
    private val leases = CopyOnWriteArrayList<JsonObject>()
    private val reports = CopyOnWriteArrayList<JsonObject>()
    private val userAgents = CopyOnWriteArrayList<String>()
    private val requests = CopyOnWriteArrayList<RecordedRequest>()
    @Volatile private var busy = false
    @Volatile private var failStatus = 0
    @Volatile private var failBody = ""

    private fun leaseJson(id: Long) = buildJsonObject {
        put("lease_id", "l$id"); put("key_id", id); put("service", "serpapi"); put("secret", "sk$id")
        put("auth", buildJsonObject { put("type", "query"); put("name", "api_key") })
        put("base_url", "https://serpapi.com"); put("expires_at", "2026-11-01T12:00:00Z")
    }

    private fun json(status: Int, body: String) =
        MockResponse().setResponseCode(status).setHeader("Content-Type", "application/json").setBody(body)

    private fun nextKey(): Long? = synchronized(keys) { keys.removeFirstOrNull() }

    private val dispatcher = object : Dispatcher() {
        override fun dispatch(request: RecordedRequest): MockResponse {
            val path = request.requestUrl!!.encodedPath
            userAgents += request.getHeader("User-Agent") ?: ""
            requests += request
            if (path == "/api/v1/proxy/ca.pem") {
                return MockResponse().setHeader("Content-Type", "application/x-pem-file").setBody(TEST_CA)
            }
            if (request.getHeader("Authorization") != "Bearer acn_test") {
                return json(401, """{"error":{"code":"unauthenticated","message":"bad token"}}""")
            }
            val body = request.body.readUtf8()
            if (path.startsWith("/proxy/")) {
                if (path.endsWith("/redirect")) {
                    return MockResponse().setResponseCode(302).setHeader("Location", "http://example.invalid/steal")
                }
                val out = buildJsonObject {
                    put("method", request.method); put("path", path); put("query", request.requestUrl!!.encodedQuery)
                    put("body", body); put("ct", request.getHeader("Content-Type")); put("custom", request.getHeader("X-Custom"))
                    put("idem", request.getHeader("Idempotency-Key")); put("cc", request.getHeader("Cache-Control"))
                }
                if (path == "/proxy/maps-search") {
                    return json(200, out.toString())
                        .setHeader("X-Acnologia-Capability", "maps-search").setHeader("X-Acnologia-Service", "omkar")
                        .setHeader("X-Acnologia-Tried", "serpapi=429, omkar=200").setHeader("X-Acnologia-Attempts", "2")
                }
                if (path.endsWith("/cached")) {
                    return json(200, out.toString()).setHeader("X-Acnologia-Cache", "hit").setHeader("Idempotent-Replayed", "true")
                        .setHeader("X-Acnologia-Service", "serpapi").setHeader("X-Acnologia-Key-Id", "7").setHeader("X-Acnologia-Attempts", "1")
                }
                return json(if (path.endsWith("/teapot")) 418 else 200, out.toString())
                    .setHeader("X-Acnologia-Key-Id", "7").setHeader("X-Acnologia-Attempts", "1")
            }
            if (failStatus != 0) return json(failStatus, failBody)
            return when {
                request.method == "GET" && path == "/api/v1/services" -> json(
                    200,
                    """{"services":[{"slug":"serpapi","name":"SerpAPI","provider":"serpapi","category":"search",""" +
                        """"available":true,"base_url":"https://serpapi.com","auth":{"type":"query","name":"api_key"},""" +
                        """"guide_url":"http://x/api/v1/services/serpapi/guide"}]}""",
                )
                request.method == "GET" && path == "/api/v1/me" -> json(200, ME_JSON)
                request.method == "GET" && path == "/api/v1/services/serpapi/guide" ->
                    if (request.getHeader("Accept") == "text/markdown") {
                        MockResponse().setHeader("Content-Type", "text/markdown").setBody("# SerpAPI\n\nSearch.")
                    } else {
                        json(200, """{"markdown":"# SerpAPI","generated":true,"guide":{"endpoints":[{"path":"/search.json"}]}}""")
                    }
                request.method == "GET" && path == "/api/v1/capabilities" -> json(
                    200,
                    """{"capabilities":[{"slug":"maps-search","name":"Search places","description":"Find places",""" +
                        """"strategy":"priority","params":[{"name":"query","type":"string"}],""" +
                        """"routes":[{"service":"omkar","mapped":true},{"service":"serpapi","mapped":false}],""" +
                        """"url":"http://x/proxy/maps-search"}]}""",
                )
                request.method == "GET" && path == "/api/v1/proxy" -> json(200, PROXY_JSON)
                request.method == "POST" && path == "/api/v1/lease" -> {
                    leases += Json.parseToJsonElement(body).jsonObject
                    if (busy) return json(503, """{"error":{"code":"all_keys_busy","message":"every key is at its limit"},"retry_after_sec":1}""")
                    val id = nextKey()
                        ?: return json(503, """{"error":{"code":"no_key_available","message":"all keys cooling"},"retry_after_sec":42}""")
                    json(200, leaseJson(id).toString())
                }
                path.matches(Regex("/api/v1/leases/[^/]+/report")) -> {
                    val b = Json.parseToJsonElement(body).jsonObject
                    reports += JsonObject(b + ("_lease_id" to kotlinx.serialization.json.JsonPrimitive(path.split("/")[4])))
                    val next = if (b["rotate"]?.jsonPrimitive?.boolean == true) nextKey() else null
                    json(200, buildJsonObject {
                        put("ok", true)
                        put("next", next?.let { leaseJson(it) } ?: kotlinx.serialization.json.JsonNull)
                    }.toString())
                }
                path.matches(Regex("/api/v1/leases/[^/]+/renew")) -> {
                    if (path.contains("gone")) return json(404, """{"error":{"code":"lease_not_found","message":"lease not found"}}""")
                    val ttl = Json.parseToJsonElement(body).jsonObject["ttl_sec"]?.jsonPrimitive?.int ?: 0
                    json(200, """{"ok":true,"expires_at":"2026-11-01T12:0${ttl % 10}:00.123456789Z"}""")
                }
                else -> json(404, """{"error":{"code":"not_found","message":"no route"}}""")
            }
        }
    }

    @BeforeTest
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = dispatcher
        server.start()
        c = Acnologia(server.url("/").toString(), "acn_test")
    }

    @AfterTest
    fun tearDown() {
        server.shutdown()
    }

    private fun keys(vararg ids: Long) = synchronized(keys) { keys.addAll(ids.toList()) }

    private fun fake(type: String, name: String, prefix: String = "") =
        Lease(c, "l", 1, "svc", "s3cr3t", Auth(type, name, prefix), "https://api.example.com/", null, false)

    @Test
    fun services() = runBlocking {
        val s = c.services().single()
        assertEquals("serpapi", s.slug)
        assertTrue(s.available)
        assertEquals("https://serpapi.com", s.baseUrl)
        assertEquals(Auth("query", "api_key"), s.auth)
        assertEquals("http://x/api/v1/services/serpapi/guide", s.guideUrl)
        assertEquals("acnologia-kotlin/${Acnologia.VERSION}", userAgents.first())
    }

    @Test
    fun badToken() = runBlocking {
        val e = assertFailsWith<UnauthenticatedException> { Acnologia(server.url("/").toString(), "acn_wrong").services() }
        assertEquals(401, e.statusCode)
        assertEquals("unauthenticated", e.code)
        assertEquals("bad token", e.errorMessage)
    }

    @Test
    fun forbiddenAndGenericErrors() = runBlocking {
        failStatus = 403
        failBody = """{"error":{"code":"lease_not_allowed","message":"proxy only"}}"""
        assertEquals("lease_not_allowed", assertFailsWith<ForbiddenException> { c.lease("x") }.code)

        failStatus = 409
        failBody = """{"error":{"code":"report_failed","message":"lease already reported"}}"""
        val e = assertFailsWith<ApiException> { c.lease("x") }
        assertEquals(ApiException::class, e::class)
        assertEquals("acnologia: http 409: report_failed: lease already reported", e.message)

        failStatus = 502
        failBody = "bad gateway"
        assertEquals("bad gateway", assertFailsWith<ApiException> { c.services() }.errorMessage)

        failStatus = 503
        failBody = """{"error":{"code":"service_unavailable","message":"paused"},"retry_after_sec":30}"""
        assertEquals(30.seconds, assertFailsWith<ServiceUnavailableException> { c.services() }.retryAfter)
    }

    @Test
    fun leaseAndReport() = runBlocking {
        keys(1, 2)
        val l = c.lease("serpapi", LeaseOptions(excludeKeyIds = listOf(9), ttl = 1500.milliseconds, exclusive = true))
        assertEquals("l1", l.leaseId)
        assertEquals(1, l.keyId)
        assertEquals("sk1", l.secret)
        assertEquals(Instant.parse("2026-11-01T12:00:00Z"), l.expiresAt)
        val body = leases[0]
        assertEquals(9, body["exclude_key_ids"]!!.jsonArray[0].jsonPrimitive.long)
        assertEquals(2, body["ttl_sec"]!!.jsonPrimitive.int)
        assertTrue(body["exclusive"]!!.jsonPrimitive.boolean)
        assertNull(body["wait_sec"])

        val next = l.report(Outcome.RATE_LIMITED, statusCode = 429, retryAfter = 2500.milliseconds, message = "slow down", rotate = true)
        assertNotNull(next)
        assertEquals(2, next.keyId)
        val r = reports[0]
        assertEquals("l1", r["_lease_id"]!!.jsonPrimitive.content)
        assertEquals("rate_limited", r["outcome"]!!.jsonPrimitive.content)
        assertEquals(429, r["status_code"]!!.jsonPrimitive.int)
        assertEquals(3, r["retry_after_sec"]!!.jsonPrimitive.int)
        assertEquals("slow down", r["message"]!!.jsonPrimitive.content)

        assertNull(next.report(Report()))
        assertEquals("ok", reports[1]["outcome"]!!.jsonPrimitive.content)
        assertNull(reports[1]["rotate"])
        assertNull(reports[1]["status_code"])
    }

    @Test
    fun renew() = runBlocking {
        keys(1)
        val l = c.lease("serpapi")
        assertEquals(Instant.parse("2026-11-01T12:03:00.123456789Z"), l.renew(3.seconds))
        val gone = Lease(c, "gone", 1, "s", "x", Auth(), "", null, false)
        assertEquals("lease_not_found", assertFailsWith<ApiException> { gone.renew() }.code)
    }

    @Test
    fun noKeyAndBusy() = runBlocking {
        val e = assertFailsWith<NoKeyAvailableException> { c.lease("serpapi") }
        assertEquals(NoKeyAvailableException::class, e::class)
        assertEquals(42.seconds, e.retryAfter)
        assertFalse(e.isBusy)

        busy = true
        val b = assertFailsWith<AllKeysBusyException> { c.lease("serpapi", LeaseOptions(wait = 10500.milliseconds)) }
        assertTrue(b.isBusy)
        assertEquals(1.seconds, b.retryAfter)
        assertEquals(11, leases[1]["wait_sec"]!!.jsonPrimitive.int)
    }

    @Test
    fun waitIsValidated() = runBlocking {
        assertFailsWith<IllegalArgumentException> { c.lease("s", LeaseOptions(wait = 61.seconds)) }
        assertFailsWith<IllegalArgumentException> { c.lease("s", LeaseOptions(wait = (-1).seconds)) }
        assertTrue(leases.isEmpty())
    }

    @Test
    fun applyPlacesTheKey() {
        val base = Request.Builder().url("https://api.example.com/v1/x?q=a&api_key=old").build()
        assertEquals("https://api.example.com/v1/x?q=a&api_key=s3cr3t", fake("query", "api_key").apply(base).url.toString())
        assertEquals("s3cr3t", fake("header", "xi-api-key").apply(base).header("xi-api-key"))
        assertEquals("Bearer s3cr3t", fake("bearer", "").apply(base).header("Authorization"))
        assertEquals("Token s3cr3t", fake("bearer", "", "Token ").apply(base).header("Authorization"))
        assertEquals("Key s3cr3t", fake("header", "X-Key", "Key ").apply(base).header("X-Key"))

        val headers = mutableMapOf<String, String>()
        val query = mutableMapOf<String, String>()
        fake("query", "key").apply(headers, query)
        assertEquals(mapOf("key" to "s3cr3t"), query)
        assertTrue(headers.isEmpty())

        assertEquals("https://api.example.com/search.json?q=coffee", fake("query", "api_key").url("search.json", mapOf("q" to "coffee")))
        assertEquals("https://api.example.com/search.json?q=coffee&api_key=s3cr3t",
            fake("query", "api_key").request("/search.json", mapOf("q" to "coffee")).url.toString())
    }

    @Test
    fun withKeySuccess() = runBlocking {
        keys(1)
        assertEquals("used sk1", c.withKey("serpapi") { "used ${it.secret}" })
        assertEquals(1, reports.size)
        assertEquals("ok", reports[0]["outcome"]!!.jsonPrimitive.content)
    }

    @Test
    fun withKeyRotates() = runBlocking {
        keys(1, 2, 3)
        val calls = AtomicInteger()
        val out = c.withKey("serpapi") { l ->
            when (calls.incrementAndGet()) {
                1 -> throw RateLimitedException(1.hours, 429, "too many")
                2 -> throw IllegalStateException("wrapped", QuotaExhaustedException(402))
                else -> l.secret
            }
        }
        assertEquals("sk3", out)
        assertEquals(listOf("rate_limited", "quota_exhausted", "ok"), reports.map { it["outcome"]!!.jsonPrimitive.content })
        assertEquals(3600, reports[0]["retry_after_sec"]!!.jsonPrimitive.int)
        assertEquals("too many", reports[0]["message"]!!.jsonPrimitive.content)
        assertTrue(reports[0]["rotate"]!!.jsonPrimitive.boolean)
        assertEquals(1, leases.size)
    }

    @Test
    fun withKeyFallsBackToLease() = runBlocking {
        keys(1)
        val e = assertFailsWith<NoKeyAvailableException> {
            c.withKey("serpapi", options = LeaseOptions(excludeKeyIds = listOf(7), ttl = 30.seconds, wait = 5.seconds)) {
                throw InvalidKeyException(401, "bad")
            }
        }
        assertEquals(42.seconds, e.retryAfter)
        assertEquals(2, leases.size)
        assertEquals("[7,1]", leases[1]["exclude_key_ids"].toString())
        assertEquals(30, leases[1]["ttl_sec"]!!.jsonPrimitive.int)
        assertEquals(5, leases[1]["wait_sec"]!!.jsonPrimitive.int)
    }

    @Test
    fun withKeyMaxAttempts() = runBlocking {
        keys(1, 2, 3, 4)
        val calls = AtomicInteger()
        val e = assertFailsWith<RateLimitedException> {
            c.withKey<String>("serpapi", maxAttempts = 2) {
                calls.incrementAndGet()
                throw RateLimitedException(5.seconds)
            }
        }
        assertEquals(5.seconds, e.retryAfter)
        assertEquals(2, calls.get())
        assertEquals(2, reports.size)
        assertTrue(reports[0]["rotate"]!!.jsonPrimitive.boolean)
        assertNull(reports[1]["rotate"])
    }

    @Test
    fun withKeyOtherErrorAndScrub() = runBlocking {
        keys(1)
        val boom = IOException("GET https://serpapi.com/search?api_key=sk1 failed")
        val e = assertFailsWith<IOException> { c.withKey<Unit>("serpapi") { throw boom } }
        assertSame(boom, e)
        assertEquals("error", reports[0]["outcome"]!!.jsonPrimitive.content)
        assertNull(reports[0]["rotate"])
        val msg = reports[0]["message"]!!.jsonPrimitive.content
        assertFalse(msg.contains("sk1"), msg)
        assertTrue(msg.contains("api_key=[key]"), msg)
    }

    @Test
    fun withKeyReportsOnCancel() = runBlocking {
        keys(1)
        val started = CompletableDeferred<Unit>()
        val job = async { c.withKey<Unit>("serpapi") { started.complete(Unit); awaitCancellation() } }
        started.await()
        job.cancelAndJoin()
        assertEquals(1, reports.size)
        assertEquals("error", reports[0]["outcome"]!!.jsonPrimitive.content)
    }

    @Test
    fun scrub() {
        assertEquals("a [key] b [key]", scrubMessage("a k+y/= b k%2By%2F%3D", "k+y/="))
        assertEquals(500, scrubMessage("x".repeat(600), "s").length)
    }

    @Serializable
    data class Tts(val text: String)

    @Test
    fun proxy() = runBlocking {
        c.proxy("serpapi").get("/search.json", mapOf("q" to "coffee beans")).use { res ->
            assertEquals(200, res.code)
            assertEquals("7", res.header("X-Acnologia-Key-Id"))
            val b = Json.parseToJsonElement(res.body!!.string()).jsonObject
            assertEquals("GET", b["method"]!!.jsonPrimitive.content)
            assertEquals("/proxy/serpapi/search.json", b["path"]!!.jsonPrimitive.content)
            assertEquals("q=coffee+beans", b["query"]!!.jsonPrimitive.content)
        }
        c.proxy("elevenlabs").postJson("v1/tts", Tts("hi")).use { res ->
            val b = Json.parseToJsonElement(res.body!!.string()).jsonObject
            assertEquals("POST", b["method"]!!.jsonPrimitive.content)
            assertTrue(b["ct"]!!.jsonPrimitive.content.startsWith("application/json"))
            assertEquals("""{"text":"hi"}""", b["body"]!!.jsonPrimitive.content)
        }
        c.proxy("x").request("DELETE", "/teapot", headers = mapOf("X-Custom" to "1")).use { res ->
            assertEquals(418, res.code)
            assertEquals("1", Json.parseToJsonElement(res.body!!.string()).jsonObject["custom"]!!.jsonPrimitive.content)
        }
        c.proxy("x").request("POST", "/empty").use { assertEquals(200, it.code) }
        val own = Request.Builder().url("https://api.elevenlabs.io/v1/voices?page=2").header("X-Custom", "yes").build()
        c.proxy("elevenlabs").send(own).use { res ->
            val b = Json.parseToJsonElement(res.body!!.string()).jsonObject
            assertEquals("/proxy/elevenlabs/v1/voices", b["path"]!!.jsonPrimitive.content)
            assertEquals("page=2", b["query"]!!.jsonPrimitive.content)
            assertEquals("yes", b["custom"]!!.jsonPrimitive.content)
        }
        val already = Request.Builder().url(c.proxy("elevenlabs").url("/v1/models")).build()
        c.proxy("elevenlabs").send(already).use { res ->
            assertEquals("/proxy/elevenlabs/v1/models", Json.parseToJsonElement(res.body!!.string()).jsonObject["path"]!!.jsonPrimitive.content)
        }
        c.proxy("serpapi").get("/redirect").use { assertEquals(302, it.code) }
    }

    @Test
    fun toStringRedacts() {
        assertFalse(c.toString().contains("acn_test"))
        val l = fake("query", "api_key")
        assertFalse(l.toString().contains("s3cr3t"))
        assertTrue(l.toString().contains("[redacted]"))
    }

    @Test
    fun fromEnv() = runBlocking {
        val e = Acnologia.fromEnv(mapOf("ACNOLOGIA_URL" to server.url("/").toString() + "/", "ACNOLOGIA_TOKEN" to "acn_test"))
        assertFalse(e.baseUrl.endsWith("/"))
        assertEquals(1, e.services().size)
        val ex = assertFailsWith<IllegalArgumentException> { Acnologia.fromEnv(emptyMap()) }
        assertTrue(ex.message!!.contains("ACNOLOGIA_URL"))
        assertFailsWith<IllegalArgumentException> { Acnologia.fromEnv(mapOf("ACNOLOGIA_URL" to "http://x")) }
        assertIs<Acnologia>(e)
        Unit
    }

    @Test
    fun me() = runBlocking {
        val m = c.me()
        assertEquals(MeApp(3, "crawler"), m.app)
        assertEquals(1000L, m.limits.dailyRequests)
        assertNull(m.limits.monthlyRequests)
        assertEquals(5.5, m.limits.dailyBudget)
        assertEquals(12L, m.usage.todayRequests)
        assertEquals(0.25, m.usage.monthCost)
        assertEquals("2026-11-01T10:00:00Z", m.usage.lastUsedAt)
        assertEquals(LimitHit("daily_requests", "2026-11-02T00:00:00Z"), m.limitHit)
        assertEquals(MeService("serpapi", "SerpAPI", "serpapi", "search", 1, null), m.services.single())
        assertEquals("paused", m.share!!.status)
        assertEquals("Ana", m.share!!.ownerName)
        assertNull(m.share!!.endsAt)
        val r = requests.last()
        assertEquals("GET", r.method)
        assertEquals("Bearer acn_test", r.getHeader("Authorization"))
        assertNull(r.getHeader("X-Acnologia-Access"))
    }

    @Test
    fun guide() = runBlocking {
        val g = c.guide("serpapi")
        assertEquals("# SerpAPI", g.markdown)
        assertTrue(g.generated)
        assertEquals("/search.json", g.guide.jsonObject["endpoints"]!!.jsonArray[0].jsonObject["path"]!!.jsonPrimitive.content)
        assertEquals("application/json", requests.last().getHeader("Accept"))
        assertEquals("# SerpAPI\n\nSearch.", c.guideMarkdown("serpapi"))
        assertEquals("/api/v1/services/serpapi/guide", requests.last().requestUrl!!.encodedPath)
        assertEquals("text/markdown", requests.last().getHeader("Accept"))
    }

    @Test
    fun capabilities() = runBlocking {
        val cap = c.capabilities().single()
        assertEquals("maps-search", cap.slug)
        assertEquals("priority", cap.strategy)
        assertEquals(listOf(CapabilityRoute("omkar", true), CapabilityRoute("serpapi", false)), cap.routes)
        assertEquals("query", cap.params.jsonArray[0].jsonObject["name"]!!.jsonPrimitive.content)
        assertEquals("http://x/proxy/maps-search", cap.url)
    }

    @Test
    fun capabilityCall() = runBlocking {
        val params = buildJsonObject { put("query", "bakery") }
        c.capability("maps-search", params, service = "omkar", raw = true, options = ProxyOptions(idempotencyKey = "k1", noCache = true)).use { res ->
            val b = Json.parseToJsonElement(res.body!!.string()).jsonObject
            assertEquals("POST", b["method"]!!.jsonPrimitive.content)
            assertEquals("/proxy/maps-search", b["path"]!!.jsonPrimitive.content)
            assertEquals("_service=omkar&_raw=1", b["query"]!!.jsonPrimitive.content)
            assertEquals("""{"query":"bakery"}""", b["body"]!!.jsonPrimitive.content)
            assertTrue(b["ct"]!!.jsonPrimitive.content.startsWith("application/json"))
            assertEquals("k1", b["idem"]!!.jsonPrimitive.content)
            assertEquals("no-cache", b["cc"]!!.jsonPrimitive.content)
            val info = res.acnologia
            assertEquals("maps-search", info.capability)
            assertEquals("omkar", info.service)
            assertEquals(2, info.attempts)
            assertNull(info.keyId)
            assertEquals(listOf(TriedRoute("serpapi", 429), TriedRoute("omkar", 200)), info.tried)
        }
        c.capability("maps-search").use { res ->
            val b = Json.parseToJsonElement(res.body!!.string()).jsonObject
            assertEquals("{}", b["body"]!!.jsonPrimitive.content)
            assertTrue(b["query"] is kotlinx.serialization.json.JsonNull)
        }
    }

    @Test
    fun proxyOptionsAndInfo() = runBlocking {
        c.proxy("serpapi").get("/cached", mapOf("q" to "x"), options = ProxyOptions(idempotencyKey = "abc", noCache = true)).use { res ->
            val b = Json.parseToJsonElement(res.body!!.string()).jsonObject
            assertEquals("abc", b["idem"]!!.jsonPrimitive.content)
            assertEquals("no-cache", b["cc"]!!.jsonPrimitive.content)
            val info = res.acnologia
            assertEquals(7L, info.keyId)
            assertEquals(1, info.attempts)
            assertEquals("serpapi", info.service)
            assertEquals("hit", info.cache)
            assertTrue(info.replayed)
            assertNull(info.capability)
            assertTrue(info.tried.isEmpty())
        }
        c.proxy("serpapi").get("/plain").use { res ->
            val b = Json.parseToJsonElement(res.body!!.string()).jsonObject
            assertTrue(b["idem"] is kotlinx.serialization.json.JsonNull)
            assertTrue(b["cc"] is kotlinx.serialization.json.JsonNull)
            assertFalse(res.acnologia.replayed)
            assertNull(res.acnologia.cache)
        }
    }

    @Test
    fun forwardProxy() = runBlocking {
        val fp = c.forwardProxy()
        assertEquals("http://127.0.0.1:8888", fp.proxyUrl)
        assertEquals("http://app:<app token>@127.0.0.1:8888", fp.proxyUrlWithToken)
        assertEquals("SHA256:AB", fp.caFingerprint)
        assertTrue(fp.passthrough)
        assertEquals(ForwardProxyHost("serpapi.com", "serpapi", "SerpAPI", ""), fp.hosts.single())
        assertEquals("1", fp.env["NODE_USE_ENV_PROXY"])
        assertEquals("X-Acnologia-Service", fp.serviceHeader)
        assertFalse(fp.toString().contains("acn_test"))

        assertEquals("http://app:acn_test@127.0.0.1:8888", c.forwardProxyUrl())
        val env = c.forwardProxyEnv("/etc/acn.pem")
        assertEquals("http://app:acn_test@127.0.0.1:8888", env["HTTPS_PROXY"])
        assertEquals(env["HTTPS_PROXY"], env["HTTP_PROXY"])
        assertEquals("1", env["NODE_USE_ENV_PROXY"])
        for (k in listOf("NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "SSL_CERT_FILE")) assertEquals("/etc/acn.pem", env[k])
        assertEquals(6, env.size)
        assertEquals("acnologia-ca.pem", c.forwardProxyEnv()["SSL_CERT_FILE"])
    }

    @Test
    fun forwardProxyCA() = runBlocking {
        assertEquals(TEST_CA, c.forwardProxyCA())
        val r = requests.last()
        assertEquals("/api/v1/proxy/ca.pem", r.requestUrl!!.encodedPath)
        assertNull(r.getHeader("Authorization"))
        val dir = kotlin.io.path.createTempDirectory("acn")
        val saved = c.saveForwardProxyCA(dir.resolve("ca.pem").toString())
        assertEquals(TEST_CA, saved.toFile().readText())
        dir.toFile().deleteRecursively()
        Unit
    }

    @Test
    fun forwardProxyClient() = runBlocking {
        val client = c.forwardProxyClient()
        val addr = client.proxy!!.address() as java.net.InetSocketAddress
        assertEquals("127.0.0.1", addr.hostString)
        assertEquals(8888, addr.port)
        val tm = client.x509TrustManager!!
        assertTrue(tm.acceptedIssuers.any { it.subjectX500Principal.name.contains("Acnologia Test CA") })
        assertTrue(tm.acceptedIssuers.size > 1)
        val challenge = okhttp3.Response.Builder()
            .request(Request.Builder().url("https://serpapi.com/").build())
            .protocol(okhttp3.Protocol.HTTP_1_1).code(407).message("Proxy Authentication Required").build()
        val authed = client.proxyAuthenticator.authenticate(null, challenge)!!
        assertEquals(okhttp3.Credentials.basic("app", "acn_test"), authed.header("Proxy-Authorization"))
        assertNull(client.proxyAuthenticator.authenticate(null, challenge.newBuilder().request(authed).build()))
    }

    @Test
    fun accessPassword() = runBlocking {
        val a = Acnologia.fromEnv(
            mapOf("ACNOLOGIA_URL" to server.url("/").toString(), "ACNOLOGIA_TOKEN" to "acn_test", "ACNOLOGIA_ACCESS_PASSWORD" to "open sesame"),
        )
        a.services()
        assertEquals("open sesame", requests.last().getHeader("X-Acnologia-Access"))
        a.forwardProxyCA()
        assertEquals("open sesame", requests.last().getHeader("X-Acnologia-Access"))
        a.proxy("serpapi").get("/x").close()
        assertEquals("open sesame", requests.last().getHeader("X-Acnologia-Access"))
        Acnologia(server.url("/").toString(), "acn_test", accessPassword = "pw").me()
        assertEquals("pw", requests.last().getHeader("X-Acnologia-Access"))
    }

    @Test
    fun appLimitReached() = runBlocking {
        failStatus = 429
        failBody = """{"error":{"code":"app_limit_reached","message":"this app reached its daily requests limit"},""" +
            """"limit":"daily_requests","reset_at":"2026-11-02T00:00:00Z","retry_after_sec":3600}"""
        val e = assertFailsWith<AppLimitReachedException> { c.me() }
        assertEquals(429, e.statusCode)
        assertEquals("app_limit_reached", e.code)
        assertEquals("daily_requests", e.limit)
        assertEquals(Instant.parse("2026-11-02T00:00:00Z"), e.resetAt)
        assertEquals(1.hours, e.retryAfter)

        val h = okhttp3.Headers.headersOf("Retry-After", "90")
        val e2 = ApiException.fromResponse(429, """{"error":{"code":"app_limit_reached","message":"m"},"limit":"monthly_budget"}""", h)
        assertIs<AppLimitReachedException>(e2)
        assertEquals(90.seconds, e2.retryAfter)
        assertNull(e2.resetAt)
    }

    @Test
    fun shareUnavailable() = runBlocking {
        failStatus = 403
        failBody = """{"error":{"code":"share_paused","message":"the owner paused this share"}}"""
        val e = assertFailsWith<ShareUnavailableException> { c.services() }
        assertEquals("paused", e.share)
        assertEquals("share_paused", e.code)
        assertIs<ForbiddenException>(e)

        val h = okhttp3.Headers.headersOf("X-Acnologia-Share", "revoked")
        val e2 = ApiException.fromResponse(403, """{"error":{"code":"share_revoked","message":"gone"}}""", h)
        assertIs<ShareUnavailableException>(e2)
        assertEquals("revoked", e2.share)

        failBody = """{"error":{"code":"proxy_not_allowed","message":"lease only"}}"""
        assertEquals(ForbiddenException::class, assertFailsWith<ForbiddenException> { c.services() }::class)
        Unit
    }

    @Test
    fun parseTriedEdgeCases() {
        assertTrue(parseTried(null).isEmpty())
        assertEquals(listOf(TriedRoute("a", 0), TriedRoute("b", 503)), parseTried("a=x,b=503, "))
    }

    @Test
    fun durationZeroIsOmitted() {
        assertEquals(0, ceilSeconds(Duration.ZERO))
        assertEquals(2, ceilSeconds(1001.milliseconds))
    }
}

private const val TEST_CA = """-----BEGIN CERTIFICATE-----
MIIBbjCCARSgAwIBAgIUSuy6pcWr83yT5SYc4vCNjDSG2MYwCgYIKoZIzj0EAwIw
HDEaMBgGA1UEAwwRQWNub2xvZ2lhIFRlc3QgQ0EwIBcNMjYxMTA5MTg0NTE2WhgP
MjEyNjEwMTYxODQ1MTZaMBwxGjAYBgNVBAMMEUFjbm9sb2dpYSBUZXN0IENBMFkw
EwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEQwOlB1vt9pZXY1i2SoGwnFvpVHFZWd56
CvwRILXU/OVGNVNFLG/NN+31Ho4/fcaJEIsEgsc6/Vgci2vgN/3436MyMDAwHQYD
VR0OBBYEFFpb0dq3AqJwil9DDKPa9tnr2tvLMA8GA1UdEwEB/wQFMAMBAf8wCgYI
KoZIzj0EAwIDSAAwRQIgNR2AeqxA9p/dq70mUceCDM+Qaush/5qOZUrEIaFK7hYC
IQDfj9zG79RmeHwjG1axIfEQn0hwBSNhSV226ilOMiviUA==
-----END CERTIFICATE-----
"""

private const val ME_JSON = """{"app":{"id":3,"name":"crawler"},""" +
    """"limits":{"daily_requests":1000,"monthly_requests":null,"daily_budget":5.5,"monthly_budget":null},""" +
    """"usage":{"today_requests":12,"today_cost":0.1,"month_requests":40,"month_cost":0.25,"last_used_at":"2026-11-01T10:00:00Z",""" +
    """"limit_hit":{"limit":"daily_requests","reset_at":"2026-11-02T00:00:00Z"}},""" +
    """"limit_hit":{"limit":"daily_requests","reset_at":"2026-11-02T00:00:00Z"},""" +
    """"services":[{"slug":"serpapi","name":"SerpAPI","provider":"serpapi","category":"search","available":1,"logo_url":null}],""" +
    """"share":{"id":"sh1","name":"Search","owner":{"name":"Ana","id":"o1"},"owner_name":"Ana","owner_id":"o1",""" +
    """"recipient":"bo@example.com","label":"","status":"paused","mode":"proxy","ends_at":null}}"""

private const val PROXY_JSON = """{"proxy_url":"http://127.0.0.1:8888","proxy_url_with_token":"http://app:<app token>@127.0.0.1:8888",""" +
    """"ca_url":"http://127.0.0.1:8080/api/v1/proxy/ca.pem","ca_fingerprint":"SHA256:AB","passthrough":true,""" +
    """"hosts":[{"host":"serpapi.com","service":"serpapi","name":"SerpAPI","path":""}],""" +
    """"env":{"HTTPS_PROXY":"http://app:<app token>@127.0.0.1:8888","NODE_USE_ENV_PROXY":"1"},"service_header":"X-Acnologia-Service"}"""
