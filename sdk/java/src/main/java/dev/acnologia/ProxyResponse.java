package dev.acnologia;

import javax.net.ssl.SSLSession;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpHeaders;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.OptionalLong;

/**
 * An {@link HttpResponse} from the proxy with readers for Acnologia's answer
 * headers. Wrap any proxy response with {@link #of(HttpResponse)}; a
 * {@link Acnologia#capability capability} call returns one directly.
 *
 * @param <T> the body type
 */
public final class ProxyResponse<T> implements HttpResponse<T> {
    private final HttpResponse<T> res;

    private ProxyResponse(HttpResponse<T> res) {
        this.res = res;
    }

    /**
     * @param res a response from the proxy
     * @param <T> the body type
     * @return the response with Acnologia's readers
     */
    @SuppressWarnings("unchecked")
    public static <T> ProxyResponse<T> of(HttpResponse<T> res) {
        if (res instanceof ProxyResponse) {
            return (ProxyResponse<T>) res;
        }
        return new ProxyResponse<>(res);
    }

    /**
     * One route a capability call tried, from {@code X-Acnologia-Tried}.
     *
     * @param service the service slug
     * @param status  the HTTP status it answered, 0 when unknown
     */
    public record Tried(String service, int status) {
    }

    private String header(String name) {
        return res.headers().firstValue(name).orElse("");
    }

    /** @return the pool key that served the call ({@code X-Acnologia-Key-Id}); empty for a share's token */
    public OptionalLong keyId() {
        try {
            String v = header("X-Acnologia-Key-Id").trim();
            return v.isEmpty() ? OptionalLong.empty() : OptionalLong.of(Long.parseLong(v));
        } catch (NumberFormatException e) {
            return OptionalLong.empty();
        }
    }

    /** @return how many keys were tried ({@code X-Acnologia-Attempts}), 0 when absent */
    public int attempts() {
        try {
            String v = header("X-Acnologia-Attempts").trim();
            return v.isEmpty() ? 0 : Integer.parseInt(v);
        } catch (NumberFormatException e) {
            return 0;
        }
    }

    /** @return the service that answered ({@code X-Acnologia-Service}), or an empty string */
    public String service() {
        return header("X-Acnologia-Service");
    }

    /** @return the cache result ({@code X-Acnologia-Cache}: {@code hit}, {@code miss}, ...), or an empty string */
    public String cache() {
        return header("X-Acnologia-Cache");
    }

    /** @return true when the answer is a replay for an earlier {@code Idempotency-Key} */
    public boolean replayed() {
        return "true".equalsIgnoreCase(header("Idempotent-Replayed").trim());
    }

    /** @return the capability that served the call ({@code X-Acnologia-Capability}), or an empty string */
    public String capability() {
        return header("X-Acnologia-Capability");
    }

    /** @return the routes a capability call tried, in order ({@code X-Acnologia-Tried}) */
    public List<Tried> tried() {
        List<Tried> out = new ArrayList<>();
        for (String part : header("X-Acnologia-Tried").split(",")) {
            String p = part.trim();
            if (p.isEmpty()) {
                continue;
            }
            int eq = p.lastIndexOf('=');
            String svc = eq < 0 ? p : p.substring(0, eq).trim();
            int status = 0;
            if (eq >= 0) {
                try {
                    status = Integer.parseInt(p.substring(eq + 1).trim());
                } catch (NumberFormatException ignored) {
                    // keep 0
                }
            }
            out.add(new Tried(svc, status));
        }
        return List.copyOf(out);
    }

    @Override
    public int statusCode() {
        return res.statusCode();
    }

    @Override
    public HttpRequest request() {
        return res.request();
    }

    @Override
    public Optional<HttpResponse<T>> previousResponse() {
        return res.previousResponse();
    }

    @Override
    public HttpHeaders headers() {
        return res.headers();
    }

    @Override
    public T body() {
        return res.body();
    }

    @Override
    public Optional<SSLSession> sslSession() {
        return res.sslSession();
    }

    @Override
    public URI uri() {
        return res.uri();
    }

    @Override
    public HttpClient.Version version() {
        return res.version();
    }

    @Override
    public String toString() {
        return "ProxyResponse{status=" + res.statusCode() + ", uri=" + res.uri() + "}";
    }
}
