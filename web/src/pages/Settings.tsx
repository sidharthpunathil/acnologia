import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Bot, CheckCircle2, FlaskConical, KeyRound, Lock, RefreshCcw, ShieldCheck, Terminal } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { dateTime, relTime } from "../lib/format";
import type { AiRules } from "../lib/types";
import { useAuth } from "../stores/auth";
import { useAi } from "../stores/ai";
import { toast } from "../stores/toast";
import { useTitle } from "../hooks/useTitle";
import { Badge, Button, Callout, Field, PageHeader, Section, Skeleton, Textarea } from "../components/ui";

const RULES_PLACEHOLDER = `One rule per line. For example:
Always set max_concurrency for voice providers.
Prefer proxy mode for public websites.
Name services in lowercase.
Never auto publish a provider without at least one test per rule.`;

const GUARDRAILS = [
  { icon: FlaskConical, text: "Every spec is validated and fixture tested by the engine before it can be published." },
  { icon: Lock, text: "The AI writes data only. It cannot execute code on this server." },
  { icon: KeyRound, text: "Test keys are encrypted during a run and wiped when the run ends." },
  { icon: RefreshCcw, text: "A run is capped at 40 agent turns, and failed submits go back to the agent at most 3 times." },
  { icon: CheckCircle2, text: "Every publish is a new provider version, so any change can be rolled back in one click." },
];

export function SettingsPage() {
  useTitle("Settings");
  const role = useAuth((s) => s.user?.role);
  const canEdit = can.users(role); // superadmin only
  const { status, statusError, fetchStatus } = useAi();
  const [rules, setRules] = useState<AiRules | null>(null);
  const [text, setText] = useState("");
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    void fetchStatus();
    api
      .getAiRules()
      .then((r) => {
        setRules(r);
        setText(r.rules ?? "");
      })
      .catch((e) => setLoadErr(errorMessage(e)));
  }, [fetchStatus]);

  const dirty = rules !== null && text !== (rules.rules ?? "");

  const save = async () => {
    setSaving(true);
    try {
      const r = await api.putAiRules(text);
      setRules(r);
      setText(r.rules ?? "");
      toast.success("AI rules saved. They apply to the next AI action.");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      <PageHeader title="Settings" meta="AI behavior and guardrails for this Cogito" />

      <div className="grid grid-cols-1 items-start gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
        <Section
          title="AI rules"
          description="Policy every AI action must follow: the provider builder, and every Fill with AI button. Write it in plain language."
          actions={rules?.updated_at ? <span className="font-mono text-[11px] text-faint" title={dateTime(rules.updated_at)}>updated {relTime(rules.updated_at)} by {rules.updated_by || "unknown"}</span> : undefined}
        >
          {loadErr ? (
            <Callout tone="red">Could not load the rules: {loadErr}</Callout>
          ) : rules === null ? (
            <Skeleton className="h-64 rounded-xl" />
          ) : (
            <>
              <Field
                label="Rules"
                hint={canEdit ? "Saved rules are sent with every AI request. Keep them short and specific." : "Only a superadmin can change these rules."}
              >
                <Textarea
                  rows={14}
                  value={text}
                  readOnly={!canEdit}
                  onChange={(e) => setText(e.target.value)}
                  placeholder={RULES_PLACEHOLDER}
                  className="min-h-[280px] font-mono text-[13px] leading-relaxed read-only:bg-soft"
                />
              </Field>
              {canEdit && (
                <div className="mt-4 flex justify-end gap-2">
                  {dirty && (
                    <Button variant="ghost" onClick={() => setText(rules.rules ?? "")}>
                      Discard
                    </Button>
                  )}
                  <Button variant="primary" disabled={!dirty} loading={saving} onClick={() => void save()}>
                    Save rules
                  </Button>
                </div>
              )}
            </>
          )}
        </Section>

        <div className="space-y-5">
          <Section title="AI status">
            {status === null && !statusError ? (
              <Skeleton className="h-10 rounded-xl" />
            ) : status?.enabled ? (
              <div className="flex items-start gap-3">
                <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-[10px] bg-emerald-50 text-emerald-700">
                  <Bot className="h-4 w-4" />
                </span>
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-[14px] font-medium text-ink">
                    Enabled <Badge tone="green">ready</Badge>
                  </div>
                  <div className="mt-0.5 truncate font-mono text-xs text-muted">{status.model}</div>
                </div>
              </div>
            ) : (
              <Callout tone="amber" icon={<Terminal className="h-4 w-4" />}>
                <div className="font-medium">AI is off</div>
                <div className="mt-0.5">
                  {status?.reason || statusError || "No API key configured."} Add <code className="font-mono text-[12px]">ANTHROPIC_API_KEY</code> to <code className="font-mono text-[12px]">.env</code> and restart.
                </div>
              </Callout>
            )}
            <Link to="/providers/ai" className="mt-4 inline-block text-[13px] font-medium text-ink underline-offset-2 hover:underline">
              Open the provider builder
            </Link>
          </Section>

          <Section title="Built in guardrails" description="Always on. Your rules add to these and cannot turn them off.">
            <ul className="space-y-3">
              {GUARDRAILS.map((g) => (
                <li key={g.text} className="flex items-start gap-2.5 text-[13px] leading-relaxed text-ink">
                  <g.icon className="mt-0.5 h-4 w-4 shrink-0 text-muted" aria-hidden="true" />
                  {g.text}
                </li>
              ))}
            </ul>
            <p className="mt-4 flex items-center gap-1.5 text-xs text-muted">
              <ShieldCheck className="h-3.5 w-3.5" aria-hidden="true" /> Rules are guidance for the model; the engine checks still decide what ships.
            </p>
          </Section>
        </div>
      </div>
    </div>
  );
}
