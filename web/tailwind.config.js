/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        canvas: "#f3f3ef",
        ink: "#18181a",
        muted: "#6b6b66",
        faint: "#9a9a93",
        line: "#e6e5df",
      },
      fontFamily: {
        sans: ["Geist", "Inter", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["Geist Mono", "JetBrains Mono", "ui-monospace", "SFMono-Regular", "monospace"],
      },
      borderRadius: {
        card: "14px",
      },
      boxShadow: {
        card: "0 1px 2px rgba(20, 20, 15, 0.04), 0 0 0 1px rgba(20, 20, 15, 0.035)",
        pop: "0 12px 40px rgba(20, 20, 15, 0.14), 0 0 0 1px rgba(20, 20, 15, 0.06)",
      },
    },
  },
  plugins: [],
};
