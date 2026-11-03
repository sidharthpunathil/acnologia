import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, useHref, useLocation, useNavigate, type NavigateOptions } from "react-router-dom";
import { RouterProvider } from "react-aria-components";
import { App } from "./App";
import { AccessGate } from "./pages/Lock";
import { PublicRoutes, isPublicPath } from "./pages/claim/PublicRoutes";
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

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AriaRouter>
        <Root />
      </AriaRouter>
    </BrowserRouter>
  </StrictMode>,
);
