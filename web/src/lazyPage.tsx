// Route level code splitting. Each page lives in its own chunk, and main.tsx
// fetches the chunk for the address being opened before the first render,
// so a cold load never shows a Suspense fallback. React 19 holds a revealed
// fallback for at least 300ms, which made an unpreloaded split slower than
// one big bundle on a fast machine.
//
// A page whose chunk is already here renders directly, without React.lazy,
// so it never suspends. Navigating to one that is not here yet suspends
// inside a transition (BrowserRouter's v7_startTransition), and React keeps
// the current page on screen until the chunk arrives.
import { Component, lazy, type ComponentType, type ErrorInfo, type ReactNode } from "react";
import { matchPath } from "react-router-dom";
import { RiRefreshLine } from "@remixicon/react";
import { Button } from "@/components/base/buttons/button";

export interface LazyPage<P extends object> {
  (props: P): ReactNode;
  /** Fetches the chunk. Resolves once the page can render without suspending. */
  preload: () => Promise<void>;
}

/**
 * A page loaded on demand. `load` imports the module and picks the named
 * export, for example `() => import("./pages/Keys").then((m) => m.KeysPage)`.
 */
export function lazyPage<P extends object>(load: () => Promise<ComponentType<P>>): LazyPage<P> {
  let loaded: ComponentType<P> | undefined;
  let pending: Promise<ComponentType<P>> | undefined;
  const fetchPage = () =>
    (pending ??= load().then(
      (c) => (loaded = c),
      (e: unknown) => {
        // Let a later attempt try the network again.
        pending = undefined;
        throw e;
      },
    ));
  const Lazy = lazy(() => fetchPage().then((c) => ({ default: c })));
  const Page = (props: P) => {
    const Loaded = loaded;
    return Loaded ? <Loaded {...props} /> : <Lazy {...props} />;
  };
  Page.preload = () => fetchPage().then(() => undefined);
  return Page;
}

/** A path pattern (react-router syntax) and the page that renders there. */
export type PageRoute = readonly [pattern: string, page: { preload: () => Promise<void> }];

/**
 * Fetches the page that `pathname` renders, if a route in `routes` matches.
 * Never rejects: a failed fetch is retried when the page renders, and the
 * chunk error boundary deals with it from there.
 */
export function preloadRoute(routes: readonly PageRoute[], pathname: string): Promise<void> {
  const hit = routes.find(([pattern]) => matchPath(pattern, pathname));
  return hit ? hit[1].preload().catch(() => undefined) : Promise.resolve();
}

// After a deploy, an open tab still runs the old build, whose chunk names no
// longer exist on the server. Loading one fails, and a reload picks up the
// new build. The marker stops a reload loop when the failure is something
// else, such as the network being down: one automatic reload per minute.
const RELOAD_MARK = "acn.chunk-reload";
const RELOAD_WINDOW_MS = 60_000;

/** Reloads the page once for a chunk that failed to load. False when it already did so recently. */
export function reloadForNewBuild(): boolean {
  try {
    const last = Number(sessionStorage.getItem(RELOAD_MARK) ?? 0);
    if (Date.now() - last < RELOAD_WINDOW_MS) return false;
    sessionStorage.setItem(RELOAD_MARK, String(Date.now()));
  } catch {
    // No session storage (a private window, blocked site data): without the
    // marker there is no loop guard, so do not reload automatically.
    return false;
  }
  window.location.reload();
  return true;
}

/** True for the errors browsers raise when a dynamic import or its CSS fails to load. */
export function isChunkLoadError(e: unknown): boolean {
  if (!(e instanceof Error)) return false;
  return (
    /dynamically imported module|Importing a module script failed|error loading dynamically imported module|Unable to preload CSS/i.test(e.message) ||
    e.name === "ChunkLoadError"
  );
}

interface BoundaryState {
  error: unknown;
}

/**
 * Catches a page that failed to render. A chunk that failed to load reloads
 * the page once (a new build is out); anything else, or a second failure,
 * shows a short message with a reload button instead of a blank screen.
 */
export class ChunkErrorBoundary extends Component<{ children: ReactNode }, BoundaryState> {
  state: BoundaryState = { error: null };

  static getDerivedStateFromError(error: unknown): BoundaryState {
    return { error };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    if (isChunkLoadError(error) && reloadForNewBuild()) return;
    console.error(error, info.componentStack);
  }

  render() {
    if (this.state.error === null) return this.props.children;
    const chunk = isChunkLoadError(this.state.error);
    return (
      <div role="alert" className="flex min-h-dvh flex-col items-center justify-center gap-4 bg-background-full px-6 text-center">
        <h1 className="font-mono text-title-2-medium text-text-primary">{chunk ? "Acnologia was updated" : "Something went wrong"}</h1>
        <p className="max-w-sm text-body-regular text-text-secondary">
          {chunk ? "This page could not load its code. Reload to get the new version." : "This page hit an error. Reload to try again."}
        </p>
        <Button variant="primary" leadingIcon={RiRefreshLine} onClick={() => window.location.reload()}>
          Reload
        </Button>
      </div>
    );
  }
}
