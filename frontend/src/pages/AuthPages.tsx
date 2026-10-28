import { useState, type FormEvent, type ReactNode } from "react";
import { Link, Navigate, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { User } from "../lib/types";
import { useAuth } from "../auth";
import { Button, Field, Input } from "../components/ui";

function AuthShell({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-to-br from-brand-800 to-brand-600 px-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center gap-3">
          <img src="/logo.jpeg" alt="ShopAldukan" className="size-28 rounded-2xl shadow-lg ring-1 ring-cream/20" />
          <span className="text-sm tracking-wide text-cream/70">WhatsApp campaigns</span>
        </div>
        <div className="rounded-xl bg-white p-6 shadow-xl">
          <h1 className="mb-4 text-lg font-semibold text-slate-900">{title}</h1>
          {children}
        </div>
      </div>
    </div>
  );
}

function ErrorText({ error }: { error: string }) {
  return error ? <p className="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p> : null;
}

export function Login() {
  const { user } = useAuth();
  const qc = useQueryClient();
  const nav = useNavigate();
  const loc = useLocation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  if (user) return <Navigate to="/" replace />;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const u = await api.post<User>("/auth/login", { email, password });
      qc.setQueryData(["me"], u);
      nav((loc.state as { from?: string })?.from ?? "/", { replace: true });
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell title="Log in">
      <form onSubmit={submit} className="space-y-4">
        <ErrorText error={error} />
        <Field label="Email">
          <Input type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} autoFocus />
        </Field>
        <Field label="Password">
          <Input type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        <Button type="submit" className="w-full" loading={busy}>
          Log in
        </Button>
        <p className="text-center text-sm">
          <Link to="/forgot-password" className="text-brand-600 hover:underline">
            Forgot your password?
          </Link>
        </p>
      </form>
    </AuthShell>
  );
}

export function ForgotPassword() {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await api.post<{ message: string }>("/auth/forgot", { email });
      setSent(r.message);
    } catch (err) {
      setSent((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <AuthShell title="Reset your password">
      {sent ? (
        <div className="space-y-4">
          <p className="text-sm text-slate-600">{sent}</p>
          <Link to="/login" className="text-sm text-brand-600 hover:underline">
            Back to log in
          </Link>
        </div>
      ) : (
        <form onSubmit={submit} className="space-y-4">
          <p className="text-sm text-slate-500">Enter your email and we'll send you a link to choose a new password.</p>
          <Field label="Email">
            <Input type="email" required value={email} onChange={(e) => setEmail(e.target.value)} autoFocus />
          </Field>
          <Button type="submit" className="w-full" loading={busy}>
            Send reset link
          </Button>
          <p className="text-center text-sm">
            <Link to="/login" className="text-brand-600 hover:underline">
              Back to log in
            </Link>
          </p>
        </form>
      )}
    </AuthShell>
  );
}

export function ResetPassword() {
  const [params] = useSearchParams();
  const nav = useNavigate();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (password !== confirm) {
      setError("Passwords do not match");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.post("/auth/reset", { token: params.get("token"), password });
      nav("/login", { replace: true });
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <AuthShell title="Choose a new password">
      <form onSubmit={submit} className="space-y-4">
        <ErrorText error={error} />
        <Field label="New password" hint="At least 10 characters">
          <Input type="password" autoComplete="new-password" required minLength={10} value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        <Field label="Confirm password">
          <Input type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </Field>
        <Button type="submit" className="w-full" loading={busy}>
          Save password
        </Button>
      </form>
    </AuthShell>
  );
}
