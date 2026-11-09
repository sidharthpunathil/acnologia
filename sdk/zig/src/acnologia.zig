//! Zig client for Acnologia, a self-hosted API key server.
//!
//! Proxy mode sends a request through `/proxy/<service>/<path>`, so the key
//! never reaches your process. Lease mode borrows the raw key for a while;
//! report how it did, and renew it for a long job. `me` describes the token,
//! `services`, `guide` and `capabilities` describe what it may call, and
//! `forwardProxy` sets an app up to use Acnologia as its HTTPS_PROXY.
//!
//! Memory rules, in short: the Client copies its config and frees it in
//! `deinit`. Every value a call returns is owned by the caller and has a
//! `deinit` (ProxyResponse, Lease, a std.json.Parsed) or is a plain slice
//! freed with the allocator you passed. Details of a failed call go into an
//! optional `Diagnostic`, which needs no allocator.

const std = @import("std");
const Io = std.Io;
const http = std.http;
const Allocator = std.mem.Allocator;

const errors = @import("errors.zig");
const lease_mod = @import("lease.zig");
const transport = @import("transport.zig");
pub const me_types = @import("me.zig");
pub const catalog = @import("catalog.zig");

pub const Error = errors.Error;
pub const ApiError = errors.ApiError;
pub const ClientError = errors.ClientError;
pub const Diagnostic = errors.Diagnostic;
pub const isNoKey = errors.isNoKey;
pub const isShareUnavailable = errors.isShareUnavailable;
pub const errorFor = errors.errorFor;

pub const Lease = lease_mod.Lease;
pub const Auth = lease_mod.Auth;
pub const AuthKind = lease_mod.AuthKind;
pub const Outcome = lease_mod.Outcome;
pub const ReportOptions = lease_mod.ReportOptions;
pub const RenewOptions = lease_mod.RenewOptions;
pub const max_report_message = lease_mod.max_report_message;

pub const Me = me_types.Me;
pub const Service = catalog.Service;
pub const ServiceAuth = catalog.ServiceAuth;
pub const Guide = catalog.Guide;
pub const Capability = catalog.Capability;
pub const CapabilityRoute = catalog.CapabilityRoute;
pub const ForwardProxy = catalog.ForwardProxy;
pub const ForwardProxyHost = catalog.ForwardProxyHost;
pub const Tried = catalog.Tried;

/// Where `saveForwardProxyCA` and `forwardProxyEnv` put the CA by default.
pub const default_ca_path = "acnologia-ca.pem";

pub const version = "0.2.0";
pub const user_agent = "acnologia-zig/" ++ version;

/// Default timeout for API calls (lease, report, renew, me): 75 seconds,
/// longer than the 60 second maximum lease wait.
pub const default_timeout_ms: u32 = 75_000;

/// A query parameter. Names and values are percent encoded for you.
pub const QueryParam = struct {
    name: []const u8,
    value: []const u8,
};

pub const Options = struct {
    /// Where Acnologia runs, such as `http://127.0.0.1:8080`. Falls back to
    /// `ACNOLOGIA_URL` in `environ`.
    base_url: ?[]const u8 = null,
    /// App token, `acn_...`. Falls back to `ACNOLOGIA_TOKEN` in `environ`.
    token: ?[]const u8 = null,
    /// Sent as `X-Acnologia-Access` on every call. Falls back to
    /// `ACNOLOGIA_ACCESS_PASSWORD` in `environ`, but only when the server it
    /// belongs to (`ACNOLOGIA_ACCESS_URL`, or else `ACNOLOGIA_URL`) has the
    /// same origin as `base_url`. Never sent otherwise.
    access_password: ?[]const u8 = null,
    /// Environment to read the fallbacks from, such as `init.environ_map`
    /// in `main(init: std.process.Init)`. Null reads nothing.
    environ: ?*const std.process.Environ.Map = null,
    /// Timeout for API calls in milliseconds. 0 disables it. A lease with
    /// `wait_sec` gets at least `wait_sec` plus 10 seconds.
    timeout_ms: u32 = default_timeout_ms,
    /// Timeout for proxy calls in milliseconds. 0 (the default) waits as
    /// long as the provider takes. `ProxyOptions.timeout_ms` overrides it.
    proxy_timeout_ms: u32 = 0,
    /// Largest answer body read into memory, in bytes.
    max_body_bytes: usize = 32 * 1024 * 1024,
};

pub const InitError = error{
    /// No base URL was given and `ACNOLOGIA_URL` is unset or empty.
    MissingBaseUrl,
    /// No token was given and `ACNOLOGIA_TOKEN` is unset or empty.
    MissingToken,
    /// The base URL is not an http or https URL.
    InvalidUrl,
    /// The token or access password holds a line break.
    InvalidArgument,
    OutOfMemory,
};

pub const ProxyOptions = struct {
    /// Query parameters, appended in order.
    query: []const QueryParam = &.{},
    /// Extra request headers. `Authorization`, `Host`, `Connection`,
    /// `Content-Length`, `Transfer-Encoding` and the token headers are
    /// dropped: the client sets those. A `Content-Type` here is used as is.
    headers: []const http.Header = &.{},
    /// Request body. Refused (`error.InvalidArgument`) on GET, HEAD, DELETE
    /// and OPTIONS. POST, PUT and PATCH without one send an empty body.
    body: ?[]const u8 = null,
    /// Sent as `Idempotency-Key`. A repeat with the same key within the
    /// server's window replays the first answer (`replayed` is then true).
    idempotency_key: ?[]const u8 = null,
    /// Sends `Cache-Control: no-cache`, so Acnologia skips its answer cache.
    no_cache: bool = false,
    /// Overrides `Options.proxy_timeout_ms`. 0 disables the timeout.
    timeout_ms: ?u32 = null,
    /// When false (the default), an error Acnologia itself answers with
    /// (one without `X-Acnologia-Service`, such as `no_key_available` or
    /// `upstream_unreachable`) is returned as a Zig error. When true, every
    /// answer is returned as a ProxyResponse.
    raw: bool = false,
    diag: ?*Diagnostic = null,
};

/// The `X-Acnologia-*` headers of a proxied answer. Slices point into the
/// ProxyResponse that holds this.
pub const ProxyInfo = struct {
    /// `X-Acnologia-Key-Id`: the key that served the call. Null for a
    /// share's or a person's token.
    key_id: ?i64 = null,
    /// `X-Acnologia-Attempts`: how many keys were tried.
    attempts: ?u32 = null,
    /// `X-Acnologia-Service`: the service that answered (useful for a group).
    service: ?[]const u8 = null,
    /// `X-Acnologia-Cache`, such as `hit` or `miss`, when caching applies.
    cache: ?[]const u8 = null,
    /// `X-Acnologia-Share`: `revoked`, `paused` or `expired` for a share token.
    share: ?[]const u8 = null,
    /// `Idempotent-Replayed: true`: this answer is a replay.
    replayed: bool = false,
    /// `X-Acnologia-Capability`: the capability that ran, for a capability call.
    capability: ?[]const u8 = null,
    /// `X-Acnologia-Tried`: every route a capability call tried, in order.
    tried: []const Tried = &.{},
};

/// A proxied answer, returned as the provider sent it, whatever its status.
///
/// Memory: owns its headers and body through its own arena, created from
/// the allocator passed to `Client.proxy`. Call `deinit` once.
pub const ProxyResponse = struct {
    arena: std.heap.ArenaAllocator,
    status: u16,
    headers: []const http.Header,
    body: []const u8,
    acnologia: ProxyInfo,

    /// Finds a header by name, ignoring case.
    pub fn header(self: *const ProxyResponse, name: []const u8) ?[]const u8 {
        return errors.headerValue(self.headers, name);
    }

    /// Parses the body as JSON into `T`. The result is owned by the caller:
    /// call its `deinit`.
    pub fn json(self: *const ProxyResponse, comptime T: type, allocator: Allocator) !std.json.Parsed(T) {
        return std.json.parseFromSlice(T, allocator, self.body, .{ .ignore_unknown_fields = true, .allocate = .alloc_always });
    }

    pub fn deinit(self: *ProxyResponse) void {
        self.arena.deinit();
        self.* = undefined;
    }
};

pub const LeaseOptions = struct {
    /// Key ids the server must not hand out, such as ones you already tried.
    exclude_key_ids: []const i64 = &.{},
    /// Lease lifetime in seconds. Null uses the server default.
    ttl_sec: ?u32 = null,
    /// Seconds (0 to 60) the server may wait for a free slot when every
    /// usable key is busy. 0 fails fast with `error.AllKeysBusy`.
    wait_sec: u32 = 0,
    /// Keep the key from every other caller until you report or it expires.
    exclusive: bool = false,
    diag: ?*Diagnostic = null,
};

pub const MeOptions = struct {
    diag: ?*Diagnostic = null,
};

/// Options of the read calls (services, guide, capabilities, forward proxy).
pub const CallOptions = struct {
    diag: ?*Diagnostic = null,
};

pub const CapabilityOptions = struct {
    /// Use only this provider's route: `?_service=<slug>`.
    service: ?[]const u8 = null,
    /// Return the provider's own body, not the mapped shape: `?_raw=1`.
    raw: bool = false,
    /// Extra request headers, as in `ProxyOptions.headers`.
    headers: []const http.Header = &.{},
    /// Sent as `Idempotency-Key`.
    idempotency_key: ?[]const u8 = null,
    /// Sends `Cache-Control: no-cache`.
    no_cache: bool = false,
    /// Overrides `Options.proxy_timeout_ms`. 0 disables the timeout.
    timeout_ms: ?u32 = null,
    /// Like `ProxyOptions.raw`: when true, Acnologia's own errors (such as
    /// `bad_params` or `no_route_worked`) come back as a ProxyResponse
    /// instead of a Zig error.
    raw_errors: bool = false,
    diag: ?*Diagnostic = null,
};

/// Largest `wait_sec` the server accepts.
pub const max_wait_sec: u32 = 60;

/// A client for one Acnologia server and one app token.
///
/// Create it with `init`, keep it in one place (Leases point at it), and
/// call `deinit` when done. Calls are safe from one thread at a time per
/// Client; the connection pool underneath is thread safe.
pub const Client = struct {
    /// Used for the Client's own copies, connections and scratch memory.
    gpa: Allocator,
    io: Io,
    http: http.Client,
    base_url: []u8,
    token: []u8,
    access_password: ?[]u8,
    authorization: []u8,
    timeout_ms: u32,
    proxy_timeout_ms: u32,
    max_body_bytes: usize,

    /// Copies the config. `gpa` must be thread safe if the Io runs tasks on
    /// other threads (the default ones do), since the request and its
    /// timeout run as concurrent tasks.
    pub fn init(gpa: Allocator, io: Io, options: Options) InitError!Client {
        const env_url = envGet(options.environ, "ACNOLOGIA_URL");
        const base_in = nonEmpty(options.base_url) orelse env_url orelse return error.MissingBaseUrl;
        const token_in = nonEmpty(options.token) orelse envGet(options.environ, "ACNOLOGIA_TOKEN") orelse return error.MissingToken;

        const base_trimmed = std.mem.trimEnd(u8, std.mem.trim(u8, base_in, " \t\r\n"), "/");
        const base_uri = std.Uri.parse(base_trimmed) catch return error.InvalidUrl;
        if (!std.ascii.eqlIgnoreCase(base_uri.scheme, "http") and !std.ascii.eqlIgnoreCase(base_uri.scheme, "https"))
            return error.InvalidUrl;
        if (base_uri.host == null) return error.InvalidUrl;

        const access_in: ?[]const u8 = nonEmpty(options.access_password) orelse blk: {
            const pw = envGet(options.environ, "ACNOLOGIA_ACCESS_PASSWORD") orelse break :blk null;
            const owner = envGet(options.environ, "ACNOLOGIA_ACCESS_URL") orelse env_url orelse break :blk null;
            break :blk if (sameOrigin(base_trimmed, owner)) pw else null;
        };

        if (hasLineBreak(token_in)) return error.InvalidArgument;
        if (access_in) |a| if (hasLineBreak(a)) return error.InvalidArgument;

        const base_url = try gpa.dupe(u8, base_trimmed);
        errdefer gpa.free(base_url);
        const token = try gpa.dupe(u8, token_in);
        errdefer freeSecret(gpa, token);
        const access_password: ?[]u8 = if (access_in) |a| try gpa.dupe(u8, a) else null;
        errdefer if (access_password) |a| freeSecret(gpa, a);
        const authorization = try std.mem.concat(gpa, u8, &.{ "Bearer ", token_in });

        return .{
            .gpa = gpa,
            .io = io,
            .http = .{ .allocator = gpa, .io = io },
            .base_url = base_url,
            .token = token,
            .access_password = access_password,
            .authorization = authorization,
            .timeout_ms = options.timeout_ms,
            .proxy_timeout_ms = options.proxy_timeout_ms,
            .max_body_bytes = options.max_body_bytes,
        };
    }

    /// Closes connections, wipes the token and frees the Client's copies.
    pub fn deinit(self: *Client) void {
        self.http.deinit();
        freeSecret(self.gpa, self.authorization);
        freeSecret(self.gpa, self.token);
        if (self.access_password) |a| freeSecret(self.gpa, a);
        self.gpa.free(self.base_url);
        self.* = undefined;
    }

    /// Prints the client with the token hidden.
    pub fn format(self: *const Client, w: *Io.Writer) Io.Writer.Error!void {
        try w.print("acnologia.Client{{ base_url = {s}, token = ***, access_password = {s} }}", .{
            self.base_url, if (self.access_password != null) "***" else "none",
        });
    }

    /// Sends any method to `/proxy/<service>/<path>`. Acnologia attaches a
    /// key, rotates on rate limits and returns the provider's answer.
    ///
    /// The result is allocated with `allocator` and owned by the caller:
    /// call `deinit` on it. `path` is percent encoded where needed (a `%`
    /// is kept, so an encoded path passes through).
    pub fn proxy(
        self: *Client,
        allocator: Allocator,
        method: http.Method,
        service: []const u8,
        path: []const u8,
        options: ProxyOptions,
    ) Error!ProxyResponse {
        return self.proxyTo(allocator, method, service, path, true, options);
    }

    /// Calls a capability: `POST /proxy/<slug>` with `params_json` (a JSON
    /// object, such as `{"query":"coffee"}`) as the body. Null sends `{}`.
    /// Acnologia picks a provider, translates the params and moves to the
    /// next route on failure; `res.acnologia.capability`, `.service` and
    /// `.tried` say what happened. Owned by the caller: call `deinit`.
    pub fn capability(
        self: *Client,
        allocator: Allocator,
        slug: []const u8,
        params_json: ?[]const u8,
        options: CapabilityOptions,
    ) Error!ProxyResponse {
        var query_buf: [2]QueryParam = undefined;
        var n: usize = 0;
        if (options.service) |sv| {
            query_buf[n] = .{ .name = "_service", .value = sv };
            n += 1;
        }
        if (options.raw) {
            query_buf[n] = .{ .name = "_raw", .value = "1" };
            n += 1;
        }
        var headers: std.ArrayList(http.Header) = .empty;
        defer headers.deinit(self.gpa);
        try headers.append(self.gpa, .{ .name = "Content-Type", .value = "application/json" });
        try headers.appendSlice(self.gpa, options.headers);
        return self.proxyTo(allocator, .POST, slug, "", false, .{
            .query = query_buf[0..n],
            .headers = headers.items,
            .body = params_json orelse "{}",
            .idempotency_key = options.idempotency_key,
            .no_cache = options.no_cache,
            .timeout_ms = options.timeout_ms,
            .raw = options.raw_errors,
            .diag = options.diag,
        });
    }

    fn proxyTo(
        self: *Client,
        allocator: Allocator,
        method: http.Method,
        service: []const u8,
        path: []const u8,
        trailing_slash: bool,
        options: ProxyOptions,
    ) Error!ProxyResponse {
        if (options.body != null and !method.requestHasBody()) return self.fail(options.diag, error.InvalidArgument, "a body needs POST, PUT or PATCH");
        if (service.len == 0) return self.fail(options.diag, error.InvalidArgument, "service is empty");

        var arena: std.heap.ArenaAllocator = .init(allocator);
        errdefer arena.deinit();
        const a = arena.allocator();

        var url: Io.Writer.Allocating = .init(self.gpa);
        defer url.deinit();
        writeProxyUrl(&url.writer, self.base_url, service, path, trailing_slash, options.query) catch return error.OutOfMemory;

        var headers: std.ArrayList(http.Header) = .empty;
        defer headers.deinit(self.gpa);
        var content_type: ?[]const u8 = null;
        for (options.headers) |h| {
            if (hasLineBreak(h.name) or hasLineBreak(h.value) or h.name.len == 0 or std.mem.findScalar(u8, h.name, ':') != null)
                return self.fail(options.diag, error.InvalidArgument, "a header name or value is not valid");
            if (std.ascii.eqlIgnoreCase(h.name, "content-type")) {
                content_type = h.value;
                continue;
            }
            if (isReservedHeader(h.name)) continue;
            try headers.append(self.gpa, h);
        }
        if (options.idempotency_key) |k| {
            if (hasLineBreak(k)) return self.fail(options.diag, error.InvalidArgument, "the idempotency key holds a line break");
            try headers.append(self.gpa, .{ .name = "Idempotency-Key", .value = k });
        }
        if (options.no_cache) try headers.append(self.gpa, .{ .name = "Cache-Control", .value = "no-cache" });
        try self.appendClientHeaders(&headers);

        const req: transport.Request = .{
            .method = method,
            .url = url.written(),
            .authorization = self.authorization,
            .user_agent = user_agent,
            .content_type = content_type,
            .headers = headers.items,
            .body = options.body,
            .timeout_ms = options.timeout_ms orelse self.proxy_timeout_ms,
            .max_body_bytes = self.max_body_bytes,
        };
        const answer = try self.exchange(a, &req, options.diag);

        if (!options.raw and answer.status >= 400 and errors.headerValue(answer.headers, "x-acnologia-service") == null) {
            var scratch: [32 * 1024]u8 = undefined;
            const env = errors.parseEnvelope(answer.body, &scratch);
            if (errors.isProxyCode(env.code)) {
                // The errdefer above frees the arena.
                return errors.fromAnswer(answer.status, answer.headers, answer.body, self.secrets(""), options.diag);
            }
        }

        return .{
            .arena = arena,
            .status = answer.status,
            .headers = answer.headers,
            .body = answer.body,
            .acnologia = try proxyInfo(a, answer.headers),
        };
    }

    /// Borrows a key for `service`. The Lease is allocated with `allocator`
    /// and owned by the caller: report it, then call its `deinit`.
    ///
    /// `error.NoKeyAvailable` means the pool is drained or cooling down;
    /// `error.AllKeysBusy` means every usable key is busy and `wait_sec`
    /// ran out. `diag.retry_after_sec` says how long to wait.
    pub fn lease(self: *Client, allocator: Allocator, service: []const u8, options: LeaseOptions) Error!Lease {
        if (options.wait_sec > max_wait_sec) return self.fail(options.diag, error.InvalidArgument, "wait_sec must be between 0 and 60");
        var body: Io.Writer.Allocating = .init(self.gpa);
        defer body.deinit();
        writeLeaseBody(&body.writer, service, options) catch return error.OutOfMemory;

        var timeout: u64 = self.timeout_ms;
        if (options.wait_sec > 0 and timeout != 0) timeout = @max(timeout, (@as(u64, options.wait_sec) + 10) * 1000);

        var scratch: std.heap.ArenaAllocator = .init(self.gpa);
        defer scratch.deinit();
        const answer = try self.apiCallTimeout(scratch.allocator(), .POST, "/api/v1/lease", body.written(), timeout, "", options.diag);
        defer std.crypto.secureZero(u8, @constCast(answer));
        return Lease.fromJson(self, allocator, answer);
    }

    /// Reads `GET /api/v1/me`: the token's app, limits, usage, services and
    /// share. The result is allocated with `allocator` and owned by the
    /// caller: call its `deinit`.
    pub fn me(self: *Client, allocator: Allocator, options: MeOptions) Error!std.json.Parsed(Me) {
        var scratch: std.heap.ArenaAllocator = .init(self.gpa);
        defer scratch.deinit();
        const answer = try self.apiCall(scratch.allocator(), .GET, "/api/v1/me", null, 0, "", options.diag);
        return std.json.parseFromSlice(Me, allocator, answer, .{
            .ignore_unknown_fields = true,
            .allocate = .alloc_always,
        }) catch |err| switch (err) {
            error.OutOfMemory => error.OutOfMemory,
            else => self.fail(options.diag, error.BadResponse, "the me answer could not be read"),
        };
    }

    /// Reads `GET /api/v1/services`: the services this token may use, with
    /// their base URL, auth and guide URL. Owned by the caller: call `deinit`.
    pub fn services(self: *Client, allocator: Allocator, options: CallOptions) Error!std.json.Parsed([]const Service) {
        const Wrap = struct { services: []const Service = &.{} };
        const p = try self.getJson(Wrap, allocator, "/api/v1/services", "the services answer could not be read", options.diag);
        return .{ .arena = p.arena, .value = p.value.services };
    }

    /// Reads `GET /api/v1/services/<slug>/guide`: the usage guide as
    /// Markdown plus the structured guide as JSON. Owned by the caller:
    /// call `deinit`.
    pub fn guide(self: *Client, allocator: Allocator, slug: []const u8, options: CallOptions) Error!std.json.Parsed(Guide) {
        var path: Io.Writer.Allocating = .init(self.gpa);
        defer path.deinit();
        writeGuidePath(&path.writer, slug) catch return error.OutOfMemory;
        return self.getJson(Guide, allocator, path.written(), "the guide answer could not be read", options.diag);
    }

    /// The usage guide as raw Markdown (`Accept: text/markdown`). The slice
    /// is allocated with `allocator` and owned by the caller.
    pub fn guideMarkdown(self: *Client, allocator: Allocator, slug: []const u8, options: CallOptions) Error![]u8 {
        var scratch: std.heap.ArenaAllocator = .init(self.gpa);
        defer scratch.deinit();
        var path: Io.Writer.Allocating = .init(scratch.allocator());
        writeGuidePath(&path.writer, slug) catch return error.OutOfMemory;
        const body = try self.apiRequest(scratch.allocator(), path.written(), .{ .accept = "text/markdown" }, options.diag);
        return allocator.dupe(u8, body);
    }

    /// Reads `GET /api/v1/capabilities`: the capabilities this token may
    /// call with `capability`. Owned by the caller: call `deinit`.
    pub fn capabilities(self: *Client, allocator: Allocator, options: CallOptions) Error!std.json.Parsed([]const Capability) {
        const Wrap = struct { capabilities: []const Capability = &.{} };
        const p = try self.getJson(Wrap, allocator, "/api/v1/capabilities", "the capabilities answer could not be read", options.diag);
        return .{ .arena = p.arena, .value = p.value.capabilities };
    }

    /// Reads `GET /api/v1/proxy`: how to use Acnologia as the app's
    /// HTTPS_PROXY. The answer holds no token. Owned by the caller: call
    /// `deinit`.
    pub fn forwardProxy(self: *Client, allocator: Allocator, options: CallOptions) Error!std.json.Parsed(ForwardProxy) {
        return self.getJson(ForwardProxy, allocator, "/api/v1/proxy", "the forward proxy answer could not be read", options.diag);
    }

    /// Downloads the forward proxy's CA certificate (PEM) from
    /// `GET /api/v1/proxy/ca.pem`. The route is public, so no token is sent.
    /// The slice is allocated with `allocator` and owned by the caller.
    pub fn forwardProxyCA(self: *Client, allocator: Allocator, options: CallOptions) Error![]u8 {
        var scratch: std.heap.ArenaAllocator = .init(self.gpa);
        defer scratch.deinit();
        const body = try self.apiRequest(scratch.allocator(), "/api/v1/proxy/ca.pem", .{
            .accept = "application/x-pem-file",
            .send_token = false,
        }, options.diag);
        return allocator.dupe(u8, body);
    }

    /// Downloads the CA certificate and writes it to `path` (relative to
    /// the current directory), or to `default_ca_path` when null.
    pub fn saveForwardProxyCA(self: *Client, path: ?[]const u8, options: CallOptions) (Error || Io.Dir.WriteFileError)!void {
        const pem = try self.forwardProxyCA(self.gpa, options);
        defer self.gpa.free(pem);
        try Io.Dir.cwd().writeFile(self.io, .{ .sub_path = path orelse default_ca_path, .data = pem });
    }

    /// The forward proxy URL with this client's token as the password,
    /// percent encoded: `http://app:acn_...@host:8888`. Calls `forwardProxy`.
    ///
    /// The result CONTAINS THE TOKEN. It is allocated with `allocator` and
    /// owned by the caller: wipe it (`std.crypto.secureZero`) and free it.
    pub fn forwardProxyURL(self: *Client, allocator: Allocator, options: CallOptions) Error![]u8 {
        var setup = try self.forwardProxy(self.gpa, options);
        defer setup.deinit();
        return self.proxyUrlWithToken(allocator, setup.value.proxy_url, options.diag);
    }

    /// The environment that sends an app's HTTP through the forward proxy:
    /// `HTTPS_PROXY` and `HTTP_PROXY` (the `forwardProxyURL`),
    /// `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`
    /// and `SSL_CERT_FILE` set to `ca_path` (`default_ca_path` when null).
    /// Save the CA there with `saveForwardProxyCA`. Calls `forwardProxy`.
    ///
    /// The map CONTAINS THE TOKEN. It is allocated with `allocator` and
    /// owned by the caller: call its `deinit`. Pass it as a child process's
    /// `environ_map`, or copy entries into one.
    pub fn forwardProxyEnv(self: *Client, allocator: Allocator, ca_path: ?[]const u8, options: CallOptions) Error!std.process.Environ.Map {
        const url = try self.forwardProxyURL(self.gpa, options);
        defer freeSecret(self.gpa, url);
        const ca = ca_path orelse default_ca_path;
        var env = std.process.Environ.Map.init(allocator);
        errdefer env.deinit();
        try env.put("HTTPS_PROXY", url);
        try env.put("HTTP_PROXY", url);
        try env.put("NODE_USE_ENV_PROXY", "1");
        try env.put("NODE_EXTRA_CA_CERTS", ca);
        try env.put("REQUESTS_CA_BUNDLE", ca);
        try env.put("SSL_CERT_FILE", ca);
        return env;
    }

    fn proxyUrlWithToken(self: *Client, allocator: Allocator, proxy_url: []const u8, diag: ?*Diagnostic) Error![]u8 {
        const sep = std.mem.find(u8, proxy_url, "://") orelse
            return self.fail(diag, error.BadResponse, "the forward proxy URL could not be read");
        var rest = proxy_url[sep + 3 ..];
        // Drop any user info the server put there; the token goes in its place.
        const host_end = std.mem.findAny(u8, rest, "/?#") orelse rest.len;
        if (std.mem.findScalarLast(u8, rest[0..host_end], '@')) |at| rest = rest[at + 1 ..];
        var out: Io.Writer.Allocating = .init(allocator);
        errdefer {
            std.crypto.secureZero(u8, out.writer.buffer);
            out.deinit();
        }
        writeProxyUrlWithToken(&out.writer, proxy_url[0 .. sep + 3], self.token, rest) catch return error.OutOfMemory;
        return out.toOwnedSlice() catch error.OutOfMemory;
    }

    fn getJson(self: *Client, comptime T: type, allocator: Allocator, path: []const u8, what: []const u8, diag: ?*Diagnostic) Error!std.json.Parsed(T) {
        var scratch: std.heap.ArenaAllocator = .init(self.gpa);
        defer scratch.deinit();
        const answer = try self.apiCall(scratch.allocator(), .GET, path, null, 0, "", diag);
        return std.json.parseFromSlice(T, allocator, answer, .{
            .ignore_unknown_fields = true,
            .allocate = .alloc_always,
        }) catch |err| switch (err) {
            error.OutOfMemory => error.OutOfMemory,
            else => self.fail(diag, error.BadResponse, what),
        };
    }

    const RequestExtra = struct {
        accept: []const u8 = "application/json",
        send_token: bool = true,
    };

    /// A GET with a chosen Accept, and optionally no token.
    fn apiRequest(self: *Client, arena: Allocator, path: []const u8, extra: RequestExtra, diag: ?*Diagnostic) Error![]const u8 {
        return self.apiRequestFull(arena, .GET, path, null, self.timeout_ms, "", extra, diag);
    }

    /// The same as `lease.report(options)`.
    pub fn report(self: *Client, l: *Lease, options: ReportOptions) Error!?Lease {
        _ = self;
        return l.report(options);
    }

    /// The same as `lease.renew(options)`.
    pub fn renew(self: *Client, l: *Lease, options: RenewOptions) Error![]const u8 {
        _ = self;
        return l.renew(options);
    }

    /// Calls a consumer API route and returns the 2xx body, allocated in
    /// `arena`. Maps an error answer to an error. `extra_secret` is scrubbed
    /// from any stored message. Used by Lease.
    pub fn apiCall(
        self: *Client,
        arena: Allocator,
        method: http.Method,
        path: []const u8,
        body: ?[]const u8,
        timeout_override: u64,
        extra_secret: []const u8,
        diag: ?*Diagnostic,
    ) Error![]const u8 {
        const timeout = if (timeout_override != 0) timeout_override else self.timeout_ms;
        return self.apiCallTimeout(arena, method, path, body, timeout, extra_secret, diag);
    }

    fn apiCallTimeout(
        self: *Client,
        arena: Allocator,
        method: http.Method,
        path: []const u8,
        body: ?[]const u8,
        timeout_ms: u64,
        extra_secret: []const u8,
        diag: ?*Diagnostic,
    ) Error![]const u8 {
        return self.apiRequestFull(arena, method, path, body, timeout_ms, extra_secret, .{}, diag);
    }

    fn apiRequestFull(
        self: *Client,
        arena: Allocator,
        method: http.Method,
        path: []const u8,
        body: ?[]const u8,
        timeout_ms: u64,
        extra_secret: []const u8,
        extra: RequestExtra,
        diag: ?*Diagnostic,
    ) Error![]const u8 {
        const url = try std.mem.concat(arena, u8, &.{ self.base_url, path });
        var headers: std.ArrayList(http.Header) = .empty;
        try headers.append(arena, .{ .name = "Accept", .value = extra.accept });
        try self.appendClientHeadersTo(arena, &headers);
        const req: transport.Request = .{
            .method = method,
            .url = url,
            .authorization = if (extra.send_token) self.authorization else "",
            .user_agent = user_agent,
            .content_type = if (body != null) "application/json" else null,
            .headers = headers.items,
            .body = body,
            .timeout_ms = timeout_ms,
            .max_body_bytes = self.max_body_bytes,
        };
        const answer = try self.exchange(arena, &req, diag);
        if (answer.status < 200 or answer.status >= 300) {
            return errors.fromAnswer(answer.status, answer.headers, answer.body, self.secrets(extra_secret), diag);
        }
        return answer.body;
    }

    fn exchange(self: *Client, arena: Allocator, req: *const transport.Request, diag: ?*Diagnostic) Error!transport.Response {
        var detail: []const u8 = "";
        return transport.send(&self.http, self.io, arena, req, &detail) catch |err| {
            errors.fromClient(diag, 0, detail);
            return switch (err) {
                error.Timeout => error.Timeout,
                error.RequestFailed => error.RequestFailed,
                error.ResponseTooLarge => error.ResponseTooLarge,
                error.InvalidUrl => error.InvalidUrl,
                error.OutOfMemory => error.OutOfMemory,
                error.Canceled => error.Canceled,
            };
        };
    }

    fn fail(self: *Client, diag: ?*Diagnostic, err: Error, detail: []const u8) Error {
        _ = self;
        errors.fromClient(diag, 0, detail);
        return err;
    }

    fn secrets(self: *const Client, extra: []const u8) errors.Secrets {
        return .{
            .token = self.token,
            .access_password = self.access_password orelse "",
            .lease_secret = extra,
        };
    }

    fn appendClientHeaders(self: *Client, headers: *std.ArrayList(http.Header)) Allocator.Error!void {
        return self.appendClientHeadersTo(self.gpa, headers);
    }

    fn appendClientHeadersTo(self: *Client, allocator: Allocator, headers: *std.ArrayList(http.Header)) Allocator.Error!void {
        try headers.append(allocator, .{ .name = "X-Acnologia-Client", .value = user_agent });
        if (self.access_password) |a| try headers.append(allocator, .{ .name = "X-Acnologia-Access", .value = a });
    }
};

fn writeProxyUrl(w: *Io.Writer, base_url: []const u8, service: []const u8, path: []const u8, trailing_slash: bool, query: []const QueryParam) Io.Writer.Error!void {
    try w.writeAll(base_url);
    try w.writeAll("/proxy/");
    try errors.percentEncode(w, service);
    const rest = std.mem.trimStart(u8, path, "/");
    if (trailing_slash or rest.len > 0) try w.writeByte('/');
    try errors.percentEncodePath(w, rest);
    for (query, 0..) |q, i| {
        try w.writeByte(if (i == 0) '?' else '&');
        try errors.percentEncode(w, q.name);
        try w.writeByte('=');
        try errors.percentEncode(w, q.value);
    }
}

fn writeGuidePath(w: *Io.Writer, slug: []const u8) Io.Writer.Error!void {
    try w.writeAll("/api/v1/services/");
    try errors.percentEncode(w, slug);
    try w.writeAll("/guide");
}

fn writeProxyUrlWithToken(w: *Io.Writer, scheme: []const u8, token: []const u8, host_and_rest: []const u8) Io.Writer.Error!void {
    try w.writeAll(scheme);
    try w.writeAll("app:");
    try errors.percentEncode(w, token);
    try w.writeByte('@');
    try w.writeAll(host_and_rest);
}

fn writeLeaseBody(w: *Io.Writer, service: []const u8, options: LeaseOptions) Io.Writer.Error!void {
    var js: std.json.Stringify = .{ .writer = w };
    try js.beginObject();
    try js.objectField("service");
    try js.write(service);
    if (options.exclude_key_ids.len > 0) {
        try js.objectField("exclude_key_ids");
        try js.write(options.exclude_key_ids);
    }
    if (options.ttl_sec) |t| {
        try js.objectField("ttl_sec");
        try js.write(t);
    }
    if (options.wait_sec > 0) {
        try js.objectField("wait_sec");
        try js.write(options.wait_sec);
    }
    if (options.exclusive) {
        try js.objectField("exclusive");
        try js.write(true);
    }
    try js.endObject();
}

fn proxyInfo(arena: Allocator, headers: []const http.Header) Allocator.Error!ProxyInfo {
    const get = errors.headerValue;
    return .{
        .key_id = if (get(headers, "x-acnologia-key-id")) |v| std.fmt.parseInt(i64, std.mem.trim(u8, v, " "), 10) catch null else null,
        .attempts = if (get(headers, "x-acnologia-attempts")) |v| std.fmt.parseInt(u32, std.mem.trim(u8, v, " "), 10) catch null else null,
        .service = get(headers, "x-acnologia-service"),
        .cache = get(headers, "x-acnologia-cache"),
        .share = get(headers, "x-acnologia-share"),
        .replayed = if (get(headers, "idempotent-replayed")) |v| std.ascii.eqlIgnoreCase(std.mem.trim(u8, v, " "), "true") else false,
        .capability = get(headers, "x-acnologia-capability"),
        .tried = if (get(headers, "x-acnologia-tried")) |v| try catalog.parseTried(arena, v) else &.{},
    };
}

fn isReservedHeader(name: []const u8) bool {
    const reserved = [_][]const u8{
        "authorization",     "host",              "connection",         "content-length",
        "transfer-encoding", "x-acnologia-token", "x-acnologia-access", "x-acnologia-client",
        "user-agent",
    };
    for (reserved) |r| if (std.ascii.eqlIgnoreCase(name, r)) return true;
    return false;
}

fn hasLineBreak(s: []const u8) bool {
    return std.mem.findAny(u8, s, "\r\n") != null;
}

fn nonEmpty(s: ?[]const u8) ?[]const u8 {
    const v = s orelse return null;
    return if (std.mem.trim(u8, v, " \t\r\n").len == 0) null else v;
}

fn envGet(environ: ?*const std.process.Environ.Map, name: []const u8) ?[]const u8 {
    const map = environ orelse return null;
    return nonEmpty(map.get(name));
}

fn freeSecret(gpa: Allocator, s: []u8) void {
    std.crypto.secureZero(u8, s);
    gpa.free(s);
}

/// True when both URLs have the same scheme, host and port.
fn sameOrigin(a: []const u8, b: []const u8) bool {
    const ua = std.Uri.parse(std.mem.trim(u8, a, " \t\r\n")) catch return false;
    const ub = std.Uri.parse(std.mem.trim(u8, b, " \t\r\n")) catch return false;
    if (!std.ascii.eqlIgnoreCase(ua.scheme, ub.scheme)) return false;
    var ba: [std.Io.net.HostName.max_len]u8 = undefined;
    var bb: [std.Io.net.HostName.max_len]u8 = undefined;
    const ha = (ua.host orelse return false).toRaw(&ba) catch return false;
    const hb = (ub.host orelse return false).toRaw(&bb) catch return false;
    if (!std.ascii.eqlIgnoreCase(ha, hb)) return false;
    return portOf(ua) == portOf(ub);
}

fn portOf(u: std.Uri) u16 {
    return u.port orelse if (std.ascii.eqlIgnoreCase(u.scheme, "https")) 443 else 80;
}

test {
    _ = errors;
    _ = lease_mod;
    _ = catalog;
}
