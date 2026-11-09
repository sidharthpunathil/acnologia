package dev.acnologia;

import java.time.Duration;
import java.time.Instant;

/**
 * HTTP 429 {@code app_limit_reached}: the app used up one of its own daily or
 * monthly request or budget limits. {@link #limit()} says which, and
 * {@link #resetAt()} when it resets.
 */
public class AppLimitReachedException extends ApiException {
    private static final long serialVersionUID = 1L;

    private final String limit;
    private final Instant resetAt;

    /**
     * @param message    the error message
     * @param limit      {@code daily_requests}, {@code monthly_requests}, {@code daily_budget} or {@code monthly_budget}
     * @param resetAt    when the limit resets, may be null
     * @param retryAfter how long to wait, zero if unknown
     */
    public AppLimitReachedException(String message, String limit, Instant resetAt, Duration retryAfter) {
        super(429, "app_limit_reached", message, retryAfter);
        this.limit = limit == null ? "" : limit;
        this.resetAt = resetAt;
    }

    /** @return the limit reached, such as {@code daily_requests}, or an empty string */
    public String limit() {
        return limit;
    }

    /** @return when the limit resets, or null when the server did not say */
    public Instant resetAt() {
        return resetAt;
    }
}
