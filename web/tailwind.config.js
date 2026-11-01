import defaultColors from "tailwindcss/colors";
import plugin from "tailwindcss/plugin";

/*
 * Theme tokens. Every color is a CSS variable holding "r g b" so Tailwind's
 * opacity modifiers (bg-ink/40) keep working. Light values are the original
 * palette; dark values are a warm near-black with lighter surfaces.
 * The html element carries data-theme="light" or "dark" (set before first
 * paint by the inline script in index.html).
 */

const hex = (h) => {
  const n = parseInt(h.replace("#", ""), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
};
const rgb = (h) => hex(h).join(" ");
/** Mixes `a` over `b` at `t` (0..1) and returns "r g b". */
const mix = (a, b, t) => {
  const x = hex(a);
  const y = hex(b);
  return x.map((v, i) => Math.round(v * t + y[i] * (1 - t))).join(" ");
};

const tokens = {
  light: {
    canvas: "#eeede9",
    surface: "#ffffff",
    soft: "#fafaf7",
    well: "#ecebe5",
    line: "#e6e5df",
    "line-strong": "#d9d8d1",
    ink: "#18181a",
    "ink-strong": "#000000",
    "on-ink": "#ffffff",
    muted: "#6b6b66",
    faint: "#75756f",
    brand: "#e80000",
    "brand-soft": "#fdecec",
    "brand-ink": "#b00000",
    spark: "#ff6b6b",
    wordmark: "#e80000",
    "chart-ok": "#3f3f3c",
    "code-bg": "#1b1b1d",
    "code-ink": "#ececec",
  },
  dark: {
    canvas: "#151412",
    surface: "#1e1d1a",
    soft: "#252421",
    well: "#2c2a27",
    line: "#2f2d29",
    "line-strong": "#5e5b54",
    ink: "#ecebe6",
    "ink-strong": "#ffffff",
    "on-ink": "#151412",
    muted: "#aaa89f",
    faint: "#96948c",
    brand: "#ff5148",
    "brand-soft": "#3a1c19",
    "brand-ink": "#ff9a92",
    spark: "#ff6b6b",
    wordmark: "#ff4a40",
    "chart-ok": "#c9c7bf",
    "code-bg": "#0f0e0d",
    "code-ink": "#e6e4de",
  },
};

// Status hues used across the UI. Light keeps Tailwind's palette; dark flips
// the scale so tinted backgrounds stay dark and text shades stay light.
const HUES = ["red", "emerald", "amber", "sky", "violet", "orange", "stone"];
const SHADES = ["50", "100", "200", "300", "400", "500", "600", "700", "800", "900", "950"];

function hueVars(mode) {
  const out = {};
  const base = tokens.dark.surface;
  for (const h of HUES) {
    const p = defaultColors[h];
    for (const s of SHADES) {
      let v;
      if (mode === "light") v = rgb(p[s]);
      else {
        v = {
          50: mix(p[500], base, 0.13),
          100: mix(p[500], base, 0.2),
          200: mix(p[500], base, 0.32),
          300: mix(p[500], base, 0.45),
          400: rgb(p[400]),
          500: rgb(p[500]),
          // Solid fills get lighter in dark so dots and buttons stay visible on dark surfaces.
          600: rgb(p[400]),
          700: rgb(p[300]),
          800: rgb(p[200]),
          900: rgb(p[100]),
          950: rgb(p[50]),
        }[s];
      }
      out[`--${h}-${s}`] = v;
    }
  }
  return out;
}

function themeVars(mode) {
  const t = tokens[mode];
  const vars = {};
  for (const [k, v] of Object.entries(t)) vars[`--${k}`] = rgb(v);
  Object.assign(vars, hueVars(mode));
  if (mode === "light") {
    vars["--shadow-card"] = "0 1px 2px rgba(20, 20, 15, 0.04), 0 0 0 1px rgba(20, 20, 15, 0.035)";
    vars["--shadow-lift"] = "0 4px 14px rgba(20, 20, 15, 0.07), 0 0 0 1px rgba(20, 20, 15, 0.06)";
    vars["--shadow-pop"] = "0 12px 40px rgba(20, 20, 15, 0.14), 0 0 0 1px rgba(20, 20, 15, 0.06)";
    vars["--scrim"] = "rgba(24, 24, 26, 0.4)";
    vars["--logo-ring"] = "rgba(20, 20, 15, 0.09)";
    vars["--selection"] = "rgba(24, 24, 26, 0.12)";
    vars["--skeleton-a"] = "#ecebe5";
    vars["--skeleton-b"] = "#f4f3ee";
    vars["color-scheme"] = "light";
  } else {
    vars["--shadow-card"] = "0 1px 2px rgba(0, 0, 0, 0.3), 0 0 0 1px rgba(255, 255, 240, 0.06)";
    vars["--shadow-lift"] = "0 6px 18px rgba(0, 0, 0, 0.4), 0 0 0 1px rgba(255, 255, 240, 0.1)";
    vars["--shadow-pop"] = "0 16px 48px rgba(0, 0, 0, 0.55), 0 0 0 1px rgba(255, 255, 240, 0.08)";
    vars["--scrim"] = "rgba(0, 0, 0, 0.6)";
    vars["--logo-ring"] = "rgba(255, 255, 240, 0.1)";
    vars["--selection"] = "rgba(255, 81, 72, 0.3)";
    vars["--skeleton-a"] = "#25241f";
    vars["--skeleton-b"] = "#302e2a";
    vars["color-scheme"] = "dark";
  }
  return vars;
}

const v = (name) => `rgb(var(--${name}) / <alpha-value>)`;

const hueColors = Object.fromEntries(HUES.map((h) => [h, Object.fromEntries(SHADES.map((s) => [s, v(`${h}-${s}`)]))]));

/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      screens: {
        // Centered dialogs and side sheets: wide screens, except short touch screens
        // (phones in landscape), which keep bottom sheets. Mirrors SHEET_QUERY in Modal.tsx.
        dlg: { raw: "(min-width: 640px) and (hover: hover), (min-width: 640px) and (min-height: 501px)" },
      },
      colors: {
        canvas: v("canvas"),
        surface: v("surface"),
        soft: v("soft"),
        well: v("well"),
        line: { DEFAULT: v("line"), strong: v("line-strong") },
        ink: { DEFAULT: v("ink"), strong: v("ink-strong") },
        "on-ink": v("on-ink"),
        muted: v("muted"),
        faint: v("faint"),
        brand: { DEFAULT: v("brand"), soft: v("brand-soft"), ink: v("brand-ink") },
        spark: v("spark"),
        "chart-ok": v("chart-ok"),
        code: { bg: v("code-bg"), ink: v("code-ink") },
        ...hueColors,
      },
      fontFamily: {
        sans: ["Geist", "Inter", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["Geist Mono", "JetBrains Mono", "ui-monospace", "SFMono-Regular", "monospace"],
      },
      borderRadius: {
        card: "14px",
      },
      // Strong curves from the motion pass (plans/001-motion-pass.md).
      transitionTimingFunction: {
        out: "cubic-bezier(0.23, 1, 0.32, 1)",
        "in-out": "cubic-bezier(0.77, 0, 0.175, 1)",
      },
      boxShadow: {
        card: "var(--shadow-card)",
        lift: "var(--shadow-lift)",
        pop: "var(--shadow-pop)",
      },
    },
  },
  plugins: [
    plugin(({ addBase }) => {
      addBase({
        ":root": themeVars("light"),
        ':root[data-theme="dark"]': themeVars("dark"),
        // No script ran (or storage was blocked before it set the attribute): follow the OS.
        "@media (prefers-color-scheme: dark)": { ":root:not([data-theme])": themeVars("dark") },
      });
    }),
  ],
  // Hover styles only on devices that can hover, so taps do not leave cards lifted.
  future: { hoverOnlyWhenSupported: true },
  // Some classes are built from data at runtime.
  safelist: ["animate-pulse"],
};
