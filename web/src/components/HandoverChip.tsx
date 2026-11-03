// "Handed over to <peer>" on the owner's key list and key page.
import { useEffect } from "react";
import { create } from "zustand";
import { RiKey2Line } from "@remixicon/react";
import { sharing, shareMode } from "../lib/sharing";
import type { Key, KeyHandover } from "../lib/types";
import { useAuth } from "../stores/auth";
import { can } from "../lib/perms";
import { Chip } from "@/components/base/badges/chip";
import { cx } from "@/utils/cx";

const TTL = 60e3;

interface HandoversStore {
  /** By key id, derived from the shares list. */
  byKey: Record<number, KeyHandover[]>;
  at: number;
  loading: boolean;
  load: () => Promise<void>;
}

/**
 * Exposure records read from the shares list (share.handover.keys), cached
 * for a minute. Used until keys carry `handed_over` themselves.
 */
const useHandovers = create<HandoversStore>((set, get) => ({
  byKey: {},
  at: 0,
  loading: false,
  load: async () => {
    const s = get();
    if (s.loading || Date.now() - s.at < TTL) return;
    set({ loading: true });
    try {
      const { shares } = await sharing.listShares();
      const byKey: Record<number, KeyHandover[]> = {};
      for (const sh of shares ?? []) {
        if (shareMode(sh) !== "handover") continue;
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
      set({ byKey, at: Date.now(), loading: false });
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
