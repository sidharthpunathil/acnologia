/* Offline tests: the SDK against the fake server in fake_server.c. */
#if !defined(_WIN32) && !defined(_POSIX_C_SOURCE)
#define _POSIX_C_SOURCE 200809L
#endif

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "acnologia/acnologia.h"
#include "fake_server.h"

#define TOKEN "acn_test_token+/="
#define TOKEN_ENC "acn_test_token%2B%2F%3D"

static int failures = 0;
static int checks = 0;

#define CHECK(cond)                                                              \
    do {                                                                         \
        checks++;                                                                \
        if (!(cond)) {                                                           \
            failures++;                                                          \
            fprintf(stderr, "%s:%d: check failed: %s\n", __FILE__, __LINE__, #cond); \
        }                                                                        \
    } while (0)

#define CHECK_STR(a, b)                                                                   \
    do {                                                                                  \
        const char *a_ = (a), *b_ = (b);                                                  \
        checks++;                                                                         \
        if (!a_ || !b_ || strcmp(a_, b_) != 0) {                                          \
            failures++;                                                                   \
            fprintf(stderr, "%s:%d: %s is \"%s\", want \"%s\"\n", __FILE__, __LINE__, #a, \
                    a_ ? a_ : "(null)", b_ ? b_ : "(null)");                              \
        }                                                                                 \
    } while (0)

#define CHECK_CODE(got, want)                                                                \
    do {                                                                                     \
        acn_code g_ = (got), w_ = (want);                                                    \
        checks++;                                                                            \
        if (g_ != w_) {                                                                      \
            failures++;                                                                      \
            fprintf(stderr, "%s:%d: code %d (%s), want %d (%s)\n", __FILE__, __LINE__, (int)g_, \
                    acn_strerror(g_), (int)w_, acn_strerror(w_));                            \
        }                                                                                    \
    } while (0)

static fake_server *srv;

static void set_env(const char *name, const char *value) {
#ifdef _WIN32
    _putenv_s(name, value ? value : "");
#else
    if (value) setenv(name, value, 1);
    else unsetenv(name);
#endif
}

static void clear_env(void) {
    set_env("ACNOLOGIA_URL", NULL);
    set_env("ACNOLOGIA_TOKEN", NULL);
    set_env("ACNOLOGIA_ACCESS_PASSWORD", NULL);
    set_env("ACNOLOGIA_ACCESS_URL", NULL);
}

static acn_client *new_client(void) {
    acn_client_options o;
    acn_client_options_init(&o);
    o.base_url = fake_url(srv);
    o.token = TOKEN;
    o.access_password = "";
    acn_client *c = NULL;
    acn_error err;
    if (acn_client_new(&o, &c, &err) != ACN_OK) {
        fprintf(stderr, "client: %s\n", err.message);
        abort();
    }
    return c;
}

/* ---- canned answers ---- */

typedef struct canned {
    int status;
    const char *body;
    const char *h1, *v1, *h2, *v2;
} canned;

static void canned_handler(const fake_req *req, fake_resp *resp, void *user) {
    (void)req;
    const canned *c = user;
    fake_json(resp, c->status, c->body);
    if (c->h1) fake_add_header(resp, c->h1, c->v1);
    if (c->h2) fake_add_header(resp, c->h2, c->v2);
}

/* ---- a small consumer API ---- */

typedef struct pool {
    int keys[8];
    int nkeys;
    int bad[8];
    int nbad;
    int counter;
    int no_next;
    const char *auth;
} pool;

static int is_bad(const pool *p, int k) {
    for (int i = 0; i < p->nbad; i++) {
        if (p->bad[i] == k) return 1;
    }
    return 0;
}

static int excluded(const char *body, int k) {
    const char *ids = strstr(body, "\"exclude_key_ids\":[");
    if (!ids) return 0;
    const char *end = strchr(ids, ']');
    for (const char *p = ids + 19; p < end;) {
        int n = atoi(p);
        if (n == k) return 1;
        const char *comma = strchr(p, ',');
        if (!comma || comma > end) break;
        p = comma + 1;
    }
    return 0;
}

static int lease_json(pool *p, char *out, size_t size, const char *body, int except) {
    for (int i = 0; i < p->nkeys; i++) {
        int k = p->keys[i];
        if (k == except || is_bad(p, k) || (body && excluded(body, k))) continue;
        p->counter++;
        snprintf(out, size,
                 "{\"lease_id\":\"l/%d\",\"key_id\":%d,\"service\":\"agi-labs\",\"secret\":\"sk-%d+/\","
                 "\"auth\":%s,\"base_url\":\"https://api.agi-labs.test/\",\"expires_at\":\"2026-11-01T12:05:00Z\","
                 "\"exclusive\":%s}",
                 p->counter, k, k, p->auth ? p->auth : "{\"type\":\"query\",\"name\":\"api_key\"}",
                 (body && strstr(body, "\"exclusive\":true")) ? "true" : "false");
        return k;
    }
    return 0;
}

/* The key of the lease handed out last: the tests report leases in order. */
static int last_key = 0;

static void pool_handler(const fake_req *req, fake_resp *resp, void *user) {
    pool *p = user;
    char buf[4096];
    if (strcmp(req->path, "/api/v1/lease") == 0) {
        int k = lease_json(p, buf, sizeof buf, req->body, -1);
        if (!k) {
            fake_json(resp, 503, "{\"error\":{\"code\":\"no_key_available\",\"message\":\"pool drained\"},"
                                 "\"retry_after_sec\":42}");
            return;
        }
        last_key = k;
        fake_json(resp, 200, buf);
        return;
    }
    if (strncmp(req->path, "/api/v1/leases/", 15) == 0 && strstr(req->path, "/report")) {
        int key = last_key;
        if (strstr(req->body, "\"rate_limited\"") || strstr(req->body, "\"quota_exhausted\"") ||
            strstr(req->body, "\"invalid\"")) {
            p->bad[p->nbad++] = key;
        }
        if (strstr(req->body, "\"rotate\":true") && !p->no_next) {
            char lease[2048];
            int k = lease_json(p, lease, sizeof lease, NULL, key);
            if (k) {
                last_key = k;
                snprintf(buf, sizeof buf, "{\"ok\":true,\"next\":%s}", lease);
                fake_json(resp, 200, buf);
                return;
            }
        }
        fake_json(resp, 200, "{\"ok\":true,\"next\":null}");
        return;
    }
    if (strncmp(req->path, "/api/v1/leases/", 15) == 0 && strstr(req->path, "/renew")) {
        if (strstr(req->path, "gone")) {
            fake_json(resp, 404, "{\"error\":{\"code\":\"lease_not_found\",\"message\":\"lease not found\"}}");
            return;
        }
        fake_json(resp, 200, "{\"ok\":true,\"expires_at\":\"2026-11-01T13:00:00Z\"}");
        return;
    }
    fake_json(resp, 404, "{\"error\":{\"code\":\"not_found\",\"message\":\"nope\"}}");
}

static void init_pool(pool *p) {
    memset(p, 0, sizeof *p);
    p->keys[0] = 1;
    p->keys[1] = 2;
    p->keys[2] = 3;
    p->nkeys = 3;
    last_key = 0;
}

/* ---- tests ---- */

static void test_env_defaults(void) {
    clear_env();
    acn_client *c = NULL;
    acn_error err;
    CHECK_CODE(acn_client_new(NULL, &c, &err), ACN_ERR_INVALID_ARGUMENT);
    CHECK(c == NULL);
    CHECK(strstr(err.message, "ACNOLOGIA_URL") != NULL);

    set_env("ACNOLOGIA_URL", fake_url(srv));
    CHECK_CODE(acn_client_new(NULL, &c, &err), ACN_ERR_INVALID_ARGUMENT);
    CHECK(strstr(err.message, "ACNOLOGIA_TOKEN") != NULL);

    char url[128];
    snprintf(url, sizeof url, "%s//", fake_url(srv));
    set_env("ACNOLOGIA_URL", url);
    set_env("ACNOLOGIA_TOKEN", "acn_from_env");
    CHECK_CODE(acn_client_new(NULL, &c, &err), ACN_OK);
    CHECK_STR(acn_client_base_url(c), fake_url(srv));

    canned ok = {200, "{\"services\":[]}", NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &ok);
    fake_reset(srv);
    acn_service_list *list = NULL;
    CHECK_CODE(acn_services(c, &list, &err), ACN_OK);
    CHECK(list && list->count == 0);
    acn_service_list_free(list);
    const fake_req *r = fake_last(srv);
    CHECK_STR(fake_header(r, "Authorization"), "Bearer acn_from_env");
    CHECK_STR(fake_header(r, "X-Acnologia-Client"), "acnologia-c/" ACN_VERSION);
    CHECK_STR(fake_header(r, "User-Agent"), "acnologia-c/" ACN_VERSION);
    CHECK_STR(fake_header(r, "Accept"), "application/json");
    CHECK(fake_header(r, "X-Acnologia-Access") == NULL);
    acn_client_free(c);

    /* Options win over the environment. */
    acn_client_options o;
    acn_client_options_init(&o);
    o.token = "acn_explicit";
    o.user_agent = "my-app/2";
    CHECK_CODE(acn_client_new(&o, &c, &err), ACN_OK);
    fake_reset(srv);
    CHECK_CODE(acn_services(c, &list, &err), ACN_OK);
    acn_service_list_free(list);
    CHECK_STR(fake_header(fake_last(srv), "Authorization"), "Bearer acn_explicit");
    CHECK_STR(fake_header(fake_last(srv), "User-Agent"), "my-app/2");
    acn_client_free(c);

    o.base_url = "ftp://example.test";
    CHECK_CODE(acn_client_new(&o, &c, &err), ACN_ERR_INVALID_ARGUMENT);
    clear_env();
}

static const char *access_sent(const acn_client_options *o) {
    static char seen[128];
    acn_client *c = NULL;
    acn_error err;
    if (acn_client_new(o, &c, &err) != ACN_OK) return "(client failed)";
    canned ok = {200, "{\"services\":[]}", NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &ok);
    fake_reset(srv);
    acn_service_list *list = NULL;
    acn_services(c, &list, &err);
    acn_service_list_free(list);
    acn_client_free(c);
    const char *h = fake_header(fake_last(srv), "X-Acnologia-Access");
    if (!h) return NULL;
    snprintf(seen, sizeof seen, "%s", h);
    return seen;
}

static void test_access_password(void) {
    clear_env();
    acn_client_options o;
    acn_client_options_init(&o);
    o.base_url = fake_url(srv);
    o.token = TOKEN;

    CHECK(access_sent(&o) == NULL);

    o.access_password = "open sesame";
    CHECK_STR(access_sent(&o), "open sesame");

    /* From the environment, only to the server it belongs to. */
    o.access_password = NULL;
    set_env("ACNOLOGIA_ACCESS_PASSWORD", "from-env");
    CHECK(access_sent(&o) == NULL);
    set_env("ACNOLOGIA_URL", fake_url(srv));
    CHECK_STR(access_sent(&o), "from-env");
    set_env("ACNOLOGIA_ACCESS_URL", "http://other.test:9");
    CHECK(access_sent(&o) == NULL);
    char upper[128];
    snprintf(upper, sizeof upper, "HTTP://127.0.0.1:%d/", fake_port(srv));
    set_env("ACNOLOGIA_ACCESS_URL", upper);
    CHECK_STR(access_sent(&o), "from-env");

    /* "" turns it off, environment or not. */
    o.access_password = "";
    CHECK(access_sent(&o) == NULL);
    clear_env();
}

static void test_me(void) {
    acn_client *c = new_client();
    acn_error err;
    canned full = {200,
                   "{\"app\":{\"id\":7,\"name\":\"Website\"},"
                   "\"limits\":{\"daily_requests\":500,\"monthly_requests\":null,\"daily_budget\":null,\"monthly_budget\":20.5},"
                   "\"usage\":{\"today_requests\":12,\"today_cost\":0.25,\"month_requests\":300,\"month_cost\":4.5,"
                   "\"last_used_at\":\"2026-11-01T10:00:00Z\",\"limit_hit\":{\"limit\":\"daily_requests\",\"reset_at\":\"2026-11-02T00:00:00Z\"}},"
                   "\"limit_hit\":{\"limit\":\"daily_requests\",\"reset_at\":\"2026-11-02T00:00:00Z\"},"
                   "\"services\":[{\"slug\":\"agi-labs\",\"name\":\"AGI Labs\",\"provider\":\"generic\",\"category\":\"LLM\",\"available\":1,\"logo_url\":null}],"
                   "\"share\":{\"id\":\"sh_1\",\"name\":\"Team\",\"owner\":{\"name\":\"Ops\",\"id\":\"srv_9\"},\"owner_name\":\"Ops\","
                   "\"owner_id\":\"srv_9\",\"recipient\":\"lab\",\"label\":\"q4\",\"status\":\"paused\",\"mode\":\"proxy\",\"ends_at\":null}}",
                   NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &full);
    fake_reset(srv);
    acn_me *me = NULL;
    CHECK_CODE(acn_me_get(c, &me, &err), ACN_OK);
    CHECK_STR(fake_last(srv)->method, "GET");
    CHECK_STR(fake_last(srv)->path, "/api/v1/me");
    if (me) {
        CHECK(me->app_id == 7);
        CHECK_STR(me->app_name, "Website");
        CHECK(me->daily_requests_limit == 500);
        CHECK(me->monthly_requests_limit == -1);
        CHECK(me->daily_budget_limit == -1);
        CHECK(me->monthly_budget_limit == 20.5);
        CHECK(me->today_requests == 12 && me->month_requests == 300);
        CHECK(me->today_cost == 0.25 && me->month_cost == 4.5);
        CHECK_STR(me->last_used_at, "2026-11-01T10:00:00Z");
        CHECK_STR(me->limit_hit, "daily_requests");
        CHECK_STR(me->limit_reset_at, "2026-11-02T00:00:00Z");
        CHECK(me->service_count == 1);
        if (me->service_count == 1) {
            CHECK_STR(me->services[0].slug, "agi-labs");
            CHECK(me->services[0].available == 1);
        }
        CHECK(me->share != NULL);
        if (me->share) {
            CHECK_STR(me->share->status, "paused");
            CHECK_STR(me->share->owner_name, "Ops");
            CHECK_STR(me->share->owner_id, "srv_9");
            CHECK(me->share->ends_at == NULL);
        }
        CHECK(me->raw_json && strstr(me->raw_json, "\"recipient\":\"lab\"") != NULL);
    }
    acn_me_free(me);

    canned plain = {200,
                    "{\"app\":{\"id\":3,\"name\":\"Batch\"},\"limits\":{\"daily_requests\":null,\"monthly_requests\":null,"
                    "\"daily_budget\":null,\"monthly_budget\":null},\"usage\":{\"today_requests\":0,\"today_cost\":0,"
                    "\"month_requests\":0,\"month_cost\":0,\"last_used_at\":null,\"limit_hit\":null},\"limit_hit\":null,"
                    "\"services\":[],\"share\":null}",
                    NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &plain);
    CHECK_CODE(acn_me_get(c, &me, &err), ACN_OK);
    if (me) {
        CHECK(me->share == NULL);
        CHECK(me->limit_hit == NULL && me->limit_reset_at == NULL);
        CHECK(me->last_used_at == NULL);
        CHECK(me->daily_requests_limit == -1 && me->monthly_budget_limit == -1);
        CHECK(me->service_count == 0 && me->services == NULL);
    }
    acn_me_free(me);

    canned unauth = {401, "{\"error\":{\"code\":\"unauthenticated\",\"message\":\"bad token\"}}", NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &unauth);
    me = NULL;
    CHECK_CODE(acn_me_get(c, &me, &err), ACN_ERR_UNAUTHENTICATED);
    CHECK(me == NULL);
    CHECK(err.status == 401);
    CHECK_STR(err.error_code, "unauthenticated");

    canned junk = {200, "not json", NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &junk);
    CHECK_CODE(acn_me_get(c, &me, &err), ACN_ERR_BAD_RESPONSE);
    acn_client_free(c);
}

static void test_services(void) {
    acn_client *c = new_client();
    acn_error err;
    canned ok = {200,
                 "{\"services\":[{\"slug\":\"agi-labs\",\"name\":\"AGI Labs\",\"provider\":\"generic\",\"category\":\"LLM\","
                 "\"available\":true,\"base_url\":\"https://api.agi-labs.test\",\"auth\":{\"type\":\"bearer\",\"name\":\"\",\"prefix\":\"Key \"}}]}",
                 NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &ok);
    acn_service_list *list = NULL;
    CHECK_CODE(acn_services(c, &list, &err), ACN_OK);
    CHECK(list && list->count == 1);
    if (list && list->count == 1) {
        CHECK_STR(list->items[0].slug, "agi-labs");
        CHECK(list->items[0].available == 1);
        CHECK_STR(list->items[0].auth.type, "bearer");
        CHECK_STR(list->items[0].auth.prefix, "Key ");
    }
    acn_service_list_free(list);
    acn_client_free(c);
}

static void test_lease_body_and_fields(void) {
    acn_client *c = new_client();
    acn_error err;
    pool p;
    init_pool(&p);
    fake_set_handler(srv, pool_handler, &p);
    fake_reset(srv);

    acn_lease *l = NULL;
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_OK);
    CHECK_STR(fake_last(srv)->body, "{\"service\":\"agi-labs\"}");
    CHECK_STR(fake_header(fake_last(srv), "Content-Type"), "application/json");
    if (l) {
        CHECK_STR(l->lease_id, "l/1");
        CHECK(l->key_id == 1);
        CHECK_STR(l->secret, "sk-1+/");
        CHECK_STR(l->service, "agi-labs");
        CHECK_STR(l->auth.type, "query");
        CHECK_STR(l->auth.name, "api_key");
        CHECK_STR(l->auth.prefix, "");
        CHECK_STR(l->base_url, "https://api.agi-labs.test");
        CHECK_STR(l->expires_at, "2026-11-01T12:05:00Z");
        CHECK(l->exclusive == 0 && l->reported == 0);
    }
    acn_lease_free(l);

    int64_t ex[] = {1, 2};
    acn_lease_options o = {0};
    o.exclude_key_ids = ex;
    o.exclude_count = 2;
    o.ttl_sec = 60;
    o.wait_sec = 10;
    o.exclusive = 1;
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", &o, &l, &err), ACN_OK);
    CHECK_STR(fake_last(srv)->body,
              "{\"service\":\"agi-labs\",\"exclude_key_ids\":[1,2],\"ttl_sec\":60,\"wait_sec\":10,\"exclusive\":true}");
    CHECK(l && l->key_id == 3 && l->exclusive == 1);
    acn_lease_free(l);

    o.wait_sec = 61;
    size_t before = fake_count(srv);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", &o, &l, &err), ACN_ERR_INVALID_ARGUMENT);
    CHECK(fake_count(srv) == before);
    o.wait_sec = -1;
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", &o, &l, &err), ACN_ERR_INVALID_ARGUMENT);
    CHECK_CODE(acn_lease_acquire(c, "", NULL, &l, &err), ACN_ERR_INVALID_ARGUMENT);

    p.nkeys = 0;
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_NO_KEY_AVAILABLE);
    CHECK(l == NULL);
    CHECK(err.status == 503 && err.retry_after_sec == 42);
    CHECK(acn_is_no_key(err.code));
    acn_client_free(c);
}

static void test_renew(void) {
    acn_client *c = new_client();
    acn_error err;
    pool p;
    init_pool(&p);
    fake_set_handler(srv, pool_handler, &p);
    acn_lease *l = NULL;
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_OK);
    if (!l) {
        acn_client_free(c);
        return;
    }
    CHECK_CODE(acn_lease_renew(c, l, 30, &err), ACN_OK);
    CHECK_STR(fake_last(srv)->path, "/api/v1/leases/l%2F1/renew");
    CHECK_STR(fake_last(srv)->body, "{\"ttl_sec\":30}");
    CHECK_STR(l->expires_at, "2026-11-01T13:00:00Z");
    CHECK_CODE(acn_lease_renew(c, l, 0, &err), ACN_OK);
    CHECK_STR(fake_last(srv)->body, "{}");

    free(l->lease_id);
    l->lease_id = malloc(5);
    memcpy(l->lease_id, "gone", 5);
    CHECK_CODE(acn_lease_renew(c, l, 0, &err), ACN_ERR_LEASE_NOT_FOUND);
    CHECK(err.status == 404);
    CHECK_STR(err.error_code, "lease_not_found");
    CHECK_STR(l->expires_at, "2026-11-01T13:00:00Z");
    acn_lease_free(l);
    acn_client_free(c);
}

static void test_report(void) {
    acn_client *c = new_client();
    acn_error err;
    pool p;
    init_pool(&p);
    fake_set_handler(srv, pool_handler, &p);
    acn_lease *l = NULL, *next = NULL;
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_OK);
    if (!l) {
        acn_client_free(c);
        return;
    }
    acn_report r = {0};
    r.outcome = ACN_OUTCOME_RATE_LIMITED;
    r.status_code = 429;
    r.retry_after_sec = 2;
    r.message = "429 for https://api.agi-labs.test/v1/chat?api_key=sk-1%2B%2F and sk-1+/";
    r.rotate = 1;
    CHECK_CODE(acn_lease_report(c, l, &r, &next, &err), ACN_OK);
    const fake_req *q = fake_last(srv);
    CHECK_STR(q->path, "/api/v1/leases/l%2F1/report");
    CHECK_STR(q->body,
              "{\"outcome\":\"rate_limited\",\"rotate\":true,\"status_code\":429,\"retry_after_sec\":2,"
              "\"message\":\"429 for https://api.agi-labs.test/v1/chat?api_key=[key] and [key]\"}");
    CHECK(strstr(q->body, "sk-1") == NULL);
    CHECK(l->reported == 1);
    CHECK(next != NULL && next->key_id == 2);
    acn_lease_free(l);

    /* Without rotate, no next lease. The message is cut to 500 bytes. */
    char big[700];
    memset(big, 'x', sizeof big - 1);
    big[sizeof big - 1] = '\0';
    acn_report ok = {0};
    ok.message = big;
    acn_lease sentinel;
    acn_lease *none = &sentinel; /* must be reset to NULL */
    CHECK_CODE(acn_lease_report(c, next, &ok, &none, &err), ACN_OK);
    CHECK(none == NULL);
    q = fake_last(srv);
    const char *m = strstr(q->body, "\"message\":\"");
    CHECK(m != NULL);
    if (m) {
        const char *end = strchr(m + 11, '"');
        CHECK(end && end - (m + 11) == 500);
    }
    CHECK(strstr(q->body, "\"outcome\":\"ok\",\"rotate\":false") != NULL);

    /* Reporting twice: the server answers 409 report_failed. */
    canned dup = {409, "{\"error\":{\"code\":\"report_failed\",\"message\":\"lease already reported\"}}", NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &dup);
    CHECK_CODE(acn_lease_report(c, next, &ok, NULL, &err), ACN_ERR_REPORT_FAILED);
    CHECK(err.status == 409);

    ok.outcome = (acn_outcome)99;
    CHECK_CODE(acn_lease_report(c, next, &ok, NULL, &err), ACN_ERR_INVALID_ARGUMENT);
    acn_lease_free(next);
    acn_client_free(c);
}

typedef struct seen_keys {
    int64_t keys[16];
    int n;
    acn_outcome fail_with;
    int64_t succeed_on;
} seen_keys;

static void with_key_fn(const acn_lease *lease, acn_report *report, void *user) {
    seen_keys *s = user;
    s->keys[s->n++] = lease->key_id;
    if (lease->key_id != s->succeed_on) {
        report->outcome = s->fail_with;
        report->status_code = 429;
        report->retry_after_sec = 5;
        report->message = lease->secret; /* must be scrubbed */
    }
}

static void test_with_key(void) {
    acn_client *c = new_client();
    acn_error err;
    pool p;
    init_pool(&p);
    fake_set_handler(srv, pool_handler, &p);
    fake_reset(srv);

    seen_keys s = {{0}, 0, ACN_OUTCOME_RATE_LIMITED, 3};
    CHECK_CODE(acn_with_key(c, "agi-labs", NULL, 0, with_key_fn, &s, &err), ACN_OK);
    CHECK(s.n == 3 && s.keys[0] == 1 && s.keys[1] == 2 && s.keys[2] == 3);
    int reports = 0;
    for (size_t i = 0; i < fake_count(srv); i++) {
        const fake_req *q = fake_at(srv, i);
        if (strstr(q->path, "/report")) {
            reports++;
            CHECK(strstr(q->body, "sk-") == NULL);
        }
    }
    CHECK(reports == 3);
    CHECK(strstr(fake_last(srv)->body, "\"outcome\":\"ok\",\"rotate\":false") != NULL);

    /* max_attempts: the last rejected attempt is reported without rotate. */
    init_pool(&p);
    fake_reset(srv);
    seen_keys t = {{0}, 0, ACN_OUTCOME_INVALID, 99};
    CHECK_CODE(acn_with_key(c, "agi-labs", NULL, 2, with_key_fn, &t, &err), ACN_ERR_ATTEMPTS_EXHAUSTED);
    CHECK(t.n == 2);
    CHECK(strstr(fake_last(srv)->body, "\"outcome\":\"invalid\",\"rotate\":false") != NULL);

    /* Pool drained: a fresh lease excludes every key tried, then no key. */
    init_pool(&p);
    p.no_next = 1;
    fake_reset(srv);
    seen_keys u = {{0}, 0, ACN_OUTCOME_QUOTA_EXHAUSTED, 99};
    acn_lease_options o = {0};
    o.wait_sec = 3;
    CHECK_CODE(acn_with_key(c, "agi-labs", &o, 10, with_key_fn, &u, &err), ACN_ERR_NO_KEY_AVAILABLE);
    CHECK(u.n == 3);
    CHECK(err.retry_after_sec == 42);
    CHECK(strstr(fake_last(srv)->body, "\"exclude_key_ids\":[1,2,3]") != NULL);
    CHECK(strstr(fake_last(srv)->body, "\"wait_sec\":3") != NULL);
    acn_client_free(c);
}

static void echo_handler(const fake_req *req, fake_resp *resp, void *user) {
    (void)user;
    if (strstr(req->path, "/redirect")) {
        resp->status = 302;
        fake_add_header(resp, "Location", "http://127.0.0.1:1/steal");
        fake_add_header(resp, "X-Acnologia-Service", "agi-labs");
        return;
    }
    if (strstr(req->path, "/upstream-error")) {
        fake_json(resp, 429, "{\"error\":{\"code\":\"rate_limit\",\"message\":\"slow down\"}}");
        fake_add_header(resp, "X-Acnologia-Service", "agi-labs");
        fake_add_header(resp, "X-Acnologia-Key-Id", "4");
        return;
    }
    fake_json(resp, 201, "{\"ok\":true}");
    fake_add_header(resp, "X-Acnologia-Key-Id", "12");
    fake_add_header(resp, "X-Acnologia-Attempts", "2");
    fake_add_header(resp, "X-Acnologia-Service", "agi-labs");
    if (fake_header(req, "Idempotency-Key")) {
        fake_add_header(resp, "X-Acnologia-Cache", "replay");
        fake_add_header(resp, "Idempotent-Replayed", "true");
    }
}

static void test_proxy(void) {
    acn_client *c = new_client();
    acn_error err;
    fake_set_handler(srv, echo_handler, NULL);
    fake_reset(srv);

    acn_pair query[] = {{"q", "hello world"}, {"lang", "en&fr"}};
    acn_pair headers[] = {{"Content-Type", "application/json"}, {"X-Trace", "abc"}, {"Authorization", "Bearer nope"}};
    const char *body = "{\"prompt\":\"hi\"}";
    acn_proxy_request req = {0};
    req.method = "post";
    req.service = "agi-labs";
    req.path = "/v1/chat";
    req.query = query;
    req.query_count = 2;
    req.headers = headers;
    req.header_count = 3;
    req.body = body;
    req.body_len = strlen(body);
    acn_response *resp = NULL;
    CHECK_CODE(acn_proxy(c, &req, &resp, &err), ACN_OK);
    const fake_req *q = fake_last(srv);
    CHECK_STR(q->method, "POST");
    CHECK_STR(q->target, "/proxy/agi-labs/v1/chat?q=hello%20world&lang=en%26fr");
    CHECK_STR(q->body, body);
    CHECK_STR(fake_header(q, "Content-Type"), "application/json");
    CHECK_STR(fake_header(q, "X-Trace"), "abc");
    CHECK_STR(fake_header(q, "Authorization"), "Bearer " TOKEN);
    CHECK(fake_header(q, "Idempotency-Key") == NULL);
    if (resp) {
        CHECK(resp->status == 201);
        CHECK_STR(resp->body, "{\"ok\":true}");
        CHECK(resp->body_len == 11);
        CHECK(resp->key_id == 12);
        CHECK(resp->attempts == 2);
        CHECK_STR(resp->service, "agi-labs");
        CHECK(resp->cache == NULL);
        CHECK(resp->idempotent_replayed == 0);
        CHECK_STR(acn_response_header(resp, "content-type"), "application/json");
    }
    acn_response_free(resp);

    /* Idempotency-Key, a path with its own query, a body without a type. */
    req.method = "PUT";
    req.path = "v1/items?id=7";
    req.query = NULL;
    req.query_count = 0;
    req.headers = NULL;
    req.header_count = 0;
    req.body = "raw";
    req.body_len = 3;
    req.idempotency_key = "order-42";
    CHECK_CODE(acn_proxy(c, &req, &resp, &err), ACN_OK);
    q = fake_last(srv);
    CHECK_STR(q->method, "PUT");
    CHECK_STR(q->target, "/proxy/agi-labs/v1/items?id=7");
    CHECK_STR(fake_header(q, "Idempotency-Key"), "order-42");
    CHECK(fake_header(q, "Content-Type") == NULL);
    CHECK(resp && resp->idempotent_replayed == 1);
    CHECK(resp && resp->cache && strcmp(resp->cache, "replay") == 0);
    acn_response_free(resp);

    /* GET, DELETE, HEAD; a path that already names the proxy route. */
    acn_proxy_request get = {0};
    get.service = "agi-labs";
    get.path = "/proxy/agi-labs/v1/models";
    CHECK_CODE(acn_proxy(c, &get, &resp, &err), ACN_OK);
    CHECK_STR(fake_last(srv)->method, "GET");
    CHECK_STR(fake_last(srv)->target, "/proxy/agi-labs/v1/models");
    acn_response_free(resp);
    get.method = "DELETE";
    CHECK_CODE(acn_proxy(c, &get, &resp, &err), ACN_OK);
    CHECK_STR(fake_last(srv)->method, "DELETE");
    acn_response_free(resp);
    get.method = "HEAD";
    CHECK_CODE(acn_proxy(c, &get, &resp, &err), ACN_OK);
    CHECK_STR(fake_last(srv)->method, "HEAD");
    CHECK(resp && resp->body_len == 0);
    acn_response_free(resp);

    /* A provider error comes back as a response, not an error. */
    get.method = NULL;
    get.path = "/upstream-error";
    CHECK_CODE(acn_proxy(c, &get, &resp, &err), ACN_OK);
    CHECK(resp && resp->status == 429 && resp->key_id == 4 && resp->attempts == 0);
    acn_response_free(resp);

    /* Redirects are returned, never followed. */
    get.path = "/redirect";
    size_t before = fake_count(srv);
    CHECK_CODE(acn_proxy(c, &get, &resp, &err), ACN_OK);
    CHECK(resp && resp->status == 302);
    CHECK(fake_count(srv) == before + 1);
    acn_response_free(resp);

    /* Header injection is refused. */
    acn_pair evil[] = {{"X-A", "1\r\nX-B: 2"}};
    get.headers = evil;
    get.header_count = 1;
    CHECK_CODE(acn_proxy(c, &get, &resp, &err), ACN_ERR_INVALID_ARGUMENT);
    get.headers = NULL;
    get.header_count = 0;
    get.method = "GE T";
    CHECK_CODE(acn_proxy(c, &get, &resp, &err), ACN_ERR_INVALID_ARGUMENT);
    acn_client_free(c);
}

typedef struct mapping {
    int status;
    const char *code;
    acn_code want;
} mapping;

static void test_error_mapping(void) {
    static const mapping table[] = {
        {401, "unauthenticated", ACN_ERR_UNAUTHENTICATED},
        {403, "unauthenticated", ACN_ERR_UNAUTHENTICATED},
        {403, "service_not_allowed", ACN_ERR_SERVICE_NOT_ALLOWED},
        {403, "proxy_not_allowed", ACN_ERR_PROXY_NOT_ALLOWED},
        {403, "lease_not_allowed", ACN_ERR_LEASE_NOT_ALLOWED},
        {503, "no_key_available", ACN_ERR_NO_KEY_AVAILABLE},
        {503, "all_keys_busy", ACN_ERR_ALL_KEYS_BUSY},
        {503, "service_unavailable", ACN_ERR_SERVICE_UNAVAILABLE},
        {429, "app_limit_reached", ACN_ERR_APP_LIMIT_REACHED},
        {403, "share_revoked", ACN_ERR_SHARE_REVOKED},
        {403, "share_paused", ACN_ERR_SHARE_PAUSED},
        {403, "share_expired", ACN_ERR_SHARE_EXPIRED},
        {404, "lease_not_found", ACN_ERR_LEASE_NOT_FOUND},
        {409, "report_failed", ACN_ERR_REPORT_FAILED},
        {410, "report_failed", ACN_ERR_REPORT_FAILED},
        {502, "upstream_unreachable", ACN_ERR_UPSTREAM_UNREACHABLE},
        {502, "upstream_blocked", ACN_ERR_UPSTREAM_BLOCKED},
        {508, "loop_detected", ACN_ERR_LOOP_DETECTED},
        {400, "bad_path", ACN_ERR_BAD_PATH},
        {413, "body_too_large", ACN_ERR_BODY_TOO_LARGE},
        {404, "unknown_service", ACN_ERR_API},
        {405, "method_not_allowed", ACN_ERR_API},
        {503, "empty_group", ACN_ERR_API},
        {403, "lease_revoked", ACN_ERR_API},
    };
    acn_client *c = new_client();
    acn_error err;
    for (size_t i = 0; i < sizeof table / sizeof table[0]; i++) {
        char body[256];
        snprintf(body, sizeof body, "{\"error\":{\"code\":\"%s\",\"message\":\"m-%s\"}}", table[i].code, table[i].code);
        canned cn = {table[i].status, body, NULL, NULL, NULL, NULL};
        fake_set_handler(srv, canned_handler, &cn);

        /* Through the JSON API. */
        acn_lease *l = NULL;
        acn_code got = acn_lease_acquire(c, "agi-labs", NULL, &l, &err);
        if (got != table[i].want) fprintf(stderr, "  lease mapping for %s\n", table[i].code);
        CHECK_CODE(got, table[i].want);
        CHECK(l == NULL);
        CHECK(err.code == table[i].want);
        CHECK(err.status == table[i].status);
        CHECK_STR(err.error_code, table[i].code);
        CHECK(strncmp(err.message, "m-", 2) == 0);

        /* Through the proxy: Acnologia's own answer has no X-Acnologia-Service. */
        acn_proxy_request req = {0};
        req.service = "agi-labs";
        req.path = "/v1/chat";
        acn_response *resp = NULL;
        got = acn_proxy(c, &req, &resp, &err);
        if (got != table[i].want) fprintf(stderr, "  proxy mapping for %s\n", table[i].code);
        CHECK_CODE(got, table[i].want);
        CHECK(resp == NULL);
        CHECK(err.status == table[i].status);
    }

    /* No JSON at all. */
    canned text = {500, "upstream exploded", NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &text);
    acn_lease *l = NULL;
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_API);
    CHECK(err.status == 500);
    CHECK_STR(err.error_code, "http_500");
    CHECK_STR(err.message, "upstream exploded");
    canned bare401 = {401, "", NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &bare401);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_UNAUTHENTICATED);
    CHECK_STR(err.message, "http 401");

    /* A share refusal named only by its header. */
    canned share = {403, "{\"error\":{\"code\":\"forbidden\",\"message\":\"no\"}}", "X-Acnologia-Share", "paused", NULL, NULL};
    fake_set_handler(srv, canned_handler, &share);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_SHARE_PAUSED);
    acn_client_free(c);
}

static void test_retry_after(void) {
    acn_client *c = new_client();
    acn_error err;
    acn_lease *l = NULL;

    /* The body's retry_after_sec wins over the header. */
    canned both = {503, "{\"error\":{\"code\":\"all_keys_busy\",\"message\":\"busy\"},\"retry_after_sec\":1}",
                   "Retry-After", "9", NULL, NULL};
    fake_set_handler(srv, canned_handler, &both);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_ALL_KEYS_BUSY);
    CHECK(err.retry_after_sec == 1);
    CHECK(acn_is_no_key(err.code));

    /* Only the header. */
    canned header = {503, "{\"error\":{\"code\":\"service_unavailable\",\"message\":\"circuit open\"}}", "Retry-After",
                     "30", NULL, NULL};
    fake_set_handler(srv, canned_handler, &header);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_SERVICE_UNAVAILABLE);
    CHECK(err.retry_after_sec == 30);
    CHECK(!acn_is_no_key(err.code));

    /* retry_after_sec 0 falls back to the header too. */
    canned zero = {503, "{\"error\":{\"code\":\"no_key_available\",\"message\":\"x\"},\"retry_after_sec\":0}",
                   "Retry-After", "7", NULL, NULL};
    fake_set_handler(srv, canned_handler, &zero);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_NO_KEY_AVAILABLE);
    CHECK(err.retry_after_sec == 7);

    /* An HTTP date. */
    char date[64];
    time_t at = time(NULL) + 120;
    struct tm tmv;
#ifdef _WIN32
    gmtime_s(&tmv, &at);
#else
    gmtime_r(&at, &tmv);
#endif
    strftime(date, sizeof date, "%a, %d %b %Y %H:%M:%S GMT", &tmv);
    canned dated = {503, "{\"error\":{\"code\":\"no_key_available\",\"message\":\"x\"}}", "Retry-After", date, NULL, NULL};
    fake_set_handler(srv, canned_handler, &dated);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_NO_KEY_AVAILABLE);
    CHECK(err.retry_after_sec > 100 && err.retry_after_sec <= 121);

    /* Neither. */
    canned neither = {503, "{\"error\":{\"code\":\"no_key_available\",\"message\":\"x\"}}", NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &neither);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_NO_KEY_AVAILABLE);
    CHECK(err.retry_after_sec == 0);

    /* app_limit_reached carries limit and reset_at, on the proxy as well. */
    canned limit = {429,
                    "{\"error\":{\"code\":\"app_limit_reached\",\"message\":\"this app reached its daily requests limit\"},"
                    "\"limit\":\"daily_requests\",\"reset_at\":\"2026-11-05T00:00:00Z\",\"retry_after_sec\":3600}",
                    "Retry-After", "3600", NULL, NULL};
    fake_set_handler(srv, canned_handler, &limit);
    acn_proxy_request req = {0};
    req.service = "agi-labs";
    req.path = "/v1/chat";
    acn_response *resp = NULL;
    CHECK_CODE(acn_proxy(c, &req, &resp, &err), ACN_ERR_APP_LIMIT_REACHED);
    CHECK(err.status == 429);
    CHECK(err.retry_after_sec == 3600);
    CHECK_STR(err.limit, "daily_requests");
    CHECK_STR(err.reset_at, "2026-11-05T00:00:00Z");
    acn_client_free(c);
}

static void test_scrubbing(void) {
    acn_client_options o;
    acn_client_options_init(&o);
    o.base_url = fake_url(srv);
    o.token = TOKEN;
    o.access_password = "hunter2pw";
    acn_client *c = NULL;
    acn_error err;
    CHECK_CODE(acn_client_new(&o, &c, &err), ACN_OK);

    canned echo = {403,
                   "{\"error\":{\"code\":\"service_not_allowed\",\"message\":\"token " TOKEN " or " TOKEN_ENC
                   " with hunter2pw\"}}",
                   NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &echo);
    acn_lease *l = NULL;
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_SERVICE_NOT_ALLOWED);
    CHECK(strstr(err.message, "acn_test_token") == NULL);
    CHECK(strstr(err.message, "hunter2pw") == NULL);
    CHECK_STR(err.message, "token [token] or [token] with [redacted]");
    CHECK_STR(fake_header(fake_last(srv), "X-Acnologia-Access"), "hunter2pw");

    /* Errors on a lease call hide the lease secret too. */
    pool p;
    init_pool(&p);
    fake_set_handler(srv, pool_handler, &p);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_OK);
    canned leak = {409, "{\"error\":{\"code\":\"report_failed\",\"message\":\"key sk-1+/ (sk-1%2B%2F) done\"}}", NULL, NULL,
                   NULL, NULL};
    fake_set_handler(srv, canned_handler, &leak);
    acn_report r = {0};
    CHECK_CODE(acn_lease_report(c, l, &r, NULL, &err), ACN_ERR_REPORT_FAILED);
    CHECK_STR(err.message, "key [key] ([key]) done");
    acn_lease_free(l);

    /* A long message is cut on a character boundary. */
    char body[2048];
    char msg[1100];
    size_t n = 0;
    while (n + 2 < 1000) {
        msg[n++] = '\xC3'; /* U+00E9, two bytes */
        msg[n++] = '\xA9';
    }
    msg[n] = '\0';
    snprintf(body, sizeof body, "{\"error\":{\"code\":\"x\",\"message\":\"a%s\"}}", msg);
    canned longmsg = {400, body, NULL, NULL, NULL, NULL};
    fake_set_handler(srv, canned_handler, &longmsg);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_API);
    CHECK(strlen(err.message) == 499);
    acn_client_free(c);

    /* A transport error never names the token. */
    acn_client_options_init(&o);
    o.base_url = "http://127.0.0.1:1";
    o.token = TOKEN;
    o.access_password = "";
    o.connect_timeout_ms = 2000;
    CHECK_CODE(acn_client_new(&o, &c, &err), ACN_OK);
    CHECK_CODE(acn_lease_acquire(c, "agi-labs", NULL, &l, &err), ACN_ERR_TRANSPORT);
    CHECK(err.status == 0);
    CHECK(err.message[0] != '\0');
    CHECK(strstr(err.message, "acn_test") == NULL);
    acn_client_free(c);
}

static acn_lease make_lease(const char *type, const char *name, const char *prefix) {
    acn_lease l;
    memset(&l, 0, sizeof l);
    l.secret = (char *)"sk/1";
    l.auth.type = (char *)type;
    l.auth.name = (char *)name;
    l.auth.prefix = (char *)prefix;
    l.base_url = (char *)"https://api.agi-labs.test";
    return l;
}

static void check_apply(const char *type, const char *name, const char *prefix, const char *url, const char *want_url,
                        const char *want_header) {
    acn_lease l = make_lease(type, name, prefix);
    char *u = NULL, *h = NULL;
    acn_error err;
    CHECK_CODE(acn_lease_apply(&l, url, &u, &h, &err), ACN_OK);
    CHECK_STR(u, want_url);
    if (want_header) {
        CHECK_STR(h, want_header);
    } else {
        CHECK(h == NULL);
    }
    acn_free(u);
    acn_free(h);
}

static void test_apply(void) {
    const char *u = "https://api.agi-labs.test/v1/chat";
    check_apply("query", "api_key", "", u, "https://api.agi-labs.test/v1/chat?api_key=sk%2F1", NULL);
    check_apply("query", "key", "pk_", "https://api.agi-labs.test/v1/chat?q=a#top",
                "https://api.agi-labs.test/v1/chat?q=a&key=pk_sk%2F1#top", NULL);
    check_apply("query", "", "", "https://api.agi-labs.test/v1/chat?", "https://api.agi-labs.test/v1/chat?api_key=sk%2F1",
                NULL);
    check_apply("header", "X-Api-Key", "", u, u, "X-Api-Key: sk/1");
    check_apply("header", "Authorization", "Token ", u, u, "Authorization: Token sk/1");
    check_apply("header", "", "", u, u, "Authorization: sk/1");
    check_apply("bearer", "", "", u, u, "Authorization: Bearer sk/1");
    check_apply("bearer", "X-Auth", "", u, u, "X-Auth: Bearer sk/1");
    check_apply("bearer", "", "Key ", u, u, "Authorization: Key sk/1");

    acn_lease l = make_lease("weird", "x", "");
    char *out = NULL, *h = NULL;
    acn_error err;
    CHECK_CODE(acn_lease_apply(&l, u, &out, &h, &err), ACN_ERR_INVALID_ARGUMENT);
    CHECK(out == NULL && h == NULL);

    /* Query auth needs no header slot. */
    l = make_lease("query", "api_key", "");
    CHECK_CODE(acn_lease_apply(&l, u, &out, NULL, &err), ACN_OK);
    acn_free(out);
    l = make_lease("header", "X-Api-Key", "");
    CHECK_CODE(acn_lease_apply(&l, u, &out, NULL, &err), ACN_ERR_INVALID_ARGUMENT);

    CHECK_CODE(acn_lease_url(&l, "/v1/chat", &out, &err), ACN_OK);
    CHECK_STR(out, "https://api.agi-labs.test/v1/chat");
    acn_free(out);
    CHECK_CODE(acn_lease_url(&l, "v1/chat", &out, &err), ACN_OK);
    CHECK_STR(out, "https://api.agi-labs.test/v1/chat");
    acn_free(out);
}

static void test_names(void) {
    CHECK_STR(acn_outcome_name(ACN_OUTCOME_QUOTA_EXHAUSTED), "quota_exhausted");
    CHECK_STR(acn_outcome_name(ACN_OUTCOME_BANNED), "banned");
    CHECK_STR(acn_strerror(ACN_ERR_APP_LIMIT_REACHED), "app limit reached");
    CHECK_STR(acn_strerror(ACN_OK), "ok");
    acn_client_free(NULL);
    acn_lease_free(NULL);
    acn_response_free(NULL);
    acn_me_free(NULL);
    acn_service_list_free(NULL);
    acn_free(NULL);
}

int main(void) {
    if (acn_global_init() != ACN_OK) return 2;
    srv = fake_start(NULL, NULL);
    struct {
        const char *name;
        void (*fn)(void);
    } tests[] = {
        {"env_defaults", test_env_defaults},
        {"access_password", test_access_password},
        {"me", test_me},
        {"services", test_services},
        {"lease_body_and_fields", test_lease_body_and_fields},
        {"renew", test_renew},
        {"report", test_report},
        {"with_key", test_with_key},
        {"proxy", test_proxy},
        {"error_mapping", test_error_mapping},
        {"retry_after", test_retry_after},
        {"scrubbing", test_scrubbing},
        {"apply", test_apply},
        {"names", test_names},
    };
    for (size_t i = 0; i < sizeof tests / sizeof tests[0]; i++) {
        int before = failures;
        fake_reset(srv);
        tests[i].fn();
        printf("%s %s\n", failures == before ? "ok  " : "FAIL", tests[i].name);
    }
    fake_stop(srv);
    acn_global_cleanup();
    printf("%d checks, %d failed\n", checks, failures);
    return failures ? 1 : 0;
}
