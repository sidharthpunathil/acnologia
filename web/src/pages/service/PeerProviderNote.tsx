import { useCallback, useEffect, useState } from "react";
import { RiDownloadCloud2Line } from "@remixicon/react";
import { Callout } from "@/components/acn";
import { ConfirmButton } from "../../components/ConfirmButton";
import { Button } from "@/components/base/buttons/button";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { ApiError, errorMessage } from "../../lib/api";
import { peerProviders, type PeerProvider } from "../../lib/peerProviders";
import type { Service } from "../../lib/types";
import { useProviders } from "../../stores/providers";
import { toast } from "../../stores/toast";

/**
 * On a service another server shares: says when its provider spec was added
 * from that server for this service, and offers a superadmin the owner's
 * newer version. Updates are never pulled on their own, because a changed
 * spec could send the keys added here somewhere else.
 */
export function PeerProviderNote({ service, canPull, className }: { service: Service; canPull: boolean; className?: string }) {
  const linkId = service.peer_link_id;
  const slug = service.peer_provider;
  const peer = service.peer_name || "the server that shares it";
  const [row, setRow] = useState<PeerProvider | null>(null);
  const [busy, setBusy] = useState(false);
  const refreshProviders = useProviders((s) => s.fetch);

  const load = useCallback(async () => {
    if (!linkId || !slug) return;
    const res = await peerProviders.list(linkId);
    setRow(res.providers.find((p) => p.provider === slug) ?? null);
  }, [linkId, slug]);

  useEffect(() => {
    load().catch(() => setRow(null));
  }, [load]);

  if (!linkId || !slug || !row || row.missing) return null;

  const pull = async (allowBaseUrlChange: boolean) => {
    setBusy(true);
    try {
      const res = await peerProviders.pull(linkId, slug, allowBaseUrlChange);
      setRow(res.providers.find((p) => p.provider === slug) ?? null);
      await refreshProviders().catch(() => undefined);
      toast.success(`Updated ${row.name} from ${peer}`);
    } catch (e) {
      if (e instanceof ApiError && e.code === "base_url_changes") {
        toast.error(`${errorMessage(e)}. Load the page again to confirm the new URL.`);
        await load().catch(() => undefined);
      } else toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const moves = row.update_available && row.remote_base_url !== "" && row.remote_base_url !== row.base_url;
  const action =
    row.update_available && canPull ? (
      moves ? (
        <ConfirmButton
          label="Pull update"
          confirmLabel="Pull update"
          icon={RiDownloadCloud2Line}
          description={`The new version sends ${row.name} requests, and any keys added here, to ${row.remote_base_url} instead of ${row.base_url}.`}
          onConfirm={() => pull(true)}
        />
      ) : (
        <Button size="small" variant="ghost" leadingIcon={busy ? ButtonSpinner : RiDownloadCloud2Line} disabled={busy} onClick={() => void pull(false)}>
          Pull update
        </Button>
      )
    ) : undefined;

  const added = row.kind === "dependency" ? `${row.name} was added from ${peer} for this service.` : `${row.name} was shared by ${peer}.`;
  return (
    <Callout tone={row.update_available ? "info" : "neutral"} action={action} className={className}>
      {added}{" "}
      {row.update_available
        ? `An update is available from ${peer}. It is never applied on its own.`
        : row.edited_here
          ? "It was edited here since, so updates from that server are not offered."
          : "It is not updated on its own."}
    </Callout>
  );
}
