//! The async client.

use std::collections::BTreeMap;
use std::fmt;
use std::future::Future;
use std::ops::Deref;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use reqwest::header::{ACCEPT, AUTHORIZATION, RETRY_AFTER, USER_AGENT};
use reqwest::Method;
use serde::de::DeserializeOwned;
use serde_json::Value;

use crate::core::{self, Redacted};
use crate::error::Error;
use crate::lease_info::LeaseInfo;
use crate::proxy::Proxy;
use crate::types::{
    Capability, CapabilityOptions, Credential, ForwardProxy, Guide, LeaseOptions, Me, Report,
    Service, WithKeyOptions,
};

/// Bounds API calls (services, lease, report, renew) by default. It is longer
/// than [`MAX_WAIT`](crate::MAX_WAIT) so a lease that waits for a free slot can
/// still succeed. Proxy calls have no default timeout.
pub const DEFAULT_TIMEOUT: Duration = Duration::from_secs(75);

/// Environment variable holding the server URL.
pub const ENV_URL: &str = "ACNOLOGIA_URL";
/// Environment variable holding the app token.
pub const ENV_TOKEN: &str = "ACNOLOGIA_TOKEN";
/// Environment variable holding the access password, read when the builder
/// sets none.
pub const ENV_ACCESS_PASSWORD: &str = "ACNOLOGIA_ACCESS_PASSWORD";
/// Where [`Client::save_forward_proxy_ca`] writes the CA by default.
pub const DEFAULT_CA_PATH: &str = core::DEFAULT_CA_PATH;

pub(crate) struct Inner {
    pub base_url: String,
    pub token: String,
    pub access_password: Option<String>,
    pub user_agent: String,
    pub http: reqwest::Client,
    pub proxy_http: reqwest::Client,
}

/// An async client for an Acnologia server, authenticated with an app token
/// (`acn_...`; tokens from before the rename, `cog_...`, still work).
///
/// Cloning is cheap and shares the connection pool.
///
/// ```no_run
/// # async fn run() -> Result<(), acnologia::Error> {
/// let acn = acnologia::Client::new("http://localhost:8080", "acn_your_app_token")?;
/// for s in acn.services().await? {
///     println!("{} available={}", s.slug, s.available);
/// }
/// # Ok(()) }
/// ```
#[derive(Clone)]
pub struct Client {
    pub(crate) inner: Arc<Inner>,
}

impl fmt::Debug for Client {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Client")
            .field("base_url", &self.inner.base_url)
            .field("token", &Redacted)
            .finish()
    }
}

/// Configures a [`Client`].
pub struct ClientBuilder {
    base_url: String,
    token: String,
    timeout: Option<Duration>,
    user_agent: Option<String>,
    access_password: Option<String>,
    http: Option<reqwest::Client>,
}

impl fmt::Debug for ClientBuilder {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ClientBuilder")
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

/// The access password: the one set on the builder, else
/// `ACNOLOGIA_ACCESS_PASSWORD`. Empty means none.
pub(crate) fn access_password(set: Option<String>) -> Option<String> {
    set.or_else(|| std::env::var(ENV_ACCESS_PASSWORD).ok())
        .map(|p| p.trim().to_string())
        .filter(|p| !p.is_empty())
}

impl ClientBuilder {
    /// Send `X-Acnologia-Access` with every request, for a server behind
    /// its access password. When not set, `ACNOLOGIA_ACCESS_PASSWORD` is
    /// used if present.
    pub fn access_password(mut self, password: impl Into<String>) -> Self {
        self.access_password = Some(password.into());
        self
    }

    /// Timeout for API calls. Default [`DEFAULT_TIMEOUT`]. Keep it longer
    /// than any lease wait.
    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }

    /// Override the User-Agent header.
    pub fn user_agent(mut self, ua: impl Into<String>) -> Self {
        self.user_agent = Some(ua.into());
        self
    }

    /// Use your own reqwest client for every call, proxy calls included. The
    /// SDK then applies no timeout and no redirect policy of its own; set
    /// `redirect::Policy::none()` on it so the app token is never replayed.
    pub fn http_client(mut self, http: reqwest::Client) -> Self {
        self.http = Some(http);
        self
    }

    /// Build the client.
    pub fn build(self) -> Result<Client, Error> {
        let (base_url, token) = validate(&self.base_url, &self.token)?;
        let (http, proxy_http) = match self.http {
            Some(h) => (h.clone(), h),
            None => {
                let api = reqwest::Client::builder()
                    .redirect(reqwest::redirect::Policy::none())
                    .timeout(self.timeout.unwrap_or(DEFAULT_TIMEOUT))
                    .build()?;
                let proxy = reqwest::Client::builder()
                    .redirect(reqwest::redirect::Policy::none())
                    .build()?;
                (api, proxy)
            }
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

pub(crate) fn validate(base_url: &str, token: &str) -> Result<(String, String), Error> {
    let base = base_url.trim().trim_end_matches('/').to_string();
    if base.is_empty() {
        return Err(Error::InvalidInput(format!(
            "base URL is required (or set {ENV_URL})"
        )));
    }
    url::Url::parse(&base).map_err(|e| Error::InvalidInput(format!("bad base URL: {e}")))?;
    let token = token.trim().to_string();
    if token.is_empty() {
        return Err(Error::InvalidInput(format!(
            "app token is required (or set {ENV_TOKEN})"
        )));
    }
    Ok((base, token))
}

pub(crate) fn env_pair() -> Result<(String, String), Error> {
    let base = std::env::var(ENV_URL).unwrap_or_default();
    let token = std::env::var(ENV_TOKEN).unwrap_or_default();
    validate(&base, &token)
}

impl Client {
    /// A client for the server at `base_url` (such as
    /// `http://localhost:8080`) using an app token.
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

    /// User agent and access password, no token.
    pub(crate) fn base_headers(&self, rb: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
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

    pub(crate) fn auth_headers(&self, rb: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
        let mut auth =
            reqwest::header::HeaderValue::from_str(&format!("Bearer {}", self.inner.token))
                .unwrap_or_else(|_| reqwest::header::HeaderValue::from_static(""));
        auth.set_sensitive(true);
        self.base_headers(rb.header(AUTHORIZATION, auth))
    }

    /// Send an API request and return the body of a 2xx answer.
    async fn send(
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
        let resp = rb.send().await?;
        let status = resp.status();
        let headers = resp.headers().clone();
        let bytes = resp.bytes().await?;
        if !status.is_success() {
            return Err(Error::from_api(status.as_u16(), &headers, &bytes));
        }
        Ok(bytes.to_vec())
    }

    async fn call<T: DeserializeOwned>(
        &self,
        method: Method,
        path: &str,
        body: Option<Value>,
    ) -> Result<T, Error> {
        let bytes = self
            .send(method, path, body, "application/json", true)
            .await?;
        Ok(serde_json::from_slice(&bytes)?)
    }

    /// The token's own access: its app, limits, use, services and, for a
    /// share's token, the share. A share that stopped still answers here,
    /// with its `status`.
    pub async fn me(&self) -> Result<Me, Error> {
        self.call(Method::GET, "/api/v1/me", None).await
    }

    /// A service's usage guide: Markdown plus the structured guide.
    pub async fn guide(&self, slug: &str) -> Result<Guide, Error> {
        self.call(Method::GET, &core::guide_path(slug), None).await
    }

    /// A service's usage guide as Markdown.
    pub async fn guide_markdown(&self, slug: &str) -> Result<String, Error> {
        let bytes = self
            .send(
                Method::GET,
                &core::guide_path(slug),
                None,
                "text/markdown",
                true,
            )
            .await?;
        Ok(String::from_utf8_lossy(&bytes).into_owned())
    }

    /// The capabilities this app may call.
    pub async fn capabilities(&self) -> Result<Vec<Capability>, Error> {
        let out: core::CapabilitiesResponse =
            self.call(Method::GET, "/api/v1/capabilities", None).await?;
        Ok(out.capabilities)
    }

    /// Call a capability: `POST /proxy/{slug}` with `params` as the JSON
    /// body. Like a proxy call, the answer comes back as is, so a non-2xx
    /// status is an `Ok(Response)`; [`Error::from_api`] turns one into a typed
    /// error. Read who served it with
    /// [`ProxyResponseExt`](crate::ProxyResponseExt).
    pub async fn capability<P: serde::Serialize + ?Sized>(
        &self,
        slug: &str,
        params: &P,
        opts: CapabilityOptions,
    ) -> Result<reqwest::Response, Error> {
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
        Ok(rb.send().await?)
    }

    /// How to use the forward proxy. Holds no token.
    pub async fn forward_proxy(&self) -> Result<ForwardProxy, Error> {
        self.call(Method::GET, "/api/v1/proxy", None).await
    }

    /// The forward proxy's CA certificate as PEM text. The route is public,
    /// so no token is sent.
    pub async fn forward_proxy_ca(&self) -> Result<String, Error> {
        let bytes = self
            .send(
                Method::GET,
                "/api/v1/proxy/ca.pem",
                None,
                "application/x-pem-file",
                false,
            )
            .await?;
        Ok(String::from_utf8_lossy(&bytes).into_owned())
    }

    /// Save the forward proxy's CA certificate to `path` (default
    /// [`DEFAULT_CA_PATH`]) and return the path.
    pub async fn save_forward_proxy_ca(&self, path: Option<&Path>) -> Result<PathBuf, Error> {
        let pem = self.forward_proxy_ca().await?;
        let path = path.unwrap_or(Path::new(DEFAULT_CA_PATH)).to_path_buf();
        std::fs::write(&path, pem)?;
        Ok(path)
    }

    /// The forward proxy URL with user `app` and this client's token as the
    /// password, such as `http://app:acn_...@host:8888`.
    ///
    /// The result contains the app token: keep it out of logs.
    pub async fn forward_proxy_url(&self) -> Result<String, Error> {
        let fp = self.forward_proxy().await?;
        core::forward_proxy_url(&fp.proxy_url, &self.inner.token)
    }

    /// The environment that sends an app's HTTP calls through the forward
    /// proxy: `HTTPS_PROXY` and `HTTP_PROXY` (the
    /// [`forward_proxy_url`](Client::forward_proxy_url)),
    /// `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`,
    /// `REQUESTS_CA_BUNDLE` and `SSL_CERT_FILE` set to `ca_path` (default
    /// [`DEFAULT_CA_PATH`]). Save the CA there with
    /// [`save_forward_proxy_ca`](Client::save_forward_proxy_ca).
    ///
    /// The proxy variables contain the app token: keep them out of logs.
    pub async fn forward_proxy_env(
        &self,
        ca_path: Option<&str>,
    ) -> Result<BTreeMap<String, String>, Error> {
        let url = self.forward_proxy_url().await?;
        Ok(core::forward_proxy_env(
            &url,
            ca_path.unwrap_or(DEFAULT_CA_PATH),
        ))
    }

    /// A new reqwest client that sends every request through the forward
    /// proxy with this client's token, and trusts the Acnologia CA next to
    /// the usual roots. Call provider URLs with it as they are; Acnologia
    /// injects the key.
    ///
    /// The client carries the app token in its proxy settings.
    pub async fn forward_proxy_client(&self) -> Result<reqwest::Client, Error> {
        let url = self.forward_proxy_url().await?;
        let pem = self.forward_proxy_ca().await?;
        Ok(reqwest::Client::builder()
            .proxy(reqwest::Proxy::all(url)?)
            .add_root_certificate(reqwest::Certificate::from_pem(pem.as_bytes())?)
            .build()?)
    }

    /// The services this app token may use.
    pub async fn services(&self) -> Result<Vec<Service>, Error> {
        let out: core::ServicesResponse = self.call(Method::GET, "/api/v1/services", None).await?;
        Ok(out.services)
    }

    /// Lease a key for `service`.
    ///
    /// Fails with [`Error::NoKeyAvailable`] when every key is cooling down or
    /// excluded, and [`Error::AllKeysBusy`] when every usable key is at its
    /// concurrency limit and the wait ran out.
    ///
    /// Always report the lease: on services with a concurrency limit it holds
    /// one of the key's slots until it is reported or its TTL expires.
    pub async fn lease(&self, service: &str, opts: LeaseOptions) -> Result<Lease, Error> {
        let body = core::lease_body(service, &opts)?;
        let w: core::LeaseWire = self.call(Method::POST, "/api/v1/lease", Some(body)).await?;
        Ok(self.wrap(w))
    }

    fn wrap(&self, w: core::LeaseWire) -> Lease {
        Lease {
            info: LeaseInfo::from_wire(w),
            client: self.clone(),
        }
    }

    /// [`Client::with_key_opts`] with default options (5 attempts).
    pub async fn with_key<T, F, Fut>(&self, service: &str, f: F) -> Result<T, Error>
    where
        F: FnMut(Lease) -> Fut,
        Fut: Future<Output = Result<T, Error>>,
    {
        self.with_key_opts(service, WithKeyOptions::default(), f)
            .await
    }

    /// Lease a key for `service`, call `f` with it, and report the outcome.
    ///
    /// - `f` returns `Ok`: the lease is reported `ok` and the value returned
    ///   (or the report error, if the report fails).
    /// - `f` returns [`Error::RateLimited`], [`Error::QuotaExhausted`] or
    ///   [`Error::InvalidKey`]: the outcome is reported with `rotate` and `f`
    ///   runs again with the next key, up to `max_attempts`. When attempts run
    ///   out, the last such error is returned; the final attempt is reported
    ///   without asking for a next key.
    /// - `f` returns any other error: the lease is reported `error` and the
    ///   error is returned unchanged.
    ///
    /// If the pool has no further key, [`Error::NoKeyAvailable`] or
    /// [`Error::AllKeysBusy`] is returned. Messages sent with a report have
    /// the secret scrubbed out.
    ///
    /// Every lease is reported, also when `f` panics or the future returned
    /// by `with_key` is dropped while `f` runs. In those two cases the report
    /// is spawned on the current Tokio runtime, best effort.
    ///
    /// ```no_run
    /// # async fn run(acn: acnologia::Client) -> Result<(), acnologia::Error> {
    /// let http = reqwest::Client::new();
    /// let body: String = acn
    ///     .with_key("serpapi", |lease| {
    ///         let http = http.clone();
    ///         async move {
    ///             let req = http.get(lease.url("/search.json", &[("q", "coffee")]));
    ///             let resp = lease.apply(req).send().await?;
    ///             let resp = acnologia::check_status(resp)?; // 429, 402, 401, 403 rotate
    ///             Ok(resp.text().await?)
    ///         }
    ///     })
    ///     .await?;
    /// # Ok(()) }
    /// ```
    pub async fn with_key_opts<T, F, Fut>(
        &self,
        service: &str,
        opts: WithKeyOptions,
        mut f: F,
    ) -> Result<T, Error>
    where
        F: FnMut(Lease) -> Fut,
        Fut: Future<Output = Result<T, Error>>,
    {
        let max = opts.max_attempts.max(1);
        let mut tried = opts.lease.exclude_key_ids.clone();
        let mut lease = self.lease(service, opts.lease.clone()).await?;
        let mut last_err: Option<Error> = None;
        for attempt in 1..=max {
            let mut guard = ReportGuard {
                lease: Some(lease.clone()),
            };
            let res = f(lease.clone()).await;
            guard.lease = None;
            let err = match res {
                Ok(v) => {
                    lease.report(Report::ok()).await?;
                    return Ok(v);
                }
                Err(e) => e,
            };
            let (mut report, rotate) = core::classify(&err, lease.secret());
            if !rotate {
                let _ = lease.report(report).await;
                return Err(err);
            }
            tried.push(lease.key_id);
            if attempt == max {
                report.rotate = false;
                let _ = lease.report(report).await;
                last_err = Some(err);
                break;
            }
            let next = lease.report(report).await?;
            last_err = Some(err);
            lease = match next {
                Some(n) => n,
                None => {
                    let o = LeaseOptions {
                        exclude_key_ids: tried.clone(),
                        ..opts.lease.clone()
                    };
                    self.lease(service, o).await?
                }
            };
        }
        Err(last_err.unwrap_or_else(|| Error::InvalidInput("with_key made no attempt".into())))
    }

    /// A proxy helper for `service`: requests go to `/proxy/{service}/...`,
    /// and Acnologia injects a key and rotates on rate limits.
    pub fn proxy(&self, service: impl Into<String>) -> Proxy {
        Proxy::new(self.clone(), service.into())
    }
}

/// Reports a lease as `error` if dropped while still armed: the callback
/// panicked or the with_key future was dropped.
struct ReportGuard {
    lease: Option<Lease>,
}

impl Drop for ReportGuard {
    fn drop(&mut self) {
        let Some(lease) = self.lease.take() else {
            return;
        };
        let msg = if std::thread::panicking() {
            "with_key callback panicked"
        } else {
            "with_key cancelled before the callback returned"
        };
        if let Ok(handle) = tokio::runtime::Handle::try_current() {
            handle.spawn(async move {
                let _ = lease.report(Report::error().message(msg)).await;
            });
        }
    }
}

/// A key handed out by the pool. Report its outcome when done.
///
/// Derefs to [`LeaseInfo`] for its fields. `Debug` and `Display` redact the
/// secret.
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

    /// Put the secret on a request where the provider expects it (header,
    /// bearer or query, from [`LeaseInfo::auth`]). A query parameter is
    /// appended; use [`LeaseInfo::apply_to`] on a built request to replace one.
    pub fn apply(&self, rb: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
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
    /// lease when another key is available, otherwise `None`.
    pub async fn report(&self, report: Report) -> Result<Option<Lease>, Error> {
        let body = core::report_body(&report);
        let out: core::ReportResponse = self
            .client
            .call(Method::POST, &core::report_path(&self.lease_id), Some(body))
            .await?;
        Ok(out.next.map(|w| self.client.wrap(w)))
    }

    /// Extend this open lease. `None` uses the server default (120s). Returns
    /// the new expiry as RFC 3339 text. Fails with an [`Error::Api`] of code
    /// `lease_not_found` when the lease is no longer open.
    pub async fn renew(&self, ttl: Option<Duration>) -> Result<String, Error> {
        let out: core::RenewResponse = self
            .client
            .call(
                Method::POST,
                &core::renew_path(&self.lease_id),
                Some(core::renew_body(ttl)),
            )
            .await?;
        Ok(out.expires_at)
    }
}

/// Turn a provider response with a rotation status into the matching error
/// (429 rate limited with its `Retry-After`, 402 quota exhausted, 401 and 403
/// invalid key), and pass every other response through. Use it inside a
/// `with_key` callback.
pub fn check_status(resp: reqwest::Response) -> Result<reqwest::Response, Error> {
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
