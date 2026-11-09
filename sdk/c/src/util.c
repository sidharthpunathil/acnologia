#include <ctype.h>
#include <stdlib.h>
#include <string.h>

#include "internal.h"

void acn__buf_append(acn__buf *b, const void *data, size_t len) {
    if (b->failed) return;
    if (len > (size_t)-1 - b->len - 1) {
        b->failed = 1;
        return;
    }
    if (b->len + len + 1 > b->cap) {
        size_t cap = b->cap ? b->cap : 64;
        while (cap < b->len + len + 1) {
            if (cap > (size_t)-1 / 2) {
                cap = b->len + len + 1;
                break;
            }
            cap *= 2;
        }
        char *p = realloc(b->data, cap);
        if (!p) {
            b->failed = 1;
            return;
        }
        b->data = p;
        b->cap = cap;
    }
    if (len) memcpy(b->data + b->len, data, len);
    b->len += len;
    b->data[b->len] = '\0';
}

void acn__buf_puts(acn__buf *b, const char *s) {
    if (s) acn__buf_append(b, s, strlen(s));
}

void acn__buf_putc(acn__buf *b, char c) { acn__buf_append(b, &c, 1); }

static int unreserved(unsigned char c) {
    return isalnum(c) || c == '-' || c == '.' || c == '_' || c == '~';
}

void acn__buf_encode(acn__buf *b, const char *s) {
    static const char hex[] = "0123456789ABCDEF";
    for (const unsigned char *p = (const unsigned char *)s; p && *p; p++) {
        if (unreserved(*p) && *p < 0x80) {
            acn__buf_putc(b, (char)*p);
        } else {
            char enc[3] = {'%', hex[*p >> 4], hex[*p & 15]};
            acn__buf_append(b, enc, 3);
        }
    }
}

char *acn__buf_take(acn__buf *b) {
    if (b->failed) {
        acn__buf_free(b);
        return NULL;
    }
    if (!b->data) acn__buf_append(b, "", 0);
    if (b->failed) {
        acn__buf_free(b);
        return NULL;
    }
    char *out = b->data;
    b->data = NULL;
    b->len = b->cap = 0;
    return out;
}

void acn__buf_free(acn__buf *b) {
    free(b->data);
    b->data = NULL;
    b->len = b->cap = 0;
    b->failed = 0;
}

char *acn__strndup(const char *s, size_t n) {
    char *p = malloc(n + 1);
    if (!p) return NULL;
    if (n) memcpy(p, s, n);
    p[n] = '\0';
    return p;
}

char *acn__strdup(const char *s) { return s ? acn__strndup(s, strlen(s)) : NULL; }

int acn__strncaseeq(const char *a, const char *b, size_t n) {
    for (size_t i = 0; i < n; i++) {
        unsigned char x = (unsigned char)a[i], y = (unsigned char)b[i];
        if (tolower(x) != tolower(y)) return 0;
        if (!x) return 1;
    }
    return 1;
}

int acn__strcaseeq(const char *a, const char *b) {
    size_t la = strlen(a), lb = strlen(b);
    return la == lb && acn__strncaseeq(a, b, la);
}

static size_t utf8_cut(const char *s, size_t len, size_t max) {
    if (len <= max) return len;
    size_t n = max;
    /* Back up off continuation bytes so a character is never split. */
    while (n > 0 && ((unsigned char)s[n] & 0xC0) == 0x80) n--;
    return n;
}

void acn__copy_trunc(char *dst, size_t dst_size, const char *src, size_t src_len) {
    if (!dst || !dst_size) return;
    size_t n = utf8_cut(src ? src : "", src ? src_len : 0, dst_size - 1);
    if (n) memcpy(dst, src, n);
    dst[n] = '\0';
}

char *acn__scrub(const char *msg, size_t msg_len, const acn__redact *redact, size_t n, size_t max) {
    /* Each needle is matched raw and percent encoded. */
    size_t forms = n * 2;
    char **needles = calloc(forms ? forms : 1, sizeof *needles);
    if (!needles) return NULL;
    acn__buf out = {0};
    int ok = 1;
    for (size_t i = 0; i < n && ok; i++) {
        if (!redact[i].needle || !*redact[i].needle) continue;
        acn__buf e = {0};
        acn__buf_encode(&e, redact[i].needle);
        needles[2 * i] = acn__strdup(redact[i].needle);
        needles[2 * i + 1] = acn__buf_take(&e);
        if (!needles[2 * i] || !needles[2 * i + 1]) ok = 0;
    }
    for (size_t pos = 0; ok && pos < msg_len;) {
        size_t best = 0, best_i = 0;
        for (size_t f = 0; f < forms; f++) {
            if (!needles[f]) continue;
            size_t l = strlen(needles[f]);
            if (l > best && l <= msg_len - pos && memcmp(msg + pos, needles[f], l) == 0) {
                best = l;
                best_i = f / 2;
            }
        }
        if (best) {
            acn__buf_puts(&out, redact[best_i].replacement);
            pos += best;
        } else {
            acn__buf_putc(&out, msg[pos]);
            pos++;
        }
    }
    for (size_t f = 0; f < forms; f++) free(needles[f]);
    free(needles);
    if (!ok) {
        acn__buf_free(&out);
        return NULL;
    }
    char *s = acn__buf_take(&out);
    if (s) s[utf8_cut(s, strlen(s), max)] = '\0';
    return s;
}

acn_code acn__fail(acn_error *err, acn_code code, const char *msg) {
    if (err) {
        memset(err, 0, sizeof *err);
        err->code = code;
        acn__copy_trunc(err->message, sizeof err->message, msg ? msg : acn_strerror(code),
                        strlen(msg ? msg : acn_strerror(code)));
    }
    return code;
}

void acn__wipe(char *s) {
    if (!s) return;
    volatile char *p = s;
    while (*p) *p++ = '\0';
}

void acn_free(void *ptr) { free(ptr); }

const char *acn_strerror(acn_code code) {
    switch (code) {
    case ACN_OK: return "ok";
    case ACN_ERR_INVALID_ARGUMENT: return "invalid argument";
    case ACN_ERR_NO_MEMORY: return "out of memory";
    case ACN_ERR_TRANSPORT: return "transport error";
    case ACN_ERR_BAD_RESPONSE: return "bad response";
    case ACN_ERR_API: return "api error";
    case ACN_ERR_UNAUTHENTICATED: return "unauthenticated";
    case ACN_ERR_SERVICE_NOT_ALLOWED: return "service not allowed";
    case ACN_ERR_PROXY_NOT_ALLOWED: return "proxy not allowed";
    case ACN_ERR_LEASE_NOT_ALLOWED: return "lease not allowed";
    case ACN_ERR_NO_KEY_AVAILABLE: return "no key available";
    case ACN_ERR_ALL_KEYS_BUSY: return "all keys busy";
    case ACN_ERR_SERVICE_UNAVAILABLE: return "service unavailable";
    case ACN_ERR_APP_LIMIT_REACHED: return "app limit reached";
    case ACN_ERR_SHARE_REVOKED: return "share revoked";
    case ACN_ERR_SHARE_PAUSED: return "share paused";
    case ACN_ERR_SHARE_EXPIRED: return "share expired";
    case ACN_ERR_LEASE_NOT_FOUND: return "lease not found";
    case ACN_ERR_REPORT_FAILED: return "report failed";
    case ACN_ERR_UPSTREAM_UNREACHABLE: return "upstream unreachable";
    case ACN_ERR_UPSTREAM_BLOCKED: return "upstream blocked";
    case ACN_ERR_LOOP_DETECTED: return "loop detected";
    case ACN_ERR_BAD_PATH: return "bad path";
    case ACN_ERR_BODY_TOO_LARGE: return "body too large";
    case ACN_ERR_ATTEMPTS_EXHAUSTED: return "attempts exhausted";
    }
    return "unknown error";
}

int acn_is_no_key(acn_code code) {
    return code == ACN_ERR_NO_KEY_AVAILABLE || code == ACN_ERR_ALL_KEYS_BUSY;
}

int acn_is_share_unavailable(acn_code code) { return acn_share_status(code) != NULL; }

const char *acn_share_status(acn_code code) {
    switch (code) {
    case ACN_ERR_SHARE_REVOKED: return "revoked";
    case ACN_ERR_SHARE_PAUSED: return "paused";
    case ACN_ERR_SHARE_EXPIRED: return "expired";
    default: return NULL;
    }
}

const char *acn_outcome_name(acn_outcome outcome) {
    switch (outcome) {
    case ACN_OUTCOME_OK: return "ok";
    case ACN_OUTCOME_RATE_LIMITED: return "rate_limited";
    case ACN_OUTCOME_QUOTA_EXHAUSTED: return "quota_exhausted";
    case ACN_OUTCOME_INVALID: return "invalid";
    case ACN_OUTCOME_ERROR: return "error";
    case ACN_OUTCOME_BUSY: return "busy";
    case ACN_OUTCOME_BANNED: return "banned";
    }
    return NULL;
}
