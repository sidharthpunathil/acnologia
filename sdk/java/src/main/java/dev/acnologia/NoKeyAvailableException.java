package dev.acnologia;

import java.time.Duration;

/**
 * HTTP 503: no key can be leased right now. The code is
 * {@code no_key_available} when every key is cooling down, exhausted or
 * excluded. When every usable key is at its concurrency limit the subclass
 * {@link AllKeysBusyException} is thrown instead.
 */
public class NoKeyAvailableException extends ApiException {
    private static final long serialVersionUID = 1L;

    private final Duration retryAfter;

    /**
     * @param statusCode the HTTP status (503)
     * @param code       {@code no_key_available} or {@code all_keys_busy}
     * @param message    the error message
     * @param retryAfter how long to wait before retrying, zero if unknown
     */
    public NoKeyAvailableException(int statusCode, String code, String message, Duration retryAfter) {
        super(statusCode, code, message);
        this.retryAfter = retryAfter == null ? Duration.ZERO : retryAfter;
    }

    /** @return how long to wait before retrying, {@link Duration#ZERO} if unknown */
    public Duration retryAfter() {
        return retryAfter;
    }

    /** @return true when every usable key is at its concurrency limit ({@code all_keys_busy}) */
    public boolean isBusy() {
        return "all_keys_busy".equals(code());
    }
}
