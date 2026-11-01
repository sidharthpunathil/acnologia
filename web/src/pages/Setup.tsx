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
      toast.success("Superadmin created. Welcome to Cogito.");
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell title="Set up Cogito" meta="first run · create the superadmin account">
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
        {err && <p className="text-sm text-red-600">{err}</p>}
        <Button type="submit" variant="primary" loading={busy} className="w-full">
          Create superadmin
        </Button>
        <p className="text-xs text-muted">
          This account can see hidden keys, reveal secrets and manage users. You can invite admins and members later.
        </p>
      </form>
    </AuthShell>
  );
}
