package dev.acnologia;

import java.time.Duration;

/** The provider rate limited the key. Throw it from a {@code withKey} callback to rotate. */
public class RateLimitedException extends KeyRotationException {
    private static final long serialVersionUID = 1L;

    private final Duration retryAfter;

    /** Rate limited, with no cooldown hint. */
    public RateLimitedException() {
        this(Duration.ZERO, 0, null, null);
    }

    /**
     * @param retryAfter how long the key should cool down, zero if unknown
     */
    public RateLimitedException(Duration retryAfter) {
        this(retryAfter, 0, null, null);
    }

    /**
     * @param retryAfter how long the key should cool down, zero if unknown
     * @param statusCode the provider's HTTP status, 0 if unknown
     * @param message    extra text for the report, may be null
     */
    public RateLimitedException(Duration retryAfter, int statusCode, String message) {
        this(retryAfter, statusCode, message, null);
    }

    /**
     * @param retryAfter how long the key should cool down, zero if unknown
     * @param statusCode the provider's HTTP status, 0 if unknown
     * @param message    extra text for the report, may be null
     * @param cause      the underlying error, may be null
     */
    public RateLimitedException(Duration retryAfter, int statusCode, String message, Throwable cause) {
        super(base(retryAfter), statusCode, message, cause);
        this.retryAfter = retryAfter == null ? Duration.ZERO : retryAfter;
    }

    private static String base(Duration retryAfter) {
        if (retryAfter != null && !retryAfter.isZero() && !retryAfter.isNegative()) {
            return "acnologia: rate limited (retry after " + retryAfter + ")";
        }
        return "acnologia: rate limited";
    }

    /** @return how long the key should cool down, {@link Duration#ZERO} if unknown */
    public Duration retryAfter() {
        return retryAfter;
    }

    @Override
    public Outcome outcome() {
        return Outcome.RATE_LIMITED;
    }

    @Override
    Report.Builder toReport(String secret) {
        return super.toReport(secret).retryAfter(retryAfter);
    }
}
