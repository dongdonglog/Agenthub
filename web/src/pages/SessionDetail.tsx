import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import { Badge, Breadcrumbs, Button, Card, CopyButton, Field, Input, PageHeader, Skeleton, useAsync, useConfirm, useToast } from "../components/ui";

type Message = { ID: string; Role: string; Text: string; CreatedAt: string; Status: string; MsgType: string };
type Run = { ID: string; State: string; Output: string; Error: string; StartedAt: string; InputTokens: number; OutputTokens: number };
type Detail = { id: string; profile?: string; priority?: number | null; messages: Message[]; runs: Run[] };
type RunEvent = { kind: string; name?: string; detail?: string; duration_ms?: number; created_at: string };
type Artifact = { id: string; name: string; kind: string; size: number; created_at: string };
type Delivery = { IdempotencyKey: string; Kind: string; Name: string; State: string; Error: string; Attempts: number; SendStartedAt?: string };
type RunDetail = { run: Run; events: RunEvent[]; artifacts: Artifact[]; deliveries: Delivery[] };

export function SessionDetail() {
  const { id = "" } = useParams();
  const conv = decodeURIComponent(id);
  const { data, loading, error, reload } = useAsync<Detail>(() => api(`/sessions/${encodeURIComponent(conv)}`), [conv]);
  const [priority, setPriority] = useState("");
  const [selectedRun, setSelectedRun] = useState("");
  const { show } = useToast();
  const confirm = useConfirm();

  const applyConfig = async () => {
    if (!(await confirm({
      title: "把当前配置应用到本会话？",
      description: "会同步模型、MCP、技能到工作区，并更新当前代的 Profile 绑定；下一批运行生效。",
      confirmText: "应用",
    }))) return;
    try {
      const result = await api<{ profile: string }>(`/sessions/${encodeURIComponent(conv)}/apply-config`, { method: "POST" });
      show(`已应用配置：${result.profile}`);
      reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const savePriority = async () => {
    try {
      await api(`/sessions/${encodeURIComponent(conv)}/priority`, {
        method: "PUT",
        body: JSON.stringify({ priority: Number(priority) || 0 }),
      });
      show("优先级已更新");
      reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  if (loading && !data) return <Skeleton height={320} />;
  if (error) return <Card title="加载失败"><p className="danger-text">{error}</p></Card>;
  if (!data) return null;

  return (
    <div className="stack">
      <div>
        <Breadcrumbs items={[{ label: "在线会话", to: "/sessions" }, { label: data.messages[0]?.ID ?? conv }]} />
        <PageHeader
          title="会话详情"
          description={
            <span className="row" style={{ gap: 8 }}>
              <span className="mono">{conv.length > 48 ? `${conv.slice(0, 48)}…` : conv}</span>
              <CopyButton value={conv} />
            </span>
          }
          actions={
            <div className="row" style={{ gap: 8 }}>
              <Button onClick={applyConfig}>应用当前配置</Button>
              <Link className="btn" to="/sessions">返回列表</Link>
            </div>
          }
        />
      </div>

      <Card title="调度优先级" actions={<span className="muted small">数值越大越优先；留空表示默认</span>}>
        <div className="row">
          <Input
            style={{ width: 120 }}
            type="number"
            placeholder={data.priority == null ? "默认" : String(data.priority)}
            value={priority}
            onChange={(event) => setPriority(event.target.value)}
          />
          <Button onClick={savePriority}>保存</Button>
        </div>
      </Card>

      <Card title={`消息 · ${data.messages.length}`}>
        <div className="stack" style={{ gap: 10 }}>
          {data.messages.map((message) => (
            <div key={message.ID} style={{ borderBottom: "1px solid var(--border)", paddingBottom: 10 }}>
              <div className="row" style={{ justifyContent: "space-between" }}>
                <Badge kind={message.Role === "assistant" ? "brand" : ""}>{message.Role === "assistant" ? "助手" : "用户"}</Badge>
                <span className="muted small">{new Date(message.CreatedAt).toLocaleString()}</span>
              </div>
              <div style={{ marginTop: 6, whiteSpace: "pre-wrap" }}>{message.Text || "（空）"}</div>
            </div>
          ))}
          {data.messages.length === 0 ? <div className="muted">还没有消息</div> : null}
        </div>
      </Card>

      <Card title={`运行 · ${data.runs.length}`}>
        <div className="table-wrap">
          <table>
            <thead><tr><th>运行</th><th>状态</th><th>Token</th><th>开始</th><th>输出</th><th /></tr></thead>
            <tbody>
              {data.runs.map((run) => (
                <tr key={run.ID}>
                  <td className="mono">{run.ID}</td>
                  <td><Badge kind={run.State === "completed" ? "ok" : run.State === "failed" ? "danger" : "warn"}>{run.State}</Badge></td>
                  <td className="nowrap">{run.InputTokens}/{run.OutputTokens}</td>
                  <td className="nowrap muted">{run.StartedAt ? new Date(run.StartedAt).toLocaleString() : "—"}</td>
                  <td className="muted" style={{ maxWidth: 420 }}>{(run.Output || run.Error || "—").slice(0, 160)}</td>
                  <td><Button className="link" onClick={() => setSelectedRun(run.ID)}>详情</Button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      {selectedRun ? <RunDetailPanel runId={selectedRun} onClose={() => setSelectedRun("")} /> : null}
    </div>
  );
}

function RunDetailPanel({ runId, onClose }: { runId: string; onClose: () => void }) {
  const { data, loading, error } = useAsync<RunDetail>(() => api(`/runs/${encodeURIComponent(runId)}`), [runId]);
  return (
    <Card title="运行详情" actions={<Button className="link" onClick={onClose}>关闭</Button>}>
      {loading && !data ? <Skeleton height={120} /> : null}
      {error ? <div className="danger-text">{error}</div> : null}
      {data ? (
        <div className="stack">
          <div className="muted small mono">{runId} · {data.run.State}</div>
          <div>
            <div className="muted small" style={{ marginBottom: 6 }}>执行轨迹</div>
            {data.events.length === 0 ? <div className="muted">没有轨迹事件</div> : (
              <div className="table-wrap">
                <table>
                  <thead><tr><th>类型</th><th>名称</th><th>耗时</th><th>详情</th></tr></thead>
                  <tbody>
                    {data.events.map((event, index) => (
                      <tr key={`${event.created_at}-${index}`}>
                        <td>{event.kind}</td>
                        <td className="mono">{event.name || "—"}</td>
                        <td>{event.duration_ms ? `${event.duration_ms} ms` : "—"}</td>
                        <td className="muted" style={{ maxWidth: 420, wordBreak: "break-all" }}>{event.detail || "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
          <div>
            <div className="muted small" style={{ marginBottom: 6 }}>交付物</div>
            {data.artifacts?.length ? (
              <div className="stack" style={{ gap: 6 }}>
                {data.artifacts.map((artifact) => (
                  <div className="row" key={artifact.id} style={{ justifyContent: "space-between" }}>
                    <span className="mono">{artifact.name} · {formatSize(artifact.size)}</span>
                    <a className="btn link" href={`/admin/api/v1/artifacts/${encodeURIComponent(artifact.id)}`}>下载</a>
                  </div>
                ))}
              </div>
            ) : <div className="muted">本次运行没有交付物</div>}
          </div>
          <div>
            <div className="muted small" style={{ marginBottom: 6 }}>投递状态</div>
            {data.deliveries?.length ? (
              <div className="table-wrap">
                <table>
                  <thead><tr><th>类型</th><th>状态</th><th>尝试</th><th>错误</th></tr></thead>
                  <tbody>
                    {data.deliveries.map((delivery) => (
                      <tr key={delivery.IdempotencyKey}>
                        <td>{delivery.Kind || "text"}{delivery.Name ? ` · ${delivery.Name}` : ""}</td>
                        <td><Badge kind={delivery.State === "ambiguous" || delivery.State === "failed" ? "danger" : delivery.State === "sent" ? "ok" : "warn"}>{delivery.State}</Badge></td>
                        <td>{delivery.Attempts}</td>
                        <td className="muted" style={{ maxWidth: 420, wordBreak: "break-all" }}>{delivery.Error || "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : <div className="muted">没有投递记录</div>}
          </div>
        </div>
      ) : null}
    </Card>
  );
}

function formatSize(bytes: number) {
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
}
