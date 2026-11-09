//! The shapes of the catalog routes: `GET /api/v1/services`, the usage
//! guide, `GET /api/v1/capabilities` and `GET /api/v1/proxy`. Unknown fields
//! are ignored, so a newer server does not break an older SDK.

const std = @import("std");

/// Where a service expects its key.
pub const ServiceAuth = struct {
    /// `header`, `bearer` or `query`.
    type: []const u8 = "",
    /// The header or query parameter name.
    name: []const u8 = "",
    prefix: []const u8 = "",
};

/// One entry of `GET /api/v1/services`.
pub const Service = struct {
    slug: []const u8 = "",
    name: []const u8 = "",
    provider: []const u8 = "",
    category: []const u8 = "",
    /// Whether a key is free right now.
    available: bool = false,
    base_url: []const u8 = "",
    auth: ServiceAuth = .{},
    /// Where the usage guide lives, `.../api/v1/services/<slug>/guide`.
    guide_url: []const u8 = "",
};

/// `GET /api/v1/services/<slug>/guide`.
pub const Guide = struct {
    /// The guide as Markdown.
    markdown: []const u8 = "",
    /// True when Acnologia wrote the guide from the provider's spec.
    generated: bool = false,
    /// The structured guide, kept as JSON.
    guide: std.json.Value = .null,
};

pub const CapabilityRoute = struct {
    service: []const u8 = "",
    /// True when the route maps the answer into the capability's shape.
    mapped: bool = false,
};

/// One entry of `GET /api/v1/capabilities`.
pub const Capability = struct {
    slug: []const u8 = "",
    name: []const u8 = "",
    description: []const u8 = "",
    strategy: []const u8 = "",
    /// The params the capability takes, kept as JSON.
    params: std.json.Value = .null,
    routes: []const CapabilityRoute = &.{},
    /// Where to call it, `.../proxy/<slug>`.
    url: []const u8 = "",
};

/// A host the forward proxy routes to a service.
pub const ForwardProxyHost = struct {
    host: []const u8 = "",
    service: []const u8 = "",
    name: []const u8 = "",
    path: []const u8 = "",
};

/// `GET /api/v1/proxy`: how to use Acnologia as the app's HTTPS_PROXY.
///
/// Holds no token: `proxy_url_with_token` and `env` carry the placeholder
/// `<app token>`. `Client.forwardProxyURL` and `Client.forwardProxyEnv`
/// fill the real token in.
pub const ForwardProxy = struct {
    proxy_url: []const u8 = "",
    proxy_url_with_token: []const u8 = "",
    ca_url: []const u8 = "",
    ca_fingerprint: []const u8 = "",
    /// Whether HTTPS to hosts that are no service is tunnelled through.
    passthrough: bool = false,
    hosts: []const ForwardProxyHost = &.{},
    /// The environment the server suggests, with the token as a placeholder.
    env: std.json.ArrayHashMap([]const u8) = .{},
    /// The request header that picks a service, `X-Acnologia-Service`.
    service_header: []const u8 = "",

    /// Prints the setup. Holds no token, so nothing is hidden.
    pub fn format(self: *const ForwardProxy, w: *std.Io.Writer) std.Io.Writer.Error!void {
        try w.print("acnologia.ForwardProxy{{ proxy_url = {s}, ca_url = {s}, ca_fingerprint = {s}, passthrough = {}, hosts = {d} }}", .{
            self.proxy_url, self.ca_url, self.ca_fingerprint, self.passthrough, self.hosts.len,
        });
    }
};

/// One route a capability call tried, from `X-Acnologia-Tried`.
pub const Tried = struct {
    service: []const u8,
    /// The status that route answered, or 0 when it could not be read.
    status: u16,
};

/// Parses `X-Acnologia-Tried` (`svc=200, other=503`) into a slice
/// allocated with `allocator`. Service names point into `value`. Entries without `=` keep status 0.
pub fn parseTried(allocator: std.mem.Allocator, value: []const u8) std.mem.Allocator.Error![]const Tried {
    var list: std.ArrayList(Tried) = .empty;
    errdefer list.deinit(allocator);
    var it = std.mem.splitScalar(u8, value, ',');
    while (it.next()) |part| {
        const p = std.mem.trim(u8, part, " \t");
        if (p.len == 0) continue;
        if (std.mem.findScalarLast(u8, p, '=')) |eq| {
            const status = std.fmt.parseInt(u16, std.mem.trim(u8, p[eq + 1 ..], " "), 10) catch 0;
            try list.append(allocator, .{ .service = std.mem.trim(u8, p[0..eq], " "), .status = status });
        } else {
            try list.append(allocator, .{ .service = p, .status = 0 });
        }
    }
    return list.toOwnedSlice(allocator);
}

test "parseTried reads service=status pairs" {
    const t = try parseTried(std.testing.allocator, "serpapi=429, tavily=200,  ,odd");
    defer std.testing.allocator.free(t);
    try std.testing.expectEqual(@as(usize, 3), t.len);
    try std.testing.expectEqualStrings("serpapi", t[0].service);
    try std.testing.expectEqual(@as(u16, 429), t[0].status);
    try std.testing.expectEqualStrings("tavily", t[1].service);
    try std.testing.expectEqual(@as(u16, 200), t[1].status);
    try std.testing.expectEqualStrings("odd", t[2].service);
    try std.testing.expectEqual(@as(u16, 0), t[2].status);
}
