import { useEffect, useState } from "react";
import { api } from "./api";
import type { ForwardProxySetup } from "./types";

/*
 * The forward proxy: an app sets Acnologia as its HTTPS_PROXY and calls the
 * provider's own URL. Calls to a service's host get a pool key; HTTPS needs
 * the server's CA trusted.
 */

/** Where anyone can download the CA certificate apps trust. */
export const CA_PATH = "/api/v1/proxy/ca.pem";
/** The file name the snippets use for the downloaded CA. */
export const CA_FILE = "acnologia-ca.pem";

/**
 * The proxy URL with the app token as its password, for display:
 * http://app:<token>@host:port. Built here rather than taken from the
 * server's proxy_url_with_token, whose placeholder is percent encoded.
 */
export function proxyWithToken(proxyUrl: string, token: string): string {
  const m = /^([a-z][a-z0-9+.-]*:\/\/)(?:[^@/]*@)?(.*)$/i.exec(proxyUrl.trim());
  if (!m) return `http://app:${token}@${proxyUrl.trim()}`;
  return `${m[1]}app:${token}@${m[2].replace(/\/$/, "")}`;
}

/** The proxy address for a reader who cannot load the setup: this page's origin, which always takes proxy requests. */
export const fallbackProxyUrl = () => window.location.origin;

let cached: Promise<ForwardProxySetup> | null = null;

/** The server's forward proxy setup, loaded once per page load. Admins only; others get an error. */
export function loadForwardProxy(fresh = false): Promise<ForwardProxySetup> {
  if (!cached || fresh) {
    cached = api.forwardProxy();
    cached.catch(() => {
      cached = null;
    });
  }
  return cached;
}

/**
 * The forward proxy setup when `enabled`. `failed` is true once the load
 * fails (a member, or an older server), so callers fall back to the origin.
 */
export function useForwardProxy(enabled: boolean): { setup: ForwardProxySetup | null; failed: boolean } {
  const [setup, setSetup] = useState<ForwardProxySetup | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    loadForwardProxy()
      .then((s) => live && setSetup(s))
      .catch(() => live && setFailed(true));
    return () => {
      live = false;
    };
  }, [enabled]);
  return { setup, failed };
}
