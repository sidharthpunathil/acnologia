// "Handed over to <peer>" and "Revealed to <label>" on the owner's key list and key page.
import { useEffect } from "react";
import { create } from "zustand";
import { RiEyeLine, RiKey2Line } from "@remixicon/react";
import { personLabel, sharing, shareMode } from "../lib/sharing";
import type { Key, KeyHandover, KeyReveal } from "../lib/types";
import { useAuth } from "../stores/auth";
import { can } from "../lib/perms";
import { Chip } from "@/components/base/badges/chip";
import { cx } from "@/utils/cx";

const TTL = 60e3;

interface HandoversStore {
  /** By key id, derived from the shares list. */
  byKey: Record<number, KeyHandover[]>;
  /** By key id: reveal shares that showed the key to a person. */
  revealedByKey: Record<number, KeyReveal[]>;
  at: number;
  loading: boolean;
  load: () => Promise<void>;
}

/**
 * Exposure records read from the shares list (share.handover.keys and
 * share.revealed_keys), cached for a minute. Used until keys carry
 * `handed_over` and `revealed_to` themselves.
 */
const useHandovers = create<HandoversStore>((set, get) => ({
  byKey: {},
  revealedByKey: {},
  at: 0,
  loading: false,
  load: async () => {
    const s = get();
    if (s.loading || Date.now() - s.at < TTL) return;
    set({ loading: true });
    try {
      const { shares } = await sharing.listShares();
      const byKey: Record<number, KeyHandover[]> = {};
      const revealedByKey: Record<number, KeyReveal[]> = {};
      for (const sh of shares ?? []) {
        const mode = shareMode(sh);
        if (mode === "reveal") {
          for (const k of sh.revealed_keys ?? []) {
            (revealedByKey[k.key_id] ??= []).push({ share_id: sh.id, share_name: sh.name, label: personLabel(sh), revealed_at: k.revealed_at ?? null });
          }
          continue;
        }
        if (mode !== "handover") continue;
        for (const k of sh.handover?.keys ?? []) {
          (byKey[k.key_id] ??= []).push({
            share_id: sh.id,
            peer_id: sh.peer_id,
            peer_name: sh.peer_name || sh.name,
            live: sh.status === "active" && !k.wiped_at,
            wiped_at: k.wiped_at ?? null,
          });
        }
      }
      set({ byKey, revealedByKey, at: Date.now(), loading: false });
    } catch {
      // Not allowed or not available: no chips, nothing else breaks.
      set({ at: Date.now(), loading: false });
    }
  },
}));

/** Loads the exposure records once for admins; call it on a page that lists keys. */
export function useLoadHandovers() {
  const role = useAuth((s) => s.user?.role);
  const load = useHandovers((s) => s.load);
  const ok = can.manage(role);
  useEffect(() => {
    if (ok) void load();
  }, [ok, load]);
}

/** The key's own records when the server sends them, else the ones from the shares list. */
function recordsFor(k: Key, byKey: Record<number, KeyHandover[]>): KeyHandover[] {
  return k.handed_over ?? byKey[k.id] ?? [];
}

/** "Handed over to <peer>" while live, "Was handed over to <peer>" after. Nothing for a key never handed over. */
export function HandoverChip({ k, className }: { k: Key; className?: string }) {
  const byKey = useHandovers((s) => s.byKey);
  const recs = recordsFor(k, byKey);
  if (!recs.length) return null;
  const live = recs.filter((r) => r.live);
  const shown = live.length ? live : recs;
  const names = [...new Set(shown.map((r) => r.peer_name || r.peer_id || "another server"))];
  const peer = names.length > 1 ? `${names[0]} and ${names.length - 1} more` : names[0];
  const text = live.length ? `Handed over to ${peer}` : `Was handed over to ${peer}`;
  return (
    <Chip
      variant="caption"
      color={live.length ? "orange" : "soft"}
      title={live.length ? "Another server holds a copy of this key." : "Another server held a copy of this key. Rotate it if you do not trust that server."}
      className={cx("max-w-full gap-1 rounded-full px-2 py-0", className)}
    >
      <RiKey2Line className="size-3 shrink-0" aria-hidden />
      <span className="min-w-0 truncate">{text}</span>
    </Chip>
  );
}

/** "Revealed to <label>": a person saw this key through a reveal share. It stays until the key is rotated. */
export function RevealedChip({ k, className }: { k: Key; className?: string }) {
  const byKey = useHandovers((s) => s.revealedByKey);
  const recs = k.revealed_to ?? byKey[k.id] ?? [];
  if (!recs.length) return null;
  const names = [...new Set(recs.map((r) => r.label || r.share_name || "a person"))];
  const who = names.length > 1 ? `${names[0]} and ${names.length - 1} more` : names[0];
  return (
    <Chip
      variant="caption"
      color="orange"
      title={`${names.join(", ")} saw this key. Rotate it at the provider if you no longer want them to use it.`}
      className={cx("max-w-full gap-1 rounded-full px-2 py-0", className)}
    >
      <RiEyeLine className="size-3 shrink-0" aria-hidden />
      <span className="min-w-0 truncate">Revealed to {who}</span>
    </Chip>
  );
}

/** Both exposure chips side by side, or nothing when the key never left this server. */
export function ExposureChips({ k, className }: { k: Key; className?: string }) {
  const handed = useHandovers((s) => s.byKey);
  const revealed = useHandovers((s) => s.revealedByKey);
  const any = recordsFor(k, handed).length > 0 || (k.revealed_to ?? revealed[k.id] ?? []).length > 0;
  if (!any) return null;
  return (
    <span className={cx("flex max-w-full flex-wrap gap-1", className)}>
      <HandoverChip k={k} />
      <RevealedChip k={k} />
    </span>
  );
}
