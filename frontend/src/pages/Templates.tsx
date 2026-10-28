import { useMemo, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, FileText, MousePointerClick, Plus, RefreshCw } from "lucide-react";
import { api } from "../lib/api";
import type { Template, TemplateSpec } from "../lib/types";
import { fmtDateTime } from "../lib/format";
import { useAuth } from "../auth";
import TemplatePreview from "../components/TemplatePreview";
import ImageUpload from "../components/ImageUpload";
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Modal, PageHeader, Select, StatusBadge, Textarea, useToast } from "../components/ui";

export default function Templates() {
  const { user } = useAuth();
  const toast = useToast();
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ["templates"], queryFn: () => api.get<Template[]>("/templates") });
  const [creating, setCreating] = useState(false);

  const sync = useMutation({
    mutationFn: () => api.post<{ synced: number }>("/templates/sync"),
    onSuccess: (r) => {
      toast.success(`Synced ${r.synced} templates from Bird`);
      qc.invalidateQueries({ queryKey: ["templates"] });
    },
    onError: toast.error,
  });

  return (
    <>
      <PageHeader
        title="Templates"
        description="Messages approved by WhatsApp. Campaigns can only use approved templates."
        actions={
          <>
            <Button variant="secondary" onClick={() => sync.mutate()} loading={sync.isPending}>
              <RefreshCw className="size-4" /> Sync from Bird
            </Button>
            {user?.role === "admin" && (
              <Button onClick={() => setCreating(true)}>
                <Plus className="size-4" /> New template
              </Button>
            )}
          </>
        }
      />
      {isLoading ? (
        <Loading />
      ) : !data?.length ? (
        <Card>
          <EmptyState icon={<FileText className="size-10" />} title="No templates yet">
            Click "Sync from Bird" to load your approved templates, or create a new one.
          </EmptyState>
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {data.map((t) => (
            <Card key={t.slug} className="flex flex-col">
              <div className="flex items-start justify-between gap-2 border-b border-slate-100 px-4 py-3">
                <div className="min-w-0">
                  <p className="truncate font-semibold text-slate-900">{t.name || t.slug}</p>
                  <p className="truncate font-mono text-xs text-slate-500">{t.slug}</p>
                </div>
                <StatusBadge status={t.status} />
              </div>
              <div className="flex-1 p-4">
                <TemplatePreview spec={t.spec} />
              </div>
              <div className="flex flex-wrap items-center gap-1.5 border-t border-slate-100 px-4 py-2.5 text-xs text-slate-500">
                <Badge color={t.category === "marketing" ? "purple" : "blue"}>{t.category}</Badge>
                {Object.entries(t.languages ?? {}).map(([lang, l]) => (
                  <span key={lang} title={`${lang}: ${l.status}`}>
                    <Badge color={l.status === "approved" ? "green" : l.status === "rejected" ? "red" : "yellow"}>
                      {lang} · {l.status}
                    </Badge>
                  </span>
                ))}
                {t.spec.buttons?.some((b) => b.tracked) && (
                  <span title="Shop Now clicks are tracked">
                    <Badge color="green">
                      <MousePointerClick className="mr-1 size-3" /> click tracking
                    </Badge>
                  </span>
                )}
                <span className="ml-auto">Synced {fmtDateTime(t.synced_at)}</span>
              </div>
            </Card>
          ))}
        </div>
      )}
      {creating && <CreateTemplateModal onClose={() => setCreating(false)} />}
    </>
  );
}

function CreateTemplateModal({ onClose }: { onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["settings"], queryFn: () => api.get<{ settings: { bird_dashboard_url: string; bird_waba_id: string } }>("/settings") });
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [category, setCategory] = useState("marketing");
  const [language, setLanguage] = useState("en");
  const [headerImage, setHeaderImage] = useState("");
  const [body, setBody] = useState("Hi {{1}}, our new {{2}} collection has arrived! {{3}}");
  const [examples, setExamples] = useState<string[]>(["Sara", "Winter", "Enjoy 20% off this week."]);
  const [footer, setFooter] = useState("Reply STOP to unsubscribe");
  const [shopButton, setShopButton] = useState("Shop Now");
  const [stopButton, setStopButton] = useState(true);
  const [busy, setBusy] = useState(false);

  const varCount = useMemo(() => Math.max(0, ...[...body.matchAll(/\{\{(\d+)\}\}/g)].map((m) => Number(m[1]))), [body]);
  const spec: TemplateSpec = {
    header_format: headerImage ? "image" : "",
    header_example_url: headerImage,
    body,
    body_params: Array.from({ length: varCount }, (_, i) => ({ example: examples[i] ?? "" })),
    footer,
    buttons: [...(shopButton ? [{ type: "url", text: shopButton, tracked: true }] : []), ...(stopButton ? [{ type: "quick_reply", text: "Stop promotions", tracked: false }] : [])],
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post("/templates", {
        slug,
        name,
        category,
        language,
        header_image_url: headerImage,
        body,
        body_examples: examples.slice(0, varCount),
        footer,
        shop_button_text: shopButton,
        stop_button: stopButton,
      });
      toast.success("Template submitted to WhatsApp for approval. Approval usually takes minutes to a day.");
      qc.invalidateQueries({ queryKey: ["templates"] });
      onClose();
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(false);
    }
  };

  const birdURL = settings.data?.settings.bird_dashboard_url;
  return (
    <Modal
      open
      onClose={onClose}
      title="New template"
      wide="xl"
      footer={
        <>
          {birdURL && (
            <a href={birdURL} target="_blank" rel="noreferrer" className="mr-auto">
              <Button variant="ghost">
                <ExternalLink className="size-4" /> Open Bird's template editor
              </Button>
            </a>
          )}
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="tpl-form" loading={busy}>
            Submit for approval
          </Button>
        </>
      }
    >
      <div className="grid gap-6 md:grid-cols-[1fr_400px]">
        <form id="tpl-form" onSubmit={submit} className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Template ID" hint="lowercase_with_underscores">
              <Input required value={slug} onChange={(e) => setSlug(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, "_"))} placeholder="winter_launch" />
            </Field>
            <Field label="Display name">
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Winter launch" />
            </Field>
            <Field label="Category">
              <Select value={category} onChange={(e) => setCategory(e.target.value)}>
                <option value="marketing">Marketing (promotions)</option>
                <option value="utility">Utility (order / appointment updates)</option>
              </Select>
            </Field>
            <Field label="Language">
              <Select value={language} onChange={(e) => setLanguage(e.target.value)}>
                <option value="en">English</option>
                <option value="ar">Arabic</option>
              </Select>
            </Field>
          </div>
          <div>
            <ImageUpload label="Header image" optional value={headerImage} onChange={setHeaderImage} />
            <p className="mt-1 text-xs text-slate-500">A sample WhatsApp reviews. Each campaign uploads its own image.</p>
          </div>
          <Field label="Message" hint="Use {{1}}, {{2}}… for parts that change per campaign (customer name, collection, offer). *bold* and _italic_ work.">
            <Textarea rows={4} required value={body} onChange={(e) => setBody(e.target.value)} dir="auto" />
          </Field>
          {varCount > 0 && (
            <div className="grid gap-2 sm:grid-cols-2">
              {Array.from({ length: varCount }, (_, i) => (
                <Field key={i} label={`Example for {{${i + 1}}}`}>
                  <Input
                    required
                    value={examples[i] ?? ""}
                    onChange={(e) => {
                      const n = [...examples];
                      n[i] = e.target.value;
                      setExamples(n);
                    }}
                  />
                </Field>
              ))}
            </div>
          )}
          <Field label="Footer (optional)">
            <Input value={footer} onChange={(e) => setFooter(e.target.value)} maxLength={60} />
          </Field>
          <Field label="Shop button text" hint="You add the shop link in each campaign. Leave empty for no button.">
            <Input value={shopButton} onChange={(e) => setShopButton(e.target.value)} maxLength={25} />
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={stopButton} onChange={(e) => setStopButton(e.target.checked)} />
            Add a "Stop promotions" quick-reply button (counts as opt-out)
          </label>
        </form>
        <div>
          <div className="md:sticky md:top-0">
            <p className="mb-2 text-sm font-medium text-slate-700">Preview</p>
            <TemplatePreview spec={spec} size="lg" />
          </div>
        </div>
      </div>
    </Modal>
  );
}
