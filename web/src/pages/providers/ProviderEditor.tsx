import { useCallback, useEffect, useMemo, useRef, useState, type JSX } from "react";
import { useTitle } from "../../hooks/useTitle";
import { useNavigate, useParams } from "react-router-dom";
import {
  RiAddLine,
  RiAlertLine,
  RiCheckboxBlankCircleLine,
  RiCheckboxCircleLine,
  RiCheckLine,
  RiCloseCircleLine,
  RiExternalLinkLine,
  RiHistoryLine,
  RiKey2Line,
  RiPlugLine,
  RiSparkling2Line,
} from "@remixicon/react";
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
import { AiFillButton, AiFillPanel, useAiFill } from "../../components/AiFill";
import { Button } from "@/components/base/buttons/button";
import { LinkButton } from "@/components/base/buttons/link-button";
import { Chip } from "@/components/base/badges/chip";
import { Input } from "@/components/base/input/input";
import { Select, SelectItem } from "@/components/base/select/select";
import { Textarea } from "@/components/base/textarea/textarea";
import { Tab, TabList, TabPanel, Tabs } from "@/components/base/tabs/tabs";
import { ButtonSpinner, SpinnerGlyph } from "@/components/base/spinner/spinner";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { Callout } from "@/components/acn/callout";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { EmptyState } from "@/components/acn/empty-state";
import { PageHeader } from "@/components/acn/page-header";
import { PageSkeleton } from "@/components/acn/page-skeleton";
import { Section } from "@/components/acn/section";
import { cx } from "@/utils/cx";
import { ConnectionTab, ExamplesTab, HealthTab, JsonTab, OutcomeBadge, RulesTab, TestsTab } from "./SpecTabs";
import { RouteButton, SourceBadge } from "./ProvidersPage";
import { AddServiceModal } from "../AddServiceModal";

type TabId = "overview" | "connection" | "rules" | "health" | "examples" | "tests" | "json" | "versions" | "live";

type Validation =
  | { state: "idle" }
  | { state: "checking" }
  | { state: "done"; errors: string[]; report: TestReport | null }
  | { state: "error"; message: string };

const PROVIDER_AI_FIELDS = ["name", "category", "description", "website", "docs_url", "color"] as const;

// ---------------------------------------------------------------- validation panel

export function TestReportList({ report }: { report: TestReport }) {
  if (!report.results?.length) return <p className="text-caption-1-regular text-text-secondary">This spec has no tests yet.</p>;
  return (
    <ul className="flex flex-col gap-1.5">
      {report.results.map((r, i) => (
        <li key={i} className="flex items-start gap-2 text-body-2-regular">
          {r.ok ? (
            <RiCheckboxCircleLine className="mt-0.5 size-3.5 shrink-0 text-status-lime-text" aria-label="passed" />
          ) : (
            <RiCloseCircleLine className="mt-0.5 size-3.5 shrink-0 text-text-error-primary" aria-label="failed" />
          )}
          <div className="min-w-0">
            <div className="break-words text-text-primary">{r.name || `Test ${i + 1}`}</div>
            {!r.ok && (
              <div className="wrap-anywhere font-mono text-caption-2-regular text-text-error-primary">
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

type DoneValidation = Extract<Validation, { state: "done" }>;

function ValidationPanel({ v, onRetry }: { v: Validation; onRetry: () => void }) {
  // Every edit re-checks the spec. Keep the last result on screen while that
  // runs, so the panel (and on phones, the form below it) does not jump.
  const [last, setLast] = useState<DoneValidation | null>(null);
  useEffect(() => {
    if (v.state === "done") setLast(v);
    else if (v.state === "idle") setLast(null);
  }, [v]);
  const fade = useScrollFade();
  const shown: DoneValidation | null = v.state === "done" ? v : v.state === "checking" ? last : null;
  const checking = v.state === "checking";
  const failed = shown?.report?.failed ?? 0;
  const ok = !!shown && shown.errors.length === 0 && failed === 0;

  let head: JSX.Element;
  if (v.state === "idle")
    head = (
      <span className="flex items-center gap-2 text-text-secondary">
        <RiCheckboxBlankCircleLine className="size-4 shrink-0" aria-hidden /> Not checked yet
      </span>
    );
  else if (v.state === "error")
    head = (
      <span className="flex items-center gap-2 text-status-yellow-text">
        <RiAlertLine className="size-4 shrink-0" aria-hidden /> Could not check the spec
      </span>
    );
  else if (!shown)
    head = (
      <span className="flex items-center gap-2 text-text-secondary">
        <SpinnerGlyph size="sm" className="text-current" /> Checking the spec and running tests
      </span>
    );
  else
    head = (
      <span className={cx("flex items-center gap-2", ok ? "text-status-lime-text" : "text-text-error-primary")}>
        {checking ? (
          <SpinnerGlyph size="sm" className="text-text-tertiary" />
        ) : ok ? (
          <RiCheckboxCircleLine className="size-4 shrink-0" aria-hidden />
        ) : (
          <RiCloseCircleLine className="size-4 shrink-0" aria-hidden />
        )}
        <span className="min-w-0">
          {ok ? (
            <>Valid · {plural(shown.report?.passed ?? 0, "test")} passing</>
          ) : (
            <>
              {shown.errors.length ? plural(shown.errors.length, "problem") : ""}
              {shown.errors.length && failed ? " · " : ""}
              {failed ? `${failed} failing` : ""}
            </>
          )}
        </span>
        {checking && <span className="ms-auto shrink-0 font-mono text-caption-2-regular text-text-tertiary">checking</span>}
      </span>
    );
  return (
    <section
      aria-label="Validation"
      aria-live="polite"
      aria-busy={checking}
      className="flex flex-col rounded-3xl border border-border-button-default bg-background-primary-default p-5 lg:max-h-[calc(100dvh-7rem)]"
    >
      <div className="shrink-0 text-body-2-medium">{head}</div>
      {v.state === "error" && (
        <p className="mt-2 text-caption-1-regular text-text-secondary">
          {v.message}.{" "}
          <LinkButton size="xs" onClick={onRetry}>
            Retry
          </LinkButton>
        </p>
      )}
      {shown && (shown.errors.length > 0 || shown.report) && (
        // Long reports scroll inside the sticky card; the fade softens the top edge.
        <div className={cx("relative mt-3 min-h-0 border-t border-separator-border", ok && !shown.errors.length && "hidden lg:block")}>
          <div
            onScroll={fade.onScroll}
            className={cx("max-h-full overflow-y-auto overscroll-contain pt-3 transition-opacity duration-200 ease-out lg:max-h-[calc(100dvh-12rem)]", checking && "opacity-60")}
          >
            {shown.errors.length > 0 && (
              <ul className="flex flex-col gap-1.5">
                {shown.errors.map((e, i) => (
                  <li key={i} className="flex items-start gap-2 text-body-2-regular text-text-error-primary">
                    <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-current" aria-hidden="true" />
                    <span className="min-w-0 break-words">{e}</span>
                  </li>
                ))}
              </ul>
            )}
            {/* Phones show the editor right under this panel: list tests only when one fails. */}
            {shown.report && (
              <div className={cx(shown.errors.length > 0 && "mt-3 border-t border-separator-border pt-3", ok && "hidden lg:block")}>
                <TestReportList report={shown.report} />
              </div>
            )}
          </div>
          <ScrollFade progress={fade.progress} />
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------- overview

const HEX = /^#[0-9a-f]{6}$/i;

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
    <div className="flex flex-col gap-5">
      <Section title="Identity" description="How this provider shows up across Acnologia." actions={!readOnly && <AiFillButton ai={ai} />}>
        <AiFillPanel ai={ai} />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Input
            label="Name"
            isRequired
            isInvalid={!!errors.name}
            errorMessage={errors.name}
            className={cx("rounded-xl", ai.flash("name"))}
            value={draft.name}
            isDisabled={readOnly}
            onChange={(name) => {
              setDraft((d) => ({ ...d, name, slug: isNew && (d.slug === "" || d.slug === slugify(d.name)) ? slugify(name) : d.slug }));
            }}
            placeholder="Acme Mail"
          />
          <Input
            label="Slug"
            isRequired
            isInvalid={!!errors.slug}
            errorMessage={errors.slug}
            hint={isNew ? "Lowercase id that services use. You cannot change it later." : "Fixed, because services use it."}
            value={draft.slug}
            isDisabled={!isNew || readOnly}
            onChange={(v) => set("slug", v.toLowerCase().replace(/[^a-z0-9_-]/g, ""))}
            placeholder="acme-mail"
            spellCheck="false"
            inputClassName="font-mono"
            inputDir="ltr"
          />
          <Input
            label="Category"
            className={cx("rounded-xl", ai.flash("category"))}
            value={draft.category}
            isDisabled={readOnly}
            onChange={(v) => set("category", v)}
            placeholder="Email"
          />
          <Select
            label="Status"
            hint="New services cannot use a disabled provider."
            selectedKey={draft.status}
            isDisabled={readOnly}
            onSelectionChange={(k) => k != null && set("status", k as ProviderInput["status"])}
            triggerClassName="font-mono"
          >
            <SelectItem id="active">active</SelectItem>
            <SelectItem id="disabled">disabled</SelectItem>
          </Select>
          <Textarea
            label="Description"
            className={cx("rounded-xl sm:col-span-2", ai.flash("description"))}
            rows={2}
            autoResize
            value={draft.description}
            isDisabled={readOnly}
            onChange={(v) => set("description", v)}
            placeholder="Transactional email API"
          />
          <Input
            label="Website"
            type="url"
            inputMode="url"
            isInvalid={!!errors.website}
            errorMessage={errors.website}
            className={cx("rounded-xl", ai.flash("website"))}
            value={draft.website}
            isDisabled={readOnly}
            onChange={(v) => set("website", v)}
            placeholder="https://example.com"
            inputClassName="font-mono"
          />
          <Input
            label="Docs URL"
            type="url"
            inputMode="url"
            isInvalid={!!errors.docs_url}
            errorMessage={errors.docs_url}
            className={cx("rounded-xl", ai.flash("docs_url"))}
            value={draft.docs_url}
            isDisabled={readOnly}
            onChange={(v) => set("docs_url", v)}
            placeholder="https://example.com/docs"
            inputClassName="font-mono"
          />
          <Input
            label="Color"
            hint="A six digit hex value. Fills the lettermark when there is no logo."
            className={cx("rounded-xl", ai.flash("color"))}
            value={draft.color}
            isDisabled={readOnly}
            onChange={(v) => set("color", v)}
            placeholder="Hex value"
            inputClassName="font-mono"
            inputDir="ltr"
            leadingAddon={
              // Preview of the colour; the value itself is data, so it rides an inline style.
              <span
                aria-hidden
                className="ms-1 size-5 shrink-0 rounded-md border border-border-button-default"
                style={{ backgroundColor: HEX.test(draft.color) ? draft.color : "transparent" }}
              />
            }
          />
        </div>
      </Section>

      <Section title="Logo" description="Shows on provider and service cards, and in pickers.">
        {isNew ? (
          <div className="flex items-center gap-4">
            <ProviderLogo provider={{ name: draft.name || "?", color: draft.color, logo_url: null }} size="xl" />
            <p className="text-body-2-regular text-text-secondary">Create the provider first, then upload a logo or fetch one from its website.</p>
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
          tone={!detail.builtin && detail.services_count === 0 ? "danger" : "default"}
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
                description={`${detail.name} and all of its versions are removed. This cannot be undone.`}
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
  if (!versions.length) return <EmptyState icon={RiHistoryLine} title="No version history" body="Each save creates a version you can roll back to." />;
  return (
    <ol className="divide-y divide-separator-border overflow-hidden rounded-3xl border border-border-button-default bg-background-primary-default">
      {versions.map((v) => {
        const current = v.version === detail.version;
        return (
          <li key={v.version} className="flex flex-col gap-2 px-5 py-3.5 sm:flex-row sm:items-center sm:gap-4">
            <div className="flex items-center gap-2 sm:w-28">
              <span className="font-mono text-body-medium text-text-primary">v{v.version}</span>
              {current && <Chip variant="caption" color="purple">current</Chip>}
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-body-2-regular text-text-primary">{v.note || <span className="text-text-tertiary">No note</span>}</div>
              <div className="font-mono text-caption-2-regular text-text-secondary">
                {v.source || "manual"} · {v.created_by || "system"} · <span title={dateTime(v.created_at)}>{relTime(v.created_at)}</span>
              </div>
            </div>
            {!current && !readOnly && (
              <ConfirmButton
                label={`Roll back to v${v.version}`}
                confirmLabel={`Restore v${v.version}`}
                description="The old spec is saved again as a new version. Nothing is lost."
                icon={RiHistoryLine}
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

function OkText({ ok }: { ok: boolean }) {
  return <span className={ok ? "text-status-lime-text" : "text-text-error-primary"}>{ok ? "ok" : "failed"}</span>;
}

export function LiveReportView({ live }: { live: LiveReport }) {
  return (
    <div className="flex flex-col gap-3">
      <div className={cx("flex items-center gap-2 text-body-medium", live.ok ? "text-status-lime-text" : "text-text-error-primary")}>
        {live.ok ? <RiCheckboxCircleLine className="size-4" aria-hidden /> : <RiCloseCircleLine className="size-4" aria-hidden />}
        {live.ok ? "The key works with the real API" : "The live check failed"}
      </div>
      <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="rounded-2xl bg-background-secondary-default p-3.5">
          <dt className="text-caption-2-semibold uppercase text-text-tertiary">Health check</dt>
          <dd className="mt-1 text-body-2-regular text-text-primary">
            <OkText ok={live.health.ok} />
            {live.health.status ? <span className="font-mono text-text-secondary"> · HTTP {live.health.status}</span> : null}
            {(live.health.quota_remaining !== null || live.health.quota_limit !== null) && (
              <div className="mt-1 font-mono text-caption-1-regular text-text-secondary">
                quota {live.health.quota_remaining ?? "?"} / {live.health.quota_limit ?? "?"}
              </div>
            )}
            {live.health.detail && <div className="mt-1 break-words text-caption-1-regular text-text-secondary">{live.health.detail}</div>}
          </dd>
        </div>
        <div className="rounded-2xl bg-background-secondary-default p-3.5">
          <dt className="text-caption-2-semibold uppercase text-text-tertiary">First safe example</dt>
          <dd className="mt-1 text-body-2-regular text-text-primary">
            {live.example.ran ? (
              <>
                <OkText ok={live.example.ok} />
                {live.example.status ? <span className="font-mono text-text-secondary"> · HTTP {live.example.status}</span> : null}
                {live.example.outcome && (
                  <span className="ms-2">
                    <OutcomeBadge outcome={live.example.outcome} />
                  </span>
                )}
              </>
            ) : (
              <span className="text-text-secondary">Not run: no example is marked safe</span>
            )}
            {live.example.detail && <div className="mt-1 break-words text-caption-1-regular text-text-secondary">{live.example.detail}</div>}
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
        <Callout tone="warning" className="mb-4">
          This checks the saved version. Save first to test your changes.
        </Callout>
      )}
      <form
        className="flex flex-col gap-2 sm:flex-row sm:items-start"
        onSubmit={(e) => {
          e.preventDefault();
          if (secret.trim()) void run();
        }}
      >
        <Input
          label="Test key"
          className="flex-1"
          type="password"
          autoComplete="off"
          value={secret}
          onChange={setSecret}
          placeholder="Paste an API key"
          isDisabled={readOnly}
          isInvalid={!!err}
          errorMessage={err}
          inputClassName="font-mono"
          inputDir="ltr"
        />
        <Button
          type="submit"
          variant="primary"
          className="sm:mt-6"
          disabled={!secret.trim() || readOnly || busy}
          aria-busy={busy || undefined}
          leadingIcon={busy ? ButtonSpinner : RiKey2Line}
        >
          Run live check
        </Button>
      </form>
      {live && (
        <div className="fade-up mt-5 border-t border-separator-border pt-5">
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
  const [tab, setTab] = useState<TabId>("overview");
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
      <EmptyState
        as="h1"
        icon={RiPlugLine}
        title="Provider not found"
        body="It may have been deleted, or the link is wrong."
        action={
          <RouteButton to="/providers" variant="primary">
            All providers
          </RouteButton>
        }
      />
    );
  if (!draft) return <PageSkeleton variant="list" />;

  const report = validation.state === "done" ? validation.report : null;
  const setSpec = (spec: ProviderSpec) => setDraft((d) => ({ ...d, spec }));
  const specTab = { spec: draft.spec, onChange: setSpec, readOnly };

  const tabs: { id: TabId; label: string; count?: number }[] = [
    { id: "overview", label: "Overview" },
    { id: "connection", label: "Connection" },
    { id: "rules", label: "Rules", count: draft.spec.rules?.length ?? 0 },
    { id: "health", label: "Health check" },
    { id: "examples", label: "Examples", count: draft.spec.examples?.length ?? 0 },
    { id: "tests", label: "Tests", count: draft.spec.tests?.length ?? 0 },
    { id: "json", label: "JSON" },
    ...(!isNew ? [{ id: "versions" as TabId, label: "Versions", count: detail?.versions?.length }, { id: "live" as TabId, label: "Live check" }] : []),
  ];

  const changeTab = (t: TabId) => {
    // The JSON tab may leave fields out; fill them before the structured tabs render.
    if (tab === "json") setDraft((d) => ({ ...d, spec: normalizeSpec(d.spec) }));
    setTab(t);
  };

  const docs = detail ? safeHref(detail.docs_url) : undefined;
  const meta =
    !isNew && detail ? (
      <span className="flex flex-wrap items-center gap-1.5">
        <span>{detail.slug}</span>
        <SourceBadge source={detail.source} />
        <Chip variant="caption" color="neutral" className="font-mono">
          v{detail.version}
        </Chip>
        {detail.status === "disabled" && <Chip variant="caption" color="yellow">disabled</Chip>}
        {/* Kept on one line so a narrow screen never strands "docs" on its own row. */}
        <span className="ms-1 inline-flex items-center gap-1.5 whitespace-nowrap">
          <span>{plural(detail.services_count, "service")}</span>
          {docs && (
            <>
              <span aria-hidden>·</span>
              <LinkButton href={docs} target="_blank" rel="noreferrer" variant="secondary" size="xs" trailingIcon={RiExternalLinkLine} className="font-mono">
                docs
              </LinkButton>
            </>
          )}
        </span>
      </span>
    ) : (
      <span>Write the rules by hand, or let the AI builder write them from a docs URL.</span>
    );

  const actions = readOnly ? undefined : isNew ? (
    <RouteButton to="/providers/ai" variant="secondary" leadingIcon={RiSparkling2Line}>
      Build with AI instead
    </RouteButton>
  ) : (
    <>
      <Button variant="secondary" leadingIcon={RiAddLine} onClick={() => setAddingService(true)}>
        Add service
      </Button>
      <RouteButton to={`/providers/ai?provider=${encodeURIComponent(draft.slug)}`} variant="primary" leadingIcon={RiSparkling2Line}>
        Improve with AI
      </RouteButton>
    </>
  );

  return (
    <div className={(dirty || isNew) && !readOnly ? "pb-28" : undefined}>
      <PageHeader
        back={{ href: "/providers", label: "Providers" }}
        leading={<ProviderLogo provider={{ name: draft.name || "New", color: draft.color, logo_url: detail?.logo_url ?? null }} size="lg" />}
        title={isNew ? draft.name || "New provider" : draft.name}
        meta={meta}
        actions={actions}
        className="mb-0"
      />

      {readOnly && (
        <Callout className="mt-6">You can view this provider. Only admins can change it.</Callout>
      )}

      <div className="mt-8 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className="min-w-0">
          <Tabs selectedKey={tab} onSelectionChange={(k) => changeTab(k as TabId)} className="gap-6">
            {/* The strip scrolls sideways on narrow screens instead of wrapping. */}
            <div className="-mx-4 overflow-x-auto px-4 [scrollbar-width:none] [mask-image:linear-gradient(to_right,black_calc(100%-2rem),transparent)] sm:mx-0 sm:px-0">
              <TabList aria-label="Provider sections" className="w-max min-w-full pe-8">
                {tabs.map((t) => (
                  <Tab key={t.id} id={t.id} count={t.count}>
                    {t.label}
                  </Tab>
                ))}
              </TabList>
            </div>
            <TabPanel id="overview">
              <OverviewTab draft={draft} setDraft={setDraft} isNew={isNew} readOnly={readOnly} detail={detail} errors={errors} />
            </TabPanel>
            <TabPanel id="connection">
              <ConnectionTab {...specTab} />
            </TabPanel>
            <TabPanel id="rules">
              <RulesTab {...specTab} />
            </TabPanel>
            <TabPanel id="health">
              <HealthTab {...specTab} />
            </TabPanel>
            <TabPanel id="examples">
              <ExamplesTab {...specTab} />
            </TabPanel>
            <TabPanel id="tests">
              <TestsTab {...specTab} report={report} />
            </TabPanel>
            <TabPanel id="json">
              <JsonTab {...specTab} />
            </TabPanel>
            {!isNew && (
              <TabPanel id="versions">{detail && <VersionsTab detail={detail} readOnly={readOnly} onRolledBack={() => void load()} />}</TabPanel>
            )}
            {!isNew && (
              <TabPanel id="live">
                <LiveCheckTab slug={draft.slug} dirty={dirty} readOnly={readOnly} />
              </TabPanel>
            )}
          </Tabs>
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
        <div className="fade-up fixed inset-x-0 bottom-[var(--tabbar-h)] z-30 border-t border-separator-border bg-background-primary-default/95 backdrop-blur-sm">
          <div className="mx-auto flex max-w-[1076px] flex-col gap-2 px-4 py-2.5 sm:px-6 sm:py-3 lg:pb-[max(0.75rem,env(safe-area-inset-bottom))]">
            {/* Phones: the version note opens on demand so the bar stays one row above the keyboard. */}
            {!isNew && noteOpen && (
              <Input
                value={note}
                onChange={setNote}
                placeholder="What changed (optional)"
                aria-label="Version note"
                autoFocus
                className="sm:hidden"
              />
            )}
            <div className="flex items-center gap-2">
              <div className="flex min-w-0 flex-1 items-center gap-2 text-body-2-regular text-text-secondary">
                <span className="size-2 shrink-0 rounded-full bg-accent-500" aria-hidden="true" />
                <span className="truncate sm:hidden">{isNew ? "Not created" : "Unsaved"}</span>
                <span className="hidden truncate sm:inline">{isNew ? "New provider, not created yet" : "Unsaved changes. Saving creates a new version."}</span>
              </div>
              {!isNew && (
                <Input
                  value={note}
                  onChange={setNote}
                  placeholder="What changed (optional)"
                  aria-label="Version note"
                  className="hidden sm:flex sm:w-64"
                />
              )}
              {!isNew && (
                <Button
                  size="small"
                  variant="ghost"
                  className="sm:hidden"
                  aria-expanded={noteOpen}
                  aria-label={note.trim() ? "Version note, added" : "Add a version note"}
                  leadingIcon={note.trim() ? RiCheckLine : undefined}
                  onClick={() => setNoteOpen(!noteOpen)}
                >
                  Note
                </Button>
              )}
              {!isNew && (
                <Button variant="ghost" onClick={discard}>
                  Discard
                </Button>
              )}
              <Button
                variant="primary"
                disabled={saving}
                aria-busy={saving || undefined}
                leadingIcon={saving ? ButtonSpinner : undefined}
                onClick={() => void save()}
              >
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
