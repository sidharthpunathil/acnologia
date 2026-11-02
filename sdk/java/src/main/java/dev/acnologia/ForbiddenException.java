package dev.acnologia;

/**
 * HTTP 403: the app is disabled, or not allowed this service or mode. The code
 * is one of {@code unauthenticated}, {@code service_not_allowed},
 * {@code proxy_not_allowed} or {@code lease_not_allowed}.
 */
public class ForbiddenException extends ApiException {
    private static final long serialVersionUID = 1L;

    /**
     * @param code    the error code
     * @param message the error message
     */
    public ForbiddenException(String code, String message) {
        super(403, code, message);
    }
}
