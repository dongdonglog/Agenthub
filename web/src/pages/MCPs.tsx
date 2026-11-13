import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, type MCP, type Paged } from "../api/client";
import { Badge, Breadcrumbs, Button, Card, Input, PageHeader, Select, Skeleton, useAsync, useConfirm, useToast } from "../components/ui";

export function MCPs() {
  const { data, loading, error } = useAsync<Paged<MCP>>(() => api("/mcps?page=1&size=100"), []);
  return (
    <div className="stack">
      <PageHeader
        title="MCP"
        description="配置保存在 data/mcp.yaml；保存后热更新到 pi 与已有工作区。"
        actions={<Link className="btn primary" to="/mcps/new">新建 MCP</Link>}
      />
      <Card>
        {loading && !data ? <Skeleton height={160} /> : null}
        {error ? <div className="danger-text">{error}</div> : null}
        {data && data.items.length === 0 ? <div className="empty"><div className="empty-icon">🔌</div><div>还没有 MCP 配置</div></div> : null}
        {data && data.items.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead><tr><th>名称</th><th>传输</th><th>目标</th><th>状态</th><th /></tr></thead>
              <tbody>
                {data.items.map((mcp) => (
                  <tr key={mcp.name}>
                    <td className="cell-main">{mcp.name}</td>
                    <td>{mcp.transport}</td>
                    <td className="mono muted">{mcp.command || mcp.url || "—"}</td>
                    <td><Badge kind={mcp.enabled ? "ok" : ""}>{mcp.enabled ? "启用" : "停用"}</Badge></td>
                    <td><Link className="btn link" to={`/mcps/${encodeURIComponent(mcp.name)}`}>编辑</Link></td>
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

export function MCPDetail() {
  const { name = "" } = useParams();
  const isNew = name === "new";
  const all = useAsync<Paged<MCP>>(() => api("/mcps?page=1&size=100"), []);
  const existing = isNew ? undefined : all.data?.items.find((item) => item.name === name);

  const [form, setForm] = useState<MCP>({ name: isNew ? "" : name, transport: "stdio", command: "", args: [], url: "", enabled: true });
  const [args, setArgs] = useState("");
  const [hydrated, setHydrated] = useState(false);
  const { show } = useToast();
  const confirm = useConfirm();

  if (!isNew && existing && !hydrated) {
    setHydrated(true);
    setForm({ ...existing });
    setArgs((existing.args ?? []).join(" "));
  }

  const save = async () => {
    const key = isNew ? form.name.trim() : name;
    if (!key) return show("先填名称", true);
    try {
      await api(`/mcps/${encodeURIComponent(key)}`, {
        method: "PUT",
        body: JSON.stringify({
          transport: form.transport,
          command: form.command ?? "",
          args: args.trim() ? args.trim().split(/\s+/) : [],
          url: form.url ?? "",
          enabled: form.enabled,
        }),
      });
      show("已保存");
      window.location.hash = "#/mcps";
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const remove = async () => {
    if (isNew) return;
    if (!(await confirm({ title: `删除 MCP ${name}？`, danger: true, confirmText: "删除" }))) return;
    try {
      await api(`/mcps/${encodeURIComponent(name)}`, { method: "DELETE" });
      show("已删除");
      window.location.hash = "#/mcps";
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  return (
    <div className="stack">
      <Breadcrumbs items={[{ label: "MCP", to: "/mcps" }, { label: isNew ? "新建" : name }]} />
      <PageHeader title={isNew ? "新建 MCP" : `MCP · ${name}`} actions={<Link className="btn" to="/mcps">返回列表</Link>} />
      <Card>
        <div className="form-grid">
          <div className="field"><label>名称</label><Input value={form.name} disabled={!isNew} onChange={(event) => setForm({ ...form, name: event.target.value })} /></div>
          <div className="field">
            <label>传输方式</label>
            <Select value={form.transport} onChange={(event) => setForm({ ...form, transport: event.target.value })}>
              <option value="stdio">stdio</option>
              <option value="http">http</option>
            </Select>
          </div>
          <div className="field"><label>命令</label><Input value={form.command ?? ""} onChange={(event) => setForm({ ...form, command: event.target.value })} placeholder="python3" /></div>
          <div className="field"><label>参数</label><Input value={args} onChange={(event) => setArgs(event.target.value)} placeholder="/app/examples/mcp/time_server.py" /></div>
          <div className="field"><label>地址</label><Input value={form.url ?? ""} onChange={(event) => setForm({ ...form, url: event.target.value })} placeholder="http 传输时填写" /></div>
          <label className="row"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} /> 启用</label>
          <div className="form-actions">
            <Button className="primary" onClick={save}>保存</Button>
            {!isNew ? <Button className="danger" onClick={remove}>删除</Button> : null}
          </div>
        </div>
      </Card>
    </div>
  );
}
