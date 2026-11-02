//! A synchronous client, enabled with the `blocking` feature.
//!
//! It mirrors the async API on top of `reqwest::blocking`. Like that client,
//! it must not be used from inside an async runtime.
//!
//! ```no_run
//! use acnologia::blocking::Client;
//! use acnologia::{LeaseOptions, Report};
//!
//! # fn run() -> Result<(), acnologia::Error> {
//! let acn = Client::from_env()?;
//! let lease = acn.lease("serpapi", LeaseOptions::new())?;
//! // ... call the provider with lease.secret() ...
//! lease.report(Report::ok())?;
//! # Ok(()) }
//! ```

use std::fmt;
use std::ops::Deref;
use std::panic::{self, AssertUnwindSafe};
use std::sync::Arc;
use std::time::Duration;

use reqwest::blocking::{RequestBuilder, Response};
use reqwest::header::{ACCEPT, AUTHORIZATION, RETRY_AFTER, USER_AGENT};
use reqwest::Method;
use serde::de::DeserializeOwned;
use serde::Serialize;
use serde_json::Value;

use crate::client::{env_pair, validate, DEFAULT_TIMEOUT};
use crate::core::{self, Redacted};
use crate::error::{parse_error, Error};
use crate::lease_info::LeaseInfo;
use crate::types::{Credential, LeaseOptions, Report, Service, WithKeyOptions};

struct Inner {
    base_url: String,
    token: String,
    user_agent: String,
    http: reqwest::blocking::Client,
    proxy_http: reqwest::blocking::Client,
}

/// A blocking client for an Acnologia server. Cloning is cheap.
#[derive(Clone)]
pub struct Client {
    inner: Arc<Inner>,
}

impl fmt::Debug for Client {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("blocking::Client")
            .field("base_url", &self.inner.base_url)
            .field("token", &Redacted)
            .finish()
    }
}

/// Configures a blocking [`Client`].
pub struct ClientBuilder {
    base_url: String,
    token: String,
    timeout: Option<Duration>,
    user_agent: Option<String>,
    http: Option<reqwest::blocking::Client>,
}

impl fmt::Debug for ClientBuilder {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("blocking::ClientBuilder")
            .field("base_url", &self.base_url)
            .field("token", &Redacted)
            .field("timeout", &self.timeout)
            .finish()
    }
}

impl ClientBuilder {
    /// Timeout for API calls. Default [`DEFAULT_TIMEOUT`].
    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }

    /// Override the User-Agent header.
    pub fn user_agent(mut self, ua: impl Into<String>) -> Self {
        self.user_agent = Some(ua.into());
        self
    }

    /// Use your own reqwest blocking client for every call. Set
    /// `redirect::Policy::none()` on it so the app token is never replayed.
    pub fn http_client(mut self, http: reqwest::blocking::Client) -> Self {
        self.http = Some(http);
        self
    }

    /// Build the client.
    pub fn build(self) -> Result<Client, Error> {
        let (base_url, token) = validate(&self.base_url, &self.token)?;
        let (http, proxy_http) = match self.http {
            Some(h) => (h.clone(), h),
            None => (
                reqwest::blocking::Client::builder()
                    .redirect(reqwest::redirect::Policy::none())
                    .timeout(self.timeout.unwrap_or(DEFAULT_TIMEOUT))
                    .build()?,
                reqwest::blocking::Client::builder()
                    .redirect(reqwest::redirect::Policy::none())
                    .timeout(None)
                    .build()?,
            ),
        };
        Ok(Client {
            inner: Arc::new(Inner {
                base_url,
                token,
                user_agent: self
                    .user_agent
                    .unwrap_or_else(|| core::USER_AGENT.to_string()),
                http,
                proxy_http,
            }),
        })
    }
}

impl Client {
    /// A client for the server at `base_url` using an app token.
    pub fn new(base_url: impl Into<String>, token: impl Into<String>) -> Result<Self, Error> {
        Self::builder(base_url, token).build()
    }

    /// A client configured from `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN`.
    pub fn from_env() -> Result<Self, Error> {
        let (base, token) = env_pair()?;
        Self::new(base, token)
    }

    /// A builder for more options.
    pub fn builder(base_url: impl Into<String>, token: impl Into<String>) -> ClientBuilder {
        ClientBuilder {
            base_url: base_url.into(),
            token: token.into(),
            timeout: None,
            user_agent: None,
            http: None,
        }
    }

    /// The server URL without a trailing slash.
    pub fn base_url(&self) -> &str {
        &self.inner.base_url
    }

    fn auth_headers(&self, rb: RequestBuilder) -> RequestBuilder {
        let mut auth =
            reqwest::header::HeaderValue::from_str(&format!("Bearer {}", self.inner.token))
                .unwrap_or_else(|_| reqwest::header::HeaderValue::from_static(""));
        auth.set_sensitive(true);
        rb.header(AUTHORIZATION, auth)
            .header(USER_AGENT, &self.inner.user_agent)
    }

    fn call<T: DeserializeOwned>(
        &self,
        method: Method,
        path: &str,
        body: Option<Value>,
    ) -> Result<T, Error> {
        let url = format!("{}{}", self.inner.base_url, path);
        let mut rb = self
            .auth_headers(self.inner.http.request(method, url))
            .header(ACCEPT, "application/json");
        if let Some(b) = body {
            rb = rb.json(&b);
        }
        let resp = rb.send()?;
        let status = resp.status();
        let retry = resp
            .headers()
            .get(RETRY_AFTER)
            .and_then(|v| v.to_str().ok())
            .map(str::to_owned);
        let bytes = resp.bytes()?;
        if !status.is_success() {
            return Err(parse_error(status.as_u16(), retry.as_deref(), &bytes));
        }
        Ok(serde_json::from_slice(&bytes)?)
    }

    /// The services this app token may use.
    pub fn services(&self) -> Result<Vec<Service>, Error> {
        let out: core::ServicesResponse = self.call(Method::GET, "/api/v1/services", None)?;
        Ok(out.services)
    }

    /// Lease a key for `service`. See [`crate::Client::lease`].
    pub fn lease(&self, service: &str, opts: LeaseOptions) -> Result<Lease, Error> {
        let body = core::lease_body(service, &opts)?;
        let w: core::LeaseWire = self.call(Method::POST, "/api/v1/lease", Some(body))?;
        Ok(self.wrap(w))
    }

    fn wrap(&self, w: core::LeaseWire) -> Lease {
        Lease {
            info: LeaseInfo::from_wire(w),
            client: self.clone(),
        }
    }

    /// [`Client::with_key_opts`] with default options (5 attempts).
    pub fn with_key<T, F>(&self, service: &str, f: F) -> Result<T, Error>
    where
        F: FnMut(&Lease) -> Result<T, Error>,
    {
        self.with_key_opts(service, WithKeyOptions::default(), f)
    }

    /// Lease a key, call `f`, report the outcome, and rotate on the three
    /// rotation errors. Same rules as [`crate::Client::with_key_opts`]. When
    /// `f` panics, the lease is reported `error` and the panic resumes.
    pub fn with_key_opts<T, F>(
        &self,
        service: &str,
        opts: WithKeyOptions,
        mut f: F,
    ) -> Result<T, Error>
    where
        F: FnMut(&Lease) -> Result<T, Error>,
    {
        let max = opts.max_attempts.max(1);
        let mut tried = opts.lease.exclude_key_ids.clone();
        let mut lease = self.lease(service, opts.lease.clone())?;
        let mut last_err: Option<Error> = None;
        for attempt in 1..=max {
            let res = match panic::catch_unwind(AssertUnwindSafe(|| f(&lease))) {
                Ok(r) => r,
                Err(p) => {
                    let _ = lease.report(Report::error().message("with_key callback panicked"));
                    panic::resume_unwind(p);
                }
            };
            let err = match res {
                Ok(v) => {
                    lease.report(Report::ok())?;
                    return Ok(v);
                }
                Err(e) => e,
            };
            let (mut report, rotate) = core::classify(&err, lease.secret());
            if !rotate {
                let _ = lease.report(report);
                return Err(err);
            }
            tried.push(lease.key_id);
            if attempt == max {
                report.rotate = false;
                let _ = lease.report(report);
                last_err = Some(err);
                break;
            }
            let next = lease.report(report)?;
            last_err = Some(err);
            lease = match next {
                Some(n) => n,
                None => self.lease(
                    service,
                    LeaseOptions {
                        exclude_key_ids: tried.clone(),
                        ..opts.lease.clone()
                    },
                )?,
            };
        }
        Err(last_err.unwrap_or_else(|| Error::InvalidInput("with_key made no attempt".into())))
    }

    /// A proxy helper for `service`.
    pub fn proxy(&self, service: impl Into<String>) -> Proxy {
        Proxy {
            client: self.clone(),
            service: service.into(),
        }
    }
}

/// A key handed out by the pool (blocking). Derefs to [`LeaseInfo`].
/// `Debug` and `Display` redact the secret.
#[derive(Clone)]
pub struct Lease {
    info: LeaseInfo,
    client: Client,
}

impl Deref for Lease {
    type Target = LeaseInfo;
    fn deref(&self) -> &LeaseInfo {
        &self.info
    }
}

impl fmt::Debug for Lease {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        fmt::Debug::fmt(&self.info, f)
    }
}

impl fmt::Display for Lease {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        fmt::Display::fmt(&self.info, f)
    }
}

impl Lease {
    /// The lease data without the client handle.
    pub fn info(&self) -> &LeaseInfo {
        &self.info
    }

    /// Put the secret on a request where the provider expects it.
    pub fn apply(&self, rb: RequestBuilder) -> RequestBuilder {
        match self.credential() {
            Credential::Header { name, value } => {
                match reqwest::header::HeaderValue::from_str(&value) {
                    Ok(mut v) => {
                        v.set_sensitive(true);
                        rb.header(name, v)
                    }
                    Err(_) => rb.header(name, value),
                }
            }
            Credential::Query { name, value } => rb.query(&[(name, value)]),
        }
    }

    /// Report how the key behaved. With `report.rotate` set, returns the next
    /// lease when another key is available.
    pub fn report(&self, report: Report) -> Result<Option<Lease>, Error> {
        let body = core::report_body(&report);
        let out: core::ReportResponse =
            self.client
                .call(Method::POST, &core::report_path(&self.lease_id), Some(body))?;
        Ok(out.next.map(|w| self.client.wrap(w)))
    }

    /// Extend this open lease. Returns the new expiry as RFC 3339 text.
    pub fn renew(&self, ttl: Option<Duration>) -> Result<String, Error> {
        let out: core::RenewResponse = self.client.call(
            Method::POST,
            &core::renew_path(&self.lease_id),
            Some(core::renew_body(ttl)),
        )?;
        Ok(out.expires_at)
    }
}

/// Blocking form of [`crate::check_status`].
pub fn check_status(resp: Response) -> Result<Response, Error> {
    let retry = core::retry_after_header(
        resp.headers()
            .get(RETRY_AFTER)
            .and_then(|v| v.to_str().ok()),
    );
    match Error::for_status(resp.status().as_u16(), retry) {
        Some(e) => Err(e),
        None => Ok(resp),
    }
}

/// Proxy mode for the blocking client. See [`crate::Proxy`].
#[derive(Debug, Clone)]
pub struct Proxy {
    client: Client,
    service: String,
}

impl Proxy {
    /// The service slug this proxy targets.
    pub fn service(&self) -> &str {
        &self.service
    }

    /// The full proxy URL for a provider path and query.
    pub fn url(&self, path: &str, query: &[(&str, &str)]) -> String {
        core::proxy_url(&self.client.inner.base_url, &self.service, path, query)
    }

    /// A request builder with the app token and user agent set.
    pub fn request(&self, method: Method, path: &str) -> RequestBuilder {
        let rb = self
            .client
            .inner
            .proxy_http
            .request(method, self.url(path, &[]));
        self.client.auth_headers(rb)
    }

    /// Send a GET with query parameters.
    pub fn get(&self, path: &str, query: &[(&str, &str)]) -> Result<Response, Error> {
        let rb = self.client.inner.proxy_http.get(self.url(path, query));
        Ok(self.client.auth_headers(rb).send()?)
    }

    /// Send a POST with a body and content type.
    pub fn post(
        &self,
        path: &str,
        content_type: &str,
        body: impl Into<reqwest::blocking::Body>,
    ) -> Result<Response, Error> {
        Ok(self
            .request(Method::POST, path)
            .header(reqwest::header::CONTENT_TYPE, content_type)
            .body(body)
            .send()?)
    }

    /// Encode `body` as JSON and POST it.
    pub fn post_json<B: Serialize + ?Sized>(
        &self,
        path: &str,
        body: &B,
    ) -> Result<Response, Error> {
        Ok(self.request(Method::POST, path).json(body).send()?)
    }
}
