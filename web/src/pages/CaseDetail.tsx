import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "../api/client";
import { Badge, Breadcrumbs, Button, Card, Field, Input, Modal, PageHeader, Skeleton, useAsync, useToast } from "../components/ui";
import { severityKind, statusKind, type Approval, type CaseDetail as Detail } from "../types";

export function CaseDetail() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { show } = useToast();
  const detail = useAsync<Detail>(() => api(`/cases/${encodeURIComponent(id)}`), [id]);
  const [approval, setApproval] = useState(false);
  const [output, setOutput] = useState<{ title: string; text: string } | null>(null);
  const [note, setNote] = useState("");

  const reload = () => { detail.reload(); };
  const act = async (path: string, body?: unknown) => {
    try {
      const result = await api<{ summary?: string; markdown?: string }>(`/cases/${encodeURIComponent(id)}/${path}`, {
        method: "POST", body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (result.markdown) setOutput({ title: "复盘草稿", text: result.markdown });
      if (result.summary) setOutput({ title: "交接摘要", text: result.summary });
      show("已执行");
      reload();
    } catch (err) { show((err as Error).message, true); }
  };
  const decide = async (approvalID: string, action: "approve" | "reject") => {
    try {
      await api(`/approvals/${encodeURIComponent(approvalID)}/${action}`, { method: "POST", body: JSON.stringify({ actor: "admin" }) });
      show(action === "approve" ? "已批准" : "已拒绝");
      reload();
    } catch (err) { show((err as Error).message, true); }
  };

  if (detail.loading && !detail.data) return <div className="stack"><PageHeader title="案件详情" /><Skeleton height={260} /></div>;
  if (detail.error) return <Card title="加载失败"><div className="danger-text">{detail.error}</div><Button onClick={reload}>重试</Button></Card>;
  if (!detail.data?.case) return <Card title="案件不存在"><Button onClick={() => navigate("/cases")}>返回案件列表</Button></Card>;

  const c = detail.data.case;
  const incident = detail.data.incident ?? null;
  const overdue = c.sla_due_at && c.status !== "resolved" && c.status !== "closed" && new Date(c.sla_due_at).getTime() < Date.now();
  // 事故认领/恢复直接复用事故接口，案件页不再需要跳转。
  const actIncident = async (path: "ack" | "resolve") => {
    if (!incident) return;
    try {
      await api(`/incidents/${encodeURIComponent(incident.id)}/${path}`, {
        method: "POST", body: JSON.stringify({ actor: "admin" }),
      });
      show("已更新");
      reload();
    } catch (err) { show((err as Error).message, true); }
  };
  return (
    <div className="stack">
      <Breadcrumbs items={[{ label: "案件", to: "/cases" }, { label: c.id }]} />
      <PageHeader
        title={c.title}
        description={<span>{c.service || "未标服务"} · {c.environment || "未标环境"} · <span className="mono">{c.id}</span></span>}
        actions={<div className="row"><Button onClick={() => { navigator.clipboard?.writeText(c.id); show("案件 ID 已复制"); }}>复制 ID</Button><Button className="primary" onClick={() => navigate(`/cases/${c.id}/report`)}>处置报告</Button><Button onClick={reload}>刷新</Button></div>}
      />
      <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
        <Badge kind={statusKind(c.status)}>{c.status}</Badge>
        <Badge kind={c.severity === "critical" ? "danger" : "warn"}>{c.severity || "未分级"}</Badge>
        <Badge>{c.type}</Badge>
        {c.sla_due_at ? <span className={overdue ? "danger-text small" : "muted small"}>SLA {new Date(c.sla_due_at).toLocaleString()}{overdue ? "（已超时）" : ""}</span> : null}
      </div>
      <Card title="摘要"><div style={{ whiteSpace: "pre-wrap" }}>{c.summary || c.title}</div></Card>

      {incident ? (
        <Card title="告警聚合" actions={<span className="muted small">按告警组去重 {incident.alert_count} 条</span>}>
          <div className="row" style={{ gap: 8, flexWrap: "wrap", marginBottom: 10 }}>
            <Badge kind={statusKind(incident.status)}>{incident.status}</Badge>
            <Badge kind={severityKind(incident.severity)}>{incident.severity || "未分级"}</Badge>
            <span className="muted small">服务 {incident.service || "—"}</span>
            {incident.acknowledged_by ? <span className="muted small">认领：{incident.acknowledged_by}</span> : null}
            <span className="mono muted small">{incident.id}</span>
          </div>
          <div className="row" style={{ gap: 8 }}>
            {incident.status !== "resolved" ? <Button onClick={() => actIncident("ack")}>认领事故</Button> : null}
            {incident.status !== "resolved" ? <Button className="danger" onClick={() => actIncident("resolve")}>手动恢复</Button> : null}
          </div>
        </Card>
      ) : null}

      <Card title="诊断输出" actions={detail.data.run ? <span className="muted small">{detail.data.run.State} · {detail.data.run.InputTokens} in / {detail.data.run.OutputTokens} out</span> : null}>
        {detail.data.run ? (
          detail.data.run.Error ? <div className="danger-text">{detail.data.run.Error}</div> :
            <pre className="code-block" style={{ whiteSpace: "pre-wrap", maxHeight: 520, overflow: "auto" }}>{detail.data.run.Output || "（无输出）"}</pre>
        ) : <div className="muted">暂无诊断运行。支持工单或早期案件可能没有 run 记录。</div>}
      </Card>

      <Card title="处置" actions={<span className="muted small">所有写动作都需要审批或人工确认</span>}>
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <Button onClick={() => act("handoff")}>生成交接</Button>
          <Button onClick={() => act("postmortem")}>生成复盘</Button>
          <Button onClick={() => act("feedback", { verdict: "useful", csat: 5, note })}>标记有用</Button>
          <Button onClick={() => act("feedback", { verdict: "not_useful", csat: 1, note })}>标记无用</Button>
          <Button onClick={() => setApproval(true)}>申请 K8s 动作</Button>
          <Button className="danger" onClick={() => act("resolve")}>关闭案件</Button>
        </div>
        <div style={{ marginTop: 12 }}>
          <Field label="反馈备注（可选）"><Input value={note} onChange={(event) => setNote(event.target.value)} placeholder="例如：根因正确 / 证据不足" /></Field>
        </div>
      </Card>

      <Card title="动作审批">
        {detail.data.approvals?.length ? (
          <div className="table-wrap">
            <table>
              <thead><tr><th>动作</th><th>状态</th><th>申请人</th><th>结果</th><th /></tr></thead>
              <tbody>
                {detail.data.approvals.map((item: Approval) => (
                  <tr key={item.id}>
                    <td className="mono">{item.action}</td>
                    <td><Badge kind={item.status === "approved" ? "ok" : item.status === "rejected" ? "danger" : "warn"}>{item.status}</Badge></td>
                    <td>{item.requested_by || "—"}</td>
                    <td className="muted" style={{ maxWidth: 420, wordBreak: "break-all" }}>{item.result || "—"}</td>
                    <td className="nowrap">
                      {item.status === "pending" ? <><Button className="link" onClick={() => decide(item.id, "approve")}>批准</Button><Button className="link danger-text" onClick={() => decide(item.id, "reject")}>拒绝</Button></> : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : <div className="muted">没有审批记录</div>}
      </Card>

      <Card title="时间线">
        {detail.data.events.length === 0 ? <div className="muted">暂无事件</div> : (
          <div className="table-wrap">
            <table>
              <thead><tr><th>时间</th><th>类型</th><th>操作者</th><th>内容</th></tr></thead>
              <tbody>
                {detail.data.events.map((event) => (
                  <tr key={event.id}>
                    <td className="nowrap muted">{new Date(event.created_at).toLocaleString()}</td>
                    <td>{event.kind}</td><td>{event.actor || "—"}</td>
                    <td className="muted" style={{ maxWidth: 620, whiteSpace: "pre-wrap" }}>{event.text || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {approval ? <ApprovalForm onClose={() => setApproval(false)} onSubmit={async (params) => {
        await api(`/cases/${encodeURIComponent(id)}/approvals`, { method: "POST", body: JSON.stringify({ action: "k8s_scale", params, actor: "admin" }) });
        setApproval(false); show("已提交审批"); reload();
      }} /> : null}
      {output ? <Modal title={output.title} onClose={() => setOutput(null)}><pre className="code-block" style={{ whiteSpace: "pre-wrap", maxHeight: 520, overflow: "auto" }}>{output.text}</pre><Button onClick={() => { navigator.clipboard?.writeText(output.text); show("已复制"); }}>复制</Button></Modal> : null}
    </div>
  );
}

function ApprovalForm({ onClose, onSubmit }: { onClose: () => void; onSubmit: (params: { namespace: string; deployment: string; replicas: number }) => Promise<void> }) {
  const [namespace, setNamespace] = useState("agenthub-test");
  const [deployment, setDeployment] = useState("nginx");
  const [replicas, setReplicas] = useState(2);
  const [busy, setBusy] = useState(false);
  return (
    <Modal title="申请 K8s 扩容" onClose={onClose}>
      <div className="form-grid">
        <Field label="Namespace"><Input value={namespace} onChange={(event) => setNamespace(event.target.value)} /></Field>
        <Field label="Deployment"><Input value={deployment} onChange={(event) => setDeployment(event.target.value)} /></Field>
        <Field label="副本数（1–20）"><Input type="number" min={1} max={20} value={replicas} onChange={(event) => setReplicas(Number(event.target.value))} /></Field>
        <div className="row"><Button className="primary" disabled={busy} onClick={async () => { setBusy(true); try { await onSubmit({ namespace, deployment, replicas }); } finally { setBusy(false); } }}>{busy ? "提交中…" : "提交审批"}</Button></div>
      </div>
    </Modal>
  );
}
