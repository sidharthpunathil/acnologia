import { useEffect, type ReactNode } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { useAuth } from "./stores/auth";
import { can } from "./lib/perms";
import { RiRefreshLine } from "@remixicon/react";
import { Layout } from "./components/Layout";
import { Toaster } from "./components/Toaster";
import { Mark } from "@/components/acn/brand";
import { Button } from "@/components/base/buttons/button";
import { Spinner } from "@/components/base/spinner/spinner";
import { SetupPage } from "./pages/Setup";
import { LoginPage } from "./pages/Login";
import { NotFoundPage } from "./pages/NotFound";
import { lazyPage, preloadRoute, type PageRoute } from "./lazyPage";

// Each signed in page is its own chunk (lazyPage.tsx). Setup, sign in and
// not found stay in the main bundle.
const CatalogPage = lazyPage(() => import("./pages/Catalog").then((m) => m.CatalogPage));
const KeysPage = lazyPage(() => import("./pages/Keys").then((m) => m.KeysPage));
const ServicePage = lazyPage(() => import("./pages/service/ServicePage").then((m) => m.ServicePage));
const AppsPage = lazyPage(() => import("./pages/Apps").then((m) => m.AppsPage));
const ActivityPage = lazyPage(() => import("./pages/Activity").then((m) => m.ActivityPage));
const UsersPage = lazyPage(() => import("./pages/Users").then((m) => m.UsersPage));
const AuditPage = lazyPage(() => import("./pages/Audit").then((m) => m.AuditPage));
const AccountPage = lazyPage(() => import("./pages/Account").then((m) => m.AccountPage));
const ProvidersPage = lazyPage(() => import("./pages/providers/ProvidersPage").then((m) => m.ProvidersPage));
const ProviderEditorPage = lazyPage(() => import("./pages/providers/ProviderEditor").then((m) => m.ProviderEditorPage));
const AiBuilderPage = lazyPage(() => import("./pages/providers/AiBuilderPage").then((m) => m.AiBuilderPage));
const AiRunPage = lazyPage(() => import("./pages/providers/AiRunPage").then((m) => m.AiRunPage));
const SettingsPage = lazyPage(() => import("./pages/Settings").then((m) => m.SettingsPage));
const DocsPage = lazyPage(() => import("./pages/Docs").then((m) => m.DocsPage));
const DocPage = lazyPage(() => import("./pages/docs/DocPage").then((m) => m.DocPage));
const WebhooksPage = lazyPage(() => import("./pages/Webhooks").then((m) => m.WebhooksPage));
const EgressPage = lazyPage(() => import("./pages/Egress").then((m) => m.EgressPage));
const GroupsPage = lazyPage(() => import("./pages/Groups").then((m) => m.GroupsPage));
const SharingPage = lazyPage(() => import("./pages/Sharing").then((m) => m.SharingPage));
const UsagePage = lazyPage(() => import("./pages/Usage").then((m) => m.UsagePage));
const AssistantPage = lazyPage(() => import("./pages/Assistant").then((m) => m.AssistantPage));
const CliAuthorizePage = lazyPage(() => import("./pages/CliAuthorize").then((m) => m.CliAuthorizePage));
const IconGalleryPage = lazyPage(() => import("./pages/dev/IconGallery").then((m) => m.IconGalleryPage));
const IllustrationGallery = lazyPage(() => import("./components/foundations/illustrations/gallery").then((m) => m.IllustrationGallery));

// The page each address renders, for preloadAppPage. Keep it in step with the
// routes below; a path missing here still works, it just loads after the
// first render instead of before it.
const PAGES: readonly PageRoute[] = [
  ["/cli/authorize", CliAuthorizePage],
  ["/docs", DocsPage],
  ["/docs/:slug", DocPage],
  ["/", CatalogPage],
  ["/assistant", AssistantPage],
  ["/assistant/:threadId", AssistantPage],
  ["/keys", KeysPage],
  ["/services/:id", ServicePage],
  ["/providers", ProvidersPage],
  ["/providers/ai", AiBuilderPage],
  ["/providers/ai/:runId", AiRunPage],
  ["/providers/new", ProviderEditorPage],
  ["/providers/:slug", ProviderEditorPage],
  ["/settings", SettingsPage],
  ["/apps", AppsPage],
  ["/groups", GroupsPage],
  ["/sharing", SharingPage],
  ["/usage", UsagePage],
  ["/webhooks", WebhooksPage],
  ["/egress", EgressPage],
  ["/activity", ActivityPage],
  ["/users", UsersPage],
  ["/audit", AuditPage],
  ["/account", AccountPage],
  ["/dev/icons", IconGalleryPage],
  ["/dev/illustrations", IllustrationGallery],
];

/** Fetches the chunk of the page at `pathname` so the first render does not wait on it. */
export function preloadAppPage(pathname: string): Promise<void> {
  return preloadRoute(PAGES, pathname);
}

function RequireRole({ ok, children }: { ok: boolean; children: ReactNode }) {
  return ok ? <>{children}</> : <Navigate to="/" replace />;
}

export function App() {
  const { status, user, error, load } = useAuth();
  const location = useLocation();

  useEffect(() => {
    void load();
  }, [load]);

  let body: ReactNode;
  if (status === "loading") {
    body = (
      <div className="delayed-in flex min-h-dvh items-center justify-center bg-background-full" role="status" aria-live="polite">
        <Spinner size="lg" aria-label="Loading Acnologia" />
      </div>
    );
  } else if (status === "error") {
    body = (
      <div role="alert" className="flex min-h-dvh flex-col items-center justify-center gap-4 bg-background-full px-6 text-center">
        <Mark size={48} />
        <h1 className="font-mono text-title-2-medium text-text-primary">Cannot reach Acnologia</h1>
        <p className="max-w-sm text-body-regular text-text-secondary">{error} Make sure the server is running, then try again.</p>
        <Button variant="primary" leadingIcon={RiRefreshLine} onClick={() => void load()}>
          Try again
        </Button>
      </div>
    );
  } else if (status === "setup") {
    body = <SetupPage />;
  } else if (status === "anonymous") {
    body = (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/docs" element={<DocsPage />} />
        <Route path="/docs/:slug" element={<DocPage />} />
        {/* Signed out, home is the public landing page; it signs in on demand. */}
        <Route index element={<Navigate to="/landing" replace />} />
        <Route
          path="*"
          element={<Navigate to="/login" replace state={{ from: location.pathname + location.search }} />}
        />
      </Routes>
    );
  } else {
    const role = user?.role;
    body = (
      <Routes>
        <Route path="/login" element={<Navigate to="/" replace />} />
        <Route path="/cli/authorize" element={<CliAuthorizePage />} />
        <Route path="/docs" element={<DocsPage />} />
        <Route path="/docs/:slug" element={<DocPage />} />
        <Route element={<Layout />}>
          <Route index element={<CatalogPage />} />
          <Route path="assistant" element={<AssistantPage />} />
          <Route path="assistant/:threadId" element={<AssistantPage />} />
          <Route path="keys" element={<KeysPage />} />
          <Route path="services/:id" element={<ServicePage />} />
          <Route path="providers" element={<ProvidersPage />} />
          <Route path="providers/ai" element={<AiBuilderPage />} />
          <Route path="providers/ai/:runId" element={<AiRunPage />} />
          <Route
            path="providers/new"
            element={
              <RequireRole ok={can.manage(role)}>
                <ProviderEditorPage />
              </RequireRole>
            }
          />
          <Route path="providers/:slug" element={<ProviderEditorPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="apps" element={<AppsPage />} />
          <Route path="groups" element={<GroupsPage />} />
          <Route
            path="sharing"
            element={
              <RequireRole ok={can.manage(role)}>
                <SharingPage />
              </RequireRole>
            }
          />
          <Route path="usage" element={<UsagePage />} />
          <Route path="webhooks" element={<WebhooksPage />} />
          <Route path="egress" element={<EgressPage />} />
          <Route path="activity" element={<ActivityPage />} />
          <Route
            path="users"
            element={
              <RequireRole ok={can.users(role)}>
                <UsersPage />
              </RequireRole>
            }
          />
          <Route
            path="audit"
            element={
              <RequireRole ok={can.audit(role)}>
                <AuditPage />
              </RequireRole>
            }
          />
          <Route path="account" element={<AccountPage />} />
          <Route path="dev/icons" element={<IconGalleryPage />} />
          <Route path="dev/illustrations" element={<IllustrationGallery />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    );
  }

  return (
    <>
      {body}
      <Toaster />
    </>
  );
}
