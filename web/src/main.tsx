import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, useHref, useNavigate, type NavigateOptions } from "react-router-dom";
import { RouterProvider } from "react-aria-components";
import { App } from "./App";
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

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AriaRouter>
        <App />
      </AriaRouter>
    </BrowserRouter>
  </StrictMode>,
);
