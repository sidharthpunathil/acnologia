import { useState } from "react";
import { useAuth } from "@/stores/auth";
import { errorMessage } from "@/lib/api";
import { toast } from "@/stores/toast";
import { MIN_PASSWORD } from "@/lib/perms";
import { AuthCard } from "@/components/application/auth/auth-card";
import { Callout } from "@/components/acn/callout";
import { AuthShell } from "./AuthShell";

export function SetupPage() {
  const setup = useAuth((s) => s.setup);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const submit = async (data: FormData) => {
    setErr(null);
    const name = String(data.get("name") ?? "").trim();
    const email = String(data.get("email") ?? "").trim();
    const password = String(data.get("password") ?? "");
    const confirm = String(data.get("confirmPassword") ?? "");
    if (password.length < MIN_PASSWORD) return setErr(`Use at least ${MIN_PASSWORD} characters for the password.`);
    if (password !== confirm) return setErr("Passwords do not match.");
    setBusy(true);
    try {
      await setup(email, name, password);
      toast.success("You are in. Welcome to Acnologia.");
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell meta="Self-hosted API key server · first run">
      <AuthCard
        mode="signup"
        confirmPassword
        askName
        title="Set up Acnologia"
        description="Create the first account. It runs this instance."
        passwordHint={`At least ${MIN_PASSWORD} characters.`}
        passwordPlaceholder={`At least ${MIN_PASSWORD} characters`}
        alert={err ? <Callout tone="error">{err}</Callout> : null}
        isPending={busy}
        cta="Create my account"
        onSubmit={(d) => void submit(d)}
        formNote="This first account is the superadmin: it can see hidden keys, reveal secrets and add people. You can add admins and members later."
      />
    </AuthShell>
  );
}
