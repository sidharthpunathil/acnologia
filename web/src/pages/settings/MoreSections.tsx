import { RiShareForwardLine, RiWebhookLine } from "@remixicon/react";
import { AcnAppToken, AcnEgress, AcnServiceGroup } from "@/components/foundations/icons/acn";
import { api } from "@/lib/api";
import { num, plural } from "@/lib/format";
import { ops } from "@/lib/ops";
import { envOf, type SettingsSummary } from "@/lib/settings";
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

/** Go's Duration text ("6h0m0s") without the zero tail ("6h"). */
function duration(v: string) {
  if (v === "0s") return "Off";
  return v.replace(/(\d[hm])0s$/, "$1").replace(/(\d)h0m$/, "$1h");
}

/** Housekeeping and size limits: environment only, shown to admins. */
export function AdvancedSection({ isAdmin, summary }: { isAdmin: boolean; summary: Loaded<SettingsSummary> }) {
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
      <SettingsCard title="Set by the environment. Change them in .env and restart.">
        <SettingRow label="Event retention" description="How long activity events are kept before they are deleted.">
          <EnvValue env={env("ACNOLOGIA_EVENT_RETENTION_DAYS")} format={(v) => plural(Number(v), "day")} unsetLabel="Default" />
        </SettingRow>
        <SettingRow label="Background health checks" description="How often every key is checked against its provider.">
          <EnvValue env={env("ACNOLOGIA_HEALTH_INTERVAL")} format={duration} unsetLabel="Default" />
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
    );
  }
  return (
    <SettingsSection id="advanced" title="Advanced" description="Housekeeping and size limits. Most servers never change these.">
      {body}
    </SettingsSection>
  );
}
