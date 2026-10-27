import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2, UsersRound } from "lucide-react";
import { api } from "../lib/api";
import type { Group } from "../lib/types";
import { fmtDate, fmtNum } from "../lib/format";
import { Button, Card, EmptyState, Field, Input, Loading, Modal, PageHeader, td, th, useToast } from "../components/ui";

export default function Groups() {
  const toast = useToast();
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ["groups"], queryFn: () => api.get<Group[]>("/groups") });
  const [editing, setEditing] = useState<Group | "new" | null>(null);

  const del = useMutation({
    mutationFn: (id: number) => api.del(`/groups/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups"] });
      toast.success("Group deleted (contacts were kept)");
    },
    onError: toast.error,
  });

  return (
    <>
      <PageHeader
        title="Groups"
        description="Lists of contacts you send campaigns to. A contact can be in several groups."
        actions={
          <Button onClick={() => setEditing("new")}>
            <Plus className="size-4" /> New group
          </Button>
        }
      />
      <Card>
        {isLoading ? (
          <Loading />
        ) : !data?.length ? (
          <EmptyState icon={<UsersRound className="size-10" />} title="No groups yet">
            Create a group, or choose "New group" when importing contacts.
          </EmptyState>
        ) : (
          <table className="min-w-full divide-y divide-slate-100">
            <thead>
              <tr>
                <th className={th}>Name</th>
                <th className={`${th} text-right`}>Contacts</th>
                <th className={`${th} text-right`}>Subscribed</th>
                <th className={th}>Created</th>
                <th className={th}></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {data.map((g) => (
                <tr key={g.id} className="hover:bg-slate-50">
                  <td className={td}>
                    <Link to={`/contacts?group=${g.id}`} className="font-medium text-slate-900">
                      {g.name}
                    </Link>
                    {g.description && <div className="text-xs text-slate-500">{g.description}</div>}
                  </td>
                  <td className={`${td} text-right tabular-nums`}>{fmtNum(g.contacts)}</td>
                  <td className={`${td} text-right tabular-nums`}>
                    {fmtNum(g.contacts - g.opted_out)}
                    {g.opted_out > 0 && <span className="ml-1 text-xs text-slate-400">({g.opted_out} opted out)</span>}
                  </td>
                  <td className={`${td} text-slate-500`}>{fmtDate(g.created_at)}</td>
                  <td className={`${td} whitespace-nowrap text-right`}>
                    <Button variant="ghost" size="sm" onClick={() => setEditing(g)} title="Rename">
                      <Pencil className="size-4" />
                    </Button>
                    <Button variant="ghost" size="sm" title="Delete" onClick={() => window.confirm(`Delete group "${g.name}"? Contacts are not deleted.`) && del.mutate(g.id)}>
                      <Trash2 className="size-4 text-red-500" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
      {editing && <GroupModal group={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}
    </>
  );
}

function GroupModal({ group, onClose }: { group: Group | null; onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [name, setName] = useState(group?.name ?? "");
  const [description, setDescription] = useState(group?.description ?? "");
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      if (group) await api.put(`/groups/${group.id}`, { name, description });
      else await api.post("/groups", { name, description });
      qc.invalidateQueries({ queryKey: ["groups"] });
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
      title={group ? "Edit group" : "New group"}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="group-form" loading={busy}>
            Save
          </Button>
        </>
      }
    >
      <form id="group-form" onSubmit={submit} className="space-y-4">
        <Field label="Name">
          <Input required value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </Field>
        <Field label="Description (optional)">
          <Input value={description} onChange={(e) => setDescription(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}
