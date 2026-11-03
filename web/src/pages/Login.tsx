import { useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "@/stores/auth";
import { errorMessage } from "@/lib/api";
import { AuthCard } from "@/components/application/auth/auth-card";
import { Callout } from "@/components/acn/callout";
import { AuthShell } from "./AuthShell";

export function LoginPage() {
  const login = useAuth((s) => s.login);
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? "/";
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const submit = async (data: FormData) => {
    setErr(null);
    setBusy(true);
    try {
      await login(String(data.get("email") ?? "").trim(), String(data.get("password") ?? ""));
      navigate(from === "/login" ? "/" : from, { replace: true });
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell meta="Self-hosted API key server">
      <AuthCard
        mode="signin"
        title="Sign in"
        description="Use the account your superadmin made for you."
        alert={err ? <Callout tone="error">{err}</Callout> : null}
        isPending={busy}
        onSubmit={(d) => void submit(d)}
        formNote="No account yet? Ask your Acnologia superadmin to add you."
      />
    </AuthShell>
  );
}
