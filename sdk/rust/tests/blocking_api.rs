#![cfg(feature = "blocking")]

mod common;

use std::time::Duration;

use acnologia::blocking::Client;
use acnologia::{Error, LeaseOptions, Report, WithKeyOptions};
use common::{reports, start, TOKEN};
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
