import { useEffect, useState } from "react";
import { Link } from "react-aria-components";
import {
  RiArrowRightLine,
  RiCheckboxCircleLine,
  RiFlaskLine,
  RiHistoryLine,
  RiLockLine,
  RiShieldCheckLine,
  RiTerminalBoxLine,
} from "@remixicon/react";
import { AcnKeyVault } from "@/components/foundations/icons/acn";
import { api, errorMessage } from "@/lib/api";
import { can } from "@/lib/perms";
import { dateTime, relTime, sentence } from "@/lib/format";
import type { AiRules } from "@/lib/types";
import { useAuth } from "@/stores/auth";
import { useAi } from "@/stores/ai";
import { toast } from "@/stores/toast";
import { useTitle } from "@/hooks/useTitle";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Textarea } from "@/components/base/textarea/textarea";
import { Callout } from "@/components/acn/callout";
import { PageHeader } from "@/components/acn/page-header";
import { Section } from "@/components/acn/section";
import { ClaudeLogo } from "@/components/ClaudeLogo";

const RULES_PLACEHOLDER = `One rule per line. For example:
Always set max_concurrency for voice providers.
Prefer proxy mode for public websites.
Name services in lowercase.
Never auto publish a provider without at least one test per rule.`;

const GUARDRAILS = [
  { icon: RiFlaskLine, text: "The engine validates every spec and runs its tests before it can go live." },
  { icon: RiLockLine, text: "The AI writes data only. It cannot run code on this server." },
  { icon: AcnKeyVault, text: "Test keys stay encrypted during a run and are wiped when it ends." },
  { icon: RiHistoryLine, text: "A run stops after 40 turns. A failed submit goes back to the agent at most 3 times." },
  { icon: RiCheckboxCircleLine, text: "Every publish is a new provider version, so you can roll back any change in one click." },
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
      toast.success("Rules saved. The next AI action follows them.");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      <PageHeader title="Settings" meta="AI rules and guardrails" />

      <div className="grid grid-cols-1 items-start gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
        <Section
          title="AI rules"
          description="Plain language rules the AI follows everywhere: in the provider builder and in every Fill with AI button."
          actions={
            rules?.updated_at ? (
              <span className="font-mono text-caption-1-regular text-text-tertiary" title={dateTime(rules.updated_at)}>
                updated {relTime(rules.updated_at)} by {rules.updated_by || "someone"}
              </span>
            ) : undefined
          }
        >
          {loadErr ? (
            <Callout tone="error">Could not load the rules: {loadErr}</Callout>
          ) : rules === null ? (
            <Skeleton className="h-72 rounded-2lg" />
          ) : (
            <>
              <Textarea
                label="Rules"
                hint={canEdit ? "Sent with every AI request. Short, specific rules work best." : "Only a superadmin can change these rules."}
                rows={14}
                value={text}
                isReadOnly={!canEdit}
                onChange={setText}
                placeholder={RULES_PLACEHOLDER}
                inputClassName="min-h-[280px] font-mono text-body-2-regular"
              />
              {canEdit && (
                <div className="mt-4 flex justify-end gap-2">
                  {dirty && (
                    <Button variant="ghost" onClick={() => setText(rules.rules ?? "")}>
                      Discard
                    </Button>
                  )}
                  <Button
                    variant="primary"
                    disabled={!dirty || saving}
                    leadingIcon={saving ? ButtonSpinner : undefined}
                    onClick={() => void save()}
                  >
                    Save rules
                  </Button>
                </div>
              )}
            </>
          )}
        </Section>

        <div className="flex flex-col gap-5">
          <Section title="AI status">
            {status === null && !statusError ? (
              <Skeleton className="h-10 rounded-xl" />
            ) : status?.enabled ? (
              <div className="flex items-start gap-3">
                <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-status-orange-background">
                  <ClaudeLogo className="size-[18px]" />
                </span>
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-body-medium text-text-primary">
                    Enabled
                    <Chip variant="caption" color="lime">
                      ready
                    </Chip>
                  </div>
                  <div className="mt-0.5 font-mono text-caption-1-regular wrap-anywhere text-text-secondary">{status.model}</div>
                </div>
              </div>
            ) : (
              <Callout tone="warning" icon={RiTerminalBoxLine} title="AI is off">
                <div className="mt-0.5 wrap-anywhere">
                  {sentence(status?.reason || statusError || "Add the AI key to .env, then restart Acnologia")}
                </div>
              </Callout>
            )}
            <Link
              href="/providers/ai"
              className="group mt-4 inline-flex items-center gap-1 rounded-md text-body-2-medium text-text-primary underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
            >
              Open the provider builder
              <RiArrowRightLine
                className="size-4 text-foreground-icon-secondary transition-transform duration-150 ease-out group-hover:translate-x-0.5 motion-reduce:transition-none rtl:-scale-x-100"
                aria-hidden
              />
            </Link>
          </Section>

          {can.manage(role) && (
            <Section title="Sharing between servers" description="This server's identity, shares to other servers and connections to theirs. Keys never leave the server that owns them.">
              <Link
                href="/sharing"
                className="group inline-flex items-center gap-1 rounded-md text-body-2-medium text-text-primary underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
              >
                Open Sharing
                <RiArrowRightLine
                  className="size-4 text-foreground-icon-secondary transition-transform duration-150 ease-out group-hover:translate-x-0.5 motion-reduce:transition-none rtl:-scale-x-100"
                  aria-hidden
                />
              </Link>
            </Section>
          )}

          <Section title="Built in guardrails" description="Always on. Your rules add to these and cannot turn them off.">
            <ul className="flex flex-col gap-3">
              {GUARDRAILS.map((g) => (
                <li key={g.text} className="flex items-start gap-2.5 text-body-2-regular text-text-primary">
                  <g.icon className="mt-0.5 size-4 shrink-0 text-foreground-icon-secondary" aria-hidden />
                  {g.text}
                </li>
              ))}
            </ul>
            <p className="mt-4 flex items-center gap-1.5 text-caption-1-regular text-text-secondary">
              <RiShieldCheckLine className="size-4 shrink-0" aria-hidden /> Rules guide the AI. The engine checks still decide what goes live.
            </p>
          </Section>
        </div>
      </div>
    </div>
  );
}
