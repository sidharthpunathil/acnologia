import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { AlertCircle, ArrowLeft, ArrowRight, Bot, Check, Copy, FileText, Menu, RotateCw } from "lucide-react";
import { useTitle } from "../../hooks/useTitle";
import { useTheme } from "../../stores/theme";
import { copyText } from "../../components/CopyButton";
import { Modal } from "../../components/Modal";
import { cx } from "../../components/ui";
import { DocsHeader } from "./DocsHeader";
import { extractHeadings, type DocHeading } from "./headings";
import { DOCS, docBySlug, mdUrl, type DocEntry } from "./manifest";

// The renderer (react-markdown and remark-gfm) is its own chunk, so the rest
// of the dashboard does not carry it.
const Markdown = lazy(() => import("./Markdown").then((m) => ({ default: m.Markdown })));

// Fetched Markdown, kept for the session so moving between docs is instant.
const cache = new Map<string, string>();

async function fetchDoc(slug: string, signal?: AbortSignal): Promise<string> {
  const hit = cache.get(slug);
  if (hit !== undefined) return hit;
  const res = await fetch(mdUrl(slug), { signal, headers: { Accept: "text/markdown" } });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const text = await res.text();
  cache.set(slug, text);
  return text;
}

type Load = { state: "loading" } | { state: "error"; message: string } | { state: "ready"; text: string; slug: string };

function useDoc(slug: string | undefined) {
  const [load, setLoad] = useState<Load>(() => {
    const hit = slug ? cache.get(slug) : undefined;
    return hit !== undefined && slug ? { state: "ready", text: hit, slug } : { state: "loading" };
  });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!slug || !docBySlug(slug)) return;
    const hit = cache.get(slug);
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

/** Which heading the reader is in: the last one scrolled past the header. */
function useActiveHeading(headings: DocHeading[]) {
  const [active, setActive] = useState<string | null>(null);
  useEffect(() => {
    if (!headings.length) return;
    let frame = 0;
    const update = () => {
      frame = 0;
      let current: string | null = null;
      for (const h of headings) {
        const el = document.getElementById(h.id);
        if (!el) continue;
        if (el.getBoundingClientRect().top <= 140) current = h.id;
        else break;
      }
      setActive(current);
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(update);
    };
    update();
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    return () => {
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [headings]);
  return active;
}

function DocNav({ current, onPick }: { current?: string; onPick?: () => void }) {
  return (
    <nav aria-label="Docs">
      <p className="px-2.5 font-mono text-[11px] uppercase tracking-[0.14em] text-faint">Guides</p>
      <ul className="mt-2 space-y-0.5">
        {DOCS.map((d) => {
          const on = d.slug === current;
          return (
            <li key={d.slug}>
              <Link
                to={`/docs/${d.slug}`}
                onClick={onPick}
                aria-current={on ? "page" : undefined}
                className={cx(
                  "tap flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-[13.5px] transition-colors duration-150 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ink",
                  on ? "bg-surface font-medium text-ink-strong shadow-card" : "text-muted hover:bg-ink/[0.04] hover:text-ink",
                )}
              >
                <d.icon className={cx("h-4 w-4 shrink-0", on ? "text-brand" : "text-faint")} aria-hidden />
                {d.title}
              </Link>
            </li>
          );
        })}
      </ul>
      <p className="mt-8 px-2.5 font-mono text-[11px] uppercase tracking-[0.14em] text-faint">For agents</p>
      <ul className="mt-2 space-y-0.5 text-[13.5px]">
        {[
          ["/llms.txt", "llms.txt"],
          ["/llms-full.txt", "llms-full.txt"],
        ].map(([href, label]) => (
          <li key={href}>
            <a
              href={href}
              className="tap flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 font-mono text-[12.5px] text-muted transition-colors duration-150 hover:bg-ink/[0.04] hover:text-ink focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ink"
            >
              <Bot className="h-4 w-4 shrink-0 text-faint" aria-hidden />
              {label}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  );
}

function Toc({
  headings,
  active,
  onPick,
}: {
  headings: DocHeading[];
  active: string | null;
  onPick?: (id: string, e: MouseEvent<HTMLAnchorElement>) => void;
}) {
  const items = headings.filter((h) => h.depth === 2 || h.depth === 3);
  if (!items.length) return null;
  return (
    <nav aria-label="On this page">
      <p className="font-mono text-[11px] uppercase tracking-[0.14em] text-faint">On this page</p>
      <ul className="mt-3 space-y-0.5 border-l border-line">
        {items.map((h) => {
          const on = h.id === active;
          return (
            <li key={h.id}>
              <a
                href={`#${h.id}`}
                onClick={onPick ? (e) => onPick(h.id, e) : undefined}
                aria-current={on ? "location" : undefined}
                className={cx(
                  "-ml-px block border-l py-1 text-[13px] leading-snug transition-colors duration-150 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ink",
                  h.depth === 3 ? "pl-6" : "pl-3",
                  on ? "border-brand text-ink-strong" : "border-transparent text-muted hover:text-ink",
                )}
              >
                {h.text}
              </a>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

type CopyState = "idle" | "busy" | "done" | "error";

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
      const text = cache.get(slug) ?? (await fetchDoc(slug));
      ok = await copyText(text);
    } catch {
      ok = false;
    }
    setCopy(ok ? "done" : "error");
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopy("idle"), 2000);
  };

  const btn =
    "tap inline-flex h-8 items-center gap-1.5 rounded-lg border border-line bg-surface px-3 text-[13px] font-medium text-ink transition-[border-color,transform] duration-150 ease-out hover:border-line-strong active:scale-[0.97] focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ink focus-visible:ring-offset-2 focus-visible:ring-offset-canvas";

  return (
    <div className="flex flex-wrap items-center gap-2">
      <button type="button" onClick={onCopy} disabled={copy === "busy"} className={cx(btn, "justify-center sm:min-w-[10.5rem]")}>
        {copy === "done" ? (
          <span key="done" className="swap-in inline-flex items-center gap-1.5">
            <Check className="h-3.5 w-3.5 text-emerald-600" aria-hidden />
            Copied
          </span>
        ) : copy === "error" ? (
          <span key="err" className="swap-in inline-flex items-center gap-1.5">
            <AlertCircle className="h-3.5 w-3.5 text-red-600" aria-hidden />
            Could not copy
          </span>
        ) : (
          <span className="inline-flex items-center gap-1.5">
            <Copy className="h-3.5 w-3.5" aria-hidden />
            Copy as Markdown
          </span>
        )}
      </button>
      <a href={mdUrl(slug)} target="_blank" rel="noopener" className={btn}>
        <FileText className="h-3.5 w-3.5" aria-hidden />
        View .md
      </a>
      <a href="/llms.txt" target="_blank" rel="noopener" className={btn}>
        <Bot className="h-3.5 w-3.5" aria-hidden />
        <span>
          <span className="hidden sm:inline">Open </span>llms.txt
        </span>
      </a>
      <span className="sr-only" aria-live="polite">
        {copy === "done" ? "Copied the page as Markdown" : copy === "error" ? "Could not copy the page" : ""}
      </span>
    </div>
  );
}

function DocSkeleton() {
  return (
    <div aria-busy="true" className="delayed-in">
      <span className="sr-only">Loading the page</span>
      <div className="skeleton h-9 w-2/3 rounded-lg" />
      <div className="mt-6 space-y-2.5">
        {[100, 96, 88, 92, 60].map((w, i) => (
          <div key={i} className="skeleton h-4 rounded-sm" style={{ width: `${w}%` }} />
        ))}
      </div>
      <div className="skeleton mt-10 h-6 w-1/3 rounded-lg" />
      <div className="mt-4 space-y-2.5">
        {[94, 98, 72].map((w, i) => (
          <div key={i} className="skeleton h-4 rounded-sm" style={{ width: `${w}%` }} />
        ))}
      </div>
    </div>
  );
}

function PrevNext({ doc }: { doc: DocEntry }) {
  const i = DOCS.findIndex((d) => d.slug === doc.slug);
  const prev = DOCS[i - 1];
  const next = DOCS[i + 1];
  const card =
    "tap group flex min-w-0 flex-1 flex-col rounded-xl border border-line bg-surface px-4 py-3 shadow-card transition-[border-color,transform] duration-150 ease-out hover:border-line-strong active:scale-[0.99] focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ink";
  return (
    <nav aria-label="More docs" className="mt-16 flex flex-col gap-3 border-t border-line pt-8 sm:flex-row">
      {prev ? (
        <Link to={`/docs/${prev.slug}`} className={card}>
          <span className="inline-flex items-center gap-1 text-xs text-faint">
            <ArrowLeft className="h-3 w-3" aria-hidden />
            Previous
          </span>
          <span className="mt-1 truncate font-medium text-ink-strong">{prev.title}</span>
        </Link>
      ) : (
        <span className="hidden flex-1 sm:block" />
      )}
      {next ? (
        <Link to={`/docs/${next.slug}`} className={cx(card, "sm:items-end sm:text-right")}>
          <span className="inline-flex items-center gap-1 text-xs text-faint">
            Next
            <ArrowRight className="h-3 w-3" aria-hidden />
          </span>
          <span className="mt-1 truncate font-medium text-ink-strong">{next.title}</span>
        </Link>
      ) : (
        <span className="hidden flex-1 sm:block" />
      )}
    </nav>
  );
}

/** One doc, rendered from /docs/<slug>.md, with docs navigation and an on-page outline. */
export function DocPage() {
  const { slug } = useParams<{ slug: string }>();
  const doc = docBySlug(slug);
  const theme = useTheme((s) => s.theme);
  const location = useLocation();
  const navigate = useNavigate();
  const { load, retry } = useDoc(slug);
  const [menuOpen, setMenuOpen] = useState(false);
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

  const pickInMenu = useCallback(
    (id: string, e: MouseEvent<HTMLAnchorElement>) => {
      e.preventDefault();
      setMenuOpen(false);
      // Wait for the sheet to close and release the page scroll.
      window.setTimeout(() => {
        navigate({ hash: id }, { replace: true });
        document.getElementById(id)?.scrollIntoView({ block: "start" });
      }, 240);
    },
    [navigate],
  );

  const headerNav = (
    <nav className="hidden items-center gap-5 text-[13px] text-muted lg:flex" aria-label="Docs sections">
      <Link to="/docs#guides" className="transition-colors hover:text-ink">
        Guides
      </Link>
      <Link to="/docs/api" className="transition-colors hover:text-ink">
        HTTP API
      </Link>
      <a href="/llms-full.txt" className="transition-colors hover:text-ink">
        Full reference
      </a>
    </nav>
  );

  return (
    <div className="min-h-dvh bg-canvas text-ink">
      <a
        href="#doc-content"
        className="sr-only z-50 rounded-lg bg-ink px-3 py-2 text-sm text-canvas focus:not-sr-only focus:fixed focus:left-3 focus:top-3"
      >
        Skip to content
      </a>
      <DocsHeader nav={headerNav} />

      {/* Phones and tablets: one bar that opens the docs list and outline. */}
      <div className="sticky top-14 z-10 border-b border-line bg-canvas/90 backdrop-blur-sm lg:hidden">
        <div className="mx-auto flex h-11 max-w-6xl items-center gap-2 px-4 sm:px-6">
          <button
            type="button"
            onClick={() => setMenuOpen(true)}
            aria-haspopup="dialog"
            aria-expanded={menuOpen}
            className="tap -ml-2 inline-flex min-w-0 items-center gap-2 rounded-lg px-2 py-1 text-[13px] text-muted transition-colors hover:text-ink focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ink"
          >
            <Menu className="h-4 w-4 shrink-0" aria-hidden />
            <span className="shrink-0">Docs</span>
            <span aria-hidden className="text-faint">/</span>
            <span className="truncate font-medium text-ink">{doc?.title ?? "Not found"}</span>
          </button>
        </div>
      </div>

      <Modal open={menuOpen} onClose={() => setMenuOpen(false)} title="Docs" variant="drawer">
        <div className="space-y-8 pb-2">
          <DocNav current={slug} onPick={() => setMenuOpen(false)} />
          {headings.length ? (
            <div className="px-2.5">
              <Toc headings={headings} active={active} onPick={pickInMenu} />
            </div>
          ) : null}
        </div>
      </Modal>

      <div className="mx-auto max-w-6xl px-4 sm:px-6 lg:grid lg:grid-cols-[13rem_minmax(0,1fr)] lg:gap-10 xl:grid-cols-[12.5rem_minmax(0,1fr)_12rem]">
        <aside className="hidden lg:block">
          <div className="no-scrollbar sticky top-14 max-h-[calc(100dvh-3.5rem)] overflow-y-auto py-10 pr-1">
            <DocNav current={slug} />
          </div>
        </aside>

        <main id="doc-content" ref={main} tabIndex={-1} className="min-w-0 pb-24 pt-8 focus:outline-hidden lg:pt-10">
          {doc ? (
            <>
              <div className="mb-8 flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
                <p className="hidden text-[13px] text-faint lg:block">
                  <Link to="/docs" className="transition-colors hover:text-ink">
                    Docs
                  </Link>
                  <span aria-hidden className="mx-1.5">
                    /
                  </span>
                  <span className="text-muted">{doc.title}</span>
                </p>
                <DocActions slug={doc.slug} />
              </div>
              <article key={doc.slug} className={cx(load.state === "ready" && "fade-up")}>
                {load.state === "loading" ? (
                  <DocSkeleton />
                ) : load.state === "error" ? (
                  <div role="alert" className="rounded-xl border border-line bg-surface p-6 shadow-card">
                    <p className="font-medium text-ink-strong">Could not load this page</p>
                    <p className="mt-1.5 text-sm text-muted">
                      The server did not return {mdUrl(doc.slug)} ({load.message}). Check that Acnologia is running, then try again.
                    </p>
                    <button
                      type="button"
                      onClick={retry}
                      className="tap mt-4 inline-flex h-8 items-center gap-1.5 rounded-lg bg-ink px-3 text-[13px] font-medium text-canvas transition-[opacity,transform] duration-150 ease-out hover:opacity-90 active:scale-[0.97]"
                    >
                      <RotateCw className="h-3.5 w-3.5" aria-hidden />
                      Try again
                    </button>
                  </div>
                ) : (
                  <Suspense fallback={<DocSkeleton />}>
                    <Markdown source={load.text} headings={headings} theme={theme} />
                  </Suspense>
                )}
              </article>
              <PrevNext doc={doc} />
            </>
          ) : (
            <div className="py-10">
              <h1 className="text-2xl font-semibold tracking-tight text-ink-strong">No doc called “{slug}”</h1>
              <p className="mt-3 text-muted">Pick one of these instead.</p>
              <ul className="mt-6 grid gap-3 sm:grid-cols-2">
                {DOCS.map((d) => (
                  <li key={d.slug}>
                    <Link
                      to={`/docs/${d.slug}`}
                      className="tap block rounded-xl border border-line bg-surface p-4 shadow-card transition-colors hover:border-line-strong"
                    >
                      <span className="font-medium text-ink-strong">{d.title}</span>
                      <span className="mt-1 block text-sm text-muted">{d.blurb}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </main>

        <aside className="hidden xl:block">
          <div className="no-scrollbar sticky top-14 max-h-[calc(100dvh-3.5rem)] overflow-y-auto py-10">
            <Toc headings={headings} active={active} />
          </div>
        </aside>
      </div>
    </div>
  );
}
