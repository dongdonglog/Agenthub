import { useState } from "react";
import { Link } from "react-router-dom";
import { api, qs, type Paged } from "../api/client";
import { Badge, Button, Card, Empty, Input, PageHeader, Pagination, Select, Skeleton, useAsync } from "../components/ui";
import { TimeFilter } from "../components/TimeFilter";
import { severityKind, statusKind, type Incident } from "../types";

const PAGE_SIZE = 20;

export function Incidents() {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const incidents = useAsync<Paged<Incident>>(
    () => api(`/incidents${qs({ q, status, from, to, limit: PAGE_SIZE, offset: (page - 1) * PAGE_SIZE })}`),
    [q, status, from, to, page],
  );
  const reset = (setter: (value: string) => void) => (value: string) => { setter(value); setPage(1); };

  return (
    <div className="stack">
      <PageHeader title="运维事故" description="Alertmanager 聚合后的事故；点开查看告警、诊断、关联案件和时间线。" />
      <Card>
        <div className="row" style={{ gap: 10, flexWrap: "wrap", marginBottom: 14 }}>
          <Input placeholder="搜索标题 / 服务 / 摘要" value={q} onChange={(event) => reset(setQ)(event.target.value)} style={{ maxWidth: 320 }} />
          <Select value={status} onChange={(event) => reset(setStatus)(event.target.value)}>
            <option value="">全部状态</option><option value="firing">firing</option><option value="acknowledged">acknowledged</option><option value="resolved">resolved</option>
          </Select>
          <TimeFilter from={from} to={to} onChange={(nextFrom, nextTo) => { setFrom(nextFrom); setTo(nextTo); setPage(1); }} />
          <Button onClick={() => incidents.reload()}>刷新</Button>
        </div>
        {incidents.loading && !incidents.data ? <Skeleton height={240} /> : null}
        {incidents.error ? <div className="danger-text">{incidents.error}</div> : null}
        {incidents.data && incidents.data.items.length === 0 ? <Empty icon="🚨" text="没有匹配的事故" /> : null}
        {incidents.data && incidents.data.items.length > 0 ? (
          <>
            <div className="table-wrap">
              <table>
                <thead><tr><th>状态</th><th>级别</th><th>事故</th><th>服务</th><th>告警数</th><th>认领</th><th>更新时间</th></tr></thead>
                <tbody>
                  {incidents.data.items.map((incident) => (
                    <tr key={incident.id}>
                      <td><Badge kind={statusKind(incident.status)}>{incident.status}</Badge></td>
                      <td><Badge kind={severityKind(incident.severity)}>{incident.severity || "—"}</Badge></td>
                      <td><Link className="cell-main" to={`/incidents/${incident.id}`}>{incident.title}</Link><div className="cell-sub mono">{incident.id}</div></td>
                      <td>{incident.service || "—"}<div className="cell-sub">{incident.environment}</div></td>
                      <td>{incident.alert_count}</td>
                      <td className="muted">{incident.acknowledged_by || "—"}</td>
                      <td className="nowrap muted">{new Date(incident.updated_at).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination page={page} size={PAGE_SIZE} total={incidents.data.total} onChange={setPage} />
          </>
        ) : null}
      </Card>
    </div>
  );
}
