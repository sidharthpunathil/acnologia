/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        canvas: "#eeede9",
        ink: "#18181a",
        muted: "#6b6b66",
        faint: "#9a9a93",
        line: "#e6e5df",
        soft: "#fafaf7",
        brand: { DEFAULT: "#E80000", soft: "#fdecec", ink: "#b00000" },
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
        card: "0 1px 2px rgba(20, 20, 15, 0.04), 0 0 0 1px rgba(20, 20, 15, 0.035)",
        pop: "0 12px 40px rgba(20, 20, 15, 0.14), 0 0 0 1px rgba(20, 20, 15, 0.06)",
      },
    },
  },
  plugins: [],
  // Hover styles only on devices that can hover, so taps do not leave cards lifted.
  future: { hoverOnlyWhenSupported: true },
  // Some classes are built from data at runtime.
  safelist: ["animate-pulse"],
};
