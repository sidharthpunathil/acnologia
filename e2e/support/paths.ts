import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

export const E2E_DIR = path.resolve(here, "..");
export const REPO_DIR = path.resolve(E2E_DIR, "..");
export const BIN = process.env.ACNOLOGIA_BIN || path.join(REPO_DIR, "bin", "acnologia");
export const RUN_DIR = path.join(E2E_DIR, ".run");
export const STATE_FILE = path.join(RUN_DIR, "server.json");
export const AUTH_FILE = path.join(RUN_DIR, "admin.json");

export const ADMIN = { email: "demo@acnologia.dev", name: "Demo", password: "supersecret1" };

/** What global setup hands to the workers and to global teardown. */
export interface RunState {
  pid: number;
  dataDir: string;
  baseURL: string;
  /** Origin of the fake provider API the seeded services point at. */
  upstream: string;
  /** Epoch ms the page clock is frozen at: a minute after seeding started. */
  now: number;
  serviceIds: Record<string, number>;
  /** A second server that redeemed one of the seeded shares. */
  peer: { pid: number; dataDir: string };
}
