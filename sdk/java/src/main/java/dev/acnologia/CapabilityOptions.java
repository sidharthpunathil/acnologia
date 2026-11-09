package dev.acnologia;

/** Optional parameters for {@link Acnologia#capability(String, Object, CapabilityOptions)}. Immutable. */
public final class CapabilityOptions {
    /** No options: the server picks the route and maps the answer. */
    public static final CapabilityOptions DEFAULT = builder().build();

    private final String service;
    private final boolean raw;
    private final String idempotencyKey;
    private final boolean noCache;

    private CapabilityOptions(Builder b) {
        this.service = b.service;
        this.raw = b.raw;
        this.idempotencyKey = b.idempotencyKey;
        this.noCache = b.noCache;
    }

    /** @return a new builder */
    public static Builder builder() {
        return new Builder();
    }

    /** @return the service to use, or null to let the server pick */
    public String service() {
        return service;
    }

    /** @return true to get the provider's own answer, not the mapped one */
    public boolean raw() {
        return raw;
    }

    /** @return the {@code Idempotency-Key} to send, or null */
    public String idempotencyKey() {
        return idempotencyKey;
    }

    /** @return true to send {@code Cache-Control: no-cache} */
    public boolean noCache() {
        return noCache;
    }

    /** Builds {@link CapabilityOptions}. */
    public static final class Builder {
        private String service;
        private boolean raw;
        private String idempotencyKey;
        private boolean noCache;

        private Builder() {
        }

        /**
         * @param service a service slug; sent as {@code ?_service=}
         * @return this builder
         */
        public Builder service(String service) {
            this.service = service;
            return this;
        }

        /**
         * @param raw true to send {@code ?_raw=1}
         * @return this builder
         */
        public Builder raw(boolean raw) {
            this.raw = raw;
            return this;
        }

        /**
         * @param key sent as {@code Idempotency-Key}, so a retry replays the first answer
         * @return this builder
         */
        public Builder idempotencyKey(String key) {
            this.idempotencyKey = key;
            return this;
        }

        /**
         * @param noCache true to skip Acnologia's response cache
         * @return this builder
         */
        public Builder noCache(boolean noCache) {
            this.noCache = noCache;
            return this;
        }

        /** @return the options */
        public CapabilityOptions build() {
            return new CapabilityOptions(this);
        }
    }
}
