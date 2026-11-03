// Sharing with people: the claim link shown once, a new link while it is
// unclaimed, and the bits of a person share's row.
import { useState } from "react";
import { RiLinkM, RiUserLine } from "@remixicon/react";
import { sharing, personLabel, type InviteValidity, type Share } from "../../lib/sharing";
import { dateTime } from "../../lib/format";
import { errorMessage } from "../../lib/api";
import { Modal } from "../../components/Modal";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { Popover, PopoverFooter, PopoverHeader, PopoverTrigger } from "@/components/base/popover/popover";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout, SecretBox } from "@/components/acn";
import { ChoicePills } from "./Timers";

export const LINK_VALIDITY: { id: InviteValidity; label: string }[] = [
  { id: "1h", label: "1 hour" },
  { id: "24h", label: "24 hours" },
  { id: "7d", label: "7 days" },
];

export interface ClaimLink {
  label: string;
  url: string;
  expiresAt: string | null;
  mode: "proxy" | "reveal";
}

/** "Person" next to the share name. */
export function RecipientChip() {
  return (
    <Chip variant="caption" color="blue" className="gap-1">
      <RiUserLine className="size-3" aria-hidden />
      Person
    </Chip>
  );
}

/** Claimed, or waiting with how long the link still works. */
export function ClaimState({ share }: { share: Share }) {
  const who = personLabel(share);
  if (share.claimed_at) return <span>Claimed {dateTime(share.claimed_at)}</span>;
  if (share.status === "revoked" || share.status === "expired") return <span>Never claimed</span>;
  const t = share.claim_expires_at ? Date.parse(share.claim_expires_at) : NaN;
  const lapsed = !Number.isNaN(t) && t <= Date.now();
  return (
    <span>
      Waiting for <bdi className="text-text-primary">{who}</bdi> to claim
      {lapsed ? ", the link expired. Make a new one." : !Number.isNaN(t) ? <>, link works until {dateTime(share.claim_expires_at)}</> : null}
    </span>
  );
}

/** The claim URL, shown once right after it is made. */
export function ClaimLinkModal({ link, onClose }: { link: ClaimLink | null; onClose: () => void }) {
  return (
    <Modal
      open={!!link}
      onClose={onClose}
      title={`Link for ${link?.label ?? ""}`}
      footer={
        <Button variant="primary" onClick={onClose}>
          I have copied it
        </Button>
      }
    >
      {link && (
        <div className="flex flex-col gap-3">
          <Callout tone="warning">This link is shown once and works once. Copy it now.</Callout>
          <SecretBox value={link.url} />
          {link.expiresAt && (
            <p className="text-body-2-regular text-text-secondary">
              Works until {dateTime(link.expiresAt)}, your time. You can make a new link while it is unclaimed.
            </p>
          )}
          <p className="text-body-2-regular text-text-primary">
            Send it to <bdi>{link.label}</bdi> over a private channel.
          </p>
          {link.mode === "reveal" ? (
            <p className="text-body-2-regular text-text-secondary">When they open it, they see the keys once. Nothing is emailed.</p>
          ) : (
            <p className="text-body-2-regular text-text-secondary">
              When they open it, they get a personal token, code snippets and an MCP setup. Later they can check their usage at{" "}
              <code dir="ltr" className="font-mono text-text-primary">
                {window.location.origin}/me
              </code>
              . Nothing is emailed.
            </p>
          )}
        </div>
      )}
    </Modal>
  );
}

/** "New link": pick how long it works, then the old link stops and the new one is shown once. */
export function NewLinkButton({ share, onLink }: { share: Share; onLink: (url: string, expiresAt: string | null) => void }) {
  const [open, setOpen] = useState(false);
  const [valid, setValid] = useState<InviteValidity>("24h");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const run = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await sharing.newClaimLink(share.id, valid);
      setOpen(false);
      onLink(r.claim_url, r.claim_expires_at ?? r.share?.claim_expires_at ?? null);
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <PopoverTrigger
      isOpen={open}
      onOpenChange={(o) => {
        if (busy) return;
        if (o) {
          setValid("24h");
          setErr(null);
        }
        setOpen(o);
      }}
    >
      <Button size="small" variant="ghost" leadingIcon={RiLinkM}>
        New link
      </Button>
      <Popover placement="bottom end" className="w-80">
        <PopoverHeader title="New claim link" description={`The current link for ${personLabel(share)} stops working.`} />
        <div className="flex flex-col gap-2">
          <div className="text-caption-1-medium text-text-primary">Works for</div>
          <ChoicePills label="Link works for" value={valid} options={LINK_VALIDITY} onChange={setValid} />
        </div>
        {err && <p className="text-caption-1-regular text-text-error-primary">{err}</p>}
        <PopoverFooter>
          <Button variant="secondary" size="small" onClick={() => setOpen(false)} disabled={busy}>
            Cancel
          </Button>
          <Button variant="primary" size="small" onClick={() => void run()} disabled={busy} aria-busy={busy || undefined} leadingIcon={busy ? ButtonSpinner : undefined}>
            Make link
          </Button>
        </PopoverFooter>
      </Popover>
    </PopoverTrigger>
  );
}
