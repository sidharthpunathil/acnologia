/*
 * Acnologia C SDK.
 *
 * A client for the Acnologia consumer API: the proxy (/proxy/<service>/...),
 * leases (/api/v1/lease and /api/v1/leases/<id>/...), /api/v1/services,
 * usage guides, capabilities, the forward proxy setup and /api/v1/me,
 * authenticated with an app token (acn_...).
 *
 * Conventions
 *
 * - Every call returns an acn_code. ACN_OK is zero.
 * - Every call that can fail takes an acn_error pointer last. It may be NULL.
 *   On failure it is filled with the code, the HTTP status, the server's error
 *   code and message, and the retry hint. The message never holds the app
 *   token, the access password or a lease secret.
 * - Out pointers are set only on ACN_OK. On failure they are left NULL.
 * - Every struct the SDK returns has a matching free function, and every
 *   string it returns on its own is freed with acn_free. Free functions accept
 *   NULL.
 * - Input pointers are borrowed for the length of the call only.
 *
 * Thread safety
 *
 * Call acn_global_init once at program start, before any other thread runs,
 * and acn_global_cleanup once at the end. An acn_client does not change after
 * acn_client_new, and each call uses its own libcurl handle, so one client may
 * be shared by many threads. An acn_lease is not synchronized: use it from one
 * thread at a time. The SDK keeps no global state of its own.
 */
#ifndef ACNOLOGIA_ACNOLOGIA_H
#define ACNOLOGIA_ACNOLOGIA_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Sent as X-Acnologia-Client and in the User-Agent: acnologia-c/<version>. */
#define ACN_VERSION "0.2.0"

/* The longest wait_sec the server accepts on a lease. */
#define ACN_MAX_WAIT_SEC 60

/* Result codes. Each server error code has its own value; anything else with
 * a non 2xx status is ACN_ERR_API, and acn_error.error_code says which. */
typedef enum acn_code {
    ACN_OK = 0,
    ACN_ERR_INVALID_ARGUMENT,     /* a bad argument, or missing URL or token */
    ACN_ERR_NO_MEMORY,
    ACN_ERR_TRANSPORT,            /* no answer: refused, DNS, TLS, timeout */
    ACN_ERR_BAD_RESPONSE,         /* a 2xx answer the SDK could not read */
    ACN_ERR_API,                  /* any other non 2xx answer */
    ACN_ERR_UNAUTHENTICATED,      /* 401, or 403 unauthenticated: token missing, wrong, or app disabled */
    ACN_ERR_SERVICE_NOT_ALLOWED,  /* 403: this app may not use the service */
    ACN_ERR_PROXY_NOT_ALLOWED,    /* 403: the app is lease only */
    ACN_ERR_LEASE_NOT_ALLOWED,    /* 403: the app is proxy only, or the service is proxy only */
    ACN_ERR_NO_KEY_AVAILABLE,     /* 503: every key is cooling down, exhausted or excluded */
    ACN_ERR_ALL_KEYS_BUSY,        /* 503: every usable key is at its concurrency limit */
    ACN_ERR_SERVICE_UNAVAILABLE,  /* 503: the provider's circuit breaker is open */
    ACN_ERR_APP_LIMIT_REACHED,    /* 429: a daily or monthly request or budget limit */
    ACN_ERR_SHARE_REVOKED,        /* 403: share token, the owner revoked the share */
    ACN_ERR_SHARE_PAUSED,         /* 403: share token, the owner paused the share */
    ACN_ERR_SHARE_EXPIRED,        /* 403: share token, the share expired */
    ACN_ERR_LEASE_NOT_FOUND,      /* 404: renewing a lease that is not open */
    ACN_ERR_REPORT_FAILED,        /* 409 or 410: lease already reported or long expired */
    ACN_ERR_UPSTREAM_UNREACHABLE, /* 502: the provider could not be reached */
    ACN_ERR_UPSTREAM_BLOCKED,     /* 502: the provider address is blocked */
    ACN_ERR_LOOP_DETECTED,        /* 508: too many Acnologia hops */
    ACN_ERR_BAD_PATH,             /* 400: proxy path with ?, # or a dot segment */
    ACN_ERR_BODY_TOO_LARGE,       /* 413: proxy body over the server's limit */
    ACN_ERR_ATTEMPTS_EXHAUSTED    /* acn_with_key: every attempt was rejected */
} acn_code;

/* What went wrong. Caller owned, fixed size, nothing to free. */
typedef struct acn_error {
    acn_code code;
    int status;               /* HTTP status, 0 when there was no answer */
    char error_code[64];      /* the server's code, such as "no_key_available" */
    char message[512];        /* readable detail, secrets removed */
    double retry_after_sec;   /* seconds to wait, 0 when unknown */
    char limit[32];           /* app_limit_reached: which limit, such as "daily_requests" */
    char reset_at[64];        /* app_limit_reached: RFC 3339 time the limit resets */
} acn_error;

/* A short English name for a code, such as "no key available". */
const char *acn_strerror(acn_code code);

/* True (1) for the two no key codes: ACN_ERR_NO_KEY_AVAILABLE and
 * ACN_ERR_ALL_KEYS_BUSY. */
int acn_is_no_key(acn_code code);

/* True (1) for the three share codes: ACN_ERR_SHARE_REVOKED,
 * ACN_ERR_SHARE_PAUSED and ACN_ERR_SHARE_EXPIRED (the share is unavailable).
 * The server also sends the state as X-Acnologia-Share. */
int acn_is_share_unavailable(acn_code code);

/* The share's state for a share code: "revoked", "paused" or "expired".
 * NULL for any other code. */
const char *acn_share_status(acn_code code);

/* Wrap curl_global_init and curl_global_cleanup. Call once each, from the
 * main thread, before and after every other thread uses the SDK. */
acn_code acn_global_init(void);
void acn_global_cleanup(void);

/* Frees a string returned by the SDK. */
void acn_free(void *ptr);

/* A name and value pair, borrowed: query parameters and request headers. */
typedef struct acn_pair {
    const char *name;
    const char *value;
} acn_pair;

/* A response header, owned by its acn_response. */
typedef struct acn_header {
    char *name;
    char *value;
} acn_header;

/* ---- Client ---------------------------------------------------------- */

typedef struct acn_client acn_client;

typedef struct acn_client_options {
    /* Server URL, such as http://127.0.0.1:8080. NULL reads ACNOLOGIA_URL. */
    const char *base_url;
    /* App token. NULL reads ACNOLOGIA_TOKEN. */
    const char *token;
    /* Sent as X-Acnologia-Access when the server has an access password.
     * NULL reads ACNOLOGIA_ACCESS_PASSWORD, but only when base_url is the
     * server it belongs to: ACNOLOGIA_ACCESS_URL when set, else ACNOLOGIA_URL.
     * "" sends no header and ignores the environment. */
    const char *access_password;
    /* Timeout for API calls in milliseconds. 0 means 75000; negative means
     * none. A lease with wait_sec stretches it to wait_sec plus 10 seconds. */
    long timeout_ms;
    /* Connect timeout in milliseconds. 0 means 10000; negative means none. */
    long connect_timeout_ms;
    /* Timeout for proxy calls in milliseconds. 0 or negative means none,
     * since a provider may take long. */
    long proxy_timeout_ms;
    /* User-Agent header. NULL means acnologia-c/<version>. */
    const char *user_agent;
} acn_client_options;

/* Sets every field to its default (all NULL and zero). */
void acn_client_options_init(acn_client_options *opts);

/* Creates a client. opts may be NULL to read everything from the
 * environment. Fails with ACN_ERR_INVALID_ARGUMENT when the URL or token is
 * missing. The client copies every string it is given. */
acn_code acn_client_new(const acn_client_options *opts, acn_client **out, acn_error *err);
void acn_client_free(acn_client *client);

/* The base URL without a trailing slash. Owned by the client. */
const char *acn_client_base_url(const acn_client *client);

/* ---- Proxy ----------------------------------------------------------- */

typedef struct acn_proxy_request {
    const char *method;            /* NULL means GET */
    const char *service;           /* service or group slug, required */
    /* Path under the service, such as "/v1/chat". It may carry its own query
     * string. A path that already starts with /proxy/<service>/ is kept. */
    const char *path;
    const acn_pair *query;         /* appended to the URL, encoded */
    size_t query_count;
    const acn_pair *headers;       /* sent as given; Authorization and User-Agent are set by the SDK */
    size_t header_count;
    const void *body;              /* may be NULL */
    size_t body_len;
    const char *idempotency_key;   /* sent as Idempotency-Key when not NULL */
    int no_cache;                  /* 1 sends Cache-Control: no-cache (since 0.2.0) */
} acn_proxy_request;

/* One route a capability call tried, from X-Acnologia-Tried. */
typedef struct acn_tried {
    char *service;
    int status;                    /* 0 when the route failed before an answer */
} acn_tried;

typedef struct acn_response {
    int status;
    acn_header *headers;           /* in the order received */
    size_t header_count;
    char *body;                    /* body_len bytes, plus a NUL after them */
    size_t body_len;

    /* From the X-Acnologia-* answer headers. */
    int64_t key_id;                /* X-Acnologia-Key-Id, -1 when absent */
    int attempts;                  /* X-Acnologia-Attempts, 0 when absent */
    const char *service;           /* X-Acnologia-Service, NULL when absent */
    const char *cache;             /* X-Acnologia-Cache ("hit", "replay"), NULL when absent */
    int idempotent_replayed;       /* 1 when Idempotent-Replayed: true */

    /* Since 0.2.0, set on capability calls. */
    const char *capability;        /* X-Acnologia-Capability, NULL when absent */
    acn_tried *tried;              /* X-Acnologia-Tried, in order; NULL when absent */
    size_t tried_count;
} acn_response;

/* Sends a request through the proxy. Redirects are never followed.
 *
 * The provider's answer comes back as it is, whatever its status: ACN_OK
 * with *out set. When Acnologia itself refuses the request (no key, app
 * limit, bad path and the others), the call fails with the matching code and
 * *out stays NULL. Acnologia's own refusals are told apart by the missing
 * X-Acnologia-Service header. */
acn_code acn_proxy(acn_client *client, const acn_proxy_request *req, acn_response **out,
                   acn_error *err);
void acn_response_free(acn_response *resp);

/* The first header with this name, compared without case, or NULL. Owned by
 * the response. */
const char *acn_response_header(const acn_response *resp, const char *name);

/* ---- Leases ---------------------------------------------------------- */

/* Where the provider expects the key. type is "query", "header" or
 * "bearer"; name is the parameter or header name; a non empty prefix goes in
 * front of the secret (for bearer it replaces "Bearer "). */
typedef struct acn_auth {
    char *type;
    char *name;
    char *prefix;
} acn_auth;

/* A key handed out by the pool. Read the fields, do not change them. Report
 * it when done: on services with a concurrency limit an unreported lease
 * holds one of the key's slots until its TTL runs out. */
typedef struct acn_lease {
    char *lease_id;
    int64_t key_id;
    char *service;
    char *secret;                  /* the provider key: never log it */
    acn_auth auth;
    char *base_url;                /* without a trailing slash */
    char *expires_at;              /* RFC 3339, updated by acn_lease_renew */
    int exclusive;
    int reported;                  /* set once acn_lease_report succeeded */
} acn_lease;

typedef struct acn_lease_options {
    const int64_t *exclude_key_ids; /* keys that must not be returned */
    size_t exclude_count;
    int ttl_sec;                    /* 0 uses the server default */
    int wait_sec;                   /* 0 to ACN_MAX_WAIT_SEC; 0 fails fast when busy */
    int exclusive;                  /* 1 keeps the key from every other caller */
} acn_lease_options;

/* POST /api/v1/lease. opts may be NULL. Fails with ACN_ERR_NO_KEY_AVAILABLE
 * or ACN_ERR_ALL_KEYS_BUSY (see retry_after_sec) when no key can be had. */
acn_code acn_lease_acquire(acn_client *client, const char *service, const acn_lease_options *opts,
                           acn_lease **out, acn_error *err);
void acn_lease_free(acn_lease *lease);

/* POST /api/v1/leases/<id>/renew. ttl_sec 0 uses the server default (120s).
 * Updates lease->expires_at. ACN_ERR_LEASE_NOT_FOUND when it is not open. */
acn_code acn_lease_renew(acn_client *client, acn_lease *lease, int ttl_sec, acn_error *err);

typedef enum acn_outcome {
    ACN_OUTCOME_OK = 0,
    ACN_OUTCOME_RATE_LIMITED,
    ACN_OUTCOME_QUOTA_EXHAUSTED,
    ACN_OUTCOME_INVALID,
    ACN_OUTCOME_ERROR,
    ACN_OUTCOME_BUSY,              /* the account is at its concurrency cap */
    ACN_OUTCOME_BANNED             /* the provider suspended the account */
} acn_outcome;

/* The wire name: "ok", "rate_limited" and so on. */
const char *acn_outcome_name(acn_outcome outcome);

typedef struct acn_report {
    acn_outcome outcome;
    int status_code;               /* the provider's status, 0 to omit */
    int retry_after_sec;           /* cooldown, 0 to omit */
    const char *message;           /* optional; the lease secret is removed before sending */
    int rotate;                    /* 1 asks for the next key in the same call */
} acn_report;

/* POST /api/v1/leases/<id>/report. next may be NULL. When report->rotate is
 * set and another key is free, *next is the new lease (free it too);
 * otherwise *next is NULL. Sets lease->reported. */
acn_code acn_lease_report(acn_client *client, acn_lease *lease, const acn_report *report,
                          acn_lease **next, acn_error *err);

/* Puts the secret where the provider expects it, for any HTTP library.
 *
 * For query auth, *out_url is url with name=<prefix><secret> appended and
 * *out_header is NULL. For header and bearer auth, *out_url is a copy of url
 * and *out_header is a "Name: value" line (Authorization when the name is
 * empty, "Bearer <secret>" for bearer without a prefix). Free both with
 * acn_free. They hold the secret: do not log them. out_header may be NULL
 * when you only want the URL of a query auth service. An unknown auth type
 * fails with ACN_ERR_INVALID_ARGUMENT. */
acn_code acn_lease_apply(const acn_lease *lease, const char *url, char **out_url, char **out_header,
                         acn_error *err);

/* base_url joined with path, without the secret. Free with acn_free. */
acn_code acn_lease_url(const acn_lease *lease, const char *path, char **out, acn_error *err);

/* Leases a key and calls fn until it succeeds.
 *
 * fn gets the lease and a report already set to ACN_OUTCOME_OK. It sets the
 * outcome (and status_code, retry_after_sec, message) to say how the key
 * did. RATE_LIMITED, QUOTA_EXHAUSTED and INVALID report the key with rotate
 * and call fn again with the next key, up to max_attempts (0 means 5). Any
 * other outcome is reported and ends the loop with ACN_OK. Every lease is
 * reported. After the last rejected attempt the result is
 * ACN_ERR_ATTEMPTS_EXHAUSTED; when the pool runs dry it is the no key error. */
typedef void (*acn_key_fn)(const acn_lease *lease, acn_report *report, void *user);

acn_code acn_with_key(acn_client *client, const char *service, const acn_lease_options *opts,
                      int max_attempts, acn_key_fn fn, void *user, acn_error *err);

/* ---- Services and me ------------------------------------------------- */

typedef struct acn_service {
    char *slug;
    char *name;
    char *provider;
    char *category;
    int available;
    char *base_url;
    acn_auth auth;
    char *guide_url;               /* the usage guide's URL, "" when not given (since 0.2.0) */
} acn_service;

typedef struct acn_service_list {
    acn_service *items;
    size_t count;
} acn_service_list;

/* GET /api/v1/services: the services this token may use. */
acn_code acn_services(acn_client *client, acn_service_list **out, acn_error *err);
void acn_service_list_free(acn_service_list *list);

typedef struct acn_me_service {
    char *slug;
    char *name;
    char *provider;
    char *category;
    int64_t available;             /* keys free now for a share token; 1 or 0 for an app */
    char *logo_url;                /* a data: image URI, NULL when none (since 0.2.0) */
} acn_me_service;

typedef struct acn_me_share {
    char *id;
    char *name;
    char *owner_name;
    char *owner_id;
    char *recipient;
    char *label;
    char *status;                  /* "active", "paused", "expired", "revoked" */
    char *mode;
    char *ends_at;                 /* RFC 3339, NULL when it does not end */
} acn_me_share;

typedef struct acn_me {
    int64_t app_id;
    char *app_name;

    /* Limits, -1 when not set. */
    int64_t daily_requests_limit;
    int64_t monthly_requests_limit;
    double daily_budget_limit;
    double monthly_budget_limit;

    /* Use so far. */
    int64_t today_requests;
    double today_cost;
    int64_t month_requests;
    double month_cost;
    char *last_used_at;            /* RFC 3339 or NULL */

    /* The limit the app has reached, both NULL when none. */
    char *limit_hit;               /* such as "daily_requests" */
    char *limit_reset_at;

    acn_me_service *services;
    size_t service_count;

    acn_me_share *share;           /* NULL for an ordinary app */

    char *raw_json;                /* the whole answer, for fields not listed here */
} acn_me;

/* GET /api/v1/me: the token's own app, limits, use, services and share. */
acn_code acn_me_get(acn_client *client, acn_me **out, acn_error *err);
void acn_me_free(acn_me *me);

/* ---- Usage guides ---------------------------------------------------- */

typedef struct acn_guide {
    char *markdown;                /* the guide as Markdown */
    int generated;                 /* 1 when no guide was written and this one was generated */
    char *guide_json;              /* the structured guide as raw JSON ("null" when none) */
} acn_guide;

/* GET /api/v1/services/<slug>/guide: how to call the service. Fails with
 * ACN_ERR_SERVICE_NOT_ALLOWED for a service the token may not use. */
acn_code acn_guide_get(acn_client *client, const char *slug, acn_guide **out, acn_error *err);
void acn_guide_free(acn_guide *guide);

/* The same guide as Markdown only (Accept: text/markdown). Free *out with
 * acn_free. */
acn_code acn_guide_markdown(acn_client *client, const char *slug, char **out, acn_error *err);

/* ---- Capabilities ---------------------------------------------------- */

typedef struct acn_capability_route {
    char *service;
    int mapped;                    /* 1 when the route maps the answer to the capability's fields */
} acn_capability_route;

/* One job many providers can do, behind one slug. */
typedef struct acn_capability {
    char *slug;
    char *name;
    char *description;
    char *strategy;
    char *params_json;             /* the params as raw JSON ("null" when none) */
    acn_capability_route *routes;
    size_t route_count;
    char *url;                     /* <server>/proxy/<slug> */
} acn_capability;

typedef struct acn_capability_list {
    acn_capability *items;
    size_t count;
} acn_capability_list;

/* GET /api/v1/capabilities: the capabilities this token may call. Empty for
 * a share's token. */
acn_code acn_capabilities(acn_client *client, acn_capability_list **out, acn_error *err);
void acn_capability_list_free(acn_capability_list *list);

typedef struct acn_capability_options {
    const char *service;           /* adds ?_service=<slug>: use only that route */
    int raw;                       /* 1 adds ?_raw=1: the provider's body, unmapped */
    const char *idempotency_key;   /* sent as Idempotency-Key when not NULL */
    int no_cache;                  /* 1 sends Cache-Control: no-cache */
} acn_capability_options;

/* POST /proxy/<slug> with params_json (a JSON object, NULL means {}) as the
 * body. opts may be NULL. Answers and errors as for acn_proxy; the response
 * carries capability, service and tried. */
acn_code acn_capability_call(acn_client *client, const char *slug, const char *params_json,
                             const acn_capability_options *opts, acn_response **out, acn_error *err);

/* ---- Forward proxy --------------------------------------------------- */

/* A host the forward proxy sends to a service. */
typedef struct acn_forward_host {
    char *host;
    char *service;
    char *name;
    char *path;
} acn_forward_host;

/* How to use Acnologia as an HTTP(S) proxy. None of these fields holds the
 * token: proxy_url_with_token has an "<app token>" placeholder. */
typedef struct acn_forward_proxy {
    char *proxy_url;
    char *proxy_url_with_token;
    char *ca_url;
    char *ca_fingerprint;
    int passthrough;               /* 1 when HTTPS to other hosts is tunneled */
    acn_forward_host *hosts;
    size_t host_count;
    acn_header *env;               /* the server's suggested environment */
    size_t env_count;
    char *service_header;          /* request header that picks a service */
} acn_forward_proxy;

/* GET /api/v1/proxy. */
acn_code acn_forward_proxy_get(acn_client *client, acn_forward_proxy **out, acn_error *err);
void acn_forward_proxy_free(acn_forward_proxy *fp);

/* GET /api/v1/proxy/ca.pem, sent without the token (it is public). Free
 * *out with acn_free. */
acn_code acn_forward_proxy_ca(acn_client *client, char **out, acn_error *err);

/* Fetches the CA and writes it to path (NULL means "acnologia-ca.pem"). */
acn_code acn_forward_proxy_save_ca(acn_client *client, const char *path, acn_error *err);

/* The proxy URL with user "app" and the client's token as the password,
 * percent escaped: http://app:acn_...@host:8888. It holds the token: do not
 * log it. Free with acn_free. */
acn_code acn_forward_proxy_url(acn_client *client, char **out, acn_error *err);

/* Environment variables, owned by the acn_env. */
typedef struct acn_env {
    acn_header *vars;
    size_t count;
} acn_env;

/* The environment that sends an app through the forward proxy: HTTPS_PROXY
 * and HTTP_PROXY (acn_forward_proxy_url), NODE_USE_ENV_PROXY=1, and
 * NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE and SSL_CERT_FILE set to ca_path
 * (NULL means "acnologia-ca.pem"). The proxy values hold the token: do not
 * log them. Free with acn_env_free, which wipes them. */
acn_code acn_forward_proxy_env(acn_client *client, const char *ca_path, acn_env **out, acn_error *err);
void acn_env_free(acn_env *env);

/* The value of one variable, or NULL. Owned by the env. */
const char *acn_env_get(const acn_env *env, const char *name);

#ifdef __cplusplus
}
#endif

#endif /* ACNOLOGIA_ACNOLOGIA_H */
