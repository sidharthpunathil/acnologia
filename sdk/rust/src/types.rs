//! Plain data types shared by the async and blocking clients.

use std::time::Duration;

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use serde_json::Value;

/// The longest [`LeaseOptions::wait`] the server accepts.
pub const MAX_WAIT: Duration = Duration::from_secs(60);

/// Where a provider expects the key.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum AuthType {
    /// A query parameter named [`Auth::name`].
    Query,
    /// A header named [`Auth::name`] (`Authorization` when empty).
    #[default]
    Header,
    /// An `Authorization: Bearer <secret>` style header.
    Bearer,
    /// A type this SDK version does not know. Treated like [`AuthType::Header`].
    #[serde(other)]
    Other,
}

/// How a service takes its key: the type, the parameter or header name, and an
/// optional prefix.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct Auth {
    /// Query, header or bearer.
    #[serde(rename = "type", default)]
    pub kind: AuthType,
    /// The query parameter or header name.
    #[serde(default)]
    pub name: String,
    /// When non empty, goes in front of the secret for every type. For bearer
    /// it replaces the default `Bearer ` prefix.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub prefix: String,
}

/// An entry from `GET /api/v1/services`.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct Service {
    /// Service slug, used in leases and proxy paths.
    pub slug: String,
    /// Display name.
    #[serde(default)]
    pub name: String,
    /// Provider slug.
    #[serde(default)]
    pub provider: String,
    /// Provider category.
    #[serde(default)]
    pub category: String,
    /// True when at least one key can be handed out right now.
    #[serde(default)]
    pub available: bool,
    /// The provider's base URL.
    #[serde(default)]
    pub base_url: String,
    /// Where the provider expects the key.
    #[serde(default)]
    pub auth: Auth,
    /// Where the service's usage guide is served
    /// (`/api/v1/services/{slug}/guide`). Empty from older servers.
    #[serde(default)]
    pub guide_url: String,
}

/// The result of using a leased key, sent with a report.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Outcome {
    /// The call worked.
    #[default]
    Ok,
    /// The provider rate limited the key.
    RateLimited,
    /// The key used up its quota.
    QuotaExhausted,
    /// The provider rejected the key.
    Invalid,
    /// Something else went wrong. The key is not penalized much.
    Error,
    /// The account is at its concurrency cap; the key steps aside briefly.
    Busy,
    /// The provider suspended the account; the key leaves rotation.
    Banned,
}

impl Outcome {
    /// The wire value, such as `rate_limited`.
    pub fn as_str(self) -> &'static str {
        match self {
            Outcome::Ok => "ok",
            Outcome::RateLimited => "rate_limited",
            Outcome::QuotaExhausted => "quota_exhausted",
            Outcome::Invalid => "invalid",
            Outcome::Error => "error",
            Outcome::Busy => "busy",
            Outcome::Banned => "banned",
        }
    }
}

impl std::fmt::Display for Outcome {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

/// Optional parameters for a lease.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct LeaseOptions {
    /// Keys that must not be returned.
    pub exclude_key_ids: Vec<i64>,
    /// Lease lifetime. `None` uses the server default (120s on services with a
    /// concurrency limit, 300s otherwise). Sent as `ttl_sec`, rounded up.
    pub ttl: Option<Duration>,
    /// How long the server may wait for a free slot when every usable key is
    /// at its concurrency limit, at most [`MAX_WAIT`]. `None` or zero fails
    /// fast with [`Error::AllKeysBusy`](crate::Error::AllKeysBusy). Sent as
    /// `wait_sec`, rounded up.
    pub wait: Option<Duration>,
    /// Keep the key from every other caller until the lease ends.
    pub exclusive: bool,
}

impl LeaseOptions {
    /// Empty options: server defaults, no exclusions.
    pub fn new() -> Self {
        Self::default()
    }

    /// Set the lease lifetime.
    pub fn ttl(mut self, ttl: Duration) -> Self {
        self.ttl = Some(ttl);
        self
    }

    /// Let the server wait up to `wait` for a free slot.
    pub fn wait(mut self, wait: Duration) -> Self {
        self.wait = Some(wait);
        self
    }

    /// Exclude these key ids.
    pub fn exclude(mut self, ids: impl IntoIterator<Item = i64>) -> Self {
        self.exclude_key_ids.extend(ids);
        self
    }

    /// Ask for an exclusive lease.
    pub fn exclusive(mut self, exclusive: bool) -> Self {
        self.exclusive = exclusive;
        self
    }
}

/// How a leased key behaved. Set `rotate` to receive the next key in the
/// same round trip.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Report {
    /// What happened.
    pub outcome: Outcome,
    /// The provider's HTTP status, if any.
    pub status_code: Option<u16>,
    /// How long the key should cool down. Sent as `retry_after_sec`, rounded up.
    pub retry_after: Option<Duration>,
    /// A short note shown in the dashboard. Never put the secret here.
    pub message: Option<String>,
    /// Ask for the next key in the same call.
    pub rotate: bool,
}

impl Report {
    /// A report with this outcome and nothing else set.
    pub fn new(outcome: Outcome) -> Self {
        Report {
            outcome,
            ..Default::default()
        }
    }

    /// Outcome `ok`.
    pub fn ok() -> Self {
        Self::new(Outcome::Ok)
    }

    /// Outcome `rate_limited`, with an optional cooldown.
    pub fn rate_limited(retry_after: Option<Duration>) -> Self {
        Report {
            retry_after,
            ..Self::new(Outcome::RateLimited)
        }
    }

    /// Outcome `quota_exhausted`.
    pub fn quota_exhausted() -> Self {
        Self::new(Outcome::QuotaExhausted)
    }

    /// Outcome `invalid`.
    pub fn invalid() -> Self {
        Self::new(Outcome::Invalid)
    }

    /// Outcome `error`.
    pub fn error() -> Self {
        Self::new(Outcome::Error)
    }

    /// Outcome `busy`.
    pub fn busy() -> Self {
        Self::new(Outcome::Busy)
    }

    /// Outcome `banned`.
    pub fn banned() -> Self {
        Self::new(Outcome::Banned)
    }

    /// Set the provider's HTTP status.
    pub fn status(mut self, status_code: u16) -> Self {
        self.status_code = Some(status_code);
        self
    }

    /// Set the cooldown.
    pub fn retry_after(mut self, retry_after: Duration) -> Self {
        self.retry_after = Some(retry_after);
        self
    }

    /// Set the message.
    pub fn message(mut self, message: impl Into<String>) -> Self {
        self.message = Some(message.into());
        self
    }

    /// Ask for the next key in the same call.
    pub fn rotate(mut self) -> Self {
        self.rotate = true;
        self
    }
}

/// Where and how to send a lease's secret, as computed by `Lease::credential`.
///
/// Its `Debug` output redacts the value.
#[derive(Clone, PartialEq, Eq)]
pub enum Credential {
    /// Send `name: value` as a header.
    Header {
        /// Header name.
        name: String,
        /// Header value, secret included.
        value: String,
    },
    /// Send `name=value` as a query parameter.
    Query {
        /// Parameter name.
        name: String,
        /// Parameter value, secret included.
        value: String,
    },
}

impl std::fmt::Debug for Credential {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Credential::Header { name, .. } => f
                .debug_struct("Header")
                .field("name", name)
                .field("value", &crate::core::Redacted)
                .finish(),
            Credential::Query { name, .. } => f
                .debug_struct("Query")
                .field("name", name)
                .field("value", &crate::core::Redacted)
                .finish(),
        }
    }
}

/// Options for `with_key`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WithKeyOptions {
    /// How many keys to try before giving up. Default 5, at least 1.
    pub max_attempts: u32,
    /// Options for the first lease. `ttl`, `wait` and `exclusive` also apply
    /// to every later lease.
    pub lease: LeaseOptions,
}

impl Default for WithKeyOptions {
    fn default() -> Self {
        WithKeyOptions {
            max_attempts: 5,
            lease: LeaseOptions::default(),
        }
    }
}

impl WithKeyOptions {
    /// Defaults: 5 attempts, server lease defaults.
    pub fn new() -> Self {
        Self::default()
    }

    /// Set how many keys to try (values below 1 are treated as 1).
    pub fn max_attempts(mut self, n: u32) -> Self {
        self.max_attempts = n;
        self
    }

    /// Set the lease options.
    pub fn lease(mut self, lease: LeaseOptions) -> Self {
        self.lease = lease;
        self
    }

    /// Set the lease wait for every lease taken.
    pub fn wait(mut self, wait: Duration) -> Self {
        self.lease.wait = Some(wait);
        self
    }

    /// Set the lease lifetime for every lease taken.
    pub fn ttl(mut self, ttl: Duration) -> Self {
        self.lease.ttl = Some(ttl);
        self
    }
}

/// The answer of `GET /api/v1/me`: the token's own access, limits and use.
#[derive(Debug, Clone, PartialEq, Default, Deserialize)]
pub struct Me {
    /// The app the token belongs to.
    #[serde(default)]
    pub app: MeApp,
    /// The app's request and budget limits.
    #[serde(default)]
    pub limits: MeLimits,
    /// What the app used today and this month.
    #[serde(default)]
    pub usage: MeUsage,
    /// The limit the app reached, if any. The same as `usage.limit_hit`.
    #[serde(default)]
    pub limit_hit: Option<LimitHit>,
    /// The services the token may use.
    #[serde(default)]
    pub services: Vec<MeService>,
    /// The share this token belongs to, `None` for an ordinary app.
    #[serde(default)]
    pub share: Option<MeShare>,
}

/// The app in [`Me`].
#[derive(Debug, Clone, PartialEq, Eq, Default, Deserialize)]
pub struct MeApp {
    /// App id.
    #[serde(default)]
    pub id: String,
    /// App name (the share's name for a share's token).
    #[serde(default)]
    pub name: String,
}

/// An app's limits. `None` means no limit.
#[derive(Debug, Clone, PartialEq, Default, Deserialize)]
pub struct MeLimits {
    /// Requests per day.
    #[serde(default)]
    pub daily_requests: Option<i64>,
    /// Requests per month.
    #[serde(default)]
    pub monthly_requests: Option<i64>,
    /// Spend per day, in the server's currency.
    #[serde(default)]
    pub daily_budget: Option<f64>,
    /// Spend per month, in the server's currency.
    #[serde(default)]
    pub monthly_budget: Option<f64>,
}

/// An app's use so far.
#[derive(Debug, Clone, PartialEq, Default, Deserialize)]
pub struct MeUsage {
    /// Requests today.
    #[serde(default)]
    pub today_requests: i64,
    /// Spend today.
    #[serde(default)]
    pub today_cost: f64,
    /// Requests this month.
    #[serde(default)]
    pub month_requests: i64,
    /// Spend this month.
    #[serde(default)]
    pub month_cost: f64,
    /// When the app last made a call, as RFC 3339 text.
    #[serde(default)]
    pub last_used_at: Option<String>,
    /// The limit the app reached, if any.
    #[serde(default)]
    pub limit_hit: Option<LimitHit>,
}

/// A limit an app reached.
#[derive(Debug, Clone, PartialEq, Eq, Default, Deserialize)]
pub struct LimitHit {
    /// `daily_requests`, `monthly_requests`, `daily_budget` or `monthly_budget`.
    #[serde(default)]
    pub limit: String,
    /// When the limit resets, as RFC 3339 text.
    #[serde(default)]
    pub reset_at: String,
}

/// A service in [`Me`].
#[derive(Debug, Clone, PartialEq, Eq, Default, Deserialize)]
pub struct MeService {
    /// Service slug.
    pub slug: String,
    /// Display name.
    #[serde(default)]
    pub name: String,
    /// Provider slug.
    #[serde(default)]
    pub provider: String,
    /// Provider category.
    #[serde(default)]
    pub category: String,
    /// Keys free right now for a share's token; 1 or 0 for an ordinary app.
    #[serde(default)]
    pub available: i64,
    /// The service logo as a `data:` URI, if it has one.
    #[serde(default)]
    pub logo_url: Option<String>,
}

/// The share behind a share's token, in [`Me`].
#[derive(Debug, Clone, PartialEq, Eq, Default, Deserialize)]
pub struct MeShare {
    /// Share id.
    #[serde(default)]
    pub id: String,
    /// Share name.
    #[serde(default)]
    pub name: String,
    /// Who shared it.
    #[serde(default)]
    pub owner_name: String,
    /// The owner's id.
    #[serde(default)]
    pub owner_id: String,
    /// Who it was shared with.
    #[serde(default)]
    pub recipient: String,
    /// The owner's label for it.
    #[serde(default)]
    pub label: String,
    /// `active`, `paused`, `expired` or `revoked`.
    #[serde(default)]
    pub status: String,
    /// The share mode.
    #[serde(default)]
    pub mode: String,
    /// When the share ends, as RFC 3339 text, if it does.
    #[serde(default)]
    pub ends_at: Option<String>,
}

/// A service's usage guide from `GET /api/v1/services/{slug}/guide`.
#[derive(Debug, Clone, PartialEq, Default, Deserialize)]
pub struct Guide {
    /// The guide as Markdown.
    #[serde(default)]
    pub markdown: String,
    /// True when the server wrote the guide from the provider spec.
    #[serde(default)]
    pub generated: bool,
    /// The structured guide.
    #[serde(default)]
    pub guide: Value,
}

/// A capability from `GET /api/v1/capabilities`: one job several providers
/// can do, called with [`Client::capability`](crate::Client::capability).
#[derive(Debug, Clone, PartialEq, Default, Deserialize)]
pub struct Capability {
    /// Capability slug, used in `/proxy/{slug}`.
    pub slug: String,
    /// Display name.
    #[serde(default)]
    pub name: String,
    /// What it does.
    #[serde(default)]
    pub description: String,
    /// How a route is picked.
    #[serde(default)]
    pub strategy: Option<String>,
    /// The params it takes.
    #[serde(default)]
    pub params: Value,
    /// The services that can serve it.
    #[serde(default)]
    pub routes: Vec<CapabilityRoute>,
    /// Its proxy URL.
    #[serde(default)]
    pub url: String,
}

/// One route of a [`Capability`].
#[derive(Debug, Clone, PartialEq, Eq, Default, Deserialize)]
pub struct CapabilityRoute {
    /// Service slug.
    #[serde(default)]
    pub service: String,
    /// True when the answer is mapped into the capability's own shape.
    #[serde(default)]
    pub mapped: bool,
}

/// Options for a capability call.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct CapabilityOptions {
    /// Use only this service's route (`?_service=`).
    pub service: Option<String>,
    /// Return the provider's own body (`?_raw=1`).
    pub raw: bool,
    /// Sent as `Idempotency-Key`.
    pub idempotency_key: Option<String>,
    /// Send `Cache-Control: no-cache`.
    pub no_cache: bool,
}

impl CapabilityOptions {
    /// Empty options.
    pub fn new() -> Self {
        Self::default()
    }

    /// Pin one service.
    pub fn service(mut self, service: impl Into<String>) -> Self {
        self.service = Some(service.into());
        self
    }

    /// Ask for the provider's own body.
    pub fn raw(mut self) -> Self {
        self.raw = true;
        self
    }

    /// Set the idempotency key.
    pub fn idempotency_key(mut self, key: impl Into<String>) -> Self {
        self.idempotency_key = Some(key.into());
        self
    }

    /// Skip the cache.
    pub fn no_cache(mut self) -> Self {
        self.no_cache = true;
        self
    }
}

/// How to use the forward proxy, from `GET /api/v1/proxy`.
///
/// It holds no token: `proxy_url_with_token` has a `<app token>`
/// placeholder, so `Debug` output is safe to log.
#[derive(Debug, Clone, PartialEq, Eq, Default, Deserialize)]
pub struct ForwardProxy {
    /// The proxy URL without credentials, such as `http://host:8888`.
    #[serde(default)]
    pub proxy_url: String,
    /// The proxy URL with a `<app token>` placeholder.
    #[serde(default)]
    pub proxy_url_with_token: String,
    /// Where the CA certificate is served.
    #[serde(default)]
    pub ca_url: String,
    /// The CA certificate's fingerprint.
    #[serde(default)]
    pub ca_fingerprint: String,
    /// True when HTTPS to other hosts is tunneled through.
    #[serde(default)]
    pub passthrough: bool,
    /// The hosts the proxy routes to a service.
    #[serde(default)]
    pub hosts: Vec<ForwardProxyHost>,
    /// The environment the server suggests, with placeholders.
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    /// The request header that picks a service when several match.
    #[serde(default)]
    pub service_header: String,
}

/// A host the forward proxy routes to a service.
#[derive(Debug, Clone, PartialEq, Eq, Default, Deserialize)]
pub struct ForwardProxyHost {
    /// Host name.
    #[serde(default)]
    pub host: String,
    /// Service slug.
    #[serde(default)]
    pub service: String,
    /// Service name.
    #[serde(default)]
    pub name: String,
    /// Path prefix, when the host maps to a sub path of the service.
    #[serde(default)]
    pub path: String,
}

/// One route tried by a capability call, from `X-Acnologia-Tried`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Tried {
    /// Service slug.
    pub service: String,
    /// The HTTP status it answered.
    pub status: u16,
}
