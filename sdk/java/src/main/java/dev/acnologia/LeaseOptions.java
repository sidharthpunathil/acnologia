package dev.acnologia;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Collection;
import java.util.List;

/** Optional parameters for {@link Acnologia#lease(String, LeaseOptions)}. Immutable. */
public final class LeaseOptions {
    /** No options: server defaults, fail fast when every key is busy. */
    public static final LeaseOptions DEFAULT = builder().build();

    private final List<Long> excludeKeyIds;
    private final Duration ttl;
    private final Duration wait;
    private final boolean exclusive;

    private LeaseOptions(Builder b) {
        this.excludeKeyIds = List.copyOf(b.excludeKeyIds);
        this.ttl = b.ttl;
        this.wait = b.wait;
        this.exclusive = b.exclusive;
    }

    /** @return a new builder */
    public static Builder builder() {
        return new Builder();
    }

    /** @return a builder that starts from these options */
    public Builder toBuilder() {
        Builder b = new Builder();
        b.excludeKeyIds.addAll(excludeKeyIds);
        b.ttl = ttl;
        b.wait = wait;
        b.exclusive = exclusive;
        return b;
    }

    /** @return key ids the server must not hand out */
    public List<Long> excludeKeyIds() {
        return excludeKeyIds;
    }

    /** @return the lease lifetime, {@link Duration#ZERO} for the server default */
    public Duration ttl() {
        return ttl;
    }

    /** @return how long the server may wait for a free slot, zero to fail fast */
    public Duration waitFor() {
        return wait;
    }

    /** @return true when the key is reserved for this lease alone */
    public boolean exclusive() {
        return exclusive;
    }

    /** Builds {@link LeaseOptions}. */
    public static final class Builder {
        private final List<Long> excludeKeyIds = new ArrayList<>();
        private Duration ttl = Duration.ZERO;
        private Duration wait = Duration.ZERO;
        private boolean exclusive;

        private Builder() {
        }

        /**
         * Keys the server must not return, for example keys you already tried.
         *
         * @param ids key ids
         * @return this builder
         */
        public Builder excludeKeyIds(Collection<Long> ids) {
            excludeKeyIds.clear();
            excludeKeyIds.addAll(ids);
            return this;
        }

        /**
         * Keys the server must not return.
         *
         * @param ids key ids
         * @return this builder
         */
        public Builder excludeKeyIds(long... ids) {
            excludeKeyIds.clear();
            for (long id : ids) {
                excludeKeyIds.add(id);
            }
            return this;
        }

        /**
         * Lease lifetime, sent as {@code ttl_sec} rounded up to whole seconds.
         * Zero uses the server default (120 seconds on services with a
         * concurrency limit, 300 otherwise).
         *
         * @param ttl the lifetime
         * @return this builder
         */
        public Builder ttl(Duration ttl) {
            this.ttl = ttl == null ? Duration.ZERO : ttl;
            return this;
        }

        /**
         * Lets the server wait up to this long (at most {@link Acnologia#MAX_WAIT})
         * for a free slot when every usable key is at its concurrency limit.
         * Zero fails fast with {@link AllKeysBusyException}. Sent as
         * {@code wait_sec}, rounded up to whole seconds.
         *
         * @param wait how long to wait
         * @return this builder
         */
        public Builder waitFor(Duration wait) {
            this.wait = wait == null ? Duration.ZERO : wait;
            return this;
        }

        /**
         * Keep the key from every other caller, leases and proxy alike, until
         * this lease is reported or expires.
         *
         * @param exclusive true to reserve the key
         * @return this builder
         */
        public Builder exclusive(boolean exclusive) {
            this.exclusive = exclusive;
            return this;
        }

        /** @return the options */
        public LeaseOptions build() {
            return new LeaseOptions(this);
        }
    }
}
