package dev.acnologia;

/**
 * The callback for {@link Acnologia#withKey}. It may throw a checked exception
 * of type {@code E}, which {@code withKey} rethrows unchanged.
 *
 * @param <T> the result type
 * @param <E> the checked exception the callback may throw
 */
@FunctionalInterface
public interface LeaseFunction<T, E extends Exception> {
    /**
     * Uses the leased key.
     *
     * @param lease the key; put it on your request with {@link Lease#apply}
     * @return your result
     * @throws E any error; throw a {@link KeyRotationException} subclass to rotate
     */
    T apply(Lease lease) throws E;
}
