import { useEffect, useMemo, useState } from "react";
import { RiDownload2Line, RiFileCopyLine, RiRobot2Line } from "@remixicon/react";
import type { GuideDoc, Service } from "../../lib/types";
import { api, errorMessage } from "../../lib/api";
import { can } from "../../lib/perms";
import { useAuth } from "../../stores/auth";
import { useProviders } from "../../stores/providers";
import { docProviderSlug } from "../../lib/peerProviders";
import { toast } from "../../stores/toast";
import { Callout, CodeBlock, copyText } from "@/components/acn";
import { Button } from "@/components/base/buttons/button";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "@/components/base/modal/modal";
import { RadioGroup } from "@/components/base/radio/radio";
import { RadioCard } from "@/components/base/radio/radio-card";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { buildBrief } from "./brief-markdown";
import { useForwardProxy } from "../../lib/forwardProxy";
import { ActionIcon } from "./parts";

type TokenChoice = "placeholder" | "create";

/**
 * "Copy for LLM": a Markdown brief that lets any model call this service
 * through the proxy. It can mint an app token limited to this service and
 * proxy mode, so the brief works when pasted. Provider keys are never in it.
 */
export function LlmBriefButton({ service }: { service: Service }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      {/* Phones: icon only, so the header actions keep one row at 390px. */}
      <span className="sm:hidden">
        <ActionIcon icon={RiRobot2Line} label="Copy for LLM" size="medium" onClick={() => setOpen(true)} />
      </span>
      <Button variant="secondary" leadingIcon={RiRobot2Line} onClick={() => setOpen(true)} className="max-sm:hidden">
        Copy for LLM
      </Button>
      {open && <LlmBriefModal service={service} onClose={() => setOpen(false)} />}
    </>
  );
}

function LlmBriefModal({ service, onClose }: { service: Service; onClose: () => void }) {
  const manage = can.manage(useAuth((s) => s.user?.role));
  const slug = docProviderSlug(service);
  const provider = useProviders((st) => st.providers.find((p) => p.slug === slug));
  const [choice, setChoice] = useState<TokenChoice>("placeholder");
  const [token, setToken] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // The provider's written guide adds endpoints, params and notes. The brief works without it.
  const [guide, setGuide] = useState<GuideDoc | null>(null);
  useEffect(() => {
    let live = true;
    api
      .serviceGuide(service.id)
      .then((g) => {
        if (live) setGuide(g.guide);
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [service.id]);

  // A shared service is not on the forward proxy; elsewhere the admin view knows its real address.
  const peer = !!service.peer_link_id;
  const fwd = useForwardProxy(manage && !peer);
  const forwardProxy = peer ? null : (fwd.setup?.proxy_url ?? null) || undefined;
  const md = useMemo(
    () => buildBrief({ service, provider, host: window.location.origin, token: choice === "create" ? token : null, guide, forwardProxy }),
    [service, provider, choice, token, guide, forwardProxy],
  );
  const ready = choice === "placeholder" || token !== null;

  const createToken = async () => {
    setBusy(true);
    try {
      const res = await api.createApp({
        name: `${service.name} for an LLM`,
        description: `Created by Copy for LLM on ${new Date().toISOString().slice(0, 10)}. Proxy only, ${service.slug} only. Delete it on the Apps page to revoke.`,
        mode: "proxy",
        allowed_services: [service.slug],
      });
      setToken(res.token);
      toast.success(`Created the app "${res.app.name}". Its token is in the brief.`);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (await copyText(md)) toast.success("Copied. Paste it into your LLM.");
    else toast.error("Could not copy. Select the text in the preview instead.");
  };

  const download = () => {
    const url = URL.createObjectURL(new Blob([md], { type: "text/markdown" }));
    const a = Object.assign(document.createElement("a"), { href: url, download: `${service.slug}-for-llm.md` });
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <Modal isOpen onOpenChange={(o) => !o && !busy && onClose()} size="lg">
      <ModalHeader
        title="Copy for LLM"
        description={`Instructions any model can follow to call ${service.name} through Acnologia. It never contains a ${service.name} key: the model sends requests to the proxy, and Acnologia adds the key.`}
      />
      <ModalBody>
        <div className="flex flex-col gap-4">
          <RadioGroup
            aria-label="Token in the brief"
            value={choice}
            onChange={(v) => setChoice(v as TokenChoice)}
            className="gap-1.5"
          >
            <RadioCard
              value="placeholder"
              title="Use a placeholder"
              description={<span className="whitespace-normal text-caption-1-regular">The brief reads the token from ACNOLOGIA_TOKEN. Best when the model runs your code.</span>}
              className="items-start"
            />
            <RadioCard
              value="create"
              isDisabled={!manage}
              title="Include a working token"
              description={
                <span className="whitespace-normal text-caption-1-regular">
                  {manage
                    ? `Creates an app that can only call ${service.name}, in proxy mode. Paste and it works. Delete the app to revoke it.`
                    : "Only admins can create app tokens."}
                </span>
              }
              className="items-start"
            />
          </RadioGroup>

          {choice === "create" && token === null && (
            <Callout tone="info">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <span>The token is shown once, here. Anyone with the brief can call {service.name} until you delete the app.</span>
                <Button size="small" variant="primary" onClick={createToken} disabled={busy} leadingIcon={busy ? ButtonSpinner : undefined}>
                  {busy ? "Creating" : "Create token"}
                </Button>
              </div>
            </Callout>
          )}

          <CodeBlock code={md} label={`${service.slug}-for-llm.md`} className="max-h-[22rem] overflow-y-auto" />
        </div>
      </ModalBody>
      <ModalFooter>
        <Button variant="secondary" leadingIcon={RiDownload2Line} onClick={download} disabled={!ready}>
          Download .md
        </Button>
        <Button variant="primary" leadingIcon={RiFileCopyLine} onClick={copy} disabled={!ready}>
          Copy as Markdown
        </Button>
      </ModalFooter>
    </Modal>
  );
}
