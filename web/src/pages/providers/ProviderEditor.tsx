import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTitle } from "../../hooks/useTitle";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  CircleDashed,
  ExternalLink,
  History,
  KeyRound,
  Loader2,
  Plus,
  Sparkles,
  XCircle,
} from "lucide-react";
import { ApiError, api, errorMessage } from "../../lib/api";
import { can } from "../../lib/perms";
import { dateTime, plural, relTime, slugify } from "../../lib/format";
import { blankProviderInput, inputOf, normalizeSpec } from "../../lib/providerSpec";
import { safeHref } from "../../lib/url";
import type { LiveReport, ProviderDetail, ProviderInput, ProviderSpec, TestReport } from "../../lib/types";
import { useProviders } from "../../stores/providers";
import { useAuth } from "../../stores/auth";
import { toast } from "../../stores/toast";
import { ProviderLogo } from "../../components/EntityLogo";
import { LogoEditor } from "../../components/LogoEditor";
import { Tabs } from "../../components/Tabs";
import { ConfirmButton } from "../../components/ConfirmButton";
import { AiFillButton, AiFillPanel, useAiFill } from "../../components/AiFill";
import { Badge, Button, Callout, Empty, Field, Input, PageSkeleton, Section, Select, Textarea, cx } from "../../components/ui";
import { ConnectionTab, ExamplesTab, HealthTab, JsonTab, OutcomeBadge, RulesTab, TestsTab } from "./SpecTabs";
import { SourceBadge } from "./ProvidersPage";
import { AddServiceModal } from "../AddServiceModal";

type Tab = "overview" | "connection" | "rules" | "health" | "examples" | "tests" | "json" | "versions" | "live";

type Validation =
  | { state: "idle" }
  | { state: "checking" }
  | { state: "done"; errors: string[]; report: TestReport | null }
  | { state: "error"; message: string };

const PROVIDER_AI_FIELDS = ["name", "category", "description", "website", "docs_url", "color"] as const;

// ---------------------------------------------------------------- validation panel

export function TestReportList({ report }: { report: TestReport }) {
  if (!report.results?.length) return <p className="text-xs text-muted">This spec has no tests yet.</p>;
  return (
    <ul className="space-y-1.5">
      {report.results.map((r, i) => (
        <li key={i} className="flex items-start gap-2 text-[13px]">
          {r.ok ? (
            <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-700" aria-label="passed" />
          ) : (
            <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-red-700" aria-label="failed" />
          )}
          <div className="min-w-0">
            <div className="break-words text-ink">{r.name || `Test ${i + 1}`}</div>
            {!r.ok && (
              <div className="wrap-anywhere font-mono text-[11px] text-red-700">
                expected {r.expect}, got {r.got || "nothing"}
                {r.detail ? `. ${r.detail}` : ""}
              </div>
            )}
          </div>
        </li>
      ))}
    </ul>
  );
}

function ValidationPanel({ v, onRetry }: { v: Validation; onRetry: () => void }) {
  let head: JSX.Element;
  if (v.state === "idle")
    head = (
      <span className="flex items-center gap-2 text-muted">
        <CircleDashed className="h-4 w-4" /> Not checked yet
      </span>
    );
  else if (v.state === "checking")
    head = (
      <span className="flex items-center gap-2 text-muted">
        <Loader2 className="h-4 w-4 animate-spin" /> Checking the spec and running tests
      </span>
    );
  else if (v.state === "error")
    head = (
      <span className="flex items-center gap-2 text-amber-800">
        <AlertTriangle className="h-4 w-4" /> Could not check the spec
      </span>
    );
  else {
    const failed = v.report?.failed ?? 0;
    const ok = v.errors.length === 0 && failed === 0;
    head = ok ? (
      <span className="flex items-center gap-2 text-emerald-700">
        <CheckCircle2 className="h-4 w-4" /> Valid · {plural(v.report?.passed ?? 0, "test")} passing
      </span>
    ) : (
      <span className="flex items-center gap-2 text-red-700">
        <XCircle className="h-4 w-4" />
        {v.errors.length ? plural(v.errors.length, "problem") : ""}
        {v.errors.length && failed ? " · " : ""}
        {failed ? `${failed} failing` : ""}
      </span>
    );
  }
  return (
    <section aria-label="Validation" aria-live="polite" className="rounded-card bg-surface p-4 shadow-card">
      <div className="text-[13px] font-medium">{head}</div>
      {v.state === "error" && (
        <p className="mt-2 text-xs text-muted">
          {v.message}.{" "}
          <button type="button" onClick={onRetry} className="font-medium text-ink underline underline-offset-2">
            Retry
          </button>
        </p>
      )}
      {v.state === "done" && v.errors.length > 0 && (
        <ul className="mt-3 space-y-1.5 border-t border-line pt-3">
          {v.errors.map((e, i) => (
            <li key={i} className="flex items-start gap-2 text-[13px] text-red-800">
              <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-red-500" aria-hidden="true" />
              <span className="min-w-0 break-words">{e}</span>
            </li>
          ))}
        </ul>
      )}
      {v.state === "done" && v.report && (
        <div className="mt-3 border-t border-line pt-3">
          <TestReportList report={v.report} />
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------- overview

function OverviewTab({
  draft,
  setDraft,
  isNew,
  readOnly,
  detail,
  errors,
}: {
  draft: ProviderInput;
  setDraft: (d: ProviderInput | ((p: ProviderInput) => ProviderInput)) => void;
  isNew: boolean;
  readOnly: boolean;
  detail: ProviderDetail | null;
  errors: Record<string, string>;
}) {
  const { setLogo, fetchLogo, removeLogo, remove } = useProviders();
  const navigate = useNavigate();
  const ai = useAiFill(
    "provider",
    () => ({ ...draft, spec: undefined }),
    (fields) =>
      setDraft((d) => {
        const next = { ...d } as Record<string, unknown>;
        for (const k of PROVIDER_AI_FIELDS) if (typeof fields[k] === "string") next[k] = fields[k];
        return next as unknown as ProviderInput;
      }),
  );
  const set = <K extends keyof ProviderInput>(k: K, v: ProviderInput[K]) => setDraft((d) => ({ ...d, [k]: v }));

  return (
    <div className="space-y-5">
      <Section title="Identity" description="How this provider shows up across Acnologia." actions={!readOnly && <AiFillButton ai={ai} />}>
        <AiFillPanel ai={ai} />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Name" required error={errors.name} className={ai.flash("name")}>
            <Input
              value={draft.name}
              disabled={readOnly}
              onChange={(e) => {
                const name = e.target.value;
                setDraft((d) => ({ ...d, name, slug: isNew && (d.slug === "" || d.slug === slugify(d.name)) ? slugify(name) : d.slug }));
              }}
              placeholder="Acme Mail"
            />
          </Field>
          <Field label="Slug" required error={errors.slug} hint={isNew ? "Lowercase id that services use. You cannot change it later." : "Fixed, because services use it."}>
            <Input
              value={draft.slug}
              disabled={!isNew || readOnly}
              onChange={(e) => set("slug", e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ""))}
              placeholder="acme-mail"
              spellCheck={false}
              className="font-mono"
            />
          </Field>
          <Field label="Category" className={ai.flash("category")}>
            <Input value={draft.category} disabled={readOnly} onChange={(e) => set("category", e.target.value)} placeholder="Email" />
          </Field>
          <Field label="Status" hint="New services cannot use a disabled provider.">
            <Select value={draft.status} disabled={readOnly} onChange={(e) => set("status", e.target.value as ProviderInput["status"])}>
              <option value="active">active</option>
              <option value="disabled">disabled</option>
            </Select>
          </Field>
          <Field label="Description" className={cx("sm:col-span-2", ai.flash("description"))}>
            <Textarea rows={2} value={draft.description} disabled={readOnly} onChange={(e) => set("description", e.target.value)} placeholder="Transactional email API" />
          </Field>
          <Field label="Website" error={errors.website} className={ai.flash("website")}>
            <Input type="url" inputMode="url" value={draft.website} disabled={readOnly} onChange={(e) => set("website", e.target.value)} placeholder="https://example.com" className="font-mono" />
          </Field>
          <Field label="Docs URL" error={errors.docs_url} className={ai.flash("docs_url")}>
            <Input type="url" inputMode="url" value={draft.docs_url} disabled={readOnly} onChange={(e) => set("docs_url", e.target.value)} placeholder="https://example.com/docs" className="font-mono" />
          </Field>
          <Field label="Color" hint="Fills the lettermark when there is no logo." className={ai.flash("color")}>
            <div className="flex items-center gap-2">
              <input
                type="color"
                aria-label="Pick color"
                value={/^#[0-9a-f]{6}$/i.test(draft.color) ? draft.color : "#6b6b66"}
                disabled={readOnly}
                onChange={(e) => set("color", e.target.value)}
                className="tap h-10 w-12 shrink-0 cursor-pointer rounded-xl border border-line bg-surface p-1"
              />
              <Input value={draft.color} disabled={readOnly} onChange={(e) => set("color", e.target.value)} placeholder="#3b6ef5" className="font-mono" />
            </div>
          </Field>
        </div>
      </Section>

      <Section title="Logo" description="Shows on provider and service cards, and in pickers.">
        {isNew ? (
          <div className="flex items-center gap-4">
            <ProviderLogo provider={{ name: draft.name || "?", color: draft.color, logo_url: null }} size="xl" />
            <p className="text-[13px] text-muted">Create the provider first, then upload a logo or fetch one from its website.</p>
          </div>
        ) : (
          <LogoEditor
            src={detail?.logo_url}
            name={draft.name}
            color={draft.color}
            hasOwn={!!detail?.logo_url}
            disabled={readOnly}
            website={detail?.website || draft.website}
            onUpload={(l) => setLogo(draft.slug, l)}
            onFetch={(url) => fetchLogo(draft.slug, url)}
            onRemove={() => removeLogo(draft.slug)}
          />
        )}
      </Section>

      {!isNew && detail && !readOnly && (
        <Section
          title="Delete provider"
          description={
            detail.builtin
              ? "Built in providers cannot be deleted. Disable it instead."
              : detail.services_count > 0
                ? `${plural(detail.services_count, "service")} still ${detail.services_count === 1 ? "uses" : "use"} this provider. Delete or move ${detail.services_count === 1 ? "it" : "them"} first.`
                : "Removes the provider and all of its versions."
          }
          actions={
            !detail.builtin &&
            detail.services_count === 0 && (
              <ConfirmButton
                label="Delete provider"
                confirmLabel="Delete forever"
                onConfirm={async () => {
                  try {
                    await remove(detail.slug);
                    toast.success(`${detail.name} deleted`);
                    navigate("/providers");
                  } catch (e) {
                    toast.error(errorMessage(e));
                  }
                }}
              />
            )
          }
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------- versions

function VersionsTab({ detail, readOnly, onRolledBack }: { detail: ProviderDetail; readOnly: boolean; onRolledBack: () => void }) {
  const rollback = useProviders((s) => s.rollback);
  const versions = [...(detail.versions ?? [])].sort((a, b) => b.version - a.version);
  if (!versions.length) return <Empty icon={<History className="h-5 w-5" />} title="No version history" body="Each save creates a version you can roll back to." />;
  return (
    <ol className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card">
      {versions.map((v) => {
        const current = v.version === detail.version;
        return (
          <li key={v.version} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
            <div className="flex items-center gap-2 sm:w-28">
              <span className="font-mono text-[15px] text-ink">v{v.version}</span>
              {current && <Badge tone="dark">current</Badge>}
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] text-ink">{v.note || <span className="text-faint">No note</span>}</div>
              <div className="font-mono text-[11px] text-muted">
                {v.source || "manual"} · {v.created_by || "system"} · <span title={dateTime(v.created_at)}>{relTime(v.created_at)}</span>
              </div>
            </div>
            {!current && !readOnly && (
              <ConfirmButton
                label={`Roll back to v${v.version}`}
                confirmLabel={`Restore v${v.version}`}
                icon={<History className="h-3.5 w-3.5" />}
                onConfirm={async () => {
                  try {
                    await rollback(detail.slug, v.version);
                    toast.success(`Restored v${v.version} as a new version`);
                    onRolledBack();
                  } catch (e) {
                    toast.error(errorMessage(e));
                  }
                }}
              />
            )}
          </li>
        );
      })}
    </ol>
  );
}

// ---------------------------------------------------------------- live check

export function LiveReportView({ live }: { live: LiveReport }) {
  return (
    <div className="space-y-3">
      <div className={cx("flex items-center gap-2 text-[14px] font-medium", live.ok ? "text-emerald-700" : "text-red-700")}>
        {live.ok ? <CheckCircle2 className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
        {live.ok ? "The key works with the real API" : "The live check failed"}
      </div>
      <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="rounded-xl bg-soft p-3">
          <dt className="font-mono text-[11px] uppercase tracking-wide text-faint">Health check</dt>
          <dd className="mt-1 text-[13px] text-ink">
            <span className={live.health.ok ? "text-emerald-700" : "text-red-700"}>{live.health.ok ? "ok" : "failed"}</span>
            {live.health.status ? <span className="font-mono text-muted"> · HTTP {live.health.status}</span> : null}
            {(live.health.quota_remaining !== null || live.health.quota_limit !== null) && (
              <div className="mt-1 font-mono text-xs text-muted">
                quota {live.health.quota_remaining ?? "?"} / {live.health.quota_limit ?? "?"}
              </div>
            )}
            {live.health.detail && <div className="mt-1 break-words text-xs text-muted">{live.health.detail}</div>}
          </dd>
        </div>
        <div className="rounded-xl bg-soft p-3">
          <dt className="font-mono text-[11px] uppercase tracking-wide text-faint">First safe example</dt>
          <dd className="mt-1 text-[13px] text-ink">
            {live.example.ran ? (
              <>
                <span className={live.example.ok ? "text-emerald-700" : "text-red-700"}>{live.example.ok ? "ok" : "failed"}</span>
                {live.example.status ? <span className="font-mono text-muted"> · HTTP {live.example.status}</span> : null}
                {live.example.outcome && (
                  <span className="ml-2">
                    <OutcomeBadge outcome={live.example.outcome} />
                  </span>
                )}
              </>
            ) : (
              <span className="text-muted">Not run: no example is marked safe</span>
            )}
            {live.example.detail && <div className="mt-1 break-words text-xs text-muted">{live.example.detail}</div>}
          </dd>
        </div>
      </dl>
    </div>
  );
}

function LiveCheckTab({ slug, dirty, readOnly }: { slug: string; dirty: boolean; readOnly: boolean }) {
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [live, setLive] = useState<LiveReport | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const run = async () => {
    setBusy(true);
    setErr(null);
    try {
      setLive(await api.liveCheck(slug, secret.trim()));
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
      setSecret("");
    }
  };
  return (
    <Section
      title="Live check"
      description="Runs the health check and the first safe example against the real API with a key you paste. Acnologia does not store the key."
    >
      {dirty && (
        <Callout tone="amber" icon={<AlertTriangle className="h-4 w-4" />} className="mb-4">
          This checks the saved version. Save first to test your changes.
        </Callout>
      )}
      <form
        className="flex flex-col gap-2 sm:flex-row sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          if (secret.trim()) void run();
        }}
      >
        <Field label="Test key" className="flex-1" error={err}>
          <Input type="password" autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder="Paste an API key" disabled={readOnly} className="font-mono" />
        </Field>
        <Button type="submit" variant="primary" loading={busy} disabled={!secret.trim() || readOnly} icon={<KeyRound className="h-4 w-4" />}>
          Run live check
        </Button>
      </form>
      {live && (
        <div className="fade-up mt-5 border-t border-line pt-5">
          <LiveReportView live={live} />
        </div>
      )}
    </Section>
  );
}

// ---------------------------------------------------------------- page

function localErrors(d: ProviderInput, isNew: boolean): Record<string, string> {
  const e: Record<string, string> = {};
  if (!d.name.trim()) e.name = "Give the provider a name.";
  if (isNew && !d.slug.trim()) e.slug = "Add a slug.";
  else if (isNew && !/^[a-z0-9][a-z0-9_-]*$/.test(d.slug)) e.slug = "Use lowercase letters, numbers, dashes or underscores.";
  for (const k of ["website", "docs_url"] as const) if (d[k] && !/^https?:\/\//i.test(d[k])) e[k] = "Start with http:// or https://.";
  return e;
}

export function ProviderEditorPage() {
  const params = useParams();
  const slugParam = params.slug ?? "";
  const isNew = slugParam === "" || slugParam === "new";
  const navigate = useNavigate();
  const role = useAuth((s) => s.user?.role);
  const readOnly = !can.manage(role);
  const { fetchOne, create, update } = useProviders();
  const detail = useProviders((s) => (isNew ? null : (s.details[slugParam] ?? null)));

  const [draft, setDraftState] = useState<ProviderInput | null>(isNew ? blankProviderInput() : null);
  const [baseline, setBaseline] = useState<string>(isNew ? JSON.stringify(blankProviderInput()) : "");
  const [tab, setTab] = useState<Tab>("overview");
  const [notFound, setNotFound] = useState(false);
  const [validation, setValidation] = useState<Validation>({ state: "idle" });
  const [note, setNote] = useState("");
  const [noteOpen, setNoteOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [addingService, setAddingService] = useState(false);
  const [triedSave, setTriedSave] = useState(false);
  const validateSeq = useRef(0);
  useTitle(isNew ? "New provider" : draft?.name || slugParam);

  const setDraft = useCallback((d: ProviderInput | ((p: ProviderInput) => ProviderInput)) => {
    setDraftState((prev) => (typeof d === "function" ? (prev ? d(prev) : prev) : d));
  }, []);

  const load = useCallback(async () => {
    if (isNew) return;
    try {
      const d = await fetchOne(slugParam);
      const input = inputOf(d);
      setDraftState(input);
      setBaseline(JSON.stringify(input));
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setNotFound(true);
      else toast.error(errorMessage(e));
    }
  }, [isNew, slugParam, fetchOne]);

  useEffect(() => {
    setNotFound(false);
    setTab("overview");
    setValidation({ state: "idle" });
    if (isNew) {
      const b = blankProviderInput();
      setDraftState(b);
      setBaseline(JSON.stringify(b));
    } else {
      setDraftState(null);
      void load();
    }
  }, [isNew, slugParam, load]);

  const dirty = !!draft && JSON.stringify(draft) !== baseline;

  // Warn before leaving the tab with unsaved edits.
  useEffect(() => {
    if (!dirty) return;
    const h = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", h);
    return () => window.removeEventListener("beforeunload", h);
  }, [dirty]);

  const runValidation = useCallback(async (d: ProviderInput) => {
    const seq = ++validateSeq.current;
    setValidation({ state: "checking" });
    try {
      const res = await api.validateProvider(d);
      if (seq === validateSeq.current) setValidation({ state: "done", errors: res.errors ?? [], report: res.report ?? null });
    } catch (e) {
      if (seq !== validateSeq.current) return;
      if (e instanceof ApiError && e.status === 400 && e.data && typeof e.data === "object") {
        const data = e.data as { errors?: string[]; report?: TestReport };
        setValidation({ state: "done", errors: data.errors ?? [e.message], report: data.report ?? null });
      } else setValidation({ state: "error", message: errorMessage(e) });
    }
  }, []);

  // Debounced live validation on every change.
  const serialized = draft ? JSON.stringify(draft) : "";
  useEffect(() => {
    if (!draft || readOnly) return;
    const t = window.setTimeout(() => void runValidation(draft), 700);
    return () => window.clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serialized, readOnly, runValidation]);

  const errors = useMemo(() => (draft && triedSave ? localErrors(draft, isNew) : {}), [draft, isNew, triedSave]);

  const save = async () => {
    if (!draft) return;
    setTriedSave(true);
    const errs = localErrors(draft, isNew);
    if (Object.keys(errs).length) {
      setTab("overview");
      toast.error(Object.values(errs)[0]);
      return;
    }
    setSaving(true);
    const payload: ProviderInput = { ...draft, spec: normalizeSpec(draft.spec) };
    try {
      if (isNew) {
        const res = await create(payload);
        toast.success(`${res.provider.name} created${res.report ? ` · ${res.report.passed} tests passing` : ""}`);
        setBaseline(JSON.stringify(payload));
        navigate(`/providers/${encodeURIComponent(res.provider.slug)}`, { replace: true });
      } else {
        const res = await update(draft.slug, payload, note.trim());
        toast.success(`Saved v${res.provider.version}`);
        setNote("");
        setNoteOpen(false);
        const fresh = useProviders.getState().details[draft.slug];
        const input = fresh ? inputOf(fresh) : payload;
        setDraftState(input);
        setBaseline(JSON.stringify(input));
      }
    } catch (e) {
      if (e instanceof ApiError && e.data && typeof e.data === "object" && ("errors" in e.data || "report" in e.data)) {
        const data = e.data as { errors?: string[]; report?: TestReport };
        setValidation({ state: "done", errors: data.errors ?? [e.message], report: data.report ?? null });
        toast.error("Not saved. Fix the problems listed under Validation.");
      } else toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const discard = () => {
    if (!baseline) return;
    setDraftState(JSON.parse(baseline) as ProviderInput);
    setTriedSave(false);
  };

  if (notFound)
    return (
      <Empty
        title="Provider not found"
        body="It may have been deleted, or the link is wrong."
        action={
          <Link to="/providers" className="tap inline-flex h-10 items-center rounded-full bg-ink px-4 text-sm font-medium text-on-ink">
            All providers
          </Link>
        }
      />
    );
  if (!draft) return <PageSkeleton variant="list" />;

  const report = validation.state === "done" ? validation.report : null;
  const setSpec = (spec: ProviderSpec) => setDraft((d) => ({ ...d, spec }));
  const specTab = { spec: draft.spec, onChange: setSpec, readOnly };

  const tabs: { id: Tab; label: string; count?: number }[] = [
    { id: "overview", label: "Overview" },
    { id: "connection", label: "Connection" },
    { id: "rules", label: "Rules", count: draft.spec.rules?.length ?? 0 },
    { id: "health", label: "Health check" },
    { id: "examples", label: "Examples", count: draft.spec.examples?.length ?? 0 },
    { id: "tests", label: "Tests", count: draft.spec.tests?.length ?? 0 },
    { id: "json", label: "JSON" },
    ...(!isNew ? [{ id: "versions" as Tab, label: "Versions", count: detail?.versions?.length }, { id: "live" as Tab, label: "Live check" }] : []),
  ];

  const changeTab = (t: Tab) => {
    // The JSON tab may leave fields out; fill them before the structured tabs render.
    if (tab === "json") setDraft((d) => ({ ...d, spec: normalizeSpec(d.spec) }));
    setTab(t);
  };

  return (
    <div className={(dirty || isNew) && !readOnly ? "pb-28" : undefined}>
      <Link to="/providers" className="tap mb-5 inline-flex items-center gap-1.5 font-mono text-xs text-muted hover:text-ink">
        <ArrowLeft className="h-3.5 w-3.5" /> Providers
      </Link>

      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 items-center gap-4">
          <ProviderLogo provider={{ name: draft.name || "New", color: draft.color, logo_url: detail?.logo_url ?? null }} size="lg" />
          <div className="min-w-0">
            <h1 className="break-words font-mono text-[26px] leading-tight tracking-tight text-ink sm:text-[30px]">
              {isNew ? draft.name || "New provider" : draft.name}
            </h1>
            <div className="mt-1.5 flex flex-wrap items-center gap-1.5 font-mono text-xs text-muted">
              {!isNew && detail ? (
                <>
                  <span>{detail.slug}</span>
                  <SourceBadge source={detail.source} />
                  <Badge>v{detail.version}</Badge>
                  {detail.status === "disabled" && <Badge tone="amber">disabled</Badge>}
                  <span>· {plural(detail.services_count, "service")}</span>
                  {safeHref(detail.docs_url) && (
                    <a href={safeHref(detail.docs_url)} target="_blank" rel="noreferrer" className="tap-inline inline-flex items-center gap-1 hover:text-ink">
                      · docs <ExternalLink className="h-3 w-3" />
                    </a>
                  )}
                </>
              ) : (
                <span>Write the rules by hand, or let the AI builder write them from a docs URL.</span>
              )}
            </div>
          </div>
        </div>
        {!readOnly && (
          <div className="flex shrink-0 flex-wrap gap-2">
            {isNew ? (
              <Link to="/providers/ai" className="tap inline-flex h-10 items-center gap-2 rounded-full bg-surface px-4 text-sm font-medium text-ink shadow-card hover:bg-soft">
                <Sparkles className="h-4 w-4 text-brand" /> Build with AI instead
              </Link>
            ) : (
              <>
                <Button icon={<Plus className="h-4 w-4" />} onClick={() => setAddingService(true)}>
                  Add service
                </Button>
                <Link
                  to={`/providers/ai?provider=${encodeURIComponent(draft.slug)}`}
                  className="tap inline-flex h-10 items-center gap-2 rounded-full bg-ink px-4 text-sm font-medium text-on-ink hover:bg-ink-strong"
                >
                  <Sparkles className="h-4 w-4 text-spark" /> Improve with AI
                </Link>
              </>
            )}
          </div>
        )}
      </div>

      {readOnly && (
        <Callout className="mt-6">You can view this provider. Only admins can change it.</Callout>
      )}

      <div className="mt-8 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className="min-w-0">
          <Tabs items={tabs} value={tab} onChange={changeTab} />
          <div className="mt-6">
            {tab === "overview" && <OverviewTab draft={draft} setDraft={setDraft} isNew={isNew} readOnly={readOnly} detail={detail} errors={errors} />}
            {tab === "connection" && <ConnectionTab {...specTab} />}
            {tab === "rules" && <RulesTab {...specTab} />}
            {tab === "health" && <HealthTab {...specTab} />}
            {tab === "examples" && <ExamplesTab {...specTab} />}
            {tab === "tests" && <TestsTab {...specTab} report={report} />}
            {tab === "json" && <JsonTab {...specTab} />}
            {tab === "versions" && detail && <VersionsTab detail={detail} readOnly={readOnly} onRolledBack={() => void load()} />}
            {tab === "live" && <LiveCheckTab slug={draft.slug} dirty={dirty} readOnly={readOnly} />}
          </div>
        </div>
        {!readOnly && (
          <aside className="order-first lg:order-none">
            <div className="lg:sticky lg:top-20">
              <ValidationPanel v={validation} onRetry={() => draft && void runValidation(draft)} />
            </div>
          </aside>
        )}
      </div>

      {!readOnly && (dirty || isNew) && (
        <div className="fixed inset-x-0 bottom-[var(--tabbar-h)] z-30 border-t border-line bg-surface/95 backdrop-blur">
          <div className="mx-auto flex max-w-[1076px] flex-col gap-2 px-4 py-2.5 sm:px-6 sm:py-3 lg:pb-[max(0.75rem,env(safe-area-inset-bottom))]">
            {/* Phones: the version note opens on demand so the bar stays one row above the keyboard. */}
            {!isNew && noteOpen && (
              <Input
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder="What changed (optional)"
                aria-label="Version note"
                data-autofocus
                className="sm:hidden"
              />
            )}
            <div className="flex items-center gap-2">
              <div className="flex min-w-0 flex-1 items-center gap-2 text-[13px] text-muted">
                <span className="h-2 w-2 shrink-0 rounded-full bg-brand" aria-hidden="true" />
                <span className="truncate sm:hidden">{isNew ? "Not created" : "Unsaved"}</span>
                <span className="hidden truncate sm:inline">{isNew ? "New provider, not created yet" : "Unsaved changes. Saving creates a new version."}</span>
              </div>
              {!isNew && (
                <Input
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  placeholder="What changed (optional)"
                  aria-label="Version note"
                  className="hidden sm:block sm:w-64"
                />
              )}
              {!isNew && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="sm:hidden"
                  aria-expanded={noteOpen}
                  aria-label={note.trim() ? "Version note, added" : "Add a version note"}
                  icon={note.trim() ? <CheckCircle2 className="h-3.5 w-3.5" aria-hidden="true" /> : undefined}
                  onClick={() => setNoteOpen(!noteOpen)}
                >
                  Note
                </Button>
              )}
              {!isNew && (
                <Button size="sm" variant="ghost" onClick={discard} className="sm:h-10 sm:px-4 sm:text-sm">
                  Discard
                </Button>
              )}
              <Button variant="primary" loading={saving} onClick={() => void save()}>
                {isNew ? "Create provider" : "Save version"}
              </Button>
            </div>
          </div>
        </div>
      )}

      {!isNew && <AddServiceModal open={addingService} onClose={() => setAddingService(false)} initialProvider={draft.slug} />}
    </div>
  );
}
