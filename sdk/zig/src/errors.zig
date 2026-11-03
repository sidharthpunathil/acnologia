//! Errors, the Diagnostic that carries their details, and the redaction
//! helpers every message passes through.

const std = @import("std");
const http = std.http;

/// Errors the server answers with, mapped from the HTTP status and the
/// `error.code` of its JSON body. A code this SDK does not know becomes
/// `error.Api`; read `Diagnostic.code()` for it.
pub const ApiError = error{
    /// 401, or 403 `unauthenticated`: the token is missing, wrong or its app is disabled.
    Unauthenticated,
    /// 403 `service_not_allowed`: this token may not use the service.
    ServiceNotAllowed,
    /// 403 `proxy_not_allowed`: the app is lease only.
    ProxyNotAllowed,
    /// 403 `lease_not_allowed`: the app is proxy only, or the service is one
    /// another server shares with yours (those are proxy only).
    LeaseNotAllowed,
    /// 403 `lease_revoked`: the app may no longer hold this lease's key.
    LeaseRevoked,
    /// 503 `no_key_available`: every usable key is cooling down, exhausted
    /// or disabled. `Diagnostic.retry_after_sec` says how long to wait.
    NoKeyAvailable,
    /// 503 `all_keys_busy`: usable keys exist but all are at a concurrency or
    /// rate limit. A kind of NoKeyAvailable (see `isNoKey`). Retry after
    /// `Diagnostic.retry_after_sec`, or lease with `wait_sec`.
    AllKeysBusy,
    /// 503 `service_unavailable`: the provider keeps failing and its circuit
    /// breaker is open. Not a NoKeyAvailable.
    ServiceUnavailable,
    /// 429 `app_limit_reached`: the app used up a request or budget limit.
    /// `Diagnostic.limit()` and `Diagnostic.resetAt()` say which and until when.
    AppLimitReached,
    /// 403 `share_revoked`: the owner revoked the share behind this token.
    ShareRevoked,
    /// 403 `share_paused`: the owner paused the share behind this token.
    SharePaused,
    /// 403 `share_expired`: the share behind this token expired.
    ShareExpired,
    /// 404 `lease_not_found`: renewing a lease that is not open.
    LeaseNotFound,
    /// 409 or 410 `report_failed`: the lease was already reported, or expired
    /// more than 10 minutes ago.
    ReportFailed,
    /// 404 `unknown_service`: no service or group has that slug.
    UnknownService,
    /// 502 `upstream_unreachable`: the provider could not be reached.
    UpstreamUnreachable,
    /// 502 `upstream_blocked`: the service points at an address Acnologia refuses.
    UpstreamBlocked,
    /// 508 `loop_detected`: the request passed through too many Acnologia servers.
    LoopDetected,
    /// 400 `bad_path`: the proxy path has a literal `?`, `#`, or a `.` or `..` segment.
    BadPath,
    /// 413 `body_too_large`: the proxy body is over the server's limit.
    BodyTooLarge,
    /// Any other error answer. `Diagnostic` has the status and the code.
    Api,
};

/// Errors raised on this side of the wire.
pub const ClientError = error{
    /// No answer before the timeout.
    Timeout,
    /// The connection failed or the answer was not valid HTTP.
    /// `Diagnostic.message()` names the underlying failure.
    RequestFailed,
    /// The answer body was over `Options.max_body_bytes`.
    ResponseTooLarge,
    /// The server answered 2xx with a body this SDK could not read.
    BadResponse,
    /// A URL could not be built or parsed.
    InvalidUrl,
    /// An argument was out of range, or a header held a line break.
    InvalidArgument,
    OutOfMemory,
    Canceled,
};

/// Every error a call can return.
pub const Error = ApiError || ClientError;

/// True for `error.NoKeyAvailable` and its kind `error.AllKeysBusy`.
pub fn isNoKey(err: anyerror) bool {
    return err == error.NoKeyAvailable or err == error.AllKeysBusy;
}

/// Details of the last failed call, filled when you pass a pointer to one in
/// a call's options. It holds fixed size buffers, so it needs no allocator
/// and no deinit. Messages are scrubbed of the app token, the access
/// password and any lease secret before they are stored.
pub const Diagnostic = struct {
    /// HTTP status, or 0 when no answer arrived.
    status: u16 = 0,
    /// Seconds to wait before retrying: `retry_after_sec` from the body, or
    /// else the `Retry-After` header. Null when the server gave neither.
    retry_after_sec: ?u32 = null,

    code_buf: [64]u8 = undefined,
    code_len: usize = 0,
    message_buf: [512]u8 = undefined,
    message_len: usize = 0,
    limit_buf: [32]u8 = undefined,
    limit_len: usize = 0,
    reset_at_buf: [64]u8 = undefined,
    reset_at_len: usize = 0,

    /// The server's `error.code`, such as `lease_not_found`. Empty when none.
    pub fn code(d: *const Diagnostic) []const u8 {
        return d.code_buf[0..d.code_len];
    }

    /// The server's `error.message`, or the name of a client side failure.
    pub fn message(d: *const Diagnostic) []const u8 {
        return d.message_buf[0..d.message_len];
    }

    /// For `AppLimitReached`: which limit, such as `daily_requests`.
    pub fn limit(d: *const Diagnostic) []const u8 {
        return d.limit_buf[0..d.limit_len];
    }

    /// For `AppLimitReached`: when the limit resets (RFC 3339).
    pub fn resetAt(d: *const Diagnostic) []const u8 {
        return d.reset_at_buf[0..d.reset_at_len];
    }

    pub fn reset(d: *Diagnostic) void {
        d.* = .{};
    }

    pub fn format(d: *const Diagnostic, w: *std.Io.Writer) std.Io.Writer.Error!void {
        try w.print("acnologia: HTTP {d}", .{d.status});
        if (d.code_len > 0) try w.print(" {s}", .{d.code()});
        if (d.message_len > 0) try w.print(": {s}", .{d.message()});
        if (d.retry_after_sec) |s| try w.print(" (retry after {d}s)", .{s});
    }
};

/// Values that must never appear in a stored message.
pub const Secrets = struct {
    token: []const u8 = "",
    access_password: []const u8 = "",
    lease_secret: []const u8 = "",
};

/// Maps a status and an error code to an error. Exactly the reference SDKs'
/// rules, plus a distinct error for each documented consumer code.
pub fn errorFor(status: u16, code: []const u8) ApiError {
    const map = std.StaticStringMap(ApiError).initComptime(.{
        .{ "unauthenticated", error.Unauthenticated },
        .{ "service_not_allowed", error.ServiceNotAllowed },
        .{ "proxy_not_allowed", error.ProxyNotAllowed },
        .{ "lease_not_allowed", error.LeaseNotAllowed },
        .{ "lease_revoked", error.LeaseRevoked },
        .{ "no_key_available", error.NoKeyAvailable },
        .{ "all_keys_busy", error.AllKeysBusy },
        .{ "service_unavailable", error.ServiceUnavailable },
        .{ "app_limit_reached", error.AppLimitReached },
        .{ "share_revoked", error.ShareRevoked },
        .{ "share_paused", error.SharePaused },
        .{ "share_expired", error.ShareExpired },
        .{ "lease_not_found", error.LeaseNotFound },
        .{ "report_failed", error.ReportFailed },
        .{ "unknown_service", error.UnknownService },
        .{ "upstream_unreachable", error.UpstreamUnreachable },
        .{ "upstream_blocked", error.UpstreamBlocked },
        .{ "loop_detected", error.LoopDetected },
        .{ "bad_path", error.BadPath },
        .{ "body_too_large", error.BodyTooLarge },
    });
    if (map.get(code)) |e| return e;
    if (code.len == 0) {
        if (status == 401) return error.Unauthenticated;
        if (status == 503) return error.NoKeyAvailable;
    }
    return error.Api;
}

/// Codes only Acnologia's own proxy answers with. A proxied error carrying
/// one of these and no `X-Acnologia-Service` header came from Acnologia,
/// not from the provider.
pub fn isProxyCode(code: []const u8) bool {
    const codes = [_][]const u8{
        "unauthenticated",  "service_not_allowed", "proxy_not_allowed", "no_key_available",
        "all_keys_busy",    "service_unavailable", "app_limit_reached", "unknown_service",
        "share_revoked",    "share_paused",        "share_expired",     "upstream_unreachable",
        "upstream_blocked", "loop_detected",       "bad_path",          "bad_body",
        "body_too_large",   "method_not_allowed",  "empty_group",
    };
    for (codes) |c| if (std.mem.eql(u8, c, code)) return true;
    return false;
}

/// Finds a header by name, ignoring case.
pub fn headerValue(headers: []const http.Header, name: []const u8) ?[]const u8 {
    for (headers) |h| {
        if (std.ascii.eqlIgnoreCase(h.name, name)) return h.value;
    }
    return null;
}

/// Parses a Retry-After header holding whole seconds. HTTP dates are ignored.
pub fn parseRetryAfter(value: ?[]const u8) ?u32 {
    const v = std.mem.trim(u8, value orelse return null, " \t");
    const n = std.fmt.parseInt(u32, v, 10) catch return null;
    return if (n > 0) n else null;
}

/// Reads an error answer into `diag` and returns the matching error.
/// `body` may be anything; an unreadable one still maps by status.
pub fn fromAnswer(
    status: u16,
    headers: []const http.Header,
    body: []const u8,
    secrets: Secrets,
    diag: ?*Diagnostic,
) ApiError {
    var scratch: [32 * 1024]u8 = undefined;
    const parsed = parseEnvelope(body, &scratch);
    if (diag) |d| {
        d.reset();
        d.status = status;
        d.code_len = copyInto(&d.code_buf, parsed.code);
        const msg = if (parsed.message.len > 0) parsed.message else (@as(http.Status, @fromBackingInt(@intCast(status))).phrase() orelse "");
        d.message_len = scrubInto(&d.message_buf, msg, secrets);
        d.limit_len = copyInto(&d.limit_buf, parsed.limit);
        d.reset_at_len = copyInto(&d.reset_at_buf, parsed.reset_at);
        d.retry_after_sec = parsed.retry_after orelse parseRetryAfter(headerValue(headers, "retry-after"));
    }
    return errorFor(status, parsed.code);
}

/// Records a client side failure in `diag`.
pub fn fromClient(diag: ?*Diagnostic, status: u16, detail: []const u8) void {
    if (diag) |d| {
        d.reset();
        d.status = status;
        d.message_len = copyInto(&d.message_buf, detail);
    }
}

pub const Envelope = struct {
    code: []const u8 = "",
    message: []const u8 = "",
    limit: []const u8 = "",
    reset_at: []const u8 = "",
    retry_after: ?u32 = null,
};

/// The interesting fields of `{"error": {"code", "message"}, "retry_after_sec",
/// "limit", "reset_at"}`, read with a fixed buffer so a bad body cannot cost
/// an allocation. Slices point into `body` or into `scratch`.
pub fn parseEnvelope(body: []const u8, scratch: []u8) Envelope {
    var out: Envelope = .{};
    var fba: std.heap.FixedBufferAllocator = .init(scratch);
    const value = std.json.parseFromSliceLeaky(std.json.Value, fba.allocator(), body, .{}) catch return out;
    if (value != .object) return out;
    const obj = value.object;
    if (obj.get("error")) |e| switch (e) {
        .object => |eo| {
            out.code = stringOf(eo.get("code"));
            out.message = stringOf(eo.get("message"));
        },
        .string => |s| out.message = s,
        else => {},
    };
    out.limit = stringOf(obj.get("limit"));
    out.reset_at = stringOf(obj.get("reset_at"));
    if (obj.get("retry_after_sec")) |ra| switch (ra) {
        .integer => |n| out.retry_after = if (n > 0) @intCast(@min(n, std.math.maxInt(u32))) else null,
        .float => |f| out.retry_after = if (f > 0) @intFromFloat(@min(@ceil(f), 4294967295.0)) else null,
        else => {},
    };
    return out;
}

fn stringOf(v: ?std.json.Value) []const u8 {
    const value = v orelse return "";
    return switch (value) {
        .string => |s| s,
        else => "",
    };
}

fn copyInto(buf: []u8, s: []const u8) usize {
    const n = utf8Prefix(s, buf.len);
    @memcpy(buf[0..n], s[0..n]);
    return n;
}

/// Copies `s` into `buf` with every secret replaced by `[redacted]`.
pub fn scrubInto(buf: []u8, s: []const u8, secrets: Secrets) usize {
    var w: std.Io.Writer = .fixed(buf);
    writeScrubbed(&w, s, &.{ secrets.token, secrets.access_password, secrets.lease_secret }, "[redacted]") catch {};
    return utf8Prefix(buf[0..w.end], w.end);
}

/// Writes `s` with each non empty needle (raw or percent encoded) replaced.
pub fn writeScrubbed(w: *std.Io.Writer, s: []const u8, needles: []const []const u8, replacement: []const u8) std.Io.Writer.Error!void {
    var enc_bufs: [3][1024]u8 = undefined;
    var encoded: [3][]const u8 = .{ "", "", "" };
    for (needles, 0..) |n, i| {
        if (i >= encoded.len or n.len == 0) continue;
        var ew: std.Io.Writer = .fixed(&enc_bufs[i]);
        percentEncode(&ew, n) catch continue;
        if (!std.mem.eql(u8, ew.buffered(), n)) encoded[i] = ew.buffered();
    }
    var i: usize = 0;
    outer: while (i < s.len) {
        for (needles, 0..) |n, j| {
            if (n.len > 0 and std.mem.startsWith(u8, s[i..], n)) {
                try w.writeAll(replacement);
                i += n.len;
                continue :outer;
            }
            if (j < encoded.len and encoded[j].len > 0 and std.mem.startsWith(u8, s[i..], encoded[j])) {
                try w.writeAll(replacement);
                i += encoded[j].len;
                continue :outer;
            }
        }
        try w.writeByte(s[i]);
        i += 1;
    }
}

/// The longest prefix of `s` no longer than `max` that ends on a UTF-8
/// character boundary.
pub fn utf8Prefix(s: []const u8, max: usize) usize {
    if (s.len <= max) return s.len;
    var n = max;
    while (n > 0 and (s[n] & 0xC0) == 0x80) n -= 1;
    return n;
}

fn isUnreserved(c: u8) bool {
    return std.ascii.isAlphanumeric(c) or c == '-' or c == '.' or c == '_' or c == '~';
}

/// Percent encodes everything except RFC 3986 unreserved characters. For
/// query names and values, and path segments such as a service slug.
pub fn percentEncode(w: *std.Io.Writer, raw: []const u8) std.Io.Writer.Error!void {
    return std.Uri.Component.percentEncode(w, raw, isUnreserved);
}

fn isPathChar(c: u8) bool {
    return isUnreserved(c) or c == '/' or c == '%' or c == ':' or c == '@' or
        c == '!' or c == '$' or c == '&' or c == '\'' or c == '(' or c == ')' or
        c == '*' or c == '+' or c == ',' or c == ';' or c == '=';
}

/// Percent encodes a path, keeping `/` and characters a path may hold. `%`
/// is kept too, so an already encoded path passes through unchanged.
pub fn percentEncodePath(w: *std.Io.Writer, raw: []const u8) std.Io.Writer.Error!void {
    return std.Uri.Component.percentEncode(w, raw, isPathChar);
}

test "errorFor follows the reference rules" {
    try std.testing.expectEqual(error.Unauthenticated, errorFor(401, ""));
    try std.testing.expectEqual(error.NoKeyAvailable, errorFor(503, ""));
    try std.testing.expectEqual(error.Api, errorFor(500, ""));
    try std.testing.expectEqual(error.Api, errorFor(400, "bad_body"));
    try std.testing.expect(isNoKey(error.AllKeysBusy));
    try std.testing.expect(!isNoKey(error.ServiceUnavailable));
}

test "scrubInto hides raw and encoded secrets" {
    var buf: [128]u8 = undefined;
    const n = scrubInto(&buf, "GET /x?k=a%2Fb and a/b and acn_tok", .{ .token = "acn_tok", .lease_secret = "a/b" });
    try std.testing.expectEqualStrings("GET /x?k=[redacted] and [redacted] and [redacted]", buf[0..n]);
}
