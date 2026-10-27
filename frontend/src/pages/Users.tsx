import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { api } from "../lib/api";
import type { Role, UserRow } from "../lib/types";
import { fmtDateTime } from "../lib/format";
import { useAuth } from "../auth";
import { Badge, Button, Card, Field, Input, Loading, Modal, PageHeader, Select, td, th, useToast } from "../components/ui";

export default function Users() {
  const { user: me } = useAuth();
  const toast = useToast();
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ["users"], queryFn: () => api.get<UserRow[]>("/users") });
  const [editing, setEditing] = useState<UserRow | "new" | null>(null);
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/users/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
    onError: toast.error,
  });

  return (
    <>
      <PageHeader
        title="Users"
        description="Admins can do everything. Staff can manage contacts and campaigns but not users or settings."
        actions={
          <Button onClick={() => setEditing("new")}>
            <Plus className="size-4" /> Add user
          </Button>
        }
      />
      <Card>
        {isLoading ? (
          <Loading />
        ) : (
          <table className="min-w-full divide-y divide-slate-100">
            <thead>
              <tr>
                <th className={th}>User</th>
                <th className={th}>Role</th>
                <th className={th}>Status</th>
                <th className={th}>Last login</th>
                <th className={th}></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {data?.map((u) => (
                <tr key={u.id}>
                  <td className={td}>
                    <div className="font-medium text-slate-900">{u.name || "—"}</div>
                    <div className="text-xs text-slate-500">{u.email}</div>
                  </td>
                  <td className={td}>
                    <Badge color={u.role === "admin" ? "purple" : "blue"}>{u.role}</Badge>
                  </td>
                  <td className={td}>{u.active ? <Badge color="green">active</Badge> : <Badge>disabled</Badge>}</td>
                  <td className={`${td} text-slate-500`}>{fmtDateTime(u.last_login_at)}</td>
                  <td className={`${td} whitespace-nowrap text-right`}>
                    <Button variant="ghost" size="sm" onClick={() => setEditing(u)} title="Edit">
                      <Pencil className="size-4" />
                    </Button>
                    {u.id !== me?.id && (
                      <Button variant="ghost" size="sm" title="Delete" onClick={() => window.confirm(`Delete ${u.email}?`) && del.mutate(u.id)}>
                        <Trash2 className="size-4 text-red-500" />
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
      {editing && <UserModal user={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}
    </>
  );
}

function UserModal({ user, onClose }: { user: UserRow | null; onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [email, setEmail] = useState(user?.email ?? "");
  const [name, setName] = useState(user?.name ?? "");
  const [role, setRole] = useState<Role>(user?.role ?? "staff");
  const [active, setActive] = useState(user?.active ?? true);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      if (user) await api.put(`/users/${user.id}`, { name, role, active, password });
      else await api.post("/users", { email, name, role, password });
      qc.invalidateQueries({ queryKey: ["users"] });
      toast.success(user ? "User updated" : "User created. Share the password with them securely.");
      onClose();
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={user ? "Edit user" : "Add user"}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="user-form" loading={busy}>
            Save
          </Button>
        </>
      }
    >
      <form id="user-form" onSubmit={submit} className="space-y-4">
        <Field label="Email">
          <Input type="email" required disabled={!!user} value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Role">
          <Select value={role} onChange={(e) => setRole(e.target.value as Role)}>
            <option value="staff">Staff — contacts and campaigns</option>
            <option value="admin">Admin — everything</option>
          </Select>
        </Field>
        <Field label={user ? "New password (leave empty to keep)" : "Password"} hint="At least 10 characters">
          <Input type="password" autoComplete="new-password" required={!user} minLength={10} value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        {user && (
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} />
            Account active
          </label>
        )}
      </form>
    </Modal>
  );
}
