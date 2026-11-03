import { useEffect, useRef, useState, type FormEvent } from "react";
import { errorMessage } from "@/lib/api";
import { sharing, type ServerIdentity } from "@/lib/sharing";
import { envOf, type SettingsSummary } from "@/lib/settings";
import { toast } from "@/stores/toast";
import { Input } from "@/components/base/input/input";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { CopyButton } from "@/components/acn/copy-button";
import { useLoad, useSaveShortcut, SAVE_SHORTCUT, type Loaded } from "./hooks";
import { EnvValue, ReadOnlyNote, RowsSkeleton, SaveBar, SectionError, SettingRow, SettingsCard, SettingsSection } from "./parts";

const MAX_NAME = 80;

/** A value in the mono voice that may run long (ids, URLs), kept left to right. */
function Mono({ children }: { children: string }) {
  return (
    <span dir="ltr" className="font-mono text-body-2-regular wrap-anywhere text-text-primary">
      {children}
    </span>
  );
}

function urlHint(i: ServerIdentity) {
  if (i.url_source === "saved") return "Saved here. Other servers reach this one at this URL.";
  return "Guessed from this page's address. Save the URL other servers should use.";
}

/** Name and public URL, editable by a superadmin. Uses the sharing identity API. */
function IdentityCard({
  identity,
  canEdit,
  onSaved,
  onDirty,
}: {
  identity: ServerIdentity;
  canEdit: boolean;
  onSaved: (i: ServerIdentity) => void;
  /** Reports unsaved edits up, for the nav's dot and the leave warning. */
  onDirty: (dirty: boolean) => void;
}) {
  const savedUrl = identity.url_source === "saved" ? identity.url : "";
  const [name, setName] = useState(identity.name);
  const [url, setUrl] = useState(savedUrl);
  const [busy, setBusy] = useState(false);
  const form = useRef<HTMLFormElement>(null);

  useEffect(() => {
    setName(identity.name);
    setUrl(identity.url_source === "saved" ? identity.url : "");
  }, [identity]);

  const urlFromEnv = identity.url_source === "env";
  const nameErr = name.trim() === "" ? "Give this server a name." : undefined;
  const dirty = name.trim() !== identity.name || (!urlFromEnv && url.trim() !== savedUrl);

  useEffect(() => {
    onDirty(canEdit && dirty);
    return () => onDirty(false);
  }, [canEdit, dirty, onDirty]);

  const discard = () => {
    setName(identity.name);
    setUrl(savedUrl);
  };

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!dirty || busy || nameErr) return;
    setBusy(true);
    try {
      const body: { name?: string; url?: string } = { name: name.trim() };
      if (!urlFromEnv) body.url = url.trim();
      onSaved(await sharing.updateIdentity(body));
      toast.success("Saved this server");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  useSaveShortcut(form, canEdit && dirty, () => void save());

  const urlRow = urlFromEnv ? (
    <SettingRow label="Public URL" description="Where other servers and links reach this one.">
      <EnvValue env={{ name: "ACNOLOGIA_PUBLIC_URL", set: true, value: identity.url }} format={(v) => <Mono>{v}</Mono>} />
    </SettingRow>
  ) : canEdit ? (
    <SettingRow label="Public URL" labelId="srv-url-label" description="Where other servers and links reach this one." stacked>
      <Input
        aria-labelledby="srv-url-label"
        type="url"
        value={url}
        onChange={setUrl}
        placeholder={identity.url}
        hint={urlHint(identity)}
        fieldClassName="font-mono"
        inputDir="ltr"
        autoComplete="off"
      />
    </SettingRow>
  ) : (
    <SettingRow label="Public URL" description={urlHint(identity)}>
      <Mono>{identity.url}</Mono>
    </SettingRow>
  );

  const rows = (
    <>
      {canEdit ? (
        <SettingRow label="Name" labelId="srv-name-label" description="Other servers see this name when you share with them or connect to them." stacked>
          <Input
            aria-labelledby="srv-name-label"
            value={name}
            onChange={setName}
            maxLength={MAX_NAME}
            isInvalid={!!nameErr}
            errorMessage={nameErr}
            autoComplete="off"
          />
        </SettingRow>
      ) : (
        <SettingRow label="Name" description="Other servers see this name when you share with them or connect to them.">
          <span className="text-body-regular wrap-anywhere text-text-primary">{identity.name}</span>
        </SettingRow>
      )}
      {urlRow}
      <SettingRow label="Server id" description="Stable for the life of the data directory.">
        <span className="inline-flex items-center gap-1">
          <Mono>{identity.id}</Mono>
          <CopyButton text={identity.id} label="Copy server id" iconOnly size="xs" />
        </span>
      </SettingRow>
      <SettingRow label="Key fingerprint" description="Check it with the other side before you connect two servers.">
        <Mono>{identity.fingerprint}</Mono>
      </SettingRow>
    </>
  );

  if (!canEdit) {
    return (
      <SettingsCard>
        {rows}
        <div className="px-4 py-3 sm:px-5">
          <ReadOnlyNote>Only a superadmin can rename this server or change its URL.</ReadOnlyNote>
        </div>
      </SettingsCard>
    );
  }
  return (
    <form ref={form} onSubmit={(e) => void save(e)} aria-label="This server" noValidate>
      <SettingsCard>
        {rows}
        <SaveBar dirty={dirty} saving={busy} onDiscard={discard} shortcut={SAVE_SHORTCUT} />
      </SettingsCard>
    </form>
  );
}

/**
 * General: this server's identity (name, URL, id, fingerprint), its version
 * and where the master key lives. Admins see it; a superadmin edits the name
 * and URL. Members get the reason instead.
 */
export function ServerSection({
  isAdmin,
  isSuper,
  summary,
  onDirty,
}: {
  isAdmin: boolean;
  isSuper: boolean;
  summary: Loaded<SettingsSummary>;
  onDirty: (dirty: boolean) => void;
}) {
  const identity = useLoad(sharing.identity, isAdmin);

  let body;
  if (!isAdmin) {
    body = (
      <SettingsCard>
        <SettingRow label="Server details" description="Name, public URL, id, version and key fingerprint.">
          <ReadOnlyNote>Only admins can see this server's details.</ReadOnlyNote>
        </SettingRow>
      </SettingsCard>
    );
  } else if (identity.error) {
    body = <SectionError what="this server" message={identity.error} onRetry={identity.reload} />;
  } else if (!identity.data) {
    body = (
      <SettingsCard>
        <RowsSkeleton rows={4} />
      </SettingsCard>
    );
  } else {
    body = <IdentityCard identity={identity.data} canEdit={isSuper} onSaved={identity.set} onDirty={onDirty} />;
  }

  return (
    <SettingsSection id="general" title="General" description="This server's identity, as other servers and shared links see it.">
      {body}
      {isAdmin &&
        (summary.error ? (
          <SectionError what="the server configuration" message={summary.error} onRetry={summary.reload} />
        ) : (
          <SettingsCard>
            <SettingRow label="Version" description="The Acnologia build this server runs.">
              {summary.data ? <Mono>{summary.data.version || "dev"}</Mono> : <Skeleton shape="text" className="h-4 w-20" />}
            </SettingRow>
            <SettingRow label="Master key" description="Encrypts every stored secret. Keep a copy somewhere other than this server.">
              <EnvValue env={envOf(summary.data, "ACNOLOGIA_MASTER_KEY")} unsetLabel="Kept in master.key in the data directory" />
            </SettingRow>
          </SettingsCard>
        ))}
    </SettingsSection>
  );
}
