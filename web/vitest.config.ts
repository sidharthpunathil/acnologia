import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Unit tests for the dashboard: `pnpm test`. Tests run in Node by default; a
// file that needs a DOM starts with `// @vitest-environment jsdom`.
const srcDir = decodeURIComponent(new URL("./src", import.meta.url).pathname).replace(/^\/([A-Za-z]:\/)/, "$1");

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { "@": srcDir },
  },
  test: {
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "node",
  },
});
