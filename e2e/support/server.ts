import { execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { BIN } from "./paths";

// Ports other local tools already use. Never bind these.
const RESERVED = new Set([8080, 8090, 5199, 18181, 18282, 18383]);

async function freePort(): Promise<number> {
  for (let i = 0; i < 20; i++) {
    const port = await new Promise<number>((resolve, reject) => {
      const srv = net.createServer();
      srv.once("error", reject);
      srv.listen(0, "127.0.0.1", () => {
        const { port } = srv.address() as net.AddressInfo;
        srv.close(() => resolve(port));
      });
    });
    if (!RESERVED.has(port)) return port;
  }
  throw new Error("no free port found");
}

export interface Started {
  pid: number;
  dataDir: string;
  baseURL: string;
}

/** Starts bin/cogito on a free port with an empty data dir and waits for /healthz. */
export async function startServer(): Promise<Started> {
  if (!fs.existsSync(BIN)) throw new Error(`${BIN} is missing. Run \`make build\` first.`);
  const masterKey = execFileSync(BIN, ["gen-master-key"], { encoding: "utf8" }).trim().split("\n")[0].trim();
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "cogito-e2e-"));
  const port = await freePort();
  const baseURL = `http://127.0.0.1:${port}`;
  const log = fs.openSync(path.join(dataDir, "server.log"), "a");
  // A minimal environment, so an ANTHROPIC_API_KEY or COGITO_* variable in the
  // developer's shell cannot change what the dashboard renders.
  const env = {
    PATH: process.env.PATH ?? "/usr/bin:/bin",
    HOME: process.env.HOME ?? dataDir,
    TZ: "UTC",
    COGITO_ADDR: `127.0.0.1:${port}`,
    COGITO_DATA_DIR: dataDir,
    COGITO_MASTER_KEY: masterKey,
    COGITO_HEALTH_INTERVAL: "off",
  };
  const child = spawn(BIN, [], { env, stdio: ["ignore", log, log], detached: true });
  child.unref();
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) break;
    try {
      const res = await fetch(`${baseURL}/healthz`);
      if (res.ok) return { pid: child.pid!, dataDir, baseURL };
    } catch {
      // Not listening yet.
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  const output = fs.readFileSync(path.join(dataDir, "server.log"), "utf8");
  stopServer(child.pid!, dataDir);
  throw new Error(`cogito did not come up on ${baseURL}. Log:\n${output}`);
}

/** Kills the server and removes its data dir. Safe to call twice. */
export function stopServer(pid: number, dataDir: string): void {
  try {
    process.kill(pid, "SIGTERM");
  } catch {
    // Already gone.
  }
  fs.rmSync(dataDir, { recursive: true, force: true });
}
