/* Calls every part of the SDK against a running Acnologia server.
 *
 *   ACNOLOGIA_URL=http://127.0.0.1:8080 ACNOLOGIA_TOKEN=acn_... ./acnologia_quickstart agi-labs /v1/chat
 *
 * Prints what it did. Never prints a secret. Exits nonzero on the first
 * failure. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "acnologia/acnologia.h"

static int fail(const char *what, const acn_error *err) {
    fprintf(stderr, "%s failed: %s (status %d, code %s): %s\n", what, acn_strerror(err->code), err->status,
            err->error_code, err->message);
    return 1;
}

static int run(acn_client *c, const char *service, const char *path) {
    acn_error err;

    acn_me *me = NULL;
    if (acn_me_get(c, &me, &err) != ACN_OK) return fail("me", &err);
    printf("me: app %lld \"%s\", %zu services, %lld requests today\n", (long long)me->app_id, me->app_name,
           me->service_count, (long long)me->today_requests);
    acn_me_free(me);

    acn_pair query[] = {{"q", "hello"}};
    acn_pair headers[] = {{"Content-Type", "application/json"}};
    const char *body = "{\"prompt\":\"hi\"}";
    acn_proxy_request req = {0};
    req.method = "POST";
    req.service = service;
    req.path = path;
    req.query = query;
    req.query_count = 1;
    req.headers = headers;
    req.header_count = 1;
    req.body = body;
    req.body_len = strlen(body);
    req.idempotency_key = "quickstart-1";
    for (int i = 0; i < 2; i++) {
        acn_response *resp = NULL;
        if (acn_proxy(c, &req, &resp, &err) != ACN_OK) return fail("proxy", &err);
        printf("proxy: status %d, key %lld, attempts %d, replayed %d, body %.*s\n", resp->status,
               (long long)resp->key_id, resp->attempts, resp->idempotent_replayed, (int)resp->body_len, resp->body);
        acn_response_free(resp);
    }

    acn_lease *lease = NULL;
    acn_lease_options lo = {0};
    lo.ttl_sec = 60;
    if (acn_lease_acquire(c, service, &lo, &lease, &err) != ACN_OK) return fail("lease", &err);
    printf("lease: id %s, key %lld, auth %s %s, expires %s\n", lease->lease_id, (long long)lease->key_id,
           lease->auth.type, lease->auth.name, lease->expires_at);

    char *url = NULL, *header = NULL, *target = NULL;
    if (acn_lease_url(lease, path, &target, &err) != ACN_OK ||
        acn_lease_apply(lease, target, &url, &header, &err) != ACN_OK) {
        acn_free(target);
        acn_lease_free(lease);
        return fail("apply", &err);
    }
    printf("apply: header set %s, url %s\n", header ? "yes" : "no", header ? url : "(holds the key, not printed)");
    acn_free(target);
    acn_free(url);
    acn_free(header);

    if (acn_lease_renew(c, lease, 120, &err) != ACN_OK) {
        acn_lease_free(lease);
        return fail("renew", &err);
    }
    printf("renew: expires %s\n", lease->expires_at);

    acn_report rep = {0};
    rep.outcome = ACN_OUTCOME_RATE_LIMITED;
    rep.status_code = 429;
    rep.retry_after_sec = 1;
    rep.message = lease->secret; /* sent as [key] */
    rep.rotate = 1;
    acn_lease *next = NULL;
    if (acn_lease_report(c, lease, &rep, &next, &err) != ACN_OK) {
        acn_lease_free(lease);
        return fail("report", &err);
    }
    printf("report: rate_limited with rotate, next key %lld\n", next ? (long long)next->key_id : -1LL);
    acn_lease_free(lease);
    if (next) {
        acn_report ok = {0};
        if (acn_lease_report(c, next, &ok, NULL, &err) != ACN_OK) {
            acn_lease_free(next);
            return fail("report next", &err);
        }
        printf("report: ok for key %lld\n", (long long)next->key_id);
        acn_lease_free(next);
    }

    acn_lease *again = NULL;
    acn_lease_options none = {0};
    acn_code rc = acn_lease_acquire(c, "no-such-service", &none, &again, &err);
    printf("error: %s, status %d, code %s\n", acn_strerror(rc), err.status, err.error_code);
    acn_lease_free(again);
    return 0;
}

int main(int argc, char **argv) {
    const char *service = argc > 1 ? argv[1] : "agi-labs";
    const char *path = argc > 2 ? argv[2] : "/v1/chat";
    if (acn_global_init() != ACN_OK) return 2;
    acn_client *c = NULL;
    acn_error err;
    int code = 1;
    if (acn_client_new(NULL, &c, &err) != ACN_OK) {
        code = fail("client", &err);
    } else {
        code = run(c, service, path);
    }
    acn_client_free(c);
    acn_global_cleanup();
    return code;
}
