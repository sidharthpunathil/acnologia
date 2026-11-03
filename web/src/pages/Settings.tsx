import { useCallback, useMemo, useState } from "react";
import { RiEqualizerLine, RiPaletteLine, RiPlugLine, RiServerLine, RiShieldKeyholeLine, RiSparklingLine } from "@remixicon/react";
import { can } from "@/lib/perms";
import { settingsApi } from "@/lib/settings";
import { useAuth } from "@/stores/auth";
import { useTitle } from "@/hooks/useTitle";
import { PageHeader } from "@/components/acn/page-header";
import { AiSection } from "./settings/AiSection";
import { useLoad, useUnsavedWarning } from "./settings/hooks";
import { AdvancedSection, AppearanceSection, IntegrationsSection } from "./settings/MoreSections";
import { SettingsNav, useActiveSection, useHashLanding, type SettingsNavItem } from "./settings/nav";
import { SecuritySection } from "./settings/SecuritySection";
import { ServerSection } from "./settings/ServerSection";

/**
 * One page of sections with an in page nav, rather than a sub route per
 * section: every section is short enough to scan in one scroll, the browser's
 * find works across all of them, and /settings#ai deep links still land on
 * one. A section that grows into its own editor can move to /settings/<id>
 * later without changing the others.
 */
export function SettingsPage() {
  useTitle("Settings");
  const role = useAuth((s) => s.user?.role);
  const isAdmin = can.manage(role);
  const isSuper = can.users(role);

  // The environment's configuration, shared by every section that shows part of it.
  const summary = useLoad(settingsApi.summary, isAdmin);

  // Unsaved edits per editable form, for the nav's dots and the leave warning.
  const [serverDirty, setServerDirty] = useState(false);
  const [rulesDirty, setRulesDirty] = useState(false);
  const onServerDirty = useCallback((d: boolean) => setServerDirty(d), []);
  const onRulesDirty = useCallback((d: boolean) => setRulesDirty(d), []);
  useUnsavedWarning(serverDirty || rulesDirty);

  const items: SettingsNavItem[] = useMemo(
    () => [
      { id: "general", label: "General", icon: RiServerLine, dirty: serverDirty },
      { id: "appearance", label: "Appearance", icon: RiPaletteLine },
      { id: "ai", label: "AI", icon: RiSparklingLine, dirty: rulesDirty },
      { id: "security", label: "Security and access", icon: RiShieldKeyholeLine },
      { id: "integrations", label: "Integrations", icon: RiPlugLine },
      { id: "advanced", label: "Advanced", icon: RiEqualizerLine },
    ],
    [serverDirty, rulesDirty],
  );
  const ids = items.map((i) => i.id);
  const active = useActiveSection(ids);
  useHashLanding(ids);

  return (
    <div>
      <PageHeader title="Settings" meta={isSuper ? "This server, appearance, AI and access" : `Signed in as ${role ?? "member"}: some settings are read only`} />
      <div className="lg:grid lg:grid-cols-[13rem_minmax(0,1fr)] lg:gap-10">
        <SettingsNav items={items} active={active} />
        <div className="flex min-w-0 flex-col gap-12">
          <ServerSection isAdmin={isAdmin} isSuper={isSuper} summary={summary} onDirty={onServerDirty} />
          <AppearanceSection />
          <AiSection isAdmin={isAdmin} isSuper={isSuper} summary={summary} onDirty={onRulesDirty} />
          <SecuritySection isAdmin={isAdmin} isSuper={isSuper} summary={summary} />
          <IntegrationsSection isAdmin={isAdmin} />
          <AdvancedSection isAdmin={isAdmin} summary={summary} />
        </div>
      </div>
    </div>
  );
}
