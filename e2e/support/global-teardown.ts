import fs from "node:fs";
import { STATE_FILE, type RunState } from "./paths";
import { stopServer } from "./server";

export default async function globalTeardown(): Promise<void> {
  if (!fs.existsSync(STATE_FILE)) return;
  const state = JSON.parse(fs.readFileSync(STATE_FILE, "utf8")) as RunState;
  stopServer(state.pid, state.dataDir);
  if (state.peer) stopServer(state.peer.pid, state.peer.dataDir);
  fs.rmSync(STATE_FILE, { force: true });
}
