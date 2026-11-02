package dev.acnologia;

import java.time.Duration;

/**
 * How a leased key behaved. Set {@code rotate} to receive the next key in the
 * same round trip. Immutable; build one with {@link #builder(Outcome)} or the
 * shortcuts such as {@link #ok()} and {@link #rateLimited(Duration)}.
 */
public final class Report {
    private final Outcome outcome;
    private final int statusCode;
    private final Duration retryAfter;
    private final String message;
    private final boolean rotate;

    private Report(Builder b) {
        this.outcome = b.outcome == null ? Outcome.OK : b.outcome;
        this.statusCode = b.statusCode;
        this.retryAfter = b.retryAfter;
        this.message = b.message;
        this.rotate = b.rotate;
    }

    /**
     * @param outcome the outcome to report
     * @return a builder
     */
    public static Builder builder(Outcome outcome) {
        return new Builder(outcome);
    }

    /** @return an {@code ok} report */
    public static Report ok() {
        return builder(Outcome.OK).build();
    }

    /**
     * @param retryAfter how long the key should cool down, zero if unknown
     * @return a {@code rate_limited} report
     */
    public static Report rateLimited(Duration retryAfter) {
        return builder(Outcome.RATE_LIMITED).retryAfter(retryAfter).build();
    }

    /** @return a {@code quota_exhausted} report */
    public static Report quotaExhausted() {
        return builder(Outcome.QUOTA_EXHAUSTED).build();
    }

    /** @return an {@code invalid} report */
    public static Report invalid() {
        return builder(Outcome.INVALID).build();
    }

    /**
     * @param message what went wrong
     * @return an {@code error} report
     */
    public static Report error(String message) {
        return builder(Outcome.ERROR).message(message).build();
    }

    /** @return a builder that starts from this report */
    public Builder toBuilder() {
        return builder(outcome).statusCode(statusCode).retryAfter(retryAfter).message(message).rotate(rotate);
    }

    /** @return the outcome */
    public Outcome outcome() {
        return outcome;
    }

    /** @return the provider's HTTP status, 0 if not set */
    public int statusCode() {
        return statusCode;
    }

    /** @return the cooldown, {@link Duration#ZERO} if not set */
    public Duration retryAfter() {
        return retryAfter;
    }

    /** @return the message, empty if not set */
    public String message() {
        return message;
    }

    /** @return true when the next key should come back with the report */
    public boolean rotate() {
        return rotate;
    }

    @Override
    public String toString() {
        return "Report{outcome=" + outcome + ", statusCode=" + statusCode + ", retryAfter=" + retryAfter
                + ", rotate=" + rotate + "}";
    }

    /** Builds a {@link Report}. */
    public static final class Builder {
        private final Outcome outcome;
        private int statusCode;
        private Duration retryAfter = Duration.ZERO;
        private String message = "";
        private boolean rotate;

        private Builder(Outcome outcome) {
            this.outcome = outcome;
        }

        /**
         * @param statusCode the provider's HTTP status, 0 to omit
         * @return this builder
         */
        public Builder statusCode(int statusCode) {
            this.statusCode = statusCode;
            return this;
        }

        /**
         * How long the key should cool down, sent as {@code retry_after_sec}
         * rounded up to whole seconds.
         *
         * @param retryAfter the cooldown, zero to omit
         * @return this builder
         */
        public Builder retryAfter(Duration retryAfter) {
            this.retryAfter = retryAfter == null ? Duration.ZERO : retryAfter;
            return this;
        }

        /**
         * @param message a short note shown on the dashboard. Never include the secret.
         * @return this builder
         */
        public Builder message(String message) {
            this.message = message == null ? "" : message;
            return this;
        }

        /**
         * @param rotate true to receive the next key in the same call
         * @return this builder
         */
        public Builder rotate(boolean rotate) {
            this.rotate = rotate;
            return this;
        }

        /** @return the report */
        public Report build() {
            return new Report(this);
        }
    }
}
