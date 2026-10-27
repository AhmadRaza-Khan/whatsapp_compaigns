import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Copy, PlugZap, XCircle } from "lucide-react";
import { api } from "../lib/api";
import type { Settings, SettingsInfo } from "../lib/types";
import { Button, Card, Field, Input, Loading, PageHeader, Select, Textarea, useToast } from "../components/ui";

type Resp = { settings: Settings; info: SettingsInfo };

function Check({ ok, children }: { ok: boolean; children: ReactNode }) {
  return (
    <li className="flex items-center gap-2 text-sm">
      {ok ? <CheckCircle2 className="size-4 text-emerald-600" /> : <XCircle className="size-4 text-red-500" />}
      {children}
    </li>
  );
}

function CopyField({ label, value }: { label: string; value: string }) {
  const toast = useToast();
  return (
    <Field label={label}>
      <div className="flex gap-2">
        <Input readOnly value={value} className="font-mono text-xs" />
        <Button
          type="button"
          variant="secondary"
          onClick={() => {
            navigator.clipboard.writeText(value);
            toast.success("Copied");
          }}
        >
          <Copy className="size-4" />
        </Button>
      </div>
    </Field>
  );
}

export default function SettingsPage() {
  const toast = useToast();
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ["settings"], queryFn: () => api.get<Resp>("/settings") });
  const [s, setS] = useState<Settings | null>(null);
  const [keywords, setKeywords] = useState("");

  useEffect(() => {
    if (data) {
      setS(data.settings);
      setKeywords(data.settings.stop_keywords.join("\n"));
    }
  }, [data]);

  const save = useMutation({
    mutationFn: (body: Settings) => api.put<Resp>("/settings", body),
    onSuccess: (r) => {
      qc.setQueryData(["settings"], r);
      toast.success("Settings saved");
    },
    onError: toast.error,
  });
  const test = useMutation({
    mutationFn: () => api.get<{ ok: boolean; message: string; templates?: number }>("/settings/bird-status"),
    onSuccess: (r) => (r.ok ? toast.success(`${r.message} · ${r.templates} templates visible`) : toast.error(r.message)),
    onError: toast.error,
  });

  if (isLoading || !data || !s) return <Loading />;
  const info = data.info;
  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setS({ ...s, [k]: v });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({ ...s, stop_keywords: keywords.split("\n").map((k) => k.trim()).filter(Boolean) });
  };

  return (
    <>
      <PageHeader title="Settings" description="Admin only. Secrets (Bird API key, webhook secret) are set on the server, never here." />
      <div className="grid gap-6 lg:grid-cols-[1fr_380px]">
        <form onSubmit={submit} className="space-y-6">
          <Card title="Sending & cost">
            <div className="grid gap-4 p-5 sm:grid-cols-2">
              <Field label="Marketing rate per message" hint="Meta + Bird fee. Qatar currently 0.0341 + 0.0050 = 0.0391">
                <Input type="number" step="0.0001" min="0" value={s.marketing_rate} onChange={(e) => set("marketing_rate", Number(e.target.value))} />
              </Field>
              <Field label="Currency">
                <Select value={s.currency} onChange={(e) => set("currency", e.target.value)}>
                  <option>USD</option>
                  <option>EUR</option>
                  <option>QAR</option>
                </Select>
              </Field>
              <Field label="Send speed (messages per minute)" hint="Must match your Bird plan: Startup 30, Growth 100">
                <Input type="number" min="1" max="1000" value={s.send_rate_per_minute} onChange={(e) => set("send_rate_per_minute", Number(e.target.value))} />
              </Field>
              <Field label="Default country for numbers" hint="Numbers without + are read as this country (QA = +974)">
                <Input maxLength={2} value={s.default_region} onChange={(e) => set("default_region", e.target.value.toUpperCase())} />
              </Field>
              <Field label="Name used when a contact has none" hint="Fills {name} / {first_name}">
                <Input value={s.name_fallback} onChange={(e) => set("name_fallback", e.target.value)} />
              </Field>
            </div>
          </Card>
          <Card title="Bird">
            <div className="grid gap-4 p-5 sm:grid-cols-2">
              <Field label="Sender number" hint="The connected WhatsApp number in E.164, e.g. +97412345678 (or Bird's number ID)">
                <Input value={s.bird_from} onChange={(e) => set("bird_from", e.target.value)} dir="ltr" />
              </Field>
              <Field label="WhatsApp Business Account (WABA) ID" hint="Needed to create templates from the platform">
                <Input value={s.bird_waba_id} onChange={(e) => set("bird_waba_id", e.target.value)} />
              </Field>
              <div className="sm:col-span-2">
                <Field label="Bird dashboard link" hint="Opened from the Templates screen">
                  <Input value={s.bird_dashboard_url} onChange={(e) => set("bird_dashboard_url", e.target.value)} />
                </Field>
              </div>
            </div>
          </Card>
          <Card title="Opt-out keywords">
            <div className="p-5">
              <Field label="One per line" hint="A reply that exactly matches one of these (any capitalisation) opts the customer out. Bird's own STOP handling also applies.">
                <Textarea rows={6} value={keywords} onChange={(e) => setKeywords(e.target.value)} dir="auto" />
              </Field>
            </div>
          </Card>
          <Button type="submit" loading={save.isPending}>
            Save settings
          </Button>
        </form>

        <div className="space-y-6">
          <Card title="Server status" actions={<Button size="sm" variant="secondary" onClick={() => test.mutate()} loading={test.isPending}><PlugZap className="size-3.5" /> Test Bird</Button>}>
            <ul className="space-y-2 p-5">
              <Check ok={info.bird_configured}>Bird API key set</Check>
              <Check ok={info.webhook_secret_configured}>Webhook signing secret set</Check>
              <Check ok={info.smtp_configured}>Email (password reset) configured</Check>
              <li className="pt-1 text-xs text-slate-500">API host: {info.bird_api_host}</li>
            </ul>
          </Card>
          <Card title="Bird webhook setup">
            <div className="space-y-4 p-5">
              <CopyField label="Webhook URL" value={info.webhook_url} />
              <div>
                <p className="mb-1 text-sm font-medium text-slate-700">Subscribe to these events</p>
                <ul className="space-y-0.5 font-mono text-xs text-slate-600">
                  {info.webhook_events.map((e) => (
                    <li key={e}>{e}</li>
                  ))}
                </ul>
              </div>
              <CopyField label="Shop Now button URL for templates" value={info.click_url_pattern} />
            </div>
          </Card>
        </div>
      </div>
    </>
  );
}
