// Writes .br and .gz siblings next to the hashed text files of a build, so
// the server can send them compressed without compressing per request
// (server/internal/api/server.go, serveWeb). Only files the server knows a
// type for get them, and only when compression saves at least a tenth.
//
// usage: node scripts/precompress.mjs [assets dir]
import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { brotliCompressSync, constants, gzipSync } from "node:zlib";

const dir = process.argv[2] ?? fileURLToPath(new URL("../../server/internal/webui/dist/assets", import.meta.url));
// Keep in step with compressibleType in server.go.
const text = /\.(m?js|css|svg|json|webmanifest|txt)$/;
const minBytes = 1024;

let files = 0;
let raw = 0;
let brTotal = 0;
let gzTotal = 0;
for (const name of readdirSync(dir, { recursive: true })) {
  const path = join(dir, String(name));
  if (!text.test(path) || !statSync(path).isFile()) continue;
  const body = readFileSync(path);
  if (body.length < minBytes) continue;
  const br = brotliCompressSync(body, {
    params: {
      [constants.BROTLI_PARAM_QUALITY]: constants.BROTLI_MAX_QUALITY,
      [constants.BROTLI_PARAM_MODE]: constants.BROTLI_MODE_TEXT,
      [constants.BROTLI_PARAM_SIZE_HINT]: body.length,
    },
  });
  const gz = gzipSync(body, { level: 9 });
  files++;
  raw += body.length;
  if (br.length <= body.length * 0.9) {
    writeFileSync(path + ".br", br);
    brTotal += br.length;
  }
  if (gz.length <= body.length * 0.9) {
    writeFileSync(path + ".gz", gz);
    gzTotal += gz.length;
  }
}
const kb = (n) => `${Math.round(n / 1024)}KB`;
console.log(`precompress: ${files} files, ${kb(raw)} raw, ${kb(brTotal)} br, ${kb(gzTotal)} gz`);
