/* A tiny HTTP/1.1 server on 127.0.0.1 for the tests. One thread, one
 * connection at a time, Connection: close on every answer. */
#ifndef ACN_TEST_FAKE_SERVER_H
#define ACN_TEST_FAKE_SERVER_H

#include <stddef.h>

#define FAKE_MAX_HEADERS 64

typedef struct fake_req {
    char method[16];
    char *target;       /* path and query as sent, still percent encoded */
    char *path;         /* target up to the ? */
    char *query;        /* after the ?, or "" */
    char *header_names[FAKE_MAX_HEADERS];
    char *header_values[FAKE_MAX_HEADERS];
    size_t header_count;
    char *body;         /* NUL terminated */
    size_t body_len;
} fake_req;

typedef struct fake_resp {
    int status;
    char headers[2048]; /* extra header lines, each ending in \r\n */
    char body[16384];
    size_t body_len;    /* 0 means strlen(body) */
} fake_resp;

typedef void (*fake_handler)(const fake_req *req, fake_resp *resp, void *user);

typedef struct fake_server fake_server;

fake_server *fake_start(fake_handler handler, void *user);
void fake_stop(fake_server *s);
/* http://127.0.0.1:<port> */
const char *fake_url(const fake_server *s);
int fake_port(const fake_server *s);
void fake_set_handler(fake_server *s, fake_handler handler, void *user);

/* Requests received so far, oldest first. Valid until fake_reset or stop. */
size_t fake_count(fake_server *s);
const fake_req *fake_at(fake_server *s, size_t i);
const fake_req *fake_last(fake_server *s);
void fake_reset(fake_server *s);

/* First header with this name, any case, or NULL. */
const char *fake_header(const fake_req *req, const char *name);

/* Helpers for handlers. */
void fake_json(fake_resp *resp, int status, const char *body);
void fake_add_header(fake_resp *resp, const char *name, const char *value);

#endif
