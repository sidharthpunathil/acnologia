import { useEffect, useState } from "react";
import { ShieldAlert } from "lucide-react";
import type { Key } from "../../lib/types";
import { useKeys } from "../../stores/keys";
import { errorMessage } from "../../lib/api";
import { Modal } from "../../components/Modal";
import { SecretBox } from "../../components/CopyButton";
import { Button, Spinner } from "../../components/ui";

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
    <Modal
      open={!!k}
      onClose={onClose}
      title={`Secret for ${k?.label || "key"}`}
      description={<span className="font-mono">{k?.hint}</span>}
      footer={
        <Button variant="primary" onClick={onClose}>
          Done
        </Button>
      }
    >
      <div className="space-y-3">
        <div className="flex items-start gap-2 rounded-xl bg-amber-50 px-3 py-2.5 text-xs text-amber-800">
          <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" />
          This reveal is recorded in the audit log. Do not paste it anywhere you would not paste a password.
        </div>
        {err ? <p className="text-sm text-red-600">{err}</p> : secret === null ? <div className="flex justify-center py-4"><Spinner /></div> : <SecretBox value={secret} />}
      </div>
    </Modal>
  );
}
