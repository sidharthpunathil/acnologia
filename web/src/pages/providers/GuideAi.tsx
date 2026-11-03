import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { RiSparkling2Line } from "@remixicon/react";
import { api, errorMessage } from "../../lib/api";
import { can } from "../../lib/perms";
import type { GenerateGuideResult } from "../../lib/types";
import { ACTIVE_STATUSES, useAi } from "../../stores/ai";
import { useAuth } from "../../stores/auth";
import { toast } from "../../stores/toast";
import { Callout } from "@/components/acn";
import { Button } from "@/components/base/buttons/button";
import { ButtonSpinner, SpinnerGlyph } from "@/components/base/spinner/spinner";

/** The run to follow in a generate response, if the server queued one. */
function runOf(res: GenerateGuideResult): { id: number; status: string } | null {
  if (res.run?.id) return res.run;
  if (typeof res.id === "number" && typeof res.status === "string") return { id: res.id, status: res.status };
  return null;
}

export interface GuideAiState {
  /** Admins only; null when the viewer may not use it at all. */
  allowed: boolean;
  /** Why the button is disabled, or null when it can run. */
  blocked: string | null;
  busy: boolean;
  run: { id: number; status: string } | null;
  start: () => Promise<void>;
}

/**
 * "Write guide with AI": asks the server to write or rewrite the provider's
 * guide. A queued run is polled like the builder's runs; when it finishes,
 * or when the server answers with the updated provider, onDone reloads.
 */
export function useGuideAi(slug: string, dirty: boolean, onDone: () => void): GuideAiState {
  const allowed = can.manage(useAuth((s) => s.user?.role));
  const status = useAi((s) => s.status);
  const ensureStatus = useAi((s) => s.ensureStatus);
  const poll = useAi((s) => s.poll);
  const [busy, setBusy] = useState(false);
  const [run, setRun] = useState<{ id: number; status: string } | null>(null);
  const done = useRef(onDone);
  done.current = onDone;

  useEffect(() => ensureStatus(), [ensureStatus]);

  const runId = run?.id;
  const active = !!run && ACTIVE_STATUSES.has(run.status);
  useEffect(() => {
    if (!runId || !active) return;
    let live = true;
    const t = window.setInterval(async () => {
      try {
        const st = await poll(runId);
        if (!live) return;
        setRun({ id: runId, status: st.run.status });
        if (ACTIVE_STATUSES.has(st.run.status)) return;
        if (st.run.status === "published") {
          toast.success("The guide is written and saved.");
          done.current();
        } else if (st.run.status === "failed") toast.error(st.run.error || "The AI run failed.");
      } catch (e) {
        if (live) toast.error(errorMessage(e));
      }
    }, 2000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, [runId, active, poll]);

  const blocked = !status
    ? "Checking whether AI is set up."
    : !status.enabled
      ? status.reason || "AI is not set up on this server."
      : dirty
        ? "Save or discard your changes first."
        : null;

  const start = async () => {
    setBusy(true);
    try {
      const res = await api.generateGuide(slug);
      const r = runOf(res);
      if (r) {
        setRun(r);
        if (!ACTIVE_STATUSES.has(r.status)) done.current();
      } else if (res.updated === false) {
        toast.info(res.summary || "The AI could not read enough of the docs, so the guide was not changed.");
      } else {
        toast.success("The guide is written and saved as a new version.");
        done.current();
      }
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return { allowed, blocked, busy: busy || active, run, start };
}

export function GuideAiButton({ ai, hasGuide }: { ai: GuideAiState; hasGuide: boolean }) {
  if (!ai.allowed) return null;
  return (
    <Button
      size="small"
      variant="secondary"
      leadingIcon={ai.busy ? ButtonSpinner : RiSparkling2Line}
      onClick={() => void ai.start()}
      disabled={ai.busy || !!ai.blocked}
      title={ai.blocked ?? undefined}
      aria-busy={ai.busy || undefined}
    >
      {hasGuide ? "Rewrite with AI" : "Write guide with AI"}
    </Button>
  );
}

/** Progress of a queued run, with a link to its page. Nothing when there is no run. */
export function GuideAiProgress({ ai }: { ai: GuideAiState }) {
  const run = ai.run;
  if (!run) return null;
  const link = (
    <Link
      to={`/providers/ai/${run.id}`}
      className="rounded-sm text-text-primary underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
    >
      Open the run
    </Link>
  );
  if (ACTIVE_STATUSES.has(run.status))
    return (
      <Callout tone="info" icon={<SpinnerGlyph size="sm" className="text-current" />}>
        AI is reading the docs and writing the guide. {link}
      </Callout>
    );
  if (run.status === "needs_review")
    return <Callout tone="warning">The AI wrote a guide that needs your review before it is saved. {link}</Callout>;
  if (run.status === "failed" || run.status === "cancelled")
    return <Callout tone="error">The AI run did not finish. {link}</Callout>;
  return null;
}
