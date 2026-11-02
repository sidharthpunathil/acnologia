//! A wiremock fake of the consumer API that replays the real response shapes.
#![allow(dead_code)]

use std::collections::VecDeque;
use std::sync::{Arc, Mutex};

use serde_json::{json, Value};
use wiremock::matchers::{header, method, path, path_regex};
use wiremock::{Mock, MockServer, Request, Respond, ResponseTemplate};

pub const TOKEN: &str = "acn_test";

pub fn lease_json(id: i64) -> Value {
    json!({
        "lease_id": format!("lse_{id}"),
        "key_id": id,
        "service": "serpapi",
        "secret": format!("sk{id}"),
        "auth": {"type": "query", "name": "api_key"},
        "base_url": "https://serpapi.com",
        "expires_at": "2026-11-01T12:00:00Z",
        "exclusive": false
    })
}

#[derive(Default)]
pub struct State {
    pub keys: VecDeque<i64>,
    pub busy: bool,
    /// When true, a rotating report answers `next: null`, so the client has
    /// to lease again itself.
    pub no_next: bool,
}

#[derive(Clone)]
pub struct Fake {
    pub state: Arc<Mutex<State>>,
}

struct LeaseResponder(Fake);
struct ReportResponder(Fake);

impl Respond for LeaseResponder {
    fn respond(&self, _req: &Request) -> ResponseTemplate {
        let mut s = self.0.state.lock().unwrap();
        if s.busy {
            return ResponseTemplate::new(503).insert_header("Retry-After", "1").set_body_json(json!({
                "error": {"code": "all_keys_busy", "message": "every usable key is at its concurrency limit, retry shortly"},
                "retry_after_sec": 1
            }));
        }
        match s.keys.pop_front() {
            Some(id) => ResponseTemplate::new(200).set_body_json(lease_json(id)),
            None => ResponseTemplate::new(503).insert_header("Retry-After", "42").set_body_json(json!({
                "error": {"code": "no_key_available", "message": "every key for this service is cooling down, exhausted or disabled"},
                "retry_after_sec": 42
            })),
        }
    }
}

impl Respond for ReportResponder {
    fn respond(&self, req: &Request) -> ResponseTemplate {
        let body: Value = serde_json::from_slice(&req.body).unwrap_or(Value::Null);
        let mut s = self.0.state.lock().unwrap();
        let rotate = body["rotate"] == json!(true) && body["outcome"] != json!("ok");
        let next = if rotate && !s.no_next {
            s.keys.pop_front().map(lease_json).unwrap_or(Value::Null)
        } else {
            Value::Null
        };
        ResponseTemplate::new(200).set_body_json(json!({"ok": true, "next": next}))
    }
}

/// Start a fake pool holding `keys`.
pub async fn start(keys: &[i64]) -> (MockServer, Fake) {
    let server = MockServer::start().await;
    let fake = Fake {
        state: Arc::new(Mutex::new(State {
            keys: keys.iter().copied().collect(),
            ..Default::default()
        })),
    };
    // Wrong token: the real server's 401 shape.
    Mock::given(path_regex("^/(api|proxy)/"))
        .respond_with(ResponseTemplate::new(401).set_body_json(json!({
            "error": {"code": "unauthenticated", "message": "invalid app token"}
        })))
        .with_priority(10)
        .mount(&server)
        .await;
    let authed = || header("authorization", format!("Bearer {TOKEN}").as_str());
    Mock::given(method("GET"))
        .and(path("/api/v1/services"))
        .and(authed())
        .respond_with(
            ResponseTemplate::new(200).set_body_json(json!({"services": [{
                "slug": "serpapi", "name": "SerpAPI", "provider": "serpapi", "category": "search",
                "available": true, "base_url": "https://serpapi.com",
                "auth": {"type": "query", "name": "api_key"}
            }, {
                "slug": "gh", "name": "GitHub", "provider": "github", "category": "dev",
                "available": false, "base_url": "https://api.github.com",
                "auth": {"type": "bearer", "name": "Authorization", "prefix": "token "}
            }]})),
        )
        .with_priority(2)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v1/lease"))
        .and(authed())
        .respond_with(LeaseResponder(fake.clone()))
        .with_priority(2)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path_regex(r"^/api/v1/leases/[^/]+/report$"))
        .and(authed())
        .respond_with(ReportResponder(fake.clone()))
        .with_priority(2)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v1/leases/lse_1/renew"))
        .and(authed())
        .respond_with(
            ResponseTemplate::new(200)
                .set_body_json(json!({"ok": true, "expires_at": "2026-11-01T13:00:00Z"})),
        )
        .with_priority(2)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path_regex(r"^/api/v1/leases/[^/]+/renew$"))
        .and(authed())
        .respond_with(ResponseTemplate::new(404).set_body_json(json!({
            "error": {"code": "lease_not_found", "message": "lease not found, already reported, or expired"}
        })))
        .with_priority(3)
        .mount(&server)
        .await;
    (server, fake)
}

/// Every request the server saw to `p` (exact path), as JSON bodies.
pub async fn bodies(server: &MockServer, p: &str) -> Vec<Value> {
    server
        .received_requests()
        .await
        .unwrap_or_default()
        .into_iter()
        .filter(|r| r.url.path() == p)
        .map(|r| serde_json::from_slice(&r.body).unwrap_or(Value::Null))
        .collect()
}

/// Every report body, with the lease id it was sent for.
pub async fn reports(server: &MockServer) -> Vec<(String, Value)> {
    server
        .received_requests()
        .await
        .unwrap_or_default()
        .into_iter()
        .filter(|r| r.url.path().ends_with("/report"))
        .map(|r| {
            let id = r.url.path().split('/').nth(4).unwrap_or("").to_string();
            (id, serde_json::from_slice(&r.body).unwrap_or(Value::Null))
        })
        .collect()
}

/// Mount a one-off error answer for `POST /api/v1/lease`, ahead of the pool.
pub async fn lease_error(server: &MockServer, status: u16, body: Value) {
    Mock::given(method("POST"))
        .and(path("/api/v1/lease"))
        .respond_with(ResponseTemplate::new(status).set_body_json(body))
        .with_priority(1)
        .mount(server)
        .await;
}
