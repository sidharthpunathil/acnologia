#include <ctype.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <curl/curl.h>

#include "cJSON.h"
#include "internal.h"

#define DEFAULT_TIMEOUT_MS 75000L
#define DEFAULT_CONNECT_TIMEOUT_MS 10000L
#define CLIENT_ID "acnologia-c/" ACN_VERSION

struct acn_client {
    char *base_url;
    char *token;
    char *access_password; /* NULL when none is sent */
    char *user_agent;
    long timeout_ms;         /* 0 means none */
    long connect_timeout_ms; /* 0 means none */
    long proxy_timeout_ms;   /* 0 means none */
};

/* ---- small helpers ---- */

static char *join2(const char *a, const char *b) {
    acn__buf buf = {0};
    acn__buf_puts(&buf, a);
    acn__buf_puts(&buf, b);
    return acn__buf_take(&buf);
}

static int has_crlf(const char *s) { return s && (strchr(s, '\r') || strchr(s, '\n')); }

static const char *env(const char *name) {
    const char *v = getenv(name);
    return (v && *v) ? v : NULL;
}

/* scheme://host[:port], lower case, so two spellings of one server match. */
static char *origin_of(const char *url) {
    const char *p = strstr(url, "://");
    if (!p) return NULL;
    p += 3;
    size_t n = (size_t)(p - url);
    while (p[0] && p[0] != '/' && p[0] != '?' && p[0] != '#') {
        p++;
        n++;
    }
    char *o = acn__strndup(url, n);
    if (o) {
        for (char *c = o; *c; c++) *c = (char)tolower((unsigned char)*c);
    }
    return o;
}

static int same_origin(const char *a, const char *b) {
    char *oa = origin_of(a), *ob = origin_of(b);
    int same = oa && ob && strcmp(oa, ob) == 0;
    free(oa);
    free(ob);
    return same;
}

static size_t redactions(const acn_client *c, const char *secret, acn__redact out[3]) {
    size_t n = 0;
    out[n].needle = c->token;
    out[n++].replacement = "[token]";
    if (c->access_password) {
        out[n].needle = c->access_password;
        out[n++].replacement = "[redacted]";
    }
    if (secret) {
        out[n].needle = secret;
        out[n++].replacement = "[key]";
    }
    return n;
}

static acn_code fail_transport(const acn_client *c, const char *secret, const char *what, acn_error *err) {
    acn__redact r[3];
    size_t n = redactions(c, secret, r);
    char *msg = acn__scrub(what, strlen(what), r, n, ACN__MAX_MESSAGE);
    acn__fail(err, ACN_ERR_TRANSPORT, msg ? msg : "transport error");
    free(msg);
    return ACN_ERR_TRANSPORT;
}

static struct curl_slist *add_header(struct curl_slist *list, int *ok, const char *name, const char *value) {
    if (!*ok) return list;
    acn__buf b = {0};
    acn__buf_puts(&b, name);
    acn__buf_puts(&b, value ? ": " : ":");
    acn__buf_puts(&b, value);
    char *line = acn__buf_take(&b);
    struct curl_slist *next = line ? curl_slist_append(list, line) : NULL;
    if (line) {
        /* The line may hold the token: wipe it before freeing. */
        acn__wipe(line);
        free(line);
    }
    if (!next) {
        *ok = 0;
        return list;
    }
    return next;
}

static struct curl_slist *public_headers(const acn_client *c, struct curl_slist *list, int *ok) {
    list = add_header(list, ok, "X-Acnologia-Client", CLIENT_ID);
    if (c->access_password) list = add_header(list, ok, "X-Acnologia-Access", c->access_password);
    /* No 100-continue round trip. */
    list = add_header(list, ok, "Expect", NULL);
    return list;
}

static struct curl_slist *sdk_headers(const acn_client *c, struct curl_slist *list, int *ok) {
    acn__buf auth = {0};
    acn__buf_puts(&auth, "Bearer ");
    acn__buf_puts(&auth, c->token);
    char *bearer = acn__buf_take(&auth);
    if (!bearer) {
        *ok = 0;
        return list;
    }
    list = add_header(list, ok, "Authorization", bearer);
    acn__wipe(bearer);
    free(bearer);
    return public_headers(c, list, ok);
}

/* ---- client ---- */

void acn_client_options_init(acn_client_options *opts) {
    if (opts) memset(opts, 0, sizeof *opts);
}

void acn_client_free(acn_client *c) {
    if (!c) return;
    acn__wipe(c->token);
    acn__wipe(c->access_password);
    free(c->base_url);
    free(c->token);
    free(c->access_password);
    free(c->user_agent);
    free(c);
}

acn_code acn_client_new(const acn_client_options *opts, acn_client **out, acn_error *err) {
    if (!out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "out is NULL");
    *out = NULL;
    acn_client_options defaults;
    acn_client_options_init(&defaults);
    if (!opts) opts = &defaults;

    const char *url = opts->base_url ? opts->base_url : env("ACNOLOGIA_URL");
    const char *token = opts->token ? opts->token : env("ACNOLOGIA_TOKEN");
    if (!url || !*url) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "base URL is missing: pass base_url or set ACNOLOGIA_URL");
    }
    if (!token || !*token) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "app token is missing: pass token or set ACNOLOGIA_TOKEN");
    }
    if (strncmp(url, "http://", 7) != 0 && strncmp(url, "https://", 8) != 0) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "base URL must start with http:// or https://");
    }
    if (has_crlf(url) || has_crlf(token) || has_crlf(opts->access_password) || has_crlf(opts->user_agent)) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "options must not contain line breaks");
    }

    const char *password = NULL;
    if (opts->access_password) {
        password = *opts->access_password ? opts->access_password : NULL;
    } else if (env("ACNOLOGIA_ACCESS_PASSWORD")) {
        /* Only to the server the password belongs to. */
        const char *home = env("ACNOLOGIA_ACCESS_URL") ? env("ACNOLOGIA_ACCESS_URL") : env("ACNOLOGIA_URL");
        if (home && same_origin(home, url) && !has_crlf(env("ACNOLOGIA_ACCESS_PASSWORD"))) {
            password = env("ACNOLOGIA_ACCESS_PASSWORD");
        }
    }

    acn_client *c = calloc(1, sizeof *c);
    if (!c) return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    size_t ulen = strlen(url);
    while (ulen > 0 && url[ulen - 1] == '/') ulen--;
    c->base_url = acn__strndup(url, ulen);
    c->token = acn__strdup(token);
    c->access_password = password ? acn__strdup(password) : NULL;
    c->user_agent = acn__strdup(opts->user_agent ? opts->user_agent : CLIENT_ID);
    if (!c->base_url || !c->token || !c->user_agent || (password && !c->access_password)) {
        acn_client_free(c);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    c->timeout_ms = opts->timeout_ms == 0 ? DEFAULT_TIMEOUT_MS : (opts->timeout_ms < 0 ? 0 : opts->timeout_ms);
    c->connect_timeout_ms = opts->connect_timeout_ms == 0
                                ? DEFAULT_CONNECT_TIMEOUT_MS
                                : (opts->connect_timeout_ms < 0 ? 0 : opts->connect_timeout_ms);
    c->proxy_timeout_ms = opts->proxy_timeout_ms > 0 ? opts->proxy_timeout_ms : 0;
    *out = c;
    return ACN_OK;
}

const char *acn_client_base_url(const acn_client *c) { return c ? c->base_url : NULL; }

/* ---- JSON API calls ---- */

/* Sends a JSON request to path and parses a 2xx answer into *out. secret,
 * when set, is scrubbed from error messages too. */
static acn_code api_call(acn_client *c, const char *method, const char *path, const cJSON *body,
                         long timeout_ms, const char *secret, cJSON **out, acn_error *err) {
    *out = NULL;
    char *url = join2(c->base_url, path);
    char *payload = body ? cJSON_PrintUnformatted(body) : NULL;
    int ok = 1;
    struct curl_slist *headers = sdk_headers(c, NULL, &ok);
    headers = add_header(headers, &ok, "Accept", "application/json");
    if (body) headers = add_header(headers, &ok, "Content-Type", "application/json");
    if (!url || (body && !payload) || !ok) {
        free(url);
        free(payload);
        curl_slist_free_all(headers);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }

    acn__http_req req = {0};
    req.method = method;
    req.url = url;
    req.user_agent = c->user_agent;
    req.headers = headers;
    req.body = payload;
    req.body_len = payload ? strlen(payload) : 0;
    req.has_body = payload != NULL;
    req.timeout_ms = timeout_ms;
    req.connect_timeout_ms = c->connect_timeout_ms;

    acn__http_resp resp = {0};
    char errbuf[512];
    int failed = acn__http_do(&req, &resp, errbuf, sizeof errbuf);
    curl_slist_free_all(headers);
    acn__wipe(payload);
    free(payload);
    free(url);

    acn_code rc = ACN_OK;
    if (failed) {
        rc = fail_transport(c, secret, errbuf, err);
    } else if (resp.status < 200 || resp.status > 299) {
        acn__redact r[3];
        size_t n = redactions(c, secret, r);
        rc = acn__map_error(&resp, r, n, err);
    } else {
        cJSON *json = resp.body.len ? cJSON_ParseWithLength(resp.body.data, resp.body.len) : NULL;
        if (!cJSON_IsObject(json)) {
            cJSON_Delete(json);
            rc = acn__fail(err, ACN_ERR_BAD_RESPONSE, "the server's answer is not a JSON object");
            if (err) err->status = (int)resp.status;
        } else {
            *out = json;
        }
    }
    acn__http_resp_free(&resp);
    return rc;
}

/* GETs path and hands back the 2xx body as text. with_token 0 sends no
 * Authorization header, for the public routes. */
static acn_code text_call(acn_client *c, const char *path, const char *accept, int with_token, char **out,
                          acn_error *err) {
    *out = NULL;
    char *url = join2(c->base_url, path);
    int ok = 1;
    struct curl_slist *headers = with_token ? sdk_headers(c, NULL, &ok) : public_headers(c, NULL, &ok);
    headers = add_header(headers, &ok, "Accept", accept);
    if (!url || !ok) {
        free(url);
        curl_slist_free_all(headers);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    acn__http_req req = {0};
    req.method = "GET";
    req.url = url;
    req.user_agent = c->user_agent;
    req.headers = headers;
    req.timeout_ms = c->timeout_ms;
    req.connect_timeout_ms = c->connect_timeout_ms;

    acn__http_resp resp = {0};
    char errbuf[512];
    int failed = acn__http_do(&req, &resp, errbuf, sizeof errbuf);
    curl_slist_free_all(headers);
    free(url);

    acn_code rc = ACN_OK;
    if (failed) {
        rc = fail_transport(c, NULL, errbuf, err);
    } else if (resp.status < 200 || resp.status > 299) {
        acn__redact r[3];
        size_t n = redactions(c, NULL, r);
        rc = acn__map_error(&resp, r, n, err);
    } else {
        *out = acn__buf_take(&resp.body);
        if (!*out) rc = acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    acn__http_resp_free(&resp);
    return rc;
}

static const char *jstr(const cJSON *obj, const char *name) {
    const cJSON *v = cJSON_GetObjectItemCaseSensitive(obj, name);
    return cJSON_IsString(v) ? v->valuestring : NULL;
}

/* Copies a string field; *ok drops to 0 only when memory runs out. */
static char *jdup(const cJSON *obj, const char *name, int *ok) {
    const char *s = jstr(obj, name);
    if (!s) return NULL;
    char *d = acn__strdup(s);
    if (!d) *ok = 0;
    return d;
}

static char *jdup_or_empty(const cJSON *obj, const char *name, int *ok) {
    const char *s = jstr(obj, name);
    char *d = acn__strdup(s ? s : "");
    if (!d) *ok = 0;
    return d;
}

static int64_t jint(const cJSON *obj, const char *name, int64_t fallback) {
    const cJSON *v = cJSON_GetObjectItemCaseSensitive(obj, name);
    /* Out of range doubles do not convert to int64_t safely. */
    if (!cJSON_IsNumber(v) || !(v->valuedouble > -9.2e18 && v->valuedouble < 9.2e18)) return fallback;
    return (int64_t)v->valuedouble;
}

static double jnum(const cJSON *obj, const char *name, double fallback) {
    const cJSON *v = cJSON_GetObjectItemCaseSensitive(obj, name);
    return cJSON_IsNumber(v) ? v->valuedouble : fallback;
}

static int jbool(const cJSON *obj, const char *name) {
    const cJSON *v = cJSON_GetObjectItemCaseSensitive(obj, name);
    if (cJSON_IsBool(v)) return cJSON_IsTrue(v);
    if (cJSON_IsNumber(v)) return v->valuedouble != 0;
    return 0;
}

static void free_auth(acn_auth *a) {
    free(a->type);
    free(a->name);
    free(a->prefix);
}

static void parse_auth(const cJSON *obj, acn_auth *a, int *ok) {
    const cJSON *auth = cJSON_GetObjectItemCaseSensitive(obj, "auth");
    a->type = acn__strdup(jstr(auth, "type") && *jstr(auth, "type") ? jstr(auth, "type") : "header");
    if (!a->type) *ok = 0;
    a->name = jdup_or_empty(auth, "name", ok);
    a->prefix = jdup_or_empty(auth, "prefix", ok);
}

/* ---- leases ---- */

void acn_lease_free(acn_lease *l) {
    if (!l) return;
    acn__wipe(l->secret);
    free(l->lease_id);
    free(l->service);
    free(l->secret);
    free_auth(&l->auth);
    free(l->base_url);
    free(l->expires_at);
    free(l);
}

static acn_code parse_lease(const cJSON *j, acn_lease **out, acn_error *err) {
    if (!jstr(j, "lease_id") || !*jstr(j, "lease_id") || !jstr(j, "secret")) {
        return acn__fail(err, ACN_ERR_BAD_RESPONSE, "the lease answer has no lease_id or secret");
    }
    acn_lease *l = calloc(1, sizeof *l);
    if (!l) return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    int ok = 1;
    l->lease_id = jdup(j, "lease_id", &ok);
    l->key_id = jint(j, "key_id", 0);
    l->service = jdup_or_empty(j, "service", &ok);
    l->secret = jdup(j, "secret", &ok);
    parse_auth(j, &l->auth, &ok);
    l->base_url = jdup_or_empty(j, "base_url", &ok);
    if (l->base_url) {
        size_t n = strlen(l->base_url);
        while (n > 0 && l->base_url[n - 1] == '/') l->base_url[--n] = '\0';
    }
    l->expires_at = jdup_or_empty(j, "expires_at", &ok);
    l->exclusive = jbool(j, "exclusive");
    if (!ok) {
        acn_lease_free(l);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    *out = l;
    return ACN_OK;
}

acn_code acn_lease_acquire(acn_client *c, const char *service, const acn_lease_options *opts, acn_lease **out,
                           acn_error *err) {
    if (out) *out = NULL;
    if (!c || !service || !*service || !out) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client, service and out are required");
    }
    acn_lease_options none = {0};
    if (!opts) opts = &none;
    if (opts->wait_sec < 0 || opts->wait_sec > ACN_MAX_WAIT_SEC) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "wait_sec must be between 0 and 60");
    }
    if (opts->ttl_sec < 0) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "ttl_sec must not be negative");
    if (opts->exclude_count && !opts->exclude_key_ids) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "exclude_key_ids is NULL");
    }

    cJSON *body = cJSON_CreateObject();
    int ok = body && cJSON_AddStringToObject(body, "service", service);
    if (ok && opts->exclude_count) {
        cJSON *ids = cJSON_AddArrayToObject(body, "exclude_key_ids");
        ok = ids != NULL;
        for (size_t i = 0; ok && i < opts->exclude_count; i++) {
            cJSON *n = cJSON_CreateNumber((double)opts->exclude_key_ids[i]);
            ok = n && cJSON_AddItemToArray(ids, n);
        }
    }
    if (ok && opts->ttl_sec > 0) ok = cJSON_AddNumberToObject(body, "ttl_sec", opts->ttl_sec) != NULL;
    if (ok && opts->wait_sec > 0) ok = cJSON_AddNumberToObject(body, "wait_sec", opts->wait_sec) != NULL;
    if (ok && opts->exclusive) ok = cJSON_AddTrueToObject(body, "exclusive") != NULL;
    if (!ok) {
        cJSON_Delete(body);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }

    /* A lease that may wait for a slot needs a timeout that outlasts it. */
    long timeout = c->timeout_ms;
    if (timeout > 0 && opts->wait_sec > 0 && timeout < (opts->wait_sec + 10) * 1000L) {
        timeout = (opts->wait_sec + 10) * 1000L;
    }
    cJSON *json = NULL;
    acn_code rc = api_call(c, "POST", "/api/v1/lease", body, timeout, NULL, &json, err);
    cJSON_Delete(body);
    if (rc != ACN_OK) return rc;
    rc = parse_lease(json, out, err);
    cJSON_Delete(json);
    return rc;
}

static char *lease_path(const acn_lease *l, const char *action) {
    acn__buf b = {0};
    acn__buf_puts(&b, "/api/v1/leases/");
    acn__buf_encode(&b, l->lease_id);
    acn__buf_putc(&b, '/');
    acn__buf_puts(&b, action);
    return acn__buf_take(&b);
}

acn_code acn_lease_renew(acn_client *c, acn_lease *l, int ttl_sec, acn_error *err) {
    if (!c || !l || !l->lease_id) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client and lease are required");
    if (ttl_sec < 0) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "ttl_sec must not be negative");
    cJSON *body = cJSON_CreateObject();
    char *path = lease_path(l, "renew");
    int ok = body && path;
    if (ok && ttl_sec > 0) ok = cJSON_AddNumberToObject(body, "ttl_sec", ttl_sec) != NULL;
    if (!ok) {
        cJSON_Delete(body);
        free(path);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    cJSON *json = NULL;
    acn_code rc = api_call(c, "POST", path, body, c->timeout_ms, l->secret, &json, err);
    cJSON_Delete(body);
    free(path);
    if (rc != ACN_OK) return rc;
    const char *exp = jstr(json, "expires_at");
    if (!exp) {
        cJSON_Delete(json);
        rc = acn__fail(err, ACN_ERR_BAD_RESPONSE, "the renew answer has no expires_at");
        if (err) err->status = 200;
        return rc;
    }
    char *copy = acn__strdup(exp);
    cJSON_Delete(json);
    if (!copy) return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    free(l->expires_at);
    l->expires_at = copy;
    return ACN_OK;
}

acn_code acn_lease_report(acn_client *c, acn_lease *l, const acn_report *report, acn_lease **next,
                          acn_error *err) {
    if (next) *next = NULL;
    if (!c || !l || !l->lease_id || !report) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client, lease and report are required");
    }
    const char *outcome = acn_outcome_name(report->outcome);
    if (!outcome) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "unknown outcome");
    if (report->status_code < 0 || report->retry_after_sec < 0) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "status_code and retry_after_sec must not be negative");
    }

    cJSON *body = cJSON_CreateObject();
    char *path = lease_path(l, "report");
    int ok = body && path && cJSON_AddStringToObject(body, "outcome", outcome) &&
             cJSON_AddBoolToObject(body, "rotate", report->rotate ? 1 : 0);
    if (ok && report->status_code > 0) ok = cJSON_AddNumberToObject(body, "status_code", report->status_code) != NULL;
    if (ok && report->retry_after_sec > 0) {
        ok = cJSON_AddNumberToObject(body, "retry_after_sec", report->retry_after_sec) != NULL;
    }
    if (ok && report->message) {
        /* Errors from query auth services often carry the full URL, key
         * included: never send the secret back. */
        acn__redact r[3];
        size_t n = redactions(c, l->secret, r);
        char *msg = acn__scrub(report->message, strlen(report->message), r, n, ACN__MAX_MESSAGE);
        ok = msg && cJSON_AddStringToObject(body, "message", msg);
        free(msg);
    }
    if (!ok) {
        cJSON_Delete(body);
        free(path);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    cJSON *json = NULL;
    acn_code rc = api_call(c, "POST", path, body, c->timeout_ms, l->secret, &json, err);
    cJSON_Delete(body);
    free(path);
    if (rc != ACN_OK) return rc;
    l->reported = 1;
    const cJSON *nx = cJSON_GetObjectItemCaseSensitive(json, "next");
    if (cJSON_IsObject(nx) && next) rc = parse_lease(nx, next, err);
    cJSON_Delete(json);
    return rc;
}

static int is_rotation(acn_outcome o) {
    return o == ACN_OUTCOME_RATE_LIMITED || o == ACN_OUTCOME_QUOTA_EXHAUSTED || o == ACN_OUTCOME_INVALID;
}

acn_code acn_with_key(acn_client *c, const char *service, const acn_lease_options *opts, int max_attempts,
                      acn_key_fn fn, void *user, acn_error *err) {
    if (!c || !service || !fn) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client, service and fn are required");
    if (max_attempts <= 0) max_attempts = 5;
    acn_lease_options lo = {0};
    if (opts) lo = *opts;

    /* Every key tried so far, plus the caller's own exclusions. */
    size_t tried_n = lo.exclude_count;
    int64_t *tried = malloc((tried_n + (size_t)max_attempts) * sizeof *tried);
    if (!tried) return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    if (tried_n) memcpy(tried, lo.exclude_key_ids, tried_n * sizeof *tried);

    acn_lease *lease = NULL;
    acn_code rc = acn_lease_acquire(c, service, &lo, &lease, err);
    for (int attempt = 1; rc == ACN_OK; attempt++) {
        acn_report rep = {0};
        rep.outcome = ACN_OUTCOME_OK;
        fn(lease, &rep, user);
        int rotating = is_rotation(rep.outcome);
        int last = attempt >= max_attempts;
        if (rotating) rep.rotate = !last;
        acn_lease *next = NULL;
        rc = acn_lease_report(c, lease, &rep, &next, err);
        tried[tried_n++] = lease->key_id;
        acn_lease_free(lease);
        lease = NULL;
        if (rc != ACN_OK || !rotating) {
            acn_lease_free(next);
            break;
        }
        if (last) {
            acn_lease_free(next);
            char msg[160];
            snprintf(msg, sizeof msg, "every one of %d attempts was rejected (last outcome %s)", max_attempts,
                     acn_outcome_name(rep.outcome));
            rc = acn__fail(err, ACN_ERR_ATTEMPTS_EXHAUSTED, msg);
            break;
        }
        if (next) {
            lease = next;
        } else {
            lo.exclude_key_ids = tried;
            lo.exclude_count = tried_n;
            rc = acn_lease_acquire(c, service, &lo, &lease, err);
        }
    }
    free(tried);
    return rc;
}

static int valid_auth(const char *type) {
    return strcmp(type, "query") == 0 || strcmp(type, "header") == 0 || strcmp(type, "bearer") == 0;
}

acn_code acn_lease_apply(const acn_lease *l, const char *url, char **out_url, char **out_header, acn_error *err) {
    if (out_url) *out_url = NULL;
    if (out_header) *out_header = NULL;
    if (!l || !l->secret || !url || !out_url) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "lease, url and out_url are required");
    }
    const char *type = l->auth.type ? l->auth.type : "header";
    const char *name = l->auth.name ? l->auth.name : "";
    const char *prefix = l->auth.prefix ? l->auth.prefix : "";
    if (!valid_auth(type)) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "unknown auth type");

    acn__buf u = {0};
    acn__buf_puts(&u, url);
    if (strcmp(type, "query") == 0) {
        size_t len = strlen(url);
        char *hash = strchr(url, '#');
        size_t base_len = hash ? (size_t)(hash - url) : len;
        acn__buf_free(&u);
        acn__buf_append(&u, url, base_len);
        int has_query = memchr(url, '?', base_len) != NULL;
        if (!has_query) {
            acn__buf_putc(&u, '?');
        } else if (base_len && url[base_len - 1] != '?' && url[base_len - 1] != '&') {
            acn__buf_putc(&u, '&');
        }
        acn__buf_encode(&u, *name ? name : "api_key");
        acn__buf_putc(&u, '=');
        acn__buf_encode(&u, prefix);
        acn__buf_encode(&u, l->secret);
        if (hash) acn__buf_puts(&u, hash);
        *out_url = acn__buf_take(&u);
        return *out_url ? ACN_OK : acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    if (!out_header) {
        acn__buf_free(&u);
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "out_header is required for header and bearer auth");
    }
    acn__buf h = {0};
    acn__buf_puts(&h, *name ? name : "Authorization");
    acn__buf_puts(&h, ": ");
    if (strcmp(type, "bearer") == 0 && !*prefix) {
        acn__buf_puts(&h, "Bearer ");
    } else {
        acn__buf_puts(&h, prefix);
    }
    acn__buf_puts(&h, l->secret);
    *out_url = acn__buf_take(&u);
    *out_header = acn__buf_take(&h);
    if (!*out_url || !*out_header) {
        free(*out_url);
        free(*out_header);
        *out_url = *out_header = NULL;
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    return ACN_OK;
}

acn_code acn_lease_url(const acn_lease *l, const char *path, char **out, acn_error *err) {
    if (out) *out = NULL;
    if (!l || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "lease and out are required");
    acn__buf b = {0};
    acn__buf_puts(&b, l->base_url);
    if (!path || *path != '/') acn__buf_putc(&b, '/');
    acn__buf_puts(&b, path);
    *out = acn__buf_take(&b);
    return *out ? ACN_OK : acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
}

/* ---- proxy ---- */

void acn_response_free(acn_response *r) {
    if (!r) return;
    for (size_t i = 0; i < r->header_count; i++) {
        free(r->headers[i].name);
        free(r->headers[i].value);
    }
    free(r->headers);
    free(r->body);
    for (size_t i = 0; i < r->tried_count; i++) free(r->tried[i].service);
    free(r->tried);
    free(r);
}

/* Parses "svc=200, other=503" into r->tried. 0 when out of memory. */
static int parse_tried(acn_response *r, const char *v) {
    if (!v || !*v) return 1;
    size_t n = 1;
    for (const char *p = v; *p; p++) n += *p == ',';
    r->tried = calloc(n, sizeof *r->tried);
    if (!r->tried) return 0;
    const char *p = v;
    while (*p) {
        const char *end = strchr(p, ',');
        if (!end) end = p + strlen(p);
        const char *a = p, *b = end;
        while (a < b && (*a == ' ' || *a == '\t')) a++;
        while (b > a && (b[-1] == ' ' || b[-1] == '\t')) b--;
        if (a < b) {
            const char *eq = b;
            while (eq > a && eq[-1] != '=') eq--;
            const char *name_end = eq > a ? eq - 1 : b;
            acn_tried *t = &r->tried[r->tried_count];
            t->service = acn__strndup(a, (size_t)(name_end - a));
            if (!t->service) return 0;
            t->status = eq > a ? atoi(eq) : 0;
            r->tried_count++;
        }
        p = *end ? end + 1 : end;
    }
    return 1;
}

const char *acn_response_header(const acn_response *r, const char *name) {
    return (r && name) ? acn__header_get(r->headers, r->header_count, name) : NULL;
}

static char *proxy_url(const acn_client *c, const acn_proxy_request *req) {
    const char *path = req->path ? req->path : "";
    acn__buf b = {0};
    acn__buf_puts(&b, c->base_url);
    acn__buf prefix = {0};
    acn__buf_puts(&prefix, "/proxy/");
    acn__buf_puts(&prefix, req->service);
    acn__buf_putc(&prefix, '/');
    /* "/proxy/<service>/..." is kept, and so is "/proxy/<service>" alone. */
    int whole = prefix.data && strncmp(path, prefix.data, prefix.len - 1) == 0 &&
                (path[prefix.len - 1] == '\0' || path[prefix.len - 1] == '?');
    if (whole || (prefix.data && strncmp(path, prefix.data, prefix.len) == 0)) {
        acn__buf_puts(&b, path);
    } else {
        while (*path == '/') path++;
        acn__buf_puts(&b, "/proxy/");
        acn__buf_encode(&b, req->service);
        acn__buf_putc(&b, '/');
        acn__buf_puts(&b, path);
    }
    if (prefix.failed) b.failed = 1;
    acn__buf_free(&prefix);
    int has_query = b.data && strchr(b.data, '?') != NULL;
    for (size_t i = 0; i < req->query_count; i++) {
        if (!req->query[i].name) continue;
        acn__buf_putc(&b, (has_query || i > 0) ? '&' : '?');
        has_query = 1;
        acn__buf_encode(&b, req->query[i].name);
        acn__buf_putc(&b, '=');
        acn__buf_encode(&b, req->query[i].value ? req->query[i].value : "");
    }
    return acn__buf_take(&b);
}

static int method_has_body(const char *m) {
    return strcmp(m, "POST") == 0 || strcmp(m, "PUT") == 0 || strcmp(m, "PATCH") == 0;
}

/* Acnologia's own refusal: an error JSON body and no X-Acnologia-Service,
 * which the proxy sets on every provider answer it relays. */
static int gateway_error(const acn__http_resp *r) {
    if (r->status < 400) return 0;
    if (acn__header_get(r->headers, r->header_count, "X-Acnologia-Service")) return 0;
    cJSON *j = r->body.len ? cJSON_ParseWithLength(r->body.data, r->body.len) : NULL;
    const cJSON *e = cJSON_GetObjectItemCaseSensitive(j, "error");
    int yes = cJSON_IsObject(e) && cJSON_IsString(cJSON_GetObjectItemCaseSensitive(e, "code"));
    cJSON_Delete(j);
    return yes;
}

acn_code acn_proxy(acn_client *c, const acn_proxy_request *req, acn_response **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !req || !req->service || !*req->service || !out) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client, request, service and out are required");
    }
    if (req->query_count && !req->query) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "query is NULL");
    if (req->header_count && !req->headers) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "headers is NULL");
    if (req->body_len && !req->body) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "body is NULL");

    char method[16];
    const char *m = req->method ? req->method : "GET";
    size_t mlen = strlen(m);
    if (!mlen || mlen >= sizeof method) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "bad method");
    for (size_t i = 0; i <= mlen; i++) {
        char ch = m[i];
        if (ch && !isalpha((unsigned char)ch)) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "bad method");
        method[i] = (char)toupper((unsigned char)ch);
    }
    if (has_crlf(req->path) || has_crlf(req->service) || has_crlf(req->idempotency_key)) {
        return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "path, service and idempotency key must not contain line breaks");
    }

    int ok = 1, has_type = 0;
    struct curl_slist *headers = NULL;
    for (size_t i = 0; i < req->header_count; i++) {
        const char *name = req->headers[i].name, *value = req->headers[i].value;
        if (!name || !*name || strchr(name, ':') || has_crlf(name) || has_crlf(value)) {
            curl_slist_free_all(headers);
            return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "bad header name or value");
        }
        /* The SDK sets these itself. */
        if (acn__strcaseeq(name, "Authorization") || acn__strcaseeq(name, "User-Agent")) continue;
        if (acn__strcaseeq(name, "Content-Type")) has_type = 1;
        if (value && *value) {
            headers = add_header(headers, &ok, name, value);
        } else {
            /* "Name;" sends the header with an empty value in libcurl. */
            acn__buf b = {0};
            acn__buf_puts(&b, name);
            acn__buf_putc(&b, ';');
            char *line = acn__buf_take(&b);
            struct curl_slist *nx = line ? curl_slist_append(headers, line) : NULL;
            free(line);
            if (nx) headers = nx;
            else ok = 0;
        }
    }
    headers = sdk_headers(c, headers, &ok);
    if (req->idempotency_key) headers = add_header(headers, &ok, "Idempotency-Key", req->idempotency_key);
    if (req->no_cache) headers = add_header(headers, &ok, "Cache-Control", "no-cache");
    int has_body = req->body != NULL || method_has_body(method);
    /* Without this libcurl would label a body as a form. */
    if (has_body && !has_type) headers = add_header(headers, &ok, "Content-Type", NULL);
    char *url = proxy_url(c, req);
    if (!ok || !url) {
        curl_slist_free_all(headers);
        free(url);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }

    acn__http_req hr = {0};
    hr.method = method;
    hr.url = url;
    hr.user_agent = c->user_agent;
    hr.headers = headers;
    hr.body = req->body;
    hr.body_len = req->body_len;
    hr.has_body = has_body;
    hr.timeout_ms = c->proxy_timeout_ms;
    hr.connect_timeout_ms = c->connect_timeout_ms;

    acn__http_resp resp = {0};
    char errbuf[512];
    int failed = acn__http_do(&hr, &resp, errbuf, sizeof errbuf);
    curl_slist_free_all(headers);
    free(url);
    if (failed) {
        acn__http_resp_free(&resp);
        return fail_transport(c, NULL, errbuf, err);
    }
    if (gateway_error(&resp)) {
        acn__redact r[3];
        size_t n = redactions(c, NULL, r);
        acn_code rc = acn__map_error(&resp, r, n, err);
        acn__http_resp_free(&resp);
        return rc;
    }

    acn_response *o = calloc(1, sizeof *o);
    if (!o) {
        acn__http_resp_free(&resp);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    o->status = (int)resp.status;
    o->headers = resp.headers;
    o->header_count = resp.header_count;
    resp.headers = NULL;
    resp.header_count = resp.header_cap = 0;
    o->body_len = resp.body.len;
    o->body = acn__buf_take(&resp.body);
    acn__http_resp_free(&resp);
    if (!o->body) {
        acn_response_free(o);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    const char *kid = acn_response_header(o, "X-Acnologia-Key-Id");
    const char *att = acn_response_header(o, "X-Acnologia-Attempts");
    const char *rep = acn_response_header(o, "Idempotent-Replayed");
    o->key_id = kid && *kid ? (int64_t)strtoll(kid, NULL, 10) : -1;
    o->attempts = att ? atoi(att) : 0;
    o->service = acn_response_header(o, "X-Acnologia-Service");
    o->cache = acn_response_header(o, "X-Acnologia-Cache");
    o->idempotent_replayed = rep && acn__strcaseeq(rep, "true");
    o->capability = acn_response_header(o, "X-Acnologia-Capability");
    if (!parse_tried(o, acn_response_header(o, "X-Acnologia-Tried"))) {
        acn_response_free(o);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    *out = o;
    return ACN_OK;
}

/* ---- services and me ---- */

static void free_service(acn_service *s) {
    free(s->slug);
    free(s->name);
    free(s->provider);
    free(s->category);
    free(s->base_url);
    free_auth(&s->auth);
    free(s->guide_url);
}

void acn_service_list_free(acn_service_list *list) {
    if (!list) return;
    for (size_t i = 0; i < list->count; i++) free_service(&list->items[i]);
    free(list->items);
    free(list);
}

acn_code acn_services(acn_client *c, acn_service_list **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client and out are required");
    cJSON *json = NULL;
    acn_code rc = api_call(c, "GET", "/api/v1/services", NULL, c->timeout_ms, NULL, &json, err);
    if (rc != ACN_OK) return rc;
    const cJSON *arr = cJSON_GetObjectItemCaseSensitive(json, "services");
    size_t n = cJSON_IsArray(arr) ? (size_t)cJSON_GetArraySize(arr) : 0;
    acn_service_list *list = calloc(1, sizeof *list);
    if (list && n) list->items = calloc(n, sizeof *list->items);
    int ok = list && (!n || list->items);
    const cJSON *it = NULL;
    if (ok) {
        cJSON_ArrayForEach(it, arr) {
            if (!cJSON_IsObject(it)) continue;
            acn_service *s = &list->items[list->count++];
            s->slug = jdup_or_empty(it, "slug", &ok);
            s->name = jdup_or_empty(it, "name", &ok);
            s->provider = jdup_or_empty(it, "provider", &ok);
            s->category = jdup_or_empty(it, "category", &ok);
            s->available = jbool(it, "available");
            s->base_url = jdup_or_empty(it, "base_url", &ok);
            parse_auth(it, &s->auth, &ok);
            s->guide_url = jdup_or_empty(it, "guide_url", &ok);
        }
    }
    cJSON_Delete(json);
    if (!ok) {
        acn_service_list_free(list);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    *out = list;
    return ACN_OK;
}

void acn_me_free(acn_me *me) {
    if (!me) return;
    free(me->app_name);
    free(me->last_used_at);
    free(me->limit_hit);
    free(me->limit_reset_at);
    for (size_t i = 0; i < me->service_count; i++) {
        free(me->services[i].slug);
        free(me->services[i].name);
        free(me->services[i].provider);
        free(me->services[i].category);
        free(me->services[i].logo_url);
    }
    free(me->services);
    if (me->share) {
        acn_me_share *s = me->share;
        free(s->id);
        free(s->name);
        free(s->owner_name);
        free(s->owner_id);
        free(s->recipient);
        free(s->label);
        free(s->status);
        free(s->mode);
        free(s->ends_at);
        free(s);
    }
    free(me->raw_json);
    free(me);
}

acn_code acn_me_get(acn_client *c, acn_me **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client and out are required");
    cJSON *json = NULL;
    acn_code rc = api_call(c, "GET", "/api/v1/me", NULL, c->timeout_ms, NULL, &json, err);
    if (rc != ACN_OK) return rc;
    acn_me *me = calloc(1, sizeof *me);
    int ok = me != NULL;
    if (ok) {
        const cJSON *app = cJSON_GetObjectItemCaseSensitive(json, "app");
        me->app_id = jint(app, "id", 0);
        me->app_name = jdup_or_empty(app, "name", &ok);

        const cJSON *lim = cJSON_GetObjectItemCaseSensitive(json, "limits");
        me->daily_requests_limit = jint(lim, "daily_requests", -1);
        me->monthly_requests_limit = jint(lim, "monthly_requests", -1);
        me->daily_budget_limit = jnum(lim, "daily_budget", -1);
        me->monthly_budget_limit = jnum(lim, "monthly_budget", -1);

        const cJSON *use = cJSON_GetObjectItemCaseSensitive(json, "usage");
        me->today_requests = jint(use, "today_requests", 0);
        me->today_cost = jnum(use, "today_cost", 0);
        me->month_requests = jint(use, "month_requests", 0);
        me->month_cost = jnum(use, "month_cost", 0);
        me->last_used_at = jdup(use, "last_used_at", &ok);

        const cJSON *hit = cJSON_GetObjectItemCaseSensitive(json, "limit_hit");
        if (cJSON_IsObject(hit)) {
            me->limit_hit = jdup(hit, "limit", &ok);
            me->limit_reset_at = jdup(hit, "reset_at", &ok);
        }

        const cJSON *arr = cJSON_GetObjectItemCaseSensitive(json, "services");
        size_t n = cJSON_IsArray(arr) ? (size_t)cJSON_GetArraySize(arr) : 0;
        if (n) {
            me->services = calloc(n, sizeof *me->services);
            if (!me->services) ok = 0;
        }
        const cJSON *it = NULL;
        if (me->services) {
            cJSON_ArrayForEach(it, arr) {
                if (!cJSON_IsObject(it)) continue;
                acn_me_service *s = &me->services[me->service_count++];
                s->slug = jdup_or_empty(it, "slug", &ok);
                s->name = jdup_or_empty(it, "name", &ok);
                s->provider = jdup_or_empty(it, "provider", &ok);
                s->category = jdup_or_empty(it, "category", &ok);
                const cJSON *av = cJSON_GetObjectItemCaseSensitive(it, "available");
                s->available = cJSON_IsBool(av) ? cJSON_IsTrue(av) : jint(it, "available", 0);
                s->logo_url = jdup(it, "logo_url", &ok);
            }
        }

        const cJSON *sh = cJSON_GetObjectItemCaseSensitive(json, "share");
        if (cJSON_IsObject(sh)) {
            acn_me_share *s = calloc(1, sizeof *s);
            me->share = s;
            if (!s) {
                ok = 0;
            } else {
                const cJSON *owner = cJSON_GetObjectItemCaseSensitive(sh, "owner");
                s->id = jdup_or_empty(sh, "id", &ok);
                s->name = jdup_or_empty(sh, "name", &ok);
                s->owner_name = jstr(sh, "owner_name") ? jdup(sh, "owner_name", &ok) : jdup_or_empty(owner, "name", &ok);
                s->owner_id = jstr(sh, "owner_id") ? jdup(sh, "owner_id", &ok) : jdup_or_empty(owner, "id", &ok);
                s->recipient = jdup_or_empty(sh, "recipient", &ok);
                s->label = jdup_or_empty(sh, "label", &ok);
                s->status = jdup_or_empty(sh, "status", &ok);
                s->mode = jdup_or_empty(sh, "mode", &ok);
                s->ends_at = jdup(sh, "ends_at", &ok);
            }
        }
        me->raw_json = cJSON_PrintUnformatted(json);
        if (!me->raw_json) ok = 0;
    }
    cJSON_Delete(json);
    if (!ok) {
        acn_me_free(me);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    *out = me;
    return ACN_OK;
}

/* ---- usage guides ---- */

static char *guide_path(const char *slug) {
    acn__buf b = {0};
    acn__buf_puts(&b, "/api/v1/services/");
    acn__buf_encode(&b, slug);
    acn__buf_puts(&b, "/guide");
    return acn__buf_take(&b);
}

/* cJSON value as raw JSON, "null" when missing. */
static char *jraw(const cJSON *obj, const char *name, int *ok) {
    const cJSON *v = cJSON_GetObjectItemCaseSensitive(obj, name);
    char *d = v ? cJSON_PrintUnformatted(v) : acn__strdup("null");
    if (!d) *ok = 0;
    return d;
}

void acn_guide_free(acn_guide *g) {
    if (!g) return;
    free(g->markdown);
    free(g->guide_json);
    free(g);
}

acn_code acn_guide_get(acn_client *c, const char *slug, acn_guide **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !slug || !*slug || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client, slug and out are required");
    char *path = guide_path(slug);
    if (!path) return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    cJSON *json = NULL;
    acn_code rc = api_call(c, "GET", path, NULL, c->timeout_ms, NULL, &json, err);
    free(path);
    if (rc != ACN_OK) return rc;
    acn_guide *g = calloc(1, sizeof *g);
    int ok = g != NULL;
    if (ok) {
        g->markdown = jdup_or_empty(json, "markdown", &ok);
        g->generated = jbool(json, "generated");
        g->guide_json = jraw(json, "guide", &ok);
    }
    cJSON_Delete(json);
    if (!ok) {
        acn_guide_free(g);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    *out = g;
    return ACN_OK;
}

acn_code acn_guide_markdown(acn_client *c, const char *slug, char **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !slug || !*slug || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client, slug and out are required");
    char *path = guide_path(slug);
    if (!path) return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    acn_code rc = text_call(c, path, "text/markdown", 1, out, err);
    free(path);
    return rc;
}

/* ---- capabilities ---- */

void acn_capability_list_free(acn_capability_list *list) {
    if (!list) return;
    for (size_t i = 0; i < list->count; i++) {
        acn_capability *k = &list->items[i];
        free(k->slug);
        free(k->name);
        free(k->description);
        free(k->strategy);
        free(k->params_json);
        for (size_t j = 0; j < k->route_count; j++) free(k->routes[j].service);
        free(k->routes);
        free(k->url);
    }
    free(list->items);
    free(list);
}

acn_code acn_capabilities(acn_client *c, acn_capability_list **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client and out are required");
    cJSON *json = NULL;
    acn_code rc = api_call(c, "GET", "/api/v1/capabilities", NULL, c->timeout_ms, NULL, &json, err);
    if (rc != ACN_OK) return rc;
    const cJSON *arr = cJSON_GetObjectItemCaseSensitive(json, "capabilities");
    size_t n = cJSON_IsArray(arr) ? (size_t)cJSON_GetArraySize(arr) : 0;
    acn_capability_list *list = calloc(1, sizeof *list);
    if (list && n) list->items = calloc(n, sizeof *list->items);
    int ok = list && (!n || list->items);
    const cJSON *it = NULL;
    if (ok) {
        cJSON_ArrayForEach(it, arr) {
            if (!cJSON_IsObject(it)) continue;
            acn_capability *k = &list->items[list->count++];
            k->slug = jdup_or_empty(it, "slug", &ok);
            k->name = jdup_or_empty(it, "name", &ok);
            k->description = jdup_or_empty(it, "description", &ok);
            k->strategy = jdup_or_empty(it, "strategy", &ok);
            k->params_json = jraw(it, "params", &ok);
            k->url = jdup_or_empty(it, "url", &ok);
            const cJSON *routes = cJSON_GetObjectItemCaseSensitive(it, "routes");
            size_t rn = cJSON_IsArray(routes) ? (size_t)cJSON_GetArraySize(routes) : 0;
            if (rn) {
                k->routes = calloc(rn, sizeof *k->routes);
                if (!k->routes) ok = 0;
            }
            const cJSON *rt = NULL;
            if (k->routes) {
                cJSON_ArrayForEach(rt, routes) {
                    if (!cJSON_IsObject(rt)) continue;
                    acn_capability_route *r = &k->routes[k->route_count++];
                    r->service = jdup_or_empty(rt, "service", &ok);
                    r->mapped = jbool(rt, "mapped");
                }
            }
        }
    }
    cJSON_Delete(json);
    if (!ok) {
        acn_capability_list_free(list);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    *out = list;
    return ACN_OK;
}

acn_code acn_capability_call(acn_client *c, const char *slug, const char *params_json,
                             const acn_capability_options *opts, acn_response **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !slug || !*slug || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client, slug and out are required");
    acn_capability_options none = {0};
    if (!opts) opts = &none;
    acn_pair query[2];
    size_t nq = 0;
    if (opts->service && *opts->service) {
        query[nq].name = "_service";
        query[nq++].value = opts->service;
    }
    if (opts->raw) {
        query[nq].name = "_raw";
        query[nq++].value = "1";
    }
    acn_pair headers[] = {{"Content-Type", "application/json"}};
    const char *body = params_json && *params_json ? params_json : "{}";
    char *path = join2("/proxy/", slug);
    if (!path) return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    acn_proxy_request req = {0};
    req.method = "POST";
    req.service = slug;
    req.path = path;
    req.query = nq ? query : NULL;
    req.query_count = nq;
    req.headers = headers;
    req.header_count = 1;
    req.body = body;
    req.body_len = strlen(body);
    req.idempotency_key = opts->idempotency_key;
    req.no_cache = opts->no_cache;
    acn_code rc = acn_proxy(c, &req, out, err);
    free(path);
    return rc;
}

/* ---- forward proxy ---- */

void acn_forward_proxy_free(acn_forward_proxy *fp) {
    if (!fp) return;
    free(fp->proxy_url);
    free(fp->proxy_url_with_token);
    free(fp->ca_url);
    free(fp->ca_fingerprint);
    for (size_t i = 0; i < fp->host_count; i++) {
        free(fp->hosts[i].host);
        free(fp->hosts[i].service);
        free(fp->hosts[i].name);
        free(fp->hosts[i].path);
    }
    free(fp->hosts);
    for (size_t i = 0; i < fp->env_count; i++) {
        free(fp->env[i].name);
        free(fp->env[i].value);
    }
    free(fp->env);
    free(fp->service_header);
    free(fp);
}

acn_code acn_forward_proxy_get(acn_client *c, acn_forward_proxy **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client and out are required");
    cJSON *json = NULL;
    acn_code rc = api_call(c, "GET", "/api/v1/proxy", NULL, c->timeout_ms, NULL, &json, err);
    if (rc != ACN_OK) return rc;
    acn_forward_proxy *fp = calloc(1, sizeof *fp);
    int ok = fp != NULL;
    if (ok) {
        fp->proxy_url = jdup_or_empty(json, "proxy_url", &ok);
        fp->proxy_url_with_token = jdup_or_empty(json, "proxy_url_with_token", &ok);
        fp->ca_url = jdup_or_empty(json, "ca_url", &ok);
        fp->ca_fingerprint = jdup_or_empty(json, "ca_fingerprint", &ok);
        fp->passthrough = jbool(json, "passthrough");
        fp->service_header = jdup_or_empty(json, "service_header", &ok);

        const cJSON *hosts = cJSON_GetObjectItemCaseSensitive(json, "hosts");
        size_t n = cJSON_IsArray(hosts) ? (size_t)cJSON_GetArraySize(hosts) : 0;
        if (n) {
            fp->hosts = calloc(n, sizeof *fp->hosts);
            if (!fp->hosts) ok = 0;
        }
        const cJSON *it = NULL;
        if (fp->hosts) {
            cJSON_ArrayForEach(it, hosts) {
                if (!cJSON_IsObject(it)) continue;
                acn_forward_host *h = &fp->hosts[fp->host_count++];
                h->host = jdup_or_empty(it, "host", &ok);
                h->service = jdup_or_empty(it, "service", &ok);
                h->name = jdup_or_empty(it, "name", &ok);
                h->path = jdup_or_empty(it, "path", &ok);
            }
        }

        const cJSON *env = cJSON_GetObjectItemCaseSensitive(json, "env");
        size_t en = cJSON_IsObject(env) ? (size_t)cJSON_GetArraySize(env) : 0;
        if (en) {
            fp->env = calloc(en, sizeof *fp->env);
            if (!fp->env) ok = 0;
        }
        if (fp->env) {
            cJSON_ArrayForEach(it, env) {
                if (!cJSON_IsString(it) || !it->string) continue;
                acn_header *h = &fp->env[fp->env_count++];
                h->name = acn__strdup(it->string);
                h->value = acn__strdup(it->valuestring);
                if (!h->name || !h->value) ok = 0;
            }
        }
    }
    cJSON_Delete(json);
    if (!ok) {
        acn_forward_proxy_free(fp);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    *out = fp;
    return ACN_OK;
}

acn_code acn_forward_proxy_ca(acn_client *c, char **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client and out are required");
    return text_call(c, "/api/v1/proxy/ca.pem", "application/x-pem-file", 0, out, err);
}

acn_code acn_forward_proxy_save_ca(acn_client *c, const char *path, acn_error *err) {
    if (!c) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client is required");
    if (!path || !*path) path = "acnologia-ca.pem";
    char *pem = NULL;
    acn_code rc = acn_forward_proxy_ca(c, &pem, err);
    if (rc != ACN_OK) return rc;
    FILE *f = fopen(path, "wb");
    size_t len = strlen(pem);
    int written = f && fwrite(pem, 1, len, f) == len;
    if (f && fclose(f) != 0) written = 0;
    free(pem);
    if (!written) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "could not write the CA file");
    return ACN_OK;
}

/* proxy_url with app:<token>@ put in, any user info it had replaced. */
static char *url_with_token(const acn_client *c, const char *proxy_url) {
    const char *sep = strstr(proxy_url, "://");
    if (!sep) return NULL;
    const char *host = sep + 3;
    const char *end = host + strcspn(host, "/?#");
    const char *at = NULL;
    for (const char *p = host; p < end; p++) {
        if (*p == '@') at = p;
    }
    if (at) host = at + 1;
    acn__buf b = {0};
    acn__buf_append(&b, proxy_url, (size_t)(sep + 3 - proxy_url));
    acn__buf_puts(&b, "app:");
    acn__buf_encode(&b, c->token);
    acn__buf_putc(&b, '@');
    acn__buf_puts(&b, host);
    return acn__buf_take(&b);
}

acn_code acn_forward_proxy_url(acn_client *c, char **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client and out are required");
    acn_forward_proxy *fp = NULL;
    acn_code rc = acn_forward_proxy_get(c, &fp, err);
    if (rc != ACN_OK) return rc;
    if (!fp->proxy_url || !strstr(fp->proxy_url, "://")) {
        acn_forward_proxy_free(fp);
        rc = acn__fail(err, ACN_ERR_BAD_RESPONSE, "the proxy answer has no proxy_url");
        if (err) err->status = 200;
        return rc;
    }
    *out = url_with_token(c, fp->proxy_url);
    acn_forward_proxy_free(fp);
    return *out ? ACN_OK : acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
}

void acn_env_free(acn_env *e) {
    if (!e) return;
    for (size_t i = 0; i < e->count; i++) {
        acn__wipe(e->vars[i].value);
        free(e->vars[i].name);
        free(e->vars[i].value);
    }
    free(e->vars);
    free(e);
}

const char *acn_env_get(const acn_env *e, const char *name) {
    if (!e || !name) return NULL;
    for (size_t i = 0; i < e->count; i++) {
        if (strcmp(e->vars[i].name, name) == 0) return e->vars[i].value;
    }
    return NULL;
}

acn_code acn_forward_proxy_env(acn_client *c, const char *ca_path, acn_env **out, acn_error *err) {
    if (out) *out = NULL;
    if (!c || !out) return acn__fail(err, ACN_ERR_INVALID_ARGUMENT, "client and out are required");
    if (!ca_path || !*ca_path) ca_path = "acnologia-ca.pem";
    char *url = NULL;
    acn_code rc = acn_forward_proxy_url(c, &url, err);
    if (rc != ACN_OK) return rc;
    const char *names[] = {"HTTPS_PROXY", "HTTP_PROXY", "NODE_USE_ENV_PROXY",
                           "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "SSL_CERT_FILE"};
    const char *values[] = {url, url, "1", ca_path, ca_path, ca_path};
    size_t n = sizeof names / sizeof names[0];
    acn_env *e = calloc(1, sizeof *e);
    int ok = e && (e->vars = calloc(n, sizeof *e->vars)) != NULL;
    for (size_t i = 0; ok && i < n; i++) {
        e->vars[i].name = acn__strdup(names[i]);
        e->vars[i].value = acn__strdup(values[i]);
        e->count++;
        if (!e->vars[i].name || !e->vars[i].value) ok = 0;
    }
    acn__wipe(url);
    free(url);
    if (!ok) {
        acn_env_free(e);
        return acn__fail(err, ACN_ERR_NO_MEMORY, NULL);
    }
    *out = e;
    return ACN_OK;
}
