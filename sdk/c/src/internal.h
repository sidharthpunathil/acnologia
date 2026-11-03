/* Shared by the SDK's own sources. Not installed. */
#ifndef ACNOLOGIA_INTERNAL_H
#define ACNOLOGIA_INTERNAL_H

#include <stddef.h>

#include "acnologia/acnologia.h"

/* Report messages and error messages are cut to this many bytes. */
#define ACN__MAX_MESSAGE 500

/* A growable byte buffer that keeps a NUL after its data. */
typedef struct acn__buf {
    char *data;
    size_t len;
    size_t cap;
    int failed; /* set once an allocation failed; later appends do nothing */
} acn__buf;

void acn__buf_append(acn__buf *b, const void *data, size_t len);
void acn__buf_puts(acn__buf *b, const char *s);
void acn__buf_putc(acn__buf *b, char c);
/* Percent encodes everything but A-Z a-z 0-9 - . _ ~ */
void acn__buf_encode(acn__buf *b, const char *s);
/* Hands the data over (never NULL unless failed) and resets the buffer. */
char *acn__buf_take(acn__buf *b);
void acn__buf_free(acn__buf *b);

char *acn__strdup(const char *s);
char *acn__strndup(const char *s, size_t n);
int acn__strcaseeq(const char *a, const char *b);
int acn__strncaseeq(const char *a, const char *b, size_t n);
/* Copies src into dst (size dst_size), cutting on a UTF-8 boundary. */
void acn__copy_trunc(char *dst, size_t dst_size, const char *src, size_t src_len);

/* Overwrites a string before it is freed, in a way the compiler keeps. */
void acn__wipe(char *s);

/* One thing to hide and what to put in its place. Empty needles are skipped. */
typedef struct acn__redact {
    const char *needle;
    const char *replacement;
} acn__redact;

/* Returns a new string: msg with every needle, raw and percent encoded,
 * replaced, cut to max bytes on a UTF-8 boundary. NULL when out of memory. */
char *acn__scrub(const char *msg, size_t msg_len, const acn__redact *redact, size_t n, size_t max);

/* Fills err (when not NULL). msg is used as is: scrub it first. */
acn_code acn__fail(acn_error *err, acn_code code, const char *msg);

/* ---- HTTP ---- */

typedef struct acn__http_resp {
    long status;
    acn_header *headers;
    size_t header_count;
    size_t header_cap;
    acn__buf body;
    int oom;
} acn__http_resp;

typedef struct acn__http_req {
    const char *method;
    const char *url;
    const char *user_agent;
    struct curl_slist *headers;
    const void *body;
    size_t body_len;
    int has_body;
    long timeout_ms;          /* 0 means none */
    long connect_timeout_ms;  /* 0 means none */
} acn__http_req;

/* Performs the request. Returns 0 on an answer of any status, otherwise
 * fills errbuf (at least 256 bytes) and returns nonzero. */
int acn__http_do(const acn__http_req *req, acn__http_resp *resp, char *errbuf, size_t errbuf_size);
void acn__http_resp_free(acn__http_resp *resp);
const char *acn__header_get(const acn_header *h, size_t n, const char *name);

/* Maps a non 2xx answer to a code and fills err. The message is scrubbed
 * of every redact entry. */
acn_code acn__map_error(const acn__http_resp *resp, const acn__redact *redact, size_t n, acn_error *err);

/* Parses a Retry-After value: delta seconds or an HTTP date. 0 when unknown. */
double acn__parse_retry_after(const char *value);

#endif
