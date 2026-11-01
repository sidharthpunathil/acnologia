import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import {
  RiArrowLeftLine,
  RiArrowRightLine,
  RiCheckLine,
  RiFileCopyLine,
  RiFileTextLine,
  RiListCheck,
  RiMenuLine,
  RiRefreshLine,
  RiRobot2Line,
} from "@remixicon/react";
import { useTitle } from "@/hooks/useTitle";
import { useTheme } from "@/stores/theme";
import { toast } from "@/stores/toast";
import { copyText } from "@/components/CopyButton";
import { Callout } from "@/components/acn/callout";
import { Button, ButtonLink } from "@/components/base/buttons/button";
import { Drawer, DrawerBody, DrawerHeader } from "@/components/base/drawer/drawer";
import { MENU_ITEM, MENU_ITEM_ACTIVE, MENU_ITEM_INTERACTIVE } from "@/components/base/dropdown/menu-styles";
import { Popover, PopoverTrigger } from "@/components/base/popover/popover";
import { Skeleton, SkeletonText } from "@/components/base/skeleton/skeleton";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { cx } from "@/utils/cx";
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

/** The docs list: menu rows from BoardUI's menu recipe, the current page marked. */
function DocNav({ current, onPick }: { current?: string; onPick?: () => void }) {
  const row = (on: boolean) =>
    cx(
      MENU_ITEM,
      on ? MENU_ITEM_ACTIVE : MENU_ITEM_INTERACTIVE,
      "min-h-9 px-2.5 py-1.5 text-body-2-regular focus-visible:ring-2 focus-visible:ring-border-focus-ring",
      on ? "text-body-2-medium text-text-primary" : "text-text-secondary hover:text-text-primary",
    );
  return (
    <nav aria-label="Docs">
      <p className="px-2.5 text-caption-2-semibold text-text-tertiary uppercase">Guides</p>
      <ul className="mt-2 flex flex-col gap-0.5">
        {DOCS.map((d) => {
          const on = d.slug === current;
          return (
            <li key={d.slug}>
              <Link to={`/docs/${d.slug}`} onClick={onPick} aria-current={on ? "page" : undefined} className={row(on)}>
                <d.icon
                  className={cx("size-4 shrink-0", on ? "text-status-purple-text" : "text-foreground-icon-tertiary")}
                  aria-hidden
                />
                {d.title}
              </Link>
            </li>
          );
        })}
      </ul>
      <p className="mt-8 px-2.5 text-caption-2-semibold text-text-tertiary uppercase">For agents</p>
      <ul className="mt-2 flex flex-col gap-0.5">
        {[
          ["/llms.txt", "llms.txt"],
          ["/llms-full.txt", "llms-full.txt"],
        ].map(([href, label]) => (
          <li key={href}>
            <a href={href} className={cx(row(false), "font-mono")}>
              <RiRobot2Line className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
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
  label = true,
}: {
  headings: DocHeading[];
  active: string | null;
  onPick?: (id: string, e: MouseEvent<HTMLAnchorElement>) => void;
  label?: boolean;
}) {
  const items = headings.filter((h) => h.depth === 2 || h.depth === 3);
  if (!items.length) return null;
  return (
    <nav aria-label="On this page">
      {label && <p className="text-caption-2-semibold text-text-tertiary uppercase">On this page</p>}
      <ul className={cx("flex flex-col border-s border-separator-border", label && "mt-3")}>
        {items.map((h) => {
          const on = h.id === active;
          return (
            <li key={h.id}>
              <a
                href={`#${h.id}`}
                onClick={onPick ? (e) => onPick(h.id, e) : undefined}
                aria-current={on ? "location" : undefined}
                className={cx(
                  "-ms-px block rounded-e-md border-s-2 py-1 text-body-2-regular outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-border-focus-ring",
                  h.depth === 3 ? "ps-6" : "ps-3",
                  on ? "border-accent-500 text-text-primary" : "border-transparent text-text-secondary hover:text-text-primary",
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

/** A sticky side column that scrolls on its own, with the BoardUI soft top edge once scrolled. */
function SideScroller({ children, className }: { children: ReactNode; className?: string }) {
  const fade = useScrollFade();
  return (
    <div className="sticky top-14 h-[calc(100dvh-3.5rem)]">
      <div className="relative h-full">
        <div onScroll={fade.onScroll} className={cx("no-scrollbar h-full overflow-y-auto overscroll-contain py-10", className)}>
          {children}
        </div>
        <ScrollFade progress={fade.progress} surfaceClassName="from-background-full" />
      </div>
    </div>
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
    if (ok) toast.success("Copied the page as Markdown");
    else toast.error("Could not copy the page");
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopy("idle"), 2000);
  };

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button
        size="small"
        variant="secondary"
        onClick={onCopy}
        disabled={copy === "busy"}
        leadingIcon={copy === "done" ? RiCheckLine : RiFileCopyLine}
        className="justify-center sm:min-w-[10.5rem]"
      >
        {copy === "done" ? "Copied" : copy === "error" ? "Could not copy" : "Copy as Markdown"}
      </Button>
      <ButtonLink size="small" variant="secondary" href={mdUrl(slug)} target="_blank" rel="noopener" leadingIcon={RiFileTextLine}>
        View .md
      </ButtonLink>
      <ButtonLink size="small" variant="secondary" href="/llms.txt" target="_blank" rel="noopener" leadingIcon={RiRobot2Line}>
        <span className="hidden sm:inline">Open&nbsp;</span>llms.txt
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

function PrevNext({ doc }: { doc: DocEntry }) {
  const i = DOCS.findIndex((d) => d.slug === doc.slug);
  const prev = DOCS[i - 1];
  const next = DOCS[i + 1];
  const card = cx(
    "group button-press-motion flex min-w-0 flex-1 flex-col rounded-3xl border border-border-button-default bg-background-primary-default px-5 py-4 shadow-xs",
    "outline-none transition-[border-color,background-color] duration-150 ease-out hover:border-border-button-hover hover:bg-background-primary-hover",
    "focus-visible:ring-2 focus-visible:ring-border-focus-ring",
  );
  return (
    <nav aria-label="More docs" className="mt-16 flex flex-col gap-3 border-t border-separator-border pt-8 sm:flex-row">
      {prev ? (
        <Link to={`/docs/${prev.slug}`} className={card}>
          <span className="inline-flex items-center gap-1 text-caption-1-regular text-text-tertiary">
            <RiArrowLeftLine className="size-3.5" aria-hidden />
            Previous
          </span>
          <span className="mt-1 truncate text-headline-medium text-text-primary">{prev.title}</span>
        </Link>
      ) : (
        <span className="hidden flex-1 sm:block" />
      )}
      {next ? (
        <Link to={`/docs/${next.slug}`} className={cx(card, "sm:items-end sm:text-end")}>
          <span className="inline-flex items-center gap-1 text-caption-1-regular text-text-tertiary">
            Next
            <RiArrowRightLine className="size-3.5" aria-hidden />
          </span>
          <span className="mt-1 truncate text-headline-medium text-text-primary">{next.title}</span>
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
  const [tocOpen, setTocOpen] = useState(false);
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

  const pickInPopover = useCallback(
    (id: string, e: MouseEvent<HTMLAnchorElement>) => {
      e.preventDefault();
      setTocOpen(false);
      navigate({ hash: id }, { replace: true });
      document.getElementById(id)?.scrollIntoView({ block: "start" });
    },
    [navigate],
  );

  const headerNav = (
    <nav className="hidden items-center gap-1 lg:flex" aria-label="Docs sections">
      {[
        { to: "/docs#guides", label: "Guides" },
        { to: "/docs/api", label: "HTTP API" },
      ].map((l) => (
        <Link key={l.to} to={l.to} className={HEADER_LINK}>
          {l.label}
        </Link>
      ))}
      <a href="/llms-full.txt" className={HEADER_LINK}>
        Full reference
      </a>
    </nav>
  );

  return (
    <div className="min-h-dvh bg-background-full text-text-primary">
      <a
        href="#doc-content"
        className="sr-only z-50 rounded-lg bg-button-primary px-3 py-2 text-body-2-medium text-text-white focus:not-sr-only focus:fixed focus:start-3 focus:top-3"
      >
        Skip to content
      </a>
      <DocsHeader nav={headerNav} />

      {/* Phones and tablets: one bar that opens the docs list and outline. */}
      <div className="sticky top-14 z-10 border-b border-separator-border bg-background-full/90 backdrop-blur-sm lg:hidden">
        <div className="mx-auto flex h-11 max-w-6xl items-center gap-2 px-4 sm:px-6">
          <button
            type="button"
            onClick={() => setMenuOpen(true)}
            aria-haspopup="dialog"
            aria-expanded={menuOpen}
            className="-ms-2 inline-flex min-h-10 min-w-0 cursor-pointer items-center gap-2 rounded-lg px-2 text-body-2-regular text-text-secondary outline-none transition-colors duration-150 hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          >
            <RiMenuLine className="size-4 shrink-0" aria-hidden />
            <span className="shrink-0">Docs</span>
            <span aria-hidden className="text-text-tertiary">
              /
            </span>
            <span className="truncate text-body-2-medium text-text-primary">{doc?.title ?? "Not found"}</span>
          </button>
        </div>
      </div>

      <Drawer placement="start" size="sm" isOpen={menuOpen} onOpenChange={setMenuOpen}>
        <DrawerHeader title="Docs" />
        <DrawerBody>
          <div className="flex flex-col gap-8 pb-2">
            <DocNav current={slug} onPick={() => setMenuOpen(false)} />
            {headings.length ? (
              <div className="px-2.5">
                <Toc headings={headings} active={active} onPick={pickInMenu} />
              </div>
            ) : null}
          </div>
        </DrawerBody>
      </Drawer>

      <div className="mx-auto max-w-6xl px-4 sm:px-6 lg:grid lg:grid-cols-[13rem_minmax(0,1fr)] lg:gap-10 xl:grid-cols-[12.5rem_minmax(0,1fr)_12rem]">
        <aside className="hidden lg:block">
          <SideScroller className="pe-1">
            <DocNav current={slug} />
          </SideScroller>
        </aside>

        <main id="doc-content" ref={main} tabIndex={-1} className="min-w-0 pt-8 pb-24 outline-none lg:pt-10">
          {doc ? (
            <>
              <div className="mb-8 flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
                <p className="hidden text-body-2-regular text-text-tertiary lg:block">
                  <Link to="/docs" className="rounded-sm outline-none transition-colors duration-150 hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring">
                    Docs
                  </Link>
                  <span aria-hidden className="mx-1.5">
                    /
                  </span>
                  <span className="text-text-secondary">{doc.title}</span>
                </p>
                <div className="flex flex-wrap items-center gap-2">
                  {headings.some((h) => h.depth === 2 || h.depth === 3) ? (
                    // Between 1024 and 1280 there is no outline column; the outline lives in a popover.
                    <span className="hidden lg:inline-flex xl:hidden">
                      <PopoverTrigger isOpen={tocOpen} onOpenChange={setTocOpen}>
                        <Button size="small" variant="secondary" leadingIcon={RiListCheck}>
                          On this page
                        </Button>
                        <Popover placement="bottom end" aria-label="On this page" className="w-72" dialogClassName="max-h-[60vh] overflow-y-auto p-3">
                          <Toc headings={headings} active={active} label={false} onPick={pickInPopover} />
                        </Popover>
                      </PopoverTrigger>
                    </span>
                  ) : null}
                  <DocActions slug={doc.slug} />
                </div>
              </div>
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
            <div className="py-10">
              <h1 className="text-title-1-semibold text-text-primary">No doc called “{slug}”</h1>
              <p className="mt-3 text-body-regular text-text-secondary">Pick one of these instead.</p>
              <ul className="mt-6 grid gap-3 sm:grid-cols-2">
                {DOCS.map((d) => (
                  <li key={d.slug}>
                    <Link
                      to={`/docs/${d.slug}`}
                      className="block rounded-3xl border border-border-button-default bg-background-primary-default p-4 shadow-xs outline-none transition-colors duration-150 hover:border-border-button-hover focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                    >
                      <span className="text-headline-semibold text-text-primary">{d.title}</span>
                      <span className="mt-1 block text-body-regular text-text-secondary">{d.blurb}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </main>

        <aside className="hidden xl:block">
          <SideScroller>
            <Toc headings={headings} active={active} />
          </SideScroller>
        </aside>
      </div>
    </div>
  );
}

const HEADER_LINK =
  "rounded-lg px-2 py-1.5 text-body-2-regular text-text-secondary outline-none transition-colors duration-150 hover:bg-background-secondary-default hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring";
