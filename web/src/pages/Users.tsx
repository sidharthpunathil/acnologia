import { useEffect, useState, type FormEvent } from "react";
import { useTitle } from "../hooks/useTitle";
import { Plus, Users as UsersIcon } from "lucide-react";
import { useUsers } from "../stores/users";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { MIN_PASSWORD } from "../lib/perms";
import { dateTime, plural, relTime } from "../lib/format";
import type { Role, User } from "../lib/types";
import { useNow } from "../hooks/useNow";
import { Modal } from "../components/Modal";
import { ConfirmButton } from "../components/ConfirmButton";
import { Badge, Button, Empty, Field, Input, PageHeader, PageSkeleton, Select } from "../components/ui";

const ROLE_HELP: Record<Role, string> = {
  member: "Read only: catalog, keys metadata and activity.",
  admin: "Manages services, keys and apps. Cannot see hidden keys, reveal secrets or manage users.",
  superadmin: "Everything, including hidden keys, secrets, users and the audit log.",
};

function CreateUserModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const create = useUsers((s) => s.create);
  const [form, setForm] = useState({ email: "", name: "", password: "", role: "member" as Role });
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) setForm({ email: "", name: "", password: "", role: "member" });
  }, [open]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (form.password.length < MIN_PASSWORD) {
      toast.error(`Password needs at least ${MIN_PASSWORD} characters.`);
      return;
    }
    setBusy(true);
    try {
      const u = await create({ ...form, email: form.email.trim(), name: form.name.trim() });
      toast.success(`Created ${u.email}. Share the password with them securely.`);
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Invite user"
      description="Create the account and share the initial password with them."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="user-form" loading={busy}>
            Create user
          </Button>
        </>
      }
    >
      <form id="user-form" onSubmit={submit} className="space-y-4">
        <Field label="Email">
          <Input type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} required autoComplete="off" />
        </Field>
        <Field label="Name">
          <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required autoComplete="off" />
        </Field>
        <Field label="Initial password" hint={`At least ${MIN_PASSWORD} characters. They can change it from Account.`}>
          <Input type="text" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required autoComplete="off" className="font-mono" />
        </Field>
        <Field label="Role" hint={ROLE_HELP[form.role]}>
          <Select value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value as Role })}>
            <option value="member">member</option>
            <option value="admin">admin</option>
            <option value="superadmin">superadmin</option>
          </Select>
        </Field>
      </form>
    </Modal>
  );
}

export function UsersPage() {
  useTitle("Users");
  const { users, loaded, fetch, update, remove } = useUsers();
  const me = useAuth((s) => s.user);
  const now = useNow(30000);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    fetch().catch((e) => toast.error(errorMessage(e)));
  }, [fetch]);

  const patch = async (u: User, p: Partial<{ role: Role; disabled: boolean }>, msg: string) => {
    try {
      await update(u.id, p);
      toast.success(msg);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  if (!loaded) return <PageSkeleton variant="list" />;

  const sorted = [...users].sort((a, b) => a.email.localeCompare(b.email));

  return (
    <div>
      <PageHeader
        title="Users"
        meta={`${plural(users.length, "user")} · ${users.filter((u) => u.role === "superadmin").length} superadmin`}
        actions={
          <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setOpen(true)}>
            Invite user
          </Button>
        }
      />

      {sorted.length <= 1 && (
        <div className="mb-4">
          <Empty
            icon={<UsersIcon className="h-5 w-5" />}
            title="Just you so far"
            body="Invite admins to manage keys and apps, or members who only need to see what is in the pool."
          />
        </div>
      )}

      <ul className="space-y-2.5">
        {sorted.map((u) => {
          const self = u.id === me?.id;
          return (
            <li key={u.id} className="flex flex-col gap-3 rounded-card bg-white p-4 shadow-card sm:flex-row sm:items-center sm:justify-between sm:px-5">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-[15px] font-medium text-ink">{u.name || u.email}</span>
                  {self && <Badge>you</Badge>}
                  {u.disabled && <Badge tone="red">disabled</Badge>}
                </div>
                <div className="mt-1 font-mono text-xs text-muted">
                  {u.email} · <span title={dateTime(u.last_login_at)}>last login {relTime(u.last_login_at, now)}</span>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Select
                  value={u.role}
                  disabled={self}
                  title={self ? "You cannot change your own role" : undefined}
                  onChange={(e) => void patch(u, { role: e.target.value as Role }, `${u.email} is now ${e.target.value}`)}
                  className="h-8 w-36 text-[13px]"
                >
                  <option value="member">member</option>
                  <option value="admin">admin</option>
                  <option value="superadmin">superadmin</option>
                </Select>
                {!self && (
                  <>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => void patch(u, { disabled: !u.disabled }, `${u.email} ${u.disabled ? "enabled" : "disabled"}`)}
                    >
                      {u.disabled ? "Enable" : "Disable"}
                    </Button>
                    <ConfirmButton
                      iconOnly
                      label="Delete user"
                      confirmLabel="Delete"
                      onConfirm={async () => {
                        try {
                          await remove(u.id);
                          toast.success(`Deleted ${u.email}`);
                        } catch (e) {
                          toast.error(errorMessage(e));
                        }
                      }}
                    />
                  </>
                )}
              </div>
            </li>
          );
        })}
      </ul>

      <CreateUserModal open={open} onClose={() => setOpen(false)} />
    </div>
  );
}
