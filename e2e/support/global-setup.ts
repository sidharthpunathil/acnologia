import { request } from "@playwright/test";
import fs from "node:fs";
import { ADMIN, AUTH_FILE, RUN_DIR, STATE_FILE, type RunState } from "./paths";
import { seed } from "./seed";
import { startServer, stopServer } from "./server";
import { startUpstream } from "./upstream";

export default async function globalSetup(): Promise<() => void> {
  fs.mkdirSync(RUN_DIR, { recursive: true });
  // Page clocks freeze a minute after seeding starts, so "created 1m ago"
  // style text is stable no matter how long the run takes.
  const now = Date.now() + 60_000;
  const upstream = await startUpstream(now);
  const { pid, dataDir, baseURL } = await startServer();
  try {
    const serviceIds = await seed(baseURL, upstream.origin);

    // Sign in once; every test reuses this cookie.
    const api = await request.newContext({ baseURL });
    const res = await api.post("/api/auth/login", { data: { email: ADMIN.email, password: ADMIN.password } });
    if (!res.ok()) throw new Error(`login failed with ${res.status()}: ${await res.text()}`);
    await api.storageState({ path: AUTH_FILE });
    await api.dispose();

    const state: RunState = { pid, dataDir, baseURL, upstream: upstream.origin, now, serviceIds };
    fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));
  } catch (e) {
    stopServer(pid, dataDir);
    upstream.close();
    throw e;
  }
  // Runs after global teardown has stopped the server.
  return () => upstream.close();
}
