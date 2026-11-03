//! One HTTP exchange with a deadline. Internal to the SDK.
//!
//! std.http.Client has no read timeout of its own, so the request runs as a
//! concurrent task next to a timer, and whichever finishes first wins. When
//! the Io implementation cannot run tasks concurrently, the request runs
//! inline with no deadline.

const std = @import("std");
const Io = std.Io;
const http = std.http;
const Allocator = std.mem.Allocator;

/// What to send. Every slice is borrowed and must outlive `send`.
pub const Request = struct {
    method: http.Method,
    url: []const u8,
    authorization: []const u8,
    user_agent: []const u8,
    content_type: ?[]const u8 = null,
    headers: []const http.Header = &.{},
    body: ?[]const u8 = null,
    /// 0 means no deadline.
    timeout_ms: u64 = 0,
    max_body_bytes: usize,
};

/// A received answer. Every slice lives in the arena passed to `send`.
pub const Response = struct {
    status: u16,
    headers: []const http.Header,
    body: []const u8,
};

pub const SendError = error{
    /// The deadline passed before the answer arrived.
    Timeout,
    /// The connection failed, or the answer was not valid HTTP.
    RequestFailed,
    /// The answer body was larger than `max_body_bytes`.
    ResponseTooLarge,
    /// The URL could not be parsed or used.
    InvalidUrl,
    OutOfMemory,
    Canceled,
};

/// Sends `req` and reads the whole answer into `arena`. On error, `detail`
/// (when given) receives the name of the underlying failure, which never
/// contains a header value or a body.
pub fn send(
    client: *http.Client,
    io: Io,
    arena: Allocator,
    req: *const Request,
    detail: ?*[]const u8,
) SendError!Response {
    if (req.timeout_ms == 0) return exchange(client, arena, req, detail);

    const Outcome = union(enum) {
        done: SendError!Response,
        timer: Io.Cancelable!void,
    };
    var buffer: [2]Outcome = undefined;
    var select: Io.Select(Outcome) = .init(io, &buffer);

    select.concurrent(.timer, sleepMs, .{ io, req.timeout_ms }) catch
        return exchange(client, arena, req, detail);
    select.concurrent(.done, exchange, .{ client, arena, req, detail }) catch {
        select.cancelDiscard();
        return exchange(client, arena, req, detail);
    };

    const first = select.await() catch |err| {
        // The caller was canceled. Stop both tasks; anything they
        // allocated lives in the caller's arena.
        while (select.cancel()) |_| {}
        return err;
    };
    switch (first) {
        .done => |result| {
            select.cancelDiscard();
            return result;
        },
        .timer => {
            while (select.cancel()) |_| {}
            if (detail) |d| d.* = "Timeout";
            return error.Timeout;
        },
    }
}

fn sleepMs(io: Io, ms: u64) Io.Cancelable!void {
    const capped: i64 = @intCast(@min(ms, std.math.maxInt(i32)));
    return io.sleep(.fromMilliseconds(capped), .awake);
}

fn exchange(
    client: *http.Client,
    arena: Allocator,
    req: *const Request,
    detail: ?*[]const u8,
) SendError!Response {
    return exchangeInner(client, arena, req) catch |err| {
        if (detail) |d| d.* = @errorName(err);
        return switch (err) {
            error.OutOfMemory => error.OutOfMemory,
            error.Canceled => error.Canceled,
            error.StreamTooLong => error.ResponseTooLarge,
            error.UnexpectedCharacter,
            error.InvalidFormat,
            error.InvalidPort,
            error.InvalidHostName,
            error.UnsupportedUriScheme,
            error.UriMissingHost,
            => error.InvalidUrl,
            else => error.RequestFailed,
        };
    };
}

fn exchangeInner(client: *http.Client, arena: Allocator, req: *const Request) !Response {
    const uri = try std.Uri.parse(req.url);

    var request = try client.request(req.method, uri, .{
        // Never follow a redirect: the app token must not reach another host.
        .redirect_behavior = .unhandled,
        .keep_alive = true,
        .headers = .{
            .authorization = .{ .override = req.authorization },
            .user_agent = .{ .override = req.user_agent },
            .content_type = if (req.content_type) |ct| .{ .override = ct } else .default,
        },
        .extra_headers = req.headers,
    });
    defer request.deinit();

    if (req.method.requestHasBody()) {
        const payload = req.body orelse "";
        request.transfer_encoding = .{ .content_length = payload.len };
        var body_writer = try request.sendBodyUnflushed(&.{});
        try body_writer.writer.writeAll(payload);
        try body_writer.end();
        try request.connection.?.flush();
    } else {
        // The caller refuses a body on GET, HEAD and friends before this.
        try request.sendBodiless();
    }

    var response = try request.receiveHead(&.{});
    const status: u16 = @backingInt(response.head.status);

    // Copy the headers before reading the body: reading invalidates them.
    var headers: std.ArrayList(http.Header) = .empty;
    var it = response.head.iterateHeaders();
    while (it.next()) |h| {
        try headers.append(arena, .{
            .name = try arena.dupe(u8, h.name),
            .value = try arena.dupe(u8, h.value),
        });
    }

    const decompress_buffer: []u8 = switch (response.head.content_encoding) {
        .identity => &.{},
        .zstd => try arena.alloc(u8, std.compress.zstd.default_window_len),
        .deflate, .gzip => try arena.alloc(u8, std.compress.flate.max_window_len),
        .compress => return error.UnsupportedCompressionMethod,
    };
    var transfer_buffer: [64]u8 = undefined;
    var decompress: http.Decompress = undefined;
    const reader = response.readerDecompressing(&transfer_buffer, &decompress, decompress_buffer);
    const body = reader.allocRemaining(arena, .limited(req.max_body_bytes)) catch |err| switch (err) {
        error.ReadFailed => return response.bodyErr() orelse error.ReadFailed,
        else => |e| return e,
    };

    return .{ .status = status, .headers = headers.items, .body = body };
}
