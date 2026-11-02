//! Plain data types shared by the async and blocking clients.

use std::time::Duration;

use serde::{Deserialize, Serialize};

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
