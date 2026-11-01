import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import {
  RiArrowLeftLine,
  RiArrowRightLine,
  RiArrowUpLine,
  RiCheckLine,
  RiFileCopyLine,
  RiFileTextLine,
  RiListCheck,
  RiRefreshLine,
  RiRobot2Line,
} from "@remixicon/react";
import { useTitle } from "@/hooks/useTitle";
import { useTheme } from "@/stores/theme";
import { toast } from "@/stores/toast";
import { copyText } from "@/components/CopyButton";
import { Callout } from "@/components/acn/callout";
import { Breadcrumb, BreadcrumbItem } from "@/components/base/breadcrumb/breadcrumb";
import { Button, ButtonLink } from "@/components/base/buttons/button";
import { Skeleton, SkeletonText } from "@/components/base/skeleton/skeleton";
import { cx } from "@/utils/cx";
import { DocsLayout, SideScroller } from "./DocsLayout";
import { extractHeadings, type DocHeading } from "./headings";
import { DOCS, docBySlug, groupOf, mdUrl, type DocEntry } from "./manifest";
import { cachedDoc, fetchDoc } from "./source";
import { Toc, tocItems, useActiveHeading } from "./Toc";

// The renderer (react-markdown and remark-gfm) is its own chunk, so the rest
// of the dashboard does not carry it.
const Markdown = lazy(() => import("./Markdown").then((m) => ({ default: m.Markdown })));

type Load = { state: "loading" } | { state: "error"; message: string } | { state: "ready"; text: string; slug: string };

function useDoc(slug: string | undefined) {
  const [load, setLoad] = useState<Load>(() => {
    const hit = slug ? cachedDoc(slug) : undefined;
    return hit !== undefined && slug ? { state: "ready", text: hit, slug } : { state: "loading" };
  });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!slug || !docBySlug(slug)) return;
    const hit = cachedDoc(slug);
    if (hit !== undefined) {
      setLoad({ state: "ready", text: hit, slug });
      return;
    }
    const ac = new AbortController();
    setLoad({ state: "loading" });
    fetchDoc(slug, ac.signal)
      .then((text) => setLoad({ state: "ready", text, slug }))
      .catch((e: unknown) => {
        if (ac.signal.aborted) return;
        setLoad({ state: "error", message: e instanceof Error ? e.message : "request failed" });
      });
    return () => ac.abort();
  }, [slug, attempt]);
  // Until the effect catches up with a new slug, do not show the previous doc.
  const current: Load = load.state === "ready" && load.slug !== slug ? { state: "loading" } : load;
  return { load: current, retry: () => setAttempt((n) => n + 1) };
}

const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;

type CopyState = "idle" | "busy" | "done" | "error";

/** Copy the page as Markdown, and the raw .md file. */
function DocActions({ slug }: { slug: string }) {
  const [copy, setCopy] = useState<CopyState>("idle");
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const onCopy = async () => {
    setCopy("busy");
    let ok = false;
    try {
      // The page already fetched this file, so the cached copy is the same text;
      // copying it without waiting keeps Safari's user gesture alive.
      const text = cachedDoc(slug) ?? (await fetchDoc(slug));
      ok = await copyText(text);
    } catch {
      ok = false;
    }
    setCopy(ok ? "done" : "error");
    if (ok) toast.success("Copied the page as Markdown");
    else toast.error("Could not copy the page");
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopy("idle"), 2000);
  };

  return (
    <div className="flex shrink-0 items-center gap-2">
      <Button
        size="small"
        variant="secondary"
        onClick={onCopy}
        disabled={copy === "busy"}
        leadingIcon={copy === "done" ? RiCheckLine : RiFileCopyLine}
        className="min-w-[8.5rem] justify-center"
      >
        {copy === "done" ? "Copied" : copy === "error" ? "Could not copy" : "Copy as .md"}
      </Button>
      <ButtonLink size="small" variant="secondary" href={mdUrl(slug)} target="_blank" rel="noopener" leadingIcon={RiFileTextLine}>
        View .md
      </ButtonLink>
    </div>
  );
}

function DocSkeleton() {
  return (
    <div aria-busy="true" className="delayed-in">
      <span className="sr-only">Loading the page</span>
      <Skeleton className="h-9 w-2/3" />
      <SkeletonText lines={5} className="mt-6" lineClassName="h-4" />
      <Skeleton className="mt-10 h-6 w-1/3" />
      <SkeletonText lines={3} className="mt-4" lineClassName="h-4" />
    </div>
  );
}

/**
 * Below 1280px there is no outline rail, so the outline folds into a
 * disclosure above the page. It renders at once with the doc, disabled until
 * the headings exist, so nothing moves when the Markdown arrives.
 */
function TocDisclosure({
  headings,
  active,
  ready,
  onPick,
}: {
  headings: DocHeading[];
  active: string | null;
  ready: boolean;
  onPick: (id: string, e: MouseEvent<HTMLAnchorElement>) => void;
}) {
  const [open, setOpen] = useState(false);
  const items = tocItems(headings);
  if (ready && !items.length) return null;
  const current = items.find((h) => h.id === active);
  return (
    <details
      open={open}
      onToggle={(e) => setOpen(e.currentTarget.open)}
      className="group mb-8 rounded-3xl border border-border-button-default bg-background-primary-default shadow-xs xl:hidden"
    >
      <summary
        aria-disabled={!ready || undefined}
        onClick={(e) => !ready && e.preventDefault()}
        className={cx(
          "flex min-h-12 cursor-pointer list-none items-center gap-2.5 rounded-3xl px-4 text-body-medium text-text-primary outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring [&::-webkit-details-marker]:hidden",
          !ready && "cursor-default text-text-tertiary",
        )}
      >
        <RiListCheck className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
        <span className="shrink-0">On this page</span>
        {current ? <span className="min-w-0 truncate text-body-regular text-text-tertiary">{current.text}</span> : null}
        <RiArrowRightLine
          className="ms-auto size-4 shrink-0 text-foreground-icon-tertiary transition-transform duration-200 group-open:rotate-90 motion-reduce:transition-none"
          aria-hidden
        />
      </summary>
      <div className="max-h-[50dvh] overflow-y-auto overscroll-contain px-4 pb-4">
        <Toc
          headings={headings}
          active={active}
          onPick={(id, e) => {
            setOpen(false);
            onPick(id, e);
          }}
        />
      </div>
    </details>
  );
}

function PrevNext({ doc }: { doc: DocEntry }) {
  const i = DOCS.findIndex((d) => d.slug === doc.slug);
  const prev = DOCS[i - 1];
  const next = DOCS[i + 1];
  const card = cx(
    "group button-press-motion flex min-w-0 flex-1 flex-col rounded-3xl border border-border-button-default bg-background-primary-default px-5 py-4 shadow-xs",
    "outline-none transition-[border-color,background-color] duration-150 ease-out hover:border-border-button-hover hover:bg-background-primary-hover",
    "focus-visible:ring-2 focus-visible:ring-border-focus-ring",
  );
  const arrow = "size-3.5 transition-transform duration-150 motion-reduce:transition-none";
  return (
    <nav aria-label="Previous and next" className="mt-16 flex flex-col gap-3 border-t border-separator-border pt-8 sm:flex-row">
      {prev ? (
        <Link to={`/docs/${prev.slug}`} className={card} rel="prev">
          <span className="inline-flex items-center gap-1 text-caption-1-regular text-text-tertiary">
            <RiArrowLeftLine className={cx(arrow, "group-hover:-translate-x-0.5")} aria-hidden />
            Previous
          </span>
          <span className="mt-1 truncate text-headline-medium text-text-primary">{prev.title}</span>
        </Link>
      ) : (
        <span className="hidden flex-1 sm:block" />
      )}
      {next ? (
        <Link to={`/docs/${next.slug}`} className={cx(card, "sm:items-end sm:text-end")} rel="next">
          <span className="inline-flex items-center gap-1 text-caption-1-regular text-text-tertiary">
            Next
            <RiArrowRightLine className={cx(arrow, "group-hover:translate-x-0.5")} aria-hidden />
          </span>
          <span className="mt-1 truncate text-headline-medium text-text-primary">{next.title}</span>
        </Link>
      ) : (
        <span className="hidden flex-1 sm:block" />
      )}
    </nav>
  );
}

const RAIL_LINK =
  "inline-flex items-center gap-2 rounded-md text-body-2-regular text-text-secondary outline-none transition-colors duration-150 hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring";

/** One doc, rendered from /docs/<slug>.md, inside the docs frame with an outline rail. */
export function DocPage() {
  const { slug } = useParams<{ slug: string }>();
  const doc = docBySlug(slug);
  const theme = useTheme((s) => s.theme);
  const location = useLocation();
  const navigate = useNavigate();
  const { load, retry } = useDoc(slug);
  const main = useRef<HTMLElement>(null);
  const firstSlug = useRef(slug);
  useTitle(doc ? doc.title : "Docs");

  const text = load.state === "ready" ? load.text : "";
  const headings = useMemo(() => (text ? extractHeadings(text) : []), [text]);
  const active = useActiveHeading(headings);

  // A new doc starts at the top, and focus moves to it for keyboard and screen reader users.
  useEffect(() => {
    if (slug === firstSlug.current) return;
    firstSlug.current = undefined;
    if (!location.hash) window.scrollTo(0, 0);
    main.current?.focus({ preventScroll: true });
  }, [slug]);

  // Deep links such as /docs/flows#mcp land once the content exists.
  // The renderer loads lazily and diagrams change height as they draw, so keep
  // the target in place for a moment, until the reader scrolls themselves.
  useEffect(() => {
    if (load.state !== "ready" || !location.hash) return;
    const id = decodeURIComponent(location.hash.slice(1));
    const until = performance.now() + 4000;
    let frame = 0;
    let stopped = false;
    const stop = () => {
      stopped = true;
    };
    const tick = () => {
      if (stopped || performance.now() > until) return;
      const el = document.getElementById(id);
      if (el) {
        const offset = parseFloat(getComputedStyle(el).scrollMarginTop) || 0;
        if (Math.abs(el.getBoundingClientRect().top - offset) > 4) el.scrollIntoView({ block: "start" });
      }
      frame = requestAnimationFrame(tick);
    };
    tick();
    const opts = { passive: true } as const;
    window.addEventListener("wheel", stop, opts);
    window.addEventListener("touchstart", stop, opts);
    window.addEventListener("keydown", stop);
    window.addEventListener("pointerdown", stop);
    return () => {
      stop();
      cancelAnimationFrame(frame);
      window.removeEventListener("wheel", stop);
      window.removeEventListener("touchstart", stop);
      window.removeEventListener("keydown", stop);
      window.removeEventListener("pointerdown", stop);
    };
  }, [load.state, location.hash, slug]);

  /** Outline links scroll in place (smoothly unless reduced motion is on) and keep the hash shareable. */
  const pick = useCallback(
    (id: string, e: MouseEvent<HTMLAnchorElement>) => {
      e.preventDefault();
      navigate({ hash: id }, { replace: true });
      document.getElementById(id)?.scrollIntoView({ block: "start", behavior: reducedMotion() ? "auto" : "smooth" });
    },
    [navigate],
  );

  const rail = doc ? (
    <SideScroller className="ps-1">
      {tocItems(headings).length ? (
        <nav aria-label="On this page">
          <p className="text-caption-1-semibold text-text-tertiary">On this page</p>
          <Toc headings={headings} active={active} onPick={pick} className="mt-3" />
        </nav>
      ) : load.state === "loading" ? (
        <div aria-hidden>
          <Skeleton className="h-3 w-20" />
          <SkeletonText lines={6} className="mt-4" lineClassName="h-3" />
        </div>
      ) : null}
      <div className="mt-8 flex flex-col items-start gap-2.5 border-t border-separator-border pt-5">
        <a href={mdUrl(doc.slug)} target="_blank" rel="noopener" className={RAIL_LINK}>
          <RiFileTextLine className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
          View as Markdown
        </a>
        <a href="/llms.txt" className={RAIL_LINK}>
          <RiRobot2Line className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
          llms.txt for agents
        </a>
        <button
          type="button"
          onClick={() => window.scrollTo({ top: 0, behavior: reducedMotion() ? "auto" : "smooth" })}
          className={cx(RAIL_LINK, "cursor-pointer")}
        >
          <RiArrowUpLine className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
          Back to top
        </button>
      </div>
    </SideScroller>
  ) : undefined;

  return (
    <DocsLayout current={slug} rail={rail}>
      <main id="doc-content" ref={main} tabIndex={-1} className="min-w-0 pt-6 pb-24 outline-none lg:pt-8">
        <div className="max-w-[42rem]">
          {doc ? (
            <>
              <div className="mb-6 flex flex-wrap items-center justify-between gap-x-4 gap-y-3">
                <Breadcrumb className="w-auto min-w-0">
                  <BreadcrumbItem href="/docs">Docs</BreadcrumbItem>
                  <BreadcrumbItem href={`/docs#group-${doc.group}`}>{groupOf(doc).title}</BreadcrumbItem>
                  <BreadcrumbItem current>{doc.title}</BreadcrumbItem>
                </Breadcrumb>
                <DocActions slug={doc.slug} />
              </div>
              <TocDisclosure headings={headings} active={active} ready={load.state !== "loading"} onPick={pick} />
              <article key={doc.slug} className={cx(load.state === "ready" && "fade-up")}>
                {load.state === "loading" ? (
                  <DocSkeleton />
                ) : load.state === "error" ? (
                  <Callout
                    tone="error"
                    title="Could not load this page"
                    action={
                      <Button size="small" variant="secondary" leadingIcon={RiRefreshLine} onClick={retry}>
                        Try again
                      </Button>
                    }
                  >
                    The server did not return {mdUrl(doc.slug)} ({load.message}). Check that Acnologia is running, then try again.
                  </Callout>
                ) : (
                  <Suspense fallback={<DocSkeleton />}>
                    <Markdown source={load.text} headings={headings} theme={theme} />
                  </Suspense>
                )}
              </article>
              <PrevNext doc={doc} />
            </>
          ) : (
            <div className="py-6">
              <h1 className="text-title-1-semibold text-text-primary">No doc called “{slug}”</h1>
              <p className="mt-3 text-body-regular text-text-secondary">Pick one of these instead.</p>
              <ul className="mt-6 grid gap-3 sm:grid-cols-2">
                {DOCS.map((d) => (
                  <li key={d.slug}>
                    <Link
                      to={`/docs/${d.slug}`}
                      className="block h-full rounded-3xl border border-border-button-default bg-background-primary-default p-4 shadow-xs outline-none transition-colors duration-150 hover:border-border-button-hover focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                    >
                      <span className="text-headline-semibold text-text-primary">{d.title}</span>
                      <span className="mt-1 block text-body-regular text-text-secondary">{d.blurb}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      </main>
    </DocsLayout>
  );
}
