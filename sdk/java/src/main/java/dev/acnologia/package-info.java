/**
 * Java client for Acnologia, a self-hosted API key pool.
 *
 * <p>Two ways to use pooled keys:
 * <ul>
 *   <li>Lease mode: {@link dev.acnologia.Acnologia#lease} a key, call the
 *       provider yourself, then {@link dev.acnologia.Lease#report} the
 *       outcome. {@link dev.acnologia.Acnologia#withKey} runs that loop and
 *       rotates keys for you.</li>
 *   <li>Proxy mode: send requests through {@link dev.acnologia.Proxy}.
 *       Acnologia injects a key and rotates on rate limits without ever
 *       exposing the secret.</li>
 * </ul>
 */
package dev.acnologia;
