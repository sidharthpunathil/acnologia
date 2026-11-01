import { useEffect, useState } from "react";
import { AcnKeyVault } from "@/components/foundations/icons/acn";
import type { Key } from "../../lib/types";
import { useKeys } from "../../stores/keys";
import { errorMessage } from "../../lib/api";
import { Callout, SecretBox } from "@/components/acn";
import { Button } from "@/components/base/buttons/button";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "@/components/base/modal/modal";
import { Spinner } from "@/components/base/spinner/spinner";

export function RevealModal({ k, onClose }: { k: Key | null; onClose: () => void }) {
  const reveal = useKeys((s) => s.reveal);
  const [secret, setSecret] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    setSecret(null);
    setErr(null);
    if (!k) return;
    let live = true;
    reveal(k)
      .then((s) => live && setSecret(s))
      .catch((e) => live && setErr(errorMessage(e)));
    return () => {
      live = false;
    };
  }, [k, reveal]);

  return (
    <Modal isOpen={!!k} onOpenChange={(o) => !o && onClose()} size="sm">
      <ModalHeader title={`Secret for ${k?.label || "key"}`} description={<span className="font-mono">{k?.hint}</span>} icon={AcnKeyVault} />
      <ModalBody className="space-y-3">
        <Callout tone="warning">The audit log records this reveal. Treat the secret like a password.</Callout>
        {err ? (
          <Callout tone="error">{err}</Callout>
        ) : secret === null ? (
          <div className="flex justify-center py-4">
            <Spinner size="md" aria-label="Loading secret" />
          </div>
        ) : (
          <SecretBox value={secret} />
        )}
      </ModalBody>
      <ModalFooter>
        <Button variant="primary" onClick={onClose}>
          Done
        </Button>
      </ModalFooter>
    </Modal>
  );
}
