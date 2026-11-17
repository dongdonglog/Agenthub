import { useNavigate, useParams, Link } from "react-router-dom";
import { api } from "../api/client";
import { Badge, Breadcrumbs, Button, Card, PageHeader, Skeleton, useAsync, useToast } from "../components/ui";
import { severityKind, statusKind, type IncidentDetail as Detail } from "../types";

export function IncidentDetail() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { show } = useToast();
  const detail = useAsync<Detail>(() => api(`/incidents/${encodeURIComponent(id)}`), [id]);

  const action = async (path: string) => {
    try {
      await api(`/incidents/${encodeURIComponent(id)}/${path}`, { method: "POST", body: JSON.stringify({ actor: "admin" }) });
      show("已更新"); detail.reload();
    } catch (err) { show((err as Error).message, true); }
  };

  if (detail.loading && !detail.data) return <div className="stack"><PageHeader title="事故详情" /><Skeleton height={260} /></div>;
  if (detail.error) return <Card title="加载失败"><div className="danger-text">{detail.error}</div><Button onClick={detail.reload}>重试</Button></Card>;
  if (!detail.data?.incident) return <Card title="事故不存在"><Button onClick={() => navigate("/incidents")}>返回事故列表</Button></Card>;

  const inc = detail.data.incident;
  return (
    <div className="stack">
      <Breadcrumbs items={[{ label: "运维事故", to: "/incidents" }, { label: inc.id }]} />
      <PageHeader title={inc.title} description={<span>{inc.service || "未标服务"} · {inc.environment || "未标环境"} · <span className="mono">{inc.id}</span></span>}
        actions={<div className="row">{detail.data.case ? <Button className="primary" onClick={() => navigate(`/cases/${detail.data?.case?.id}/report`)}>写处置报告</Button> : null}{inc.status !== "resolved" ? <Button onClick={() => action("ack")}>认领</Button> : null}{inc.status !== "resolved" ? <Button className="danger" onClick={() => action("resolve")}>手动恢复</Button> : null}<Button onClick={detail.reload}>刷新</Button></div>} />
      <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
        <Badge kind={statusKind(inc.status)}>{inc.status}</Badge>
        <Badge kind={severityKind(inc.severity)}>{inc.severity || "未分级"}</Badge>
        <span className="muted small">告警 {inc.alert_count}</span>
        {inc.acknowledged_by ? <span className="muted small">认领：{inc.acknowledged_by}</span> : null}
        {detail.data.case ? <Link className="badge" to={`/cases/${detail.data.case.id}`}>查看案件 {detail.data.case.id}</Link> : null}
      </div>
      <Card title="摘要"><div style={{ whiteSpace: "pre-wrap" }}>{inc.summary || inc.title}</div></Card>
      <Card title="诊断" actions={detail.data.run ? <span className="muted small">{detail.data.run.State} · {detail.data.run.InputTokens} in / {detail.data.run.OutputTokens} out</span> : null}>
        {detail.data.run ? <pre className="code-block" style={{ whiteSpace: "pre-wrap", maxHeight: 520, overflow: "auto" }}>{detail.data.run.Output || "（无输出）"}</pre> : <div className="muted">该事故暂无关联诊断运行</div>}
      </Card>
      <Card title="时间线">
        {detail.data.events.length === 0 ? <div className="muted">暂无事件</div> : (
          <div className="table-wrap"><table>
            <thead><tr><th>时间</th><th>类型</th><th>操作者</th><th>内容</th></tr></thead>
            <tbody>{detail.data.events.map((event) => (
              <tr key={event.id}><td className="nowrap muted">{new Date(event.created_at).toLocaleString()}</td><td>{event.kind}</td><td>{event.actor || "—"}</td><td className="muted" style={{ maxWidth: 640, whiteSpace: "pre-wrap" }}>{event.text || "—"}</td></tr>
            ))}</tbody>
          </table></div>
        )}
      </Card>
    </div>
  );
}
