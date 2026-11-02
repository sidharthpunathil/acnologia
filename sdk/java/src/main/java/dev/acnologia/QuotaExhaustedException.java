package dev.acnologia;

/** The key has used up its quota. Throw it from a {@code withKey} callback to rotate. */
public class QuotaExhaustedException extends KeyRotationException {
    private static final long serialVersionUID = 1L;

    /** Quota exhausted, with no details. */
    public QuotaExhaustedException() {
        this(0, null, null);
    }

    /**
     * @param statusCode the provider's HTTP status, 0 if unknown
     * @param message    extra text for the report, may be null
     */
    public QuotaExhaustedException(int statusCode, String message) {
        this(statusCode, message, null);
    }

    /**
     * @param statusCode the provider's HTTP status, 0 if unknown
     * @param message    extra text for the report, may be null
     * @param cause      the underlying error, may be null
     */
    public QuotaExhaustedException(int statusCode, String message, Throwable cause) {
        super("acnologia: quota exhausted", statusCode, message, cause);
    }

    @Override
    public Outcome outcome() {
        return Outcome.QUOTA_EXHAUSTED;
    }
}
