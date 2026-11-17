import { useEffect, useState } from "react";
import { api, type Paged } from "../api/client";
import { Badge, Button, Card, Field, Input, Modal, PageHeader, Skeleton, useAsync, useToast } from "../components/ui";
import type { Employee } from "../types";

type Stats = { employee_id: string; cases: number; resolved: number; useful: number; not_useful: number; avg_resolve_ms: number; csat: number; reviews: number; avg_evidence: number; avg_root_cause: number; avg_safety: number; avg_usefulness: number; avg_honesty: number };

export function Employees() {
  const employees = useAsync<Paged<Employee>>(() => api("/employees"), []);
  const [editing, setEditing] = useState<Employee | null>(null);
  const [stats, setStats] = useState<Stats | null>(null);
  const [allStats, setAllStats] = useState<Record<string, Stats>>({});
  const { show } = useToast();

  useEffect(() => {
    const items = employees.data?.items ?? [];
    if (items.length === 0) return;
    Promise.all(items.map((employee) => api<Stats>(`/employees/${encodeURIComponent(employee.id)}/stats`).catch(() => null)))
      .then((values) => {
        const map: Record<string, Stats> = {};
        values.forEach((value) => { if (value) map[value.employee_id] = value; });
        setAllStats(map);
      });
  }, [employees.data]);

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
      {Object.keys(allStats).length ? (
        <Card title="绩效对比">
          <div className="table-wrap"><table>
            <thead><tr><th>员工</th><th>案件</th><th>解决</th><th>采纳率</th><th>CSAT</th><th>评审数</th><th>证据</th><th>根因</th><th>安全</th><th>有用</th><th>诚实</th></tr></thead>
            <tbody>{Object.values(allStats).map((item) => {
              const totalFeedback = item.useful + item.not_useful;
              return <tr key={item.employee_id}>
                <td className="mono">{item.employee_id}</td><td>{item.cases}</td><td>{item.resolved}</td>
                <td>{totalFeedback ? `${Math.round((item.useful / totalFeedback) * 100)}%` : "—"}</td>
                <td>{item.csat ? item.csat.toFixed(1) : "—"}</td><td>{item.reviews}</td>
                <td>{item.avg_evidence ? item.avg_evidence.toFixed(1) : "—"}</td><td>{item.avg_root_cause ? item.avg_root_cause.toFixed(1) : "—"}</td>
                <td>{item.avg_safety ? item.avg_safety.toFixed(1) : "—"}</td><td>{item.avg_usefulness ? item.avg_usefulness.toFixed(1) : "—"}</td><td>{item.avg_honesty ? item.avg_honesty.toFixed(1) : "—"}</td>
              </tr>;
            })}</tbody>
          </table></div>
        </Card>
      ) : null}
      {employees.data?.items.length === 0 ? <Card><div className="muted">还没有数字员工</div></Card> : null}
      {stats ? (
        <Card title={`绩效 · ${stats.employee_id}`} actions={<Button className="link" onClick={() => setStats(null)}>关闭</Button>}>
          <div className="grid grid-4">
            <Stat label="案件" value={stats.cases} />
            <Stat label="已解决" value={stats.resolved} />
            <Stat label="有用 / 无用" value={`${stats.useful} / ${stats.not_useful}`} />
            <Stat label="平均解决" value={`${Math.round(stats.avg_resolve_ms / 1000)}s`} />
            <Stat label="CSAT" value={stats.csat ? stats.csat.toFixed(1) : "—"} />
            <Stat label="评审" value={`${stats.reviews} 条`} />
            <Stat label="证据 / 根因" value={`${stats.avg_evidence.toFixed(1)} / ${stats.avg_root_cause.toFixed(1)}`} />
            <Stat label="安全 / 有用 / 诚实" value={`${stats.avg_safety.toFixed(1)} / ${stats.avg_usefulness.toFixed(1)} / ${stats.avg_honesty.toFixed(1)}`} />
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
