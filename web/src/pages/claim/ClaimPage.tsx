// The claim link a person opens: what is shared and by whom, then, after
// "Get my access", their token (or the keys) shown once.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import type { Key as AriaKey } from "react-aria-components";
import {
  RiArrowRightLine,
  RiDownloadLine,
  RiErrorWarningLine,
  RiEyeLine,
  RiLinkUnlinkM,
  RiRefreshLine,
  RiShieldKeyholeLine,
  RiTimeLine,
  RiUserForbidLine,
} from "@remixicon/react";
import { useTitle } from "../../hooks/useTitle";
import { dateTime } from "../../lib/format";
import { errorMessage, ApiError } from "../../lib/api";
import {
  authType,
  claim,
  claimErrorKind,
  hasLimits,
  isReveal,
  limitsOf,
  previewView,
  type ClaimErrorKind,
  type ClaimService,
  type PreviewView,
  type RedeemProxy,
  type RedeemReveal,
  type RedeemResult,
} from "../../lib/claim";
import { proxySnippets } from "../service/IntegratePanel";
import { mcpSnippets } from "../AppMcpPanel";
import { limitSummary } from "../sharing/Limits";
import { Countdown } from "../sharing/Timers";
import { EntityLogo } from "../../components/EntityLogo";
import { GuideView } from "../../components/GuideView";
import type { GuideResponse } from "../../lib/types";
import { Button } from "@/components/base/buttons/button";
import { ButtonSpinner, Spinner } from "@/components/base/spinner/spinner";
import { SegmentedControl, SegmentedControlItem } from "@/components/base/segmented-control/segmented-control";
import { Callout, CodeBlock, CopyButton, EmptyState, SecretBox } from "@/components/acn";
import { cx } from "@/utils/cx";
import { PublicShell } from "./PublicRoutes";

type Lang = "curl" | "python" | "typescript";
const LANGS: { id: Lang; label: string }[] = [
  { id: "curl", label: "curl" },
  { id: "python", label: "Python" },
  { id: "typescript", label: "TypeScript" },
];

function Card({ children, className, labelledBy }: { children: ReactNode; className?: string; labelledBy?: string }) {
  return (
    <section aria-labelledby={labelledBy} className={cx("rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:p-6", className)}>
      {children}
    </section>
  );
}

function ServiceList({ services }: { services: ClaimService[] }) {
  if (!services.length) return <p className="text-body-2-regular text-text-secondary">No services listed.</p>;
  return (
    <ul className="flex flex-col gap-2">
      {services.map((s) => (
        <li key={s.slug} className="flex min-w-0 items-center gap-3">
          <EntityLogo src={s.logo_url ?? null} name={s.name || s.slug} size="sm" />
          <span className="min-w-0 wrap-anywhere text-body-2-medium text-text-primary">{s.name || s.slug}</span>
          <code dir="ltr" className="ms-auto shrink-0 font-mono text-caption-1-regular text-text-secondary">
            {s.slug}
          </code>
        </li>
      ))}
    </ul>
  );
}

function EndsLine({ endsAt }: { endsAt: string | null | undefined }) {
  if (!endsAt) return <>No end date. The owner can still pause or revoke it.</>;
  return (
    <span className="flex flex-col">
      <span>{dateTime(endsAt)}, your time</span>
      <Countdown iso={endsAt} className="text-caption-1-regular text-text-secondary" />
    </span>
  );
}

function Facts({ rows }: { rows: { label: string; value: ReactNode }[] }) {
  return (
    <dl className="grid grid-cols-1 gap-3 sm:grid-cols-[8rem_1fr] sm:gap-x-4">
      {rows.map((r) => (
        <div key={r.label} className="contents">
          <dt className="text-caption-1-medium text-text-secondary sm:pt-0.5">{r.label}</dt>
          <dd className="-mt-2 min-w-0 text-body-2-regular text-text-primary sm:mt-0">{r.value}</dd>
        </div>
      ))}
    </dl>
  );
}

// ---- errors ----

const ERRORS: Record<Exclude<ClaimErrorKind, "other">, { icon: typeof RiTimeLine; title: string; body: (owner: string) => string }> = {
  already_claimed: {
    icon: RiUserForbidLine,
    title: "This link was already used",
    body: (o) => `Each link works once. If that was not you, tell ${o} so they can revoke it and send you a new link.`,
  },
  full: { icon: RiUserForbidLine, title: "This link is full", body: (o) => `It was claimed as many times as ${o} allowed. Ask them for a new link.` },
  expired: { icon: RiTimeLine, title: "This link has expired", body: (o) => `Ask ${o} to send you a new link.` },
  invalid: {
    icon: RiLinkUnlinkM,
    title: "This link does not work",
    body: () => "Check that you opened the whole link, or ask the person who sent it for a new one.",
  },
  rate_limited: { icon: RiErrorWarningLine, title: "Too many tries", body: () => "Wait a minute, then try again." },
};

function ClaimError({ kind, message, owner, onRetry }: { kind: ClaimErrorKind; message: string; owner: string; onRetry?: () => void }) {
  const e = kind === "other" ? null : ERRORS[kind];
  return (
    <div role="alert">
      <EmptyState
        as="h1"
        icon={e?.icon ?? RiErrorWarningLine}
        title={e?.title ?? "Something went wrong"}
        body={e ? e.body(owner) : message}
        action={
          onRetry && (kind === "rate_limited" || kind === "other") ? (
            <Button variant="secondary" leadingIcon={RiRefreshLine} onClick={onRetry}>
              Try again
            </Button>
          ) : undefined
        }
      />
    </div>
  );
}

// ---- before redeem ----

function ModeNote({ mode, owner }: { mode: "proxy" | "reveal"; owner: string }) {
  if (mode === "reveal")
    return (
      <Callout tone="warning" icon={RiEyeLine} title="You will see the API keys once">
        Copy them somewhere safe when they appear. {owner} can ask you to stop using them at any time, and will change them when this share ends.
      </Callout>
    );
  return (
    <Callout tone="info" icon={RiShieldKeyholeLine} title="You get a personal token">
      Your calls go through {owner}'s server, which adds the key. You never see a key, and {owner} can pause or end your access at any time.
    </Callout>
  );
}

function Preview({ p, busy, onRedeem }: { p: PreviewView; busy: boolean; onRedeem: () => void }) {
  const limits = limitSummary(p.limits);
  return (
    <Card labelledBy="claim-title">
      <p className="text-caption-1-medium text-text-secondary">
        <bdi>{p.owner}</bdi> shared access with you
      </p>
      <h1 id="claim-title" className="mt-1 wrap-anywhere text-title-2-medium text-text-primary">
        {p.name || `Access from ${p.owner}`}
      </h1>
      <div className="mt-5 flex flex-col gap-5">
        <div>
          <h2 className="mb-2 text-body-medium text-text-primary">What you can use</h2>
          <ServiceList services={p.services} />
        </div>
        <ModeNote mode={p.mode} owner={p.owner} />
        <Facts
          rows={[
            { label: "Ends", value: <EndsLine endsAt={p.endsAt} /> },
            ...(p.mode === "proxy"
              ? [{ label: "Limits", value: hasLimits(p.limits) ? (p.reusable ? `${limits}, shared by everyone with this link` : limits) : "No limits" }]
              : []),
            ...(p.linkExpiresAt
              ? [{ label: "This link", value: p.reusable ? <>Shared, works until {dateTime(p.linkExpiresAt)}</> : <>Works once, until {dateTime(p.linkExpiresAt)}</> }]
              : []),
          ]}
        />
        <div className="flex flex-col gap-2 border-t border-separator-border pt-5">
          <Button
            variant="primary"
            className="w-full sm:w-auto sm:self-start"
            onClick={onRedeem}
            disabled={busy}
            aria-busy={busy || undefined}
            leadingIcon={busy ? ButtonSpinner : undefined}
            trailingIcon={busy ? undefined : RiArrowRightLine}
          >
            Get my access
          </Button>
          <p className="text-caption-1-regular text-text-secondary">
            {p.reusable
              ? "You get a token of your own. It is shown once, so have somewhere ready to save it."
              : <>This uses up the link. {p.mode === "reveal" ? "The keys are" : "Your token is"} shown once, so have somewhere ready to save {p.mode === "reveal" ? "them" : "it"}.</>}
          </p>
        </div>
      </div>
    </Card>
  );
}

// ---- after redeem: proxy ----

/** The origin snippets call: base_url without a trailing /proxy or slash, or this page's origin. */
function hostOf(base: string | undefined): string {
  const b = (base || window.location.origin).replace(/\/+$/, "");
  return b.replace(/\/proxy$/, "");
}

function mcpName(owner: string): string {
  const slug = owner
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 30)
    .replace(/-+$/, "");
  return slug ? `acnologia-${slug}` : "acnologia";
}

function ShownOnce() {
  return (
    <Callout tone="warning" title="This is shown once. Save it now.">
      Leaving or reloading this page hides it for good. If you lose it, ask for a new link.
    </Callout>
  );
}

function ProxyResult({ r, owner }: { r: RedeemProxy; owner: string }) {
  const [lang, setLang] = useState<Lang>("curl");
  const host = hostOf(r.base_url);
  const services = (r.services ?? []).filter((s) => s && s.slug);
  const mcpUrl = r.mcp_url || `${host}/mcp`;
  const mcp = mcpSnippets(mcpName(owner), mcpUrl, r.token);
  const limits = limitsOf(r.limits);
  return (
    <>
      <Card labelledBy="got-title">
        <h1 id="got-title" className="text-title-2-medium text-text-primary">
          Your access is ready
        </h1>
        <p className="mt-1 text-body-2-regular text-text-secondary">
          Shared by <bdi>{owner}</bdi>. Keep the token private: anyone with it can use your access.
        </p>
        <div className="mt-4 flex flex-col gap-4">
          <ShownOnce />
          <div className="flex flex-col gap-1.5">
            <h2 className="text-body-medium text-text-primary">Your token</h2>
            <SecretBox value={r.token} />
          </div>
          <div className="flex flex-col gap-1.5">
            <h2 className="text-body-medium text-text-primary">Base URL</h2>
            <SecretBox value={host} />
          </div>
          <Facts
            rows={[
              { label: "Ends", value: <EndsLine endsAt={r.ends_at} /> },
              { label: "Limits", value: hasLimits(limits) ? limitSummary(limits) : "No limits" },
            ]}
          />
        </div>
      </Card>

      {services.length > 0 && (
        <Card labelledBy="code-title">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0">
              <h2 id="code-title" className="text-headline-semibold text-text-primary">
                Call it from code
              </h2>
              <p className="mt-1 text-body-2-regular text-text-secondary">
                {lang === "typescript" ? "Set ACNOLOGIA_TOKEN to your token first." : "Your token is already filled in."}
              </p>
            </div>
            <SegmentedControl
              aria-label="Language"
              selectedKeys={new Set<AriaKey>([lang])}
              onSelectionChange={(sel) => {
                const next = [...sel][0];
                if (next !== undefined) setLang(String(next) as Lang);
              }}
              className="w-full sm:w-auto"
            >
              {LANGS.map((l) => (
                <SegmentedControlItem key={l.id} id={l.id} className="min-h-9 flex-1 sm:min-h-0 sm:flex-none">
                  {l.label}
                </SegmentedControlItem>
              ))}
            </SegmentedControl>
          </div>
          <div className="mt-4 flex flex-col gap-5">
            {services.map((s) => {
              const a = authType(s.auth);
              const code = proxySnippets({ slug: s.slug, base_url: "", auth_type: a.type, auth_name: a.name }, host, s.examples, r.token)[lang];
              return (
                <div key={s.slug} className="flex min-w-0 flex-col gap-2">
                  <h3 className="flex items-center gap-2 text-body-medium text-text-primary">
                    <EntityLogo src={s.logo_url ?? null} name={s.name || s.slug} size="xs" />
                    <span className="min-w-0 wrap-anywhere">{s.name || s.slug}</span>
                  </h3>
                  <CodeBlock code={code} label={`${s.slug} · ${LANGS.find((l) => l.id === lang)?.label}`} />
                </div>
              );
            })}
          </div>
        </Card>
      )}

      <ServiceGuides services={services} />

      <Card labelledBy="mcp-title">
        <h2 id="mcp-title" className="text-headline-semibold text-text-primary">
          Use it from an AI agent
        </h2>
        <p className="mt-1 text-body-2-regular text-text-secondary">The same token works over MCP, with the services above as tools.</p>
        <div className="mt-4 flex flex-col gap-4">
          <div className="flex min-w-0 flex-col gap-2">
            <h3 className="text-body-medium text-text-primary">Claude Code</h3>
            <CodeBlock code={mcp["claude-code"][0].code} label="terminal" />
            <p className="text-caption-1-regular text-text-secondary">Run it in your project folder. Add --scope user to use it in every project.</p>
          </div>
          <div className="flex min-w-0 flex-col gap-2">
            <h3 className="text-body-medium text-text-primary">Cursor</h3>
            <CodeBlock code={mcp.cursor[0].code} label=".cursor/mcp.json" />
            <p className="text-caption-1-regular text-text-secondary">Put it in .cursor/mcp.json in your project, or ~/.cursor/mcp.json for every project.</p>
          </div>
        </div>
      </Card>

      <Card>
        <p className="text-body-2-regular text-text-primary">Check your usage, limits and time left any time with your token.</p>
        <Link
          to="/me"
          className="mt-2 inline-flex items-center gap-1 rounded-md text-body-2-medium text-text-primary underline underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring pointer-coarse:min-h-11"
        >
          Open my access page
          <RiArrowRightLine className="size-4" aria-hidden />
        </Link>
      </Card>
    </>
  );
}

/** Saves Markdown as a file, named after the service. */
function downloadMd(slug: string, md: string) {
  const url = URL.createObjectURL(new Blob([md], { type: "text/markdown" }));
  const a = Object.assign(document.createElement("a"), { href: url, download: `${slug}-guide.md` });
  a.click();
  URL.revokeObjectURL(url);
}

/**
 * The usage guide for each shared service, the same one the provider page
 * shows: rendered, with Copy for LLM and Download .md. One guide at a time,
 * so heading anchors stay unique.
 */
function ServiceGuides({ services }: { services: ClaimService[] }) {
  const withGuide = services.filter((s) => s.guide);
  const [pick, setPick] = useState(withGuide[0]?.slug ?? "");
  const cur = withGuide.find((s) => s.slug === pick) ?? withGuide[0];
  if (!cur?.guide) return null;
  const md = cur.guide;
  return (
    <div className="flex flex-col gap-3">
      {withGuide.length > 1 && (
        <SegmentedControl
          aria-label="Service guide"
          selectedKeys={new Set<AriaKey>([cur.slug])}
          onSelectionChange={(sel) => {
            const next = [...sel][0];
            if (next !== undefined) setPick(String(next));
          }}
          className="w-full overflow-x-auto sm:w-auto sm:self-start"
        >
          {withGuide.map((s) => (
            <SegmentedControlItem key={s.slug} id={s.slug} className="min-h-9 flex-1 sm:min-h-0 sm:flex-none">
              {s.name || s.slug}
            </SegmentedControlItem>
          ))}
        </SegmentedControl>
      )}
      <GuideView
        title={withGuide.length > 1 ? `How to use ${cur.name || cur.slug}` : "How to use it"}
        description="Endpoints, parameters, examples, limits and errors. Send your token where it says acn_. Save it, or give it to your AI agent."
        fetcher={() => Promise.resolve({ markdown: md, generated: false } as GuideResponse)}
        fetchKey={cur.slug}
        actions={
          <Button variant="secondary" size="small" leadingIcon={RiDownloadLine} onClick={() => downloadMd(cur.slug, md)}>
            Download .md
          </Button>
        }
      />
    </div>
  );
}

// ---- after redeem: reveal ----

function RevealResult({ r, owner }: { r: RedeemReveal; owner: string }) {
  const all = r.keys.map((k) => `# ${k.service}${k.label ? `: ${k.label}` : ""}\n${k.secret}`).join("\n\n");
  return (
    <Card labelledBy="keys-title">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h1 id="keys-title" className="text-title-2-medium text-text-primary">
            Your keys
          </h1>
          <p className="mt-1 text-body-2-regular text-text-secondary">
            Shared by <bdi>{owner}</bdi>. Call each provider with its key directly.
          </p>
        </div>
        {r.keys.length > 1 && <CopyButton text={all} label="Copy all" />}
      </div>
      <div className="mt-4 flex flex-col gap-4">
        <ShownOnce />
        <Callout tone="neutral" icon={RiEyeLine}>
          {owner} can ask you to stop using these keys at any time, and will change them when this share ends.
        </Callout>
        {r.keys.length === 0 ? (
          <p className="text-body-2-regular text-text-secondary">No keys were in scope. Ask {owner} to check the share.</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {r.keys.map((k, i) => {
              const a = authType(k.auth);
              const how = a.type === "query" ? `in the ${a.name || "api_key"} query parameter` : a.type === "header" ? `in the ${a.name || "Authorization"} header` : "as a Bearer token";
              return (
                <li key={`${k.service}-${i}`} className="flex min-w-0 flex-col gap-2 rounded-2xl border border-border-button-default p-3 sm:p-4">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <EntityLogo src={null} name={k.service} size="xs" />
                    <span className="min-w-0 wrap-anywhere text-body-2-medium text-text-primary">{k.label || "Key"}</span>
                    <code dir="ltr" className="font-mono text-caption-1-regular text-text-secondary">
                      {k.service}
                    </code>
                  </div>
                  <SecretBox value={k.secret} />
                  {(k.base_url || k.auth) && (
                    <p className="wrap-anywhere text-caption-1-regular text-text-secondary">
                      {k.base_url && (
                        <>
                          Calls go to{" "}
                          <code dir="ltr" className="font-mono text-text-primary">
                            {k.base_url}
                          </code>
                          .{" "}
                        </>
                      )}
                      Send the key {how}.
                    </p>
                  )}
                </li>
              );
            })}
          </ul>
        )}
        <Facts rows={[{ label: "Ends", value: <EndsLine endsAt={r.ends_at} /> }]} />
      </div>
    </Card>
  );
}

// ---- page ----

export function ClaimPage() {
  useTitle("Your access");
  const { code = "" } = useParams();
  const [preview, setPreview] = useState<PreviewView | null>(null);
  const [loadErr, setLoadErr] = useState<{ kind: ClaimErrorKind; message: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<RedeemResult | null>(null);
  const [redeemErr, setRedeemErr] = useState<{ kind: ClaimErrorKind; message: string } | null>(null);
  const top = useRef<HTMLDivElement>(null);

  const load = async () => {
    setLoadErr(null);
    try {
      setPreview(previewView(await claim.preview(code)));
    } catch (e) {
      setLoadErr({ kind: claimErrorKind(e), message: errorMessage(e) });
    }
  };

  useEffect(() => {
    void load();
    // The code is the whole input; load once per code.
  }, [code]);

  // Once the token or keys are on screen, ask before the page goes away.
  useEffect(() => {
    if (!result) return;
    const warn = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [result]);

  const redeem = async () => {
    setBusy(true);
    setRedeemErr(null);
    try {
      const r = await claim.redeem(code);
      setResult(r);
      // Start at the top, where the "shown once" warning is.
      window.scrollTo({ top: 0 });
      window.setTimeout(() => top.current?.focus(), 0);
    } catch (e) {
      setRedeemErr({ kind: claimErrorKind(e), message: e instanceof ApiError ? e.message : errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  const owner = preview?.owner ?? "the owner";
  let body: ReactNode;
  if (result) {
    body = isReveal(result) ? <RevealResult r={result} owner={owner} /> : <ProxyResult r={result} owner={owner} />;
  } else if (redeemErr && redeemErr.kind !== "rate_limited" && redeemErr.kind !== "other") {
    body = <ClaimError kind={redeemErr.kind} message={redeemErr.message} owner={owner} />;
  } else if (loadErr) {
    body = <ClaimError kind={loadErr.kind} message={loadErr.message} owner={owner} onRetry={() => void load()} />;
  } else if (!preview) {
    body = (
      <div className="flex justify-center py-16" role="status" aria-live="polite">
        <Spinner size="lg" aria-label="Loading the link" />
      </div>
    );
  } else {
    body = (
      <>
        {redeemErr && (
          <Callout tone="error" title={redeemErr.kind === "rate_limited" ? "Too many tries" : "That did not work"}>
            {redeemErr.kind === "rate_limited" ? "Wait a minute, then press Get my access again." : redeemErr.message}
          </Callout>
        )}
        <Preview p={preview} busy={busy} onRedeem={() => void redeem()} />
      </>
    );
  }

  return (
    <PublicShell>
      <div ref={top} tabIndex={-1} className="flex flex-col gap-4 outline-none">
        {body}
      </div>
    </PublicShell>
  );
}
