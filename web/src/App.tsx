import { useEffect, type ReactNode } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { useAuth } from "./stores/auth";
import { can } from "./lib/perms";
import { Layout, Mark } from "./components/Layout";
import { Toaster } from "./components/Toaster";
import { Button, PageLoading } from "./components/ui";
import { SetupPage } from "./pages/Setup";
import { LoginPage } from "./pages/Login";
import { CatalogPage } from "./pages/Catalog";
import { KeysPage } from "./pages/Keys";
import { ServicePage } from "./pages/service/ServicePage";
import { AppsPage } from "./pages/Apps";
import { ActivityPage } from "./pages/Activity";
import { UsersPage } from "./pages/Users";
import { AuditPage } from "./pages/Audit";
import { AccountPage } from "./pages/Account";
import { NotFoundPage } from "./pages/NotFound";
import { ProvidersPage } from "./pages/providers/ProvidersPage";
import { ProviderEditorPage } from "./pages/providers/ProviderEditor";
import { AiBuilderPage } from "./pages/providers/AiBuilderPage";
import { AiRunPage } from "./pages/providers/AiRunPage";
import { SettingsPage } from "./pages/Settings";
import { DocsPage } from "./pages/Docs";
import { DocPage } from "./pages/docs/DocPage";
import { WebhooksPage } from "./pages/Webhooks";
import { EgressPage } from "./pages/Egress";
import { GroupsPage } from "./pages/Groups";
import { UsagePage } from "./pages/Usage";
import { AssistantPage } from "./pages/Assistant";

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
      <div className="delayed-in flex min-h-dvh items-center justify-center bg-canvas">
        <PageLoading />
      </div>
    );
  } else if (status === "error") {
    body = (
      <div role="alert" className="flex min-h-dvh flex-col items-center justify-center gap-4 bg-canvas px-6 text-center">
        <Mark size={48} className="rounded-[22%]" />
        <h1 className="font-mono text-lg text-ink">Cannot reach Acnologia</h1>
        <p className="max-w-sm text-sm text-muted">{error} Make sure the server is running, then try again.</p>
        <Button variant="primary" onClick={() => void load()}>
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
