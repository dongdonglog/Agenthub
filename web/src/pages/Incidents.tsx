import { useState } from "react";
import { api, type Paged } from "../api/client";
import { Badge, Button, Card, PageHeader, Skeleton, useAsync, useToast } from "../components/ui";

type Incident = {
  id: string; fingerprint: string; status: string; severity: string; title: string; summary: string;
  service: string; environment: string; chat_id: string; alert_count: number; acknowledged_by: string;
  created_at: string; updated_at: string; resolved_at?: string;
};
type Event = { id: number; kind: string; actor: string; text: string; created_at: string };
type Detail = { incident: Incident; events: Event[] };

const severityKind = (severity: string) => severity === "critical" || severity === "page" ? "danger" : severity === "warning" ? "warn" : "";
const statusKind = (status: string) => status === "resolved" ? "ok" : status === "acknowledged" ? "brand" : "danger";

export function Incidents() {
  const { data, loading, error, reload } = useAsync<Paged<Incident>>(() => api("/incidents"), []);
  const [selected, setSelected] = useState("");
  const { show } = useToast();
  const detail = useAsync<Detail>(
    () => selected ? api(`/incidents/${encodeURIComponent(selected)}`) : Promise.resolve({ incident: null as unknown as Incident, events: [] }),
    [selected],
  );

  const ack = async (id: string) => {
    try {
      await api(`/incidents/${encodeURIComponent(id)}/ack`, { method: "POST", body: JSON.stringify({ actor: "admin" }) });
      show("已认领");
      reload();
      if (selected === id) detail.reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  return (
    <div className="stack">
      <PageHeader title="运维事故" description="Alertmanager 告警聚合后的事故、时间线与 AI 诊断。" />
      <Card>
        {loading && !data ? <Skeleton height={220} /> : null}
        {error ? <div className="danger-text">{error}</div> : null}
        {data && data.items.length === 0 ? <div className="empty"><div className="empty-icon">🚨</div><div>还没有事故</div></div> : null}
        {data && data.items.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead><tr><th>状态</th><th>级别</th><th>事故</th><th>服务</th><th>告警数</th><th>更新时间</th><th /></tr></thead>
              <tbody>
                {data.items.map((incident) => (
                  <tr key={incident.id}>
                    <td><Badge kind={statusKind(incident.status)}>{incident.status}</Badge></td>
                    <td><Badge kind={severityKind(incident.severity)}>{incident.severity || "—"}</Badge></td>
                    <td>
                      <div className="cell-main">{incident.title}</div>
                      <div className="cell-sub mono">{incident.id}</div>
                    </td>
                    <td>{incident.service || "—"}<div className="cell-sub">{incident.environment}</div></td>
                    <td>{incident.alert_count}</td>
                    <td className="nowrap muted">{new Date(incident.updated_at).toLocaleString()}</td>
                    <td className="nowrap">
                      <Button className="link" onClick={() => setSelected(incident.id)}>查看</Button>
                      {incident.status !== "resolved" ? <Button className="link" onClick={() => ack(incident.id)}>认领</Button> : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </Card>

      {selected ? (
        <Card title="事故详情" actions={<Button className="link" onClick={() => setSelected("")}>关闭</Button>}>
          {detail.loading && !detail.data ? <Skeleton height={160} /> : null}
          {detail.error ? <div className="danger-text">{detail.error}</div> : null}
          {detail.data?.incident ? (
            <div className="stack">
              <div className="row" style={{ gap: 10, flexWrap: "wrap" }}>
                <Badge kind={statusKind(detail.data.incident.status)}>{detail.data.incident.status}</Badge>
                <Badge kind={severityKind(detail.data.incident.severity)}>{detail.data.incident.severity || "未分级"}</Badge>
                <span className="muted small">{detail.data.incident.id}</span>
                {detail.data.incident.acknowledged_by ? <span className="muted small">认领：{detail.data.incident.acknowledged_by}</span> : null}
              </div>
              <div>{detail.data.incident.summary || detail.data.incident.title}</div>
              <div className="muted small">服务 {detail.data.incident.service || "—"} · 环境 {detail.data.incident.environment || "—"} · 告警 {detail.data.incident.alert_count}</div>
              <div>
                <div className="muted small" style={{ marginBottom: 6 }}>时间线</div>
                <div className="table-wrap">
                  <table>
                    <thead><tr><th>时间</th><th>类型</th><th>操作者</th><th>内容</th></tr></thead>
                    <tbody>
                      {detail.data.events.map((event) => (
                        <tr key={event.id}>
                          <td className="nowrap muted">{new Date(event.created_at).toLocaleString()}</td>
                          <td>{event.kind}</td>
                          <td>{event.actor || "—"}</td>
                          <td className="muted" style={{ maxWidth: 520, whiteSpace: "pre-wrap" }}>{event.text || "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          ) : null}
        </Card>
      ) : null}
    </div>
  );
}
