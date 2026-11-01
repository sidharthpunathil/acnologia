import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Focusable } from "react-aria-components";
import {
  RiAddLine,
  RiArrowLeftLine,
  RiArrowRightSLine,
  RiCloseLine,
  RiGitBranchLine,
  RiLink,
  RiShieldCheckLine,
  RiSparkling2Line,
  RiTerminalBoxLine,
} from "@remixicon/react";
import { useTitle } from "../../hooks/useTitle";
import { useAi, ACTIVE_STATUSES, CLEARABLE_STATUSES } from "../../stores/ai";
import { useProviders } from "../../stores/providers";
import { useAuth } from "../../stores/auth";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { can } from "../../lib/perms";
import { relTime, sentence } from "../../lib/format";
import { ProviderLogo } from "../../components/EntityLogo";
import { KeyPasteArea, KeyPreview, useKeyInput } from "../../components/KeyPaste";
import { FadeText, plainText } from "../../components/FadeText";
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Input } from "@/components/base/input/input";
import { Textarea } from "@/components/base/textarea/textarea";
import { Switch } from "@/components/base/switch/switch";
import { Badge } from "@/components/base/badges/badge";
import { StatusDot } from "@/components/base/badges/status-dot";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { cx } from "@/utils/cx";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Callout, ConfirmButton, EmptyState, Section } from "@/components/acn";
import { RunStatusBadge } from "./AiRunPage";

function UrlList({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="flex flex-col gap-2">
      {value.map((u, i) => {
        const removeOff = value.length === 1 && !u;
        return (
          <div key={i} className="flex items-center gap-2">
            <Input
              type="url"
              inputMode="url"
              value={u}
              onChange={(v) => onChange(value.map((x, j) => (j === i ? v : x)))}
              placeholder="https://docs.example.com/api"
              aria-label={`Docs URL ${i + 1}`}
              inputDir="ltr"
              className="min-w-0 flex-1"
              fieldClassName="font-mono"
            />
            {removeOff ? (
              // A disabled button cannot take focus, so it gets no tooltip.
              <IconButton icon={RiCloseLine} aria-label={`Remove URL ${i + 1}`} disabled />
            ) : (
              <TooltipTrigger delay={300}>
                <Focusable>
                  <IconButton icon={RiCloseLine} aria-label={`Remove URL ${i + 1}`} onClick={() => onChange(value.filter((_, j) => j !== i))} />
                </Focusable>
                <Tooltip>Remove</Tooltip>
              </TooltipTrigger>
            )}
          </div>
        );
      })}
      <Button type="button" size="small" variant="ghost" leadingIcon={RiAddLine} onClick={() => onChange([...value, ""])} className="w-fit">
        Add another URL
      </Button>
    </div>
  );
}

/** A switch row: label and description on the start side, the switch on the end. */
function SwitchRow({ label, description, isSelected, onChange }: { label: string; description: ReactNode; isSelected: boolean; onChange: (v: boolean) => void }) {
  return (
    <Switch isSelected={isSelected} onChange={onChange} className="w-full flex-row-reverse items-start justify-between gap-4">
      <span className="flex flex-col gap-0.5">
        <span className="text-body-medium text-text-primary">{label}</span>
        <span className="text-caption-1-regular text-text-secondary">{description}</span>
      </span>
    </Switch>
  );
}

const PLACEHOLDER = `An API name, a docs URL, or as much detail as you like. For example:

"Acme Mail email API. The free plan allows 100 emails a day and 2 requests a second. A 429 with daily_quota_exceeded means the quota is gone until midnight UTC. Any other 429 is a short rate limit. GET /domains is a cheap way to check a key."`;

export function AiBuilderPage() {
  useTitle("Build with AI");
  const [params] = useSearchParams();
  const updateSlug = params.get("provider") ?? "";
  const navigate = useNavigate();
  const role = useAuth((s) => s.user?.role);
  const manage = can.manage(role);
  const { status, statusError, fetchStatus, runs, runsLoaded, fetchRuns, start, remove, clearFinished } = useAi();
  const provider = useProviders((s) => (updateSlug ? s.providers.find((p) => p.slug === updateSlug) : undefined));
  const ensureProviders = useProviders((s) => s.ensure);

  const [context, setContext] = useState("");
  const [urls, setUrls] = useState<string[]>([""]);
  const [github, setGithub] = useState("");
  const keys = useKeyInput();
  const [autoPublish, setAutoPublish] = useState(true);
  const [createService, setCreateService] = useState(!updateSlug);
  const [busy, setBusy] = useState(false);
  const [contextErr, setContextErr] = useState<string | null>(null);

  useEffect(() => {
    void fetchStatus();
    fetchRuns().catch(() => undefined);
    ensureProviders().catch(() => undefined);
  }, [fetchStatus, fetchRuns, ensureProviders]);

  useEffect(() => setCreateService(!updateSlug), [updateSlug]);

  // Titles and descriptions arrive a moment after a run starts, so refresh while any run is still going.
  const anyActive = runs.some((r) => ACTIVE_STATUSES.has(r.status));
  const clearable = runs.filter((r) => CLEARABLE_STATUSES.has(r.status)).length;

  const deleteRun = async (id: number) => {
    try {
      await remove(id);
      toast.success(`Deleted run #${id}`);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };
  const clearRuns = async () => {
    try {
      const n = await clearFinished();
      toast.success(n === 1 ? "Cleared 1 run" : `Cleared ${n} runs`);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };
  useEffect(() => {
    if (!anyActive) return;
    const t = window.setInterval(() => fetchRuns().catch(() => undefined), 4000);
    return () => window.clearInterval(t);
  }, [anyActive, fetchRuns]);

  const enabled = !!status?.enabled;
  const cleanUrls = useMemo(() => urls.map((u) => u.trim()).filter(Boolean), [urls]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!context.trim() && !cleanUrls.length && !github.trim()) {
      setContextErr("Tell the AI which API to build. A name or a docs URL is enough.");
      document.getElementById("ai-context")?.focus();
      return;
    }
    const bad = cleanUrls.find((u) => !/^https?:\/\//i.test(u));
    if (bad) {
      toast.error(`"${bad}" is not a full URL. Start it with https://`);
      return;
    }
    const secrets = keys.parsed.keys.map((k) => k.secret);
    setBusy(true);
    try {
      const run = await start({
        kind: updateSlug ? "update_provider" : "create_provider",
        provider_slug: updateSlug || undefined,
        context: context.trim(),
        urls: cleanUrls,
        github: github.trim(),
        test_key: secrets[0],
        test_keys: secrets.length ? secrets : undefined,
        auto_publish: autoPublish,
        create_service: updateSlug ? false : createService,
      });
      keys.reset();
      navigate(`/providers/ai/${run.id}`);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const submitLabel = updateSlug ? "Start improving" : "Build provider";

  return (
    <div>
      <Link
        to={updateSlug ? `/providers/${encodeURIComponent(updateSlug)}` : "/providers"}
        className="mb-5 inline-flex items-center gap-1 rounded-md text-body-2-medium text-text-secondary outline-none transition-colors duration-150 ease hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
      >
        <RiArrowLeftLine className="size-4" aria-hidden /> <span className={updateSlug ? "font-mono" : undefined}>{updateSlug || "Providers"}</span>
      </Link>

      <header className="mb-8 flex items-start gap-4">
        <div className="flex size-12 shrink-0 items-center justify-center rounded-2xl border border-border-button-default bg-background-tertiary-default text-status-purple-text">
          <RiSparkling2Line className="size-6" aria-hidden />
        </div>
        <div className="min-w-0">
          <h1 className="break-words font-mono text-title-1-medium text-text-primary">
            {updateSlug ? `Improve ${provider?.name ?? updateSlug}` : "Build a provider with AI"}
          </h1>
          <p className="mt-1.5 max-w-2xl text-body-regular text-text-secondary">
            {updateSlug
              ? "Say what to change or fix. The agent rereads the docs, edits the spec, reruns every test and publishes a new version. You can roll back in one click."
              : "Give it an API name or docs URL. The agent researches the API, writes the error rules, health check, examples and tests, runs the tests, and publishes the provider without a restart."}
          </p>
        </div>
      </header>

      {status === null && !statusError ? (
        <Skeleton className="mb-6 h-12 rounded-2xl" />
      ) : !enabled ? (
        <Callout tone="warning" icon={RiTerminalBoxLine} title="The AI builder is off on this server" className="mb-6">
          <div className="mt-0.5 wrap-anywhere">
            {sentence(status?.reason || statusError || "Add the AI key to your .env, then restart Acnologia")} You can still{" "}
            <Link
              to="/providers/new"
              className="rounded-sm text-body-2-semibold underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
            >
              write a provider by hand
            </Link>
            .
          </div>
        </Callout>
      ) : (
        <p className="mb-6 flex items-center gap-1.5 font-mono text-caption-1-regular text-text-secondary">
          <StatusDot color="green" aria-hidden />
          AI is ready · {status?.model}
        </p>
      )}

      {!manage ? (
        <Callout>Only admins can start an AI build. Recent runs are below.</Callout>
      ) : (
        <form onSubmit={submit} noValidate className="grid grid-cols-1 gap-5 pb-16 lg:grid-cols-[minmax(0,1fr)_340px] lg:pb-0">
          <div className="flex min-w-0 flex-col gap-5">
            <Section title="What should it build?" description="A provider name or a docs URL is enough. Add anything you know about limits, plans or quirks, and the agent will follow it.">
              {provider && (
                <div className="mb-4 flex items-center gap-3 rounded-2xl bg-background-secondary-default px-3 py-2.5">
                  <ProviderLogo provider={provider} size="sm" />
                  <span className="font-mono text-caption-1-regular text-text-secondary">
                    updating <span className="text-text-primary">{provider.slug}</span> · v{provider.version}
                  </span>
                </div>
              )}
              <Textarea
                id="ai-context"
                label={updateSlug ? "What to change" : "Context"}
                rows={9}
                value={context}
                onChange={(v) => {
                  setContext(v);
                  if (contextErr) setContextErr(null);
                }}
                isInvalid={!!contextErr}
                hint={contextErr ?? undefined}
                placeholder={updateSlug ? "A 429 with code too_many_requests should be a 10 second rate limit, not out of quota. Add a test for it." : PLACEHOLDER}
                fieldClassName="min-h-[200px]"
              />
            </Section>

            <Section title="Sources" description="Optional. Without them, the agent searches the web itself.">
              <div className="flex flex-col gap-5">
                <fieldset className="flex flex-col gap-1.5">
                  <legend className="mb-1.5 flex items-center gap-1.5 text-body-medium text-text-primary">
                    <RiLink className="size-4 text-foreground-icon-secondary" aria-hidden /> Docs URLs
                  </legend>
                  <UrlList value={urls} onChange={setUrls} />
                </fieldset>
                <Input
                  label="GitHub repo"
                  hint="An official SDK or API repo, as owner/repo or a URL. The agent reads how it handles errors."
                  value={github}
                  onChange={setGithub}
                  placeholder="owner/repo"
                  spellCheck="false"
                  inputDir="ltr"
                  fieldClassName="font-mono"
                />
              </div>
            </Section>
          </div>

          <div className="flex min-w-0 flex-col gap-5">
            <Section
              title="Keys"
              description="Optional. The first key is used once for a cheap live check. If the builder creates a service, every key is added to it. Keys stay encrypted during the run and are wiped when it ends."
            >
              <div className="flex flex-col gap-3">
                <KeyPasteArea input={keys} id="ai-keys" label="Keys" rows={4} placeholder={"Paste keys, one per line or in any format, or drop a file"} />
                {(keys.text.trim() || keys.parsed.keys.length > 0) && <KeyPreview parsed={keys.parsed} stale={keys.stale} show={5} />}
                {keys.parsed.keys.length > 1 && updateSlug && (
                  <p className="text-caption-1-regular text-text-secondary">This run updates a provider and does not create a service, so only the first key is used.</p>
                )}
              </div>
            </Section>

            <Section title="When it passes">
              <div className="flex flex-col gap-4">
                <SwitchRow
                  label="Publish automatically"
                  description="Goes live once the spec is valid and every test passes. Turn off to review it first."
                  isSelected={autoPublish}
                  onChange={setAutoPublish}
                />
                {!updateSlug && (
                  <SwitchRow
                    label="Create a service"
                    description="Adds it to the catalog right away, ready for keys."
                    isSelected={createService}
                    onChange={setCreateService}
                  />
                )}
              </div>
              <p className="mt-4 flex items-start gap-2 text-caption-1-regular text-text-secondary">
                <RiShieldCheckLine className="mt-px size-3.5 shrink-0 text-foreground-icon-tertiary" aria-hidden />
                The agent only writes a JSON spec. It cannot run code, and every spec is checked and tested before it goes live.
              </p>
            </Section>

            <Button
              type="submit"
              variant="primary"
              disabled={!enabled || busy}
              aria-busy={busy || undefined}
              leadingIcon={busy ? ButtonSpinner : RiSparkling2Line}
              className="hidden w-full lg:inline-flex"
            >
              {submitLabel}
            </Button>
          </div>

          {/* Phones and tablets: the submit bar is pinned to the bottom (above the tab bar), so it stays in reach anywhere in the form. */}
          <div className="fixed inset-x-0 bottom-[var(--tabbar-h)] z-30 border-t border-separator-border bg-background-full/90 px-4 py-2.5 backdrop-blur-sm lg:hidden">
            <div className="mx-auto max-w-[1076px] sm:px-2">
              <Button
                type="submit"
                variant="primary"
                disabled={!enabled || busy}
                aria-busy={busy || undefined}
                leadingIcon={busy ? ButtonSpinner : RiSparkling2Line}
                className="h-11 w-full"
              >
                {submitLabel}
              </Button>
            </div>
          </div>
        </form>
      )}

      <section className="mt-12" aria-labelledby="recent-runs">
        <div className="mb-3 flex items-center gap-2">
          <h2 id="recent-runs" className="text-title-3-semibold text-text-primary">
            Recent runs
          </h2>
          {runsLoaded && <Badge className="font-mono tabular-nums">{runs.length}</Badge>}
          {manage && clearable > 0 && (
            <ConfirmButton
              className="ml-auto"
              label="Clear finished"
              confirmLabel={clearable === 1 ? "Clear 1 run" : `Clear ${clearable} runs`}
              description="Deletes failed, cancelled and published runs with their steps. Runs waiting for review stay. Published providers and Usage spend are not affected."
              onConfirm={clearRuns}
            />
          )}
        </div>
        {!runsLoaded ? (
          <div className="flex flex-col gap-2">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-[64px] rounded-2xl" />
            ))}
          </div>
        ) : runs.length === 0 ? (
          <EmptyState compact icon={RiSparkling2Line} title="No runs yet" body="Your first build shows up here with every step it took." />
        ) : (
          <ul className="divide-y divide-separator-border overflow-hidden rounded-3xl border border-border-button-default bg-background-primary-default">
            {runs.slice(0, 20).map((r) => (
              <li key={r.id} className="group/row relative">
                <Link
                  to={`/providers/ai/${r.id}`}
                  className={cx(
                    "group flex min-h-[64px] items-center gap-3 px-5 py-3 outline-none transition-colors duration-150 ease",
                    "hover:bg-background-secondary-default active:bg-background-secondary-hover",
                    "focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-border-focus-ring",
                  )}
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <FadeText text={plainText(r.title) || r.provider_slug || `Run #${r.id}`} className="truncate text-body-medium text-text-primary" />
                      <span className="shrink-0">
                        <RunStatusBadge status={r.status} />
                      </span>
                    </div>
                    {plainText(r.description) && (
                      <FadeText text={plainText(r.description)} className="mt-0.5 line-clamp-2 max-w-3xl text-body-2-regular text-text-secondary" />
                    )}
                    <div className="mt-0.5 truncate font-mono text-caption-1-regular text-text-tertiary">
                      #{r.id} · {r.kind === "update_provider" ? "update" : "create"}
                      {r.provider_slug ? ` · ${r.provider_slug}` : ""} · {r.created_by} · {relTime(r.created_at)}
                      {r.usage?.cost_usd ? ` · $${r.usage.cost_usd.toFixed(2)}` : ""}
                    </div>
                  </div>
                  {manage && !ACTIVE_STATUSES.has(r.status) && (
                    // Reserves room for the delete button, which sits over the row outside the link.
                    <span className="size-8 shrink-0" aria-hidden />
                  )}
                  <RiArrowRightSLine
                    className="size-4 shrink-0 text-foreground-icon-tertiary transition-transform duration-150 ease-out group-hover:translate-x-0.5 motion-reduce:transition-none"
                    aria-hidden
                  />
                </Link>
                {manage && !ACTIVE_STATUSES.has(r.status) && (
                  <div className="absolute right-11 top-1/2 -translate-y-1/2">
                    <ConfirmButton
                      iconOnly
                      label={`Delete run #${r.id}`}
                      confirmLabel="Delete run"
                      description={
                        r.status === "needs_review"
                          ? "Deletes this run and its unpublished draft."
                          : "Deletes this run and its steps. Providers it published stay."
                      }
                      onConfirm={() => deleteRun(r.id)}
                    />
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
      <p className="mt-4 flex items-center gap-1.5 font-mono text-caption-1-regular text-text-tertiary">
        <RiGitBranchLine className="size-3.5 shrink-0" aria-hidden /> Every publish is a new provider version. Roll back from the provider page.
      </p>
    </div>
  );
}
