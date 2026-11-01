import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// COGITO_BACKEND points the dev server at another instance, for example a review copy.
const env = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {};
const backend = env.COGITO_BACKEND || "http://localhost:8080";
// "/api" covers /api/docs. Keys starting with ^ are regular expressions: the raw
// docs (/docs/<slug>.md) and their images come from the backend, while /docs
// and /docs/<slug> stay with the SPA.
const proxied = ["/api", "/proxy", "/mcp", "/healthz", "/llms.txt", "/llms-full.txt", "^/docs/[^/]+\\.md$", "^/docs/assets/"];

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // "@/..." resolves to web/src, matching the paths entry in tsconfig.json (BoardUI imports use it).
  resolve: {
    alias: { "@": decodeURIComponent(new URL("./src", import.meta.url).pathname) },
  },
  build: {
    outDir: "../server/internal/webui/dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: Object.fromEntries(proxied.map((p) => [p, { target: backend, changeOrigin: true }])),
  },
});
