// The read only configuration the Settings page shows: what only the
// environment can change. Secrets come back as set or not set, never a value.
import { request } from "./api";

export interface EnvSetting {
  name: string;
  /** The variable has a value in the environment. */
  set: boolean;
  /** A secret: only `set` is known. */
  secret?: boolean;
  /** The value in effect: the default when the variable is unset. */
  value?: string;
}

export interface SettingsSummary {
  version: string;
  /** An access password locks the site, from the environment or the data dir. */
  access_gate_on: boolean;
  env: EnvSetting[];
}

export const settingsApi = {
  summary: () => request<SettingsSummary>("GET", "/api/admin/settings"),
};

/** The entry for `name`, or a placeholder when the server did not send it. */
export function envOf(summary: SettingsSummary | null, name: string): EnvSetting | null {
  if (!summary) return null;
  return summary.env.find((e) => e.name === name) ?? { name, set: false };
}
