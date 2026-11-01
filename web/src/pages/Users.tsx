import { useEffect, useState, type FormEvent } from "react";
import { RiAddLine, RiTeamLine } from "@remixicon/react";
import { useTitle } from "@/hooks/useTitle";
import { useNow } from "@/hooks/useNow";
import { useUsers } from "@/stores/users";
import { useAuth } from "@/stores/auth";
import { toast } from "@/stores/toast";
import { errorMessage } from "@/lib/api";
import { MIN_PASSWORD } from "@/lib/perms";
import { dateTime, plural, relTime } from "@/lib/format";
import type { Role, User } from "@/lib/types";
import { Avatar } from "@/components/base/avatar/avatar";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { Input } from "@/components/base/input/input";
import { Select, SelectItem } from "@/components/base/select/select";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Table, TableBody, TableCell, TableColumn, TableHeader, TableRow } from "@/components/base/table/table";
import { ConfirmButton } from "@/components/acn/confirm-button";
import { EmptyState } from "@/components/acn/empty-state";
import { PageHeader } from "@/components/acn/page-header";
import { PageSkeleton } from "@/components/acn/page-skeleton";
import { Modal } from "@/components/Modal";

const ROLES: Role[] = ["member", "admin", "superadmin"];

const ROLE_HELP: Record<Role, string> = {
  member: "View only: the catalog, key details and activity.",
  admin: "Manages services, keys and apps. Cannot see hidden keys, reveal secrets or manage people.",
  superadmin: "Everything, including hidden keys, secrets, people and the audit log.",
};

function initials(u: User) {
  const src = (u.name || u.email).trim();
  const parts = src.split(/[\s@._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "")).toUpperCase() || "?";
}

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
      toast.success(`Account created for ${u.email}. Share the password with them privately.`);
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
      title="Add a person"
      description="Create their account, then share the first password with them privately."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="user-form" disabled={busy} leadingIcon={busy ? ButtonSpinner : undefined}>
            Create account
          </Button>
        </>
      }
    >
      <form id="user-form" onSubmit={submit} className="flex flex-col gap-4 pb-1">
        <Input label="Email" type="email" value={form.email} onChange={(v) => setForm({ ...form, email: v })} isRequired autoComplete="off" />
        <Input label="Name" value={form.name} onChange={(v) => setForm({ ...form, name: v })} isRequired autoComplete="off" />
        <Input
          label="First password"
          hint={`At least ${MIN_PASSWORD} characters. They can change it later from their account.`}
          value={form.password}
          onChange={(v) => setForm({ ...form, password: v })}
          isRequired
          autoComplete="off"
          inputClassName="font-mono"
        />
        <Select
          label="Role"
          hint={ROLE_HELP[form.role]}
          selectedKey={form.role}
          onSelectionChange={(k) => k != null && setForm({ ...form, role: k as Role })}
          triggerClassName="w-full"
        >
          {ROLES.map((r) => (
            <SelectItem key={r} id={r}>
              {r}
            </SelectItem>
          ))}
        </Select>
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

  const who = (u: User, self: boolean) => (
    <div className="flex min-w-0 items-center gap-3">
      <Avatar size="md" initials={initials(u)} aria-hidden className="text-caption-1-semibold" />
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-body-medium text-text-primary">{u.name || u.email}</span>
          {self && (
            <Chip variant="caption" color="purple">
              you
            </Chip>
          )}
          {u.disabled && (
            <Chip variant="caption" color="rose">
              disabled
            </Chip>
          )}
        </div>
        <div className="font-mono text-caption-1-regular wrap-anywhere text-text-secondary">{u.email}</div>
      </div>
    </div>
  );

  const roleSelect = (u: User, self: boolean) => (
    <Select
      aria-label={`Role for ${u.email}`}
      size="sm"
      selectedKey={u.role}
      isDisabled={self}
      triggerClassName="w-32"
      aria-description={self ? "You cannot change your own role" : undefined}
      onSelectionChange={(k) => {
        if (k == null || k === u.role) return;
        void patch(u, { role: k as Role }, `${u.email} is now ${k}`);
      }}
    >
      {ROLES.map((r) => (
        <SelectItem key={r} id={r}>
          {r}
        </SelectItem>
      ))}
    </Select>
  );

  // `touch` widens the targets to 44px for the phone cards.
  const actions = (u: User, self: boolean, touch = false) =>
    self ? null : (
      <div className="flex items-center justify-end gap-1">
        <Button size="small" variant="ghost" className={touch ? "h-11 px-3" : undefined} onClick={() => void patch(u, { disabled: !u.disabled }, `${u.email} ${u.disabled ? "enabled" : "disabled"}`)}>
          {u.disabled ? "Enable" : "Disable"}
        </Button>
        <ConfirmButton
          iconOnly
          className={touch ? "size-11" : undefined}
          label="Delete user"
          confirmLabel="Delete"
          description={`${u.email} loses access at once. Their audit entries stay.`}
          onConfirm={async () => {
            try {
              await remove(u.id);
              toast.success(`Deleted ${u.email}`);
            } catch (e) {
              toast.error(errorMessage(e));
            }
          }}
        />
      </div>
    );

  return (
    <div>
      <PageHeader
        title="Users"
        meta={`${plural(users.length, "user")} · ${users.filter((u) => u.role === "superadmin").length} superadmin`}
        actions={
          <Button variant="primary" leadingIcon={RiAddLine} onClick={() => setOpen(true)}>
            Add a person
          </Button>
        }
      />

      {sorted.length <= 1 && (
        <EmptyState
          className="mb-4"
          icon={RiTeamLine}
          title="Just you so far"
          body="Add admins to manage keys and apps, or members who only need to see what is in the pool."
        />
      )}

      {/* Wide screens: a table. */}
      <div className="hidden overflow-hidden rounded-3xl border border-border-button-default bg-background-primary-default md:block">
        <Table aria-label="Users" className="[&_th]:border-t-0">
          <TableHeader>
            <TableColumn isRowHeader>Person</TableColumn>
            <TableColumn>Role</TableColumn>
            <TableColumn>Last login</TableColumn>
            <TableColumn className="text-end">
              <span className="sr-only">Actions</span>
            </TableColumn>
          </TableHeader>
          <TableBody>
            {sorted.map((u) => {
              const self = u.id === me?.id;
              return (
                <TableRow key={u.id} id={u.id}>
                  <TableCell>{who(u, self)}</TableCell>
                  <TableCell>{roleSelect(u, self)}</TableCell>
                  <TableCell className="whitespace-nowrap">
                    <span className="font-mono text-caption-1-regular text-text-secondary" title={dateTime(u.last_login_at)}>{relTime(u.last_login_at, now)}</span>
                  </TableCell>
                  <TableCell className="text-end">{actions(u, self)}</TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>

      {/* Phones: each person stacks into a card. */}
      <ul className="flex flex-col gap-2.5 md:hidden">
        {sorted.map((u) => {
          const self = u.id === me?.id;
          return (
            <li key={u.id} className="flex flex-col gap-3 rounded-3xl border border-border-button-default bg-background-primary-default p-4">
              {who(u, self)}
              <div className="font-mono text-caption-1-regular text-text-secondary" title={dateTime(u.last_login_at)}>
                last login {relTime(u.last_login_at, now)}
              </div>
              <div className="flex flex-wrap items-center justify-between gap-2">
                {roleSelect(u, self)}
                {actions(u, self, true)}
              </div>
            </li>
          );
        })}
      </ul>

      <CreateUserModal open={open} onClose={() => setOpen(false)} />
    </div>
  );
}
