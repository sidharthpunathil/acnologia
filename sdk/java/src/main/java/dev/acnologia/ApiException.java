package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;

import java.net.http.HttpHeaders;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;

/**
 * A non-2xx answer from the consumer API that has no more specific type.
 * Errors from the server look like
 * {@code {"error": {"code": "...", "message": "..."}}}. Codes worth checking
 * with {@link #code()} include {@code empty_group}, {@code bad_params},
 * {@code no_route}, {@code no_route_worked}, {@code proxy_not_allowed},
 * {@code lease_not_allowed} and {@code loop_detected}.
 */
public class ApiException extends AcnologiaException {
    private static final long serialVersionUID = 1L;

    private final int statusCode;
    private final String code;
    private final String errorMessage;
    private final Duration retryAfter;

    /**
     * Creates an API error.
     *
     * @param statusCode   the HTTP status
     * @param code         the error code from the body, or an empty string
     * @param errorMessage the error message from the body
     */
    public ApiException(int statusCode, String code, String errorMessage) {
        this(statusCode, code, errorMessage, Duration.ZERO);
    }

    /**
     * Creates an API error with a wait.
     *
     * @param statusCode   the HTTP status
     * @param code         the error code from the body, or an empty string
     * @param errorMessage the error message from the body
     * @param retryAfter   how long to wait before retrying, zero if unknown
     */
    public ApiException(int statusCode, String code, String errorMessage, Duration retryAfter) {
        super(format(statusCode, code, errorMessage));
        this.statusCode = statusCode;
        this.code = code == null ? "" : code;
        this.errorMessage = errorMessage == null ? "" : errorMessage;
        this.retryAfter = retryAfter == null ? Duration.ZERO : retryAfter;
    }

    /**
     * @return how long to wait before retrying, from {@code retry_after_sec}
     *         or the {@code Retry-After} header; {@link Duration#ZERO} if unknown
     */
    public Duration retryAfter() {
        return retryAfter;
    }

    /** @return the HTTP status code */
    public int statusCode() {
        return statusCode;
    }

    /** @return the error code, such as {@code lease_not_found}, or an empty string */
    public String code() {
        return code;
    }

    /** @return the error message sent by the server */
    public String errorMessage() {
        return errorMessage;
    }

    private static String format(int status, String code, String message) {
        if (code != null && !code.isEmpty()) {
            return "acnologia: http " + status + ": " + code + ": " + message;
        }
        return "acnologia: http " + status + ": " + message;
    }

    /**
     * Turns an error answer from Acnologia into the matching exception. The SDK
     * calls this for every consumer API call. You can call it yourself on a
     * proxy response that Acnologia produced rather than the provider (one
     * without an {@code X-Acnologia-Key-Id} header).
     *
     * @param status the HTTP status
     * @param body   the response body, may be null
     * @return the typed exception (never thrown by this method)
     */
    public static ApiException fromResponse(int status, byte[] body) {
        return fromResponse(status, body, null);
    }

    /**
     * Turns a proxy (or API) response that Acnologia produced into the
     * matching exception, reading {@code Retry-After} and
     * {@code X-Acnologia-Share} from its headers.
     *
     * @param response the response
     * @return the typed exception (never thrown by this method)
     */
    public static ApiException fromResponse(HttpResponse<byte[]> response) {
        return fromResponse(response.statusCode(), response.body(), response.headers());
    }

    /**
     * Like {@link #fromResponse(int, byte[])}, also reading
     * {@code Retry-After} and {@code X-Acnologia-Share} from the headers.
     *
     * @param status  the HTTP status
     * @param body    the response body, may be null
     * @param headers the response headers, may be null
     * @return the typed exception (never thrown by this method)
     */
    public static ApiException fromResponse(int status, byte[] body, HttpHeaders headers) {
        String code = "";
        String message = "";
        double retryAfterSec = 0;
        String limit = "";
        Instant resetAt = null;
        String text = body == null ? "" : new String(body, StandardCharsets.UTF_8);
        try {
            JsonNode root = Acnologia.JSON.readTree(text);
            if (root != null && root.isObject()) {
                JsonNode err = root.path("error");
                code = err.path("code").asText("");
                message = err.path("message").asText("");
                retryAfterSec = root.path("retry_after_sec").asDouble(0);
                limit = root.path("limit").asText("");
                resetAt = Lease.parseTime(root.path("reset_at").asText(""));
            }
        } catch (Exception ignored) {
            // not JSON, fall through to the raw body
        }
        if (retryAfterSec <= 0 && headers != null) {
            try {
                retryAfterSec = Double.parseDouble(headers.firstValue("Retry-After").orElse("0").trim());
            } catch (NumberFormatException ignored) {
                // an HTTP date; leave it unknown
            }
        }
        Duration retryAfter = Duration.ofMillis(Math.round(Math.max(retryAfterSec, 0) * 1000));
        if (code.equals("app_limit_reached")) {
            return new AppLimitReachedException(message, limit, resetAt, retryAfter);
        }
        if (code.startsWith("share_") && status == 403) {
            String share = headers == null ? "" : headers.firstValue("X-Acnologia-Share").orElse("");
            if (share.isEmpty()) {
                share = code.substring("share_".length());
            }
            return new ShareUnavailableException(code, message, share);
        }

        if (code.equals("no_key_available") || code.equals("all_keys_busy") || (status == 503 && code.isEmpty())) {
            if (code.equals("all_keys_busy")) {
                return new AllKeysBusyException(message, retryAfter);
            }
            return new NoKeyAvailableException(status, "no_key_available", message, retryAfter);
        }
        if (code.equals("service_unavailable")) {
            return new ServiceUnavailableException(status, message, retryAfter);
        }
        if (message.isEmpty() && code.isEmpty()) {
            message = text.trim();
            if (message.length() > 200) {
                message = message.substring(0, 200);
            }
            if (message.isEmpty()) {
                message = "HTTP " + status;
            }
        }
        if (status == 401) {
            return new UnauthenticatedException(code, message);
        }
        if (status == 403) {
            return new ForbiddenException(code, message);
        }
        return new ApiException(status, code, message, retryAfter);
    }
}
