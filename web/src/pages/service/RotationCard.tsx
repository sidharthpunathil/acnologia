import { useCallback, useEffect, useState } from "react";
import { Link as RouterLink } from "react-router-dom";
import { RiShuffleLine } from "@remixicon/react";
import type { Rotation, Service } from "../../lib/types";
import { ops, type RotationReport } from "../../lib/ops";
import { useServices } from "../../stores/services";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { plural } from "../../lib/format";
import { Section } from "@/components/acn";
import { Chip } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { RadioGroup } from "@/components/base/radio/radio";
import { RadioCard } from "@/components/base/radio/radio-card";
import { Label } from "@/components/base/input/label";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";

const MODES: { id: Rotation; title: string; help: string }[] = [
  { id: "", title: "Off", help: "Only this service answers its requests." },
  { id: "fallback", title: "Fallback", help: "This service first. Another takes over when it has no key, is out of quota or failing." },
  { id: "round_robin", title: "Round robin", help: "Each request starts at the next service, so the load and quota spread over all of them." },
];

const MODE_LABEL: Record<Rotation, string> = { "": "off", fallback: "fallback", round_robin: "round robin" };

/** Which endpoints two services share, in a few words. */
function sharedText(entries: string[], shared: number): string {
  const named = entries.filter((e) => e !== "");
  if (!named.length) return "Same provider and paths";
  const shown = named.slice(0, 3).join(", ");
  return shared > 3 ? `${shown} and ${shared - 3} more` : shown;
}

/**
 * Provider rotation: when other services serve the same endpoints (the same
 * provider, or another provider whose catalog lists the same entries),
 * requests can move between providers as they move between keys.
 */
export function RotationCard({ service, canManage }: { service: Service; canManage: boolean }) {
  const { update, fetch } = useServices();
  const current: Rotation = service.rotation ?? "";
  const [mode, setMode] = useState<Rotation>(current);
  const [report, setReport] = useState<RotationReport | null>(null);
  const [failed, setFailed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [joining, setJoining] = useState(false);

  useEffect(() => setMode(current), [current]);

  const load = useCallback(() => {
    ops
      .rotation(service.id)
      .then((r) => {
        setReport(r);
        setFailed(false);
      })
      .catch(() => setFailed(true));
  }, [service.id]);
  useEffect(load, [load]);

  const save = async () => {
    setSaving(true);
    try {
      await update(service.id, { rotation: mode === "" ? "off" : mode });
      toast.success(mode === "" ? "Provider rotation off" : `Provider rotation on, ${MODE_LABEL[mode]}`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const matches = report?.matches ?? [];
  const waiting = matches.filter((m) => m.rotation === "");
  const joinAll = async () => {
    const m = mode === "" ? "fallback" : mode;
    setJoining(true);
    try {
      const r = await ops.setRotation([service.id, ...waiting.map((x) => x.id)], m);
      toast.success(`Rotation on for ${plural(r.changed.length, "service")}`);
      await fetch().catch(() => undefined);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setJoining(false);
    }
  };

  // Services shared by another server rotate on the owner's side.
  if (service.peer_link_id || (failed && !report)) return null;

  return (
    <Section
      title={
        <span className="flex flex-wrap items-center gap-2">
          <RiShuffleLine className="size-5 shrink-0 text-foreground-icon-secondary" aria-hidden />
          Provider rotation
          <Chip variant="caption" color={current ? "lime" : "gray"} className="rounded-full px-2 py-0.5 font-mono">
            {MODE_LABEL[current]}
          </Chip>
        </span>
      }
      description="Rotate across providers as well as keys. Requests move between the services below that serve the same endpoint, when each has rotation on."
    >
      <RadioGroup value={mode} isDisabled={!canManage} onChange={(v) => setMode(v as Rotation)} className="gap-1.5">
        <Label>Mode</Label>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          {MODES.map((m) => (
            <RadioCard
              key={m.id || "off"}
              value={m.id}
              title={m.title}
              description={<span className="whitespace-normal text-caption-1-regular">{m.help}</span>}
              className="items-start"
            />
          ))}
        </div>
      </RadioGroup>

      <div className="mt-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-body-medium text-text-primary">Same endpoints elsewhere</h3>
          {canManage && waiting.length > 0 && (
            <Button
              variant="secondary"
              size="small"
              leadingIcon={joining ? ButtonSpinner : undefined}
              disabled={joining}
              onClick={() => void joinAll()}
            >
              Turn on for {waiting.length === 1 ? "it" : `all ${waiting.length}`}
            </Button>
          )}
        </div>
        {!report ? (
          <div className="mt-3 flex flex-col gap-2" role="status" aria-label="Loading matching services">
            <Skeleton shape="text" className="h-4 w-2/3" />
            <Skeleton shape="text" className="h-4 w-1/2" />
          </div>
        ) : matches.length === 0 ? (
          <p className="mt-2 text-body-2-regular text-text-secondary">
            No other service serves these endpoints yet. Add a provider whose catalog lists the same entries, or a second service of this provider.
          </p>
        ) : (
          <ul className="mt-2 divide-y divide-separator-border">
            {matches.map((m) => (
              <li key={m.id} className="flex flex-col gap-1 py-2.5 sm:flex-row sm:items-center sm:gap-4">
                <div className="min-w-0 flex-1">
                  <RouterLink
                    to={`/services/${m.id}`}
                    className="rounded-sm text-body-2-medium text-text-primary underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                  >
                    {m.name}
                  </RouterLink>
                  <span className="ms-1.5 text-caption-1-regular text-text-tertiary">{m.provider}</span>
                  <p className="wrap-anywhere text-caption-1-regular text-text-secondary">{sharedText(m.entries, m.shared)}</p>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <span className="font-mono text-caption-1-regular tabular-nums text-text-tertiary">{plural(m.keys, "free key")}</span>
                  <Chip variant="caption" color={m.rotation ? "lime" : "gray"} className="rounded-full px-2 py-0.5 font-mono">
                    {MODE_LABEL[m.rotation]}
                  </Chip>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>

      {canManage && (
        <div className="mt-5 flex justify-end gap-2">
          {mode !== current && (
            <Button variant="ghost" onClick={() => setMode(current)} disabled={saving}>
              Discard
            </Button>
          )}
          <Button variant="primary" disabled={mode === current || saving} leadingIcon={saving ? ButtonSpinner : undefined} onClick={() => void save()}>
            Save provider rotation
          </Button>
        </div>
      )}
    </Section>
  );
}
