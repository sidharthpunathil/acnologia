import { RiShareForwardLine, RiWebhookLine } from "@remixicon/react";
import { AcnAppToken, AcnEgress, AcnServiceGroup } from "@/components/foundations/icons/acn";
import { api } from "@/lib/api";
import { num, plural } from "@/lib/format";
import { ops } from "@/lib/ops";
import { useState } from "react";
import { errorMessage } from "@/lib/api";
import { envOf, settingsApi, type HealthSettings, type SettingsSummary } from "@/lib/settings";
import { toast } from "@/stores/toast";
import { Select, SelectItem } from "@/components/base/select/select";
import { sharing } from "@/lib/sharing";
import { ThemePicker } from "@/components/acn/theme-picker";
import { useLoad, type Loaded } from "./hooks";
import { EnvValue, LinkRow, ReadOnlyNote, SectionError, SettingRow, SettingsCard, SettingsSection, countSummary } from "./parts";

export function AppearanceSection() {
  return (
    <SettingsSection id="appearance" title="Appearance" description="How the dashboard looks in this browser.">
      <SettingsCard>
        <SettingRow label="Theme" description="System follows your device. Saved in this browser only.">
          <ThemePicker className="sm:w-80" />
        </SettingRow>
      </SettingsCard>
    </SettingsSection>
  );
}

/** Shares this server gives and links to shares from other servers. */
async function loadSharing() {
  const [s, p] = await Promise.all([sharing.listShares(), sharing.listPeers()]);
  const live = (st: string) => st === "active" || st === "pending" || st === "paused";
  return { shares: s.shares.filter((x) => live(x.status)).length, links: p.peers.filter((x) => x.status !== "revoked").length };
}

/** Links to the pages that manage what this server talks to, each with a live count. */
export function IntegrationsSection({ isAdmin }: { isAdmin: boolean }) {
  const webhooks = useLoad(ops.listWebhooks);
  const egress = useLoad(ops.listEgress);
  const groups = useLoad(ops.listGroups);
  const apps = useLoad(api.listApps);
  const shared = useLoad(loadSharing, isAdmin);

  return (
    <SettingsSection id="integrations" title="Integrations" description="What this server sends events to, routes through and shares. Each one is managed on its own page.">
      <SettingsCard>
        <LinkRow
          href="/webhooks"
          icon={RiWebhookLine}
          label="Webhooks"
          description="Post key and quota events to Slack, Discord, Telegram or any URL."
          summary={countSummary(webhooks, (d) => plural(d.webhooks.length, "webhook"))}
        />
        <LinkRow
          href="/egress"
          icon={AcnEgress}
          label="Egress proxies"
          description="Send a service's or a key's upstream calls out through a proxy."
          summary={countSummary(egress, (d) => plural(d.proxies.length, "proxy", "proxies"))}
        />
        <LinkRow
          href="/apps"
          icon={AcnAppToken}
          label="Apps and app tokens"
          description="The callers that lease keys, with their limits and MCP access."
          summary={countSummary(apps, (d) => plural(d.apps.length, "app"))}
        />
        <LinkRow
          href="/groups"
          icon={AcnServiceGroup}
          label="Service groups"
          description="One slug that falls back across several services in order."
          summary={countSummary(groups, (d) => plural(d.groups.length, "group"))}
        />
        {isAdmin && (
          <LinkRow
            href="/sharing"
            icon={RiShareForwardLine}
            label="Sharing between servers"
            description="Share services with other Acnologia servers. Keys never leave the server that owns them."
            summary={countSummary(shared, (d) => `${plural(d.shares, "share")} · ${plural(d.links, "link")}`)}
          />
        )}
      </SettingsCard>
      {!isAdmin && <ReadOnlyNote>Sharing between servers is for admins.</ReadOnlyNote>}
    </SettingsSection>
  );
}

/** "every 6 hours", "every 15 minutes", "off". */
const INTERVAL_WORDS: Record<string, string> = {
  off: "Off",
  "15m": "Every 15 minutes",
  "30m": "Every 30 minutes",
  "1h": "Every hour",
  "6h": "Every 6 hours",
  "12h": "Every 12 hours",
  "24h": "Every day",
  "7d": "Every week",
};

function intervalWords(id: string): string {
  return INTERVAL_WORDS[id] ?? (id === "off" ? "Off" : `Every ${id}`);
}

/** The env choice in the picker: drop the saved interval. */
const USE_ENV = "env";

/**
 * How often every key is checked against its provider. A superadmin picks it
 * here and it applies within a minute; "Use the environment" goes back to
 * ACNOLOGIA_HEALTH_INTERVAL. Admins see it read only.
 */
function HealthChecksRow({ isSuper }: { isSuper: boolean }) {
  const load = useLoad(settingsApi.health);
  const [busy, setBusy] = useState(false);
  const [local, setLocal] = useState<HealthSettings | null>(null);
  const h = local ?? load.data;

  const description = "How often every key is checked against its provider, with one cheap request each. While checks are on, out of quota keys are rechecked at least hourly.";
  if (load.error) {
    return (
      <SettingRow label="Key health checks" description={description}>
        <span className="text-caption-1-regular text-text-error-primary">{load.error}</span>
      </SettingRow>
    );
  }
  if (!h) {
    return (
      <SettingRow label="Key health checks" description={description}>
        <EnvValue env={null} />
      </SettingRow>
    );
  }
  const where =
    h.source === "saved" ? (
      <>
        Set here. <code className="font-mono wrap-anywhere">ACNOLOGIA_HEALTH_INTERVAL</code> says {intervalWords(h.env).toLowerCase()}
      </>
    ) : (
      <>
        From <code className="font-mono wrap-anywhere">ACNOLOGIA_HEALTH_INTERVAL</code>
      </>
    );

  if (!isSuper) {
    return (
      <SettingRow label="Key health checks" description={description}>
        <div className="flex flex-col gap-1 sm:items-end">
          <span className="text-body-2-medium text-text-primary">{intervalWords(h.interval)}</span>
          <span className="text-caption-1-regular text-text-tertiary">{where}. Only a superadmin can change it.</span>
        </div>
      </SettingRow>
    );
  }

  const choose = async (key: string) => {
    const next = key === USE_ENV ? "" : key;
    if ((h.source === "env" && next === "") || (h.source === "saved" && next === h.saved)) return;
    setBusy(true);
    try {
      const r = await settingsApi.setHealth(next);
      setLocal(r);
      toast.success(r.interval === "off" ? "Background health checks are off" : `Health checks now run ${intervalWords(r.interval).toLowerCase()}`);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingRow label="Key health checks" description={description}>
      <div className="flex w-full flex-col gap-1 sm:w-64 sm:items-end">
        <Select
          aria-label="Key health checks"
          selectedKey={h.source === "saved" ? h.saved : USE_ENV}
          onSelectionChange={(k) => k != null && void choose(String(k))}
          isDisabled={busy}
          className="w-full"
          popoverClassName="w-(--trigger-width)"
        >
          {[
            <SelectItem key={USE_ENV} id={USE_ENV} textValue={`Use the environment (${intervalWords(h.env)})`}>
              Use the environment ({intervalWords(h.env).toLowerCase()})
            </SelectItem>,
            ...h.choices.map((c) => (
              <SelectItem key={c} id={c} textValue={intervalWords(c)}>
                {intervalWords(c)}
              </SelectItem>
            )),
          ]}
        </Select>
        <span className="text-caption-1-regular text-text-tertiary">{where}. Applies within a minute.</span>
      </div>
    </SettingRow>
  );
}

/** Housekeeping and size limits, shown to admins. Health checks can be changed here; the rest is environment only. */
export function AdvancedSection({ isAdmin, isSuper, summary }: { isAdmin: boolean; isSuper: boolean; summary: Loaded<SettingsSummary> }) {
  const env = (name: string) => envOf(summary.data, name);
  let body;
  if (!isAdmin) {
    body = (
      <SettingsCard>
        <SettingRow label="Limits and housekeeping" description="Event retention, health checks, request size and cache.">
          <ReadOnlyNote>Only admins can see these.</ReadOnlyNote>
        </SettingRow>
      </SettingsCard>
    );
  } else if (summary.error) {
    body = <SectionError what="the server limits" message={summary.error} onRetry={summary.reload} />;
  } else {
    body = (
      <>
      <SettingsCard>
        <HealthChecksRow isSuper={isSuper} />
      </SettingsCard>
      <SettingsCard title="Set by the environment. Change them in .env and restart.">
        <SettingRow label="Event retention" description="How long activity events are kept before they are deleted.">
          <EnvValue env={env("ACNOLOGIA_EVENT_RETENTION_DAYS")} format={(v) => plural(Number(v), "day")} unsetLabel="Default" />
        </SettingRow>
        <SettingRow label="Largest request body" description="Bigger proxied requests are refused.">
          <EnvValue env={env("ACNOLOGIA_MAX_BODY_MB")} format={(v) => `${num(Number(v))} MB`} unsetLabel="Default" />
        </SettingRow>
        <SettingRow label="Response cache" description="Memory for cached upstream responses. 0 turns caching off.">
          <EnvValue env={env("ACNOLOGIA_CACHE_MB")} format={(v) => (v === "0" ? "Off" : `${num(Number(v))} MB`)} unsetLabel="Default" />
        </SettingRow>
        <SettingRow label="Egress echo URL" description="Answers with the caller's IP when you test an egress proxy.">
          <EnvValue env={env("ACNOLOGIA_EGRESS_ECHO_URL")} format={(v) => <span dir="ltr">{v}</span>} unsetLabel="Default" />
        </SettingRow>
      </SettingsCard>
      </>
    );
  }
  return (
    <SettingsSection id="advanced" title="Advanced" description="Housekeeping and size limits. Most servers never change these.">
      {body}
    </SettingsSection>
  );
}
