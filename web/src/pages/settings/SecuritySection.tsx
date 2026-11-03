import { RiShieldCheckLine, RiTeamLine, RiUser3Line } from "@remixicon/react";
import { api } from "@/lib/api";
import { formatUnlockTtl, plural } from "@/lib/format";
import { ops } from "@/lib/ops";
import { envOf, type SettingsSummary } from "@/lib/settings";
import { Chip } from "@/components/base/badges/chip";
import { useLoad, type Loaded } from "./hooks";
import { EnvValue, EnvValueSkeleton, LinkRow, ReadOnlyNote, SectionError, SettingRow, SettingsCard, SettingsSection, countSummary } from "./parts";

/** "true" and "false" from the server, in words. */
export const onOff = (v: string) => <span className="font-sans text-body-2-medium">{v === "true" ? "On" : "Off"}</span>;

function AccessPasswordValue({ summary }: { summary: SettingsSummary | null }) {
  if (!summary) return <EnvValueSkeleton />;
  const env = envOf(summary, "ACNOLOGIA_ACCESS_PASSWORD");
  const on = summary.access_gate_on;
  return (
    <div className="flex flex-col gap-1 sm:items-end">
      <Chip variant="caption" color={on ? "lime" : "yellow"} className="w-fit">
        {on ? "On" : "Off"}
      </Chip>
      <span className="text-caption-1-regular text-text-tertiary">
        {env?.set ? (
          <>
            Set by <code className="font-mono wrap-anywhere">ACNOLOGIA_ACCESS_PASSWORD</code>
          </>
        ) : on ? (
          "Generated, stored in access-password in the data directory"
        ) : (
          <>
            <code className="font-mono wrap-anywhere">ACNOLOGIA_ACCESS_PASSWORD</code> not set
          </>
        )}
      </span>
    </div>
  );
}

/**
 * Security and access: the posture the environment sets (read only for
 * everyone, visible to admins), then the pages that manage people and tokens.
 */
export function SecuritySection({ isAdmin, isSuper, summary }: { isAdmin: boolean; isSuper: boolean; summary: Loaded<SettingsSummary> }) {
  const users = useLoad(api.listUsers, isSuper);
  const tokens = useLoad(() => ops.listTokens(), true);
  const env = (name: string) => envOf(summary.data, name);

  let posture;
  if (!isAdmin) {
    posture = (
      <SettingsCard>
        <SettingRow label="Access and cookies" description="The access password, cookie and proxy settings this server runs with.">
          <ReadOnlyNote>Only admins can see how this server is secured.</ReadOnlyNote>
        </SettingRow>
      </SettingsCard>
    );
  } else if (summary.error) {
    posture = <SectionError what="the security settings" message={summary.error} onRetry={summary.reload} />;
  } else {
    posture = (
      <SettingsCard title="Set by the environment. Change them in .env and restart.">
        <SettingRow label="Access password" description="One shared password in front of the whole site, before any account.">
          <AccessPasswordValue summary={summary.data} />
        </SettingRow>
        <SettingRow label="Require an access password" description="Refuse to start without one, so a public deploy never comes up open.">
          <EnvValue env={env("ACNOLOGIA_REQUIRE_ACCESS_PASSWORD")} format={onOff} />
        </SettingRow>
        <SettingRow label="Password is the whole login" description="Past the lock screen, a browser is signed in as the owner.">
          <EnvValue env={env("ACNOLOGIA_ACCESS_SIGNS_IN")} format={onOff} />
        </SettingRow>
        <SettingRow label="Stays unlocked for" description="How long a browser stays past the lock screen before it asks again.">
          <EnvValue env={env("ACNOLOGIA_ACCESS_TTL")} format={(v) => formatUnlockTtl(Number(v))} unsetLabel="Default: 30 days" />
        </SettingRow>
        <SettingRow label="Secure cookies" description="Send cookies over HTTPS only. Turn on behind a TLS proxy.">
          <EnvValue env={env("ACNOLOGIA_SECURE_COOKIES")} format={onOff} unsetLabel="On anyway for HTTPS requests" />
        </SettingRow>
        <SettingRow label="Trust proxy headers" description="Read the client IP from X-Forwarded-For. Only behind a proxy that overwrites it.">
          <EnvValue env={env("ACNOLOGIA_TRUST_PROXY_HEADERS")} format={onOff} />
        </SettingRow>
        <SettingRow label="Webhooks to private addresses" description="Let webhooks reach private and loopback addresses.">
          <EnvValue env={env("ACNOLOGIA_WEBHOOK_ALLOW_PRIVATE")} format={onOff} />
        </SettingRow>
      </SettingsCard>
    );
  }

  return (
    <SettingsSection id="security" title="Security and access" description="How this server is locked down, and who can sign in to it.">
      {posture}
      <SettingsCard>
        {isSuper && (
          <LinkRow
            href="/users"
            icon={RiTeamLine}
            label="Users"
            description="Invite people and set their roles."
            summary={countSummary(users, (d) => plural(d.users.length, "user"))}
          />
        )}
        {isSuper && <LinkRow href="/audit" icon={RiShieldCheckLine} label="Audit log" description="Every change made on this server, and by whom." summary="" />}
        <LinkRow
          href="/account"
          icon={RiUser3Line}
          label="Your account and API tokens"
          description="Your name, password and the tokens the CLI and scripts use."
          summary={countSummary(tokens, (d) => plural(d.tokens.length, "token"))}
        />
      </SettingsCard>
      {!isSuper && <ReadOnlyNote>Users and the audit log are for superadmins.</ReadOnlyNote>}
    </SettingsSection>
  );
}
