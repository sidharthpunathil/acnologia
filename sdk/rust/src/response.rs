//! Accessors for the `X-Acnologia-*` headers on proxy and capability answers.

use reqwest::header::HeaderMap;

use crate::core;
use crate::types::Tried;

/// Reads what Acnologia adds to a proxy or capability answer. Implemented for
/// `reqwest::Response`, `reqwest::blocking::Response` (with the `blocking`
/// feature) and `HeaderMap`.
///
/// ```no_run
/// use acnologia::ProxyResponseExt;
/// # async fn run(acn: acnologia::Client) -> Result<(), acnologia::Error> {
/// let resp = acn.proxy("serpapi").get("/search.json", &[("q", "x")]).await?;
/// println!("key {:?}, {:?} attempts, cache {:?}", resp.key_id(), resp.attempts(), resp.cache());
/// # Ok(()) }
/// ```
pub trait ProxyResponseExt {
    /// The answer's headers.
    fn acnologia_headers(&self) -> &HeaderMap;

    /// `X-Acnologia-Key-Id`: the key that served the call. `None` for a
    /// share's token.
    fn key_id(&self) -> Option<i64> {
        header(self.acnologia_headers(), "x-acnologia-key-id").and_then(|v| v.parse().ok())
    }

    /// `X-Acnologia-Attempts`: how many keys were tried.
    fn attempts(&self) -> Option<u32> {
        header(self.acnologia_headers(), "x-acnologia-attempts").and_then(|v| v.parse().ok())
    }

    /// `X-Acnologia-Service`: the service that answered.
    fn service(&self) -> Option<&str> {
        header(self.acnologia_headers(), "x-acnologia-service")
    }

    /// `X-Acnologia-Cache`: `hit`, `miss` and so on, when caching applies.
    fn cache(&self) -> Option<&str> {
        header(self.acnologia_headers(), "x-acnologia-cache")
    }

    /// True when `Idempotent-Replayed: true`: the answer is a replay of an
    /// earlier call with the same idempotency key.
    fn replayed(&self) -> bool {
        header(self.acnologia_headers(), "idempotent-replayed")
            .is_some_and(|v| v.eq_ignore_ascii_case("true"))
    }

    /// `X-Acnologia-Capability`: the capability that ran.
    fn capability(&self) -> Option<&str> {
        header(self.acnologia_headers(), "x-acnologia-capability")
    }

    /// `X-Acnologia-Tried`: every route a capability call tried, in order.
    /// Empty when the header is absent.
    fn tried(&self) -> Vec<Tried> {
        header(self.acnologia_headers(), "x-acnologia-tried")
            .map(core::parse_tried)
            .unwrap_or_default()
    }
}

fn header<'a>(h: &'a HeaderMap, name: &str) -> Option<&'a str> {
    h.get(name)
        .and_then(|v| v.to_str().ok())
        .map(str::trim)
        .filter(|v| !v.is_empty())
}

impl ProxyResponseExt for HeaderMap {
    fn acnologia_headers(&self) -> &HeaderMap {
        self
    }
}

impl ProxyResponseExt for reqwest::Response {
    fn acnologia_headers(&self) -> &HeaderMap {
        self.headers()
    }
}

#[cfg(feature = "blocking")]
impl ProxyResponseExt for reqwest::blocking::Response {
    fn acnologia_headers(&self) -> &HeaderMap {
        self.headers()
    }
}
