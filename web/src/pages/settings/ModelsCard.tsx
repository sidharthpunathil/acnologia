import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { errorMessage } from "@/lib/api";
import { compact, dateTime, plural, relTime } from "@/lib/format";
import {
  MODEL_FEATURES,
  modelsApi,
  type ModelChoice,
  type ModelFeature,
  type ModelInfo,
  type ModelsInput,
  type ModelSource,
} from "@/lib/settings";
import { toast } from "@/stores/toast";
import { Accordion, AccordionItem } from "@/components/base/accordion/accordion";
import { Select, SelectItem } from "@/components/base/select/select";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { Callout } from "@/components/acn/callout";
import { SAVE_SHORTCUT, useLoad, useSaveShortcut } from "./hooks";
import { ReadOnlyNote, RowsSkeleton, SaveBar, SectionError, SettingRow, SettingsCard } from "./parts";

/** The Select key for "use the default" (an empty setting). */
const DEFAULT_KEY = "__default__";

const FEATURES: Record<ModelFeature, { label: string; hint: string }> = {
  assistant: {
    label: "Assistant",
    hint: "Chats with tools. Its context window also sets when long chats are compacted.",
  },
  builder: {
    label: "Provider builder",
    hint: "Long research runs that write and test a provider spec. A larger model writes better specs.",
  },
  fill: {
    label: "Fill with AI",
    hint: "One short form at a time. Works well on a smaller, cheaper model such as Haiku.",
  },
  summary: {
    label: "Chat summaries",
    hint: "Compacts long Assistant chats. Works well on a smaller, cheaper model such as Haiku.",
  },
  refresh: {
    label: "Offer refresh",
    hint: "Re-checking provider offers, limits and pricing works well on a small model such as Haiku.",
  },
};

const SOURCE_LABEL: Record<ModelSource, string> = {
  override: "Set for this feature",
  saved: "The saved default",
  env: "From ACNOLOGIA_AI_MODEL",
  builtin: "Built in default",
};

const EMPTY: ModelsInput = { default: "", assistant: "", builder: "", fill: "", summary: "", refresh: "" };

function usd(n: number) {
  return `$${n < 1 ? n.toFixed(2) : Number.isInteger(n) ? String(n) : n.toFixed(2)}`;
}

/** "1M context · $4 / $20 per M tokens", or "price unknown". */
function modelFacts(m: ModelInfo) {
  const price = m.pricing_known ? `${usd(m.input_price)} / ${usd(m.output_price)} per M in/out` : "Price unknown";
  return `${compact(m.context_window)} context · ${price}`;
}

/** Name over id, with the window and price under them. */
function ModelOption({ name, id, facts }: { name: ReactNode; id?: string; facts?: string }) {
  return (
    <span className="flex min-w-0 flex-col gap-0.5 py-0.5">
      <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
        <span className="text-body-medium text-text-primary">{name}</span>
        {id && (
          <span dir="ltr" className="font-mono text-caption-1-regular wrap-anywhere text-text-secondary">
            {id}
          </span>
        )}
      </span>
      {facts && <span className="text-caption-1-regular text-text-tertiary tabular-nums">{facts}</span>}
    </span>
  );
}

/** The trigger's value: name and id on one line. */
function ValueLabel({ name, id }: { name: string; id?: string }) {
  return (
    <span className="flex min-w-0 items-baseline gap-2 truncate">
      <span className="truncate">{name}</span>
      {id && (
        <span dir="ltr" className="truncate font-mono text-caption-1-regular text-text-tertiary">
          {id}
        </span>
      )}
    </span>
  );
}

function ModelSelect({
  value,
  onChange,
  models,
  defaultLabel,
  defaultId,
  defaultFacts,
  labelledBy,
  describedBy,
}: {
  value: string;
  onChange: (v: string) => void;
  models: ModelInfo[];
  defaultLabel: string;
  defaultId: string;
  defaultFacts?: string;
  labelledBy: string;
  describedBy?: string;
}) {
  // A saved id the list no longer offers stays pickable, so the select can show it.
  const items = useMemo(() => {
    if (value === "" || models.some((m) => m.id === value)) return models;
    return [...models, { id: value, display_name: value, context_window: 0, input_price: 0, output_price: 0, pricing_known: false, builtin: false }];
  }, [models, value]);
  const current = items.find((m) => m.id === value);

  return (
    <Select
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      selectedKey={value === "" ? DEFAULT_KEY : value}
      onSelectionChange={(k) => k != null && onChange(k === DEFAULT_KEY ? "" : String(k))}
      className="w-full sm:w-80"
      popoverClassName="w-(--trigger-width) min-w-72"
      renderValue={current ? <ValueLabel name={current.display_name} id={current.id} /> : <ValueLabel name={defaultLabel} id={defaultId} />}
    >
      <SelectItem id={DEFAULT_KEY} textValue={defaultLabel}>
        <ModelOption name={defaultLabel} id={defaultId} facts={defaultFacts} />
      </SelectItem>
      {items.map((m) => (
        <SelectItem key={m.id} id={m.id} textValue={m.display_name}>
          <ModelOption name={m.display_name} id={m.display_name === m.id ? undefined : m.id} facts={m.context_window ? modelFacts(m) : "Not in the current list"} />
        </SelectItem>
      ))}
    </Select>
  );
}

/** The effective model as text, for roles that cannot change it. */
function ReadOnlyModel({ choice, models }: { choice: ModelChoice; models: ModelInfo[] }) {
  const m = models.find((x) => x.id === choice.model);
  return (
    <div className="flex flex-col gap-1 sm:items-end">
      <span dir="ltr" className="font-mono text-body-2-regular wrap-anywhere text-text-primary">
        {choice.model}
      </span>
      <span className="text-caption-1-regular text-text-tertiary">
        {SOURCE_LABEL[choice.source]}
        {m ? ` · ${modelFacts(m)}` : null}
      </span>
    </div>
  );
}

/**
 * The default model and the per feature overrides. A superadmin picks and
 * saves; admins see what each feature runs on and why.
 */
export function ModelsCard({ onDirty, onSaved }: { onDirty: (dirty: boolean) => void; onSaved: () => void }) {
  const load = useLoad(modelsApi.list);
  const data = load.data;
  const [draft, setDraft] = useState<ModelsInput>(EMPTY);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const form = useRef<HTMLFormElement>(null);

  const saved: ModelsInput = useMemo(() => {
    const s = data?.settings;
    return s ? { default: s.default, assistant: s.assistant, builder: s.builder, fill: s.fill, summary: s.summary, refresh: s.refresh ?? "" } : EMPTY;
  }, [data]);
  useEffect(() => setDraft(saved), [saved]);

  const canEdit = !!data?.can_edit;
  const dirty = data !== null && (Object.keys(EMPTY) as (keyof ModelsInput)[]).some((k) => draft[k] !== saved[k]);
  useEffect(() => {
    onDirty(canEdit && dirty);
    return () => onDirty(false);
  }, [canEdit, dirty, onDirty]);

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!dirty || saving || !data) return;
    setSaving(true);
    setError(null);
    try {
      const r = await modelsApi.save(draft);
      load.set({ ...data, ...r });
      const warn = r.warnings?.length ? ` ${r.warnings.join(" ")}` : "";
      toast.success(`Models saved. The next AI call uses them.${warn}`);
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };
  useSaveShortcut(form, canEdit && dirty, () => void save());

  if (load.error) {
    return (
      <SettingsCard>
        <div className="px-4 py-4 sm:px-5">
          <SectionError what="the AI models" message={load.error} onRetry={load.reload} />
        </div>
      </SettingsCard>
    );
  }
  if (!data) {
    return (
      <SettingsCard>
        <RowsSkeleton rows={1} />
        <div className="px-4 py-4 sm:px-5" aria-hidden>
          <Skeleton shape="text" className="h-4 w-48" />
        </div>
      </SettingsCard>
    );
  }

  const models = data.models;
  // What "use the default" falls back to: ACNOLOGIA_AI_MODEL, else built in.
  const fallbackId = data.env_model || data.builtin_model;
  const fallbackFrom = data.env_model ? "From ACNOLOGIA_AI_MODEL" : "Built in default";
  const fallbackModel = models.find((m) => m.id === fallbackId);
  // The default each feature would follow with the draft as it stands.
  const draftDefaultId = draft.default || fallbackId;
  const draftDefault = models.find((m) => m.id === draftDefaultId);
  const overrides = MODEL_FEATURES.filter((f) => saved[f] !== "").length;

  const listNote = data.source === "builtin" && data.note ? data.note : null;
  const keyNote = !data.ai_enabled ? (
    <Callout tone="warning" title="AI is off until ANTHROPIC_API_KEY is set">
      Your choice is saved now and used once the key is set and Acnologia restarts.
    </Callout>
  ) : null;

  const updated = data.settings.updated_at ? (
    <span title={dateTime(data.settings.updated_at)}>
      Saved {relTime(data.settings.updated_at)} by {data.settings.updated_by || "someone"}
    </span>
  ) : (
    "No model saved yet"
  );

  if (!canEdit) {
    return (
      <SettingsCard>
        <SettingRow label="Default model" description="The Claude model every AI feature uses unless it has its own.">
          <ReadOnlyModel choice={data.effective.default} models={models} />
        </SettingRow>
        {MODEL_FEATURES.map((f) => (
          <SettingRow key={f} label={FEATURES[f].label} description={FEATURES[f].hint}>
            <ReadOnlyModel choice={data.effective[f]} models={models} />
          </SettingRow>
        ))}
        <div className="px-4 py-3 sm:px-5">
          <ReadOnlyNote>Only a superadmin can switch models.</ReadOnlyNote>
        </div>
      </SettingsCard>
    );
  }

  return (
    <form ref={form} onSubmit={(e) => void save(e)} aria-label="AI models">
      <SettingsCard>
        <SettingRow
          labelId="ai-model-default"
          label="Model"
          description={
            <>
              <span id="ai-model-default-hint">The Claude model behind the Assistant, the builder and Fill with AI. Takes effect on the next AI call.</span>
              {listNote && <span className="mt-1 block text-caption-1-regular text-text-tertiary">{listNote}</span>}
            </>
          }
        >
          <ModelSelect
            value={draft.default}
            onChange={(v) => setDraft((d) => ({ ...d, default: v }))}
            models={models}
            defaultLabel="Use default"
            defaultId={fallbackId}
            defaultFacts={fallbackModel ? `${fallbackFrom} · ${modelFacts(fallbackModel)}` : fallbackFrom}
            labelledBy="ai-model-default"
            describedBy="ai-model-default-hint"
          />
        </SettingRow>
        {keyNote && <div className="px-4 py-4 sm:px-5">{keyNote}</div>}
        <div className="px-4 sm:px-5">
          <Accordion defaultExpandedKeys={overrides > 0 ? ["features"] : []}>
            <AccordionItem
              id="features"
              title={<span className="text-body-medium">Use a different model for...</span>}
              description={
                <span className="text-body-2-regular">
                  {overrides > 0 ? `${plural(overrides, "feature")} with its own model` : "Every feature uses the default model"}
                </span>
              }
            >
              <div className="-mx-4 divide-y divide-separator-border border-t border-separator-border sm:-mx-5">
                {MODEL_FEATURES.map((f) => (
                  <SettingRow key={f} labelId={`ai-model-${f}`} label={FEATURES[f].label} description={<span id={`ai-model-${f}-hint`}>{FEATURES[f].hint}</span>}>
                    <ModelSelect
                      value={draft[f]}
                      onChange={(v) => setDraft((d) => ({ ...d, [f]: v }))}
                      models={models}
                      defaultLabel="Same as default"
                      defaultId={draftDefaultId}
                      defaultFacts={draftDefault ? modelFacts(draftDefault) : undefined}
                      labelledBy={`ai-model-${f}`}
                      describedBy={`ai-model-${f}-hint`}
                    />
                  </SettingRow>
                ))}
              </div>
            </AccordionItem>
          </Accordion>
        </div>
        {error && (
          <div className="px-4 py-3 sm:px-5">
            <Callout tone="error" title="Models not saved">
              <span className="wrap-anywhere">{error}</span>
            </Callout>
          </div>
        )}
        <SaveBar
          dirty={dirty}
          saving={saving}
          onDiscard={() => {
            setDraft(saved);
            setError(null);
          }}
          saveLabel="Save models"
          status={updated}
          shortcut={SAVE_SHORTCUT}
        />
      </SettingsCard>
    </form>
  );
}
