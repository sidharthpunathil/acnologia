//! Calls a real Acnologia server: me, a proxied request, then a lease that is
//! renewed and reported. Reads ACNOLOGIA_URL and ACNOLOGIA_TOKEN.
//!
//!   ACNOLOGIA_URL=http://127.0.0.1:8080 ACNOLOGIA_TOKEN=acn_... zig build example -- agi-labs

const std = @import("std");
const acn = @import("acnologia");

pub fn main(init: std.process.Init) !void {
    const gpa = init.gpa;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    const service = if (args.len > 1) args[1] else "agi-labs";

    var client = try acn.Client.init(gpa, init.io, .{ .environ = init.environ_map });
    defer client.deinit();
    std.debug.print("{f}\n", .{client});

    var diag: acn.Diagnostic = .{};

    // Who am I?
    var me = client.me(gpa, .{ .diag = &diag }) catch |err| return fail(err, &diag);
    defer me.deinit();
    std.debug.print("me: app {d} \"{s}\", {d} service(s), requests today {d}\n", .{
        me.value.app.id, me.value.app.name, me.value.services.len, me.value.usage.today_requests,
    });

    // Proxy mode: Acnologia attaches the key.
    var res = client.proxy(gpa, .POST, service, "/v1/chat", .{
        .query = &.{.{ .name = "mode", .value = "fast" }},
        .headers = &.{.{ .name = "Content-Type", .value = "application/json" }},
        .body = "{\"prompt\":\"hello\"}",
        .idempotency_key = "example-1",
        .diag = &diag,
    }) catch |err| return fail(err, &diag);
    defer res.deinit();
    std.debug.print("proxy: HTTP {d}, key {?d}, attempts {?d}, service {?s}, body {s}\n", .{
        res.status, res.acnologia.key_id, res.acnologia.attempts, res.acnologia.service, res.body,
    });

    var again = client.proxy(gpa, .POST, service, "/v1/chat", .{
        .query = &.{.{ .name = "mode", .value = "fast" }},
        .headers = &.{.{ .name = "Content-Type", .value = "application/json" }},
        .body = "{\"prompt\":\"hello\"}",
        .idempotency_key = "example-1",
        .diag = &diag,
    }) catch |err| return fail(err, &diag);
    defer again.deinit();
    std.debug.print("proxy again with the same Idempotency-Key: HTTP {d}, replayed {}\n", .{ again.status, again.acnologia.replayed });

    // Lease mode: borrow the key, renew it, report it.
    var lease = client.lease(gpa, service, .{ .ttl_sec = 60, .diag = &diag }) catch |err| return fail(err, &diag);
    defer lease.deinit();
    std.debug.print("lease: {f}\n", .{lease});

    const expires = lease.renew(.{ .ttl_sec = 300, .diag = &diag }) catch |err| return fail(err, &diag);
    std.debug.print("renewed until {s}\n", .{expires});

    const target = try lease.url(gpa, "/v1/chat", &.{});
    defer gpa.free(target);
    if (lease.authHeader()) |h| {
        std.debug.print("call the provider with header {s}: *** (url has no key)\n", .{h.name});
    } else {
        std.debug.print("call the provider with the key in the query (url not printed)\n", .{});
    }

    // Pretend the provider rate limited the key, and ask for the next one.
    var next = lease.report(.{
        .outcome = .rate_limited,
        .status_code = 429,
        .retry_after_sec = 5,
        .message = "rate limited by the provider",
        .rotate = true,
        .diag = &diag,
    }) catch |err| return fail(err, &diag);
    if (next) |*n| {
        defer n.deinit();
        std.debug.print("rotated to key {d}\n", .{n.key_id});
        _ = n.report(.{ .outcome = .ok, .diag = &diag }) catch |err| return fail(err, &diag);
        std.debug.print("reported ok\n", .{});
    } else {
        std.debug.print("no other key free\n", .{});
    }

    // Reporting twice is refused.
    _ = lease.report(.{ .outcome = .ok, .diag = &diag }) catch |err| {
        std.debug.print("second report: error.{s} ({f})\n", .{ @errorName(err), diag });
    };
}

fn fail(err: acn.Error, diag: *const acn.Diagnostic) acn.Error {
    std.debug.print("failed: error.{s} ({f})\n", .{ @errorName(err), diag });
    return err;
}
