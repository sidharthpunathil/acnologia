package dev.acnologia;

import java.time.Duration;

/**
 * HTTP 503 {@code all_keys_busy}: usable keys exist but every one is at its
 * concurrency limit, or at a rate limit its provider publishes. It clears as
 * soon as another caller reports a lease, so retry after {@link #retryAfter()}
 * (usually one second) or lease with a wait. It extends
 * {@link NoKeyAvailableException}, so existing catches still match.
 */
public class AllKeysBusyException extends NoKeyAvailableException {
    private static final long serialVersionUID = 1L;

    /**
     * @param message    the error message
     * @param retryAfter how long to wait before retrying
     */
    public AllKeysBusyException(String message, Duration retryAfter) {
        super(503, "all_keys_busy", message, retryAfter);
    }
}
