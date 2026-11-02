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
/// `Ok(Response)`, not an error. Responses carry `X-Acnologia-Key-Id`,
/// `X-Acnologia-Attempts` and `X-Acnologia-Service`. Redirects are not
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
/// # Ok(()) }
/// ```
#[derive(Debug, Clone)]
pub struct Proxy {
    client: Client,
    service: String,
}

impl Proxy {
    pub(crate) fn new(client: Client, service: String) -> Self {
        Proxy { client, service }
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

    /// A request builder for `method` and `path`, with the app token and user
    /// agent set. Add a body or headers and call `send`.
    pub fn request(&self, method: Method, path: &str) -> reqwest::RequestBuilder {
        let rb = self
            .client
            .inner
            .proxy_http
            .request(method, self.url(path, &[]));
        self.client.auth_headers(rb)
    }

    /// Send a GET with query parameters.
    pub async fn get(
        &self,
        path: &str,
        query: &[(&str, &str)],
    ) -> Result<reqwest::Response, Error> {
        let rb = self.client.inner.proxy_http.get(self.url(path, query));
        Ok(self.client.auth_headers(rb).send().await?)
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
