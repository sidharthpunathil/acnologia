package dev.acnologia;

import java.time.Duration;

/**
 * HTTP 503 {@code service_unavailable}: the provider keeps failing and its
 * circuit breaker is open. Wait {@link #retryAfter()} before trying again.
 * Acnologia sends this from the proxy; see {@link ApiException#fromResponse}.
 */
public class ServiceUnavailableException extends ApiException {
    private static final long serialVersionUID = 1L;

    private final Duration retryAfter;

    /**
     * @param statusCode the HTTP status (503)
     * @param message    the error message
     * @param retryAfter how long to wait, zero if unknown
     */
    public ServiceUnavailableException(int statusCode, String message, Duration retryAfter) {
        super(statusCode, "service_unavailable", message);
        this.retryAfter = retryAfter == null ? Duration.ZERO : retryAfter;
    }

    /** @return how long to wait before retrying, {@link Duration#ZERO} if unknown */
    public Duration retryAfter() {
        return retryAfter;
    }
}
