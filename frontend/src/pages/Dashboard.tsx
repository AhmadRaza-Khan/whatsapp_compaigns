import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Megaphone, Plus } from "lucide-react";
import { api } from "../lib/api";
import type { Dashboard as DashboardData, Stats } from "../lib/types";
import { fmtDateTime, fmtMoney, fmtNum, pct } from "../lib/format";
import { Button, Card, EmptyState, Loading, PageHeader, StatCard, StatusBadge, td, th } from "../components/ui";

function Funnel({ s, currency }: { s: Stats; currency: string }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      <StatCard label="Sent" value={fmtNum(s.sent)} sub={`${fmtNum(s.recipients)} recipients`} />
      <StatCard label="Delivered" value={fmtNum(s.delivered)} sub={pct(s.delivered, s.sent)} accent="text-emerald-700" />
      <StatCard label="Read" value={fmtNum(s.read)} sub={pct(s.read, s.delivered)} accent="text-emerald-700" />
      <StatCard label="Clicked" value={fmtNum(s.clicked)} sub={pct(s.clicked, s.delivered)} accent="text-blue-700" />
      <StatCard label="Failed" value={fmtNum(s.failed)} sub={pct(s.failed, s.recipients)} accent={s.failed ? "text-red-600" : undefined} />
      <StatCard label="Cost" value={fmtMoney(s.actual_cost, s.cost_currency || currency)} sub={`${fmtNum(s.priced_count)} priced`} />
    </div>
  );
}

export default function Dashboard() {
  const { data, isLoading } = useQuery({ queryKey: ["dashboard"], queryFn: () => api.get<DashboardData>("/dashboard"), refetchInterval: 30_000 });
  if (isLoading || !data) return <Loading />;

  const active = (data.campaigns_by_status.sending ?? 0) + (data.campaigns_by_status.scheduled ?? 0);
  return (
    <>
      <PageHeader
        title="Dashboard"
        description="Totals across all campaigns"
        actions={
          <Link to="/campaigns/new">
            <Button>
              <Plus className="size-4" /> New campaign
            </Button>
          </Link>
        }
      />
      {!data.bird_configured && (
        <div className="mb-6 flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800">
          <AlertTriangle className="size-5 shrink-0" />
          The Bird API key is not configured on the server, so messages cannot be sent yet. Ask your administrator to set it up.
        </div>
      )}
      <div className="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard label="Contacts" value={fmtNum(data.contacts.total)} sub={`${fmtNum(data.contacts.groups)} groups`} />
        <StatCard label="Opted out" value={fmtNum(data.contacts.opted_out)} sub={pct(data.contacts.opted_out, data.contacts.total)} />
        <StatCard label="Active campaigns" value={fmtNum(active)} sub={`${data.campaigns_by_status.scheduled ?? 0} scheduled`} />
        <StatCard label="Replies (30 days)" value={fmtNum(data.replies_30_days)} />
      </div>

      <h2 className="mb-2 text-sm font-semibold text-slate-700">Last 30 days</h2>
      <div className="mb-6">
        <Funnel s={data.last_30_days} currency={data.currency} />
      </div>
      <h2 className="mb-2 text-sm font-semibold text-slate-700">All time</h2>
      <div className="mb-8">
        <Funnel s={data.all_time} currency={data.currency} />
      </div>

      <Card title="Recent campaigns" actions={<Link to="/campaigns" className="text-sm text-brand-600 hover:underline">View all</Link>}>
        {data.recent_campaigns.length === 0 ? (
          <EmptyState icon={<Megaphone className="size-10" />} title="No campaigns sent yet">
            Create your first campaign to see results here.
          </EmptyState>
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-slate-100">
              <thead>
                <tr>
                  <th className={th}>Campaign</th>
                  <th className={th}>Status</th>
                  <th className={th}>Started</th>
                  <th className={`${th} text-right`}>Delivered</th>
                  <th className={`${th} text-right`}>Read</th>
                  <th className={`${th} text-right`}>Clicked</th>
                  <th className={`${th} text-right`}>Cost</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {data.recent_campaigns.map((c) => (
                  <tr key={c.id} className="hover:bg-slate-50">
                    <td className={td}>
                      <Link to={`/campaigns/${c.id}`} className="font-medium text-slate-900 hover:text-brand-600">
                        {c.name}
                      </Link>
                    </td>
                    <td className={td}>
                      <StatusBadge status={c.status} />
                    </td>
                    <td className={`${td} text-slate-500`}>{fmtDateTime(c.started_at ?? c.scheduled_at)}</td>
                    <td className={`${td} text-right tabular-nums`}>
                      {fmtNum(c.stats.delivered)} <span className="text-slate-400">/ {fmtNum(c.stats.recipients)}</span>
                    </td>
                    <td className={`${td} text-right tabular-nums`}>{fmtNum(c.stats.read)}</td>
                    <td className={`${td} text-right tabular-nums`}>{fmtNum(c.stats.clicked)}</td>
                    <td className={`${td} text-right tabular-nums`}>{fmtMoney(c.stats.actual_cost, c.stats.cost_currency || data.currency)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  );
}
