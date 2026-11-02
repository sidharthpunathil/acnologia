package dev.acnologia;

/**
 * Base class for the exceptions that tell {@code withKey} to report the key
 * and retry with the next one. Throw a subclass from your callback, directly
 * or as the cause of another exception.
 */
public abstract class KeyRotationException extends AcnologiaException {
    private static final long serialVersionUID = 1L;

    private final int statusCode;
    private final String detail;

    /**
     * @param base       the base message, such as "acnologia: invalid key"
     * @param statusCode the provider's HTTP status, 0 if unknown
     * @param detail     extra text for the report, may be null
     * @param cause      the underlying error, may be null
     */
    protected KeyRotationException(String base, int statusCode, String detail, Throwable cause) {
        super(detail == null || detail.isEmpty() ? base : base + ": " + detail, cause);
        this.statusCode = statusCode;
        this.detail = detail == null ? "" : detail;
    }

    /** @return the outcome reported for the key */
    public abstract Outcome outcome();

    /** @return the provider's HTTP status, 0 if unknown */
    public int statusCode() {
        return statusCode;
    }

    /** @return the text sent as the report message (the secret is scrubbed first) */
    public String detail() {
        return detail;
    }

    Report.Builder toReport(String secret) {
        return Report.builder(outcome()).statusCode(statusCode).message(Acnologia.scrubMessage(detail, secret));
    }
}
