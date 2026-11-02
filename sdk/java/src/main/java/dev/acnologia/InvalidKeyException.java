package dev.acnologia;

/** The provider rejected the key. Throw it from a {@code withKey} callback to rotate. */
public class InvalidKeyException extends KeyRotationException {
    private static final long serialVersionUID = 1L;

    /** Invalid key, with no details. */
    public InvalidKeyException() {
        this(0, null, null);
    }

    /**
     * @param statusCode the provider's HTTP status, 0 if unknown
     * @param message    extra text for the report, may be null
     */
    public InvalidKeyException(int statusCode, String message) {
        this(statusCode, message, null);
    }

    /**
     * @param statusCode the provider's HTTP status, 0 if unknown
     * @param message    extra text for the report, may be null
     * @param cause      the underlying error, may be null
     */
    public InvalidKeyException(int statusCode, String message, Throwable cause) {
        super("acnologia: invalid key", statusCode, message, cause);
    }

    @Override
    public Outcome outcome() {
        return Outcome.INVALID;
    }
}
