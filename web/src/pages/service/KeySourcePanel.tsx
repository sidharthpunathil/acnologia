import { useEffect, useState } from "react";
import { Link as RouterLink } from "react-router-dom";
import { RiLinkM } from "@remixicon/react";
import { api, errorMessage } from "../../lib/api";
import { plural } from "../../lib/format";
import type { Key, Service, SharedKeysReport } from "../../lib/types";
import { toast } from "../../stores/toast";
import { Button } from "@/components/base/buttons/button";
import { cx } from "@/utils/cx";

/**
 * Where else this service gets keys: the key source (another service of the
 * same account) and its keys, with a way to stop sharing. With no source,
 * offers one when a service of the same account has keys.
 */
export function KeySourcePanel({
  service,
  shared,
  manage,
  onChange,
  className,
}: {
  service: Service;
  shared: Key[];
  manage: boolean;
  onChange: () => void;
  className?: string;
}) {
  const [offer, setOffer] = useState<SharedKeysReport["suggestions"][number] | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (service.key_source_id || !manage) {
      setOffer(null);
      return;
    }
    let live = true;
    api
      .sharedKeys()
      .then((r) => live && setOffer(r.suggestions.find((x) => x.service === service.slug) ?? null))
      .catch(() => live && setOffer(null));
    return () => {
      live = false;
    };
  }, [service.slug, service.key_source_id, service.key_counts.total, manage]);

  const setSource = async (source: string | null) => {
    setBusy(true);
    try {
      await api.setKeySource(service.id, source);
      toast.success(source ? `${service.name} now uses the keys of ${offer?.source_name ?? source}.` : `${service.name} now uses only its own keys.`);
      onChange();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  if (!service.key_source_id) {
    if (!offer) return null;
    return (
      <section
        aria-label="Keys of the same account"
        className={cx("flex flex-col gap-3 rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:flex-row sm:items-center sm:p-5", className)}
      >
        <RiLinkM className="size-5 shrink-0 text-foreground-icon-tertiary" aria-hidden />
        <p className="min-w-0 flex-1 text-body-2-regular text-text-secondary">
          <span className="text-text-primary">{offer.source_name}</span> is the same account and has {plural(offer.source_keys, "key")} that work here too.
        </p>
        <Button variant="primary" size="small" disabled={busy} aria-busy={busy || undefined} onClick={() => void setSource(offer.source)}>
          Use its keys
        </Button>
      </section>
    );
  }

  return (
    <section
      aria-label="Keys of the same account"
      className={cx("rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:p-5", className)}
    >
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
        <RiLinkM className="mt-0.5 size-5 shrink-0 text-foreground-icon-tertiary" aria-hidden />
        <div className="min-w-0 flex-1">
          <p className="text-body-2-regular text-text-secondary">
            Also uses the keys of{" "}
            <RouterLink
              to={`/services/${service.key_source_id}`}
              className="rounded text-text-primary underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
            >
              {service.key_source_name || service.key_source_slug}
            </RouterLink>
            , the same account. Its own keys go first; these back them up. They are managed there.
          </p>
          {shared.length > 0 && (
            <ul aria-label="Shared keys" className="mt-3 flex flex-col gap-1">
              {shared.map((k) => (
                <li key={k.id} className="flex min-w-0 items-center gap-2 text-caption-1-regular">
                  <span className="min-w-0 truncate text-text-primary">{k.label || "Untitled"}</span>
                  <span className="shrink-0 font-mono text-text-tertiary">{k.hint}</span>
                  <span className="shrink-0 text-text-tertiary">{k.effective_status}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
        {manage && (
          <Button variant="secondary" size="small" disabled={busy} aria-busy={busy || undefined} onClick={() => void setSource(null)}>
            Use only its own keys
          </Button>
        )}
      </div>
    </section>
  );
}
