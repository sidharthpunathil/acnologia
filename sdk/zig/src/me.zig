//! The shape of `GET /api/v1/me`. Unknown fields are ignored, so a newer
//! server does not break an older SDK.

pub const App = struct {
    id: i64 = 0,
    name: []const u8 = "",
};

/// Null means no limit.
pub const Limits = struct {
    daily_requests: ?i64 = null,
    monthly_requests: ?i64 = null,
    daily_budget: ?f64 = null,
    monthly_budget: ?f64 = null,
};

pub const LimitHit = struct {
    /// `daily_requests`, `monthly_requests`, `daily_budget` or `monthly_budget`.
    limit: []const u8 = "",
    /// RFC 3339.
    reset_at: []const u8 = "",
};

pub const Usage = struct {
    today_requests: i64 = 0,
    today_cost: f64 = 0,
    month_requests: i64 = 0,
    month_cost: f64 = 0,
    last_used_at: ?[]const u8 = null,
    limit_hit: ?LimitHit = null,
};

pub const Service = struct {
    slug: []const u8 = "",
    name: []const u8 = "",
    provider: []const u8 = "",
    category: []const u8 = "",
    /// Keys free right now for a share's token; 1 or 0 for an ordinary app.
    available: i64 = 0,
    logo_url: ?[]const u8 = null,
};

pub const Owner = struct {
    name: []const u8 = "",
    id: []const u8 = "",
};

/// Set only for a share's token.
pub const Share = struct {
    id: []const u8 = "",
    name: []const u8 = "",
    owner: Owner = .{},
    owner_name: []const u8 = "",
    owner_id: []const u8 = "",
    recipient: []const u8 = "",
    label: []const u8 = "",
    /// `active`, `paused`, `expired` or `revoked`.
    status: []const u8 = "",
    mode: []const u8 = "",
    ends_at: ?[]const u8 = null,
};

pub const Me = struct {
    app: App = .{},
    limits: Limits = .{},
    usage: Usage = .{},
    limit_hit: ?LimitHit = null,
    services: []const Service = &.{},
    share: ?Share = null,
};
