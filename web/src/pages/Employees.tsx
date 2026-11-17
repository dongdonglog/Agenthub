import { useState } from "react";
import { api, type Paged } from "../api/client";
import { Badge, Button, Card, Field, Input, Modal, PageHeader, Skeleton, useAsync, useToast } from "../components/ui";
import type { Employee } from "../types";

type Stats = { employee_id: string; cases: number; resolved: number; useful: number; not_useful: number; avg_resolve_ms: number; csat: number };

export function Employees() {
  const employees = useAsync<Paged<Employee>>(() => api("/employees"), []);
  const [editing, setEditing] = useState<Employee | null>(null);
  const [stats, setStats] = useState<Stats | null>(null);
  const { show } = useToast();

  const loadStats = async (id: string) => {
    try { setStats(await api<Stats>(`/employees/${encodeURIComponent(id)}/stats`)); } catch (err) { show((err as Error).message, true); }
  };

  return (
    <div className="stack">
      <PageHeader title="数字员工" description="岗位、工具授权、案件绩效和值班群绑定。" />
      {employees.loading && !employees.data ? <Skeleton height={180} /> : null}
      {employees.error ? <div className="danger-text">{employees.error}</div> : null}
      <div className="grid grid-2">
        {employees.data?.items.map((employee) => (
          <Card key={employee.id} title={<span>{employee.name} <Badge kind={employee.status === "active" ? "ok" : "warn"}>{employee.status}</Badge></span>}>
            <div className="cell-sub">{employee.title} · {employee.role}</div>
            <p className="muted">{employee.mission}</p>
            <div className="muted small">值班群：{employee.chat_id || "未绑定"}</div>
            <div className="muted small">工具：{employee.allowed_tools?.join(", ") || "未限制（继承全局）"}</div>
            <div className="row" style={{ marginTop: 12 }}>
              <Button className="link" onClick={() => loadStats(employee.id)}>查看绩效</Button>
              <Button className="link" onClick={() => setEditing(employee)}>编辑授权</Button>
            </div>
          </Card>
        ))}
      </div>
      {employees.data?.items.length === 0 ? <Card><div className="muted">还没有数字员工</div></Card> : null}
      {stats ? (
        <Card title={`绩效 · ${stats.employee_id}`} actions={<Button className="link" onClick={() => setStats(null)}>关闭</Button>}>
          <div className="grid grid-4">
            <Stat label="案件" value={stats.cases} />
            <Stat label="已解决" value={stats.resolved} />
            <Stat label="有用 / 无用" value={`${stats.useful} / ${stats.not_useful}`} />
            <Stat label="平均解决" value={`${Math.round(stats.avg_resolve_ms / 1000)}s`} />
            <Stat label="CSAT" value={stats.csat ? stats.csat.toFixed(1) : "—"} />
          </div>
        </Card>
      ) : null}
      {editing ? <EditEmployee employee={editing} onClose={() => setEditing(null)} onDone={() => { setEditing(null); employees.reload(); show("授权已更新"); }} /> : null}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return <div><div className="stat-label">{label}</div><div className="stat-value" style={{ fontSize: 22 }}>{value}</div></div>;
}

function EditEmployee({ employee, onClose, onDone }: { employee: Employee; onClose: () => void; onDone: () => void }) {
  const [tools, setTools] = useState((employee.allowed_tools ?? []).join(","));
  const [mission, setMission] = useState(employee.mission);
  const [busy, setBusy] = useState(false);
  const { show } = useToast();
  const submit = async () => {
    setBusy(true);
    try {
      await api(`/employees/${encodeURIComponent(employee.id)}`, {
        method: "PUT",
        body: JSON.stringify({ ...employee, mission, allowed_tools: tools.split(",").map((item) => item.trim()).filter(Boolean) }),
      });
      onDone();
    } catch (err) { show((err as Error).message, true); } finally { setBusy(false); }
  };
  return (
    <Modal title={`编辑 ${employee.name}`} onClose={onClose}>
      <div className="form-grid">
        <Field label="使命"><Input value={mission} onChange={(event) => setMission(event.target.value)} /></Field>
        <Field label="允许的工具" hint="逗号分隔；留空表示继承全局；MCP 工具前缀通常是 mcp__"><Input value={tools} onChange={(event) => setTools(event.target.value)} /></Field>
        <div className="row"><Button className="primary" disabled={busy} onClick={submit}>{busy ? "保存中…" : "保存"}</Button></div>
      </div>
    </Modal>
  );
}
