package dev.acnologia;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.util.Map;
import java.util.concurrent.CompletableFuture;

/**
 * Sends requests through Acnologia's proxy for one service. Acnologia injects
 * a key, retries on rate limits with the next key, and never returns the
 * secret. Upstream responses come back unchanged: a non-2xx status is not an
 * exception. Responses carry {@code X-Acnologia-Key-Id} and
 * {@code X-Acnologia-Attempts} headers.
 *
 * <p>Redirects are never followed, so the app token is not replayed to
 * another host. Proxy calls have no default timeout because proxied bodies
 * may stream; set one on the request when you need it.
 */
public final class Proxy {
    private final Acnologia client;
    private final String service;

    Proxy(Acnologia client, String service) {
        this.client = client;
        this.service = service;
    }

    /** @return the service slug */
    public String service() {
        return service;
    }

    private String prefixPath() {
        return "/proxy/" + Acnologia.pathEscape(service);
    }

    /**
     * @param path the provider path, such as {@code /search.json}
     * @return the full proxy URL
     */
    public String url(String path) {
        return url(path, null);
    }

    /**
     * @param path  the provider path
     * @param query query parameters, may be null. Values may be iterables for repeated keys.
     * @return the full proxy URL
     */
    public String url(String path, Map<String, ?> query) {
        String u = client.baseUrl() + prefixPath() + "/" + Acnologia.trimLeft(path);
        String q = Acnologia.encodeQuery(query);
        return q.isEmpty() ? u : u + "?" + q;
    }

    /**
     * Sends a GET.
     *
     * @param path the provider path
     * @return the upstream response
     */
    public HttpResponse<byte[]> get(String path) {
        return get(path, null);
    }

    /**
     * Sends a GET with query parameters.
     *
     * @param path  the provider path
     * @param query query parameters, may be null
     * @return the upstream response
     */
    public HttpResponse<byte[]> get(String path, Map<String, ?> query) {
        return request("GET", path, query, null, null);
    }

    /**
     * Sends a POST with the given content type and body.
     *
     * @param path        the provider path
     * @param contentType the body's media type, may be null
     * @param body        the body
     * @return the upstream response
     */
    public HttpResponse<byte[]> post(String path, String contentType, byte[] body) {
        Map<String, String> headers = contentType == null ? null : Map.of("Content-Type", contentType);
        return request("POST", path, null, headers, HttpRequest.BodyPublishers.ofByteArray(body));
    }

    /**
     * Encodes {@code value} as JSON with Jackson and POSTs it.
     *
     * @param path  the provider path
     * @param value any value Jackson can write (a Map, a POJO, a JsonNode)
     * @return the upstream response
     */
    public HttpResponse<byte[]> postJson(String path, Object value) {
        return post(path, "application/json", encode(value));
    }

    /**
     * Async form of {@link #get(String, Map)}.
     *
     * @param path  the provider path
     * @param query query parameters, may be null
     * @return the upstream response
     */
    public CompletableFuture<HttpResponse<byte[]>> getAsync(String path, Map<String, ?> query) {
        return requestAsync("GET", path, query, null, null);
    }

    /**
     * Async form of {@link #postJson(String, Object)}.
     *
     * @param path  the provider path
     * @param value any value Jackson can write
     * @return the upstream response
     */
    public CompletableFuture<HttpResponse<byte[]>> postJsonAsync(String path, Object value) {
        byte[] body;
        try {
            body = encode(value);
        } catch (RuntimeException e) {
            return CompletableFuture.failedFuture(e);
        }
        return requestAsync("POST", path, null, Map.of("Content-Type", "application/json"),
                HttpRequest.BodyPublishers.ofByteArray(body));
    }

    /**
     * Sends any request and reads the body into memory.
     *
     * @param method  the HTTP method
     * @param path    the provider path
     * @param query   query parameters, may be null
     * @param headers request headers, may be null
     * @param body    the body, null for none
     * @return the upstream response
     */
    public HttpResponse<byte[]> request(String method, String path, Map<String, ?> query,
                                        Map<String, String> headers, HttpRequest.BodyPublisher body) {
        return request(method, path, query, headers, body, HttpResponse.BodyHandlers.ofByteArray());
    }

    /**
     * Sends any request with your own body handler, for example
     * {@code BodyHandlers.ofInputStream()} to stream a large answer.
     *
     * @param method  the HTTP method
     * @param path    the provider path
     * @param query   query parameters, may be null
     * @param headers request headers, may be null
     * @param body    the body, null for none
     * @param handler how to read the response body
     * @param <T>     the body type
     * @return the upstream response
     */
    public <T> HttpResponse<T> request(String method, String path, Map<String, ?> query,
                                       Map<String, String> headers, HttpRequest.BodyPublisher body,
                                       HttpResponse.BodyHandler<T> handler) {
        return sendPrepared(build(method, path, query, headers, body), handler);
    }

    /**
     * Async form of {@link #request(String, String, Map, Map, HttpRequest.BodyPublisher)}.
     *
     * @param method  the HTTP method
     * @param path    the provider path
     * @param query   query parameters, may be null
     * @param headers request headers, may be null
     * @param body    the body, null for none
     * @return the upstream response
     */
    public CompletableFuture<HttpResponse<byte[]>> requestAsync(String method, String path, Map<String, ?> query,
                                                                Map<String, String> headers,
                                                                HttpRequest.BodyPublisher body) {
        HttpRequest req;
        try {
            req = build(method, path, query, headers, body);
        } catch (RuntimeException e) {
            return CompletableFuture.failedFuture(e);
        }
        return client.proxyHttp().sendAsync(req, HttpResponse.BodyHandlers.ofByteArray());
    }

    /**
     * Sends a request you built yourself through the proxy. Only its path
     * and query are used: they are resolved under {@code /proxy/{service}/},
     * so a request built against the provider's own URL works as is. A path
     * that already starts with {@code /proxy/{service}/} is kept. Method,
     * headers, body and timeout are copied; the app token replaces any
     * {@code Authorization} header.
     *
     * @param request the request
     * @param handler how to read the response body
     * @param <T>     the body type
     * @return the upstream response
     */
    public <T> HttpResponse<T> send(HttpRequest request, HttpResponse.BodyHandler<T> handler) {
        return sendPrepared(rewrite(request), handler);
    }

    /**
     * Async form of {@link #send(HttpRequest, HttpResponse.BodyHandler)}.
     *
     * @param request the request
     * @param handler how to read the response body
     * @param <T>     the body type
     * @return the upstream response
     */
    public <T> CompletableFuture<HttpResponse<T>> sendAsync(HttpRequest request, HttpResponse.BodyHandler<T> handler) {
        HttpRequest req;
        try {
            req = rewrite(request);
        } catch (RuntimeException e) {
            return CompletableFuture.failedFuture(e);
        }
        return client.proxyHttp().sendAsync(req, handler);
    }

    private HttpRequest rewrite(HttpRequest request) {
        URI u = request.uri();
        String path = u.getRawPath() == null ? "" : u.getRawPath();
        String svc = prefixPath();
        URI base = URI.create(client.baseUrl());
        String basePath = Acnologia.trimRight(base.getRawPath() == null ? "" : base.getRawPath());
        for (String p : new String[] {basePath + svc, svc}) {
            if (path.equals(p) || path.startsWith(p + "/")) {
                path = path.substring(p.length());
                break;
            }
        }
        String target = client.baseUrl() + svc + "/" + Acnologia.trimLeft(path);
        if (u.getRawQuery() != null) {
            target += "?" + u.getRawQuery();
        }
        HttpRequest.Builder b = HttpRequest.newBuilder(request, (n, v) -> true).uri(URI.create(target));
        client.setHeaders(b);
        return b.build();
    }

    private HttpRequest build(String method, String path, Map<String, ?> query, Map<String, String> headers,
                              HttpRequest.BodyPublisher body) {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(url(path, query)));
        b.method(method.toUpperCase(java.util.Locale.ROOT),
                body == null ? HttpRequest.BodyPublishers.noBody() : body);
        if (headers != null) {
            headers.forEach(b::setHeader);
        }
        client.setHeaders(b);
        return b.build();
    }

    private <T> HttpResponse<T> sendPrepared(HttpRequest req, HttpResponse.BodyHandler<T> handler) {
        try {
            return client.proxyHttp().send(req, handler);
        } catch (IOException e) {
            throw new AcnologiaException("acnologia: proxy " + req.method() + " " + req.uri().getRawPath() + ": " + e, e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new AcnologiaException("acnologia: interrupted", e);
        }
    }

    private static byte[] encode(Object value) {
        try {
            return Acnologia.JSON.writeValueAsBytes(value);
        } catch (IOException e) {
            throw new AcnologiaException("acnologia: encode JSON: " + e.getMessage(), e);
        }
    }

    @Override
    public String toString() {
        return "Proxy{service=" + service + "}";
    }
}
