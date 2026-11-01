import { useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { RiLogoutBoxRLine } from "@remixicon/react";
import { useTitle } from "@/hooks/useTitle";
import { useAuth } from "@/stores/auth";
import { toast } from "@/stores/toast";
import { errorMessage } from "@/lib/api";
import { MIN_PASSWORD } from "@/lib/perms";
import { dateTime } from "@/lib/format";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { Input } from "@/components/base/input/input";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { PageHeader } from "@/components/acn/page-header";
import { Section } from "@/components/acn/section";
import { ApiTokens } from "@/components/ApiTokens";

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
    <div className="max-w-3xl">
      <PageHeader
        title="Account"
        meta={
          <span className="inline-flex flex-wrap items-center gap-2 wrap-anywhere">
            {user.email}
            <Chip variant="caption" color="gray" className="font-mono">
              {user.role}
            </Chip>
            <span className="whitespace-nowrap">joined {dateTime(user.created_at)}</span>
          </span>
        }
        actions={
          <Button
            variant="secondary"
            leadingIcon={RiLogoutBoxRLine}
            onClick={async () => {
              await logout();
              navigate("/login", { replace: true });
            }}
          >
            Sign out
          </Button>
        }
      />

      <div className="flex flex-col gap-5">
        <Section title="Profile" description="The name people see in the audit log and on keys you add.">
          <form onSubmit={saveName} className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Input label="Name" value={name} onChange={setName} isRequired autoComplete="name" />
            <Input
              label="Current password"
              hint="Confirms it is you."
              type="password"
              value={nameCurrent}
              onChange={setNameCurrent}
              isRequired
              autoComplete="current-password"
            />
            <div className="flex justify-end sm:col-span-2">
              <Button
                type="submit"
                variant="primary"
                leadingIcon={busyName ? ButtonSpinner : undefined}
                disabled={busyName || name.trim() === user.name || !name.trim()}
              >
                Save name
              </Button>
            </div>
          </form>
        </Section>

        <Section title="Password">
          <form onSubmit={savePassword} className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Input
              label="Current password"
              type="password"
              value={current}
              onChange={setCurrent}
              isRequired
              autoComplete="current-password"
              className="sm:col-span-2"
            />
            <Input
              label="New password"
              hint={`At least ${MIN_PASSWORD} characters.`}
              type="password"
              value={next}
              onChange={setNext}
              isRequired
              autoComplete="new-password"
            />
            <Input label="Confirm new password" type="password" value={confirm} onChange={setConfirm} isRequired autoComplete="new-password" />
            <div className="flex justify-end sm:col-span-2">
              <Button type="submit" variant="primary" disabled={busyPw} leadingIcon={busyPw ? ButtonSpinner : undefined}>
                Change password
              </Button>
            </div>
          </form>
        </Section>

        <ApiTokens />
      </div>
    </div>
  );
}
