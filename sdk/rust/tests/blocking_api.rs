#![cfg(feature = "blocking")]

mod common;

use std::time::Duration;

use acnologia::blocking::Client;
use acnologia::{CapabilityOptions, Error, LeaseOptions, ProxyResponseExt, Report, WithKeyOptions};
use common::{reports, requests, start, CA_PEM, TOKEN};
use serde_json::json;

/// The mock server runs on its own thread; the runtime only drives setup and
/// inspection, never the blocking calls.
fn rt() -> tokio::runtime::Runtime {
    tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .unwrap()
}

#[test]
fn blocking_lease_report_and_services() {
    let rt = rt();
    let (server, _) = rt.block_on(start(&[1, 2]));
    let c = Client::new(server.uri(), TOKEN).unwrap();
    assert_eq!(c.services().unwrap().len(), 2);
    let lease = c.lease("serpapi", LeaseOptions::new()).unwrap();
    assert_eq!(lease.secret(), "sk1");
    assert!(!format!("{lease:?} {lease} {c:?}").contains("sk1"));
    assert!(!format!("{c:?}").contains(TOKEN));
    assert_eq!(lease.renew(None).unwrap(), "2026-11-01T13:00:00Z");
    let next = lease
        .report(Report::rate_limited(Some(Duration::from_secs(9))).rotate())
        .unwrap()
        .unwrap();
    assert_eq!(next.key_id, 2);
    let err = next.renew(None).unwrap_err();
    assert_eq!(err.code(), Some("lease_not_found"));
}

#[test]
fn blocking_with_key_rotates_and_gives_up() {
    let rt = rt();
    let (server, _) = rt.block_on(start(&[1, 2, 3]));
    let c = Client::new(server.uri(), TOKEN).unwrap();
    let out = c
        .with_key("serpapi", |lease| {
            if lease.key_id < 2 {
                Err(Error::rate_limited(None))
            } else {
                Ok(lease.key_id)
            }
        })
        .unwrap();
    assert_eq!(out, 2);

    let err = c
        .with_key_opts("serpapi", WithKeyOptions::new().max_attempts(2), |_| {
            Err::<(), _>(Error::quota_exhausted())
        })
        .unwrap_err();
    // Key 3 rotates, then the pool is empty.
    assert!(err.is_no_key_available(), "{err:?}");

    let r = rt.block_on(reports(&server));
    assert_eq!(r[0].1, json!({"outcome": "rate_limited", "rotate": true}));
    assert_eq!(r[1].1, json!({"outcome": "ok"}));
    assert_eq!(
        r[2].1,
        json!({"outcome": "quota_exhausted", "rotate": true})
    );
}

#[test]
fn blocking_with_key_reports_on_panic() {
    let rt = rt();
    let (server, _) = rt.block_on(start(&[1]));
    let c = Client::new(server.uri(), TOKEN).unwrap();
    let res = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        let _ = c.with_key("serpapi", |_| -> Result<(), Error> { panic!("boom") });
    }));
    assert!(res.is_err());
    let r = rt.block_on(reports(&server));
    assert_eq!(r.len(), 1);
    assert_eq!(r[0].1["outcome"], json!("error"));
}

#[test]
fn blocking_proxy() {
    let rt = rt();
    let (server, _) = rt.block_on(start(&[]));
    rt.block_on(
        wiremock::Mock::given(wiremock::matchers::path_regex("^/proxy/serpapi/"))
            .respond_with(wiremock::ResponseTemplate::new(429))
            .with_priority(1)
            .mount(&server),
    );
    let c = Client::new(server.uri(), TOKEN).unwrap();
    let resp = c
        .proxy("serpapi")
        .get("/search.json", &[("q", "x")])
        .unwrap();
    assert_eq!(resp.status(), 429);
    let reqs = rt.block_on(server.received_requests()).unwrap();
    assert_eq!(
        reqs[0].headers["authorization"],
        format!("Bearer {TOKEN}").as_str()
    );
}

#[test]
fn blocking_v2_calls() {
    let rt = rt();
    let (server, _) = rt.block_on(start(&[]));
    let c = Client::builder(server.uri(), TOKEN)
        .access_password("pw")
        .build()
        .unwrap();

    let me = c.me().unwrap();
    assert_eq!(me.app.name, "crawler");
    assert_eq!(me.limits.daily_requests, Some(1000));

    let g = c.guide("serpapi").unwrap();
    assert!(g.generated);
    assert_eq!(
        c.guide_markdown("serpapi").unwrap(),
        "# SerpAPI\n\nSearch Google.\n"
    );

    let caps = c.capabilities().unwrap();
    assert_eq!(caps[0].slug, "web-search");
    let resp = c
        .capability(
            "web-search",
            &json!({"q": "tea"}),
            CapabilityOptions::new().service("serpapi").no_cache(),
        )
        .unwrap();
    assert_eq!(resp.capability(), Some("web-search"));
    assert_eq!(resp.tried().len(), 2);
    assert_eq!(resp.key_id(), Some(4));

    let fp = c.forward_proxy().unwrap();
    assert_eq!(fp.hosts[0].host, "serpapi.com");
    assert_eq!(c.forward_proxy_ca().unwrap(), CA_PEM);
    let url = c.forward_proxy_url().unwrap();
    assert_eq!(url, format!("http://app:{TOKEN}@acn.example:8888"));
    let env = c.forward_proxy_env(Some("ca.pem")).unwrap();
    assert_eq!(env["HTTP_PROXY"], url);
    assert_eq!(env["REQUESTS_CA_BUNDLE"], "ca.pem");

    c.proxy("serpapi")
        .idempotency_key("k9")
        .no_cache()
        .get("/search.json", &[])
        .unwrap();

    let cap = &rt.block_on(requests(&server, "/proxy/web-search"))[0];
    assert_eq!(cap.url.query(), Some("_service=serpapi"));
    assert_eq!(cap.headers["cache-control"], "no-cache");
    let ca = &rt.block_on(requests(&server, "/api/v1/proxy/ca.pem"))[0];
    assert!(ca.headers.get("authorization").is_none());
    let px = &rt.block_on(requests(&server, "/proxy/serpapi/search.json"))[0];
    assert_eq!(px.headers["idempotency-key"], "k9");
    for r in rt.block_on(server.received_requests()).unwrap() {
        assert_eq!(r.headers["x-acnologia-access"], "pw");
    }
}
