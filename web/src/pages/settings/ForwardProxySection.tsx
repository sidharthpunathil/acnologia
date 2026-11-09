import { useState } from "react";
import { RiDownload2Line, RiRefreshLine } from "@remixicon/react";
import { api, errorMessage } from "@/lib/api";
import { CA_PATH, loadForwardProxy, proxyWithToken } from "@/lib/forwardProxy";
import type { ForwardProxySetup } from "@/lib/types";
import { toast } from "@/stores/toast";
import { Chip } from "@/components/base/badges/chip";
import { CopyButton } from "@/components/acn/copy-button";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { useLoad } from "./hooks";
import { RowsSkeleton, SectionError, SettingRow, SettingsCard, SettingsSection } from "./parts";

const LINK =
  "inline-flex items-center gap-1.5 rounded-sm text-body-2-medium text-text-primary underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring";

/** A value in the mono voice that may run long, kept left to right. */
function Mono({ children }: { children: string }) {
  return (
    <span dir="ltr" className="font-mono text-body-2-regular wrap-anywhere text-text-primary">
      {children}
    </span>
  );
}

/** Where a value comes from: an environment variable, set or not. */
function EnvNote({ name, set }: { name: string; set: boolean }) {
  return (
    <span className="text-caption-1-regular text-text-tertiary">
      {set ? "Set by " : null}
      <code className="font-mono wrap-anywhere">{name}</code>
      {set ? null : " not set"}
    </span>
  );
}

function SetupCards({ setup, onRotated }: { setup: ForwardProxySetup; onRotated: (fingerprint: string) => void }) {
  const display = proxyWithToken(setup.proxy_url, "<app token>");
  const rotate = async () => {
    try {
      const r = await api.rotateForwardCA();
      onRotated(r.ca_fingerprint);
      toast.success("New CA made. Every app must download and trust it again.");
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  return (
    <>
      <SettingsCard>
        <SettingRow label="Proxy URL" description="Apps set this as HTTPS_PROXY, with their app token as the password." stacked>
          <div className="flex items-center gap-2 rounded-2lg bg-background-tertiary-default py-1.5 ps-3 pe-1.5">
            <code dir="ltr" className="min-w-0 flex-1 font-mono text-body-2-regular break-all text-text-primary">
              {display}
            </code>
            <CopyButton text={display} label="Copy" />
          </div>
          <div className="mt-1.5">
            <EnvNote name="ACNOLOGIA_FORWARD_PROXY_URL" set={!!setup.configured_url} />
          </div>
        </SettingRow>
        <SettingRow label="Listens on" description="Proxy requests reach the main port; a second listener can take them too.">
          <div className="flex flex-col gap-1 sm:items-end">
            <span className="text-body-2-medium text-text-primary">{setup.listen_addr ? `Main port and ${setup.listen_addr}` : "Main port"}</span>
            <EnvNote name="ACNOLOGIA_FORWARD_PROXY_ADDR" set={!!setup.listen_addr} />
          </div>
        </SettingRow>
        <SettingRow label="Passthrough" description="HTTPS to hosts that are not services is tunneled untouched, so an app can send all its traffic through the proxy.">
          <div className="flex flex-col gap-1 sm:items-end">
            <Chip variant="caption" color={setup.passthrough ? "lime" : "soft"} className="w-fit">
              {setup.passthrough ? "On" : "Off"}
            </Chip>
            <span className="text-caption-1-regular text-text-tertiary">
              <code className="font-mono wrap-anywhere">ACNOLOGIA_FORWARD_PASSTHROUGH</code>, on by default
            </span>
          </div>
        </SettingRow>
        <SettingRow label="Service header" description="Picks a service or group when several share a host.">
          <Mono>{`${setup.service_header}: <slug>`}</Mono>
        </SettingRow>
      </SettingsCard>

      <SettingsCard title="CA certificate">
        <SettingRow label="Fingerprint" description="Apps trust this CA for HTTPS to service hosts. Node takes NODE_EXTRA_CA_CERTS, Python REQUESTS_CA_BUNDLE, curl --cacert.">
          <Mono>{setup.ca_fingerprint}</Mono>
        </SettingRow>
        <SettingRow label="Certificate" description="Rotating makes a new CA. Every app must download and trust it again, or its HTTPS calls fail.">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2 sm:justify-end">
            <a href={CA_PATH} download="acnologia-ca.pem" className={LINK}>
              <RiDownload2Line className="size-4 shrink-0 text-foreground-icon-secondary" aria-hidden />
              Download CA certificate
            </a>
            <ConfirmButton
              label="Rotate CA"
              confirmLabel="Rotate CA"
              icon={RiRefreshLine}
              variant="outline"
              description="Apps that trust the current CA stop working over HTTPS until they download the new one."
              onConfirm={rotate}
            />
          </div>
        </SettingRow>
      </SettingsCard>

      <SettingsCard title="Hosts that get a key">
        {setup.hosts.length === 0 ? (
          <div className="px-4 py-4 text-body-2-regular text-text-secondary sm:px-5">No services yet. Add one and its host shows up here.</div>
        ) : (
          <ul aria-label="Hosts that get a key" className="divide-y divide-separator-border">
            {setup.hosts.map((h) => (
              <li key={`${h.host}${h.path ?? ""}|${h.service}`} className="flex flex-col gap-0.5 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-6 sm:px-5">
                <Mono>{`${h.host}${h.path ?? ""}`}</Mono>
                <span className="min-w-0 text-body-2-regular text-text-secondary sm:text-end">
                  {h.name} <span className="font-mono text-caption-1-regular text-text-tertiary">{h.service}</span>
                </span>
              </li>
            ))}
          </ul>
        )}
      </SettingsCard>
    </>
  );
}

/**
 * Forward proxy: apps set Acnologia as HTTPS_PROXY and call the provider's
 * own URL. Read only apart from rotating the CA; the environment sets the
 * rest. Admins only: the page leaves the section out for anyone else.
 */
export function ForwardProxySection() {
  const setup = useLoad(() => loadForwardProxy(true));
  const [fingerprint, setFingerprint] = useState<string | null>(null);

  let body;
  if (setup.error) body = <SectionError what="the forward proxy" message={setup.error} onRetry={setup.reload} />;
  else if (!setup.data)
    body = (
      <SettingsCard>
        <RowsSkeleton rows={4} />
      </SettingsCard>
    );
  else
    body = (
      <SetupCards
        setup={fingerprint ? { ...setup.data, ca_fingerprint: fingerprint } : setup.data}
        onRotated={(f) => {
          setFingerprint(f);
          void loadForwardProxy(true).catch(() => undefined);
        }}
      />
    );

  return (
    <SettingsSection
      id="forward-proxy"
      title="Forward proxy"
      description="Set HTTPS_PROXY and call the provider's own URL. Calls to a service's host get a key from the pool, and any key the app sends is replaced."
    >
      {body}
    </SettingsSection>
  );
}
