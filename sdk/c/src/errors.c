#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "cJSON.h"
#include "internal.h"

static const struct {
    const char *code;
    acn_code value;
} known[] = {
    {"unauthenticated", ACN_ERR_UNAUTHENTICATED},
    {"service_not_allowed", ACN_ERR_SERVICE_NOT_ALLOWED},
    {"proxy_not_allowed", ACN_ERR_PROXY_NOT_ALLOWED},
    {"lease_not_allowed", ACN_ERR_LEASE_NOT_ALLOWED},
    {"no_key_available", ACN_ERR_NO_KEY_AVAILABLE},
    {"all_keys_busy", ACN_ERR_ALL_KEYS_BUSY},
    {"service_unavailable", ACN_ERR_SERVICE_UNAVAILABLE},
    {"app_limit_reached", ACN_ERR_APP_LIMIT_REACHED},
    {"share_revoked", ACN_ERR_SHARE_REVOKED},
    {"share_paused", ACN_ERR_SHARE_PAUSED},
    {"share_expired", ACN_ERR_SHARE_EXPIRED},
    {"lease_not_found", ACN_ERR_LEASE_NOT_FOUND},
    {"report_failed", ACN_ERR_REPORT_FAILED},
    {"upstream_unreachable", ACN_ERR_UPSTREAM_UNREACHABLE},
    {"upstream_blocked", ACN_ERR_UPSTREAM_BLOCKED},
    {"loop_detected", ACN_ERR_LOOP_DETECTED},
    {"bad_path", ACN_ERR_BAD_PATH},
    {"body_too_large", ACN_ERR_BODY_TOO_LARGE},
};

static acn_code code_for(int status, const char *code, const char *share_header) {
    for (size_t i = 0; i < sizeof known / sizeof known[0]; i++) {
        if (code && strcmp(code, known[i].code) == 0) return known[i].value;
    }
    /* A share token refused with no recognized code still names the state. */
    if (status == 403 && share_header) {
        if (acn__strcaseeq(share_header, "revoked")) return ACN_ERR_SHARE_REVOKED;
        if (acn__strcaseeq(share_header, "paused")) return ACN_ERR_SHARE_PAUSED;
        if (acn__strcaseeq(share_header, "expired")) return ACN_ERR_SHARE_EXPIRED;
    }
    if (status == 401) return ACN_ERR_UNAUTHENTICATED;
    return ACN_ERR_API;
}

static const char *json_str(const cJSON *obj, const char *name) {
    const cJSON *v = cJSON_GetObjectItemCaseSensitive(obj, name);
    return cJSON_IsString(v) ? v->valuestring : NULL;
}

acn_code acn__map_error(const acn__http_resp *resp, const acn__redact *redact, size_t n, acn_error *err) {
    int status = (int)resp->status;
    cJSON *root = resp->body.len ? cJSON_ParseWithLength(resp->body.data, resp->body.len) : NULL;
    const cJSON *e = cJSON_GetObjectItemCaseSensitive(root, "error");
    const char *code = NULL, *message = NULL;
    if (cJSON_IsObject(e)) {
        code = json_str(e, "code");
        message = json_str(e, "message");
    } else if (cJSON_IsString(e)) {
        message = e->valuestring;
    }
    const char *share = acn__header_get(resp->headers, resp->header_count, "X-Acnologia-Share");
    acn_code value = code_for(status, code, share);

    char fallback[64];
    const char *raw = message;
    size_t raw_len = message ? strlen(message) : 0;
    if (!raw && !root && resp->body.len) {
        /* Not JSON: use the start of the body. */
        raw = resp->body.data;
        raw_len = resp->body.len;
    }
    if (!raw || !raw_len) {
        snprintf(fallback, sizeof fallback, "http %d", status);
        raw = fallback;
        raw_len = strlen(fallback);
    }
    char *clean = acn__scrub(raw, raw_len, redact, n, ACN__MAX_MESSAGE);

    if (err) {
        memset(err, 0, sizeof *err);
        err->code = value;
        err->status = status;
        if (code) {
            char *c = acn__scrub(code, strlen(code), redact, n, sizeof err->error_code - 1);
            if (c) acn__copy_trunc(err->error_code, sizeof err->error_code, c, strlen(c));
            free(c);
        } else {
            snprintf(err->error_code, sizeof err->error_code, "http_%d", status);
        }
        if (clean) {
            acn__copy_trunc(err->message, sizeof err->message, clean, strlen(clean));
        } else {
            snprintf(err->message, sizeof err->message, "http %d", status);
        }
        double retry = 0;
        const cJSON *ra = cJSON_GetObjectItemCaseSensitive(root, "retry_after_sec");
        if (cJSON_IsNumber(ra) && ra->valuedouble > 0) retry = ra->valuedouble;
        if (retry <= 0) {
            retry = acn__parse_retry_after(acn__header_get(resp->headers, resp->header_count, "Retry-After"));
        }
        err->retry_after_sec = retry;
        const char *limit = json_str(root, "limit");
        const char *reset = json_str(root, "reset_at");
        if (limit) acn__copy_trunc(err->limit, sizeof err->limit, limit, strlen(limit));
        if (reset) acn__copy_trunc(err->reset_at, sizeof err->reset_at, reset, strlen(reset));
    }
    free(clean);
    cJSON_Delete(root);
    return value;
}
