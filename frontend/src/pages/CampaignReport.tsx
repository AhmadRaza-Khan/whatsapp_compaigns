import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Ban, CalendarX, Copy, Download, Pause, Play, Search } from "lucide-react";
import { api, qs } from "../lib/api";
import type { CampaignDetail, Page, Recipient, Reply } from "../lib/types";
import { fmtDateTime, fmtMoney, fmtNum, pct } from "../lib/format";
import TemplatePreview from "../components/TemplatePreview";
import { Badge, Button, Card, EmptyState, Input, Loading, PageHeader, Pagination, StatCard, StatusBadge, cx, td, th, useToast } from "../components/ui";

const filters = [
  ["", "All"],
  ["delivered", "Delivered"],
  ["read", "Read"],
  ["clicked", "Clicked"],
  ["replied", "Replied"],
  ["failed", "Failed"],
  ["skipped", "Skipped"],
  ["queued", "Queued"],
] as const;

export default function CampaignReport() {
  const { id } = useParams();
  const nav = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const [tab, setTab] = useState<"recipients" | "replies">("recipients");
  const [status, setStatus] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);

  const detail = useQuery({
    queryKey: ["campaign", id],
    queryFn: () => api.get<CampaignDetail>(`/campaigns/${id}`),
    refetchInterval: (query) => (["sending", "scheduled"].includes(query.state.data?.campaign.status ?? "") ? 4000 : 30_000),
  });
  const live = ["sending", "scheduled"].includes(detail.data?.campaign.status ?? "");
  const recipients = useQuery({
    queryKey: ["recipients", id, status, q, page],
    queryFn: () => api.get<Page<Recipient>>(`/campaigns/${id}/recipients${qs({ status, q, page, page_size: 50 })}`),
    placeholderData: keepPreviousData,
    refetchInterval: live ? 5000 : false,
    enabled: tab === "recipients",
  });
  const replies = useQuery({ queryKey: ["replies", id], queryFn: () => api.get<Reply[]>(`/campaigns/${id}/replies`), enabled: tab === "replies" });

  const act = useMutation({
    mutationFn: (a: string) => api.post<{ id?: number }>(`/campaigns/${id}/${a}`),
    onSuccess: (r, a) => {
      if (a === "duplicate" && r.id) {
        nav(`/campaigns/${r.id}/edit`);
        return;
      }
      if (a === "unschedule") {
        nav(`/campaigns/${id}/edit`);
        return;
      }
      qc.invalidateQueries({ queryKey: ["campaign", id] });
      qc.invalidateQueries({ queryKey: ["campaigns"] });
      toast.success("Done");
    },
    onError: toast.error,
  });

  if (detail.isLoading || !detail.data) return <Loading />;
  const { campaign: c, stats: s, template } = detail.data;
  const currency = s.cost_currency || detail.data.currency;
  const estimated = detail.data.estimated_cost ?? detail.data.estimate?.estimated_cost ?? 0;
  const done = s.recipients - s.queued;

  return (
    <>
      <PageHeader
        title={c.name}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <StatusBadge status={c.status} />
            <span className="font-mono text-xs">{c.template_slug}</span>
            {c.scheduled_at && c.status === "scheduled" && <span>Starts {fmtDateTime(c.scheduled_at)} (Qatar)</span>}
            {c.started_at && <span>Started {fmtDateTime(c.started_at)}</span>}
            {c.completed_at && <span>· Finished {fmtDateTime(c.completed_at)}</span>}
          </span>
        }
        actions={
          <>
            {c.status === "sending" && (
              <Button variant="secondary" onClick={() => act.mutate("pause")} loading={act.isPending}>
                <Pause className="size-4" /> Pause
              </Button>
            )}
            {c.status === "paused" && (
              <Button onClick={() => act.mutate("resume")} loading={act.isPending}>
                <Play className="size-4" /> Resume
              </Button>
            )}
            {c.status === "scheduled" && (
              <Button variant="secondary" onClick={() => act.mutate("unschedule")} loading={act.isPending}>
                <CalendarX className="size-4" /> Unschedule & edit
              </Button>
            )}
            {["scheduled", "sending", "paused"].includes(c.status) && (
              <Button variant="danger" onClick={() => window.confirm("Cancel this campaign? Messages not yet sent will be dropped.") && act.mutate("cancel")}>
                <Ban className="size-4" /> Cancel
              </Button>
            )}
            <Button variant="secondary" onClick={() => act.mutate("duplicate")}>
              <Copy className="size-4" /> Duplicate
            </Button>
            <a href={`/api/campaigns/${id}/export.csv`}>
              <Button variant="secondary">
                <Download className="size-4" /> Export CSV
              </Button>
            </a>
          </>
        }
      />

      {c.status_reason && ["paused", "completed"].includes(c.status) && c.status_reason !== "Paused by user" && (
        <div className="mb-4 flex items-start gap-2 rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
          <AlertTriangle className="size-5 shrink-0" /> {c.status_reason}
        </div>
      )}

      {(c.status === "sending" || c.status === "paused") && s.recipients > 0 && (
        <div className="mb-6">
          <div className="mb-1 flex justify-between text-xs text-slate-500">
            <span>
              {fmtNum(done)} of {fmtNum(s.recipients)} processed
            </span>
            <span>{pct(done, s.recipients)}</span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-slate-200">
            <div className="h-full rounded-full bg-brand-600 transition-all" style={{ width: pct(done, s.recipients) }} />
          </div>
        </div>
      )}

      <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-7">
        <StatCard label="Recipients" value={fmtNum(s.recipients || detail.data.estimate?.eligible || 0)} sub={s.skipped ? `${s.skipped} skipped` : undefined} />
        <StatCard label="Sent" value={fmtNum(s.sent)} sub={s.queued ? `${fmtNum(s.queued)} queued` : pct(s.sent, s.recipients)} />
        <StatCard label="Delivered" value={fmtNum(s.delivered)} sub={pct(s.delivered, s.sent)} accent="text-brand-700" />
        <StatCard label="Read" value={fmtNum(s.read)} sub={pct(s.read, s.delivered)} accent="text-brand-700" />
        <StatCard label="Clicked" value={fmtNum(s.clicked)} sub={`${fmtNum(s.clicks)} clicks · ${pct(s.clicked, s.delivered)}`} accent="text-blue-700" />
        <StatCard label="Replies" value={fmtNum(s.replied)} />
        <StatCard label="Failed" value={fmtNum(s.failed)} sub={pct(s.failed, s.recipients)} accent={s.failed ? "text-red-600" : undefined} />
      </div>

      <div className="mb-6 grid gap-4 lg:grid-cols-3">
        <Card title="Cost">
          <div className="space-y-2 p-4 text-sm">
            <div className="flex justify-between">
              <span className="text-slate-500">Estimated</span>
              <span className="tabular-nums">{fmtMoney(estimated, currency)}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-500">Actual (from Bird)</span>
              <span className="font-semibold tabular-nums">{fmtMoney(s.actual_cost, currency)}</span>
            </div>
            <p className="text-xs text-slate-500">
              {fmtNum(s.priced_count)} of {fmtNum(s.accepted)} messages priced so far. Bird prices each message shortly after it is sent.
            </p>
          </div>
        </Card>
        <Card title="Failure reasons">
          <div className="p-4 text-sm">
            {(detail.data.failure_reasons ?? []).length === 0 ? (
              <p className="text-slate-500">No failures.</p>
            ) : (
              <ul className="space-y-1.5">
                {detail.data.failure_reasons!.map((r) => (
                  <li key={r.reason} className="flex justify-between gap-3">
                    <span className="text-slate-600">{r.reason || "Unknown"}</span>
                    <span className="tabular-nums font-medium">{r.count}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </Card>
        {template && (
          <Card title="Message">
            <div className="p-4">
              <TemplatePreview spec={template.spec} values={c.variables} />
            </div>
          </Card>
        )}
      </div>

      <Card>
        <div className="flex gap-4 border-b border-slate-100 px-5">
          {(["recipients", "replies"] as const).map((t) => (
            <button key={t} onClick={() => setTab(t)} className={cx("-mb-px border-b-2 py-3 text-sm font-medium capitalize", tab === t ? "border-brand-600 text-brand-700" : "border-transparent text-slate-500 hover:text-slate-700")}>
              {t}
            </button>
          ))}
        </div>
        {tab === "recipients" ? (
          <>
            <div className="flex flex-wrap items-center gap-2 border-b border-slate-100 p-3">
              <div className="flex flex-wrap gap-1">
                {filters.map(([v, l]) => (
                  <button
                    key={v}
                    onClick={() => {
                      setStatus(v);
                      setPage(1);
                    }}
                    className={cx("rounded-lg px-2.5 py-1 text-xs font-medium", status === v ? "bg-brand-600 text-white" : "bg-slate-100 text-slate-600 hover:bg-slate-200")}
                  >
                    {l}
                  </button>
                ))}
              </div>
              <div className="relative ml-auto w-56">
                <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-slate-400" />
                <Input
                  placeholder="Phone or name"
                  value={q}
                  onChange={(e) => {
                    setQ(e.target.value);
                    setPage(1);
                  }}
                  className="pl-9"
                />
              </div>
            </div>
            {recipients.isLoading ? (
              <Loading />
            ) : !recipients.data?.items.length ? (
              <EmptyState title={s.recipients ? "No recipients match" : "Recipients appear when the campaign starts"} />
            ) : (
              <div className="overflow-x-auto">
                <table className="min-w-full divide-y divide-slate-100">
                  <thead>
                    <tr>
                      <th className={th}>Contact</th>
                      <th className={th}>Status</th>
                      <th className={th}>Delivered</th>
                      <th className={th}>Read</th>
                      <th className={th}>Clicked</th>
                      <th className={th}>Replied</th>
                      <th className={`${th} text-right`}>Cost</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100">
                    {recipients.data.items.map((r) => (
                      <tr key={r.id} className="align-top hover:bg-slate-50">
                        <td className={td}>
                          <div className="font-medium text-slate-900">{r.name || "—"}</div>
                          <div className="text-xs tabular-nums text-slate-500" dir="ltr">
                            {r.phone}
                          </div>
                        </td>
                        <td className={td}>
                          <StatusBadge status={r.status} />
                          {r.error_reason && <div className="mt-1 max-w-xs text-xs text-red-600">{r.error_reason}</div>}
                        </td>
                        <td className={`${td} whitespace-nowrap text-slate-500`}>{fmtDateTime(r.delivered_at)}</td>
                        <td className={`${td} whitespace-nowrap text-slate-500`}>{fmtDateTime(r.read_at)}</td>
                        <td className={`${td} whitespace-nowrap text-slate-500`}>
                          {r.clicked_at ? (
                            <>
                              {fmtDateTime(r.clicked_at)}
                              {r.click_count > 1 && <Badge color="blue">×{r.click_count}</Badge>}
                            </>
                          ) : (
                            "—"
                          )}
                        </td>
                        <td className={`${td} whitespace-nowrap text-slate-500`}>{fmtDateTime(r.replied_at)}</td>
                        <td className={`${td} text-right tabular-nums text-slate-500`}>{r.cost != null ? fmtMoney(r.cost, currency, 4) : "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {recipients.data && <Pagination page={page} pageSize={recipients.data.page_size} total={recipients.data.total} onChange={setPage} />}
          </>
        ) : replies.isLoading ? (
          <Loading />
        ) : !replies.data?.length ? (
          <EmptyState title="No replies yet">Customers' replies to this campaign appear here. Answer them from the WhatsApp Business app.</EmptyState>
        ) : (
          <ul className="divide-y divide-slate-100">
            {replies.data.map((r) => (
              <li key={r.id} className="flex gap-4 px-5 py-3">
                <div className="w-44 shrink-0">
                  <p className="text-sm font-medium text-slate-900">{r.name || r.phone}</p>
                  <p className="text-xs text-slate-500">{fmtDateTime(r.received_at)}</p>
                </div>
                <p className="flex-1 whitespace-pre-wrap text-sm text-slate-700" dir="auto">
                  {r.text || <span className="text-slate-400">(media or unsupported message)</span>}
                </p>
                {r.is_opt_out && <Badge color="red">opted out</Badge>}
              </li>
            ))}
          </ul>
        )}
      </Card>
      <p className="mt-4 text-xs text-slate-400">
        <Link to="/campaigns" className="hover:underline">
          ← All campaigns
        </Link>
      </p>
    </>
  );
}
