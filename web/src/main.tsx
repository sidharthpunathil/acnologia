import { StrictMode, Suspense, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, useHref, useLocation, useNavigate, type NavigateOptions } from "react-router-dom";
import { RouterProvider } from "react-aria-components";
import { App, preloadAppPage } from "./App";
import { AccessGate } from "./pages/Lock";
import { PublicRoutes, isPublicPath, preloadPublicPage } from "./pages/claim/PublicRoutes";
import { ChunkErrorBoundary, reloadForNewBuild } from "./lazyPage";
import { Spinner } from "@/components/base/spinner/spinner";
import "./index.css";
// Applies the saved theme and follows the OS setting while it is "system".
import "./stores/theme";

declare module "react-aria-components" {
  interface RouterConfig {
    routerOptions: NavigateOptions;
  }
}

/** Lets `href` on React Aria links, tabs, menu items and buttons route client side. */
function AriaRouter({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  return (
    <RouterProvider navigate={(to, options) => navigate(to, options)} useHref={useHref}>
      {children}
    </RouterProvider>
  );
}

/**
 * The claim link and "my access" page are for people without an account on
 * this server, so they render before the access gate and the sign in check.
 * They only call the public claim routes and /api/v1/me with a token.
 */
function Root() {
  const { pathname } = useLocation();
  if (isPublicPath(pathname)) return <PublicRoutes />;
  return (
    <AccessGate>
      <App />
    </AccessGate>
  );
}

/**
 * Shown only while a page's code loads outside a navigation, such as right
 * after signing in. A navigation keeps the current page on screen instead.
 */
function PageLoading() {
  return (
    <div className="delayed-in flex min-h-dvh items-center justify-center bg-background-full" role="status" aria-live="polite">
      <Spinner size="lg" aria-label="Loading Acnologia" />
    </div>
  );
}

// Vite fires this when a chunk's script or stylesheet dependency fails to
// load, which after a deploy means a new build is out.
window.addEventListener("vite:preloadError", (e) => {
  if (reloadForNewBuild()) e.preventDefault();
});

// The page being opened is fetched before the first render, so the first
// paint never waits on a Suspense fallback.
const { pathname } = window.location;
const preload = isPublicPath(pathname) ? preloadPublicPage(pathname) : preloadAppPage(pathname);

void preload.then(() =>
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <AriaRouter>
          <ChunkErrorBoundary>
            <Suspense fallback={<PageLoading />}>
              <Root />
            </Suspense>
          </ChunkErrorBoundary>
        </AriaRouter>
      </BrowserRouter>
    </StrictMode>,
  ),
);
