mod common;

use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Arc;
use std::time::Duration;

use acnologia::{
    AuthType, CapabilityOptions, Client, Credential, Error, LeaseOptions, Outcome,
    ProxyResponseExt, Report, Tried, WithKeyOptions,
};
use common::{bodies, lease_error, reports, requests, start, CA_PEM, TOKEN};
use serde_json::json;
use wiremock::matchers::{method, path_regex};
use wiremock::{Mock, ResponseTemplate};

fn client(server: &wiremock::MockServer) -> Client {
    // Trailing slash on purpose: it is trimmed.
    Client::new(format!("{}/", server.uri()), TOKEN).unwrap()
}

#[tokio::test]
async fn services_and_headers() {
    let (server, _) = start(&[]).await;
    let svcs = client(&server).services().await.unwrap();
    assert_eq!(svcs.len(), 2);
    assert_eq!(svcs[0].slug, "serpapi");
    assert!(svcs[0].available);
    assert_eq!(svcs[0].auth.kind, AuthType::Query);
    assert_eq!(svcs[1].auth.kind, AuthType::Bearer);
    assert_eq!(svcs[1].auth.prefix, "token ");

    let req = &server.received_requests().await.unwrap()[0];
    let ua = req.headers.get("user-agent").unwrap().to_str().unwrap();
    assert_eq!(ua, format!("acnologia-rust/{}", acnologia::VERSION));
    assert_eq!(ua, acnologia::USER_AGENT);
}

#[tokio::test]
async fn bad_token_is_unauthenticated() {
    let (server, _) = start(&[]).await;
    let c = Client::new(server.uri(), "acn_wrong").unwrap();
    let err = c.services().await.unwrap_err();
    assert!(
        matches!(&err, Error::Unauthenticated { code, .. } if code == "unauthenticated"),
        "{err:?}"
    );
    assert_eq!(err.status(), Some(401));
}

#[tokio::test]
async fn lease_report_rotate_and_renew() {
    let (server, _) = start(&[1, 2]).await;
    let c = client(&server);
    let lease = c
        .lease(
            "serpapi",
            LeaseOptions::new()
                .ttl(Duration::from_secs(90))
                .wait(Duration::from_millis(9500))
                .exclude([9])
                .exclusive(true),
        )
        .await
        .unwrap();
    assert_eq!(lease.lease_id, "lse_1");
    assert_eq!(lease.key_id, 1);
    assert_eq!(lease.secret(), "sk1");
    assert_eq!(lease.auth.kind, AuthType::Query);
    assert_eq!(lease.expires_at, "2026-11-01T12:00:00Z");

    let sent = bodies(&server, "/api/v1/lease").await;
    assert_eq!(
        sent[0],
        json!({"service": "serpapi", "ttl_sec": 90, "wait_sec": 10, "exclude_key_ids": [9], "exclusive": true})
    );

    assert_eq!(
        lease.renew(Some(Duration::from_secs(600))).await.unwrap(),
        "2026-11-01T13:00:00Z"
    );

    let next = lease
        .report(
            Report::rate_limited(Some(Duration::from_secs(3600)))
                .status(429)
                .message("slow down")
                .rotate(),
        )
        .await
        .unwrap()
        .expect("next lease");
    assert_eq!(next.key_id, 2);
    assert_eq!(next.secret(), "sk2");
    let r = reports(&server).await;
    assert_eq!(r[0].0, "lse_1");
    assert_eq!(
        r[0].1,
        json!({"outcome": "rate_limited", "status_code": 429, "retry_after_sec": 3600, "message": "slow down", "rotate": true})
    );

    // No key left: rotating gives None.
    assert!(next
        .report(Report::quota_exhausted().rotate())
        .await
        .unwrap()
        .is_none());

    // Renewing a lease that is not open.
    let err = next.renew(None).await.unwrap_err();
    assert!(
        matches!(&err, Error::Api { status: 404, code, .. } if code == "lease_not_found"),
        "{err:?}"
    );
}

#[tokio::test]
async fn wait_over_max_is_rejected() {
    let (server, _) = start(&[1]).await;
    let err = client(&server)
        .lease("serpapi", LeaseOptions::new().wait(Duration::from_secs(61)))
        .await
        .unwrap_err();
    assert!(matches!(err, Error::InvalidInput(_)));
    assert!(server.received_requests().await.unwrap().is_empty());
}

#[tokio::test]
async fn every_error_code() {
    type Case = (u16, serde_json::Value, fn(&Error) -> bool, Option<u64>);
    let cases: Vec<Case> = vec![
        (
            503,
            json!({"error": {"code": "no_key_available", "message": "cooling"}, "retry_after_sec": 42}),
            |e| matches!(e, Error::NoKeyAvailable { .. }) && !e.is_busy(),
            Some(42),
        ),
        (
            503,
            json!({"error": {"code": "all_keys_busy", "message": "busy"}, "retry_after_sec": 1}),
            |e| matches!(e, Error::AllKeysBusy { .. }) && e.is_no_key_available(),
            Some(1),
        ),
        (
            503,
            json!({"error": {"code": "service_unavailable", "message": "serpapi keeps failing"}, "retry_after_sec": 30}),
            |e| matches!(e, Error::ServiceUnavailable { .. }),
            Some(30),
        ),
        (
            503,
            json!({"error": {"code": "empty_group", "message": "no services"}}),
            |e| matches!(e, Error::Api { status: 503, code, .. } if code == "empty_group"),
            None,
        ),
        (
            401,
            json!({"error": {"code": "unauthenticated", "message": "app token expired"}}),
            |e| matches!(e, Error::Unauthenticated { message, .. } if message == "app token expired"),
            None,
        ),
        (
            403,
            json!({"error": {"code": "unauthenticated", "message": "app is disabled"}}),
            |e| matches!(e, Error::Forbidden { code, .. } if code == "unauthenticated"),
            None,
        ),
        (
            403,
            json!({"error": {"code": "service_not_allowed", "message": "this app cannot use service serpapi"}}),
            |e| matches!(e, Error::Forbidden { code, .. } if code == "service_not_allowed"),
            None,
        ),
        (
            403,
            json!({"error": {"code": "lease_not_allowed", "message": "this app is proxy only"}}),
            |e| matches!(e, Error::Forbidden { code, .. } if code == "lease_not_allowed"),
            None,
        ),
        (
            404,
            json!({"error": {"code": "unknown_service", "message": "no service named x"}}),
            |e| matches!(e, Error::Api { status: 404, code, .. } if code == "unknown_service"),
            None,
        ),
        (
            429,
            json!({"error": {"code": "app_limit_reached", "message": "daily limit"}, "limit": "daily_requests"}),
            |e| matches!(e, Error::AppLimitReached { limit, .. } if limit == "daily_requests"),
            None,
        ),
        (
            429,
            json!({"error": {"code": "app_limit_reached", "message": "monthly budget"}, "limit": "monthly_budget",
                   "reset_at": "2026-12-01T00:00:00Z", "retry_after_sec": 3600}),
            |e| {
                matches!(e, Error::AppLimitReached { limit, reset_at, .. }
                if limit == "monthly_budget" && reset_at == "2026-12-01T00:00:00Z")
                    && e.code() == Some("app_limit_reached")
            },
            Some(3600),
        ),
        (
            403,
            json!({"error": {"code": "share_revoked", "message": "the owner revoked this share"}}),
            |e| matches!(e, Error::ShareUnavailable { status, code, .. } if status == "revoked" && code == "share_revoked"),
            None,
        ),
        (
            403,
            json!({"error": {"code": "share_expired", "message": "expired"}}),
            |e| matches!(e, Error::ShareUnavailable { status, .. } if status == "expired"),
            None,
        ),
        (
            400,
            json!({"error": {"code": "bad_params", "message": "q is required"}}),
            |e| matches!(e, Error::Api { status: 400, code, .. } if code == "bad_params"),
            None,
        ),
        (
            400,
            json!({"error": {"code": "bad_request", "message": "invalid JSON"}}),
            |e| matches!(e, Error::Api { status: 400, .. }),
            None,
        ),
    ];
    for (status, body, check, retry) in cases {
        let (server, _) = start(&[1]).await;
        lease_error(&server, status, body.clone()).await;
        let err = client(&server)
            .lease("serpapi", LeaseOptions::new())
            .await
            .unwrap_err();
        assert!(check(&err), "{status} {body}: got {err:?}");
        assert_eq!(err.retry_after(), retry.map(Duration::from_secs), "{err:?}");
        assert_eq!(err.status(), Some(status));
    }
}

#[tokio::test]
async fn report_errors() {
    let (server, _) = start(&[1]).await;
    let c = client(&server);
    let lease = c.lease("serpapi", LeaseOptions::new()).await.unwrap();
    Mock::given(method("POST"))
        .and(path_regex("/report$"))
        .respond_with(ResponseTemplate::new(409).set_body_json(
            json!({"error": {"code": "report_failed", "message": "lease already reported"}}),
        ))
        .with_priority(1)
        .mount(&server)
        .await;
    let err = lease.report(Report::ok()).await.unwrap_err();
    assert_eq!(err.code(), Some("report_failed"));
    assert_eq!(err.status(), Some(409));
}

#[tokio::test]
async fn with_key_success() {
    let (server, _) = start(&[1, 2]).await;
    let out = client(&server)
        .with_key("serpapi", |lease| async move { Ok(lease.key_id * 10) })
        .await
        .unwrap();
    assert_eq!(out, 10);
    let r = reports(&server).await;
    assert_eq!(r, vec![("lse_1".to_string(), json!({"outcome": "ok"}))]);
}

#[tokio::test]
async fn with_key_rotates_on_each_rotation_error() {
    let (server, _) = start(&[1, 2, 3, 4]).await;
    let calls = Arc::new(AtomicU32::new(0));
    let c2 = calls.clone();
    let out = client(&server)
        .with_key("serpapi", move |lease| {
            let n = c2.fetch_add(1, Ordering::SeqCst);
            async move {
                match n {
                    0 => Err(Error::RateLimited {
                        retry_after: Some(Duration::from_secs(60)),
                        status_code: Some(429),
                        message: Some(format!("GET /search?api_key={}", lease.secret())),
                    }),
                    1 => Err(Error::quota_exhausted()),
                    2 => Err(Error::for_status(401, None).unwrap()),
                    _ => Ok(lease.secret().to_string()),
                }
            }
        })
        .await
        .unwrap();
    assert_eq!(out, "sk4");
    assert_eq!(calls.load(Ordering::SeqCst), 4);
    let r = reports(&server).await;
    assert_eq!(r.len(), 4);
    assert_eq!(
        r[0].1,
        json!({"outcome": "rate_limited", "status_code": 429, "retry_after_sec": 60, "message": "GET /search?api_key=[key]", "rotate": true})
    );
    assert_eq!(
        r[1].1,
        json!({"outcome": "quota_exhausted", "rotate": true})
    );
    assert_eq!(
        r[2].1,
        json!({"outcome": "invalid", "status_code": 401, "rotate": true})
    );
    assert_eq!(r[3], ("lse_4".to_string(), json!({"outcome": "ok"})));
    // Every next key came back from the report, so only one lease call.
    assert_eq!(bodies(&server, "/api/v1/lease").await.len(), 1);
}

#[tokio::test]
async fn with_key_leases_again_when_report_has_no_next() {
    let (server, fake) = start(&[1, 2]).await;
    fake.state.lock().unwrap().no_next = true;
    let out = client(&server)
        .with_key_opts(
            "serpapi",
            WithKeyOptions::new().wait(Duration::from_secs(5)).lease(
                LeaseOptions::new()
                    .wait(Duration::from_secs(5))
                    .exclude([7]),
            ),
            |lease| async move {
                if lease.key_id == 1 {
                    Err(Error::rate_limited(None))
                } else {
                    Ok(lease.key_id)
                }
            },
        )
        .await
        .unwrap();
    assert_eq!(out, 2);
    let leases = bodies(&server, "/api/v1/lease").await;
    assert_eq!(leases.len(), 2);
    assert_eq!(
        leases[0],
        json!({"service": "serpapi", "exclude_key_ids": [7], "wait_sec": 5})
    );
    assert_eq!(
        leases[1],
        json!({"service": "serpapi", "exclude_key_ids": [7, 1], "wait_sec": 5})
    );
}

#[tokio::test]
async fn with_key_gives_up_after_max_attempts() {
    let (server, _) = start(&[1, 2, 3, 4, 5]).await;
    let err = client(&server)
        .with_key_opts(
            "serpapi",
            WithKeyOptions::new().max_attempts(3),
            |_lease| async move {
                Err::<(), _>(Error::RateLimited {
                    retry_after: Some(Duration::from_secs(5)),
                    status_code: Some(429),
                    message: None,
                })
            },
        )
        .await
        .unwrap_err();
    assert!(matches!(err, Error::RateLimited { .. }), "{err:?}");
    assert_eq!(err.retry_after(), Some(Duration::from_secs(5)));
    let r = reports(&server).await;
    assert_eq!(r.len(), 3);
    assert_eq!(r[1].1["rotate"], json!(true));
    // The last attempt is reported without asking for another key.
    assert_eq!(
        r[2],
        (
            "lse_3".to_string(),
            json!({"outcome": "rate_limited", "status_code": 429, "retry_after_sec": 5})
        )
    );
}

#[tokio::test]
async fn with_key_no_key_left() {
    let (server, _) = start(&[1]).await;
    let err = client(&server)
        .with_key("serpapi", |_lease| async move {
            Err::<(), _>(Error::invalid_key())
        })
        .await
        .unwrap_err();
    assert!(matches!(err, Error::NoKeyAvailable { .. }), "{err:?}");
    assert_eq!(err.retry_after(), Some(Duration::from_secs(42)));
}

#[tokio::test]
async fn with_key_busy_pool() {
    let (server, fake) = start(&[1]).await;
    fake.state.lock().unwrap().busy = true;
    let err = client(&server)
        .with_key("serpapi", |_lease| async move { Ok(()) })
        .await
        .unwrap_err();
    assert!(err.is_busy() && err.is_no_key_available(), "{err:?}");
    assert_eq!(err.retry_after(), Some(Duration::from_secs(1)));
}

#[tokio::test]
async fn with_key_other_error_is_reported_and_returned() {
    let (server, _) = start(&[1, 2]).await;
    let err = client(&server)
        .with_key("serpapi", |lease| async move {
            Err::<(), _>(Error::other(format!(
                "parse failed for https://serpapi.com/?api_key={}",
                lease.secret()
            )))
        })
        .await
        .unwrap_err();
    assert!(matches!(err, Error::Other(_)));
    let r = reports(&server).await;
    assert_eq!(
        r,
        vec![(
            "lse_1".to_string(),
            json!({"outcome": "error", "message": "parse failed for https://serpapi.com/?api_key=[key]"})
        )]
    );
}

#[tokio::test]
async fn with_key_reports_on_panic() {
    let (server, _) = start(&[1]).await;
    let c = client(&server);
    let res = tokio::spawn(async move {
        c.with_key("serpapi", |_lease| async move {
            if true {
                panic!("boom");
            }
            Ok(())
        })
        .await
    })
    .await;
    assert!(res.unwrap_err().is_panic());
    for _ in 0..50 {
        if !reports(&server).await.is_empty() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    let r = reports(&server).await;
    assert_eq!(r.len(), 1);
    assert_eq!(r[0].1["outcome"], json!("error"));
}

#[tokio::test]
async fn with_key_reports_on_cancel() {
    let (server, _) = start(&[1]).await;
    let c = client(&server);
    let res = tokio::time::timeout(
        Duration::from_millis(100),
        c.with_key("serpapi", |_lease| async move {
            tokio::time::sleep(Duration::from_secs(30)).await;
            Ok(())
        }),
    )
    .await;
    assert!(res.is_err());
    for _ in 0..50 {
        if !reports(&server).await.is_empty() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    let r = reports(&server).await;
    assert_eq!(r.len(), 1);
    assert_eq!(r[0].1["outcome"], json!("error"));
}

#[tokio::test]
async fn secret_is_redacted_in_debug_and_display() {
    let (server, _) = start(&[1]).await;
    let c = Client::new(server.uri(), "acn_supersecrettoken").unwrap();
    let dbg = format!("{c:?} {c:#?}");
    assert!(!dbg.contains("supersecrettoken"), "{dbg}");
    assert!(dbg.contains("[redacted]"));

    let c = client(&server);
    let lease = c.lease("serpapi", LeaseOptions::new()).await.unwrap();
    for s in [
        format!("{lease:?}"),
        format!("{lease:#?}"),
        format!("{lease}"),
        format!("{:?}", lease.info()),
        format!("{:?}", lease.credential()),
        format!("{:?}", c.proxy("serpapi")),
    ] {
        assert!(!s.contains("sk1"), "secret leaked: {s}");
        assert!(!s.contains(TOKEN), "token leaked: {s}");
    }
    assert!(format!("{lease:?}").contains("[redacted]"));
    assert!(format!("{lease:?}").contains("lse_1"));
    // Errors do not carry it either.
    let err = Error::RateLimited {
        retry_after: None,
        status_code: None,
        message: Some("rate limited".into()),
    };
    assert!(!format!("{err:?} {err}").contains("sk1"));
}

#[tokio::test]
async fn apply_puts_the_key_in_place() {
    let (server, _) = start(&[1]).await;
    let c = client(&server);
    let lease = c.lease("serpapi", LeaseOptions::new()).await.unwrap();
    let http = reqwest::Client::new();

    let req = lease
        .apply(http.get(lease.url("/search.json", &[("q", "coffee")])))
        .build()
        .unwrap();
    assert_eq!(
        req.url().as_str(),
        "https://serpapi.com/search.json?q=coffee&api_key=sk1"
    );

    let mut req = http
        .get("https://serpapi.com/search.json?api_key=old&q=x")
        .build()
        .unwrap();
    lease.apply_to(&mut req).unwrap();
    assert_eq!(
        req.url().as_str(),
        "https://serpapi.com/search.json?q=x&api_key=sk1"
    );
    assert_eq!(
        lease.credential(),
        Credential::Query {
            name: "api_key".into(),
            value: "sk1".into()
        }
    );
}

#[tokio::test]
async fn apply_header_and_bearer() {
    let (server, _) = start(&[]).await;
    let base = json!({"lease_id": "lse_h", "key_id": 5, "service": "x", "secret": "s3cr3t",
        "base_url": "https://api.example.com", "expires_at": "2026-11-01T12:00:00Z"});
    for (auth, name, value) in [
        (
            json!({"type": "header", "name": "xi-api-key"}),
            "xi-api-key",
            "s3cr3t",
        ),
        (
            json!({"type": "bearer", "name": ""}),
            "authorization",
            "Bearer s3cr3t",
        ),
        (
            json!({"type": "bearer", "name": "Authorization", "prefix": "Token "}),
            "authorization",
            "Token s3cr3t",
        ),
    ] {
        let mut l = base.clone();
        l["auth"] = auth;
        server.reset().await;
        lease_error(&server, 200, l).await;
        let c = client(&server);
        let lease = c.lease("x", LeaseOptions::new()).await.unwrap();
        let req = lease
            .apply(reqwest::Client::new().get(lease.url("v1/me", &[])))
            .build()
            .unwrap();
        assert_eq!(req.url().as_str(), "https://api.example.com/v1/me");
        assert_eq!(req.headers().get(name).unwrap(), value);
        assert!(!format!("{:?}", req.headers()).contains("s3cr3t"));
    }
}

#[tokio::test]
async fn proxy_requests() {
    let (server, _) = start(&[]).await;
    Mock::given(path_regex("^/proxy/serpapi/"))
        .and(wiremock::matchers::header(
            "authorization",
            format!("Bearer {TOKEN}").as_str(),
        ))
        .respond_with(
            ResponseTemplate::new(200)
                .insert_header("X-Acnologia-Key-Id", "7")
                .insert_header("X-Acnologia-Attempts", "1")
                .set_body_string("{}"),
        )
        .with_priority(1)
        .mount(&server)
        .await;
    let c = client(&server);
    let p = c.proxy("serpapi");
    assert_eq!(
        p.url("/search.json", &[("q", "a b")]),
        format!("{}/proxy/serpapi/search.json?q=a+b", server.uri())
    );

    let resp = p
        .get("/search.json", &[("engine", "google"), ("q", "coffee")])
        .await
        .unwrap();
    assert_eq!(resp.status(), 200);
    assert_eq!(resp.headers()["x-acnologia-key-id"], "7");

    let resp = p
        .post_json("v1/text-to-speech/abc", &json!({"text": "hi"}))
        .await
        .unwrap();
    assert_eq!(resp.status(), 200);

    let resp = p
        .request(reqwest::Method::PUT, "/things/1")
        .header("X-Custom", "yes")
        .body("raw")
        .send()
        .await
        .unwrap();
    assert_eq!(resp.status(), 200);

    let reqs = server.received_requests().await.unwrap();
    assert_eq!(reqs[0].url.path(), "/proxy/serpapi/search.json");
    assert_eq!(reqs[0].url.query(), Some("engine=google&q=coffee"));
    assert_eq!(reqs[0].headers["user-agent"], acnologia::USER_AGENT);
    assert_eq!(reqs[1].method.as_str(), "POST");
    assert_eq!(reqs[1].headers["content-type"], "application/json");
    assert_eq!(reqs[1].body, br#"{"text":"hi"}"#);
    assert_eq!(reqs[2].method.as_str(), "PUT");
    assert_eq!(reqs[2].headers["x-custom"], "yes");
}

#[tokio::test]
async fn proxy_returns_upstream_errors_and_does_not_follow_redirects() {
    let (server, _) = start(&[]).await;
    Mock::given(path_regex("^/proxy/serpapi/missing"))
        .respond_with(ResponseTemplate::new(404).set_body_string("nope"))
        .with_priority(1)
        .mount(&server)
        .await;
    Mock::given(path_regex("^/proxy/serpapi/moved"))
        .respond_with(
            ResponseTemplate::new(302).insert_header("Location", "https://elsewhere.example/"),
        )
        .with_priority(1)
        .mount(&server)
        .await;
    let p = client(&server).proxy("serpapi");
    assert_eq!(p.get("/missing", &[]).await.unwrap().status(), 404);
    assert_eq!(p.get("/moved", &[]).await.unwrap().status(), 302);
}

#[tokio::test]
async fn check_status_maps_provider_statuses() {
    let (server, _) = start(&[]).await;
    for (status, retry) in [
        (429u16, Some("12")),
        (402, None),
        (401, None),
        (403, None),
        (500, None),
        (200, None),
    ] {
        server.reset().await;
        let mut t = ResponseTemplate::new(status);
        if let Some(r) = retry {
            t = t.insert_header("Retry-After", r);
        }
        Mock::given(method("GET"))
            .respond_with(t)
            .mount(&server)
            .await;
        let resp = reqwest::get(server.uri()).await.unwrap();
        let out = acnologia::check_status(resp);
        match status {
            429 => {
                let e = out.unwrap_err();
                assert!(matches!(
                    e,
                    Error::RateLimited {
                        status_code: Some(429),
                        ..
                    }
                ));
                assert_eq!(e.retry_after(), Some(Duration::from_secs(12)));
            }
            402 => assert!(matches!(out.unwrap_err(), Error::QuotaExhausted { .. })),
            401 | 403 => assert!(matches!(out.unwrap_err(), Error::InvalidKey { .. })),
            _ => assert_eq!(out.unwrap().status(), status),
        }
    }
}

#[test]
fn from_env_and_validation() {
    std::env::set_var(acnologia::ENV_URL, "http://localhost:9999/");
    std::env::set_var(acnologia::ENV_TOKEN, "acn_fromenv");
    let c = Client::from_env().unwrap();
    assert_eq!(c.base_url(), "http://localhost:9999");
    std::env::remove_var(acnologia::ENV_TOKEN);
    assert!(matches!(Client::from_env(), Err(Error::InvalidInput(_))));
    std::env::remove_var(acnologia::ENV_URL);
    assert!(matches!(
        Client::new("", "acn_x"),
        Err(Error::InvalidInput(_))
    ));
    assert!(matches!(
        Client::new("http://localhost:8080", " "),
        Err(Error::InvalidInput(_))
    ));
    // Old cog_ tokens are passed through unchanged.
    assert!(Client::new("http://localhost:8080", "cog_legacy").is_ok());
}

#[test]
fn outcome_wire_values() {
    for (o, s) in [
        (Outcome::Ok, "ok"),
        (Outcome::RateLimited, "rate_limited"),
        (Outcome::QuotaExhausted, "quota_exhausted"),
        (Outcome::Invalid, "invalid"),
        (Outcome::Error, "error"),
        (Outcome::Busy, "busy"),
        (Outcome::Banned, "banned"),
    ] {
        assert_eq!(o.as_str(), s);
        assert_eq!(serde_json::to_value(o).unwrap(), json!(s));
    }
}

#[tokio::test]
async fn me_reports_limits_usage_and_services() {
    let (server, _) = start(&[]).await;
    let me = client(&server).me().await.unwrap();
    assert_eq!(me.app.id, "app_1");
    assert_eq!(me.app.name, "crawler");
    assert_eq!(me.limits.daily_requests, Some(1000));
    assert_eq!(me.limits.monthly_requests, None);
    assert_eq!(me.limits.daily_budget, Some(2.5));
    assert_eq!(me.usage.month_requests, 340);
    assert_eq!(
        me.usage.last_used_at.as_deref(),
        Some("2026-11-10T09:00:00Z")
    );
    assert!(me.limit_hit.is_none());
    assert_eq!(me.services[0].slug, "serpapi");
    assert_eq!(me.services[0].available, 1);
    assert!(me.share.is_none());
    let r = &requests(&server, "/api/v1/me").await[0];
    assert_eq!(r.method.as_str(), "GET");
    assert_eq!(
        r.headers["authorization"],
        format!("Bearer {TOKEN}").as_str()
    );
}

#[tokio::test]
async fn services_carry_guide_url() {
    let (server, _) = start(&[]).await;
    Mock::given(method("GET"))
        .and(wiremock::matchers::path("/api/v1/services"))
        .respond_with(
            ResponseTemplate::new(200).set_body_json(json!({"services": [{
                "slug": "serpapi", "available": true, "auth": {"type": "query", "name": "api_key"},
                "guide_url": "http://acn.example/api/v1/services/serpapi/guide"
            }]})),
        )
        .with_priority(1)
        .mount(&server)
        .await;
    let s = client(&server).services().await.unwrap();
    assert_eq!(
        s[0].guide_url,
        "http://acn.example/api/v1/services/serpapi/guide"
    );
}

#[tokio::test]
async fn guide_as_json_and_markdown() {
    let (server, _) = start(&[]).await;
    let c = client(&server);
    let g = c.guide("serpapi").await.unwrap();
    assert!(g.generated);
    assert!(g.markdown.starts_with("# SerpAPI"));
    assert_eq!(g.guide["endpoints"][0]["path"], "/search.json");
    let md = c.guide_markdown("serpapi").await.unwrap();
    assert_eq!(md, "# SerpAPI\n\nSearch Google.\n");
    let reqs = requests(&server, "/api/v1/services/serpapi/guide").await;
    assert_eq!(reqs[0].headers["accept"], "application/json");
    assert_eq!(reqs[1].headers["accept"], "text/markdown");
    let err = c.guide("nope").await.unwrap_err();
    assert_eq!(err.code(), Some("service_not_allowed"));
}

#[tokio::test]
async fn capabilities_and_capability_call() {
    let (server, _) = start(&[]).await;
    let c = client(&server);
    let caps = c.capabilities().await.unwrap();
    assert_eq!(caps.len(), 1);
    assert_eq!(caps[0].slug, "web-search");
    assert_eq!(caps[0].strategy.as_deref(), Some("priority"));
    assert_eq!(caps[0].params[0]["name"], "q");
    assert_eq!(caps[0].routes[1].service, "tavily");
    assert!(caps[0].routes[0].mapped);
    assert_eq!(caps[0].url, "http://acn.example/proxy/web-search");

    let resp = c
        .capability(
            "web-search",
            &json!({"q": "coffee"}),
            CapabilityOptions::new()
                .service("tavily")
                .raw()
                .idempotency_key("k1")
                .no_cache(),
        )
        .await
        .unwrap();
    assert_eq!(resp.status(), 200);
    assert_eq!(resp.capability(), Some("web-search"));
    assert_eq!(ProxyResponseExt::service(&resp), Some("tavily"));
    assert_eq!(resp.key_id(), Some(4));
    assert_eq!(resp.attempts(), Some(2));
    assert_eq!(
        resp.tried(),
        vec![
            Tried {
                service: "serpapi".into(),
                status: 503
            },
            Tried {
                service: "tavily".into(),
                status: 200
            },
        ]
    );
    let r = &requests(&server, "/proxy/web-search").await[0];
    assert_eq!(r.method.as_str(), "POST");
    assert_eq!(r.url.query(), Some("_service=tavily&_raw=1"));
    assert_eq!(r.body, br#"{"q":"coffee"}"#);
    assert_eq!(r.headers["idempotency-key"], "k1");
    assert_eq!(r.headers["cache-control"], "no-cache");
    assert_eq!(
        r.headers["authorization"],
        format!("Bearer {TOKEN}").as_str()
    );
}

#[tokio::test]
async fn proxy_options_and_response_accessors() {
    let (server, _) = start(&[]).await;
    Mock::given(path_regex("^/proxy/serpapi/"))
        .respond_with(
            ResponseTemplate::new(200)
                .insert_header("X-Acnologia-Key-Id", "7")
                .insert_header("X-Acnologia-Attempts", "1")
                .insert_header("X-Acnologia-Service", "serpapi")
                .insert_header("X-Acnologia-Cache", "hit")
                .insert_header("Idempotent-Replayed", "true"),
        )
        .with_priority(1)
        .mount(&server)
        .await;
    let c = client(&server);
    let p = c.proxy("serpapi").idempotency_key("order-42").no_cache();
    let resp = p.get("/search.json", &[("q", "x")]).await.unwrap();
    assert_eq!(resp.key_id(), Some(7));
    assert_eq!(resp.attempts(), Some(1));
    assert_eq!(ProxyResponseExt::service(&resp), Some("serpapi"));
    assert_eq!(resp.cache(), Some("hit"));
    assert!(resp.replayed());
    assert_eq!(resp.capability(), None);
    assert!(resp.tried().is_empty());
    p.post_json("/x", &json!({})).await.unwrap();
    // Without options nothing extra is sent.
    c.proxy("serpapi").get("/plain", &[]).await.unwrap();

    let reqs = server.received_requests().await.unwrap();
    for r in &reqs[..2] {
        assert_eq!(r.headers["idempotency-key"], "order-42");
        assert_eq!(r.headers["cache-control"], "no-cache");
    }
    assert!(reqs[2].headers.get("idempotency-key").is_none());
    assert!(reqs[2].headers.get("cache-control").is_none());

    // A share's token gets no key id.
    let h = reqwest::header::HeaderMap::new();
    assert_eq!(h.key_id(), None);
    assert!(!h.replayed());
}

#[tokio::test]
async fn forward_proxy_calls() {
    let (server, _) = start(&[]).await;
    let c = client(&server);
    let fp = c.forward_proxy().await.unwrap();
    assert_eq!(fp.proxy_url, "http://acn.example:8888");
    assert!(fp.passthrough);
    assert_eq!(fp.hosts.len(), 2);
    assert_eq!(fp.hosts[1].path, "/~tiktok");
    assert_eq!(fp.hosts[0].path, "");
    assert_eq!(fp.env["NODE_USE_ENV_PROXY"], "1");
    assert_eq!(fp.service_header, "X-Acnologia-Service");
    assert!(!format!("{fp:?}").contains(TOKEN));

    assert_eq!(c.forward_proxy_ca().await.unwrap(), CA_PEM);
    let ca = &requests(&server, "/api/v1/proxy/ca.pem").await[0];
    assert!(
        ca.headers.get("authorization").is_none(),
        "the CA is public"
    );

    let dir = std::env::temp_dir().join(format!("acn-ca-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let path = dir.join("ca.pem");
    let saved = c.save_forward_proxy_ca(Some(&path)).await.unwrap();
    assert_eq!(std::fs::read_to_string(&saved).unwrap(), CA_PEM);
    std::fs::remove_dir_all(&dir).unwrap();

    let url = c.forward_proxy_url().await.unwrap();
    assert_eq!(url, format!("http://app:{TOKEN}@acn.example:8888"));

    let env = c.forward_proxy_env(None).await.unwrap();
    assert_eq!(env["HTTPS_PROXY"], url);
    assert_eq!(env["HTTP_PROXY"], url);
    assert_eq!(env["NODE_USE_ENV_PROXY"], "1");
    for k in ["NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "SSL_CERT_FILE"] {
        assert_eq!(env[k], acnologia::DEFAULT_CA_PATH);
    }
    assert_eq!(env.len(), 6);
    let env = c.forward_proxy_env(Some("/etc/acn.pem")).await.unwrap();
    assert_eq!(env["SSL_CERT_FILE"], "/etc/acn.pem");

    let r = &requests(&server, "/api/v1/proxy").await[0];
    assert_eq!(r.method.as_str(), "GET");
    assert_eq!(
        r.headers["authorization"],
        format!("Bearer {TOKEN}").as_str()
    );
}

#[tokio::test]
async fn access_password_is_sent_everywhere() {
    let (server, _) = start(&[]).await;
    let c = Client::builder(server.uri(), TOKEN)
        .access_password("hunter2")
        .build()
        .unwrap();
    assert!(!format!("{c:?}").contains("hunter2"));
    c.services().await.unwrap();
    c.forward_proxy_ca().await.unwrap();
    c.proxy("serpapi").get("/x", &[]).await.unwrap();
    for r in server.received_requests().await.unwrap() {
        assert_eq!(r.headers["x-acnologia-access"], "hunter2", "{}", r.url);
    }
}

#[tokio::test]
async fn app_limit_and_share_errors_from_api() {
    let (server, _) = start(&[]).await;
    Mock::given(method("GET"))
        .and(wiremock::matchers::path("/api/v1/me"))
        .respond_with(
            ResponseTemplate::new(403)
                .insert_header("X-Acnologia-Share", "paused")
                .set_body_json(json!({"error": {"code": "share_paused", "message": "paused"}})),
        )
        .with_priority(1)
        .mount(&server)
        .await;
    Mock::given(path_regex("^/proxy/serpapi/"))
        .respond_with(
            ResponseTemplate::new(429)
                .insert_header("Retry-After", "120")
                .set_body_json(json!({
                    "error": {"code": "app_limit_reached", "message": "this app reached its daily requests limit"},
                    "limit": "daily_requests", "reset_at": "2026-11-11T00:00:00Z"
                })),
        )
        .with_priority(1)
        .mount(&server)
        .await;
    let c = client(&server);
    let err = c.me().await.unwrap_err();
    assert!(
        matches!(&err, Error::ShareUnavailable { status, .. } if status == "paused"),
        "{err:?}"
    );
    assert_eq!(err.status(), Some(403));

    let resp = c.proxy("serpapi").get("/x", &[]).await.unwrap();
    let (status, headers) = (resp.status().as_u16(), resp.headers().clone());
    let err = Error::from_api(status, &headers, &resp.bytes().await.unwrap());
    assert!(
        matches!(&err, Error::AppLimitReached { limit, reset_at, .. }
            if limit == "daily_requests" && reset_at == "2026-11-11T00:00:00Z"),
        "{err:?}"
    );
    assert_eq!(err.retry_after(), Some(Duration::from_secs(120)));
}
