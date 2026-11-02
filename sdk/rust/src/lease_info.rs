//! The data of a lease, shared by the async and blocking `Lease` types.

use std::fmt;

use crate::core::{self, LeaseWire, Redacted};
use crate::types::{Auth, Credential};

/// The fields of a lease. Both `Lease` types deref to this, so
/// `lease.key_id` and `lease.secret()` work on either.
///
/// The secret is private and never printed: `Debug` and `Display` show
/// `[redacted]` in its place.
#[derive(Clone, PartialEq, Eq)]
pub struct LeaseInfo {
    /// The lease id, `lse_...`.
    pub lease_id: String,
    /// The id of the leased key. Safe to log.
    pub key_id: i64,
    /// The service slug.
    pub service: String,
    /// Where the provider expects the key.
    pub auth: Auth,
    /// The provider's base URL.
    pub base_url: String,
    /// When the lease ends, as RFC 3339 text from the server.
    pub expires_at: String,
    /// True when the key is held exclusively for this lease.
    pub exclusive: bool,
    secret: String,
}

impl LeaseInfo {
    pub(crate) fn from_wire(w: LeaseWire) -> Self {
        LeaseInfo {
            lease_id: w.lease_id,
            key_id: w.key_id,
            service: w.service,
            auth: w.auth,
            base_url: w.base_url,
            expires_at: w.expires_at,
            exclusive: w.exclusive,
            secret: w.secret,
        }
    }

    /// The raw key. Handle it with care: do not log it.
    pub fn secret(&self) -> &str {
        &self.secret
    }

    /// Where and how to send the key, following [`LeaseInfo::auth`]: a query
    /// parameter, a header, or an `Authorization: Bearer` style header. A non
    /// empty `auth.prefix` goes in front of the secret for every type; for
    /// bearer it replaces the default `Bearer ` prefix. Header names default
    /// to `Authorization` and query names to `api_key` when empty.
    pub fn credential(&self) -> Credential {
        core::credential(&self.auth, &self.secret)
    }

    /// The provider's base URL joined with `path` and `query`. The secret is
    /// not added; use `apply` on the request for that.
    pub fn url(&self, path: &str, query: &[(&str, &str)]) -> String {
        core::join_url(&self.base_url, path, query)
    }

    /// Puts the secret on a built request, replacing an existing header or
    /// query parameter of the same name.
    pub fn apply_to(&self, req: &mut reqwest::Request) -> Result<(), crate::Error> {
        match self.credential() {
            Credential::Header { name, value } => {
                let name = reqwest::header::HeaderName::from_bytes(name.as_bytes())
                    .map_err(|_| crate::Error::InvalidInput("bad auth header name".into()))?;
                let mut value = reqwest::header::HeaderValue::from_str(&value)
                    .map_err(|_| crate::Error::InvalidInput("bad auth header value".into()))?;
                value.set_sensitive(true);
                req.headers_mut().insert(name, value);
            }
            Credential::Query { name, value } => set_query(req.url_mut(), &name, &value),
        }
        Ok(())
    }
}

pub(crate) fn set_query(u: &mut url::Url, name: &str, value: &str) {
    let kept: Vec<(String, String)> = u
        .query_pairs()
        .filter(|(k, _)| k != name)
        .map(|(k, v)| (k.into_owned(), v.into_owned()))
        .collect();
    let mut q = u.query_pairs_mut();
    q.clear();
    for (k, v) in &kept {
        q.append_pair(k, v);
    }
    q.append_pair(name, value);
}

impl fmt::Debug for LeaseInfo {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Lease")
            .field("lease_id", &self.lease_id)
            .field("key_id", &self.key_id)
            .field("service", &self.service)
            .field("secret", &Redacted)
            .field("auth", &self.auth)
            .field("base_url", &self.base_url)
            .field("expires_at", &self.expires_at)
            .field("exclusive", &self.exclusive)
            .finish()
    }
}

impl fmt::Display for LeaseInfo {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "acnologia lease {} (key {}, service {}, secret [redacted], expires {})",
            self.lease_id, self.key_id, self.service, self.expires_at
        )
    }
}
