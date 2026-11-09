//! The error type returned by every call.

use std::time::Duration;

use serde::Deserialize;

/// A boxed error from your own code, carried by [`Error::Other`].
pub type BoxError = Box<dyn std::error::Error + Send + Sync + 'static>;

/// Everything that can go wrong talking to Acnologia, plus the three errors a
/// `with_key` callback returns to rotate to the next key.
///
/// The rotation errors ([`Error::RateLimited`], [`Error::QuotaExhausted`],
/// [`Error::InvalidKey`]) are never produced by the server; you return them
/// from a `with_key` callback, or [`Error::for_status`] builds them from a
/// provider status code.
#[derive(Debug, thiserror::Error)]
#[non_exhaustive]
pub enum Error {
    /// The provider rate limited the key. Rotates in `with_key`.
    #[error("acnologia: rate limited{}{}", fmt_retry(*retry_after), fmt_msg(message))]
    RateLimited {
        /// How long the key should cool down, if known.
        retry_after: Option<Duration>,
        /// The provider's HTTP status, if any.
        status_code: Option<u16>,
        /// A short note for the report.
        message: Option<String>,
    },

    /// The key used up its quota. Rotates in `with_key`.
    #[error("acnologia: quota exhausted{}", fmt_msg(message))]
    QuotaExhausted {
        /// The provider's HTTP status, if any.
        status_code: Option<u16>,
        /// A short note for the report.
        message: Option<String>,
    },

    /// The provider rejected the key. Rotates in `with_key`.
    #[error("acnologia: invalid key{}", fmt_msg(message))]
    InvalidKey {
        /// The provider's HTTP status, if any.
        status_code: Option<u16>,
        /// A short note for the report.
        message: Option<String>,
    },

    /// HTTP 503 `no_key_available`: every key is cooling down, exhausted,
    /// disabled or excluded.
    #[error("acnologia: no key available{}{}", fmt_retry(*retry_after), fmt_str(message))]
    NoKeyAvailable {
        /// How long to wait before retrying, if the server said.
        retry_after: Option<Duration>,
        /// The server's message.
        message: String,
    },

    /// HTTP 503 `all_keys_busy`: usable keys exist but every one is at its
    /// concurrency limit. It clears as soon as another caller reports, so retry
    /// after `retry_after` (usually 1s) or lease with a wait.
    #[error("acnologia: all keys busy{}{}", fmt_retry(*retry_after), fmt_str(message))]
    AllKeysBusy {
        /// How long to wait before retrying, if the server said.
        retry_after: Option<Duration>,
        /// The server's message.
        message: String,
    },

    /// HTTP 503 `service_unavailable`: the provider keeps failing and its
    /// circuit breaker is open.
    #[error("acnologia: service unavailable{}{}", fmt_retry(*retry_after), fmt_str(message))]
    ServiceUnavailable {
        /// How long to wait before retrying, if the server said.
        retry_after: Option<Duration>,
        /// The server's message.
        message: String,
    },

    /// HTTP 429 `app_limit_reached`: the app used up a daily or monthly
    /// request or budget limit.
    #[error("acnologia: app limit reached: {limit}{}{}", fmt_retry(*retry_after), fmt_str(message))]
    AppLimitReached {
        /// `daily_requests`, `monthly_requests`, `daily_budget` or
        /// `monthly_budget`.
        limit: String,
        /// When the limit resets, as RFC 3339 text.
        reset_at: String,
        /// How long until it resets, if the server said.
        retry_after: Option<Duration>,
        /// The server's message.
        message: String,
    },

    /// HTTP 403 `share_revoked`, `share_paused` or `share_expired`: the owner
    /// of the share behind this token revoked, paused or let it expire.
    #[error("acnologia: share {status}{}", fmt_str(message))]
    ShareUnavailable {
        /// `revoked`, `paused` or `expired` (also sent as
        /// `X-Acnologia-Share`).
        status: String,
        /// The error code, such as `share_revoked`.
        code: String,
        /// The server's message.
        message: String,
    },

    /// HTTP 401: the app token is missing, wrong, revoked or expired.
    #[error("acnologia: unauthenticated{}", fmt_str(message))]
    Unauthenticated {
        /// The error code, usually `unauthenticated`.
        code: String,
        /// The server's message.
        message: String,
    },

    /// HTTP 403: the app is disabled, or not allowed this service or mode
    /// (`service_not_allowed`, `lease_not_allowed`, `proxy_not_allowed`).
    #[error("acnologia: forbidden: {code}{}", fmt_str(message))]
    Forbidden {
        /// The error code.
        code: String,
        /// The server's message.
        message: String,
    },

    /// Any other non-2xx answer from the consumer API.
    #[error("acnologia: http {status}{}{}", fmt_code(code), fmt_str(message))]
    Api {
        /// HTTP status.
        status: u16,
        /// The error code from the body, empty if there was none.
        code: String,
        /// The server's message, or the start of the body.
        message: String,
    },

    /// The request could not be sent or the response not read.
    #[error("acnologia: request failed: {0}")]
    Http(#[from] reqwest::Error),

    /// The server answered 2xx with a body this SDK could not decode.
    #[error("acnologia: decode response: {0}")]
    Decode(#[from] serde_json::Error),

    /// Reading or writing a local file failed.
    #[error("acnologia: {0}")]
    Io(#[from] std::io::Error),

    /// An argument was out of range, or configuration is missing.
    #[error("acnologia: {0}")]
    InvalidInput(String),

    /// An error from your own code inside a `with_key` callback. It is reported
    /// as `error` and returned unchanged; it does not rotate.
    #[error("{0}")]
    Other(BoxError),
}

fn fmt_retry(d: Option<Duration>) -> String {
    match d {
        Some(d) if !d.is_zero() => format!(" (retry after {}s)", d.as_secs_f64()),
        _ => String::new(),
    }
}

fn fmt_msg(m: &Option<String>) -> String {
    match m {
        Some(m) if !m.is_empty() => format!(": {m}"),
        _ => String::new(),
    }
}

fn fmt_str(m: &str) -> String {
    if m.is_empty() {
        String::new()
    } else {
        format!(": {m}")
    }
}

fn fmt_code(c: &str) -> String {
    if c.is_empty() {
        String::new()
    } else {
        format!(": {c}")
    }
}

impl Error {
    /// A rate limited error with an optional cooldown, to return from a
    /// `with_key` callback.
    pub fn rate_limited(retry_after: Option<Duration>) -> Self {
        Error::RateLimited {
            retry_after,
            status_code: None,
            message: None,
        }
    }

    /// A quota exhausted error, to return from a `with_key` callback.
    pub fn quota_exhausted() -> Self {
        Error::QuotaExhausted {
            status_code: None,
            message: None,
        }
    }

    /// An invalid key error, to return from a `with_key` callback.
    pub fn invalid_key() -> Self {
        Error::InvalidKey {
            status_code: None,
            message: None,
        }
    }

    /// Wrap any error from your own code. It does not rotate.
    pub fn other(err: impl Into<BoxError>) -> Self {
        Error::Other(err.into())
    }

    /// Map a common provider status to a rotation error: 429 is rate limited,
    /// 402 is quota exhausted, 401 and 403 are an invalid key. Anything else
    /// returns `None`. Providers differ (some send 429 for an empty quota), so
    /// check the body yourself when it matters.
    pub fn for_status(status: u16, retry_after: Option<Duration>) -> Option<Self> {
        let status_code = Some(status);
        match status {
            429 => Some(Error::RateLimited {
                retry_after,
                status_code,
                message: None,
            }),
            402 => Some(Error::QuotaExhausted {
                status_code,
                message: None,
            }),
            401 | 403 => Some(Error::InvalidKey {
                status_code,
                message: None,
            }),
            _ => None,
        }
    }

    /// True for [`Error::NoKeyAvailable`] and [`Error::AllKeysBusy`]: no key
    /// could be leased right now.
    pub fn is_no_key_available(&self) -> bool {
        matches!(
            self,
            Error::NoKeyAvailable { .. } | Error::AllKeysBusy { .. }
        )
    }

    /// True for [`Error::AllKeysBusy`].
    pub fn is_busy(&self) -> bool {
        matches!(self, Error::AllKeysBusy { .. })
    }

    /// True for the three errors that make `with_key` rotate.
    pub fn is_rotation(&self) -> bool {
        matches!(
            self,
            Error::RateLimited { .. } | Error::QuotaExhausted { .. } | Error::InvalidKey { .. }
        )
    }

    /// The wait the server or provider asked for, if any.
    pub fn retry_after(&self) -> Option<Duration> {
        match self {
            Error::RateLimited { retry_after, .. }
            | Error::NoKeyAvailable { retry_after, .. }
            | Error::AllKeysBusy { retry_after, .. }
            | Error::ServiceUnavailable { retry_after, .. }
            | Error::AppLimitReached { retry_after, .. } => *retry_after,
            _ => None,
        }
    }

    /// The HTTP status of an API error, or the provider status of a rotation
    /// error.
    pub fn status(&self) -> Option<u16> {
        match self {
            Error::NoKeyAvailable { .. }
            | Error::AllKeysBusy { .. }
            | Error::ServiceUnavailable { .. } => Some(503),
            Error::Unauthenticated { .. } => Some(401),
            Error::Forbidden { .. } | Error::ShareUnavailable { .. } => Some(403),
            Error::AppLimitReached { .. } => Some(429),
            Error::Api { status, .. } => Some(*status),
            Error::RateLimited { status_code, .. }
            | Error::QuotaExhausted { status_code, .. }
            | Error::InvalidKey { status_code, .. } => *status_code,
            Error::Http(e) => e.status().map(|s| s.as_u16()),
            _ => None,
        }
    }

    /// The server's error code, such as `no_key_available`, for API errors.
    pub fn code(&self) -> Option<&str> {
        match self {
            Error::NoKeyAvailable { .. } => Some("no_key_available"),
            Error::AllKeysBusy { .. } => Some("all_keys_busy"),
            Error::ServiceUnavailable { .. } => Some("service_unavailable"),
            Error::AppLimitReached { .. } => Some("app_limit_reached"),
            Error::Unauthenticated { code, .. }
            | Error::ShareUnavailable { code, .. }
            | Error::Forbidden { code, .. }
            | Error::Api { code, .. } => Some(code.as_str()),
            _ => None,
        }
    }

    /// Build the typed error for a non-2xx answer from Acnologia, such as a
    /// proxy or capability response you hold. Reads `Retry-After` and
    /// `X-Acnologia-Share` from `headers`.
    ///
    /// ```no_run
    /// # async fn run(acn: acnologia::Client) -> Result<(), acnologia::Error> {
    /// let resp = acn.proxy("serpapi").get("/search.json", &[("q", "x")]).await?;
    /// if resp.status() == 429 {
    ///     let (status, headers) = (resp.status().as_u16(), resp.headers().clone());
    ///     let err = acnologia::Error::from_api(status, &headers, &resp.bytes().await?);
    ///     if let acnologia::Error::AppLimitReached { limit, .. } = &err {
    ///         println!("out of {limit}");
    ///     }
    /// }
    /// # Ok(()) }
    /// ```
    pub fn from_api(status: u16, headers: &reqwest::header::HeaderMap, body: &[u8]) -> Self {
        let h = |name: &str| headers.get(name).and_then(|v| v.to_str().ok());
        parse_error_full(status, h("retry-after"), h("x-acnologia-share"), body)
    }
}

#[derive(Deserialize, Default)]
struct ErrorBody {
    #[serde(default)]
    error: Option<ErrorDetail>,
    #[serde(default)]
    retry_after_sec: Option<f64>,
    #[serde(default)]
    limit: Option<String>,
    #[serde(default)]
    reset_at: Option<String>,
}

#[derive(Deserialize, Default)]
struct ErrorDetail {
    #[serde(default)]
    code: String,
    #[serde(default)]
    message: String,
}

/// Turn a non-2xx consumer API answer into a typed error. `retry_header` is
/// the `Retry-After` header, used when the body has no `retry_after_sec`.
#[cfg(test)]
pub(crate) fn parse_error(status: u16, retry_header: Option<&str>, body: &[u8]) -> Error {
    parse_error_full(status, retry_header, None, body)
}

/// [`parse_error`] with the `X-Acnologia-Share` header.
pub(crate) fn parse_error_full(
    status: u16,
    retry_header: Option<&str>,
    share_header: Option<&str>,
    body: &[u8],
) -> Error {
    let parsed: ErrorBody = serde_json::from_slice(body).unwrap_or_default();
    let detail = parsed.error.unwrap_or_default();
    let code = detail.code;
    let mut message = detail.message;
    let retry_secs = parsed
        .retry_after_sec
        .or_else(|| retry_header.and_then(|h| h.trim().parse::<f64>().ok()));
    let retry_after = retry_secs
        .filter(|s| s.is_finite() && *s > 0.0)
        .map(Duration::from_secs_f64);

    if message.is_empty() && code.is_empty() {
        let text = String::from_utf8_lossy(body);
        let text = text.trim();
        message = crate::core::truncate(text, 200).to_string();
        if message.is_empty() {
            message = reqwest::StatusCode::from_u16(status)
                .ok()
                .and_then(|s| s.canonical_reason())
                .unwrap_or("")
                .to_string();
        }
    }

    match code.as_str() {
        "all_keys_busy" => Error::AllKeysBusy {
            retry_after,
            message,
        },
        "no_key_available" => Error::NoKeyAvailable {
            retry_after,
            message,
        },
        "" if status == 503 => Error::NoKeyAvailable {
            retry_after,
            message,
        },
        "service_unavailable" => Error::ServiceUnavailable {
            retry_after,
            message,
        },
        "app_limit_reached" => Error::AppLimitReached {
            limit: parsed.limit.unwrap_or_default(),
            reset_at: parsed.reset_at.unwrap_or_default(),
            retry_after,
            message,
        },
        "share_revoked" | "share_paused" | "share_expired" => Error::ShareUnavailable {
            status: code.trim_start_matches("share_").to_string(),
            code,
            message,
        },
        "" if status == 403 && share_header.is_some() => {
            let st = share_header.unwrap_or_default().trim().to_string();
            Error::ShareUnavailable {
                code: format!("share_{st}"),
                status: st,
                message,
            }
        }
        _ if status == 401 => Error::Unauthenticated {
            code: if code.is_empty() {
                "unauthenticated".into()
            } else {
                code
            },
            message,
        },
        _ if status == 403 => Error::Forbidden { code, message },
        _ => Error::Api {
            status,
            code,
            message,
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_codes() {
        let e = parse_error(
            503,
            None,
            br#"{"error":{"code":"no_key_available","message":"cooling"},"retry_after_sec":42}"#,
        );
        assert!(matches!(e, Error::NoKeyAvailable { .. }));
        assert_eq!(e.retry_after(), Some(Duration::from_secs(42)));
        assert!(e.is_no_key_available() && !e.is_busy());

        let e = parse_error(
            503,
            None,
            br#"{"error":{"code":"all_keys_busy","message":"busy"},"retry_after_sec":1}"#,
        );
        assert!(e.is_busy() && e.is_no_key_available());
        assert_eq!(e.code(), Some("all_keys_busy"));

        let e = parse_error(
            503,
            None,
            br#"{"error":{"code":"service_unavailable","message":"x"},"retry_after_sec":30}"#,
        );
        assert!(matches!(e, Error::ServiceUnavailable { .. }));
        assert_eq!(e.retry_after(), Some(Duration::from_secs(30)));

        let e = parse_error(503, Some("7"), b"");
        assert!(matches!(e, Error::NoKeyAvailable { .. }));
        assert_eq!(e.retry_after(), Some(Duration::from_secs(7)));

        let e = parse_error(
            401,
            None,
            br#"{"error":{"code":"unauthenticated","message":"invalid app token"}}"#,
        );
        assert!(matches!(e, Error::Unauthenticated { .. }));

        let e = parse_error(
            403,
            None,
            br#"{"error":{"code":"service_not_allowed","message":"no"}}"#,
        );
        assert!(matches!(&e, Error::Forbidden { code, .. } if code == "service_not_allowed"));

        let e = parse_error(
            409,
            None,
            br#"{"error":{"code":"report_failed","message":"lease already reported"}}"#,
        );
        assert!(
            matches!(&e, Error::Api { status: 409, code, message } if code == "report_failed" && message == "lease already reported")
        );

        let e = parse_error(
            429,
            Some("60"),
            br#"{"error":{"code":"app_limit_reached","message":"this app reached its daily requests limit"},"limit":"daily_requests","reset_at":"2026-11-11T00:00:00Z"}"#,
        );
        assert!(
            matches!(&e, Error::AppLimitReached { limit, reset_at, .. } if limit == "daily_requests" && reset_at == "2026-11-11T00:00:00Z")
        );
        assert_eq!(e.retry_after(), Some(Duration::from_secs(60)));
        assert_eq!(e.code(), Some("app_limit_reached"));
        assert_eq!(e.status(), Some(429));

        let e = parse_error(
            403,
            None,
            br#"{"error":{"code":"share_paused","message":"the owner paused this share"}}"#,
        );
        assert!(matches!(&e, Error::ShareUnavailable { status, .. } if status == "paused"));
        assert_eq!(e.code(), Some("share_paused"));
        let e = parse_error_full(403, None, Some("expired"), b"");
        assert!(matches!(&e, Error::ShareUnavailable { status, .. } if status == "expired"));

        let e = parse_error(500, None, b"  boom  ");
        assert!(matches!(&e, Error::Api { status: 500, message, .. } if message == "boom"));
        let e = parse_error(502, None, b"");
        assert!(matches!(&e, Error::Api { message, .. } if message == "Bad Gateway"));
    }

    #[test]
    fn for_status() {
        assert!(matches!(
            Error::for_status(429, Some(Duration::from_secs(3))),
            Some(Error::RateLimited {
                status_code: Some(429),
                ..
            })
        ));
        assert!(matches!(
            Error::for_status(402, None),
            Some(Error::QuotaExhausted { .. })
        ));
        assert!(matches!(
            Error::for_status(401, None),
            Some(Error::InvalidKey { .. })
        ));
        assert!(Error::for_status(500, None).is_none());
        assert!(Error::for_status(200, None).is_none());
    }

    #[test]
    fn display() {
        let e = Error::RateLimited {
            retry_after: Some(Duration::from_secs(5)),
            status_code: Some(429),
            message: Some("slow down".into()),
        };
        assert_eq!(
            e.to_string(),
            "acnologia: rate limited (retry after 5s): slow down"
        );
    }
}
