import { useState } from "react";
import { api, type Paged } from "../api/client";
import { Badge, Button, Card, PageHeader, Skeleton, useAsync, useToast } from "../components/ui";

type Employee = { id: string; name: string; title: string; role: string; mission: string; status: string; chat_id: string; allowed_tools?: string[] };
type CaseItem = {
  id: string; employee_id: string; type: string; source: string; source_id: string; title: string; summary: string;
  severity: string; status: string; service: string; environment: string; sla_due_at?: string; created_at: string; updated_at: string;
};
type Event = { id: number; kind: string; actor: string; text: string; created_at: string };
type Feedback = { id: number; verdict: string; note: string; actor: string; created_at: string };
type Stats = { employee_id: string; cases: number; resolved: number; useful: number; not_useful: number; avg_resolve_ms: number; csat: number };
type Approval = { id: string; case_id: string; action: string; params: unknown; status: string; requested_by: string; approved_by: string; result: string; created_at: string };
type Detail = { case: CaseItem; events: Event[]; feedback?: Feedback[]; approvals?: Approval[] };

const statusKind = (status: string) => status === "resolved" || status === "closed" ? "ok" : status === "waiting_human" ? "warn" : "danger";

export function Workbench() {
  const employees = useAsync<Paged<Employee>>(() => api("/employees"), []);
  const cases = useAsync<Paged<CaseItem>>(() => api("/cases"), []);
  const [selected, setSelected] = useState("");
  const detail = useAsync<Detail>(
    () => selected ? api(`/cases/${encodeURIComponent(selected)}`) : Promise.resolve({ case: null as unknown as CaseItem, events: [] }),
    [selected],
  );
  const { show } = useToast();
  const [stats, setStats] = useState<Stats | null>(null);
  const loadStats = async (employeeID: string) => {
    try { setStats(await api<Stats>(`/employees/${encodeURIComponent(employeeID)}/stats`)); } catch { setStats(null); }
  };

  const decide = async (id: string, action: "approve" | "reject") => {
    try {
      await api(`/approvals/${encodeURIComponent(id)}/${action}`, { method: "POST", body: JSON.stringify({ actor: "admin" }) });
      show(action === "approve" ? "已批准" : "已拒绝");
      detail.reload();
    } catch (err) { show((err as Error).message, true); }
  };

  const createTicket = async () => {
    const title = window.prompt("工单标题");
    if (!title) return;
    const summary = window.prompt("问题描述") ?? "";
    const service = window.prompt("服务") ?? "";
    const priority = window.prompt("优先级 P1/P2/P3", "P3") ?? "P3";
    try {
      const result = await api<{ id: string }>("/cases", {
        method: "POST",
        body: JSON.stringify({ title, summary, service, priority, requester: "admin" }),
      });
      show("工单已创建");
      cases.reload();
      setSelected(result.id);
    } catch (err) { show((err as Error).message, true); }
  };

  const requestScale = async () => {
    if (!selected) return;
    const namespace = window.prompt("namespace");
    const deployment = window.prompt("deployment");
    const replicas = Number(window.prompt("replicas (1-20)"));
    if (!namespace || !deployment || !replicas) return;
    try {
      await api(`/cases/${encodeURIComponent(selected)}/approvals`, {
        method: "POST",
        body: JSON.stringify({ action: "k8s_scale", params: { namespace, deployment, replicas }, actor: "admin" }),
      });
      show("已提交审批");
      detail.reload();
    } catch (err) { show((err as Error).message, true); }
  };

  const act = async (path: string, success: string, body?: Record<string, unknown>) => {
    if (!selected) return;
    try {
      const result = await api<{ summary?: string }>(`/cases/${encodeURIComponent(selected)}/${path}`, {
        method: "POST",
        body: body ? JSON.stringify(body) : undefined,
      });
      show(result.summary ?? success);
      cases.reload();
      detail.reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  return (
    <div className="stack">
      <PageHeader title="数字员工工作台" description="员工、案件、时间线、交接。" />
      <Card title="值班员工">
        {employees.loading && !employees.data ? <Skeleton height={80} /> : null}
        {employees.data?.items.map((employee) => (
          <div className="row" key={employee.id} style={{ justifyContent: "space-between", alignItems: "flex-start", padding: "8px 0" }}>
            <div>
              <div className="cell-main">{employee.name} <Badge kind="ok">{employee.status}</Badge></div>
              <div className="cell-sub">{employee.title} · {employee.role}</div>
              <div className="muted small" style={{ marginTop: 4 }}>{employee.mission}</div>
              {employee.allowed_tools?.length ? <div className="muted small">工具：{employee.allowed_tools.join(", ")}</div> : null}
              <Button className="link" onClick={() => loadStats(employee.id)}>绩效</Button>
              <Button className="link" onClick={async () => {
                const raw = window.prompt("允许的工具（逗号分隔）", (employee.allowed_tools ?? []).join(","));
                if (raw === null) return;
                try {
                  await api(`/employees/${encodeURIComponent(employee.id)}`, {
                    method: "PUT",
                    body: JSON.stringify({ ...employee, allowed_tools: raw.split(",").map((x) => x.trim()).filter(Boolean) }),
                  });
                  show("员工授权已更新");
                  employees.reload();
                } catch (err) { show((err as Error).message, true); }
              }}>编辑授权</Button>
            </div>
            <span className="mono muted small">{employee.chat_id || "未绑定群"}</span>
          </div>
        ))}
        {employees.data && employees.data.items.length === 0 ? <div className="muted">还没有数字员工</div> : null}
        {stats ? <div className="muted small">案件 {stats.cases} · 已解决 {stats.resolved} · 有用 {stats.useful} · 无用 {stats.not_useful} · 平均解决 {Math.round(stats.avg_resolve_ms / 1000)}s · CSAT {stats.csat ? stats.csat.toFixed(1) : "—"}</div> : null}
      </Card>

      <Card title="案件" actions={<div className="row" style={{ gap: 8 }}><Button className="link" onClick={createTicket}>新建工单</Button><Button className="link" onClick={() => cases.reload()}>刷新</Button></div>}>
        {cases.loading && !cases.data ? <Skeleton height={220} /> : null}
        {cases.error ? <div className="danger-text">{cases.error}</div> : null}
        {cases.data && cases.data.items.length === 0 ? <div className="empty"><div className="empty-icon">🗂️</div><div>还没有案件</div></div> : null}
        {cases.data && cases.data.items.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead><tr><th>状态</th><th>级别</th><th>案件</th><th>服务</th><th>员工</th><th>更新时间</th><th /></tr></thead>
              <tbody>
                {cases.data.items.map((item) => (
                  <tr key={item.id}>
                    <td><Badge kind={statusKind(item.status)}>{item.status}</Badge></td>
                    <td><Badge kind={item.severity === "critical" ? "danger" : "warn"}>{item.severity || "—"}</Badge></td>
                    <td><div className="cell-main">{item.title}</div><div className="cell-sub mono">{item.id}</div></td>
                    <td>{item.service || "—"}<div className="cell-sub">{item.environment}</div></td>
                    <td className="mono muted">{item.employee_id || "—"}</td>
                    <td className="nowrap muted">{new Date(item.updated_at).toLocaleString()}</td>
                    <td><Button className="link" onClick={() => setSelected(item.id)}>查看</Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </Card>

      {selected ? (
        <Card title="案件详情" actions={<Button className="link" onClick={() => setSelected("")}>关闭</Button>}>
          {detail.loading && !detail.data ? <Skeleton height={160} /> : null}
          {detail.error ? <div className="danger-text">{detail.error}</div> : null}
          {detail.data?.case ? (
            <div className="stack">
              <div className="row" style={{ gap: 10, flexWrap: "wrap" }}>
                <Badge kind={statusKind(detail.data.case.status)}>{detail.data.case.status}</Badge>
                <Badge kind={detail.data.case.severity === "critical" ? "danger" : "warn"}>{detail.data.case.severity || "未分级"}</Badge>
                <span className="mono muted small">{detail.data.case.id}</span>
              </div>
              <div>{detail.data.case.summary || detail.data.case.title}</div>
              {detail.data.case.sla_due_at ? (
                <div className="muted small">
                  SLA 截止：{new Date(detail.data.case.sla_due_at).toLocaleString()}
                  {detail.data.case.status !== "resolved" && detail.data.case.status !== "closed" && new Date(detail.data.case.sla_due_at).getTime() < Date.now() ? "（已超时）" : ""}
                </div>
              ) : null}
              <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
                <Button onClick={() => act("handoff", "交接摘要已生成")}>生成交接</Button>
                <Button onClick={() => act("feedback", "已标记有用", { verdict: "useful", csat: 5 })}>有用</Button>
                <Button onClick={() => act("feedback", "已标记无用", { verdict: "not_useful", csat: 1 })}>无用</Button>
                <Button onClick={async () => {
                  if (!selected) return;
                  try {
                    const result = await api<{ markdown: string }>(`/cases/${encodeURIComponent(selected)}/postmortem`, { method: "POST" });
                    await navigator.clipboard?.writeText(result.markdown);
                    show("复盘草稿已生成并复制");
                    detail.reload();
                  } catch (err) { show((err as Error).message, true); }
                }}>生成复盘</Button>
                <Button className="danger" onClick={() => act("resolve", "案件已关闭")}>关闭案件</Button>
                <Button onClick={requestScale}>申请扩容审批</Button>
              </div>
              {detail.data.approvals?.length ? (
                <div>
                  <div className="muted small" style={{ marginBottom: 6 }}>动作审批</div>
                  <div className="table-wrap">
                    <table>
                      <thead><tr><th>动作</th><th>状态</th><th>申请人</th><th>结果</th><th /></tr></thead>
                      <tbody>
                        {detail.data.approvals.map((approval) => (
                          <tr key={approval.id}>
                            <td className="mono">{approval.action}</td>
                            <td><Badge kind={approval.status === "approved" ? "ok" : approval.status === "rejected" ? "danger" : "warn"}>{approval.status}</Badge></td>
                            <td>{approval.requested_by}</td>
                            <td className="muted" style={{ maxWidth: 360, wordBreak: "break-all" }}>{approval.result || "—"}</td>
                            <td className="nowrap">
                              {approval.status === "pending" ? (
                                <>
                                  <Button className="link" onClick={() => decide(approval.id, "approve")}>批准</Button>
                                  <Button className="link danger-text" onClick={() => decide(approval.id, "reject")}>拒绝</Button>
                                </>
                              ) : null}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              ) : null}
              <div className="table-wrap">
                <table>
                  <thead><tr><th>时间</th><th>类型</th><th>操作者</th><th>内容</th></tr></thead>
                  <tbody>
                    {detail.data.events.map((event) => (
                      <tr key={event.id}>
                        <td className="nowrap muted">{new Date(event.created_at).toLocaleString()}</td>
                        <td>{event.kind}</td>
                        <td>{event.actor || "—"}</td>
                        <td className="muted" style={{ maxWidth: 560, whiteSpace: "pre-wrap" }}>{event.text || "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          ) : null}
        </Card>
      ) : null}
    </div>
  );
}
