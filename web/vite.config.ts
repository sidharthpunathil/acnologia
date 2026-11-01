import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const backend = "http://localhost:8080";
const proxied = ["/api", "/proxy", "/mcp", "/healthz", "/llms.txt"];

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
