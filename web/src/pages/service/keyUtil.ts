import type { EffectiveStatus, Key } from "../../lib/types";

/**
 * effective_status from the server is a snapshot. A cooling key whose
 * cooldown has passed is available again, so reflect that without a refetch.
 */
export function liveStatus(k: Key, now: number): EffectiveStatus {
  if (k.effective_status === "cooling" && k.cooldown_until && Date.parse(k.cooldown_until) <= now) return "active";
  return k.effective_status;
}

export function withLiveStatus(k: Key, now: number): Key {
  const s = liveStatus(k, now);
  return s === k.effective_status ? k : { ...k, effective_status: s };
}
