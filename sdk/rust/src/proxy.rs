//! Proxy mode for the async client.

use reqwest::Method;
use serde::Serialize;

use crate::client::Client;
use crate::core;
use crate::error::Error;

/// Sends requests through Acnologia's proxy for one service. Acnologia
/// injects a key, retries on rate limits with the next key, and never returns
/// the secret.
///
/// Upstream answers come back unchanged: a non-2xx response is a normal
/// `Ok(Response)`, not an error ([`Error::from_api`] turns one from
/// Acnologia into a typed error). Responses carry `X-Acnologia-Key-Id`,
/// `X-Acnologia-Attempts` and `X-Acnologia-Service`, read with
/// [`ProxyResponseExt`](crate::ProxyResponseExt). Redirects are not
/// followed, and there is no default timeout because proxied bodies may
/// stream; use `RequestBuilder::timeout` to bound a call.
///
/// ```no_run
/// # async fn run(acn: acnologia::Client) -> Result<(), acnologia::Error> {
/// let resp = acn
///     .proxy("serpapi")
///     .get("/search.json", &[("engine", "google"), ("q", "coffee")])
///     .await?;
/// println!("{} via key {:?}", resp.status(), resp.headers().get("x-acnologia-key-id"));
///
/// // Per call options: a fresh answer, sent at most once.
/// let resp = acn
///     .proxy("openai")
///     .no_cache()
///     .idempotency_key("order-42")
///     .post_json("/v1/responses", &serde_json::json!({"input": "hi"}))
///     .await?;
/// # Ok(()) }
/// ```
#[derive(Debug, Clone)]
pub struct Proxy {
    client: Client,
    service: String,
    idempotency_key: Option<String>,
    no_cache: bool,
}

impl Proxy {
    pub(crate) fn new(client: Client, service: String) -> Self {
        Proxy {
            client,
            service,
            idempotency_key: None,
            no_cache: false,
        }
    }

    /// Send `Idempotency-Key` with the requests of this proxy: a retry with
    /// the same key gets the first answer replayed instead of calling the
    /// provider again.
    pub fn idempotency_key(mut self, key: impl Into<String>) -> Self {
        self.idempotency_key = Some(key.into());
        self
    }

    /// Send `Cache-Control: no-cache` with the requests of this proxy, so
    /// Acnologia skips its response cache.
    pub fn no_cache(mut self) -> Self {
        self.no_cache = true;
        self
    }

    fn options(&self, mut rb: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
        if let Some(k) = self.idempotency_key.as_deref().filter(|k| !k.is_empty()) {
            rb = rb.header("idempotency-key", k);
        }
        if self.no_cache {
            rb = rb.header(reqwest::header::CACHE_CONTROL, "no-cache");
        }
        rb
    }

    /// The service slug (or group) this proxy targets.
    pub fn service(&self) -> &str {
        &self.service
    }

    /// The full proxy URL for a provider path and query, such as
    /// `http://localhost:8080/proxy/serpapi/search.json?q=x`. The app token is
    /// not in it.
    pub fn url(&self, path: &str, query: &[(&str, &str)]) -> String {
        core::proxy_url(&self.client.inner.base_url, &self.service, path, query)
    }

    /// A request builder for `method` and `path`, with the app token, user
    /// agent and proxy options set. Add a body or headers and call `send`.
    pub fn request(&self, method: Method, path: &str) -> reqwest::RequestBuilder {
        let rb = self
            .client
            .inner
            .proxy_http
            .request(method, self.url(path, &[]));
        self.options(self.client.auth_headers(rb))
    }

    /// Send a GET with query parameters.
    pub async fn get(
        &self,
        path: &str,
        query: &[(&str, &str)],
    ) -> Result<reqwest::Response, Error> {
        let rb = self.client.inner.proxy_http.get(self.url(path, query));
        Ok(self.options(self.client.auth_headers(rb)).send().await?)
    }

    /// Send a POST with a body and content type.
    pub async fn post(
        &self,
        path: &str,
        content_type: &str,
        body: impl Into<reqwest::Body>,
    ) -> Result<reqwest::Response, Error> {
        Ok(self
            .request(Method::POST, path)
            .header(reqwest::header::CONTENT_TYPE, content_type)
            .body(body)
            .send()
            .await?)
    }

    /// Encode `body` as JSON and POST it.
    pub async fn post_json<B: Serialize + ?Sized>(
        &self,
        path: &str,
        body: &B,
    ) -> Result<reqwest::Response, Error> {
        Ok(self.request(Method::POST, path).json(body).send().await?)
    }
}
