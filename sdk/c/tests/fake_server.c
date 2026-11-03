#include "fake_server.h"

#include <ctype.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
typedef SOCKET sock_t;
#define BAD_SOCK INVALID_SOCKET
#define close_sock closesocket
typedef HANDLE thread_t;
typedef CRITICAL_SECTION mutex_t;
static void mutex_init(mutex_t *m) { InitializeCriticalSection(m); }
static void mutex_lock(mutex_t *m) { EnterCriticalSection(m); }
static void mutex_unlock(mutex_t *m) { LeaveCriticalSection(m); }
static void mutex_destroy(mutex_t *m) { DeleteCriticalSection(m); }
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <pthread.h>
#include <sys/socket.h>
#include <unistd.h>
typedef int sock_t;
#define BAD_SOCK (-1)
#define close_sock close
typedef pthread_t thread_t;
typedef pthread_mutex_t mutex_t;
static void mutex_init(mutex_t *m) { pthread_mutex_init(m, NULL); }
static void mutex_lock(mutex_t *m) { pthread_mutex_lock(m); }
static void mutex_unlock(mutex_t *m) { pthread_mutex_unlock(m); }
static void mutex_destroy(mutex_t *m) { pthread_mutex_destroy(m); }
#endif

struct fake_server {
    sock_t listener;
    int port;
    char url[64];
    thread_t thread;
    mutex_t mu;
    volatile int stopping;
    fake_handler handler;
    void *user;
    fake_req *reqs;
    size_t count;
    size_t cap;
};

static char *dupn(const char *s, size_t n) {
    char *p = malloc(n + 1);
    if (!p) abort();
    memcpy(p, s, n);
    p[n] = '\0';
    return p;
}

static void free_req(fake_req *r) {
    free(r->target);
    free(r->path);
    free(r->query);
    for (size_t i = 0; i < r->header_count; i++) {
        free(r->header_names[i]);
        free(r->header_values[i]);
    }
    free(r->body);
}

const char *fake_header(const fake_req *req, const char *name) {
    for (size_t i = 0; i < req->header_count; i++) {
        const char *a = req->header_names[i], *b = name;
        while (*a && *b && tolower((unsigned char)*a) == tolower((unsigned char)*b)) {
            a++;
            b++;
        }
        if (!*a && !*b) return req->header_values[i];
    }
    return NULL;
}

void fake_json(fake_resp *resp, int status, const char *body) {
    resp->status = status;
    snprintf(resp->body, sizeof resp->body, "%s", body);
    resp->body_len = 0;
    fake_add_header(resp, "Content-Type", "application/json");
}

void fake_add_header(fake_resp *resp, const char *name, const char *value) {
    size_t used = strlen(resp->headers);
    snprintf(resp->headers + used, sizeof resp->headers - used, "%s: %s\r\n", name, value);
}

static int send_all(sock_t s, const char *p, size_t n) {
    while (n) {
        int k = (int)send(s, p, (int)n, 0);
        if (k <= 0) return -1;
        p += k;
        n -= (size_t)k;
    }
    return 0;
}

/* Reads one request. Returns 0 on success. */
static int read_req(sock_t s, fake_req *r) {
    size_t cap = 8192, len = 0;
    char *buf = malloc(cap);
    if (!buf) abort();
    char *end = NULL;
    while (!end) {
        if (len + 1 >= cap) {
            cap *= 2;
            buf = realloc(buf, cap);
            if (!buf) abort();
        }
        int k = (int)recv(s, buf + len, (int)(cap - len - 1), 0);
        if (k <= 0) {
            free(buf);
            return -1;
        }
        len += (size_t)k;
        buf[len] = '\0';
        end = strstr(buf, "\r\n\r\n");
    }
    size_t head_len = (size_t)(end - buf) + 4;

    char *line_end = strstr(buf, "\r\n");
    char *sp1 = memchr(buf, ' ', (size_t)(line_end - buf));
    char *sp2 = sp1 ? memchr(sp1 + 1, ' ', (size_t)(line_end - sp1 - 1)) : NULL;
    if (!sp1 || !sp2) {
        free(buf);
        return -1;
    }
    snprintf(r->method, sizeof r->method, "%.*s", (int)(sp1 - buf), buf);
    r->target = dupn(sp1 + 1, (size_t)(sp2 - sp1 - 1));
    char *q = strchr(r->target, '?');
    r->path = dupn(r->target, q ? (size_t)(q - r->target) : strlen(r->target));
    r->query = dupn(q ? q + 1 : "", q ? strlen(q + 1) : 0);

    size_t content_length = 0;
    char *p = line_end + 2;
    while (p < end && r->header_count < FAKE_MAX_HEADERS) {
        char *e = strstr(p, "\r\n");
        char *colon = memchr(p, ':', (size_t)(e - p));
        if (colon) {
            char *v = colon + 1;
            while (*v == ' ') v++;
            r->header_names[r->header_count] = dupn(p, (size_t)(colon - p));
            r->header_values[r->header_count] = dupn(v, (size_t)(e - v));
            r->header_count++;
        }
        p = e + 2;
    }
    const char *cl = fake_header(r, "Content-Length");
    if (cl) content_length = (size_t)strtoul(cl, NULL, 10);

    r->body = malloc(content_length + 1);
    if (!r->body) abort();
    size_t have = len - head_len;
    if (have > content_length) have = content_length;
    memcpy(r->body, buf + head_len, have);
    free(buf);
    while (have < content_length) {
        int k = (int)recv(s, r->body + have, (int)(content_length - have), 0);
        if (k <= 0) break;
        have += (size_t)k;
    }
    r->body[have] = '\0';
    r->body_len = have;
    return 0;
}

static const char *reason(int status) {
    switch (status) {
    case 200: return "OK";
    case 302: return "Found";
    case 401: return "Unauthorized";
    case 403: return "Forbidden";
    case 404: return "Not Found";
    case 429: return "Too Many Requests";
    case 503: return "Service Unavailable";
    default: return "Status";
    }
}

static void serve(fake_server *s, sock_t c) {
    fake_req req;
    memset(&req, 0, sizeof req);
    if (read_req(c, &req) != 0) {
        free_req(&req);
        return;
    }
    fake_resp *resp = calloc(1, sizeof *resp);
    if (!resp) abort();
    resp->status = 404;
    mutex_lock(&s->mu);
    fake_handler h = s->handler;
    void *user = s->user;
    mutex_unlock(&s->mu);
    if (h) h(&req, resp, user);
    size_t blen = resp->body_len ? resp->body_len : strlen(resp->body);
    char head[4096];
    int n = snprintf(head, sizeof head, "HTTP/1.1 %d %s\r\nContent-Length: %zu\r\nConnection: close\r\n%s\r\n",
                     resp->status, reason(resp->status), blen, resp->headers);
    /* Record before answering, so the client sees it once its call returns. */
    mutex_lock(&s->mu);
    if (s->count == s->cap) {
        s->cap = s->cap ? s->cap * 2 : 16;
        s->reqs = realloc(s->reqs, s->cap * sizeof *s->reqs);
        if (!s->reqs) abort();
    }
    s->reqs[s->count++] = req;
    mutex_unlock(&s->mu);
    if (send_all(c, head, (size_t)n) == 0 && strcmp(req.method, "HEAD") != 0) send_all(c, resp->body, blen);
    free(resp);
}

#ifdef _WIN32
static DWORD WINAPI loop(LPVOID arg) {
#else
static void *loop(void *arg) {
#endif
    fake_server *s = arg;
    for (;;) {
        sock_t c = accept(s->listener, NULL, NULL);
        if (s->stopping) {
            if (c != BAD_SOCK) close_sock(c);
            break;
        }
        if (c == BAD_SOCK) continue;
        serve(s, c);
#ifdef _WIN32
        shutdown(c, SD_SEND);
#else
        shutdown(c, SHUT_WR);
#endif
        close_sock(c);
    }
    return 0;
}

fake_server *fake_start(fake_handler handler, void *user) {
#ifdef _WIN32
    WSADATA wsa;
    WSAStartup(MAKEWORD(2, 2), &wsa);
#endif
    fake_server *s = calloc(1, sizeof *s);
    if (!s) abort();
    s->handler = handler;
    s->user = user;
    mutex_init(&s->mu);
    s->listener = socket(AF_INET, SOCK_STREAM, 0);
    if (s->listener == BAD_SOCK) abort();
    struct sockaddr_in addr;
    memset(&addr, 0, sizeof addr);
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    addr.sin_port = 0;
    if (bind(s->listener, (struct sockaddr *)&addr, sizeof addr) != 0) abort();
    if (listen(s->listener, 16) != 0) abort();
    socklen_t alen = sizeof addr;
    if (getsockname(s->listener, (struct sockaddr *)&addr, &alen) != 0) abort();
    s->port = ntohs(addr.sin_port);
    snprintf(s->url, sizeof s->url, "http://127.0.0.1:%d", s->port);
#ifdef _WIN32
    s->thread = CreateThread(NULL, 0, loop, s, 0, NULL);
    if (!s->thread) abort();
#else
    if (pthread_create(&s->thread, NULL, loop, s) != 0) abort();
#endif
    return s;
}

void fake_stop(fake_server *s) {
    if (!s) return;
    s->stopping = 1;
    /* Wake accept with one last connection. */
    sock_t c = socket(AF_INET, SOCK_STREAM, 0);
    struct sockaddr_in addr;
    memset(&addr, 0, sizeof addr);
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    addr.sin_port = htons((unsigned short)s->port);
    if (c != BAD_SOCK) {
        (void)connect(c, (struct sockaddr *)&addr, sizeof addr);
    }
#ifdef _WIN32
    WaitForSingleObject(s->thread, INFINITE);
    CloseHandle(s->thread);
#else
    pthread_join(s->thread, NULL);
#endif
    if (c != BAD_SOCK) close_sock(c);
    close_sock(s->listener);
    fake_reset(s);
    free(s->reqs);
    mutex_destroy(&s->mu);
    free(s);
#ifdef _WIN32
    WSACleanup();
#endif
}

const char *fake_url(const fake_server *s) { return s->url; }
int fake_port(const fake_server *s) { return s->port; }

void fake_set_handler(fake_server *s, fake_handler handler, void *user) {
    mutex_lock(&s->mu);
    s->handler = handler;
    s->user = user;
    mutex_unlock(&s->mu);
}

size_t fake_count(fake_server *s) {
    mutex_lock(&s->mu);
    size_t n = s->count;
    mutex_unlock(&s->mu);
    return n;
}

const fake_req *fake_at(fake_server *s, size_t i) {
    mutex_lock(&s->mu);
    const fake_req *r = i < s->count ? &s->reqs[i] : NULL;
    mutex_unlock(&s->mu);
    return r;
}

const fake_req *fake_last(fake_server *s) {
    size_t n = fake_count(s);
    return n ? fake_at(s, n - 1) : NULL;
}

void fake_reset(fake_server *s) {
    mutex_lock(&s->mu);
    for (size_t i = 0; i < s->count; i++) free_req(&s->reqs[i]);
    s->count = 0;
    mutex_unlock(&s->mu);
}
