import { Link } from "react-router-dom";
import { api, type Paged } from "../api/client";
import { Badge, Button, Card, PageHeader, Skeleton, useAsync } from "../components/ui";
import { severityKind, statusKind, type CaseItem, type Employee, type Incident } from "../types";

export function Workbench() {
  const employees = useAsync<Paged<Employee>>(() => api("/employees"), []);
  const cases = useAsync<Paged<CaseItem>>(() => api("/cases?limit=8"), []);
  const incidents = useAsync<Paged<Incident>>(() => api("/incidents?limit=8&status=firing"), []);
  const waiting = useAsync<Paged<CaseItem>>(() => api("/cases?status=waiting_human&limit=1"), []);
  const pending = waiting.data?.total ?? 0;

  return (
    <div className="stack">
      <PageHeader title="值班工作台" description="先看事故和待人工案件，再进入案件详情处置。列表、详情、员工和评审已拆成独立页面。" />
      <div className="grid grid-4">
        <Summary label="值班员工" value={employees.data?.total ?? "—"} to="/employees" />
        <Summary label="近期案件" value={cases.data?.total ?? "—"} to="/cases" />
        <Summary label="Firing 事故" value={incidents.data?.total ?? "—"} to="/incidents" />
        <Summary label="等待人工" value={pending} to="/cases" />
      </div>

      <Card title="当前员工" actions={<Link className="btn" to="/employees">员工管理</Link>}>
        {employees.loading && !employees.data ? <Skeleton height={70} /> : null}
        {employees.data?.items.map((employee) => (
          <div className="row" key={employee.id} style={{ justifyContent: "space-between", padding: "8px 0", borderBottom: "1px solid var(--border)" }}>
            <div><div className="cell-main">{employee.name}</div><div className="cell-sub">{employee.title} · {employee.role}</div></div>
            <Badge kind={employee.status === "active" ? "ok" : "warn"}>{employee.status}</Badge>
          </div>
        ))}
      </Card>

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
      <div className="row" style={{ gap: 8 }}>
        <Button onClick={() => { cases.reload(); incidents.reload(); employees.reload(); waiting.reload(); }}>刷新</Button>
        <Link className="btn" to="/review">进入人工评审</Link>
      </div>
    </div>
  );
}

function Summary({ label, value, to }: { label: string; value: string | number; to: string }) {
  return <Link to={to} className="card" style={{ textDecoration: "none" }}><div className="stat-label">{label}</div><div className="stat-value">{value}</div></Link>;
}
