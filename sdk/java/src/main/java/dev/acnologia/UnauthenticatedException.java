package dev.acnologia;

/** HTTP 401: the app token is missing or wrong. */
public class UnauthenticatedException extends ApiException {
    private static final long serialVersionUID = 1L;

    /**
     * @param code    the error code, usually {@code unauthenticated}
     * @param message the error message
     */
    public UnauthenticatedException(String code, String message) {
        super(401, code, message);
    }
}
