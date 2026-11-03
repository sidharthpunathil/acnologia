#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include <curl/curl.h>

#include "internal.h"

acn_code acn_global_init(void) {
    return curl_global_init(CURL_GLOBAL_DEFAULT) == CURLE_OK ? ACN_OK : ACN_ERR_TRANSPORT;
}

void acn_global_cleanup(void) { curl_global_cleanup(); }

static void clear_headers(acn__http_resp *r) {
    for (size_t i = 0; i < r->header_count; i++) {
        free(r->headers[i].name);
        free(r->headers[i].value);
    }
    r->header_count = 0;
}

void acn__http_resp_free(acn__http_resp *r) {
    if (!r) return;
    clear_headers(r);
    free(r->headers);
    r->headers = NULL;
    r->header_cap = 0;
    acn__buf_free(&r->body);
}

static size_t on_body(char *ptr, size_t size, size_t nmemb, void *user) {
    acn__http_resp *r = user;
    size_t n = size * nmemb;
    acn__buf_append(&r->body, ptr, n);
    if (r->body.failed) {
        r->oom = 1;
        return 0;
    }
    return n;
}

static int is_space(char c) { return c == ' ' || c == '\t' || c == '\r' || c == '\n'; }

static size_t on_header(char *ptr, size_t size, size_t nmemb, void *user) {
    acn__http_resp *r = user;
    size_t n = size * nmemb, len = n;
    while (len && is_space(ptr[len - 1])) len--;
    if (len >= 5 && memcmp(ptr, "HTTP/", 5) == 0) {
        /* A new status line (after a 100 Continue): start over. */
        clear_headers(r);
        return n;
    }
    const char *colon = memchr(ptr, ':', len);
    if (!len || !colon || colon == ptr) return n;
    const char *v = colon + 1;
    while (v < ptr + len && is_space(*v)) v++;
    if (r->header_count == r->header_cap) {
        size_t cap = r->header_cap ? r->header_cap * 2 : 16;
        acn_header *h = realloc(r->headers, cap * sizeof *h);
        if (!h) {
            r->oom = 1;
            return 0;
        }
        r->headers = h;
        r->header_cap = cap;
    }
    char *name = acn__strndup(ptr, (size_t)(colon - ptr));
    char *value = acn__strndup(v, (size_t)(ptr + len - v));
    if (!name || !value) {
        free(name);
        free(value);
        r->oom = 1;
        return 0;
    }
    r->headers[r->header_count].name = name;
    r->headers[r->header_count].value = value;
    r->header_count++;
    return n;
}

const char *acn__header_get(const acn_header *h, size_t n, const char *name) {
    for (size_t i = 0; i < n; i++) {
        if (acn__strcaseeq(h[i].name, name)) return h[i].value;
    }
    return NULL;
}

int acn__http_do(const acn__http_req *req, acn__http_resp *resp, char *errbuf, size_t errbuf_size) {
    char curl_err[CURL_ERROR_SIZE];
    curl_err[0] = '\0';
    CURL *h = curl_easy_init();
    if (!h) {
        snprintf(errbuf, errbuf_size, "could not start libcurl");
        return 1;
    }
    const char *method = req->method ? req->method : "GET";
    curl_easy_setopt(h, CURLOPT_URL, req->url);
    curl_easy_setopt(h, CURLOPT_ERRORBUFFER, curl_err);
    curl_easy_setopt(h, CURLOPT_NOSIGNAL, 1L);
    curl_easy_setopt(h, CURLOPT_FOLLOWLOCATION, 0L);
#if LIBCURL_VERSION_NUM >= 0x075500
    curl_easy_setopt(h, CURLOPT_PROTOCOLS_STR, "http,https");
#else
    curl_easy_setopt(h, CURLOPT_PROTOCOLS, (long)(CURLPROTO_HTTP | CURLPROTO_HTTPS));
#endif
    if (req->user_agent) curl_easy_setopt(h, CURLOPT_USERAGENT, req->user_agent);
    if (req->headers) curl_easy_setopt(h, CURLOPT_HTTPHEADER, req->headers);
    if (req->timeout_ms > 0) curl_easy_setopt(h, CURLOPT_TIMEOUT_MS, req->timeout_ms);
    if (req->connect_timeout_ms > 0) curl_easy_setopt(h, CURLOPT_CONNECTTIMEOUT_MS, req->connect_timeout_ms);
    curl_easy_setopt(h, CURLOPT_WRITEFUNCTION, on_body);
    curl_easy_setopt(h, CURLOPT_WRITEDATA, resp);
    curl_easy_setopt(h, CURLOPT_HEADERFUNCTION, on_header);
    curl_easy_setopt(h, CURLOPT_HEADERDATA, resp);

    if (strcmp(method, "HEAD") == 0) {
        curl_easy_setopt(h, CURLOPT_NOBODY, 1L);
    } else {
        if (req->has_body) {
            curl_easy_setopt(h, CURLOPT_POSTFIELDSIZE_LARGE, (curl_off_t)req->body_len);
            curl_easy_setopt(h, CURLOPT_POSTFIELDS, req->body_len ? (const char *)req->body : "");
        }
        if (strcmp(method, "GET") != 0 && !(strcmp(method, "POST") == 0 && req->has_body)) {
            curl_easy_setopt(h, CURLOPT_CUSTOMREQUEST, method);
        }
    }

    CURLcode rc = curl_easy_perform(h);
    if (rc == CURLE_OK) {
        curl_easy_getinfo(h, CURLINFO_RESPONSE_CODE, &resp->status);
    } else if (resp->oom) {
        snprintf(errbuf, errbuf_size, "out of memory reading the response");
    } else {
        snprintf(errbuf, errbuf_size, "%s", curl_err[0] ? curl_err : curl_easy_strerror(rc));
    }
    curl_easy_cleanup(h);
    return rc == CURLE_OK ? 0 : 1;
}

double acn__parse_retry_after(const char *value) {
    if (!value) return 0;
    while (*value == ' ' || *value == '\t') value++;
    if (!*value) return 0;
    char *end = NULL;
    double secs = strtod(value, &end);
    if (end != value) {
        while (*end == ' ' || *end == '\t') end++;
        if (*end == '\0') return (secs > 0 && secs < 1e9) ? secs : 0;
    }
    time_t at = curl_getdate(value, NULL);
    if (at == (time_t)-1) return 0;
    double d = difftime(at, time(NULL));
    return d > 0 ? d : 0;
}
