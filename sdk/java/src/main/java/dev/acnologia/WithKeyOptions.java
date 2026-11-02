package dev.acnologia;

import java.time.Duration;

/** Options for {@link Acnologia#withKey}. Immutable. */
public final class WithKeyOptions {
    /** Five attempts, server defaults for the lease. */
    public static final WithKeyOptions DEFAULT = builder().build();

    private final int maxAttempts;
    private final LeaseOptions leaseOptions;

    private WithKeyOptions(Builder b) {
        this.maxAttempts = b.maxAttempts;
        this.leaseOptions = b.lease.build();
    }

    /** @return a new builder */
    public static Builder builder() {
        return new Builder();
    }

    /** @return how many keys to try before giving up */
    public int maxAttempts() {
        return maxAttempts;
    }

    /** @return the options used for the first lease; TTL, wait and exclusive apply to every lease */
    public LeaseOptions leaseOptions() {
        return leaseOptions;
    }

    /** Builds {@link WithKeyOptions}. */
    public static final class Builder {
        private int maxAttempts = 5;
        private LeaseOptions.Builder lease = LeaseOptions.builder();

        private Builder() {
        }

        /**
         * How many keys to try before giving up (default 5). Values below 1 are ignored.
         *
         * @param n the attempt count
         * @return this builder
         */
        public Builder maxAttempts(int n) {
            if (n > 0) {
                maxAttempts = n;
            }
            return this;
        }

        /**
         * Options for the first lease (exclusions, TTL, wait, exclusive). TTL,
         * wait and exclusive also apply to every later lease.
         *
         * @param options the lease options
         * @return this builder
         */
        public Builder leaseOptions(LeaseOptions options) {
            lease = options.toBuilder();
            return this;
        }

        /**
         * How long the server may wait for a free slot on every lease, at most
         * {@link Acnologia#MAX_WAIT}.
         *
         * @param wait how long to wait
         * @return this builder
         */
        public Builder waitFor(Duration wait) {
            lease.waitFor(wait);
            return this;
        }

        /**
         * Lease lifetime for every lease.
         *
         * @param ttl the lifetime
         * @return this builder
         */
        public Builder ttl(Duration ttl) {
            lease.ttl(ttl);
            return this;
        }

        /** @return the options */
        public WithKeyOptions build() {
            return new WithKeyOptions(this);
        }
    }
}
