import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  RiCheckboxCircleLine,
  RiFlaskLine,
  RiHistoryLine,
  RiLockLine,
  RiRobot2Line,
  RiShieldCheckLine,
} from "@remixicon/react";
import { AcnKeyVault } from "@/components/foundations/icons/acn";
import { api, errorMessage } from "@/lib/api";
import { dateTime, num, plural, relTime, sentence } from "@/lib/format";
import { envOf, type SettingsSummary } from "@/lib/settings";
import type { AiRules } from "@/lib/types";
import { useAi } from "@/stores/ai";
import { toast } from "@/stores/toast";
import { Chip } from "@/components/base/badges/chip";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { Textarea } from "@/components/base/textarea/textarea";
import { ClaudeLogo } from "@/components/ClaudeLogo";
import { cx } from "@/utils/cx";
import { ModelsCard } from "./ModelsCard";
import { SAVE_SHORTCUT, useLoad, useSaveShortcut, type Loaded } from "./hooks";
import { EnvValue, LinkRow, ReadOnlyNote, SaveBar, SectionError, SettingRow, SettingsCard, SettingsSection } from "./parts";

/** The server's cap on the rules text (PUT /ai/rules). */
const MAX_RULES = 20000;

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

function lineCount(s: string) {
  return s === "" ? 0 : s.split("\n").length;
}

/** Whether AI is on, and with which model. Every role sees this. */
function StatusRow() {
  const { status, statusError, fetchStatus } = useAi();
  useEffect(() => {
    void fetchStatus();
  }, [fetchStatus]);

  let value;
  if (status === null && !statusError) {
    value = (
      <span className="flex flex-col gap-1.5 sm:items-end" aria-hidden>
        <Skeleton shape="text" className="h-5 w-24" />
        <Skeleton shape="text" className="h-3 w-36" />
      </span>
    );
  } else if (status?.enabled) {
    value = (
      <span className="flex flex-col gap-1 sm:items-end">
        <Chip variant="caption" color="lime" className="w-fit">
          On
        </Chip>
        <span dir="ltr" className="font-mono text-caption-1-regular wrap-anywhere text-text-secondary">
          {status.model}
        </span>
      </span>
    );
  } else {
    value = (
      <span className="flex flex-col gap-1 sm:items-end">
        <Chip variant="caption" color="yellow" className="w-fit">
          Off
        </Chip>
        <span className="text-caption-1-regular wrap-anywhere text-text-secondary">
          {sentence(status?.reason || statusError || "Add the AI key to .env, then restart Acnologia")}
        </span>
      </span>
    );
  }

  return (
    <SettingRow
      label={
        <span className="inline-flex items-center gap-2">
          <ClaudeLogo className="size-4" />
          AI
        </span>
      }
      description="Powers the Assistant, the provider builder and every Fill with AI button."
    >
      {value}
    </SettingRow>
  );
}

/** The rules editor: a superadmin edits, everyone else reads. */
function RulesCard({ canEdit, onDirty }: { canEdit: boolean; onDirty: (dirty: boolean) => void }) {
  const rules = useLoad(api.getAiRules);
  const [text, setText] = useState("");
  const [saving, setSaving] = useState(false);
  const form = useRef<HTMLFormElement>(null);

  useEffect(() => {
    if (rules.data) setText(rules.data.rules ?? "");
  }, [rules.data]);

  const saved = rules.data?.rules ?? "";
  const dirty = rules.data !== null && text !== saved;

  useEffect(() => {
    onDirty(canEdit && dirty);
    return () => onDirty(false);
  }, [canEdit, dirty, onDirty]);

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!dirty || saving) return;
    setSaving(true);
    try {
      const r: AiRules = await api.putAiRules(text);
      rules.set(r);
      toast.success("Rules saved. The next AI action follows them.");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };
  useSaveShortcut(form, canEdit && dirty, () => void save());

  if (rules.error) return <SectionError what="the AI rules" message={rules.error} onRetry={rules.reload} />;

  const updated = rules.data?.updated_at ? (
    <span title={dateTime(rules.data.updated_at)}>
      Saved {relTime(rules.data.updated_at)} by {rules.data.updated_by || "someone"}
    </span>
  ) : rules.data ? (
    "No rules saved yet"
  ) : null;

  const lines = lineCount(text);
  const over = text.length > MAX_RULES;
  const editor = (
    <div className="px-4 pt-4 pb-4 sm:px-5">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <div className="min-w-0">
          <div id="ai-rules-label" className="text-body-medium text-text-primary">
            Rules
          </div>
          <div className="mt-0.5 text-body-2-regular text-text-secondary">
            Plain language rules sent with every AI request. Short, specific rules work best.
          </div>
        </div>
        {!canEdit && rules.data && <span className="text-caption-1-regular text-text-tertiary">{updated}</span>}
      </div>
      {rules.data === null ? (
        <Skeleton className="h-[248px] rounded-2lg" />
      ) : (
        <Textarea
          aria-labelledby="ai-rules-label"
          aria-describedby="ai-rules-count"
          rows={10}
          autoResize
          maxRows={22}
          value={text}
          isReadOnly={!canEdit}
          onChange={setText}
          placeholder={canEdit ? RULES_PLACEHOLDER : "No rules yet."}
          spellCheck="false"
          inputClassName="font-mono text-body-2-regular"
        />
      )}
      <p
        id="ai-rules-count"
        className={cx("mt-2 text-caption-1-regular tabular-nums", over ? "text-text-error-primary" : "text-text-tertiary")}
      >
        {rules.data === null ? " " : `${plural(lines, "line")} · ${num(text.length)} of ${num(MAX_RULES)} characters`}
      </p>
    </div>
  );

  if (!canEdit) {
    return (
      <SettingsCard>
        {editor}
        <div className="px-4 py-3 sm:px-5">
          <ReadOnlyNote>Only a superadmin can change the rules.</ReadOnlyNote>
        </div>
      </SettingsCard>
    );
  }
  return (
    <form ref={form} onSubmit={(e) => void save(e)} aria-label="AI rules">
      <SettingsCard>
        {editor}
        <SaveBar dirty={dirty} saving={saving} onDiscard={() => setText(saved)} saveLabel="Save rules" status={updated} shortcut={SAVE_SHORTCUT} />
      </SettingsCard>
    </form>
  );
}

/** Always on. Kept quiet: a muted list under the editor. */
function Guardrails() {
  return (
    <div className="rounded-3xl bg-background-secondary-default px-4 py-4 sm:px-5">
      <h3 className="flex items-center gap-1.5 text-caption-1-semibold text-text-secondary">
        <RiShieldCheckLine className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
        Built in guardrails, always on
      </h3>
      <p className="mt-1 text-caption-1-regular text-text-tertiary">Your rules add to these and cannot turn them off. The engine checks still decide what goes live.</p>
      <ul className="mt-3 flex flex-col gap-2">
        {GUARDRAILS.map((g) => (
          <li key={g.text} className="flex items-start gap-2 text-body-2-regular text-text-secondary">
            <g.icon className="mt-0.5 size-3.5 shrink-0 text-foreground-icon-tertiary" aria-hidden />
            {g.text}
          </li>
        ))}
      </ul>
    </div>
  );
}

export function AiSection({
  isAdmin,
  isSuper,
  summary,
  onDirty,
  onModelsDirty,
}: {
  isAdmin: boolean;
  isSuper: boolean;
  summary: Loaded<SettingsSummary>;
  onDirty: (dirty: boolean) => void;
  onModelsDirty: (dirty: boolean) => void;
}) {
  const env = (name: string) => envOf(summary.data, name);
  return (
    <SettingsSection id="ai" title="AI" description="Which model runs, when long chats are compacted, and the rules every AI action follows.">
      <SettingsCard>
        <StatusRow />
        {isAdmin &&
          (summary.error ? (
            <div className="px-4 py-4 sm:px-5">
              <SectionError what="the AI configuration" message={summary.error} onRetry={summary.reload} />
            </div>
          ) : (
            <>
              <SettingRow label="API key" description="Anthropic key used for every AI call. Restart after changing it.">
                <EnvValue env={env("ANTHROPIC_API_KEY")} unsetLabel="AI stays off" />
              </SettingRow>
              <SettingRow label="Chat compaction" description="When an Assistant thread's prompt grows past this size, older turns are summarized.">
                <EnvValue
                  env={env("ACNOLOGIA_CHAT_COMPACT_TOKENS")}
                  format={(v) => `${num(Number(v))} tokens`}
                  unsetLabel="60% of the Assistant model's context window"
                />
              </SettingRow>
            </>
          ))}
        <LinkRow href="/providers/ai" icon={RiRobot2Line} label="Provider builder" description="Describe an API and let the AI write and test its spec." summary="" />
      </SettingsCard>
      {isAdmin && <ModelsCard onDirty={onModelsDirty} onSaved={summary.reload} />}
      <RulesCard canEdit={isSuper} onDirty={onDirty} />
      <Guardrails />
    </SettingsSection>
  );
}
