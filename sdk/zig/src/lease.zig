//! A borrowed key, and the helpers that put it where the provider expects it.

const std = @import("std");
const http = std.http;
const Allocator = std.mem.Allocator;

const errors = @import("errors.zig");
const root = @import("acnologia.zig");
const Client = root.Client;
const Diagnostic = errors.Diagnostic;
const Error = errors.Error;
const QueryParam = root.QueryParam;

/// Where the provider expects the key.
pub const AuthKind = enum {
    /// A query parameter named `Auth.name` (default `api_key`).
    query,
    /// A header named `Auth.name` (default `Authorization`).
    header,
    /// `Authorization: Bearer <secret>`, or the header `Auth.name` when set.
    bearer,

    pub fn parse(s: []const u8) AuthKind {
        if (std.mem.eql(u8, s, "query")) return .query;
        if (std.mem.eql(u8, s, "bearer")) return .bearer;
        return .header;
    }
};

pub const Auth = struct {
    kind: AuthKind,
    /// The parameter or header name, defaulted when the server sent none.
    name: []const u8,
    /// Goes in front of the secret for every kind. For `bearer` it replaces
    /// the default `Bearer ` prefix.
    prefix: []const u8,
};

/// How the key did, for `Lease.report`.
pub const Outcome = enum {
    ok,
    rate_limited,
    busy,
    quota_exhausted,
    invalid,
    banned,
    @"error",
};

pub const ReportOptions = struct {
    outcome: Outcome,
    /// The provider's HTTP status, when there was one.
    status_code: ?u16 = null,
    /// How long the key should cool down, when the provider said so.
    retry_after_sec: ?u32 = null,
    /// A short note for the dashboard. The lease secret and the app token
    /// are replaced with `[key]` and the text is cut to 500 bytes before it
    /// is sent.
    message: ?[]const u8 = null,
    /// Ask for the next key in the same call. `report` then returns it.
    rotate: bool = false,
    diag: ?*Diagnostic = null,
};

pub const RenewOptions = struct {
    /// New lifetime in seconds. Null uses the server default (120 seconds);
    /// the server caps it at 24 hours.
    ttl_sec: ?u32 = null,
    diag: ?*Diagnostic = null,
};

/// Longest report message the SDK sends, in bytes.
pub const max_report_message = 500;

/// A key handed out by Acnologia.
///
/// Memory: a Lease owns every string in it through its own arena, created
/// from the allocator passed to `Client.lease`. Call `deinit` once to wipe
/// the secret and free it all. Slices you read from a Lease are valid until
/// then. A Lease keeps a pointer to its Client, so the Client must outlive it.
///
/// Report every lease. On a service with a concurrency limit an unreported
/// lease holds one of the key's slots until its TTL runs out.
pub const Lease = struct {
    client: *Client,
    arena: std.heap.ArenaAllocator,

    lease_id: []const u8,
    key_id: i64,
    service: []const u8,
    /// The raw key. Never log it; `format` prints `***` in its place.
    secret: []const u8,
    auth: Auth,
    /// The provider's base URL, without a trailing slash.
    base_url: []const u8,
    /// RFC 3339 expiry. `renew` updates it.
    expires_at: []const u8,
    /// True when the key is reserved for this lease only.
    exclusive: bool,
    /// True once `report` succeeded.
    reported: bool = false,

    /// The header value or query value: prefix plus secret.
    auth_value: []const u8,

    /// Wipes the secret and frees everything the lease owns.
    pub fn deinit(self: *Lease) void {
        std.crypto.secureZero(u8, @constCast(self.secret));
        std.crypto.secureZero(u8, @constCast(self.auth_value));
        self.arena.deinit();
        self.* = undefined;
    }

    /// Builds a Lease from the server's JSON. Copies everything it keeps.
    pub fn fromJson(client: *Client, allocator: Allocator, body: []const u8) Error!Lease {
        var arena: std.heap.ArenaAllocator = .init(allocator);
        errdefer arena.deinit();
        const a = arena.allocator();
        const raw = std.json.parseFromSliceLeaky(LeaseJson, a, body, .{
            .ignore_unknown_fields = true,
            .allocate = .alloc_always,
        }) catch |err| return switch (err) {
            error.OutOfMemory => error.OutOfMemory,
            else => error.BadResponse,
        };
        return fromParsed(client, arena, raw);
    }

    fn fromParsed(client: *Client, arena_in: std.heap.ArenaAllocator, raw: LeaseJson) Error!Lease {
        var arena = arena_in;
        const a = arena.allocator();
        if (raw.lease_id.len == 0 or raw.secret.len == 0) return error.BadResponse;
        const kind = AuthKind.parse(raw.auth.type);
        const name = if (raw.auth.name.len > 0) raw.auth.name else switch (kind) {
            .query => "api_key",
            .header, .bearer => "Authorization",
        };
        const value = switch (kind) {
            .bearer => if (raw.auth.prefix.len > 0)
                try std.mem.concat(a, u8, &.{ raw.auth.prefix, raw.secret })
            else
                try std.mem.concat(a, u8, &.{ "Bearer ", raw.secret }),
            .query, .header => try std.mem.concat(a, u8, &.{ raw.auth.prefix, raw.secret }),
        };
        return .{
            .client = client,
            .arena = arena,
            .lease_id = raw.lease_id,
            .key_id = raw.key_id,
            .service = raw.service,
            .secret = raw.secret,
            .auth = .{ .kind = kind, .name = name, .prefix = raw.auth.prefix },
            .base_url = std.mem.trimEnd(u8, raw.base_url, "/"),
            .expires_at = raw.expires_at,
            .exclusive = raw.exclusive,
            .auth_value = value,
        };
    }

    /// Reports how the key did and ends the lease.
    ///
    /// With `rotate`, returns the next lease when the server issued one, or
    /// null when no other key is free (or the app reached a limit). The next
    /// lease is allocated with the same allocator as this one and must be
    /// freed with its own `deinit`. This lease still needs its `deinit`.
    pub fn report(self: *Lease, options: ReportOptions) Error!?Lease {
        const gpa = self.client.gpa;
        var body: std.Io.Writer.Allocating = .init(gpa);
        defer body.deinit();
        writeReportBody(&body.writer, options, self.secret, self.client.token) catch return error.OutOfMemory;

        const path = try self.leasePath(gpa, "report");
        defer gpa.free(path);

        var arena: std.heap.ArenaAllocator = .init(self.arena.child_allocator);
        errdefer arena.deinit();
        const answer = try self.client.apiCall(arena.allocator(), .POST, path, body.written(), 0, self.secret, options.diag);

        const parsed = std.json.parseFromSliceLeaky(ReportJson, arena.allocator(), answer, .{
            .ignore_unknown_fields = true,
            .allocate = .alloc_always,
        }) catch |err| {
            std.crypto.secureZero(u8, @constCast(answer));
            return switch (err) {
                error.OutOfMemory => error.OutOfMemory,
                else => error.BadResponse,
            };
        };
        std.crypto.secureZero(u8, @constCast(answer));
        self.reported = true;
        const next = parsed.next orelse {
            arena.deinit();
            return null;
        };
        return try fromParsed(self.client, arena, next);
    }

    /// Extends this open lease so a long job keeps its key. Returns the new
    /// expiry, which also replaces `expires_at` and is owned by the lease.
    /// `error.LeaseNotFound` means the lease is no longer open.
    pub fn renew(self: *Lease, options: RenewOptions) Error![]const u8 {
        const gpa = self.client.gpa;
        const path = try self.leasePath(gpa, "renew");
        defer gpa.free(path);
        var body_buf: [48]u8 = undefined;
        const body = if (options.ttl_sec) |t|
            std.fmt.bufPrint(&body_buf, "{{\"ttl_sec\":{d}}}", .{t}) catch unreachable
        else
            "{}";

        var scratch: std.heap.ArenaAllocator = .init(gpa);
        defer scratch.deinit();
        const answer = try self.client.apiCall(scratch.allocator(), .POST, path, body, 0, self.secret, options.diag);
        const parsed = std.json.parseFromSliceLeaky(RenewJson, scratch.allocator(), answer, .{
            .ignore_unknown_fields = true,
        }) catch |err| return switch (err) {
            error.OutOfMemory => error.OutOfMemory,
            else => error.BadResponse,
        };
        if (parsed.expires_at.len == 0) return error.BadResponse;
        self.expires_at = try self.arena.allocator().dupe(u8, parsed.expires_at);
        return self.expires_at;
    }

    /// The header to send for `header` and `bearer` auth, or null for
    /// `query` auth. Both slices are owned by the lease.
    pub fn authHeader(self: *const Lease) ?http.Header {
        return switch (self.auth.kind) {
            .query => null,
            .header, .bearer => .{ .name = self.auth.name, .value = self.auth_value },
        };
    }

    /// Returns `headers` with the key added for `header` and `bearer` auth
    /// (replacing a header of the same name), or an unchanged copy for
    /// `query` auth. The caller frees the returned slice with `allocator`;
    /// the strings in it are borrowed from `headers` and from the lease.
    pub fn applyHeaders(self: *const Lease, allocator: Allocator, headers: []const http.Header) Allocator.Error![]http.Header {
        var out: std.ArrayList(http.Header) = try .initCapacity(allocator, headers.len + 1);
        errdefer out.deinit(allocator);
        const extra = self.authHeader();
        for (headers) |h| {
            if (extra) |e| if (std.ascii.eqlIgnoreCase(h.name, e.name)) continue;
            out.appendAssumeCapacity(h);
        }
        if (extra) |e| out.appendAssumeCapacity(e);
        return out.toOwnedSlice(allocator);
    }

    /// Returns `url` with the key added as a query parameter for `query`
    /// auth (after any query it already has), or an unchanged copy for the
    /// other kinds. The caller frees the result with `allocator`. It may
    /// hold the secret: never log it.
    pub fn applyUrl(self: *const Lease, allocator: Allocator, target: []const u8) Allocator.Error![]u8 {
        var w: std.Io.Writer.Allocating = .init(allocator);
        errdefer w.deinit();
        const hash = std.mem.findScalar(u8, target, '#') orelse target.len;
        w.writer.writeAll(target[0..hash]) catch return error.OutOfMemory;
        if (self.auth.kind == .query) {
            const sep: u8 = if (std.mem.findScalar(u8, target[0..hash], '?') == null) '?' else '&';
            w.writer.writeByte(sep) catch return error.OutOfMemory;
            writeParam(&w.writer, self.auth.name, self.auth_value) catch return error.OutOfMemory;
        }
        w.writer.writeAll(target[hash..]) catch return error.OutOfMemory;
        return w.toOwnedSlice();
    }

    /// Builds `base_url + path` with `query`, and the key for `query` auth.
    /// The caller frees the result with `allocator`. It may hold the
    /// secret: never log it.
    pub fn url(self: *const Lease, allocator: Allocator, path: []const u8, query: []const QueryParam) Allocator.Error![]u8 {
        var w: std.Io.Writer.Allocating = .init(allocator);
        errdefer w.deinit();
        buildUrl(&w.writer, self, path, query) catch return error.OutOfMemory;
        return w.toOwnedSlice();
    }

    /// Prints the lease with the secret hidden.
    pub fn format(self: *const Lease, w: *std.Io.Writer) std.Io.Writer.Error!void {
        try w.print("Lease{{ id = {s}, key_id = {d}, service = {s}, auth = {t} {s}, secret = ***, expires_at = {s}, exclusive = {}, reported = {} }}", .{
            self.lease_id, self.key_id, self.service, self.auth.kind, self.auth.name, self.expires_at, self.exclusive, self.reported,
        });
    }

    fn leasePath(self: *const Lease, allocator: Allocator, action: []const u8) Allocator.Error![]u8 {
        var w: std.Io.Writer.Allocating = .init(allocator);
        errdefer w.deinit();
        (struct {
            fn run(out: *std.Io.Writer, id: []const u8, act: []const u8) std.Io.Writer.Error!void {
                try out.writeAll("/api/v1/leases/");
                try errors.percentEncode(out, id);
                try out.writeByte('/');
                try out.writeAll(act);
            }
        }).run(&w.writer, self.lease_id, action) catch return error.OutOfMemory;
        return w.toOwnedSlice();
    }
};

fn buildUrl(w: *std.Io.Writer, lease: *const Lease, path: []const u8, query: []const QueryParam) std.Io.Writer.Error!void {
    try w.writeAll(lease.base_url);
    if (path.len > 0) {
        if (path[0] != '/') try w.writeByte('/');
        try errors.percentEncodePath(w, path);
    }
    var first = true;
    for (query) |q| {
        try w.writeByte(if (first) '?' else '&');
        first = false;
        try writeParam(w, q.name, q.value);
    }
    if (lease.auth.kind == .query) {
        try w.writeByte(if (first) '?' else '&');
        try writeParam(w, lease.auth.name, lease.auth_value);
    }
}

fn writeParam(w: *std.Io.Writer, name: []const u8, value: []const u8) std.Io.Writer.Error!void {
    try errors.percentEncode(w, name);
    try w.writeByte('=');
    try errors.percentEncode(w, value);
}

/// Writes the report JSON. The message is scrubbed of the secret and the
/// token, then cut to `max_report_message` bytes on a character boundary.
pub fn writeReportBody(w: *std.Io.Writer, options: ReportOptions, secret: []const u8, token: []const u8) std.Io.Writer.Error!void {
    var js: std.json.Stringify = .{ .writer = w };
    try js.beginObject();
    try js.objectField("outcome");
    try js.write(@tagName(options.outcome));
    try js.objectField("rotate");
    try js.write(options.rotate);
    if (options.status_code) |c| {
        try js.objectField("status_code");
        try js.write(c);
    }
    if (options.retry_after_sec) |r| {
        try js.objectField("retry_after_sec");
        try js.write(r);
    }
    if (options.message) |m| if (m.len > 0) {
        var buf: [max_report_message * 2]u8 = undefined;
        var mw: std.Io.Writer = .fixed(&buf);
        errors.writeScrubbed(&mw, m, &.{ secret, token }, "[key]") catch {};
        const scrubbed = mw.buffered();
        const n = errors.utf8Prefix(scrubbed, max_report_message);
        try js.objectField("message");
        try js.write(scrubbed[0..n]);
    };
    try js.endObject();
}

const AuthJson = struct {
    type: []const u8 = "",
    name: []const u8 = "",
    prefix: []const u8 = "",
};

const LeaseJson = struct {
    lease_id: []const u8 = "",
    key_id: i64 = 0,
    service: []const u8 = "",
    secret: []const u8 = "",
    auth: AuthJson = .{},
    base_url: []const u8 = "",
    expires_at: []const u8 = "",
    exclusive: bool = false,
};

const ReportJson = struct {
    ok: bool = false,
    next: ?LeaseJson = null,
};

const RenewJson = struct {
    ok: bool = false,
    expires_at: []const u8 = "",
};
