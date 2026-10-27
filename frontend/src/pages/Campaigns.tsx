import { useState } from "react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Megaphone, Plus } from "lucide-react";
import { api } from "../lib/api";
import type { CampaignWithStats } from "../lib/types";
import { fmtDateTime, fmtMoney, fmtNum, pct } from "../lib/format";
import { Button, Card, EmptyState, Loading, PageHeader, Select, StatusBadge, td, th } from "../components/ui";

export default function Campaigns() {
  const [status, setStatus] = useState("");
  const { data, isLoading } = useQuery({
    queryKey: ["campaigns"],
    queryFn: () => api.get<CampaignWithStats[]>("/campaigns"),
    refetchInterval: (q) => (q.state.data?.some((c) => c.status === "sending" || c.status === "scheduled") ? 5000 : false),
  });
  const rows = (data ?? []).filter((c) => !status || c.status === status);

  return (
    <>
      <PageHeader
        title="Campaigns"
        actions={
          <>
            <Select value={status} onChange={(e) => setStatus(e.target.value)} className="w-auto">
              <option value="">All statuses</option>
              {["draft", "scheduled", "sending", "paused", "completed", "cancelled"].map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </Select>
            <Link to="/campaigns/new">
              <Button>
                <Plus className="size-4" /> New campaign
              </Button>
            </Link>
          </>
        }
      />
      <Card>
        {isLoading ? (
          <Loading />
        ) : rows.length === 0 ? (
          <EmptyState icon={<Megaphone className="size-10" />} title="No campaigns">
            Pick an approved template, choose your groups and send or schedule.
          </EmptyState>
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-slate-100">
              <thead>
                <tr>
                  <th className={th}>Campaign</th>
                  <th className={th}>Status</th>
                  <th className={th}>When</th>
                  <th className={`${th} text-right`}>Recipients</th>
                  <th className={`${th} text-right`}>Delivered</th>
                  <th className={`${th} text-right`}>Read</th>
                  <th className={`${th} text-right`}>Clicked</th>
                  <th className={`${th} text-right`}>Failed</th>
                  <th className={`${th} text-right`}>Cost</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {rows.map((c) => {
                  const s = c.stats;
                  const progress = s.recipients ? Math.round(((s.recipients - s.queued) / s.recipients) * 100) : 0;
                  return (
                    <tr key={c.id} className="hover:bg-slate-50">
                      <td className={td}>
                        <Link to={c.status === "draft" ? `/campaigns/${c.id}/edit` : `/campaigns/${c.id}`} className="font-medium text-slate-900 hover:text-brand-600">
                          {c.name}
                        </Link>
                        <div className="font-mono text-xs text-slate-400">{c.template_slug}</div>
                      </td>
                      <td className={td}>
                        <StatusBadge status={c.status} />
                        {c.status === "sending" && (
                          <div className="mt-1 h-1 w-20 overflow-hidden rounded bg-slate-200">
                            <div className="h-full bg-brand-600" style={{ width: `${progress}%` }} />
                          </div>
                        )}
                      </td>
                      <td className={`${td} whitespace-nowrap text-slate-500`}>{fmtDateTime(c.started_at ?? c.scheduled_at ?? c.created_at)}</td>
                      <td className={`${td} text-right tabular-nums`}>{fmtNum(s.recipients)}</td>
                      <td className={`${td} text-right tabular-nums`}>
                        {fmtNum(s.delivered)} <span className="text-xs text-slate-400">{pct(s.delivered, s.sent)}</span>
                      </td>
                      <td className={`${td} text-right tabular-nums`}>{fmtNum(s.read)}</td>
                      <td className={`${td} text-right tabular-nums`}>{fmtNum(s.clicked)}</td>
                      <td className={`${td} text-right tabular-nums ${s.failed ? "text-red-600" : ""}`}>{fmtNum(s.failed)}</td>
                      <td className={`${td} text-right tabular-nums`}>{s.priced_count ? fmtMoney(s.actual_cost, s.cost_currency) : "—"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  );
}
