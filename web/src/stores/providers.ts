import { create } from "zustand";
import { api } from "../lib/api";
import { normalizeProvider } from "../lib/providerSpec";
import type { LogoUpload, Provider, ProviderDetail, ProviderInput, ProviderSaveResult } from "../lib/types";

interface ProvidersStore {
  providers: Provider[];
  strategies: string[];
  loaded: boolean;
  error: string | null;
  details: Record<string, ProviderDetail>;
  fetch: () => Promise<Provider[]>;
  /** Fetch once per session unless forced. */
  ensure: () => Promise<Provider[]>;
  fetchOne: (slug: string) => Promise<ProviderDetail>;
  create: (input: ProviderInput) => Promise<ProviderSaveResult>;
  update: (slug: string, input: ProviderInput, note?: string) => Promise<ProviderSaveResult>;
  remove: (slug: string) => Promise<void>;
  rollback: (slug: string, version: number) => Promise<void>;
  setLogo: (slug: string, logo: LogoUpload) => Promise<void>;
  fetchLogo: (slug: string, url?: string) => Promise<void>;
  removeLogo: (slug: string) => Promise<void>;
  bySlug: (slug: string) => Provider | undefined;
}

let inflight: Promise<Provider[]> | null = null;

export const useProviders = create<ProvidersStore>((set, get) => {
  const put = (p: Provider) => {
    const list = get().providers;
    const n = normalizeProvider(p);
    set({ providers: list.some((x) => x.slug === n.slug) ? list.map((x) => (x.slug === n.slug ? n : x)) : [...list, n] });
  };

  const refreshOne = async (slug: string) => {
    await get().fetchOne(slug);
  };

  return {
    providers: [],
    strategies: [],
    loaded: false,
    error: null,
    details: {},

    fetch: async () => {
      try {
        const res = await api.listProviders();
        const providers = (res.providers ?? []).map(normalizeProvider);
        set({ providers, strategies: res.strategies ?? [], loaded: true, error: null });
        return providers;
      } catch (e) {
        set({ loaded: true, error: e instanceof Error ? e.message : "Failed to load providers" });
        throw e;
      }
    },

    ensure: async () => {
      if (get().loaded && !get().error) return get().providers;
      if (!inflight) inflight = get().fetch().finally(() => (inflight = null));
      return inflight;
    },

    fetchOne: async (slug) => {
      const d = await api.getProvider(slug);
      const n = { ...normalizeProvider(d), versions: d.versions ?? [] };
      set({ details: { ...get().details, [slug]: n } });
      put(n);
      return n;
    },

    create: async (input) => {
      const res = await api.createProvider(input);
      put(res.provider);
      return res;
    },

    update: async (slug, input, note) => {
      const res = await api.updateProvider(slug, { ...input, note: note ?? "" });
      put(res.provider);
      await refreshOne(slug).catch(() => undefined);
      return res;
    },

    remove: async (slug) => {
      await api.deleteProvider(slug);
      const { [slug]: _gone, ...rest } = get().details;
      set({ providers: get().providers.filter((p) => p.slug !== slug), details: rest });
    },

    rollback: async (slug, version) => {
      await api.rollbackProvider(slug, version);
      await refreshOne(slug);
    },

    setLogo: async (slug, logo) => {
      await api.setProviderLogo(slug, logo);
      bumpLogos();
      await refreshOne(slug);
    },

    fetchLogo: async (slug, url) => {
      await api.fetchProviderLogo(slug, url);
      bumpLogos();
      await refreshOne(slug);
    },

    removeLogo: async (slug) => {
      await api.deleteProviderLogo(slug);
      bumpLogos();
      await refreshOne(slug);
    },

    bySlug: (slug) => get().providers.find((p) => p.slug === slug),
  };
});

/**
 * Logo URLs are stable per provider or service, so a replaced image would stay
 * cached. Bumping this version appends ?v= to every logo request.
 */
export const useLogoVersion = create<{ v: number }>(() => ({ v: 0 }));
export function bumpLogos() {
  useLogoVersion.setState({ v: Date.now() });
}
