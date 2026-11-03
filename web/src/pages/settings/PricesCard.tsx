import { useState } from "react";
import { RiEditLine, RiExternalLinkLine, RiHistoryLine } from "@remixicon/react";
import { errorMessage } from "@/lib/api";
import { compact } from "@/lib/format";
import { modelsApi, type ModelInfo, type ModelsCatalog, type PriceOverride } from "@/lib/settings";
import { safeHref } from "@/lib/url";
import { toast } from "@/stores/toast";
import { Accordion, AccordionItem } from "@/components/base/accordion/accordion";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { Input } from "@/components/base/input/input";
import { NumberField } from "@/components/base/number-field/number-field";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { ReadOnlyNote } from "./parts";

/** "$0.125": prices per million tokens keep their cents and fractions. */
function money(n: number | undefined) {
  if (n === undefined) return "-";
  return `$${Number(n.toFixed(4))}`;
}

function host(u: string) {
  try {
    const x = new URL(u);
    return x.host.replace(/^www\./, "");
  } catch {
    return u;
  }
}

/** Where a price comes from and when it was read. */
function PriceSourceLine({ m }: { m: ModelInfo }) {
  if (!m.pricing_known) return <span>No price known: costs are estimated at the default model's price.</span>;
  const href = m.price_url ? safeHref(m.price_url) : undefined;
  const what =
    m.price_source === "manual"
      ? `Edited${m.override?.updated_by ? ` by ${m.override.updated_by}` : ""}${m.price_checked ? ` on ${m.price_checked}` : ""}`
      : m.price_source === "refresh"
        ? `Updated by the price refresh on ${m.price_checked}`
        : `Built in, prices checked ${m.price_checked ?? "at release"}`;
  return (
    <span className="inline-flex flex-wrap items-center gap-x-1.5">
      <span>{what}</span>
      {href && (
        <a
          href={href}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex items-center gap-1 rounded-sm underline-offset-2 outline-none hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
        >
          {host(href)}
          <RiExternalLinkLine className="size-3" aria-hidden />
          <span className="sr-only">(price source, opens in a new tab)</span>
        </a>
      )}
    </span>
  );
}

function Editor({ m, onSaved, onCancel }: { m: ModelInfo; onSaved: (c: ModelsCatalog) => void; onCancel: () => void }) {
  const [d, setD] = useState<PriceOverride>({
    input: m.input_price,
    output: m.output_price,
    cache_write: m.cache_write_price ?? m.input_price * 1.25,
    cache_read: m.cache_read_price ?? m.input_price * 0.1,
    context_window: m.override?.context_window || m.context_window,
    source_url: m.override?.source_url ?? m.price_url ?? "",
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const num = (k: keyof PriceOverride) => (v: number) => setD((x) => ({ ...x, [k]: Number.isFinite(v) ? v : 0 }));

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const body = { ...d, context_window: d.context_window === m.context_window && !m.override?.context_window ? 0 : d.context_window };
      onSaved(await modelsApi.setPrice(m.id, body));
      toast.success(`Saved the price of ${m.display_name}. The next AI call is priced with it.`);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const field = (label: string, k: "input" | "output" | "cache_write" | "cache_read") => (
    <NumberField label={label} value={d[k]} onChange={num(k)} minValue={0} maxValue={1000} step={0.01} formatOptions={{ style: "currency", currency: "USD", maximumFractionDigits: 4 }} />
  );

  return (
    <form
      className="mt-3 flex flex-col gap-3 rounded-2xl bg-background-secondary-default p-3"
      aria-label={`Price of ${m.display_name}`}
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <p className="text-caption-1-regular text-text-secondary">USD per million tokens. Leave a cache price at 0 to use 1.25x (writes) or 0.1x (reads) of input.</p>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {field("Input", "input")}
        {field("Output", "output")}
        {field("Cache write", "cache_write")}
        {field("Cache read", "cache_read")}
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[12rem_minmax(0,1fr)]">
        <NumberField label="Context window (tokens)" value={d.context_window ?? 0} onChange={num("context_window")} minValue={0} maxValue={100_000_000} step={1000} />
        <Input label="Source page" value={d.source_url ?? ""} onChange={(v) => setD((x) => ({ ...x, source_url: v }))} placeholder="https://" />
      </div>
      {error && <p className="text-caption-1-regular text-text-error-primary wrap-anywhere">{error}</p>}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="small" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" size="small" leadingIcon={saving ? ButtonSpinner : undefined} disabled={saving}>
          Save price
        </Button>
      </div>
    </form>
  );
}

/**
 * The price of each model, used to account for what the AI features cost.
 * A superadmin can override one (prices change) and reset it to the built
 * in table; the provider refresh can also re-read Anthropic's pricing page.
 */
export function PricesCard({ data, canEdit, onChange }: { data: ModelsCatalog; canEdit: boolean; onChange: (c: ModelsCatalog) => void }) {
  const [editing, setEditing] = useState<string | null>(null);
  const edited = data.models.filter((m) => m.override).length;

  const reset = async (m: ModelInfo) => {
    try {
      onChange(await modelsApi.resetPrice(m.id));
      toast.success(`${m.display_name} is back to the built in price.`);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  return (
    <div className="px-4 sm:px-5">
      <Accordion>
        <AccordionItem
          id="prices"
          title={<span className="text-body-medium">Model prices</span>}
          description={
            <span className="text-body-2-regular">{edited > 0 ? `${edited} edited, the rest built in` : "Built in prices, used to account for AI cost"}</span>
          }
        >
          <ul className="-mx-4 divide-y divide-separator-border border-t border-separator-border sm:-mx-5">
            {data.models.map((m) => (
              <li key={m.id} className="px-4 py-3 sm:px-5">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-baseline gap-x-2">
                      <span className="text-body-medium text-text-primary">{m.display_name}</span>
                      <span dir="ltr" className="font-mono text-caption-1-regular text-text-secondary wrap-anywhere">
                        {m.id}
                      </span>
                      {m.override && (
                        <Chip variant="caption" color={m.price_source === "refresh" ? "blue" : "purple"}>
                          {m.price_source === "refresh" ? "Refreshed" : "Edited"}
                        </Chip>
                      )}
                    </div>
                    <div className="mt-1 text-caption-1-regular text-text-secondary tabular-nums">
                      {m.pricing_known
                        ? `In ${money(m.input_price)} · out ${money(m.output_price)} · cache write ${money(m.cache_write_price)} · cache read ${money(m.cache_read_price)} per M tokens · ${compact(m.context_window)} context`
                        : `${compact(m.context_window)} context`}
                    </div>
                    <div className="mt-0.5 text-caption-1-regular text-text-tertiary">
                      <PriceSourceLine m={m} />
                    </div>
                  </div>
                  {canEdit && editing !== m.id && (
                    <div className="flex shrink-0 items-center gap-1">
                      {m.override && (
                        <ConfirmButton
                          label="Reset to built in"
                          confirmLabel="Reset"
                          description="The saved price goes; the built in table prices this model again."
                          icon={RiHistoryLine}
                          size="small"
                          onConfirm={() => reset(m)}
                        />
                      )}
                      <Button variant="ghost" size="small" leadingIcon={RiEditLine} onClick={() => setEditing(m.id)}>
                        Edit
                      </Button>
                    </div>
                  )}
                </div>
                {editing === m.id && (
                  <Editor
                    m={m}
                    onCancel={() => setEditing(null)}
                    onSaved={(c) => {
                      onChange(c);
                      setEditing(null);
                    }}
                  />
                )}
              </li>
            ))}
          </ul>
          {!canEdit && <ReadOnlyNote className="py-3">Only a superadmin can edit prices.</ReadOnlyNote>}
        </AccordionItem>
      </Accordion>
    </div>
  );
}
