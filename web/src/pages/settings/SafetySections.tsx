import { useState } from "react";
import { RiArrowGoBackLine, RiDownload2Line } from "@remixicon/react";
import { errorMessage, safetyApi } from "@/lib/api";
import { plural, relTime } from "@/lib/format";
import type { TrashItem } from "@/lib/types";
import { toast } from "@/stores/toast";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { CodeBlock } from "@/components/acn/code-block";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { useLoad } from "./hooks";
import { EnvValueSkeleton, ReadOnlyNote, SectionError, SettingRow, SettingsCard, SettingsSection } from "./parts";

const KIND: Record<TrashItem["kind"], string> = { key: "Key", service: "Service", app: "App" };

function itemName(it: TrashItem): string {
  if (it.kind === "key") return it.service ? `${it.service} / ${it.name}` : it.name;
  if (it.kind === "service") return `${it.name} (${plural(it.keys ?? 0, "key")})`;
  return it.name;
}

/**
 * Deleted keys, services and apps wait here for 7 days. Restore brings one
 * back as it was; a superadmin can delete one for good sooner.
 */
export function TrashSection({ isAdmin, isSuper }: { isAdmin: boolean; isSuper: boolean }) {
  const trash = useLoad(safetyApi.listTrash, isAdmin);
  const [busy, setBusy] = useState<string | null>(null);

  const restore = async (it: TrashItem) => {
    setBusy(it.id);
    try {
      await safetyApi.restore(it.id);
      toast.success(`${KIND[it.kind]} ${it.name} is back.`);
      trash.reload();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };
  const purge = async (it: TrashItem) => {
    try {
      await safetyApi.purge(it.id);
      toast.success(`${KIND[it.kind]} ${it.name} is deleted for good.`);
      trash.reload();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  let body;
  if (!isAdmin) {
    body = (
      <SettingsCard>
        <SettingRow label="Deleted items" description="Keys, services and apps deleted in the last 7 days.">
          <ReadOnlyNote>Only admins can see and restore deleted items.</ReadOnlyNote>
        </SettingRow>
      </SettingsCard>
    );
  } else if (trash.error) {
    body = <SectionError what="the trash" message={trash.error} onRetry={trash.reload} />;
  } else if (!trash.data) {
    body = (
      <SettingsCard>
        <SettingRow label="Deleted items">
          <EnvValueSkeleton />
        </SettingRow>
      </SettingsCard>
    );
  } else if (trash.data.items.length === 0) {
    body = (
      <SettingsCard>
        <SettingRow label="The trash is empty" description="Deleted keys, services and apps show up here." />
      </SettingsCard>
    );
  } else {
    body = (
      <SettingsCard>
        {trash.data.items.map((it) => (
          <SettingRow
            key={it.id}
            label={
              <span className="flex flex-wrap items-center gap-2">
                <Chip variant="caption" color="soft">
                  {KIND[it.kind]}
                </Chip>
                <span className="wrap-anywhere">{itemName(it)}</span>
              </span>
            }
            description={`Deleted ${relTime(it.deleted_at)} by ${it.deleted_by}. Goes for good ${relTime(it.expires_at)}.`}
          >
            <div className="flex flex-wrap gap-2 sm:justify-end">
              <Button
                variant="secondary"
                size="small"
                leadingIcon={busy === it.id ? ButtonSpinner : RiArrowGoBackLine}
                disabled={busy !== null}
                onClick={() => void restore(it)}
              >
                Restore
              </Button>
              {isSuper && (
                <ConfirmButton
                  label="Delete now"
                  confirmLabel="Delete for good"
                  description="This cannot be undone."
                  onConfirm={() => purge(it)}
                />
              )}
            </div>
          </SettingRow>
        ))}
      </SettingsCard>
    );
  }

  return (
    <SettingsSection id="trash" title="Trash" description="Deleted keys, services and apps stay here for 7 days, then go for good.">
      {body}
    </SettingsSection>
  );
}

/** The running version, the newest release, and a database backup for superadmins. */
export function BackupSection({ isSuper }: { isSuper: boolean }) {
  const version = useLoad(safetyApi.version);
  const [saving, setSaving] = useState(false);

  const download = async () => {
    setSaving(true);
    try {
      const { blob, filename } = await safetyApi.downloadBackup();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      toast.success("Backup downloaded.");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const v = version.data;
  let latest;
  if (version.error) latest = <span className="text-caption-1-regular text-text-error-primary">{version.error}</span>;
  else if (!v) latest = <EnvValueSkeleton />;
  else if (!v.latest) latest = <span className="text-body-2-regular text-text-secondary">Unknown</span>;
  else
    latest = (
      <span className="flex flex-wrap items-center gap-2 sm:justify-end">
        <span className="font-mono text-body-2-medium">{v.latest}</span>
        {v.update_available === true && (
          <Chip variant="caption" color="yellow">
            Update available
          </Chip>
        )}
        {v.update_available === false && (
          <Chip variant="caption" color="lime">
            Up to date
          </Chip>
        )}
      </span>
    );

  return (
    <SettingsSection id="backup" title="Version and backup" description="Which release this server runs, and a copy of its data.">
      <SettingsCard>
        <SettingRow label="Running" description="The version of this server.">
          {v ? <span className="font-mono text-body-2-medium">{v.current}</span> : version.error ? null : <EnvValueSkeleton />}
        </SettingRow>
        <SettingRow label="Newest release" description="Checked on GitHub every few hours.">
          {latest}
        </SettingRow>
        {v?.update_available && (
          <SettingRow label="Upgrade" description="Your data stays. Migrations run on start." stacked>
            <CodeBlock code={v.upgrade_command} />
          </SettingRow>
        )}
        <SettingRow
          label="Download a backup"
          description="A copy of the database, taken without stopping the server. Keys in it stay encrypted: keep ACNOLOGIA_MASTER_KEY with it, stored apart."
        >
          {isSuper ? (
            <Button
              variant="secondary"
              size="small"
              leadingIcon={saving ? ButtonSpinner : RiDownload2Line}
              disabled={saving}
              onClick={() => void download()}
            >
              Download
            </Button>
          ) : (
            <ReadOnlyNote>Only a superadmin can download a backup.</ReadOnlyNote>
          )}
        </SettingRow>
      </SettingsCard>
    </SettingsSection>
  );
}
