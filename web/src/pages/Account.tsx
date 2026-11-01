import { useEffect, useState, type FormEvent } from "react";
import { useTitle } from "../hooks/useTitle";
import { LogOut } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { MIN_PASSWORD } from "../lib/perms";
import { dateTime } from "../lib/format";
import { Badge, Button, Field, Input, PageHeader, Section } from "../components/ui";
import { ApiTokens } from "../components/ApiTokens";

export function AccountPage() {
  useTitle("Account");
  const { user, updateMe, logout } = useAuth();
  const navigate = useNavigate();
  const [name, setName] = useState(user?.name ?? "");
  const [nameCurrent, setNameCurrent] = useState("");
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busyName, setBusyName] = useState(false);
  const [busyPw, setBusyPw] = useState(false);

  useEffect(() => setName(user?.name ?? ""), [user?.name]);

  if (!user) return null;

  const saveName = async (e: FormEvent) => {
    e.preventDefault();
    setBusyName(true);
    try {
      await updateMe({ name: name.trim(), current_password: nameCurrent });
      setNameCurrent("");
      toast.success("Name saved");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusyName(false);
    }
  };

  const savePassword = async (e: FormEvent) => {
    e.preventDefault();
    if (next.length < MIN_PASSWORD) return toast.error(`Use at least ${MIN_PASSWORD} characters for the new password.`);
    if (next !== confirm) return toast.error("The new passwords do not match.");
    setBusyPw(true);
    try {
      await updateMe({ current_password: current, password: next });
      setCurrent("");
      setNext("");
      setConfirm("");
      toast.success("Password changed");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusyPw(false);
    }
  };

  return (
    <div className="max-w-2xl">
      <PageHeader
        title="Account"
        meta={
          <span className="wrap-anywhere inline-flex flex-wrap items-center gap-2">
            {user.email} <Badge tone="dark">{user.role}</Badge>
            <span className="whitespace-nowrap">joined {dateTime(user.created_at)}</span>
          </span>
        }
        actions={
          <Button
            icon={<LogOut className="h-4 w-4" />}
            onClick={async () => {
              await logout();
              navigate("/login", { replace: true });
            }}
          >
            Sign out
          </Button>
        }
      />

      <div className="space-y-5">
        <Section title="Profile">
          <form onSubmit={saveName} className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Name">
              <Input value={name} onChange={(e) => setName(e.target.value)} required autoComplete="name" />
            </Field>
            <Field label="Current password" hint="Confirms it is you.">
              <Input type="password" value={nameCurrent} onChange={(e) => setNameCurrent(e.target.value)} required autoComplete="current-password" />
            </Field>
            <div className="flex justify-end sm:col-span-2">
              <Button type="submit" variant="primary" loading={busyName} disabled={name.trim() === user.name || !name.trim()}>
                Save name
              </Button>
            </div>
          </form>
        </Section>

        <Section title="Password">
          <form onSubmit={savePassword} className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Current password" className="sm:col-span-2">
              <Input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required autoComplete="current-password" />
            </Field>
            <Field label="New password" hint={`At least ${MIN_PASSWORD} characters.`}>
              <Input type="password" value={next} onChange={(e) => setNext(e.target.value)} required autoComplete="new-password" />
            </Field>
            <Field label="Confirm new password">
              <Input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required autoComplete="new-password" />
            </Field>
            <div className="flex justify-end sm:col-span-2">
              <Button type="submit" variant="primary" loading={busyPw}>
                Change password
              </Button>
            </div>
          </form>
        </Section>
      </div>
      <div className="mt-5">
        <ApiTokens />
      </div>
    </div>
  );
}
