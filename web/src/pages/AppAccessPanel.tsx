import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Modal } from "../components/Modal";
import { api, errorMessage } from "../lib/api";
import { usd } from "../lib/ops";
import type { App, AppControls, DenyRule, TagBudget, TagUsageReport } from "../lib/types";
import { toast } from "../stores/toast";
import {
  BLANK_LIMITS_DRAFT,
  LIMIT_FIELDS,
  LIMIT_FIELD_LABEL,
  anyLimit,
  limitsDraft,
  limitsText,
  parseLimitsDraft,
  validTagKey,
  type LimitsDraft,
} from "../lib/accessControls";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { Input } from "@/components/base/input/input";
import { Select, SelectItem } from "@/components/base/select/select";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout } from "@/components/acn";
import { ConfirmButton } from "@/components/acn/confirm-button";

const METHODS = ["*", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT"];
type TargetKind = "service" | "host" | "any";

const SECTION = "flex flex-col gap-3 rounded-2xl border border-border-button-default p-4";
const HEAD = "text-body-medium text-text-primary";
const HINT = "text-caption-1-regular text-text-secondary";

function LimitsGrid({ draft, onChange, placeholder }: { draft: LimitsDraft; onChange: (d: LimitsDraft) => void; placeholder: string }) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {LIMIT_FIELDS.map((k) => (
        <Input
          key={k}
          label={LIMIT_FIELD_LABEL[k]}
          inputMode={k.endsWith("_budget") ? "decimal" : "numeric"}
          value={draft[k]}
          onChange={(v) => onChange({ ...draft, [k]: v })}
          placeholder={placeholder}
          inputClassName="font-mono tabular-nums"
        />
      ))}
    </div>
  );
}

/** The limits every token gets unless it sets its own. */
function TokenDefaults({ app, controls, canEdit, onSaved }: { app: App; controls: AppControls; canEdit: boolean; onSaved: (c: AppControls) => void }) {
  const [draft, setDraft] = useState(limitsDraft(controls.token_limits));
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const save = async (e: FormEvent) => {
    e.preventDefault();
    const l = parseLimitsDraft(draft);
    if (typeof l === "string") return setErr(l);
    setBusy(true);
    try {
      const r = await api.setTokenDefaultLimits(app.id, l);
      onSaved({ ...controls, token_limits: r.token_limits });
      toast.success("Saved the per-token limits");
    } catch (x) {
      setErr(errorMessage(x));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form className={SECTION} onSubmit={save} noValidate>
      <div>
        <div className={HEAD}>Each token</div>
        <p className={HINT}>Every token of this app gets these limits of its own, such as each person who claimed a share link. A token's own limits win.</p>
      </div>
      <LimitsGrid
        draft={draft}
        onChange={(d) => {
          setDraft(d);
          setErr(null);
        }}
        placeholder="no limit"
      />
      {err && <Callout tone="error">{err}</Callout>}
      {canEdit && (
        <div className="flex justify-end">
          <Button size="small" variant="primary" type="submit" disabled={busy} leadingIcon={busy ? ButtonSpinner : undefined}>
            Save
          </Button>
        </div>
      )}
    </form>
  );
}

/** Budgets for each value of a caller tag key. */
function TagBudgets({ app, controls, canEdit, onSaved }: { app: App; controls: AppControls; canEdit: boolean; onSaved: (c: AppControls) => void }) {
  const [key, setKey] = useState("");
  const [draft, setDraft] = useState<LimitsDraft>(BLANK_LIMITS_DRAFT);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const put = async (budgets: TagBudget[]) => {
    setBusy(true);
    try {
      const r = await api.setTagBudgets(app.id, budgets);
      onSaved({ ...controls, tag_budgets: r.tag_budgets });
      return true;
    } catch (x) {
      setErr(errorMessage(x));
      return false;
    } finally {
      setBusy(false);
    }
  };
  const add = async (e: FormEvent) => {
    e.preventDefault();
    const k = key.trim().toLowerCase();
    if (!validTagKey(k)) return setErr("A tag key is 1 to 32 of a-z, 0-9, _ - and .");
    const l = parseLimitsDraft(draft);
    if (typeof l === "string") return setErr(l);
    if (!anyLimit(l)) return setErr("Set at least one limit.");
    const rest = controls.tag_budgets.filter((b) => b.key !== k);
    if (await put([...rest, { key: k, ...l }])) {
      setKey("");
      setDraft(BLANK_LIMITS_DRAFT);
      toast.success(`Saved the budget for each ${k}`);
    }
  };
  return (
    <form className={SECTION} onSubmit={add} noValidate>
      <div>
        <div className={HEAD}>Tag budgets</div>
        <p className={HINT}>
          Callers tag requests with <span className="font-mono">X-Acnologia-Meta: customer=cust_A</span>. A budget caps each value on its own, such as $2 a day per customer.
        </p>
      </div>
      {controls.tag_budgets.length > 0 && (
        <ul className="divide-y divide-separator-border overflow-hidden rounded-xl border border-border-button-default">
          {controls.tag_budgets.map((b) => (
            <li key={b.key} className="flex items-center justify-between gap-3 py-2 ps-3 pe-1">
              <span className="min-w-0 wrap-anywhere font-mono text-body-2-regular">
                each {b.key}: {limitsText(b)}
              </span>
              {canEdit && (
                <ConfirmButton
                  size="small"
                  label="Remove"
                  confirmLabel="Remove"
                  description={`Each ${b.key} is no longer capped.`}
                  onConfirm={async () => {
                    await put(controls.tag_budgets.filter((x) => x.key !== b.key));
                  }}
                />
              )}
            </li>
          ))}
        </ul>
      )}
      {canEdit && (
        <>
          <Input
            label="Tag key"
            value={key}
            onChange={(v) => {
              setKey(v);
              setErr(null);
            }}
            placeholder="customer"
            inputClassName="font-mono"
            autoComplete="off"
          />
          <LimitsGrid
            draft={draft}
            onChange={(d) => {
              setDraft(d);
              setErr(null);
            }}
            placeholder="no limit"
          />
          {err && <Callout tone="error">{err}</Callout>}
          <div className="flex justify-end">
            <Button size="small" variant="primary" type="submit" disabled={busy} leadingIcon={busy ? ButtonSpinner : undefined}>
              Add budget
            </Button>
          </div>
        </>
      )}
    </form>
  );
}

function ruleTarget(r: DenyRule) {
  return r.service || r.host || "any target";
}

/** Rules that block matching requests. */
function DenyRules({ app, controls, canEdit, onSaved }: { app: App; controls: AppControls; canEdit: boolean; onSaved: (c: AppControls) => void }) {
  const [method, setMethod] = useState("*");
  const [kind, setKind] = useState<TargetKind>("service");
  const [target, setTarget] = useState("");
  const [path, setPath] = useState("/");
  const [note, setNote] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const add = async (e: FormEvent) => {
    e.preventDefault();
    const t = target.trim();
    if (kind !== "any" && !t) return setErr(kind === "service" ? "Name the service." : "Name the host.");
    setBusy(true);
    try {
      const rule = await api.addDenyRule(app.id, {
        method,
        service: kind === "service" ? t : "",
        host: kind === "host" ? t : "",
        path_prefix: path.trim() || "/",
        note: note.trim(),
      });
      onSaved({ ...controls, deny_rules: [...controls.deny_rules, rule] });
      setTarget("");
      setPath("/");
      setNote("");
      toast.success("Added the rule");
    } catch (x) {
      setErr(errorMessage(x));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form className={SECTION} onSubmit={add} noValidate>
      <div>
        <div className={HEAD}>Deny rules</div>
        <p className={HINT}>Requests that match a rule are refused with 403, whatever else the app may use.</p>
      </div>
      {controls.deny_rules.length > 0 && (
        <ul className="divide-y divide-separator-border overflow-hidden rounded-xl border border-border-button-default">
          {controls.deny_rules.map((r) => (
            <li key={r.id} className="flex items-center justify-between gap-3 py-2 ps-3 pe-1">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2 font-mono text-body-2-regular">
                  <Chip variant="caption" color="rose" className="font-mono">
                    {r.method}
                  </Chip>
                  <span className="wrap-anywhere">
                    {ruleTarget(r)} {r.path_prefix}
                  </span>
                </div>
                {r.note && <div className={HINT}>{r.note}</div>}
              </div>
              {canEdit && (
                <ConfirmButton
                  size="small"
                  label="Remove"
                  confirmLabel="Remove"
                  description="Requests this rule blocks go through again."
                  onConfirm={async () => {
                    try {
                      await api.deleteDenyRule(app.id, r.id);
                      onSaved({ ...controls, deny_rules: controls.deny_rules.filter((x) => x.id !== r.id) });
                    } catch (x) {
                      toast.error(errorMessage(x));
                    }
                  }}
                />
              )}
            </li>
          ))}
        </ul>
      )}
      {canEdit && (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Select label="Method" selectedKey={method} onSelectionChange={(k) => k != null && setMethod(String(k))} className="w-full">
              {METHODS.map((m) => (
                <SelectItem key={m} id={m}>
                  {m === "*" ? "Any method" : m}
                </SelectItem>
              ))}
            </Select>
            <Select label="Applies to" selectedKey={kind} onSelectionChange={(k) => k != null && setKind(k as TargetKind)} className="w-full">
              <SelectItem id="service">A service</SelectItem>
              <SelectItem id="host">A host</SelectItem>
              <SelectItem id="any">Every target</SelectItem>
            </Select>
            {kind !== "any" && (
              <Input
                label={kind === "service" ? "Service slug" : "Host"}
                value={target}
                onChange={(v) => {
                  setTarget(v);
                  setErr(null);
                }}
                placeholder={kind === "service" ? "openai" : "api.openai.com"}
                inputClassName="font-mono"
                autoComplete="off"
              />
            )}
          </div>
          <Input
            label="Path prefix"
            value={path}
            onChange={(v) => {
              setPath(v);
              setErr(null);
            }}
            placeholder="/v1/files"
            hint="Covers the path and everything under it. End with * for a plain prefix."
            inputClassName="font-mono"
            autoComplete="off"
          />
          <Input label="Note" value={note} onChange={setNote} placeholder="No file uploads" maxLength={200} />
          {err && <Callout tone="error">{err}</Callout>}
          <div className="flex justify-end">
            <Button size="small" variant="primary" type="submit" disabled={busy} leadingIcon={busy ? ButtonSpinner : undefined}>
              Add rule
            </Button>
          </div>
        </>
      )}
    </form>
  );
}

/** What each tag value used today and this month. */
function TagUsage({ report }: { report: TagUsageReport | null }) {
  if (!report || report.tags.length === 0) {
    return (
      <div className={SECTION}>
        <div className={HEAD}>Usage by tag</div>
        <p className={HINT}>No tagged requests yet.</p>
      </div>
    );
  }
  return (
    <div className={SECTION}>
      <div className={HEAD}>Usage by tag</div>
      <div className="overflow-x-auto">
        <table className="w-full text-start font-mono text-caption-1-regular tabular-nums">
          <thead className="text-text-secondary">
            <tr>
              <th className="py-1 pe-3 text-start font-normal">Tag</th>
              <th className="py-1 pe-3 text-end font-normal">Today</th>
              <th className="py-1 text-end font-normal">This month</th>
            </tr>
          </thead>
          <tbody>
            {report.tags.slice(0, 50).map((t) => (
              <tr key={`${t.key}=${t.value}`} className="border-t border-border-table">
                <td className="py-1 pe-3 wrap-anywhere">
                  {t.key}={t.value}
                </td>
                <td className="py-1 pe-3 text-end">
                  {t.today_requests} · {usd(t.today_cost)}
                </td>
                <td className="py-1 text-end">
                  {t.month_requests} · {usd(t.month_cost)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {report.tags.length > 50 && <p className={HINT}>Showing the 50 most expensive of {report.tags.length}.</p>}
    </div>
  );
}

/**
 * An app's finer access controls: per-token limits, tag budgets, deny rules
 * and usage by tag.
 */
export function AppAccessPanel({ app, canEdit, onClose }: { app: App | null; canEdit: boolean; onClose: () => void }) {
  const [controls, setControls] = useState<AppControls | null>(null);
  const [usage, setUsage] = useState<TagUsageReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const id = app?.id;

  const load = useCallback(async (appId: number) => {
    try {
      const [c, u] = await Promise.all([api.appControls(appId), api.appTagUsage(appId)]);
      setControls(c);
      setUsage(u);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, []);

  useEffect(() => {
    if (id === undefined) return;
    setControls(null);
    setUsage(null);
    setError(null);
    void load(id);
  }, [id, load]);

  return (
    <Modal
      open={!!app}
      onClose={onClose}
      wide
      title={`Access for ${app?.name ?? ""}`}
      description="Limits per token and per caller tag, and rules that block requests. The app's own limits apply too."
      footer={
        <Button variant="ghost" onClick={onClose}>
          Close
        </Button>
      }
    >
      <div data-autofocus tabIndex={-1} className="flex flex-col gap-4 outline-none">
        {error && !controls ? (
          <Callout tone="error">Could not load: {error}</Callout>
        ) : !controls || !app ? (
          <div className="flex flex-col gap-2" role="status" aria-busy="true">
            <span className="sr-only">Loading</span>
            <Skeleton className="h-32 rounded-2xl" />
            <Skeleton className="h-32 rounded-2xl" />
          </div>
        ) : (
          <>
            <TokenDefaults key={`d${app.id}`} app={app} controls={controls} canEdit={canEdit} onSaved={setControls} />
            <TagBudgets key={`b${app.id}`} app={app} controls={controls} canEdit={canEdit} onSaved={setControls} />
            <TagUsage report={usage} />
            <DenyRules key={`r${app.id}`} app={app} controls={controls} canEdit={canEdit} onSaved={setControls} />
            {!canEdit && <p className={HINT}>Ask an admin to change these.</p>}
          </>
        )}
      </div>
    </Modal>
  );
}
