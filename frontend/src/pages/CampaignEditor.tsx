import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CalendarClock, Info, Send, Smartphone } from "lucide-react";
import { api } from "../lib/api";
import type { Audience, CampaignDetail, Estimate, Group, TagCount, Template, Variables } from "../lib/types";
import { fmtDuration, fmtMoney, fmtNum, qatarNowInput } from "../lib/format";
import { useAuth } from "../auth";
import TemplatePreview from "../components/TemplatePreview";
import { Button, Card, Field, Input, Loading, Modal, PageHeader, Select, useToast } from "../components/ui";

const emptyVars: Variables = { header_media_url: "", header_text: [], body: [], buttons: [], shop_url: "" };
const emptyAudience: Audience = { all: false, group_ids: [], tags: [] };

export default function CampaignEditor() {
  const { id } = useParams();
  const nav = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const { user } = useAuth();

  const templates = useQuery({ queryKey: ["templates"], queryFn: () => api.get<Template[]>("/templates") });
  const groups = useQuery({ queryKey: ["groups"], queryFn: () => api.get<Group[]>("/groups") });
  const tags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<TagCount[]>("/contacts/tags") });
  const existing = useQuery({ queryKey: ["campaign", id], queryFn: () => api.get<CampaignDetail>(`/campaigns/${id}`), enabled: !!id });

  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [language, setLanguage] = useState("");
  const [vars, setVars] = useState<Variables>(emptyVars);
  const [audience, setAudience] = useState<Audience>(emptyAudience);
  const [estimate, setEstimate] = useState<Estimate | null>(null);
  const [savedId, setSavedId] = useState<number | null>(id ? Number(id) : null);
  const [busy, setBusy] = useState<"" | "save" | "test" | "send">("");
  const [testPhone, setTestPhone] = useState("");
  const [sendOpen, setSendOpen] = useState(false);

  useEffect(() => {
    const c = existing.data?.campaign;
    if (!c) return;
    if (c.status !== "draft") {
      nav(`/campaigns/${c.id}`, { replace: true });
      return;
    }
    setName(c.name);
    setSlug(c.template_slug);
    setLanguage(c.template_language);
    setVars({ ...emptyVars, ...c.variables });
    setAudience({ ...emptyAudience, ...c.audience });
  }, [existing.data, nav]);

  const sendable = (templates.data ?? []).filter((t) => t.sendable);
  const template = templates.data?.find((t) => t.slug === slug);
  const spec = template?.spec;

  // Live recipient count and cost estimate.
  useEffect(() => {
    const t = setTimeout(() => {
      api
        .post<Estimate>("/campaigns/estimate", { audience })
        .then(setEstimate)
        .catch(() => setEstimate(null));
    }, 250);
    return () => clearTimeout(t);
  }, [audience]);

  const missing = useMemo(() => {
    const m: string[] = [];
    if (!name.trim()) m.push("campaign name");
    if (!template) return [...m, "template"];
    if (["image", "video", "document", "gif"].includes(spec!.header_format) && !vars.header_media_url.startsWith("https://")) m.push(`${spec!.header_format} link`);
    spec!.body_params?.forEach((_, i) => !vars.body[i]?.trim() && m.push(`message value ${i + 1}`));
    spec!.header_params?.forEach((_, i) => !vars.header_text[i]?.trim() && m.push(`header value ${i + 1}`));
    spec!.buttons?.forEach((b, i) => {
      if (b.tracked && !/^https?:\/\/.+\..+/.test(vars.shop_url)) m.push(`"${b.text}" link`);
      if (!b.tracked && b.params?.length) b.params.forEach((_, j) => !vars.buttons[i]?.[j]?.trim() && m.push(`"${b.text}" value`));
    });
    if (!audience.all && !audience.group_ids.length && !audience.tags.length) m.push("audience");
    return m;
  }, [name, template, spec, vars, audience]);

  const save = async (quiet = false): Promise<number | null> => {
    if (!name.trim() || !slug) {
      toast.error("Give the campaign a name and choose a template");
      return null;
    }
    setBusy("save");
    try {
      const body = { name, template_slug: slug, template_language: language, variables: vars, audience };
      let cid = savedId;
      if (cid) await api.put(`/campaigns/${cid}`, body);
      else {
        cid = (await api.post<{ id: number }>("/campaigns", body)).id;
        setSavedId(cid);
        window.history.replaceState(null, "", `/campaigns/${cid}/edit`);
      }
      qc.invalidateQueries({ queryKey: ["campaigns"] });
      if (!quiet) toast.success("Draft saved");
      return cid;
    } catch (err) {
      toast.error(err);
      return null;
    } finally {
      setBusy("");
    }
  };

  const sendTest = async () => {
    const cid = await save(true);
    if (!cid) return;
    setBusy("test");
    try {
      const r = await api.post<{ to: string }>(`/campaigns/${cid}/test`, { phone: testPhone });
      toast.success(`Test sent to ${r.to}. Check WhatsApp in a few seconds.`);
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy("");
    }
  };

  if (id && existing.isLoading) return <Loading />;
  if (templates.isLoading) return <Loading />;

  const setBody = (i: number, v: string) => {
    const b = [...vars.body];
    b[i] = v;
    setVars({ ...vars, body: b });
  };

  return (
    <>
      <PageHeader title={savedId ? "Edit campaign" : "New campaign"} description="Fill in the template, pick your audience, test, then send or schedule." />
      <div className="grid gap-6 lg:grid-cols-[1fr_340px]">
        <div className="space-y-6">
          <Card title="1. Message">
            <div className="space-y-4 p-5">
              <Field label="Campaign name" hint="Only you see this">
                <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Winter collection launch" />
              </Field>
              {sendable.length === 0 ? (
                <div className="flex gap-2 rounded-lg bg-amber-50 p-3 text-sm text-amber-800">
                  <AlertTriangle className="size-5 shrink-0" />
                  <span>
                    No approved templates yet. Go to <Link to="/templates" className="underline">Templates</Link> and sync from Bird.
                  </span>
                </div>
              ) : (
                <div className="grid gap-3 sm:grid-cols-[1fr_140px]">
                  <Field label="Template">
                    <Select
                      value={slug}
                      onChange={(e) => {
                        const t = templates.data?.find((x) => x.slug === e.target.value);
                        setSlug(e.target.value);
                        setLanguage(t?.default_language ?? "");
                        setVars({ ...emptyVars, shop_url: vars.shop_url });
                      }}
                    >
                      <option value="">Choose an approved template…</option>
                      {sendable.map((t) => (
                        <option key={t.slug} value={t.slug}>
                          {t.name || t.slug} ({t.category})
                        </option>
                      ))}
                    </Select>
                  </Field>
                  {template && (
                    <Field label="Language">
                      <Select value={language} onChange={(e) => setLanguage(e.target.value)}>
                        {template.available_languages.map((l) => (
                          <option key={l}>{l}</option>
                        ))}
                      </Select>
                    </Field>
                  )}
                </div>
              )}

              {spec && (
                <>
                  {["image", "video", "document", "gif"].includes(spec.header_format) && (
                    <Field label={`${spec.header_format[0].toUpperCase()}${spec.header_format.slice(1)} link`} hint="A public https:// link, e.g. from your website or Shopify files. JPG/PNG up to 5 MB.">
                      <Input value={vars.header_media_url} onChange={(e) => setVars({ ...vars, header_media_url: e.target.value })} placeholder={spec.header_example_url || "https://…/collection.jpg"} />
                    </Field>
                  )}
                  {spec.header_params?.map((p, i) => (
                    <Field key={`h${i}`} label={`Header value ${i + 1}`} hint={p.example && `Example: ${p.example}`}>
                      <Input
                        value={vars.header_text[i] ?? ""}
                        onChange={(e) => {
                          const h = [...vars.header_text];
                          h[i] = e.target.value;
                          setVars({ ...vars, header_text: h });
                        }}
                      />
                    </Field>
                  ))}
                  {spec.body_params?.map((p, i) => (
                    <Field key={`b${i}`} label={`Message value {{${p.name ?? i + 1}}}`} hint={p.example && `Example from template: ${p.example}`}>
                      <div className="flex gap-2">
                        <Input value={vars.body[i] ?? ""} onChange={(e) => setBody(i, e.target.value)} dir="auto" />
                        <Button type="button" variant="secondary" size="sm" title="Insert the customer's first name" onClick={() => setBody(i, (vars.body[i] ?? "") + "{first_name}")}>
                          + Name
                        </Button>
                      </div>
                    </Field>
                  ))}
                  {spec.buttons?.map((b, i) =>
                    b.tracked ? (
                      <Field key={`btn${i}`} label={`"${b.text}" button opens`} hint="Your shop page for this collection. Clicks are counted before forwarding.">
                        <Input value={vars.shop_url} onChange={(e) => setVars({ ...vars, shop_url: e.target.value })} placeholder="https://yourshop.com/collections/winter" />
                      </Field>
                    ) : (
                      b.params?.map((p, j) => (
                        <Field key={`btn${i}-${j}`} label={`"${b.text}" button value`} hint={p.example && `Example: ${p.example}`}>
                          <Input
                            value={vars.buttons[i]?.[j] ?? ""}
                            onChange={(e) => {
                              const bs = vars.buttons.map((x) => [...(x ?? [])]);
                              while (bs.length <= i) bs.push([]);
                              bs[i][j] = e.target.value;
                              setVars({ ...vars, buttons: bs });
                            }}
                          />
                        </Field>
                      ))
                    ),
                  )}
                  {!spec.buttons?.some((b) => b.tracked) && (
                    <p className="flex gap-2 text-xs text-slate-500">
                      <Info className="size-4 shrink-0" /> This template has no tracked Shop Now button, so clicks won't be counted.
                    </p>
                  )}
                </>
              )}
            </div>
          </Card>

          <Card title="2. Audience">
            <div className="space-y-4 p-5">
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={audience.all} onChange={(e) => setAudience({ ...audience, all: e.target.checked })} />
                <span className="font-medium">All subscribed contacts</span>
              </label>
              {!audience.all && (
                <>
                  <div>
                    <p className="mb-1.5 text-sm font-medium text-slate-700">Groups</p>
                    <div className="flex flex-wrap gap-2">
                      {(groups.data ?? []).length === 0 && <span className="text-sm text-slate-500">No groups yet.</span>}
                      {groups.data?.map((g) => (
                        <label key={g.id} className={`flex cursor-pointer items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-sm ${audience.group_ids.includes(g.id) ? "border-brand-600 bg-brand-50" : "border-slate-200"}`}>
                          <input
                            type="checkbox"
                            checked={audience.group_ids.includes(g.id)}
                            onChange={(e) => setAudience({ ...audience, group_ids: e.target.checked ? [...audience.group_ids, g.id] : audience.group_ids.filter((x) => x !== g.id) })}
                          />
                          {g.name} <span className="text-slate-400">{g.contacts - g.opted_out}</span>
                        </label>
                      ))}
                    </div>
                  </div>
                  {(tags.data ?? []).length > 0 && (
                    <div>
                      <p className="mb-1.5 text-sm font-medium text-slate-700">Tags</p>
                      <div className="flex flex-wrap gap-2">
                        {tags.data?.map((t) => (
                          <label key={t.tag} className={`flex cursor-pointer items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-sm ${audience.tags.includes(t.tag) ? "border-brand-600 bg-brand-50" : "border-slate-200"}`}>
                            <input
                              type="checkbox"
                              checked={audience.tags.includes(t.tag)}
                              onChange={(e) => setAudience({ ...audience, tags: e.target.checked ? [...audience.tags, t.tag] : audience.tags.filter((x) => x !== t.tag) })}
                            />
                            #{t.tag} <span className="text-slate-400">{t.count}</span>
                          </label>
                        ))}
                      </div>
                    </div>
                  )}
                  <p className="text-xs text-slate-500">Contacts in any selected group or tag receive the message once.</p>
                </>
              )}
            </div>
          </Card>
        </div>

        <div className="space-y-4 lg:sticky lg:top-6 lg:self-start">
          <Card title="Preview">
            <div className="p-4">{spec ? <TemplatePreview spec={spec} values={vars} sampleName={user?.name || "Sara"} /> : <p className="text-sm text-slate-500">Choose a template to see a preview.</p>}</div>
          </Card>
          <Card title="Summary">
            <div className="space-y-2 p-4 text-sm">
              <Row label="Recipients" value={estimate ? fmtNum(estimate.eligible) : "—"} strong />
              {estimate && estimate.opted_out_excluded > 0 && <Row label="Opted out (excluded)" value={fmtNum(estimate.opted_out_excluded)} />}
              <Row label="Estimated cost" value={estimate ? fmtMoney(estimate.estimated_cost, estimate.currency) : "—"} strong />
              {estimate && <p className="text-xs text-slate-500">{fmtNum(estimate.eligible)} × {fmtMoney(estimate.rate, estimate.currency, 4)} per marketing message. Sending takes {fmtDuration(estimate.send_minutes)}.</p>}
            </div>
          </Card>
          <Card title="Test">
            <div className="flex gap-2 p-4">
              <Input placeholder="Your WhatsApp number" value={testPhone} onChange={(e) => setTestPhone(e.target.value)} dir="ltr" />
              <Button variant="secondary" onClick={sendTest} loading={busy === "test"} disabled={!testPhone || missing.filter((m) => m !== "audience").length > 0}>
                <Smartphone className="size-4" /> Send test
              </Button>
            </div>
          </Card>
          {missing.length > 0 && <p className="text-xs text-amber-700">Still needed: {missing.join(", ")}</p>}
          <div className="flex gap-2">
            <Button variant="secondary" className="flex-1" onClick={() => save()} loading={busy === "save"}>
              Save draft
            </Button>
            <Button className="flex-1" disabled={missing.length > 0 || !estimate?.eligible} onClick={() => setSendOpen(true)}>
              <Send className="size-4" /> Send or schedule
            </Button>
          </div>
        </div>
      </div>
      {sendOpen && estimate && <SendModal estimate={estimate} onClose={() => setSendOpen(false)} save={save} onSent={(cid) => nav(`/campaigns/${cid}`)} />}
    </>
  );
}

function Row({ label, value, strong }: { label: string; value: string; strong?: boolean }) {
  return (
    <div className="flex justify-between">
      <span className="text-slate-500">{label}</span>
      <span className={strong ? "font-semibold tabular-nums text-slate-900" : "tabular-nums"}>{value}</span>
    </div>
  );
}

function SendModal({ estimate, onClose, save, onSent }: { estimate: Estimate; onClose: () => void; save: (quiet?: boolean) => Promise<number | null>; onSent: (id: number) => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [mode, setMode] = useState<"now" | "later">("now");
  const [when, setWhen] = useState(qatarNowInput(60));
  const [busy, setBusy] = useState(false);

  const go = async () => {
    setBusy(true);
    try {
      const cid = await save(true);
      if (!cid) return;
      await api.post(`/campaigns/${cid}/send`, mode === "later" ? { scheduled_at: when } : {});
      qc.invalidateQueries({ queryKey: ["campaigns"] });
      toast.success(mode === "now" ? "Campaign is sending" : "Campaign scheduled");
      onSent(cid);
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
      title="Send campaign"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={go} loading={busy}>
            {mode === "now" ? (
              <>
                <Send className="size-4" /> Send to {fmtNum(estimate.eligible)} now
              </>
            ) : (
              <>
                <CalendarClock className="size-4" /> Schedule
              </>
            )}
          </Button>
        </>
      }
    >
      <div className="space-y-4 text-sm">
        <div className="grid grid-cols-2 gap-2">
          {(["now", "later"] as const).map((m) => (
            <label key={m} className={`flex cursor-pointer items-center gap-2 rounded-lg border p-3 ${mode === m ? "border-brand-600 bg-brand-50" : "border-slate-200"}`}>
              <input type="radio" checked={mode === m} onChange={() => setMode(m)} />
              {m === "now" ? "Send now" : "Schedule"}
            </label>
          ))}
        </div>
        {mode === "later" && (
          <Field label="Date and time (Qatar time)">
            <Input type="datetime-local" value={when} min={qatarNowInput()} onChange={(e) => setWhen(e.target.value)} />
          </Field>
        )}
        <div className="rounded-lg bg-slate-50 p-3">
          <p>
            <b>{fmtNum(estimate.eligible)}</b> recipients · estimated <b>{fmtMoney(estimate.estimated_cost, estimate.currency)}</b>
          </p>
          <p className="text-slate-500">Takes {fmtDuration(estimate.send_minutes)} at your Bird plan's sending speed. You can pause or cancel while it sends.</p>
        </div>
      </div>
    </Modal>
  );
}
