import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// ACNOLOGIA_BACKEND points the dev server at another instance, for example a review copy.
// COGITO_BACKEND, its name from before the rename, still works.
const env = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {};
const backend = env.ACNOLOGIA_BACKEND || env.COGITO_BACKEND || "http://localhost:8080";
// "/api" covers /api/docs. Keys starting with ^ are regular expressions: the raw
// docs (/docs/<slug>.md) and their images come from the backend, while /docs
// and /docs/<slug> stay with the SPA.
const proxied = ["/api", "/proxy", "/mcp", "/healthz", "/llms.txt", "/llms-full.txt", "^/docs/[^/]+\\.md$", "^/docs/assets/"];

// web/src as a file path. On Windows a file URL's pathname is /C:/..., so the
// slash before the drive letter goes (Vite takes forward slashes there).
const srcDir = decodeURIComponent(new URL("./src", import.meta.url).pathname).replace(/^\/([A-Za-z]:\/)/, "$1");

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // "@/..." resolves to web/src, matching the paths entry in tsconfig.json (BoardUI imports use it).
  resolve: {
    alias: { "@": srcDir },
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
