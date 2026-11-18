import { useState } from "react";
import { Link } from "react-router-dom";
import { api, qs, type Paged } from "../api/client";
import { Badge, Button, Card, Empty, Field, Input, Modal, PageHeader, Pagination, Select, Skeleton, Textarea, useAsync, useToast } from "../components/ui";
import { TimeFilter } from "../components/TimeFilter";
import { statusKind, type CaseItem } from "../types";

const PAGE_SIZE = 20;

export function Cases() {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [type, setType] = useState("");
  const [page, setPage] = useState(1);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [creating, setCreating] = useState(false);
  const { show } = useToast();
  const cases = useAsync<Paged<CaseItem>>(
    () => api(`/cases${qs({ q, status, type, from, to, limit: PAGE_SIZE, offset: (page - 1) * PAGE_SIZE })}`),
    [q, status, type, from, to, page],
  );

  const change = (setter: (value: string) => void) => (value: string) => {
    setter(value);
    setPage(1);
  };

  return (
    <div className="stack">
      <PageHeader
        title="案件"
        description="事故与工单的统一队列。点开案件查看诊断、证据、时间线和审批。"
        actions={<Button className="primary" onClick={() => setCreating(true)}>新建工单</Button>}
      />
      <Card>
        <div className="row" style={{ gap: 10, flexWrap: "wrap", marginBottom: 14 }}>
          <Input placeholder="搜索标题 / 服务 / 摘要" value={q} onChange={(event) => change(setQ)(event.target.value)} style={{ maxWidth: 320 }} />
          <Select value={status} onChange={(event) => change(setStatus)(event.target.value)}>
            <option value="">全部状态</option>
            <option value="new">new</option>
            <option value="in_progress">in_progress</option>
            <option value="waiting_human">waiting_human</option>
            <option value="resolved">resolved</option>
            <option value="closed">closed</option>
          </Select>
          <Select value={type} onChange={(event) => change(setType)(event.target.value)}>
            <option value="">全部类型</option>
            <option value="incident">告警</option>
            <option value="ticket">工单</option>
          </Select>
          <TimeFilter from={from} to={to} onChange={(nextFrom, nextTo) => { setFrom(nextFrom); setTo(nextTo); setPage(1); }} />
          <Button onClick={() => cases.reload()}>刷新</Button>
        </div>
        {cases.loading && !cases.data ? <Skeleton height={240} /> : null}
        {cases.error ? <div className="danger-text">{cases.error}</div> : null}
        {cases.data && cases.data.items.length === 0 ? <Empty icon="🗂️" text="没有匹配的案件" /> : null}
        {cases.data && cases.data.items.length > 0 ? (
          <>
            <div className="table-wrap">
              <table>
                <thead><tr><th>状态</th><th>级别</th><th>案件</th><th>服务</th><th>员工</th><th>SLA</th><th>更新时间</th></tr></thead>
                <tbody>
                  {cases.data.items.map((item) => (
                    <tr key={item.id}>
                      <td><Badge kind={statusKind(item.status)}>{item.status}</Badge></td>
                      <td><Badge kind={item.severity === "critical" ? "danger" : "warn"}>{item.severity || "—"}</Badge></td>
                      <td>
                        <Link className="cell-main" to={`/cases/${item.id}`}>{item.title}</Link>
                        <div className="cell-sub mono">{item.id}</div>
                      </td>
                      <td>{item.service || "—"}<div className="cell-sub">{item.environment}</div></td>
                      <td className="mono muted">{item.employee_id || "—"}</td>
                      <td className="nowrap muted">{item.sla_due_at ? new Date(item.sla_due_at).toLocaleString() : "—"}</td>
                      <td className="nowrap muted">{new Date(item.updated_at).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination page={page} size={PAGE_SIZE} total={cases.data.total} onChange={setPage} />
          </>
        ) : null}
      </Card>
      {creating ? <CreateTicket onClose={() => setCreating(false)} onDone={() => { setCreating(false); cases.reload(); show("工单已创建"); }} /> : null}
    </div>
  );
}

function CreateTicket({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [title, setTitle] = useState("");
  const [summary, setSummary] = useState("");
  const [service, setService] = useState("");
  const [priority, setPriority] = useState("P3");
  const [busy, setBusy] = useState(false);
  const { show } = useToast();

  const submit = async () => {
    if (!title.trim()) { show("标题不能为空", true); return; }
    setBusy(true);
    try {
      await api("/cases", { method: "POST", body: JSON.stringify({ title, summary, service, priority, requester: "admin" }) });
      onDone();
    } catch (err) {
      show((err as Error).message, true);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="新建技术支持工单" onClose={onClose}>
      <div className="form-grid">
        <Field label="标题"><Input value={title} onChange={(event) => setTitle(event.target.value)} /></Field>
        <Field label="问题描述"><Textarea value={summary} onChange={(event) => setSummary(event.target.value)} rows={4} /></Field>
        <Field label="服务"><Input value={service} onChange={(event) => setService(event.target.value)} /></Field>
        <Field label="优先级">
          <Select value={priority} onChange={(event) => setPriority(event.target.value)}>
            <option value="P1">P1</option><option value="P2">P2</option><option value="P3">P3</option><option value="P4">P4</option>
          </Select>
        </Field>
        <div className="row"><Button className="primary" onClick={submit} disabled={busy}>{busy ? "创建中…" : "创建"}</Button></div>
      </div>
    </Modal>
  );
}
