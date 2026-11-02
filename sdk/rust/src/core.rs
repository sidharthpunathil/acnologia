//! Logic shared by the async and blocking clients: request bodies, URL
//! building, secret scrubbing and the with_key classification.

use std::time::Duration;

use percent_encoding::{utf8_percent_encode, AsciiSet, CONTROLS};
use serde::Deserialize;
use serde_json::{json, Map, Value};

use crate::error::Error;
use crate::types::{Auth, AuthType, Credential, LeaseOptions, Outcome, Report, MAX_WAIT};

/// The User-Agent sent with every request.
pub(crate) const USER_AGENT: &str = concat!("acnologia-rust/", env!("CARGO_PKG_VERSION"));

/// Caps the message sent with a report built from an error.
pub(crate) const MAX_REPORT_MESSAGE: usize = 500;

/// Prints `[redacted]` in Debug output.
pub(crate) struct Redacted;

impl std::fmt::Debug for Redacted {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("\"[redacted]\"")
    }
}

/// Characters escaped in one path segment, like Go's url.PathEscape.
const SEGMENT: &AsciiSet = &CONTROLS
    .add(b' ')
    .add(b'"')
    .add(b'#')
    .add(b'%')
    .add(b'/')
    .add(b'<')
    .add(b'>')
    .add(b'?')
    .add(b'`')
    .add(b'{')
    .add(b'}')
    .add(b'\\')
    .add(b'^')
    .add(b'|');

pub(crate) fn escape_segment(s: &str) -> String {
    utf8_percent_encode(s, SEGMENT).to_string()
}

/// Cut `s` to at most `max` bytes on a char boundary.
pub(crate) fn truncate(s: &str, max: usize) -> &str {
    if s.len() <= max {
        return s;
    }
    let mut end = max;
    while !s.is_char_boundary(end) {
        end -= 1;
    }
    &s[..end]
}

/// Replace the secret (raw and query escaped) with `[key]` and truncate.
/// Errors from query auth services often carry the full request URL, key
/// included, and a report must not copy it to the server.
pub(crate) fn scrub_message(msg: &str, secret: &str) -> String {
    let mut out = msg.to_string();
    if !secret.is_empty() {
        out = out.replace(secret, "[key]");
        let form: String = url::form_urlencoded::byte_serialize(secret.as_bytes()).collect();
        if form != secret {
            out = out.replace(&form, "[key]");
        }
        let pct = escape_segment(secret);
        if pct != secret {
            out = out.replace(&pct, "[key]");
        }
    }
    truncate(&out, MAX_REPORT_MESSAGE).to_string()
}

pub(crate) fn ceil_secs(d: Duration) -> u64 {
    let s = d.as_secs();
    if d.subsec_nanos() > 0 {
        s + 1
    } else {
        s
    }
}

pub(crate) fn lease_body(service: &str, opts: &LeaseOptions) -> Result<Value, Error> {
    let mut body = Map::new();
    body.insert("service".into(), json!(service));
    if !opts.exclude_key_ids.is_empty() {
        body.insert("exclude_key_ids".into(), json!(opts.exclude_key_ids));
    }
    if let Some(ttl) = opts.ttl.filter(|t| !t.is_zero()) {
        body.insert("ttl_sec".into(), json!(ceil_secs(ttl)));
    }
    if let Some(wait) = opts.wait {
        if wait > MAX_WAIT {
            return Err(Error::InvalidInput(format!(
                "lease wait must be between 0 and {}s",
                MAX_WAIT.as_secs()
            )));
        }
        if !wait.is_zero() {
            body.insert("wait_sec".into(), json!(ceil_secs(wait)));
        }
    }
    if opts.exclusive {
        body.insert("exclusive".into(), json!(true));
    }
    Ok(Value::Object(body))
}

pub(crate) fn report_body(r: &Report) -> Value {
    let mut body = Map::new();
    body.insert("outcome".into(), json!(r.outcome.as_str()));
    if let Some(code) = r.status_code.filter(|c| *c != 0) {
        body.insert("status_code".into(), json!(code));
    }
    if let Some(ra) = r.retry_after.filter(|d| !d.is_zero()) {
        body.insert("retry_after_sec".into(), json!(ceil_secs(ra)));
    }
    if let Some(m) = r.message.as_deref().filter(|m| !m.is_empty()) {
        body.insert("message".into(), json!(m));
    }
    if r.rotate {
        body.insert("rotate".into(), json!(true));
    }
    Value::Object(body)
}

pub(crate) fn renew_body(ttl: Option<Duration>) -> Value {
    match ttl.filter(|t| !t.is_zero()) {
        Some(t) => json!({ "ttl_sec": ceil_secs(t) }),
        None => json!({}),
    }
}

/// A lease as the server sends it.
#[derive(Deserialize)]
pub(crate) struct LeaseWire {
    pub lease_id: String,
    pub key_id: i64,
    #[serde(default)]
    pub service: String,
    pub secret: String,
    #[serde(default)]
    pub auth: Auth,
    #[serde(default)]
    pub base_url: String,
    #[serde(default)]
    pub expires_at: String,
    #[serde(default)]
    pub exclusive: bool,
}

#[derive(Deserialize)]
pub(crate) struct ReportResponse {
    #[serde(default)]
    pub next: Option<LeaseWire>,
}

#[derive(Deserialize)]
pub(crate) struct RenewResponse {
    #[serde(default)]
    pub expires_at: String,
}

#[derive(Deserialize)]
pub(crate) struct ServicesResponse {
    #[serde(default)]
    pub services: Vec<crate::types::Service>,
}

pub(crate) fn report_path(lease_id: &str) -> String {
    format!("/api/v1/leases/{}/report", escape_segment(lease_id))
}

pub(crate) fn renew_path(lease_id: &str) -> String {
    format!("/api/v1/leases/{}/renew", escape_segment(lease_id))
}

/// Where the secret goes for this auth spec.
pub(crate) fn credential(auth: &Auth, secret: &str) -> Credential {
    let value = format!("{}{}", auth.prefix, secret);
    let header_name = || {
        if auth.name.is_empty() {
            "Authorization".to_string()
        } else {
            auth.name.clone()
        }
    };
    match auth.kind {
        AuthType::Query => Credential::Query {
            name: if auth.name.is_empty() {
                "api_key".to_string()
            } else {
                auth.name.clone()
            },
            value,
        },
        AuthType::Bearer => Credential::Header {
            name: header_name(),
            value: if auth.prefix.is_empty() {
                format!("Bearer {secret}")
            } else {
                value
            },
        },
        AuthType::Header | AuthType::Other => Credential::Header {
            name: header_name(),
            value,
        },
    }
}

pub(crate) fn encode_query(q: &[(&str, &str)]) -> String {
    let mut s = url::form_urlencoded::Serializer::new(String::new());
    for (k, v) in q {
        s.append_pair(k, v);
    }
    s.finish()
}

/// `base` joined with `path` and an encoded query.
pub(crate) fn join_url(base: &str, path: &str, q: &[(&str, &str)]) -> String {
    let mut u = format!(
        "{}/{}",
        base.trim_end_matches('/'),
        path.trim_start_matches('/')
    );
    if !q.is_empty() {
        u.push(if u.contains('?') { '&' } else { '?' });
        u.push_str(&encode_query(q));
    }
    u
}

/// The proxy URL for a provider path. A path that already starts with
/// `/proxy/{service}` is kept as is.
pub(crate) fn proxy_url(base: &str, service: &str, path: &str, q: &[(&str, &str)]) -> String {
    let svc = format!("/proxy/{}", escape_segment(service));
    let mut p = path;
    if p == svc {
        p = "";
    } else if let Some(rest) = p.strip_prefix(&format!("{svc}/")) {
        p = rest;
    }
    join_url(&format!("{base}{svc}"), p, q)
}

/// What with_key reports for a callback error, and whether it rotates.
pub(crate) fn classify(err: &Error, secret: &str) -> (Report, bool) {
    let scrub = |m: &Option<String>| {
        m.as_deref()
            .filter(|m| !m.is_empty())
            .map(|m| scrub_message(m, secret))
    };
    match err {
        Error::RateLimited {
            retry_after,
            status_code,
            message,
        } => (
            Report {
                outcome: Outcome::RateLimited,
                status_code: *status_code,
                retry_after: *retry_after,
                message: scrub(message),
                rotate: true,
            },
            true,
        ),
        Error::QuotaExhausted {
            status_code,
            message,
        } => (
            Report {
                outcome: Outcome::QuotaExhausted,
                status_code: *status_code,
                message: scrub(message),
                rotate: true,
                ..Default::default()
            },
            true,
        ),
        Error::InvalidKey {
            status_code,
            message,
        } => (
            Report {
                outcome: Outcome::Invalid,
                status_code: *status_code,
                message: scrub(message),
                rotate: true,
                ..Default::default()
            },
            true,
        ),
        other => (
            Report {
                outcome: Outcome::Error,
                message: Some(scrub_message(&other.to_string(), secret)),
                ..Default::default()
            },
            false,
        ),
    }
}

/// Parse a `Retry-After` header given in seconds.
pub(crate) fn retry_after_header(v: Option<&str>) -> Option<Duration> {
    v.and_then(|s| s.trim().parse::<u64>().ok())
        .filter(|s| *s > 0)
        .map(Duration::from_secs)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn scrub() {
        let s = scrub_message("GET https://x/?api_key=a%2Fb+c and a/b c", "a/b c");
        assert_eq!(s, "GET https://x/?api_key=[key] and [key]");
        let long = "é".repeat(400);
        assert!(scrub_message(&long, "").len() <= MAX_REPORT_MESSAGE);
    }

    #[test]
    fn credentials() {
        let a = |kind, name: &str, prefix: &str| Auth {
            kind,
            name: name.into(),
            prefix: prefix.into(),
        };
        assert_eq!(
            credential(&a(AuthType::Query, "api_key", ""), "s"),
            Credential::Query {
                name: "api_key".into(),
                value: "s".into()
            }
        );
        assert_eq!(
            credential(&a(AuthType::Header, "xi-api-key", ""), "s"),
            Credential::Header {
                name: "xi-api-key".into(),
                value: "s".into()
            }
        );
        assert_eq!(
            credential(&a(AuthType::Bearer, "", ""), "s"),
            Credential::Header {
                name: "Authorization".into(),
                value: "Bearer s".into()
            }
        );
        assert_eq!(
            credential(&a(AuthType::Bearer, "", "Token "), "s"),
            Credential::Header {
                name: "Authorization".into(),
                value: "Token s".into()
            }
        );
        assert_eq!(
            credential(&a(AuthType::Header, "", "Key "), "s"),
            Credential::Header {
                name: "Authorization".into(),
                value: "Key s".into()
            }
        );
        let dbg = format!(
            "{:?}",
            credential(&a(AuthType::Bearer, "", ""), "topsecret")
        );
        assert!(!dbg.contains("topsecret"), "{dbg}");
    }

    #[test]
    fn urls() {
        assert_eq!(
            join_url("https://serpapi.com/", "/search.json", &[("q", "a b")]),
            "https://serpapi.com/search.json?q=a+b"
        );
        assert_eq!(
            proxy_url("http://h", "serp api", "search.json", &[]),
            "http://h/proxy/serp%20api/search.json"
        );
        assert_eq!(
            proxy_url(
                "http://h",
                "serpapi",
                "/proxy/serpapi/search.json",
                &[("q", "x")]
            ),
            "http://h/proxy/serpapi/search.json?q=x"
        );
    }

    #[test]
    fn bodies() {
        let b = lease_body(
            "s",
            &LeaseOptions::new()
                .ttl(Duration::from_millis(1500))
                .wait(Duration::from_secs(10))
                .exclude([3]),
        )
        .unwrap();
        assert_eq!(
            b,
            json!({"service":"s","ttl_sec":2,"wait_sec":10,"exclude_key_ids":[3]})
        );
        assert!(lease_body("s", &LeaseOptions::new().wait(Duration::from_secs(61))).is_err());
        let r = report_body(
            &Report::rate_limited(Some(Duration::from_secs(60)))
                .status(429)
                .rotate(),
        );
        assert_eq!(
            r,
            json!({"outcome":"rate_limited","status_code":429,"retry_after_sec":60,"rotate":true})
        );
    }
}
