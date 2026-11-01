import { useState, type FormEvent } from "react";
import { useAuth } from "../stores/auth";
import { errorMessage } from "../lib/api";
import { toast } from "../stores/toast";
import { Button, Field, Input } from "../components/ui";
import { AuthShell } from "./AuthShell";
import { MIN_PASSWORD } from "../lib/perms";

export function SetupPage() {
  const setup = useAuth((s) => s.setup);
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    if (password.length < MIN_PASSWORD) return setErr(`Use at least ${MIN_PASSWORD} characters for the password.`);
    if (password !== confirm) return setErr("Passwords do not match.");
    setBusy(true);
    try {
      await setup(email.trim(), name.trim(), password);
      toast.success("You are in. Welcome to Acnologia.");
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell title="Set up Acnologia" meta="Self-hosted API key pool · first run">
      <form onSubmit={submit} className="space-y-4">
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} required autoComplete="name" />
        </Field>
        <Field label="Email">
          <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required autoComplete="email" />
        </Field>
        <Field label="Password" hint={`At least ${MIN_PASSWORD} characters.`}>
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required autoComplete="new-password" />
        </Field>
        <Field label="Confirm password">
          <Input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required autoComplete="new-password" />
        </Field>
        {err && (<p role="alert" className="rounded-xl bg-red-50 px-3 py-2 text-sm text-red-700">{err}</p>)}
        <Button type="submit" variant="primary" loading={busy} className="w-full">
          Create my account
        </Button>
        <p className="text-xs text-muted">
          This first account is the superadmin: it can see hidden keys, reveal secrets and add people. You can add admins and members later.
        </p>
      </form>
    </AuthShell>
  );
}
