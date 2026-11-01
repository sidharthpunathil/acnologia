import { create } from "zustand";

export type ThemePref = "system" | "light" | "dark";
export type Theme = "light" | "dark";

const STORAGE_KEY = "cogito.theme";
/** Matches the canvas color of each theme, for the browser and status bar. */
const THEME_COLOR: Record<Theme, string> = { light: "#eeede9", dark: "#151412" };

function readPref(): ThemePref {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === "light" || v === "dark" || v === "system") return v;
  } catch {
    /* storage blocked: fall back to the system setting */
  }
  return "system";
}

function writePref(p: ThemePref) {
  try {
    if (p === "system") localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, p);
  } catch {
    /* storage blocked: the choice lasts for this page only */
  }
}

const media = typeof window !== "undefined" && window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;

function resolve(p: ThemePref): Theme {
  if (p === "system") return media?.matches ? "dark" : "light";
  return p;
}

function apply(t: Theme) {
  const root = document.documentElement;
  // data-theme is the original hook; BoardUI's tokens and dark: variant key off the .dark class.
  root.setAttribute("data-theme", t);
  root.classList.toggle("dark", t === "dark");
  document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]').forEach((m) => {
    m.removeAttribute("media");
    m.content = THEME_COLOR[t];
  });
  // BoardUI's ThemeToggle (components/application/theme) listens for this.
  window.dispatchEvent(new CustomEvent("boardui:theme-change", { detail: t }));
}

interface ThemeStore {
  pref: ThemePref;
  theme: Theme;
  setPref: (p: ThemePref) => void;
}

export const useTheme = create<ThemeStore>((set) => {
  const pref = readPref();
  const theme = resolve(pref);
  apply(theme);
  // Follow the OS while the preference is "system".
  media?.addEventListener?.("change", () => {
    const { pref: p } = useTheme.getState();
    if (p !== "system") return;
    const t = resolve(p);
    apply(t);
    set({ theme: t });
  });
  return {
    pref,
    theme,
    setPref: (p) => {
      writePref(p);
      const t = resolve(p);
      apply(t);
      set({ pref: p, theme: t });
    },
  };
});
