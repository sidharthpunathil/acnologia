//! Rust client for [Acnologia](https://github.com/sidharthpunathil/acnologia),
//! a self-hosted API key pool.
//!
//! Acnologia can be used in two ways:
//!
//! - **Lease mode**: [`Client::lease`] a key, call the provider yourself, then
//!   [`Lease::report`] the outcome. [`Client::with_key`] runs that loop and
//!   rotates keys for you.
//! - **Proxy mode**: send requests through [`Client::proxy`]. Acnologia injects
//!   a key and rotates on rate limits without ever exposing the secret.
//!   [`Client::capability`] calls one job that several providers can do.
//! - **Forward proxy**: [`Client::forward_proxy_env`] gives the environment
//!   that makes any HTTP client send provider calls through Acnologia, and
//!   [`Client::forward_proxy_client`] a reqwest client set up for it.
//!
//! The client is async (reqwest with rustls, on Tokio). Enable the `blocking`
//! feature for a synchronous client in [`blocking`].
//!
//! ```no_run
//! use acnologia::{Client, LeaseOptions, Report};
//! use std::time::Duration;
//!
//! # async fn run() -> Result<(), acnologia::Error> {
//! let acn = Client::from_env()?; // ACNOLOGIA_URL, ACNOLOGIA_TOKEN
//! let lease = acn.lease("serpapi", LeaseOptions::new()).await?;
//! let req = reqwest::Client::new().get(lease.url("/search.json", &[("q", "coffee")]));
//! let resp = lease.apply(req).send().await?;
//! if resp.status() == 429 {
//!     let next = lease
//!         .report(Report::rate_limited(Some(Duration::from_secs(3600))).status(429).rotate())
//!         .await?;
//!     // retry with `next` when it is Some
//! } else {
//!     lease.report(Report::ok()).await?;
//! }
//! # Ok(()) }
//! ```
//!
//! Secrets never appear in `Debug` or `Display` output: a [`Lease`] prints
//! `[redacted]` for its secret and a [`Client`] for its token, and the SDK
//! does no logging.

#![forbid(unsafe_code)]
#![warn(missing_docs)]
#![cfg_attr(docsrs, feature(doc_cfg))]

mod client;
mod core;
mod error;
mod lease_info;
mod proxy;
mod response;
mod types;

#[cfg(feature = "blocking")]
#[cfg_attr(docsrs, doc(cfg(feature = "blocking")))]
pub mod blocking;

pub use client::{
    check_status, Client, ClientBuilder, Lease, DEFAULT_CA_PATH, DEFAULT_TIMEOUT,
    ENV_ACCESS_PASSWORD, ENV_TOKEN, ENV_URL,
};
pub use error::{BoxError, Error};
pub use lease_info::LeaseInfo;
pub use proxy::Proxy;
pub use response::ProxyResponseExt;
pub use types::{
    Auth, AuthType, Capability, CapabilityOptions, CapabilityRoute, Credential, ForwardProxy,
    ForwardProxyHost, Guide, LeaseOptions, LimitHit, Me, MeApp, MeLimits, MeService, MeShare,
    MeUsage, Outcome, Report, Service, Tried, WithKeyOptions, MAX_WAIT,
};

/// The SDK version.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

/// The `User-Agent` this SDK sends: `acnologia-rust/<version>`.
pub const USER_AGENT: &str = core::USER_AGENT;

/// A `Result` with this crate's [`Error`].
pub type Result<T, E = Error> = std::result::Result<T, E>;
