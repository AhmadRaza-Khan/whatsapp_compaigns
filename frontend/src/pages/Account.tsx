import { useState, type FormEvent } from "react";
import { api } from "../lib/api";
import { useAuth } from "../auth";
import { Button, Card, Field, Input, PageHeader, useToast } from "../components/ui";

export default function Account() {
  const { user } = useAuth();
  const toast = useToast();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (next !== confirm) {
      toast.error("New passwords do not match");
      return;
    }
    setBusy(true);
    try {
      await api.post("/auth/change-password", { current_password: current, new_password: next });
      toast.success("Password changed. Other sessions were logged out.");
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader title="My account" description={`${user?.email} · ${user?.role}`} />
      <Card title="Change password" className="max-w-lg">
        <form onSubmit={submit} className="space-y-4 p-5">
          <Field label="Current password">
            <Input type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
          </Field>
          <Field label="New password" hint="At least 10 characters">
            <Input type="password" autoComplete="new-password" required minLength={10} value={next} onChange={(e) => setNext(e.target.value)} />
          </Field>
          <Field label="Confirm new password">
            <Input type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          </Field>
          <Button type="submit" loading={busy}>
            Change password
          </Button>
        </form>
      </Card>
    </>
  );
}
