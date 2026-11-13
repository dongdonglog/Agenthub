import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, type Task } from "../api/client";
import { Badge, Breadcrumbs, Button, Card, Input, PageHeader, Select, Skeleton, Textarea, useAsync, useConfirm, useToast } from "../components/ui";

export function Tasks() {
  const { data, loading, error, reload } = useAsync<{ items: Task[] }>(() => api("/tasks"), []);
  const { show } = useToast();
  const confirm = useConfirm();

  const remove = async (task: Task) => {
    if (!(await confirm({ title: `删除任务 ${task.name}？`, danger: true, confirmText: "删除" }))) return;
    try {
      await api(`/tasks/${encodeURIComponent(task.id)}`, { method: "DELETE" });
      show("已删除");
      reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  return (
    <div className="stack">
      <PageHeader
        title="定时任务"
        description="按 cron 触发，把 prompt 作为消息投递到目标会话。"
        actions={<Link className="btn primary" to="/tasks/new">新建任务</Link>}
      />
      <Card>
        {loading && !data ? <Skeleton height={180} /> : null}
        {error ? <div className="danger-text">{error}</div> : null}
        {data && data.items.length === 0 ? <div className="empty"><div className="empty-icon">⏰</div><div>还没有定时任务</div></div> : null}
        {data && data.items.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead><tr><th>名称</th><th>cron</th><th>会话</th><th>下次</th><th>上次</th><th>状态</th><th /></tr></thead>
              <tbody>
                {data.items.map((task) => (
                  <tr key={task.id}>
                    <td className="cell-main">{task.name}</td>
                    <td className="mono">{task.cron}</td>
                    <td className="mono muted">{task.chat_id}</td>
                    <td className="nowrap muted">{task.next_run_at ? new Date(task.next_run_at).toLocaleString() : "—"}</td>
                    <td className="nowrap muted">{task.last_run_at ? new Date(task.last_run_at).toLocaleString() : "—"}</td>
                    <td><Badge kind={task.last_status === "ok" ? "ok" : task.last_status ? "danger" : ""}>{task.last_status || "未运行"}</Badge></td>
                    <td>
                      <div className="row" style={{ gap: 10 }}>
                        <Link className="btn link" to={`/tasks/${encodeURIComponent(task.id)}`}>编辑</Link>
                        <button className="btn link danger-text" onClick={() => remove(task)}>删除</button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </Card>
    </div>
  );
}

export function TaskDetail() {
  const { id = "" } = useParams();
  const isNew = id === "new";
  const loaded = useAsync<Task | null>(() => (isNew ? Promise.resolve(null) : api(`/tasks/${encodeURIComponent(id)}`)), [id]);
  const [form, setForm] = useState<Task>({
    id: "",
    name: "",
    cron: "0 9 * * *",
    timezone: "Asia/Shanghai",
    chat_id: "",
    channel: "feishu",
    prompt: "",
    enabled: true,
    last_status: "",
  });
  const [hydrated, setHydrated] = useState(false);
  const { show } = useToast();
  const confirm = useConfirm();

  if (!isNew && loaded.data && !hydrated) {
    setHydrated(true);
    setForm({ ...loaded.data });
  }

  const save = async () => {
    const body = JSON.stringify({
      name: form.name,
      cron: form.cron,
      timezone: form.timezone,
      chat_id: form.chat_id,
      channel: form.channel,
      prompt: form.prompt,
      enabled: form.enabled,
    });
    try {
      if (isNew) {
        await api("/tasks", { method: "POST", body });
        show("已创建");
        window.location.hash = "#/tasks";
      } else {
        await api(`/tasks/${encodeURIComponent(id)}`, { method: "PUT", body });
        show("已保存");
        loaded.reload();
      }
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const runNow = async () => {
    if (isNew) return show("先保存", true);
    try {
      await api(`/tasks/${encodeURIComponent(id)}/run`, { method: "POST" });
      show("已触发");
      setTimeout(loaded.reload, 1200);
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const remove = async () => {
    if (isNew) return;
    if (!(await confirm({ title: "删除该任务？", danger: true, confirmText: "删除" }))) return;
    try {
      await api(`/tasks/${encodeURIComponent(id)}`, { method: "DELETE" });
      show("已删除");
      window.location.hash = "#/tasks";
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  if (loaded.loading && !isNew && !hydrated) return <Skeleton height={300} />;
  if (loaded.error) return <Card title="加载失败"><p className="danger-text">{loaded.error}</p></Card>;

  return (
    <div className="stack">
      <Breadcrumbs items={[{ label: "定时任务", to: "/tasks" }, { label: isNew ? "新建" : form.name || id }]} />
      <PageHeader
        title={isNew ? "新建定时任务" : "编辑定时任务"}
        description={isNew ? undefined : `上次状态：${form.last_status || "未运行"}`}
        actions={<Link className="btn" to="/tasks">返回列表</Link>}
      />
      <Card>
        <div className="form-grid">
          <div className="field"><label>名称</label><Input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></div>
          <div className="field"><label>cron（分 时 日 月 周）</label><Input className="mono" value={form.cron} onChange={(event) => setForm({ ...form, cron: event.target.value })} /></div>
          <div className="field"><label>时区</label><Input value={form.timezone} onChange={(event) => setForm({ ...form, timezone: event.target.value })} /></div>
          <div className="field"><label>目标会话 ID</label><Input className="mono" value={form.chat_id} onChange={(event) => setForm({ ...form, chat_id: event.target.value })} placeholder="飞书 oc_... / 钉钉 cid..." /></div>
          <div className="field">
            <label>通道</label>
            <Select value={form.channel} onChange={(event) => setForm({ ...form, channel: event.target.value })}>
              <option value="feishu">飞书</option>
              <option value="dingtalk">钉钉</option>
            </Select>
          </div>
          <div className="field"><label>内容</label><Textarea rows={6} value={form.prompt} onChange={(event) => setForm({ ...form, prompt: event.target.value })} /></div>
          <label className="row"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} /> 启用</label>
          <div className="form-actions">
            <Button className="primary" onClick={save}>保存</Button>
            {!isNew ? <Button onClick={runNow}>立即运行</Button> : null}
            {!isNew ? <Button className="danger" onClick={remove}>删除</Button> : null}
          </div>
        </div>
      </Card>
    </div>
  );
}
