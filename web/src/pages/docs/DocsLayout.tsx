import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Focusable } from "react-aria-components";
import { RiArrowRightLine, RiMenuLine, RiMoonLine, RiRobot2Line, RiSearchLine, RiSunLine } from "@remixicon/react";
import { useAuth } from "@/stores/auth";
import { useTheme } from "@/stores/theme";
import { Brand } from "@/components/acn/brand";
import { Button } from "@/components/base/buttons/button";
import { Drawer, DrawerBody, DrawerHeader } from "@/components/base/drawer/drawer";
import { Kbd } from "@/components/base/kbd/kbd";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { cx } from "@/utils/cx";
import { DocsSearch } from "./DocsSearch";
import { DOC_GROUPS, docsInGroup } from "./manifest";
import { RouterButton } from "./RouterButton";

/** Width of the docs frame: the top bar and the three columns share it, so their edges line up. */
export const DOCS_FRAME = "mx-auto w-full max-w-[88rem] px-4 sm:px-6 lg:px-8";

const FOCUS = "outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring";

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
export const SEARCH_SHORTCUT = isMac ? "⌘K" : "Ctrl K";

const SearchContext = createContext<() => void>(() => {});
/** Opens the docs search dialog. */
export const useOpenDocsSearch = () => useContext(SearchContext);

function editable(t: EventTarget | null) {
  const el = t as HTMLElement | null;
  return !!el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName));
}

/** "/" anywhere outside a field, or Cmd or Ctrl K, opens search. */
function useSearchShortcut(open: () => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      const k = e.key.toLowerCase();
      if ((e.metaKey || e.ctrlKey) && k === "k") {
        e.preventDefault();
        open();
      } else if (e.key === "/" && !e.metaKey && !e.ctrlKey && !e.altKey && !editable(e.target)) {
        e.preventDefault();
        open();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);
}

/** An icon button with its name in a BoardUI tooltip. */
function IconAction({ label, icon, onClick, className }: { label: string; icon: typeof RiSunLine; onClick: () => void; className?: string }) {
  return (
    <TooltipTrigger delay={400}>
      <Focusable>
        <Button variant="ghost" size="small" iconOnly leadingIcon={icon} aria-label={label} onClick={onClick} className={cx("pointer-coarse:size-11", className)} />
      </Focusable>
      <Tooltip>{label}</Tooltip>
    </TooltipTrigger>
  );
}

function ThemeButton() {
  const theme = useTheme((s) => s.theme);
  const setPref = useTheme((s) => s.setPref);
  const dark = theme === "dark";
  return <IconAction label={dark ? "Use light theme" : "Use dark theme"} icon={dark ? RiSunLine : RiMoonLine} onClick={() => setPref(dark ? "light" : "dark")} />;
}

function TopBar({ onMenu, onSearch }: { onMenu: () => void; onSearch: () => void }) {
  const signedIn = useAuth((s) => s.status === "authenticated");
  return (
    <header className="sticky top-0 z-30 border-b border-separator-border bg-background-full/85 pt-[env(safe-area-inset-top)] backdrop-blur-md">
      <div className={cx(DOCS_FRAME, "flex h-14 items-center gap-2 sm:gap-3")}>
        <IconAction label="Open the docs menu" icon={RiMenuLine} onClick={onMenu} className="-ms-1.5 lg:hidden" />
        {/* On wide screens this slot is as wide as the sidebar plus its gap, so search starts over the page column. */}
        <div className="flex min-w-0 shrink-0 items-center lg:w-[16.75rem]">
          {/* The brand goes home (the dashboard when signed in); the Docs label goes to the docs index. */}
          <Link
            to={signedIn ? "/" : "/docs"}
            aria-label={signedIn ? "Acnologia home" : "Acnologia docs home"}
            className={cx("-mx-1.5 flex items-center rounded-xl px-1.5 py-1", FOCUS)}
          >
            <Brand size={26} />
          </Link>
          <span aria-hidden className="ms-2.5 hidden h-5 w-px bg-separator-border sm:block" />
          <Link
            to="/docs"
            className={cx(
              "ms-1 hidden rounded-lg px-1.5 py-1 text-body-medium text-text-secondary transition-colors duration-150 hover:text-text-primary sm:block",
              FOCUS,
            )}
          >
            Docs
          </Link>
        </div>

        {/* Wide screens: a search field that opens the dialog. It sits over the content column. */}
        <button
          type="button"
          onClick={onSearch}
          aria-haspopup="dialog"
          className={cx(
            "ms-3 hidden h-9 w-full max-w-sm cursor-pointer items-center gap-2 rounded-full border border-border-button-default bg-background-primary-default ps-3 pe-1.5 shadow-xs md:flex lg:ms-0",
            "text-body-regular text-text-tertiary transition-colors duration-150 hover:border-border-button-hover hover:text-text-secondary",
            FOCUS,
          )}
        >
          <RiSearchLine className="size-4 shrink-0" aria-hidden />
          <span className="flex-1 text-start">Search docs</span>
          <Kbd className="px-1.5">{SEARCH_SHORTCUT}</Kbd>
        </button>

        <div className="ms-auto flex shrink-0 items-center gap-1 sm:gap-1.5">
          <IconAction label="Search docs" icon={RiSearchLine} onClick={onSearch} className="md:hidden" />
          <a
            href="/llms.txt"
            className={cx(
              "hidden h-8 items-center gap-1.5 rounded-lg px-2 text-body-2-medium text-text-secondary transition-colors duration-150 hover:bg-button-ghost-hover hover:text-text-primary xl:inline-flex",
              FOCUS,
            )}
          >
            <RiRobot2Line className="size-4" aria-hidden />
            llms.txt
          </a>
          <ThemeButton />
          <span aria-hidden className="mx-1 hidden h-5 w-px bg-separator-border sm:block" />
          {signedIn ? (
            <RouterButton to="/" size="small" variant="secondary" trailingIcon={RiArrowRightLine} className="shrink-0">
              <span className="sm:hidden">Dashboard</span>
              <span className="hidden sm:inline">Open dashboard</span>
            </RouterButton>
          ) : (
            <RouterButton to="/login" size="small" className="shrink-0">
              Sign in
            </RouterButton>
          )}
        </div>
      </div>
    </header>
  );
}

/** The docs list, grouped by section, with the current page marked. */
export function DocsNav({ current, onPick }: { current?: string; onPick?: () => void }) {
  return (
    <nav aria-label="Docs" className="flex flex-col gap-7">
      {DOC_GROUPS.map((g) => (
        <section key={g.id} aria-labelledby={`docs-nav-${g.id}`}>
          <h2 id={`docs-nav-${g.id}`} className="px-2.5 text-caption-1-semibold text-text-tertiary">
            {g.title}
          </h2>
          <ul className="mt-2 flex flex-col gap-0.5">
            {docsInGroup(g.id).map((d) => {
              const on = d.slug === current;
              return (
                <li key={d.slug}>
                  <Link
                    to={`/docs/${d.slug}`}
                    onClick={onPick}
                    aria-current={on ? "page" : undefined}
                    className={cx(
                      "flex min-h-9 items-center gap-2.5 rounded-2lg px-2.5 py-1.5 text-body-regular transition-colors duration-150 pointer-coarse:min-h-11",
                      FOCUS,
                      on
                        ? "bg-background-primary-default text-body-medium text-text-primary shadow-xs ring-1 ring-border-button-default"
                        : "text-text-secondary hover:bg-background-secondary-hover hover:text-text-primary",
                    )}
                  >
                    <d.icon className={cx("size-4 shrink-0", on ? "text-status-purple-text" : "text-foreground-icon-tertiary")} aria-hidden />
                    {d.title}
                  </Link>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
      <section aria-labelledby="docs-nav-agents">
        <h2 id="docs-nav-agents" className="px-2.5 text-caption-1-semibold text-text-tertiary">
          For agents
        </h2>
        <ul className="mt-2 flex flex-col gap-0.5">
          {["/llms.txt", "/llms-full.txt"].map((href) => (
            <li key={href}>
              <a
                href={href}
                className={cx(
                  "flex min-h-9 items-center gap-2.5 rounded-2lg px-2.5 py-1.5 font-mono text-body-2-regular text-text-secondary transition-colors duration-150 hover:bg-background-secondary-hover hover:text-text-primary pointer-coarse:min-h-11",
                  FOCUS,
                )}
              >
                <RiRobot2Line className="size-4 shrink-0 text-foreground-icon-tertiary" aria-hidden />
                {href.slice(1)}
              </a>
            </li>
          ))}
        </ul>
      </section>
    </nav>
  );
}

/** A sticky side column under the top bar that scrolls on its own, with BoardUI's soft top edge once scrolled. */
export function SideScroller({ children, className, label }: { children: ReactNode; className?: string; label?: string }) {
  const fade = useScrollFade();
  return (
    <div className="sticky top-[calc(3.5rem+env(safe-area-inset-top))] h-[calc(100dvh-3.5rem-env(safe-area-inset-top))]" aria-label={label}>
      <div className="relative h-full">
        <div onScroll={fade.onScroll} className={cx("no-scrollbar h-full overflow-y-auto overscroll-contain py-8", className)}>
          {children}
        </div>
        <ScrollFade progress={fade.progress} surfaceClassName="from-background-full" />
      </div>
    </div>
  );
}

/**
 * The docs frame: one sticky top bar (brand, search, theme, the way into the
 * app), the grouped docs list on the left, the page in the middle and an
 * optional rail on the right. Phones get the list in a drawer.
 */
export function DocsLayout({ current, rail, children }: { current?: string; rail?: ReactNode; children: ReactNode }) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const openSearch = useCallback(() => {
    setMenuOpen(false);
    setSearchOpen(true);
  }, []);
  useSearchShortcut(openSearch);

  return (
    <SearchContext.Provider value={openSearch}>
      <div className="min-h-dvh bg-background-full text-text-primary">
        <a
          href="#doc-content"
          className="sr-only z-50 rounded-lg bg-button-primary px-3 py-2 text-body-2-medium text-text-white focus:not-sr-only focus:fixed focus:start-3 focus:top-3"
        >
          Skip to content
        </a>
        <TopBar onMenu={() => setMenuOpen(true)} onSearch={openSearch} />

        <Drawer placement="start" size="sm" isOpen={menuOpen} onOpenChange={setMenuOpen}>
          <DrawerHeader title="Docs" />
          <DrawerBody>
            <div className="pb-4">
              <DocsNav current={current} onPick={() => setMenuOpen(false)} />
            </div>
          </DrawerBody>
        </Drawer>
        <DocsSearch open={searchOpen} onOpenChange={setSearchOpen} />

        <div
          className={cx(
            DOCS_FRAME,
            "lg:grid lg:grid-cols-[14.5rem_minmax(0,1fr)] lg:gap-12",
            rail && "xl:grid-cols-[14.5rem_minmax(0,1fr)_13.5rem]",
          )}
        >
          <aside className="hidden lg:block">
            <SideScroller className="-ms-2.5 pe-2">
              <DocsNav current={current} />
            </SideScroller>
          </aside>
          {children}
          {rail ? <aside className="hidden xl:block">{rail}</aside> : null}
        </div>
      </div>
    </SearchContext.Provider>
  );
}
