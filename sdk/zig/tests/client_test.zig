//! Client tests. Each test starts a tiny HTTP server on 127.0.0.1 with
//! canned routes, points a Client at it and checks what was sent and what
//! came back. Nothing leaves the machine.

const std = @import("std");
const acn = @import("acnologia");
const Io = std.Io;
const http = std.http;
const testing = std.testing;

const token = "acn_test_token_0123456789";

// ---------------------------------------------------------------- fake server

const Route = struct {
    /// Empty matches any method.
    method: []const u8 = "",
    /// Matched against the request path (the target before `?`). A trailing
    /// `*` makes it a prefix.
    path: []const u8,
    status: u16 = 200,
    body: []const u8 = "",
    headers: []const http.Header = &.{},
    delay_ms: u32 = 0,
};

const Recorded = struct {
    method: []u8,
    target: []u8,
    headers: []http.Header,
    body: []u8,

    fn header(r: *const Recorded, name: []const u8) ?[]const u8 {
        for (r.headers) |h| if (std.ascii.eqlIgnoreCase(h.name, name)) return h.value;
        return null;
    }

    fn deinit(r: *Recorded, gpa: std.mem.Allocator) void {
        gpa.free(r.method);
        gpa.free(r.target);
        for (r.headers) |h| {
            gpa.free(h.name);
            gpa.free(h.value);
        }
        gpa.free(r.headers);
        gpa.free(r.body);
    }
};

const Fake = struct {
    io: Io,
    gpa: std.mem.Allocator,
    server: Io.net.Server,
    routes: []const Route,
    group: Io.Group = .init,
    mutex: Io.Mutex = .init,
    recorded: std.ArrayList(Recorded) = .empty,
    url_buf: [64]u8 = undefined,
    url: []const u8 = "",

    fn start(self: *Fake, routes: []const Route) !void {
        const io = testing.io;
        const addr = try Io.net.IpAddress.parse("127.0.0.1", 0);
        self.* = .{
            .io = io,
            .gpa = testing.allocator,
            .server = try addr.listen(io, .{ .reuse_address = true }),
            .routes = routes,
        };
        self.url = try std.fmt.bufPrint(&self.url_buf, "http://127.0.0.1:{d}", .{self.server.socket.address.getPort()});
        try self.group.concurrent(io, acceptLoop, .{self});
    }

    fn stop(self: *Fake) void {
        self.group.cancel(self.io);
        self.server.deinit(self.io);
        for (self.recorded.items) |*r| r.deinit(self.gpa);
        self.recorded.deinit(self.gpa);
    }

    fn count(self: *Fake) usize {
        self.mutex.lockUncancelable(self.io);
        defer self.mutex.unlock(self.io);
        return self.recorded.items.len;
    }

    /// The n-th request received, from 0.
    fn at(self: *Fake, n: usize) *const Recorded {
        self.mutex.lockUncancelable(self.io);
        defer self.mutex.unlock(self.io);
        return &self.recorded.items[n];
    }

    fn last(self: *Fake) *const Recorded {
        return self.at(self.count() - 1);
    }

    fn acceptLoop(self: *Fake) Io.Cancelable!void {
        while (true) {
            const stream = self.server.accept(self.io) catch |err| switch (err) {
                error.Canceled => return error.Canceled,
                else => return,
            };
            self.group.concurrent(self.io, serve, .{ self, stream }) catch stream.close(self.io);
        }
    }

    fn serve(self: *Fake, stream: Io.net.Stream) Io.Cancelable!void {
        defer stream.close(self.io);
        var recv_buf: [16 * 1024]u8 = undefined;
        var send_buf: [4 * 1024]u8 = undefined;
        var reader = stream.reader(self.io, &recv_buf);
        var writer = stream.writer(self.io, &send_buf);
        var server = http.Server.init(&reader.interface, &writer.interface);
        while (true) {
            var req = server.receiveHead() catch return;
            const route = self.handle(&req) catch return;
            if (route.delay_ms > 0) try self.io.sleep(.fromMilliseconds(route.delay_ms), .awake);
            req.respond(route.body, .{
                .status = @fromBackingInt(@intCast(route.status)),
                .extra_headers = route.headers,
            }) catch return;
        }
    }

    fn handle(self: *Fake, req: *http.Server.Request) !Route {
        const gpa = self.gpa;
        var rec: Recorded = .{
            .method = try gpa.dupe(u8, @tagName(req.head.method)),
            .target = try gpa.dupe(u8, req.head.target),
            .headers = &.{},
            .body = &.{},
        };
        var headers: std.ArrayList(http.Header) = .empty;
        var it = req.iterateHeaders();
        while (it.next()) |h| try headers.append(gpa, .{ .name = try gpa.dupe(u8, h.name), .value = try gpa.dupe(u8, h.value) });
        rec.headers = try headers.toOwnedSlice(gpa);
        var body_buf: [1024]u8 = undefined;
        const body_reader = req.readerExpectNone(&body_buf);
        rec.body = try body_reader.allocRemaining(gpa, .limited(1 << 20));

        const path = rec.target[0 .. std.mem.findScalar(u8, rec.target, '?') orelse rec.target.len];
        var chosen: Route = .{ .path = "", .status = 404, .body = "{\"error\":{\"code\":\"not_found\",\"message\":\"no route\"}}" };
        for (self.routes) |r| {
            if (r.method.len > 0 and !std.mem.eql(u8, r.method, rec.method)) continue;
            const ok = if (std.mem.endsWith(u8, r.path, "*"))
                std.mem.startsWith(u8, path, r.path[0 .. r.path.len - 1])
            else
                std.mem.eql(u8, r.path, path);
            if (ok) {
                chosen = r;
                break;
            }
        }
        self.mutex.lockUncancelable(self.io);
        defer self.mutex.unlock(self.io);
        try self.recorded.append(gpa, rec);
        return chosen;
    }
};

fn newClient(fake: *Fake, options: acn.Options) !acn.Client {
    var o = options;
    if (o.base_url == null and o.environ == null) o.base_url = fake.url;
    if (o.token == null and o.environ == null) o.token = token;
    return acn.Client.init(testing.allocator, testing.io, o);
}

const json_type: http.Header = .{ .name = "Content-Type", .value = "application/json" };

const lease_body =
    \\{"lease_id":"lse_1","key_id":7,"service":"agi-labs","secret":"sk-agi/secret+1",
    \\ "auth":{"type":"header","name":"X-Api-Key","prefix":""},
    \\ "base_url":"https://api.agi-labs.test/","expires_at":"2026-11-04T10:00:00Z","exclusive":true}
;

// ---------------------------------------------------------------- config

test "config: base URL and token come from the environment" {
    var fake: Fake = undefined;
    try fake.start(&.{.{ .path = "/api/v1/me", .body = "{\"app\":{\"id\":3,\"name\":\"env app\"}}" }});
    defer fake.stop();

    var env = std.process.Environ.Map.init(testing.allocator);
    defer env.deinit();
    try env.put("ACNOLOGIA_URL", fake.url);
    try env.put("ACNOLOGIA_TOKEN", "acn_from_env");

    var client = try acn.Client.init(testing.allocator, testing.io, .{ .environ = &env });
    defer client.deinit();
    var me = try client.me(testing.allocator, .{});
    defer me.deinit();
    try testing.expectEqualStrings("env app", me.value.app.name);
    try testing.expectEqualStrings("Bearer acn_from_env", fake.last().header("authorization").?);
    try testing.expect(fake.last().header("x-acnologia-access") == null);
}

test "config: explicit values win over the environment" {
    var fake: Fake = undefined;
    try fake.start(&.{.{ .path = "/api/v1/me", .body = "{}" }});
    defer fake.stop();

    var env = std.process.Environ.Map.init(testing.allocator);
    defer env.deinit();
    try env.put("ACNOLOGIA_URL", "http://127.0.0.1:1");
    try env.put("ACNOLOGIA_TOKEN", "acn_from_env");

    var client = try acn.Client.init(testing.allocator, testing.io, .{ .environ = &env, .base_url = fake.url, .token = token });
    defer client.deinit();
    var me = try client.me(testing.allocator, .{});
    me.deinit();
    try testing.expectEqualStrings("Bearer " ++ token, fake.last().header("authorization").?);
}

test "config: missing or bad values are refused" {
    var env = std.process.Environ.Map.init(testing.allocator);
    defer env.deinit();
    try testing.expectError(error.MissingBaseUrl, acn.Client.init(testing.allocator, testing.io, .{ .token = token }));
    try testing.expectError(error.MissingBaseUrl, acn.Client.init(testing.allocator, testing.io, .{ .environ = &env, .token = token }));
    try testing.expectError(error.MissingToken, acn.Client.init(testing.allocator, testing.io, .{ .base_url = "http://127.0.0.1:1" }));
    try testing.expectError(error.MissingToken, acn.Client.init(testing.allocator, testing.io, .{ .base_url = "http://127.0.0.1:1", .token = "  " }));
    try testing.expectError(error.InvalidUrl, acn.Client.init(testing.allocator, testing.io, .{ .base_url = "ftp://x", .token = token }));
    try testing.expectError(error.InvalidArgument, acn.Client.init(testing.allocator, testing.io, .{ .base_url = "http://x", .token = "acn_a\r\nX: y" }));
}

test "config: access password is sent only when set or owned by the same server" {
    var fake: Fake = undefined;
    try fake.start(&.{.{ .path = "/api/v1/me", .body = "{}" }});
    defer fake.stop();

    // Explicit.
    {
        var client = try newClient(&fake, .{ .access_password = "gate-pw" });
        defer client.deinit();
        var me = try client.me(testing.allocator, .{});
        me.deinit();
        try testing.expectEqualStrings("gate-pw", fake.last().header("x-acnologia-access").?);
    }
    // From the environment, for the server ACNOLOGIA_URL names.
    var env = std.process.Environ.Map.init(testing.allocator);
    defer env.deinit();
    try env.put("ACNOLOGIA_URL", fake.url);
    try env.put("ACNOLOGIA_TOKEN", token);
    try env.put("ACNOLOGIA_ACCESS_PASSWORD", "env-pw");
    {
        var client = try acn.Client.init(testing.allocator, testing.io, .{ .environ = &env });
        defer client.deinit();
        var me = try client.me(testing.allocator, .{});
        me.deinit();
        try testing.expectEqualStrings("env-pw", fake.last().header("x-acnologia-access").?);
    }
    // The password belongs to another server: never sent here.
    try env.put("ACNOLOGIA_ACCESS_URL", "https://other.example");
    {
        var client = try acn.Client.init(testing.allocator, testing.io, .{ .environ = &env });
        defer client.deinit();
        var me = try client.me(testing.allocator, .{});
        me.deinit();
        try testing.expect(fake.last().header("x-acnologia-access") == null);
    }
}

// ---------------------------------------------------------------- me

test "me: reads app, limits, usage, services and share" {
    const body =
        \\{"app":{"id":42,"name":"Website"},
        \\ "limits":{"daily_requests":500,"monthly_requests":null,"daily_budget":null,"monthly_budget":20},
        \\ "usage":{"today_requests":12,"today_cost":0.25,"month_requests":80,"month_cost":1,"last_used_at":"2026-11-04T09:00:00Z","limit_hit":null},
        \\ "limit_hit":{"limit":"daily_requests","reset_at":"2026-11-05T00:00:00Z"},
        \\ "services":[{"slug":"agi-labs","name":"AGI Labs","provider":"agi-labs","category":"ai","available":1,"logo_url":null,"extra":true}],
        \\ "share":{"id":"shr_1","name":"Team","owner":{"name":"Owner","id":"own_1"},"owner_name":"Owner","owner_id":"own_1",
        \\          "recipient":"server","label":"","status":"paused","mode":"proxy","ends_at":null}}
    ;
    var fake: Fake = undefined;
    try fake.start(&.{.{ .method = "GET", .path = "/api/v1/me", .body = body, .headers = &.{json_type} }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var me = try client.me(testing.allocator, .{});
    defer me.deinit();
    const v = me.value;
    try testing.expectEqual(@as(i64, 42), v.app.id);
    try testing.expectEqualStrings("Website", v.app.name);
    try testing.expectEqual(@as(?i64, 500), v.limits.daily_requests);
    try testing.expectEqual(@as(?i64, null), v.limits.monthly_requests);
    try testing.expectEqual(@as(?f64, 20), v.limits.monthly_budget);
    try testing.expectEqual(@as(i64, 12), v.usage.today_requests);
    try testing.expectEqual(@as(f64, 0.25), v.usage.today_cost);
    try testing.expectEqualStrings("daily_requests", v.limit_hit.?.limit);
    try testing.expectEqual(@as(usize, 1), v.services.len);
    try testing.expectEqualStrings("agi-labs", v.services[0].slug);
    try testing.expectEqual(@as(i64, 1), v.services[0].available);
    try testing.expectEqualStrings("paused", v.share.?.status);
    try testing.expectEqualStrings("Owner", v.share.?.owner.name);

    const rec = fake.last();
    try testing.expectEqualStrings("GET", rec.method);
    try testing.expectEqualStrings("/api/v1/me", rec.target);
    try testing.expectEqualStrings("application/json", rec.header("accept").?);
    try testing.expectEqualStrings(acn.user_agent, rec.header("x-acnologia-client").?);
    try testing.expectEqualStrings(acn.user_agent, rec.header("user-agent").?);
}

// ---------------------------------------------------------------- proxy

test "proxy: sends method, path, query, headers, body and idempotency key" {
    var fake: Fake = undefined;
    try fake.start(&.{.{
        .method = "POST",
        .path = "/proxy/agi-labs/v1/chat",
        .status = 201,
        .body = "{\"reply\":\"hi\"}",
        .headers = &.{
            json_type,
            .{ .name = "X-Acnologia-Key-Id", .value = "7" },
            .{ .name = "X-Acnologia-Attempts", .value = "2" },
            .{ .name = "X-Acnologia-Service", .value = "agi-labs" },
            .{ .name = "X-Acnologia-Cache", .value = "miss" },
            .{ .name = "Idempotent-Replayed", .value = "true" },
        },
    }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var res = try client.proxy(testing.allocator, .POST, "agi-labs", "/v1/chat", .{
        .query = &.{ .{ .name = "model", .value = "big one" }, .{ .name = "x", .value = "a&b" } },
        .headers = &.{
            .{ .name = "Content-Type", .value = "application/json" },
            .{ .name = "X-Trace", .value = "t-1" },
            .{ .name = "Authorization", .value = "Bearer should-be-dropped" },
        },
        .body = "{\"prompt\":\"hello\"}",
        .idempotency_key = "idem-123",
    });
    defer res.deinit();

    try testing.expectEqual(@as(u16, 201), res.status);
    try testing.expectEqualStrings("{\"reply\":\"hi\"}", res.body);
    try testing.expectEqualStrings("application/json", res.header("content-type").?);
    try testing.expectEqual(@as(?i64, 7), res.acnologia.key_id);
    try testing.expectEqual(@as(?u32, 2), res.acnologia.attempts);
    try testing.expectEqualStrings("agi-labs", res.acnologia.service.?);
    try testing.expectEqualStrings("miss", res.acnologia.cache.?);
    try testing.expect(res.acnologia.replayed);

    const Reply = struct { reply: []const u8 };
    var parsed = try res.json(Reply, testing.allocator);
    defer parsed.deinit();
    try testing.expectEqualStrings("hi", parsed.value.reply);

    const rec = fake.last();
    try testing.expectEqualStrings("POST", rec.method);
    try testing.expectEqualStrings("/proxy/agi-labs/v1/chat?model=big%20one&x=a%26b", rec.target);
    try testing.expectEqualStrings("{\"prompt\":\"hello\"}", rec.body);
    try testing.expectEqualStrings("idem-123", rec.header("idempotency-key").?);
    try testing.expectEqualStrings("t-1", rec.header("x-trace").?);
    try testing.expectEqualStrings("application/json", rec.header("content-type").?);
    try testing.expectEqualStrings("Bearer " ++ token, rec.header("authorization").?);
    var auth_count: usize = 0;
    for (rec.headers) |h| {
        if (std.ascii.eqlIgnoreCase(h.name, "authorization")) auth_count += 1;
    }
    try testing.expectEqual(@as(usize, 1), auth_count);
}

test "proxy: provider errors and redirects come back as responses" {
    var fake: Fake = undefined;
    try fake.start(&.{
        .{ .path = "/proxy/agi-labs/limited", .status = 429, .body = "{\"error\":{\"code\":\"no_key_available\"}}", .headers = &.{
            .{ .name = "X-Acnologia-Service", .value = "agi-labs" },
            .{ .name = "Retry-After", .value = "9" },
        } },
        .{ .path = "/proxy/agi-labs/moved", .status = 302, .headers = &.{.{ .name = "Location", .value = "http://elsewhere.test/x" }} },
        .{ .path = "/proxy/agi-labs/gone", .status = 404, .body = "{\"error\":\"not here\"}" },
    });
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    // A provider 429 with an Acnologia looking body is still the provider's
    // answer: X-Acnologia-Service says it was relayed.
    var limited = try client.proxy(testing.allocator, .GET, "agi-labs", "limited", .{});
    defer limited.deinit();
    try testing.expectEqual(@as(u16, 429), limited.status);
    try testing.expectEqualStrings("9", limited.header("retry-after").?);

    var moved = try client.proxy(testing.allocator, .GET, "agi-labs", "moved", .{});
    defer moved.deinit();
    try testing.expectEqual(@as(u16, 302), moved.status);
    try testing.expectEqual(@as(usize, 2), fake.count());

    var gone = try client.proxy(testing.allocator, .DELETE, "agi-labs", "gone", .{});
    defer gone.deinit();
    try testing.expectEqual(@as(u16, 404), gone.status);
}

test "proxy: raw returns Acnologia's own errors as responses" {
    var fake: Fake = undefined;
    try fake.start(&.{.{ .path = "/proxy/*", .status = 502, .body = "{\"error\":{\"code\":\"upstream_unreachable\",\"message\":\"could not reach AGI Labs\"}}" }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var res = try client.proxy(testing.allocator, .GET, "agi-labs", "v1/chat", .{ .raw = true });
    defer res.deinit();
    try testing.expectEqual(@as(u16, 502), res.status);
    try testing.expectError(error.UpstreamUnreachable, client.proxy(testing.allocator, .GET, "agi-labs", "v1/chat", .{}));
}

test "proxy: refuses a body on GET and a header with a line break" {
    var fake: Fake = undefined;
    try fake.start(&.{});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();
    var diag: acn.Diagnostic = .{};
    try testing.expectError(error.InvalidArgument, client.proxy(testing.allocator, .GET, "agi-labs", "x", .{ .body = "nope", .diag = &diag }));
    try testing.expect(diag.message().len > 0);
    try testing.expectError(error.InvalidArgument, client.proxy(testing.allocator, .GET, "agi-labs", "x", .{
        .headers = &.{.{ .name = "X-Bad", .value = "a\r\nInjected: 1" }},
    }));
    try testing.expectEqual(@as(usize, 0), fake.count());
}

test "proxy: no_cache sends Cache-Control and capability headers are read" {
    var fake: Fake = undefined;
    try fake.start(&.{.{
        .path = "/proxy/agi-labs/v1/chat",
        .headers = &.{
            .{ .name = "X-Acnologia-Capability", .value = "chat" },
            .{ .name = "X-Acnologia-Tried", .value = "other=503, agi-labs=200" },
        },
    }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var res = try client.proxy(testing.allocator, .GET, "agi-labs", "v1/chat", .{ .no_cache = true });
    defer res.deinit();
    try testing.expectEqualStrings("no-cache", fake.last().header("cache-control").?);
    try testing.expectEqualStrings("chat", res.acnologia.capability.?);
    try testing.expectEqual(@as(usize, 2), res.acnologia.tried.len);
    try testing.expectEqualStrings("other", res.acnologia.tried[0].service);
    try testing.expectEqual(@as(u16, 503), res.acnologia.tried[0].status);
    try testing.expectEqualStrings("agi-labs", res.acnologia.tried[1].service);
    try testing.expectEqual(@as(u16, 200), res.acnologia.tried[1].status);
    try testing.expect(!res.acnologia.replayed);
    try testing.expectEqual(@as(?i64, null), res.acnologia.key_id);

    var plain = try client.proxy(testing.allocator, .GET, "agi-labs", "v1/chat", .{});
    defer plain.deinit();
    try testing.expect(fake.last().header("cache-control") == null);
}

// ---------------------------------------------------------------- catalog

test "services: reads base URL, auth and guide URL" {
    const body =
        \\{"services":[{"slug":"serpapi","name":"SerpApi","provider":"serpapi","category":"search","available":true,
        \\  "base_url":"https://serpapi.com","auth":{"type":"query","name":"api_key"},
        \\  "guide_url":"http://x/api/v1/services/serpapi/guide","extra":1}]}
    ;
    var fake: Fake = undefined;
    try fake.start(&.{.{ .method = "GET", .path = "/api/v1/services", .body = body, .headers = &.{json_type} }});
    defer fake.stop();
    var client = try newClient(&fake, .{ .access_password = "gate-pw" });
    defer client.deinit();

    var list = try client.services(testing.allocator, .{});
    defer list.deinit();
    try testing.expectEqual(@as(usize, 1), list.value.len);
    const s = list.value[0];
    try testing.expectEqualStrings("serpapi", s.slug);
    try testing.expect(s.available);
    try testing.expectEqualStrings("https://serpapi.com", s.base_url);
    try testing.expectEqualStrings("query", s.auth.type);
    try testing.expectEqualStrings("api_key", s.auth.name);
    try testing.expectEqualStrings("", s.auth.prefix);
    try testing.expectEqualStrings("http://x/api/v1/services/serpapi/guide", s.guide_url);

    const rec = fake.last();
    try testing.expectEqualStrings("GET", rec.method);
    try testing.expectEqualStrings("/api/v1/services", rec.target);
    try testing.expectEqualStrings("Bearer " ++ token, rec.header("authorization").?);
    try testing.expectEqualStrings("gate-pw", rec.header("x-acnologia-access").?);
}

test "guide: JSON form and Markdown form" {
    const body =
        \\{"markdown":"# SerpApi\nSearch.","generated":true,"guide":{"endpoints":[{"path":"/search"}]}}
    ;
    var fake: Fake = undefined;
    try fake.start(&.{.{ .method = "GET", .path = "/api/v1/services/serp%20api/guide", .body = body, .headers = &.{json_type} }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var g = try client.guide(testing.allocator, "serp api", .{});
    defer g.deinit();
    try testing.expectEqualStrings("# SerpApi\nSearch.", g.value.markdown);
    try testing.expect(g.value.generated);
    const eps = g.value.guide.object.get("endpoints").?.array.items;
    try testing.expectEqualStrings("/search", eps[0].object.get("path").?.string);
    try testing.expectEqualStrings("/api/v1/services/serp%20api/guide", fake.last().target);
    try testing.expectEqualStrings("application/json", fake.last().header("accept").?);

    const md = try client.guideMarkdown(testing.allocator, "serp api", .{});
    defer testing.allocator.free(md);
    try testing.expectEqualStrings(body, md);
    try testing.expectEqualStrings("text/markdown", fake.last().header("accept").?);
    try testing.expectEqualStrings("Bearer " ++ token, fake.last().header("authorization").?);
}

test "guide: a service the token may not use is ServiceNotAllowed" {
    var fake: Fake = undefined;
    try fake.start(&.{.{ .path = "/api/v1/services/*", .status = 403, .body = "{\"error\":{\"code\":\"service_not_allowed\",\"message\":\"no\"}}" }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();
    try testing.expectError(error.ServiceNotAllowed, client.guide(testing.allocator, "x", .{}));
    try testing.expectError(error.ServiceNotAllowed, client.guideMarkdown(testing.allocator, "x", .{}));
}

test "capabilities: reads params, routes and url" {
    const body =
        \\{"capabilities":[{"slug":"web-search","name":"Web search","description":"Search the web","strategy":"failover",
        \\  "params":[{"name":"query","type":"string","required":true}],
        \\  "routes":[{"service":"serpapi","mapped":true},{"service":"tavily","mapped":false}],
        \\  "url":"http://x/proxy/web-search"}]}
    ;
    var fake: Fake = undefined;
    try fake.start(&.{.{ .method = "GET", .path = "/api/v1/capabilities", .body = body, .headers = &.{json_type} }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var caps = try client.capabilities(testing.allocator, .{});
    defer caps.deinit();
    try testing.expectEqual(@as(usize, 1), caps.value.len);
    const c = caps.value[0];
    try testing.expectEqualStrings("web-search", c.slug);
    try testing.expectEqualStrings("failover", c.strategy);
    try testing.expectEqualStrings("query", c.params.array.items[0].object.get("name").?.string);
    try testing.expectEqual(@as(usize, 2), c.routes.len);
    try testing.expect(c.routes[0].mapped);
    try testing.expectEqualStrings("tavily", c.routes[1].service);
    try testing.expectEqualStrings("http://x/proxy/web-search", c.url);
    try testing.expectEqualStrings("/api/v1/capabilities", fake.last().target);
    try testing.expectEqualStrings("Bearer " ++ token, fake.last().header("authorization").?);
}

test "capability: posts params to /proxy/<slug> with _service and _raw" {
    var fake: Fake = undefined;
    try fake.start(&.{.{
        .method = "POST",
        .path = "/proxy/web-search",
        .body = "{\"items\":[],\"count\":0}",
        .headers = &.{
            json_type,
            .{ .name = "X-Acnologia-Capability", .value = "web-search" },
            .{ .name = "X-Acnologia-Service", .value = "tavily" },
            .{ .name = "X-Acnologia-Tried", .value = "serpapi=429, tavily=200" },
            .{ .name = "X-Acnologia-Key-Id", .value = "12" },
            .{ .name = "X-Acnologia-Attempts", .value = "1" },
        },
    }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var res = try client.capability(testing.allocator, "web-search", "{\"query\":\"coffee\"}", .{
        .service = "tavily",
        .raw = true,
        .idempotency_key = "cap-1",
        .no_cache = true,
    });
    defer res.deinit();
    try testing.expectEqual(@as(u16, 200), res.status);
    try testing.expectEqualStrings("web-search", res.acnologia.capability.?);
    try testing.expectEqualStrings("tavily", res.acnologia.service.?);
    try testing.expectEqual(@as(?i64, 12), res.acnologia.key_id);
    try testing.expectEqual(@as(?u32, 1), res.acnologia.attempts);
    try testing.expectEqual(@as(usize, 2), res.acnologia.tried.len);
    try testing.expectEqualStrings("serpapi", res.acnologia.tried[0].service);
    try testing.expectEqual(@as(u16, 429), res.acnologia.tried[0].status);

    const rec = fake.last();
    try testing.expectEqualStrings("POST", rec.method);
    try testing.expectEqualStrings("/proxy/web-search?_service=tavily&_raw=1", rec.target);
    try testing.expectEqualStrings("{\"query\":\"coffee\"}", rec.body);
    try testing.expectEqualStrings("application/json", rec.header("content-type").?);
    try testing.expectEqualStrings("cap-1", rec.header("idempotency-key").?);
    try testing.expectEqualStrings("no-cache", rec.header("cache-control").?);
    try testing.expectEqualStrings("Bearer " ++ token, rec.header("authorization").?);

    // No params sends an empty object and no query.
    var bare = try client.capability(testing.allocator, "web-search", null, .{});
    defer bare.deinit();
    try testing.expectEqualStrings("/proxy/web-search", fake.last().target);
    try testing.expectEqualStrings("{}", fake.last().body);
}

test "capability: Acnologia's own capability errors map to errors" {
    const cases = [_]ErrorCase{
        .{ .status = 400, .code = "bad_params", .want = error.Api },
        .{ .status = 503, .code = "no_route", .want = error.Api },
        .{ .status = 503, .code = "no_route_worked", .want = error.Api },
        .{ .status = 503, .code = "empty_group", .want = error.Api },
    };
    for (cases) |c| {
        var buf: [256]u8 = undefined;
        var fake: Fake = undefined;
        try fake.start(&.{.{ .path = "/proxy/*", .status = c.status, .body = try errorBody(&buf, c.code) }});
        defer fake.stop();
        var client = try newClient(&fake, .{});
        defer client.deinit();

        var diag: acn.Diagnostic = .{};
        try testing.expectError(c.want, client.capability(testing.allocator, "web-search", "{}", .{ .diag = &diag }));
        try testing.expectEqualStrings(c.code, diag.code());
        try testing.expectEqual(c.status, diag.status);

        var res = try client.capability(testing.allocator, "web-search", "{}", .{ .raw_errors = true });
        defer res.deinit();
        try testing.expectEqual(c.status, res.status);
    }
}

// ---------------------------------------------------------------- forward proxy

const proxy_setup_body =
    \\{"proxy_url":"http://acn.example:8888","proxy_url_with_token":"http://app:%3Capp%20token%3E@acn.example:8888",
    \\ "ca_url":"http://acn.example/api/v1/proxy/ca.pem","ca_fingerprint":"AB:CD","passthrough":true,
    \\ "hosts":[{"host":"serpapi.com","service":"serpapi","name":"SerpApi"},{"host":"tiktok-scraper.omkar.cloud","service":"omkar","name":"Omkar","path":"/~tiktok"}],
    \\ "env":{"HTTPS_PROXY":"http://app:%3Capp%20token%3E@acn.example:8888","NODE_USE_ENV_PROXY":"1"},
    \\ "service_header":"X-Acnologia-Service"}
;

const odd_token = "acn_odd/tok+en";

test "forward proxy: reads the setup, and its printed form holds no token" {
    var fake: Fake = undefined;
    try fake.start(&.{.{ .method = "GET", .path = "/api/v1/proxy", .body = proxy_setup_body, .headers = &.{json_type} }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var setup = try client.forwardProxy(testing.allocator, .{});
    defer setup.deinit();
    const v = setup.value;
    try testing.expectEqualStrings("http://acn.example:8888", v.proxy_url);
    try testing.expectEqualStrings("AB:CD", v.ca_fingerprint);
    try testing.expect(v.passthrough);
    try testing.expectEqual(@as(usize, 2), v.hosts.len);
    try testing.expectEqualStrings("/~tiktok", v.hosts[1].path);
    try testing.expectEqualStrings("", v.hosts[0].path);
    try testing.expectEqualStrings("1", v.env.map.get("NODE_USE_ENV_PROXY").?);
    try testing.expectEqualStrings("X-Acnologia-Service", v.service_header);
    try testing.expectEqualStrings("/api/v1/proxy", fake.last().target);
    try testing.expectEqualStrings("Bearer " ++ token, fake.last().header("authorization").?);

    var buf: [512]u8 = undefined;
    const printed = try std.fmt.bufPrint(&buf, "{f}", .{v});
    try testing.expect(std.mem.find(u8, printed, token) == null);
    try testing.expect(std.mem.find(u8, printed, "acn.example:8888") != null);
}

test "forward proxy: the CA is fetched without a token and saved to a file" {
    const pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n";
    var fake: Fake = undefined;
    try fake.start(&.{.{ .method = "GET", .path = "/api/v1/proxy/ca.pem", .body = pem, .headers = &.{.{ .name = "Content-Type", .value = "application/x-pem-file" }} }});
    defer fake.stop();
    var client = try newClient(&fake, .{ .access_password = "gate-pw" });
    defer client.deinit();

    const got = try client.forwardProxyCA(testing.allocator, .{});
    defer testing.allocator.free(got);
    try testing.expectEqualStrings(pem, got);
    const rec = fake.last();
    try testing.expectEqualStrings("GET", rec.method);
    try testing.expectEqualStrings("/api/v1/proxy/ca.pem", rec.target);
    try testing.expect(rec.header("authorization") == null);
    try testing.expectEqualStrings("gate-pw", rec.header("x-acnologia-access").?);

    var tmp = testing.tmpDir(.{});
    defer tmp.cleanup();
    var path_buf: [256]u8 = undefined;
    const path = try std.fmt.bufPrint(&path_buf, ".zig-cache/tmp/{s}/ca.pem", .{tmp.sub_path});
    try client.saveForwardProxyCA(path, .{});
    var read_buf: [256]u8 = undefined;
    const saved = try tmp.dir.readFile(testing.io, "ca.pem", &read_buf);
    try testing.expectEqualStrings(pem, saved);
    try testing.expect(fake.last().header("authorization") == null);
}

test "forward proxy: URL and environment carry the escaped token" {
    var fake: Fake = undefined;
    try fake.start(&.{.{ .method = "GET", .path = "/api/v1/proxy", .body = proxy_setup_body, .headers = &.{json_type} }});
    defer fake.stop();
    var client = try newClient(&fake, .{ .token = odd_token });
    defer client.deinit();

    const url = try client.forwardProxyURL(testing.allocator, .{});
    defer testing.allocator.free(url);
    try testing.expectEqualStrings("http://app:acn_odd%2Ftok%2Ben@acn.example:8888", url);
    try testing.expectEqualStrings("/api/v1/proxy", fake.last().target);
    try testing.expectEqualStrings("Bearer " ++ odd_token, fake.last().header("authorization").?);

    var env = try client.forwardProxyEnv(testing.allocator, null, .{});
    defer env.deinit();
    try testing.expectEqualStrings(url, env.get("HTTPS_PROXY").?);
    try testing.expectEqualStrings(url, env.get("HTTP_PROXY").?);
    try testing.expectEqualStrings("1", env.get("NODE_USE_ENV_PROXY").?);
    try testing.expectEqualStrings("acnologia-ca.pem", env.get("NODE_EXTRA_CA_CERTS").?);
    try testing.expectEqualStrings("acnologia-ca.pem", env.get("REQUESTS_CA_BUNDLE").?);
    try testing.expectEqualStrings("acnologia-ca.pem", env.get("SSL_CERT_FILE").?);
    try testing.expectEqual(@as(usize, 6), env.count());

    var custom = try client.forwardProxyEnv(testing.allocator, "/etc/acn/ca.pem", .{});
    defer custom.deinit();
    try testing.expectEqualStrings("/etc/acn/ca.pem", custom.get("SSL_CERT_FILE").?);
}

// ---------------------------------------------------------------- errors

const ErrorCase = struct {
    status: u16,
    code: []const u8,
    want: acn.Error,
};

const api_cases = [_]ErrorCase{
    .{ .status = 401, .code = "unauthenticated", .want = error.Unauthenticated },
    .{ .status = 403, .code = "unauthenticated", .want = error.Unauthenticated },
    .{ .status = 403, .code = "service_not_allowed", .want = error.ServiceNotAllowed },
    .{ .status = 403, .code = "proxy_not_allowed", .want = error.ProxyNotAllowed },
    .{ .status = 403, .code = "lease_not_allowed", .want = error.LeaseNotAllowed },
    .{ .status = 403, .code = "lease_revoked", .want = error.LeaseRevoked },
    .{ .status = 503, .code = "no_key_available", .want = error.NoKeyAvailable },
    .{ .status = 503, .code = "all_keys_busy", .want = error.AllKeysBusy },
    .{ .status = 503, .code = "service_unavailable", .want = error.ServiceUnavailable },
    .{ .status = 429, .code = "app_limit_reached", .want = error.AppLimitReached },
    .{ .status = 403, .code = "share_revoked", .want = error.ShareRevoked },
    .{ .status = 403, .code = "share_paused", .want = error.SharePaused },
    .{ .status = 403, .code = "share_expired", .want = error.ShareExpired },
    .{ .status = 404, .code = "lease_not_found", .want = error.LeaseNotFound },
    .{ .status = 409, .code = "report_failed", .want = error.ReportFailed },
    .{ .status = 410, .code = "report_failed", .want = error.ReportFailed },
    .{ .status = 404, .code = "unknown_service", .want = error.UnknownService },
    .{ .status = 502, .code = "upstream_unreachable", .want = error.UpstreamUnreachable },
    .{ .status = 502, .code = "upstream_blocked", .want = error.UpstreamBlocked },
    .{ .status = 508, .code = "loop_detected", .want = error.LoopDetected },
    .{ .status = 400, .code = "bad_path", .want = error.BadPath },
    .{ .status = 413, .code = "body_too_large", .want = error.BodyTooLarge },
    .{ .status = 400, .code = "bad_body", .want = error.Api },
    .{ .status = 500, .code = "internal", .want = error.Api },
    .{ .status = 401, .code = "", .want = error.Unauthenticated },
    .{ .status = 503, .code = "", .want = error.NoKeyAvailable },
    .{ .status = 500, .code = "", .want = error.Api },
};

fn errorBody(buf: []u8, code: []const u8) ![]const u8 {
    if (code.len == 0) return "upstream exploded";
    return std.fmt.bufPrint(buf, "{{\"error\":{{\"code\":\"{s}\",\"message\":\"failed with {s}\"}}}}", .{ code, code });
}

test "errors: every status and code maps like the reference SDKs (API calls)" {
    for (api_cases) |c| {
        var buf: [256]u8 = undefined;
        var fake: Fake = undefined;
        try fake.start(&.{.{ .path = "/api/v1/lease", .status = c.status, .body = try errorBody(&buf, c.code) }});
        defer fake.stop();
        var client = try newClient(&fake, .{});
        defer client.deinit();

        var diag: acn.Diagnostic = .{};
        const got = client.lease(testing.allocator, "agi-labs", .{ .diag = &diag });
        testing.expectError(c.want, got) catch |err| {
            std.debug.print("case {d} {s}\n", .{ c.status, c.code });
            return err;
        };
        try testing.expectEqual(c.status, diag.status);
        try testing.expectEqualStrings(c.code, diag.code());
        if (c.code.len > 0) try testing.expect(std.mem.endsWith(u8, diag.message(), c.code));
    }
}

test "errors: Acnologia's own proxy errors map to errors" {
    const proxy_cases = [_]ErrorCase{
        .{ .status = 401, .code = "unauthenticated", .want = error.Unauthenticated },
        .{ .status = 403, .code = "service_not_allowed", .want = error.ServiceNotAllowed },
        .{ .status = 403, .code = "proxy_not_allowed", .want = error.ProxyNotAllowed },
        .{ .status = 503, .code = "no_key_available", .want = error.NoKeyAvailable },
        .{ .status = 503, .code = "all_keys_busy", .want = error.AllKeysBusy },
        .{ .status = 503, .code = "service_unavailable", .want = error.ServiceUnavailable },
        .{ .status = 429, .code = "app_limit_reached", .want = error.AppLimitReached },
        .{ .status = 403, .code = "share_revoked", .want = error.ShareRevoked },
        .{ .status = 403, .code = "share_paused", .want = error.SharePaused },
        .{ .status = 403, .code = "share_expired", .want = error.ShareExpired },
        .{ .status = 404, .code = "unknown_service", .want = error.UnknownService },
        .{ .status = 502, .code = "upstream_unreachable", .want = error.UpstreamUnreachable },
        .{ .status = 502, .code = "upstream_blocked", .want = error.UpstreamBlocked },
        .{ .status = 508, .code = "loop_detected", .want = error.LoopDetected },
        .{ .status = 400, .code = "bad_path", .want = error.BadPath },
        .{ .status = 413, .code = "body_too_large", .want = error.BodyTooLarge },
        .{ .status = 405, .code = "method_not_allowed", .want = error.Api },
    };
    for (proxy_cases) |c| {
        var buf: [256]u8 = undefined;
        var fake: Fake = undefined;
        try fake.start(&.{.{ .path = "/proxy/*", .status = c.status, .body = try errorBody(&buf, c.code) }});
        defer fake.stop();
        var client = try newClient(&fake, .{});
        defer client.deinit();

        var diag: acn.Diagnostic = .{};
        testing.expectError(c.want, client.proxy(testing.allocator, .GET, "agi-labs", "v1/chat", .{ .diag = &diag })) catch |err| {
            std.debug.print("proxy case {d} {s}\n", .{ c.status, c.code });
            return err;
        };
        try testing.expectEqualStrings(c.code, diag.code());
    }
}

test "errors: app_limit_reached carries limit, reset_at and retry after" {
    var fake: Fake = undefined;
    try fake.start(&.{.{
        .path = "/api/v1/lease",
        .status = 429,
        .body = "{\"error\":{\"code\":\"app_limit_reached\",\"message\":\"this app reached its daily requests limit\"},\"limit\":\"daily_requests\",\"reset_at\":\"2026-11-05T00:00:00Z\",\"retry_after_sec\":3600}",
        .headers = &.{.{ .name = "Retry-After", .value = "3600" }},
    }});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var diag: acn.Diagnostic = .{};
    try testing.expectError(error.AppLimitReached, client.lease(testing.allocator, "agi-labs", .{ .diag = &diag }));
    try testing.expectEqualStrings("daily_requests", diag.limit());
    try testing.expectEqualStrings("2026-11-05T00:00:00Z", diag.resetAt());
    try testing.expectEqual(@as(?u32, 3600), diag.retry_after_sec);
    try testing.expectEqual(@as(u16, 429), diag.status);
    try testing.expectEqualStrings("", diag.share());
}

test "errors: a stopped share is ShareUnavailable with its status" {
    const cases = [_]struct { code: []const u8, status: []const u8, want: acn.Error }{
        .{ .code = "share_revoked", .status = "revoked", .want = error.ShareRevoked },
        .{ .code = "share_paused", .status = "paused", .want = error.SharePaused },
        .{ .code = "share_expired", .status = "expired", .want = error.ShareExpired },
    };
    for (cases) |c| {
        var buf: [256]u8 = undefined;
        var fake: Fake = undefined;
        try fake.start(&.{
            .{ .path = "/proxy/*", .status = 403, .body = try errorBody(&buf, c.code), .headers = &.{.{ .name = "X-Acnologia-Share", .value = c.status }} },
            .{ .path = "/api/v1/lease", .status = 403, .body = try errorBody(&buf, c.code) },
        });
        defer fake.stop();
        var client = try newClient(&fake, .{});
        defer client.deinit();

        var diag: acn.Diagnostic = .{};
        const err = client.proxy(testing.allocator, .GET, "agi-labs", "x", .{ .diag = &diag });
        try testing.expectError(c.want, err);
        _ = err catch |e| try testing.expect(acn.isShareUnavailable(e));
        try testing.expectEqualStrings(c.status, diag.share());
        // Without the header the status comes from the code.
        try testing.expectError(c.want, client.lease(testing.allocator, "agi-labs", .{ .diag = &diag }));
        try testing.expectEqualStrings(c.status, diag.share());
    }
    try testing.expect(!acn.isShareUnavailable(error.AppLimitReached));
}

test "errors: Retry-After comes from the body, else from the header" {
    var fake: Fake = undefined;
    try fake.start(&.{
        .{ .path = "/api/v1/lease", .status = 503, .body = "{\"error\":{\"code\":\"all_keys_busy\"},\"retry_after_sec\":2}", .headers = &.{.{ .name = "Retry-After", .value = "30" }} },
        .{ .path = "/proxy/*", .status = 503, .body = "{\"error\":{\"code\":\"service_unavailable\"},\"retry_after_sec\":0}", .headers = &.{.{ .name = "Retry-After", .value = "45" }} },
        .{ .path = "/api/v1/me", .status = 503, .body = "{\"error\":{\"code\":\"no_key_available\"}}" },
    });
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var diag: acn.Diagnostic = .{};
    const busy = client.lease(testing.allocator, "agi-labs", .{ .diag = &diag });
    try testing.expectError(error.AllKeysBusy, busy);
    try testing.expect(acn.isNoKey(error.AllKeysBusy));
    try testing.expectEqual(@as(?u32, 2), diag.retry_after_sec);

    try testing.expectError(error.ServiceUnavailable, client.proxy(testing.allocator, .GET, "agi-labs", "x", .{ .diag = &diag }));
    try testing.expectEqual(@as(?u32, 45), diag.retry_after_sec);

    try testing.expectError(error.NoKeyAvailable, client.me(testing.allocator, .{ .diag = &diag }));
    try testing.expectEqual(@as(?u32, null), diag.retry_after_sec);
}

test "errors: a slow server times out" {
    var fake: Fake = undefined;
    try fake.start(&.{.{ .path = "/api/v1/me", .body = "{}", .delay_ms = 3000 }});
    defer fake.stop();
    var client = try newClient(&fake, .{ .timeout_ms = 150 });
    defer client.deinit();
    var diag: acn.Diagnostic = .{};
    try testing.expectError(error.Timeout, client.me(testing.allocator, .{ .diag = &diag }));
    try testing.expectEqual(@as(u16, 0), diag.status);
}

test "errors: an unreachable server is a RequestFailed" {
    var fake: Fake = undefined;
    try fake.start(&.{});
    const dead_url = try testing.allocator.dupe(u8, fake.url);
    defer testing.allocator.free(dead_url);
    fake.stop();
    var client = try acn.Client.init(testing.allocator, testing.io, .{ .base_url = dead_url, .token = token });
    defer client.deinit();
    var diag: acn.Diagnostic = .{};
    try testing.expectError(error.RequestFailed, client.me(testing.allocator, .{ .diag = &diag }));
    try testing.expect(diag.message().len > 0);
}

// ---------------------------------------------------------------- scrubbing

test "scrubbing: messages, formatting and reports never carry the token or the secret" {
    var fake: Fake = undefined;
    try fake.start(&.{
        .{ .path = "/api/v1/me", .status = 400, .body = "{\"error\":{\"code\":\"bad_request\",\"message\":\"bad token " ++ token ++ " and pw gate-pw\"}}" },
        .{ .path = "/api/v1/lease", .body = lease_body },
        .{ .path = "/api/v1/leases/lse_1/report", .body = "{\"ok\":true,\"next\":null}" },
    });
    defer fake.stop();
    var client = try newClient(&fake, .{ .access_password = "gate-pw" });
    defer client.deinit();

    var diag: acn.Diagnostic = .{};
    try testing.expectError(error.Api, client.me(testing.allocator, .{ .diag = &diag }));
    try testing.expectEqualStrings("bad token [redacted] and pw [redacted]", diag.message());

    var printed: [512]u8 = undefined;
    const client_text = try std.fmt.bufPrint(&printed, "{f}", .{client});
    try testing.expect(std.mem.find(u8, client_text, token) == null);
    try testing.expect(std.mem.find(u8, client_text, "gate-pw") == null);
    const diag_text = try std.fmt.bufPrint(&printed, "{f}", .{diag});
    try testing.expect(std.mem.find(u8, diag_text, token) == null);

    var lease = try client.lease(testing.allocator, "agi-labs", .{});
    defer lease.deinit();
    const lease_text = try std.fmt.bufPrint(&printed, "{f}", .{lease});
    try testing.expect(std.mem.find(u8, lease_text, lease.secret) == null);
    try testing.expect(std.mem.find(u8, lease_text, "***") != null);

    // The secret, raw and URL encoded, and the token are hidden in the report
    // message, which is then cut to 500 bytes.
    var long: [900]u8 = undefined;
    @memset(&long, 'x');
    const msg = try std.fmt.allocPrint(testing.allocator, "GET /v1/chat?key=sk-agi%2Fsecret%2B1 failed, key sk-agi/secret+1, token {s} {s}", .{ token, long });
    defer testing.allocator.free(msg);
    _ = try lease.report(.{ .outcome = .invalid, .status_code = 401, .message = msg });
    const sent = fake.last().body;
    try testing.expect(std.mem.find(u8, sent, "sk-agi") == null);
    try testing.expect(std.mem.find(u8, sent, token) == null);
    try testing.expect(std.mem.find(u8, sent, "key=[key] failed, key [key], token [key]") != null);

    const Sent = struct { message: []const u8 };
    var parsed = try std.json.parseFromSlice(Sent, testing.allocator, sent, .{ .ignore_unknown_fields = true });
    defer parsed.deinit();
    try testing.expectEqual(@as(usize, acn.max_report_message), parsed.value.message.len);
}

// ---------------------------------------------------------------- leases

test "lease: sends options, then renews and reports with rotation" {
    const next_body =
        \\{"ok":true,"next":{"lease_id":"lse_2","key_id":8,"service":"agi-labs","secret":"sk-next",
        \\ "auth":{"type":"bearer","name":""},"base_url":"https://api.agi-labs.test","expires_at":"2026-11-04T10:05:00Z"}}
    ;
    var fake: Fake = undefined;
    try fake.start(&.{
        .{ .method = "POST", .path = "/api/v1/lease", .body = lease_body },
        .{ .method = "POST", .path = "/api/v1/leases/lse_1/renew", .body = "{\"ok\":true,\"expires_at\":\"2026-11-04T11:00:00Z\"}" },
        .{ .method = "POST", .path = "/api/v1/leases/lse_1/report", .body = next_body },
        .{ .method = "POST", .path = "/api/v1/leases/lse_2/report", .body = "{\"ok\":true,\"next\":null}" },
    });
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var lease = try client.lease(testing.allocator, "agi-labs", .{
        .exclude_key_ids = &.{ 3, 4 },
        .ttl_sec = 300,
        .wait_sec = 5,
        .exclusive = true,
    });
    defer lease.deinit();
    try testing.expectEqualStrings(
        "{\"service\":\"agi-labs\",\"exclude_key_ids\":[3,4],\"ttl_sec\":300,\"wait_sec\":5,\"exclusive\":true}",
        fake.last().body,
    );
    try testing.expectEqualStrings("application/json", fake.last().header("content-type").?);
    try testing.expectEqualStrings("lse_1", lease.lease_id);
    try testing.expectEqual(@as(i64, 7), lease.key_id);
    try testing.expectEqualStrings("sk-agi/secret+1", lease.secret);
    try testing.expectEqualStrings("https://api.agi-labs.test", lease.base_url);
    try testing.expect(lease.exclusive);

    const expires = try lease.renew(.{ .ttl_sec = 600 });
    try testing.expectEqualStrings("2026-11-04T11:00:00Z", expires);
    try testing.expectEqualStrings("2026-11-04T11:00:00Z", lease.expires_at);
    try testing.expectEqualStrings("{\"ttl_sec\":600}", fake.last().body);

    var next = (try lease.report(.{ .outcome = .rate_limited, .status_code = 429, .retry_after_sec = 30, .rotate = true })).?;
    defer next.deinit();
    try testing.expect(lease.reported);
    try testing.expectEqualStrings(
        "{\"outcome\":\"rate_limited\",\"rotate\":true,\"status_code\":429,\"retry_after_sec\":30}",
        fake.last().body,
    );
    try testing.expectEqualStrings("lse_2", next.lease_id);
    try testing.expectEqual(acn.AuthKind.bearer, next.auth.kind);

    try testing.expect((try client.report(&next, .{ .outcome = .ok })) == null);
    try testing.expectEqualStrings("{\"outcome\":\"ok\",\"rotate\":false}", fake.last().body);
}

test "lease: wait_sec over 60 is refused before any request" {
    var fake: Fake = undefined;
    try fake.start(&.{});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();
    try testing.expectError(error.InvalidArgument, client.lease(testing.allocator, "agi-labs", .{ .wait_sec = 61 }));
    try testing.expectEqual(@as(usize, 0), fake.count());
}

test "lease: renew of a closed lease is LeaseNotFound" {
    var fake: Fake = undefined;
    try fake.start(&.{
        .{ .path = "/api/v1/lease", .body = lease_body },
        .{ .path = "/api/v1/leases/lse_1/renew", .status = 404, .body = "{\"error\":{\"code\":\"lease_not_found\",\"message\":\"unknown lease\"}}" },
    });
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();
    var lease = try client.lease(testing.allocator, "agi-labs", .{});
    defer lease.deinit();
    var diag: acn.Diagnostic = .{};
    try testing.expectError(error.LeaseNotFound, client.renew(&lease, .{ .diag = &diag }));
    try testing.expectEqualStrings("{}", fake.last().body);
    try testing.expectEqualStrings("lease_not_found", diag.code());
}

fn leaseWithAuth(client: *acn.Client, fake: *Fake, auth: []const u8) !acn.Lease {
    _ = fake;
    var buf: [512]u8 = undefined;
    const body = try std.fmt.bufPrint(&buf,
        \\{{"lease_id":"l","key_id":1,"service":"agi-labs","secret":"s3cr3t/+","auth":{s},"base_url":"https://api.agi-labs.test/","expires_at":"x"}}
    , .{auth});
    return acn.Lease.fromJson(client, testing.allocator, body);
}

test "lease apply: header auth" {
    var fake: Fake = undefined;
    try fake.start(&.{});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var lease = try leaseWithAuth(&client, &fake, "{\"type\":\"header\",\"name\":\"X-Api-Key\",\"prefix\":\"Key \"}");
    defer lease.deinit();
    const h = lease.authHeader().?;
    try testing.expectEqualStrings("X-Api-Key", h.name);
    try testing.expectEqualStrings("Key s3cr3t/+", h.value);

    const headers = try lease.applyHeaders(testing.allocator, &.{
        .{ .name = "Accept", .value = "application/json" },
        .{ .name = "x-api-key", .value = "old" },
    });
    defer testing.allocator.free(headers);
    try testing.expectEqual(@as(usize, 2), headers.len);
    try testing.expectEqualStrings("Accept", headers[0].name);
    try testing.expectEqualStrings("Key s3cr3t/+", headers[1].value);

    const u = try lease.url(testing.allocator, "/v1/chat", &.{.{ .name = "q", .value = "a b" }});
    defer testing.allocator.free(u);
    try testing.expectEqualStrings("https://api.agi-labs.test/v1/chat?q=a%20b", u);

    const same = try lease.applyUrl(testing.allocator, "https://api.agi-labs.test/v1/chat?x=1");
    defer testing.allocator.free(same);
    try testing.expectEqualStrings("https://api.agi-labs.test/v1/chat?x=1", same);

    // Bearer: the default prefix, and the default header name.
    var bearer = try leaseWithAuth(&client, &fake, "{\"type\":\"bearer\",\"name\":\"\"}");
    defer bearer.deinit();
    try testing.expectEqualStrings("Authorization", bearer.authHeader().?.name);
    try testing.expectEqualStrings("Bearer s3cr3t/+", bearer.authHeader().?.value);
}

test "lease apply: query auth" {
    var fake: Fake = undefined;
    try fake.start(&.{});
    defer fake.stop();
    var client = try newClient(&fake, .{});
    defer client.deinit();

    var lease = try leaseWithAuth(&client, &fake, "{\"type\":\"query\",\"name\":\"api_key\"}");
    defer lease.deinit();
    try testing.expect(lease.authHeader() == null);

    const u = try lease.url(testing.allocator, "v1/chat", &.{.{ .name = "q", .value = "hi" }});
    defer testing.allocator.free(u);
    try testing.expectEqualStrings("https://api.agi-labs.test/v1/chat?q=hi&api_key=s3cr3t%2F%2B", u);

    const bare = try lease.url(testing.allocator, "/v1/chat", &.{});
    defer testing.allocator.free(bare);
    try testing.expectEqualStrings("https://api.agi-labs.test/v1/chat?api_key=s3cr3t%2F%2B", bare);

    const applied = try lease.applyUrl(testing.allocator, "https://api.agi-labs.test/v1/chat?x=1#top");
    defer testing.allocator.free(applied);
    try testing.expectEqualStrings("https://api.agi-labs.test/v1/chat?x=1&api_key=s3cr3t%2F%2B#top", applied);

    const headers = try lease.applyHeaders(testing.allocator, &.{.{ .name = "Accept", .value = "*/*" }});
    defer testing.allocator.free(headers);
    try testing.expectEqual(@as(usize, 1), headers.len);

    // No name: defaults to api_key.
    var unnamed = try leaseWithAuth(&client, &fake, "{\"type\":\"query\"}");
    defer unnamed.deinit();
    try testing.expectEqualStrings("api_key", unnamed.auth.name);
}
