import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Download, Pencil, Plus, RefreshCw, Search, Trash2, Upload, UserCheck, Users } from "lucide-react";
import { api, qs } from "../lib/api";
import type { Contact, Group, ImportResult, Page, TagCount } from "../lib/types";
import { fmtDate, fmtNum } from "../lib/format";
import { useAuth } from "../auth";
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Modal, PageHeader, Pagination, Select, td, th, useToast } from "../components/ui";

function useGroups() {
  return useQuery({ queryKey: ["groups"], queryFn: () => api.get<Group[]>("/groups") });
}

export default function Contacts() {
  const { user } = useAuth();
  const toast = useToast();
  const qc = useQueryClient();
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  const [params] = useSearchParams();
  const [groupId, setGroupId] = useState(params.get("group") ?? "");
  const [tag, setTag] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [editing, setEditing] = useState<Contact | "new" | null>(null);
  const [importing, setImporting] = useState(false);

  useEffect(() => {
    const t = setTimeout(() => setSearch(q), 300);
    return () => clearTimeout(t);
  }, [q]);
  useEffect(() => {
    setPage(1);
    setSelected(new Set());
  }, [search, groupId, tag, status]);

  const filters = { q: search, group_id: groupId, tag, status };
  const list = useQuery({
    queryKey: ["contacts", filters, page],
    queryFn: () => api.get<Page<Contact>>(`/contacts${qs({ ...filters, page, page_size: 50 })}`),
    placeholderData: keepPreviousData,
  });
  const groups = useGroups();
  const tags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<TagCount[]>("/contacts/tags") });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["contacts"] });
    qc.invalidateQueries({ queryKey: ["groups"] });
    qc.invalidateQueries({ queryKey: ["tags"] });
    qc.invalidateQueries({ queryKey: ["dashboard"] });
  };

  const bulk = useMutation({
    mutationFn: (body: { action: string; group_id?: number; tag?: string }) => api.post("/contacts/bulk", { ...body, ids: [...selected] }),
    onSuccess: () => {
      toast.success("Updated");
      setSelected(new Set());
      invalidate();
    },
    onError: toast.error,
  });

  const action = useMutation({
    mutationFn: ({ id, kind }: { id: number; kind: "delete" | "opt-out" | "opt-in" }) =>
      kind === "delete" ? api.del(`/contacts/${id}`) : api.post(`/contacts/${id}/${kind}`),
    onSuccess: () => invalidate(),
    onError: toast.error,
  });

  const syncOptOuts = useMutation({
    mutationFn: () => api.post<{ checked: number; newly_opted_out: number }>("/contacts/sync-opt-outs"),
    onSuccess: (r) => {
      toast.success(`Checked ${r.checked} opt-outs in Bird; ${r.newly_opted_out} newly marked`);
      invalidate();
    },
    onError: toast.error,
  });

  const items = list.data?.items ?? [];
  const allOnPage = items.length > 0 && items.every((c) => selected.has(c.id));
  const toggleAll = () => {
    const next = new Set(selected);
    items.forEach((c) => (allOnPage ? next.delete(c.id) : next.add(c.id)));
    setSelected(next);
  };

  return (
    <>
      <PageHeader
        title="Contacts"
        description={list.data ? `${fmtNum(list.data.total)} contacts match` : undefined}
        actions={
          <>
            {user?.role === "admin" && (
              <Button variant="secondary" onClick={() => syncOptOuts.mutate()} loading={syncOptOuts.isPending} title="Pull opt-outs from Bird">
                <RefreshCw className="size-4" /> Sync opt-outs
              </Button>
            )}
            <a href={`/api/contacts/export.csv${qs(filters)}`}>
              <Button variant="secondary">
                <Download className="size-4" /> Export
              </Button>
            </a>
            <Button variant="secondary" onClick={() => setImporting(true)}>
              <Upload className="size-4" /> Import
            </Button>
            <Button onClick={() => setEditing("new")}>
              <Plus className="size-4" /> Add contact
            </Button>
          </>
        }
      />

      <Card>
        <div className="flex flex-wrap gap-2 border-b border-slate-100 p-3">
          <div className="relative min-w-56 flex-1">
            <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-slate-400" />
            <Input placeholder="Search name, phone or email" value={q} onChange={(e) => setQ(e.target.value)} className="pl-9" />
          </div>
          <Select value={groupId} onChange={(e) => setGroupId(e.target.value)} className="w-auto">
            <option value="">All groups</option>
            {groups.data?.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name} ({g.contacts})
              </option>
            ))}
          </Select>
          <Select value={tag} onChange={(e) => setTag(e.target.value)} className="w-auto">
            <option value="">All tags</option>
            {tags.data?.map((t) => (
              <option key={t.tag} value={t.tag}>
                {t.tag} ({t.count})
              </option>
            ))}
          </Select>
          <Select value={status} onChange={(e) => setStatus(e.target.value)} className="w-auto">
            <option value="">Any status</option>
            <option value="subscribed">Subscribed</option>
            <option value="opted_out">Opted out</option>
          </Select>
        </div>

        {selected.size > 0 && <BulkBar count={selected.size} groups={groups.data ?? []} onAction={(b) => bulk.mutate(b)} busy={bulk.isPending} />}

        {list.isLoading ? (
          <Loading />
        ) : items.length === 0 ? (
          <EmptyState icon={<Users className="size-10" />} title="No contacts found">
            Import a CSV or Excel file, or add contacts one by one.
          </EmptyState>
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-slate-100">
              <thead>
                <tr>
                  <th className={`${th} w-8`}>
                    <input type="checkbox" checked={allOnPage} onChange={toggleAll} aria-label="Select all on page" />
                  </th>
                  <th className={th}>Name</th>
                  <th className={th}>Phone</th>
                  <th className={th}>Groups & tags</th>
                  <th className={th}>Status</th>
                  <th className={th}>Added</th>
                  <th className={th}></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {items.map((c) => (
                  <tr key={c.id} className="hover:bg-slate-50">
                    <td className={td}>
                      <input
                        type="checkbox"
                        checked={selected.has(c.id)}
                        onChange={() => {
                          const n = new Set(selected);
                          if (n.has(c.id)) n.delete(c.id);
                          else n.add(c.id);
                          setSelected(n);
                        }}
                        aria-label={`Select ${c.name || c.phone}`}
                      />
                    </td>
                    <td className={`${td} font-medium text-slate-900`}>
                      {c.name || <span className="text-slate-400">—</span>}
                      {c.email && <div className="text-xs font-normal text-slate-500">{c.email}</div>}
                    </td>
                    <td className={`${td} tabular-nums`} dir="ltr">
                      {c.phone}
                    </td>
                    <td className={td}>
                      <div className="flex max-w-xs flex-wrap gap-1">
                        {(c.groups ?? []).map((g) => (
                          <Badge key={g.id} color="blue">
                            {g.name}
                          </Badge>
                        ))}
                        {c.tags.map((t) => (
                          <Badge key={t}>#{t}</Badge>
                        ))}
                      </div>
                    </td>
                    <td className={td}>
                      {c.opted_out ? (
                        <span title={`Source: ${c.opt_out_source || "unknown"}`}>
                          <Badge color="red">opted out</Badge>
                        </span>
                      ) : (
                        <Badge color="green">subscribed</Badge>
                      )}
                    </td>
                    <td className={`${td} text-slate-500`}>{fmtDate(c.created_at)}</td>
                    <td className={`${td} whitespace-nowrap text-right`}>
                      <Button variant="ghost" size="sm" onClick={() => setEditing(c)} title="Edit">
                        <Pencil className="size-4" />
                      </Button>
                      {c.opted_out ? (
                        user?.role === "admin" && (
                          <Button
                            variant="ghost"
                            size="sm"
                            title="Re-subscribe (only if the customer asked)"
                            onClick={() => window.confirm(`Re-subscribe ${c.phone}? Only do this if the customer asked to receive messages again.`) && action.mutate({ id: c.id, kind: "opt-in" })}
                          >
                            <UserCheck className="size-4" />
                          </Button>
                        )
                      ) : (
                        <Button variant="ghost" size="sm" title="Mark as opted out" onClick={() => window.confirm(`Mark ${c.phone} as opted out?`) && action.mutate({ id: c.id, kind: "opt-out" })}>
                          <Ban className="size-4" />
                        </Button>
                      )}
                      <Button variant="ghost" size="sm" title="Delete" onClick={() => window.confirm(`Delete ${c.name || c.phone}?`) && action.mutate({ id: c.id, kind: "delete" })}>
                        <Trash2 className="size-4 text-red-500" />
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {list.data && <Pagination page={page} pageSize={list.data.page_size} total={list.data.total} onChange={setPage} />}
      </Card>

      {editing && <ContactModal contact={editing === "new" ? null : editing} groups={groups.data ?? []} onClose={() => setEditing(null)} onSaved={invalidate} />}
      {importing && <ImportModal groups={groups.data ?? []} onClose={() => setImporting(false)} onDone={invalidate} />}
    </>
  );
}

function BulkBar({ count, groups, onAction, busy }: { count: number; groups: Group[]; onAction: (b: { action: string; group_id?: number; tag?: string }) => void; busy: boolean }) {
  const [groupId, setGroupId] = useState("");
  const [tag, setTag] = useState("");
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-slate-100 bg-brand-50 px-3 py-2 text-sm">
      <span className="font-medium text-brand-700">{count} selected</span>
      <Select value={groupId} onChange={(e) => setGroupId(e.target.value)} className="w-auto py-1">
        <option value="">Choose group…</option>
        {groups.map((g) => (
          <option key={g.id} value={g.id}>
            {g.name}
          </option>
        ))}
      </Select>
      <Button size="sm" variant="secondary" disabled={!groupId || busy} onClick={() => onAction({ action: "add_group", group_id: Number(groupId) })}>
        Add to group
      </Button>
      <Button size="sm" variant="secondary" disabled={!groupId || busy} onClick={() => onAction({ action: "remove_group", group_id: Number(groupId) })}>
        Remove from group
      </Button>
      <Input value={tag} onChange={(e) => setTag(e.target.value)} placeholder="tag" className="w-28 py-1" />
      <Button size="sm" variant="secondary" disabled={!tag || busy} onClick={() => onAction({ action: "add_tag", tag })}>
        Add tag
      </Button>
      <Button size="sm" variant="secondary" disabled={!tag || busy} onClick={() => onAction({ action: "remove_tag", tag })}>
        Remove tag
      </Button>
      <Button size="sm" variant="danger" disabled={busy} onClick={() => window.confirm(`Delete ${count} contacts? This cannot be undone.`) && onAction({ action: "delete" })}>
        <Trash2 className="size-3.5" /> Delete
      </Button>
    </div>
  );
}

function ContactModal({ contact, groups, onClose, onSaved }: { contact: Contact | null; groups: Group[]; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [phone, setPhone] = useState(contact?.phone ?? "");
  const [name, setName] = useState(contact?.name ?? "");
  const [email, setEmail] = useState(contact?.email ?? "");
  const [tags, setTags] = useState(contact?.tags.join(", ") ?? "");
  const [groupIds, setGroupIds] = useState<number[]>(contact?.groups?.map((g) => g.id) ?? []);
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const body = { phone, name, email, tags: tags.split(",").map((t) => t.trim()).filter(Boolean), group_ids: groupIds };
      if (contact) await api.put(`/contacts/${contact.id}`, body);
      else await api.post("/contacts", body);
      toast.success(contact ? "Contact updated" : "Contact added");
      onSaved();
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
      title={contact ? "Edit contact" : "Add contact"}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="contact-form" loading={busy}>
            Save
          </Button>
        </>
      }
    >
      <form id="contact-form" onSubmit={submit} className="space-y-4">
        <Field label="Phone" hint="Local Qatar numbers like 5512 3456 become +974 automatically">
          <Input required value={phone} onChange={(e) => setPhone(e.target.value)} dir="ltr" autoFocus />
        </Field>
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Email">
          <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
        <Field label="Tags" hint="Separate with commas">
          <Input value={tags} onChange={(e) => setTags(e.target.value)} placeholder="vip, abaya" />
        </Field>
        {groups.length > 0 && (
          <div>
            <span className="mb-1 block text-sm font-medium text-slate-700">Groups</span>
            <div className="flex flex-wrap gap-2">
              {groups.map((g) => (
                <label key={g.id} className="flex items-center gap-1.5 rounded-lg border border-slate-200 px-2 py-1 text-sm">
                  <input
                    type="checkbox"
                    checked={groupIds.includes(g.id)}
                    onChange={(e) => setGroupIds(e.target.checked ? [...groupIds, g.id] : groupIds.filter((x) => x !== g.id))}
                  />
                  {g.name}
                </label>
              ))}
            </div>
          </div>
        )}
      </form>
    </Modal>
  );
}

function ImportModal({ groups, onClose, onDone }: { groups: Group[]; onClose: () => void; onDone: () => void }) {
  const toast = useToast();
  const [file, setFile] = useState<File | null>(null);
  const [groupId, setGroupId] = useState("");
  const [newGroup, setNewGroup] = useState("");
  const [tags, setTags] = useState("");
  const [updateExisting, setUpdateExisting] = useState(true);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [issueFilter, setIssueFilter] = useState<"" | "invalid" | "duplicate">("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!file) return;
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      if (groupId === "new") fd.append("new_group_name", newGroup);
      else if (groupId) fd.append("group_id", groupId);
      fd.append("tags", tags);
      fd.append("update_existing", String(updateExisting));
      const r = await api.post<ImportResult>("/contacts/import", fd);
      setResult(r);
      onDone();
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(false);
    }
  };

  const issues = useMemo(() => (result?.issues ?? []).filter((i) => !issueFilter || i.status === issueFilter), [result, issueFilter]);

  const downloadIssues = () => {
    const rows = [["row", "phone", "name", "problem", "reason"], ...(result?.issues ?? []).map((i) => [i.row, i.phone, i.name, i.status, i.reason])];
    const csv = "﻿" + rows.map((r) => r.map((v) => `"${String(v).replaceAll('"', '""')}"`).join(",")).join("\n");
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    a.download = "import-problems.csv";
    a.click();
  };

  if (result) {
    return (
      <Modal open onClose={onClose} title="Import finished" wide footer={<Button onClick={onClose}>Done</Button>}>
        <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-5">
          {[
            ["Rows", result.total, ""],
            ["New", result.created, "text-brand-700"],
            ["Updated", result.updated, "text-blue-700"],
            ["Duplicates", result.duplicates, result.duplicates ? "text-amber-600" : ""],
            ["Invalid", result.invalid, result.invalid ? "text-red-600" : ""],
          ].map(([l, v, c]) => (
            <div key={l as string} className="rounded-lg border border-slate-200 p-3">
              <p className="text-xs text-slate-500">{l}</p>
              <p className={`text-xl font-semibold tabular-nums ${c}`}>{fmtNum(v as number)}</p>
            </div>
          ))}
        </div>
        {result.columns && Object.keys(result.columns).length > 0 && (
          <p className="mb-3 text-xs text-slate-500">
            Columns used: {Object.entries(result.columns).map(([k, v]) => `${k} ← "${v}"`).join(", ")}
          </p>
        )}
        {(result.issues?.length ?? 0) > 0 ? (
          <>
            <div className="mb-2 flex items-center gap-2">
              <Select value={issueFilter} onChange={(e) => setIssueFilter(e.target.value as typeof issueFilter)} className="w-auto py-1">
                <option value="">All problems ({result.issues!.length})</option>
                <option value="invalid">Invalid ({result.invalid})</option>
                <option value="duplicate">Duplicates ({result.duplicates})</option>
              </Select>
              <Button size="sm" variant="secondary" onClick={downloadIssues}>
                <Download className="size-3.5" /> Download list
              </Button>
            </div>
            <div className="max-h-72 overflow-y-auto rounded-lg border border-slate-200">
              <table className="min-w-full divide-y divide-slate-100 text-sm">
                <thead className="sticky top-0 bg-slate-50">
                  <tr>
                    <th className={th}>Row</th>
                    <th className={th}>Phone</th>
                    <th className={th}>Name</th>
                    <th className={th}>Problem</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {issues.slice(0, 500).map((i) => (
                    <tr key={i.row}>
                      <td className={`${td} tabular-nums`}>{i.row}</td>
                      <td className={td} dir="ltr">
                        {i.phone || "—"}
                      </td>
                      <td className={td}>{i.name}</td>
                      <td className={td}>
                        <Badge color={i.status === "invalid" ? "red" : "yellow"}>{i.status}</Badge> <span className="text-slate-500">{i.reason}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        ) : (
          <p className="text-sm text-brand-700">Every row was valid.</p>
        )}
      </Modal>
    );
  }

  return (
    <Modal
      open
      onClose={onClose}
      title="Import contacts"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="import-form" loading={busy} disabled={!file || (groupId === "new" && !newGroup.trim())}>
            Import
          </Button>
        </>
      }
    >
      <form id="import-form" onSubmit={submit} className="space-y-4">
        <div className="rounded-lg bg-slate-50 p-3 text-sm text-slate-600">
          Upload a <b>.csv</b> or <b>.xlsx</b> file with a header row. Recognised columns: <b>phone</b> (or mobile, number, whatsapp), <b>name</b> (or first name + last name), <b>email</b>, <b>tags</b>.
          Numbers without a country code are treated as Qatar (+974). Duplicates and invalid numbers are skipped and listed after import.
        </div>
        <Field label="File">
          <Input type="file" accept=".csv,.xlsx,.xlsm,.txt" required onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        </Field>
        <Field label="Add everyone to a group (optional)">
          <Select value={groupId} onChange={(e) => setGroupId(e.target.value)}>
            <option value="">No group</option>
            <option value="new">+ New group…</option>
            {groups.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
          </Select>
        </Field>
        {groupId === "new" && (
          <Field label="New group name">
            <Input value={newGroup} onChange={(e) => setNewGroup(e.target.value)} required />
          </Field>
        )}
        <Field label="Add tags (optional)" hint="Separate with commas">
          <Input value={tags} onChange={(e) => setTags(e.target.value)} placeholder="summer-2026" />
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={updateExisting} onChange={(e) => setUpdateExisting(e.target.checked)} />
          Update names and emails of contacts that already exist
        </label>
        <p className="text-xs text-slate-500">Opted-out contacts stay opted out even if they appear in the file.</p>
      </form>
    </Modal>
  );
}
