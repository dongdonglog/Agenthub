import { Link } from "react-router-dom";
import { api, type Paged } from "../api/client";
import { Badge, Card, PageHeader, Skeleton, useAsync } from "../components/ui";
import { severityKind, statusKind, type CaseItem, type Incident } from "../types";

export function Workbench() {
  const cases = useAsync<Paged<CaseItem>>(() => api("/cases?limit=8"), []);
  const incidents = useAsync<Paged<Incident>>(() => api("/incidents?limit=8&status=firing"), []);
  const reports = useAsync<{ total: number }>(() => api("/reports"), []);
  const open = useAsync<Paged<CaseItem>>(() => api("/cases?status=in_progress&limit=1"), []);
  const waiting = useAsync<Paged<CaseItem>>(() => api("/cases?status=waiting_human&limit=1"), []);

  return (
    <div className="stack">
      <PageHeader title="工作台" description="先处理 firing 事故和未闭环案件，再写处置报告。" />
      <div className="grid grid-4">
        <Summary label="处理中案件" value={open.data?.total ?? "—"} to="/cases" />
        <Summary label="等待人工" value={waiting.data?.total ?? "—"} to="/cases" />
        <Summary label="Firing 事故" value={incidents.data?.total ?? "—"} to="/incidents" />
        <Summary label="处置报告" value={reports.data?.total ?? "—"} to="/reports" />
      </div>
      <div className="grid grid-2">
        <Card title="最近案件" actions={<Link className="btn" to="/cases">全部案件</Link>}>
          {cases.loading && !cases.data ? <Skeleton height={120} /> : null}
          {cases.data?.items.length === 0 ? <div className="muted">暂无案件</div> : null}
          {cases.data?.items.map((item) => (
            <div className="row" key={item.id} style={{ justifyContent: "space-between", padding: "8px 0", borderBottom: "1px solid var(--border)" }}>
              <div><Link to={`/cases/${item.id}`} className="cell-main">{item.title}</Link><div className="cell-sub">{item.service || "—"} · {new Date(item.updated_at).toLocaleString()}</div></div>
              <Badge kind={statusKind(item.status)}>{item.status}</Badge>
            </div>
          ))}
        </Card>
        <Card title="Firing 事故" actions={<Link className="btn" to="/incidents">全部事故</Link>}>
          {incidents.loading && !incidents.data ? <Skeleton height={120} /> : null}
          {incidents.data?.items.length === 0 ? <div className="muted">暂无 firing 事故</div> : null}
          {incidents.data?.items.map((incident) => (
            <div className="row" key={incident.id} style={{ justifyContent: "space-between", padding: "8px 0", borderBottom: "1px solid var(--border)" }}>
              <div><Link to={`/incidents/${incident.id}`} className="cell-main">{incident.title}</Link><div className="cell-sub">{incident.service || "—"} · 告警 {incident.alert_count}</div></div>
              <Badge kind={severityKind(incident.severity)}>{incident.severity || "—"}</Badge>
            </div>
          ))}
        </Card>
      </div>
    </div>
  );
}

function Summary({ label, value, to }: { label: string; value: string | number; to: string }) {
  return <Link to={to} className="card" style={{ textDecoration: "none" }}><div className="stat-label">{label}</div><div className="stat-value">{value}</div></Link>;
}
