package dev.acnologia;

/**
 * Base class for every exception the SDK throws. It is unchecked so it can
 * cross lambdas and futures without wrapping.
 *
 * <p>Network failures (connection refused, timeouts, interrupted calls) are
 * thrown as a plain {@code AcnologiaException} with the original error as the
 * cause. Answers from the server are {@link ApiException} or a subclass, and
 * the rotation signals you throw from {@code withKey} are
 * {@link KeyRotationException} subclasses.
 */
public class AcnologiaException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    /**
     * Creates an exception with a message.
     *
     * @param message what went wrong
     */
    public AcnologiaException(String message) {
        super(message);
    }

    /**
     * Creates an exception with a message and a cause.
     *
     * @param message what went wrong
     * @param cause   the underlying error
     */
    public AcnologiaException(String message, Throwable cause) {
        super(message, cause);
    }
}
