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

use std::collections::BTreeMap;
use std::fmt;
use std::ops::Deref;
use std::panic::{self, AssertUnwindSafe};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use reqwest::blocking::{RequestBuilder, Response};
use reqwest::header::{ACCEPT, AUTHORIZATION, RETRY_AFTER, USER_AGENT};
use reqwest::Method;
use serde::de::DeserializeOwned;
use serde::Serialize;
use serde_json::Value;

use crate::client::{access_password, env_pair, validate, DEFAULT_CA_PATH, DEFAULT_TIMEOUT};
use crate::core::{self, Redacted};
use crate::error::Error;
use crate::lease_info::LeaseInfo;
use crate::types::{
    Capability, CapabilityOptions, Credential, ForwardProxy, Guide, LeaseOptions, Me, Report,
    Service, WithKeyOptions,
};

struct Inner {
    base_url: String,
    token: String,
    access_password: Option<String>,
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
    access_password: Option<String>,
    http: Option<reqwest::blocking::Client>,
}

impl fmt::Debug for ClientBuilder {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("blocking::ClientBuilder")
            .field("base_url", &self.base_url)
            .field("token", &Redacted)
            .field(
                "access_password",
                &self.access_password.as_ref().map(|_| Redacted),
            )
            .field("timeout", &self.timeout)
            .finish()
    }
}

impl ClientBuilder {
    /// Send `X-Acnologia-Access` with every request. When not set,
    /// `ACNOLOGIA_ACCESS_PASSWORD` is used if present.
    pub fn access_password(mut self, password: impl Into<String>) -> Self {
        self.access_password = Some(password.into());
        self
    }

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
                access_password: access_password(self.access_password),
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
            access_password: None,
            http: None,
        }
    }

    /// A builder seeded from `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN`.
    pub fn builder_from_env() -> Result<ClientBuilder, Error> {
        let (base, token) = env_pair()?;
        Ok(Self::builder(base, token))
    }

    /// The server URL without a trailing slash.
    pub fn base_url(&self) -> &str {
        &self.inner.base_url
    }

    fn base_headers(&self, rb: RequestBuilder) -> RequestBuilder {
        let rb = rb.header(USER_AGENT, &self.inner.user_agent);
        match &self.inner.access_password {
            Some(p) => match reqwest::header::HeaderValue::from_str(p) {
                Ok(mut v) => {
                    v.set_sensitive(true);
                    rb.header(core::ACCESS_HEADER, v)
                }
                Err(_) => rb,
            },
            None => rb,
        }
    }

    fn auth_headers(&self, rb: RequestBuilder) -> RequestBuilder {
        let mut auth =
            reqwest::header::HeaderValue::from_str(&format!("Bearer {}", self.inner.token))
                .unwrap_or_else(|_| reqwest::header::HeaderValue::from_static(""));
        auth.set_sensitive(true);
        self.base_headers(rb.header(AUTHORIZATION, auth))
    }

    fn send(
        &self,
        method: Method,
        path: &str,
        body: Option<Value>,
        accept: &str,
        with_token: bool,
    ) -> Result<Vec<u8>, Error> {
        let url = format!("{}{}", self.inner.base_url, path);
        let rb = self.inner.http.request(method, url);
        let mut rb = if with_token {
            self.auth_headers(rb)
        } else {
            self.base_headers(rb)
        }
        .header(ACCEPT, accept);
        if let Some(b) = body {
            rb = rb.json(&b);
        }
        let resp = rb.send()?;
        let status = resp.status();
        let headers = resp.headers().clone();
        let bytes = resp.bytes()?;
        if !status.is_success() {
            return Err(Error::from_api(status.as_u16(), &headers, &bytes));
        }
        Ok(bytes.to_vec())
    }

    fn call<T: DeserializeOwned>(
        &self,
        method: Method,
        path: &str,
        body: Option<Value>,
    ) -> Result<T, Error> {
        let bytes = self.send(method, path, body, "application/json", true)?;
        Ok(serde_json::from_slice(&bytes)?)
    }

    /// The token's own access. See [`crate::Client::me`].
    pub fn me(&self) -> Result<Me, Error> {
        self.call(Method::GET, "/api/v1/me", None)
    }

    /// A service's usage guide: Markdown plus the structured guide.
    pub fn guide(&self, slug: &str) -> Result<Guide, Error> {
        self.call(Method::GET, &core::guide_path(slug), None)
    }

    /// A service's usage guide as Markdown.
    pub fn guide_markdown(&self, slug: &str) -> Result<String, Error> {
        let bytes = self.send(
            Method::GET,
            &core::guide_path(slug),
            None,
            "text/markdown",
            true,
        )?;
        Ok(String::from_utf8_lossy(&bytes).into_owned())
    }

    /// The capabilities this app may call.
    pub fn capabilities(&self) -> Result<Vec<Capability>, Error> {
        let out: core::CapabilitiesResponse =
            self.call(Method::GET, "/api/v1/capabilities", None)?;
        Ok(out.capabilities)
    }

    /// Call a capability. See [`crate::Client::capability`].
    pub fn capability<P: Serialize + ?Sized>(
        &self,
        slug: &str,
        params: &P,
        opts: CapabilityOptions,
    ) -> Result<Response, Error> {
        let url = core::capability_url(&self.inner.base_url, slug, &opts);
        let mut rb = self
            .auth_headers(self.inner.proxy_http.post(url))
            .json(params);
        if let Some(k) = opts.idempotency_key.as_deref().filter(|k| !k.is_empty()) {
            rb = rb.header("idempotency-key", k);
        }
        if opts.no_cache {
            rb = rb.header(reqwest::header::CACHE_CONTROL, "no-cache");
        }
        Ok(rb.send()?)
    }

    /// How to use the forward proxy. Holds no token.
    pub fn forward_proxy(&self) -> Result<ForwardProxy, Error> {
        self.call(Method::GET, "/api/v1/proxy", None)
    }

    /// The forward proxy's CA certificate as PEM text. No token is sent.
    pub fn forward_proxy_ca(&self) -> Result<String, Error> {
        let bytes = self.send(
            Method::GET,
            "/api/v1/proxy/ca.pem",
            None,
            "application/x-pem-file",
            false,
        )?;
        Ok(String::from_utf8_lossy(&bytes).into_owned())
    }

    /// Save the forward proxy's CA certificate to `path` (default
    /// [`DEFAULT_CA_PATH`]) and return the path.
    pub fn save_forward_proxy_ca(&self, path: Option<&Path>) -> Result<PathBuf, Error> {
        let pem = self.forward_proxy_ca()?;
        let path = path.unwrap_or(Path::new(DEFAULT_CA_PATH)).to_path_buf();
        std::fs::write(&path, pem)?;
        Ok(path)
    }

    /// The forward proxy URL with user `app` and this client's token as the
    /// password.
    ///
    /// The result contains the app token: keep it out of logs.
    pub fn forward_proxy_url(&self) -> Result<String, Error> {
        let fp = self.forward_proxy()?;
        core::forward_proxy_url(&fp.proxy_url, &self.inner.token)
    }

    /// The environment that sends an app's HTTP calls through the forward
    /// proxy. See [`crate::Client::forward_proxy_env`].
    ///
    /// The proxy variables contain the app token: keep them out of logs.
    pub fn forward_proxy_env(
        &self,
        ca_path: Option<&str>,
    ) -> Result<BTreeMap<String, String>, Error> {
        let url = self.forward_proxy_url()?;
        Ok(core::forward_proxy_env(
            &url,
            ca_path.unwrap_or(DEFAULT_CA_PATH),
        ))
    }

    /// A new blocking reqwest client that sends every request through the
    /// forward proxy and trusts the Acnologia CA. It carries the app token in
    /// its proxy settings.
    pub fn forward_proxy_client(&self) -> Result<reqwest::blocking::Client, Error> {
        let url = self.forward_proxy_url()?;
        let pem = self.forward_proxy_ca()?;
        Ok(reqwest::blocking::Client::builder()
            .proxy(reqwest::Proxy::all(url)?)
            .add_root_certificate(reqwest::Certificate::from_pem(pem.as_bytes())?)
            .build()?)
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
            idempotency_key: None,
            no_cache: false,
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
    idempotency_key: Option<String>,
    no_cache: bool,
}

impl Proxy {
    /// Send `Idempotency-Key` with the requests of this proxy.
    pub fn idempotency_key(mut self, key: impl Into<String>) -> Self {
        self.idempotency_key = Some(key.into());
        self
    }

    /// Send `Cache-Control: no-cache` with the requests of this proxy.
    pub fn no_cache(mut self) -> Self {
        self.no_cache = true;
        self
    }

    fn options(&self, mut rb: RequestBuilder) -> RequestBuilder {
        if let Some(k) = self.idempotency_key.as_deref().filter(|k| !k.is_empty()) {
            rb = rb.header("idempotency-key", k);
        }
        if self.no_cache {
            rb = rb.header(reqwest::header::CACHE_CONTROL, "no-cache");
        }
        rb
    }

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
        self.options(self.client.auth_headers(rb))
    }

    /// Send a GET with query parameters.
    pub fn get(&self, path: &str, query: &[(&str, &str)]) -> Result<Response, Error> {
        let rb = self.client.inner.proxy_http.get(self.url(path, query));
        Ok(self.options(self.client.auth_headers(rb)).send()?)
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
