import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useTitle } from "../../hooks/useTitle";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { ArrowLeft, ChevronRight, GitBranch, Link2, Plus, ShieldCheck, Sparkles, Terminal, X } from "lucide-react";
import { useAi } from "../../stores/ai";
import { useProviders } from "../../stores/providers";
import { useAuth } from "../../stores/auth";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { can } from "../../lib/perms";
import { relTime, sentence } from "../../lib/format";
import { ProviderLogo } from "../../components/EntityLogo";
import { Button, Callout, Field, IconButton, Input, Section, Skeleton, Textarea, Toggle } from "../../components/ui";
import { KeyPasteArea, KeyPreview, useKeyInput } from "../../components/KeyPaste";
import { FadeText, plainText } from "../../components/FadeText";
import { ACTIVE_STATUSES } from "../../stores/ai";
import { RunStatusBadge } from "./AiRunPage";

function UrlList({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="space-y-2">
      {value.map((u, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            type="url"
            inputMode="url"
            value={u}
            onChange={(e) => onChange(value.map((x, j) => (j === i ? e.target.value : x)))}
            placeholder="https://docs.example.com/api"
            aria-label={`Docs URL ${i + 1}`}
            className="font-mono text-[13px]"
          />
          <IconButton label={`Remove URL ${i + 1}`} onClick={() => onChange(value.filter((_, j) => j !== i))} disabled={value.length === 1 && !u}>
            <X className="h-4 w-4" />
          </IconButton>
        </div>
      ))}
      <Button size="sm" variant="ghost" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => onChange([...value, ""])}>
        Add another URL
      </Button>
    </div>
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
  const { status, statusError, fetchStatus, runs, runsLoaded, fetchRuns, start } = useAi();
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

  return (
    <div>
      <Link to={updateSlug ? `/providers/${encodeURIComponent(updateSlug)}` : "/providers"} className="tap mb-5 inline-flex items-center gap-1.5 font-mono text-xs text-muted hover:text-ink">
        <ArrowLeft className="h-3.5 w-3.5" /> {updateSlug ? updateSlug : "Providers"}
      </Link>

      <div className="mb-8 flex items-start gap-4">
        <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-[14px] bg-ink text-on-ink">
          <Sparkles className="h-5 w-5 text-spark" />
        </div>
        <div className="min-w-0">
          <h1 className="font-mono text-[26px] leading-tight tracking-tight text-ink sm:text-[30px]">
            {updateSlug ? `Improve ${provider?.name ?? updateSlug}` : "Build a provider with AI"}
          </h1>
          <p className="mt-2 max-w-2xl text-[14px] leading-relaxed text-muted">
            {updateSlug
              ? "Say what to change or fix. The agent rereads the docs, edits the spec, reruns every test and publishes a new version. You can roll back in one click."
              : "Give it an API name or docs URL. The agent researches the API, writes the error rules, health check, examples and tests, runs the tests, and publishes the provider without a restart."}
          </p>
        </div>
      </div>

      {status === null && !statusError ? (
        <Skeleton className="mb-6 h-12 rounded-xl" />
      ) : !enabled ? (
        <Callout tone="amber" icon={<Terminal className="h-4 w-4" />} className="mb-6">
          <div className="font-medium text-amber-950">The AI builder is off on this server</div>
          <div className="wrap-anywhere mt-1">
            {sentence(status?.reason || statusError || "Add the AI key to your .env, then restart Acnologia")} You can still{" "}
            <Link to="/providers/new" className="font-medium underline underline-offset-2">
              write a provider by hand
            </Link>
            .
          </div>
        </Callout>
      ) : (
        <p className="mb-6 font-mono text-xs text-muted">
          <span className="mr-1.5 inline-block h-1.5 w-1.5 rounded-full bg-emerald-500 align-middle" aria-hidden="true" />
          AI is ready · {status?.model}
        </p>
      )}

      {!manage ? (
        <Callout>Only admins can start an AI build. Recent runs are below.</Callout>
      ) : (
        <form onSubmit={submit} noValidate className="grid grid-cols-1 gap-5 pb-16 lg:grid-cols-[minmax(0,1fr)_320px] lg:pb-0">
          <div className="space-y-5">
            <Section title="What should it build?" description="A provider name or a docs URL is enough. Add anything you know about limits, plans or quirks, and the agent will follow it.">
              {provider && (
                <div className="mb-4 flex items-center gap-3 rounded-xl bg-soft px-3 py-2.5">
                  <ProviderLogo provider={provider} size="sm" />
                  <span className="font-mono text-xs text-muted">
                    updating <span className="text-ink">{provider.slug}</span> · v{provider.version}
                  </span>
                </div>
              )}
              <Field label={updateSlug ? "What to change" : "Context"} error={contextErr}>
                <Textarea
                  id="ai-context"
                  rows={9}
                  value={context}
                  onChange={(e) => {
                    setContext(e.target.value);
                    if (contextErr) setContextErr(null);
                  }}
                  placeholder={updateSlug ? "A 429 with code too_many_requests should be a 10 second rate limit, not out of quota. Add a test for it." : PLACEHOLDER}
                  className="min-h-[200px] text-[14px] leading-relaxed"
                />
              </Field>
            </Section>

            <Section title="Sources" description="Optional. Without them, the agent searches the web itself.">
              <div className="space-y-5">
                <div>
                  <div className="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium text-ink">
                    <Link2 className="h-3.5 w-3.5 text-muted" /> Docs URLs
                  </div>
                  <UrlList value={urls} onChange={setUrls} />
                </div>
                <Field label="GitHub repo" hint="An official SDK or API repo, as owner/repo or a URL. The agent reads how it handles errors.">
                  <Input value={github} onChange={(e) => setGithub(e.target.value)} placeholder="owner/repo" spellCheck={false} className="font-mono" />
                </Field>
              </div>
            </Section>
          </div>

          <div className="space-y-5">
            <Section
              title="Keys"
              description="Optional. The first key is used once for a cheap live check. If the builder creates a service, every key is added to it. Keys stay encrypted during the run and are wiped when it ends."
            >
              <div className="space-y-3">
                <KeyPasteArea input={keys} id="ai-keys" label="Keys" rows={4} placeholder={"Paste keys, one per line or in any format, or drop a file"} />
                {(keys.text.trim() || keys.parsed.keys.length > 0) && <KeyPreview parsed={keys.parsed} stale={keys.stale} show={5} />}
                {keys.parsed.keys.length > 1 && updateSlug && (
                  <p className="text-xs leading-relaxed text-muted">This run updates a provider and does not create a service, so only the first key is used.</p>
                )}
              </div>
            </Section>

            <Section title="When it passes">
              <div className="space-y-4">
                <Toggle
                  label="Publish automatically"
                  description="Goes live once the spec is valid and every test passes. Turn off to review it first."
                  checked={autoPublish}
                  onChange={setAutoPublish}
                />
                {!updateSlug && (
                  <Toggle
                    label="Create a service"
                    description="Adds it to the catalog right away, ready for keys."
                    checked={createService}
                    onChange={setCreateService}
                  />
                )}
              </div>
              <p className="mt-4 flex items-start gap-2 text-xs leading-relaxed text-muted">
                <ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                The agent only writes a JSON spec. It cannot run code, and every spec is checked and tested before it goes live.
              </p>
            </Section>

            <Button type="submit" variant="primary" loading={busy} disabled={!enabled} icon={<Sparkles className="h-4 w-4" />} className="hidden w-full lg:inline-flex">
              {updateSlug ? "Start improving" : "Build provider"}
            </Button>
          </div>

          {/* Phones and tablets: the submit bar is pinned to the bottom (above the tab bar), so it stays in reach anywhere in the form. */}
          <div className="fixed inset-x-0 bottom-[var(--tabbar-h)] z-30 border-t border-line bg-surface/95 px-4 py-2.5 backdrop-blur-sm lg:hidden">
            <div className="mx-auto max-w-[1076px] sm:px-2">
              <Button type="submit" variant="primary" loading={busy} disabled={!enabled} icon={<Sparkles className="h-4 w-4" />} className="w-full">
                {updateSlug ? "Start improving" : "Build provider"}
              </Button>
            </div>
          </div>
        </form>
      )}

      <section className="mt-12" aria-labelledby="recent-runs">
        <div className="mb-3 flex items-baseline gap-3">
          <h2 id="recent-runs" className="text-[19px] font-semibold tracking-tight text-ink">
            Recent runs
          </h2>
          {runsLoaded && <span className="font-mono text-xs text-faint">{runs.length}</span>}
        </div>
        {!runsLoaded ? (
          <div className="space-y-2">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-[60px] rounded-card" />
            ))}
          </div>
        ) : runs.length === 0 ? (
          <p className="rounded-card border border-dashed border-line-strong px-5 py-8 text-center text-sm text-muted">
            No runs yet. Your first build shows up here with every step it took.
          </p>
        ) : (
          <ul className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card">
            {runs.slice(0, 20).map((r) => (
              <li key={r.id}>
                <Link to={`/providers/ai/${r.id}`} className="flex min-h-[60px] items-center gap-3 px-4 py-3 transition-colors hover:bg-soft">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <FadeText text={plainText(r.title) || r.provider_slug || `Run #${r.id}`} className="truncate text-[14px] font-medium text-ink" />
                      <span className="shrink-0">
                        <RunStatusBadge status={r.status} />
                      </span>
                    </div>
                    {plainText(r.description) && (
                      <FadeText text={plainText(r.description)} className="mt-0.5 line-clamp-2 max-w-3xl text-[13px] leading-snug text-muted" />
                    )}
                    <div className="mt-0.5 truncate font-mono text-[11px] text-muted">
                      #{r.id} · {r.kind === "update_provider" ? "update" : "create"}
                      {r.provider_slug ? ` · ${r.provider_slug}` : ""} · {r.created_by} · {relTime(r.created_at)}
                      {r.usage?.cost_usd ? ` · $${r.usage.cost_usd.toFixed(2)}` : ""}
                    </div>
                  </div>
                  <ChevronRight className="h-4 w-4 shrink-0 text-faint" />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>
      <p className="mt-4 flex items-center gap-1.5 font-mono text-[11px] text-faint">
        <GitBranch className="h-3 w-3 shrink-0" /> Every publish is a new provider version. Roll back from the provider page.
      </p>
    </div>
  );
}
