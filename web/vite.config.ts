import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// COGITO_BACKEND points the dev server at another instance, for example a review copy.
const env = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {};
const backend = env.COGITO_BACKEND || "http://localhost:8080";
const proxied = ["/api", "/proxy", "/mcp", "/healthz", "/llms.txt", "/llms-full.txt"];

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../server/internal/webui/dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: Object.fromEntries(proxied.map((p) => [p, { target: backend, changeOrigin: true }])),
  },
});
