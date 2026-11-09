package dev.acnologia;

/**
 * HTTP 403 {@code share_revoked}, {@code share_paused} or
 * {@code share_expired}: the token belongs to a share its owner revoked,
 * paused or let expire. It extends {@link ForbiddenException}, so existing
 * catches still match.
 */
public class ShareUnavailableException extends ForbiddenException {
    private static final long serialVersionUID = 1L;

    private final String shareStatus;

    /**
     * @param code        the error code
     * @param message     the error message
     * @param shareStatus {@code revoked}, {@code paused} or {@code expired}
     */
    public ShareUnavailableException(String code, String message, String shareStatus) {
        super(code, message);
        this.shareStatus = shareStatus == null ? "" : shareStatus;
    }

    /** @return {@code revoked}, {@code paused} or {@code expired} (also sent as {@code X-Acnologia-Share}) */
    public String shareStatus() {
        return shareStatus;
    }
}
