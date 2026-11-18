import { Link } from "react-router-dom";
import { api, type Paged } from "../api/client";
import { Badge, Card, PageHeader, Skeleton, useAsync } from "../components/ui";
import { statusKind, type CaseItem, type Incident } from "../types";

export function Workbench() {
  const cases = useAsync<Paged<CaseItem>>(() => api("/cases?limit=8"), []);
  // 告警聚合数量仍来自 incident（去重与计数的事实源），但入口统一收敛到案件。
  const firing = useAsync<Paged<Incident>>(() => api("/incidents?limit=1&status=firing"), []);
  const reports = useAsync<{ total: number }>(() => api("/reports"), []);
  const open = useAsync<Paged<CaseItem>>(() => api("/cases?status=in_progress&limit=1"), []);
  const waiting = useAsync<Paged<CaseItem>>(() => api("/cases?status=waiting_human&limit=8"), []);

  return (
    <div className="stack">
      <PageHeader title="工作台" description="先处理未闭环案件和等待人工的项，再写处置报告。" />
      <div className="grid grid-4">
        <Summary label="处理中案件" value={open.data?.total ?? "—"} to="/cases" />
        <Summary label="等待人工" value={waiting.data?.total ?? "—"} to="/cases" />
        <Summary label="Firing 告警组" value={firing.data?.total ?? "—"} to="/cases?type=incident" />
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
        <Card title="等待人工" actions={<Link className="btn" to="/cases">全部案件</Link>}>
          {waiting.loading && !waiting.data ? <Skeleton height={120} /> : null}
          {waiting.data?.items.length === 0 ? <div className="muted">没有等待人工的项</div> : null}
          {waiting.data?.items.map((item) => (
            <div className="row" key={item.id} style={{ justifyContent: "space-between", padding: "8px 0", borderBottom: "1px solid var(--border)" }}>
              <div><Link to={`/cases/${item.id}`} className="cell-main">{item.title}</Link><div className="cell-sub">{item.service || "—"} · {item.type}</div></div>
              <Badge kind={statusKind(item.status)}>{item.status}</Badge>
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
