package dev.acnologia;

/** How a leased key behaved. */
public enum Outcome {
    /** The call worked. */
    OK("ok"),
    /** The provider rate limited the key; it cools down for the retry after. */
    RATE_LIMITED("rate_limited"),
    /** The key used up its quota. */
    QUOTA_EXHAUSTED("quota_exhausted"),
    /** The provider rejected the key. */
    INVALID("invalid"),
    /** Something else went wrong; the key is not blamed for long. */
    ERROR("error"),
    /** The account is at its concurrency cap; the key steps aside briefly. */
    BUSY("busy"),
    /** The provider suspended the account; the key leaves rotation. */
    BANNED("banned");

    private final String wire;

    Outcome(String wire) {
        this.wire = wire;
    }

    /** @return the value sent to the server, such as {@code rate_limited} */
    public String wireValue() {
        return wire;
    }

    @Override
    public String toString() {
        return wire;
    }
}
