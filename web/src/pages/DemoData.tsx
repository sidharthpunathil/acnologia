import { useEffect, useState } from "react";
import { RiFlaskLine } from "@remixicon/react";
import { request, errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import type { Service } from "../lib/types";
import { useAuth } from "../stores/auth";
import { useServices } from "../stores/services";
import { useApps } from "../stores/apps";
import { toast } from "../stores/toast";
import { Modal } from "../components/Modal";
import { SecretBox } from "../components/CopyButton";
import { Button } from "@/components/base/buttons/button";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { CodeBlock, ConfirmButton } from "@/components/acn";

/*
 * Demo data for the Getting started card: a fake provider this server
 * answers itself, two keys and an app, so the first call works before the
 * person owns a real key (POST /api/admin/demo, superadmin only). Once it is
 * loaded, the same row removes it again.
 */

interface DemoState {
  loaded: boolean;
  service_id?: number;
}

interface DemoLoaded {
  service: { id: number; slug: string; name: string };
  token: string;
  try: { curl: string };
}

export function DemoData({ services }: { services: Service[] }) {
  const isSuper = can.users(useAuth((s) => s.user?.role));
  const fetchServices = useServices((s) => s.fetch);
  const fetchApps = useApps((s) => s.fetch);
  const [state, setState] = useState<DemoState | null>(null);
  const [busy, setBusy] = useState(false);
  const [made, setMade] = useState<DemoLoaded | null>(null);

  useEffect(() => {
    if (!isSuper) return;
    let live = true;
    request<DemoState>("GET", "/api/admin/demo")
      .then((s) => live && setState(s))
      .catch(() => live && setState(null));
    return () => {
      live = false;
    };
  }, [isSuper]);

  const refresh = () => {
    fetchServices().catch(() => undefined);
    fetchApps().catch(() => undefined);
  };

  const load = async () => {
    setBusy(true);
    try {
      const res = await request<DemoLoaded>("POST", "/api/admin/demo", {});
      setMade(res);
      setState({ loaded: true, service_id: res.service.id });
      refresh();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    try {
      await request("DELETE", "/api/admin/demo");
      setState({ loaded: false });
      toast.success("Removed the demo data");
      refresh();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };

  if (!isSuper || !state) return null;
  if (!state.loaded && services.length > 0) return null;

  return (
    <>
      <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-separator-border pt-4">
        <p className="min-w-0 flex-1 text-body-2-regular text-text-secondary">
          {state.loaded
            ? "Demo data is loaded: a fake provider, two keys and a Demo app."
            : "No key yet? Load a fake provider with two keys and an app, and make a first call."}
        </p>
        {state.loaded ? (
          <ConfirmButton
            label="Remove demo data"
            confirmLabel="Remove"
            variant="outline"
            size="small"
            description="Deletes the demo service, its keys and the Demo app."
            onConfirm={remove}
          />
        ) : (
          <Button variant="secondary" size="small" disabled={busy} leadingIcon={busy ? ButtonSpinner : RiFlaskLine} onClick={load}>
            Load demo data
          </Button>
        )}
      </div>
      <Modal
        open={!!made}
        onClose={() => setMade(null)}
        title="Demo data is ready"
        description="This token belongs to the Demo app. It is shown once."
        footer={
          <Button variant="primary" onClick={() => setMade(null)}>
            Done
          </Button>
        }
      >
        {made && (
          <div className="flex flex-col gap-4">
            <SecretBox value={made.token} />
            <p className="text-body-2-regular text-text-secondary">
              Make a first call. Acnologia adds a demo key; the call only carries the app token. The service page's Try it panel works too.
            </p>
            <CodeBlock label="curl" code={made.try.curl} />
          </div>
        )}
      </Modal>
    </>
  );
}
