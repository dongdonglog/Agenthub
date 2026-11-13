import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import { Badge, Breadcrumbs, Button, Card, CopyButton, Field, Input, PageHeader, Skeleton, useAsync, useToast } from "../components/ui";

type Message = { ID: string; Role: string; Text: string; CreatedAt: string; Status: string; MsgType: string };
type Run = { ID: string; State: string; Output: string; Error: string; StartedAt: string; InputTokens: number; OutputTokens: number };
type Detail = { id: string; profile?: string; priority?: number | null; messages: Message[]; runs: Run[] };

export function SessionDetail() {
  const { id = "" } = useParams();
  const conv = decodeURIComponent(id);
  const { data, loading, error, reload } = useAsync<Detail>(() => api(`/sessions/${encodeURIComponent(conv)}`), [conv]);
  const [priority, setPriority] = useState("");
  const { show } = useToast();

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
          actions={<Link className="btn" to="/sessions">返回列表</Link>}
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
            <thead><tr><th>运行</th><th>状态</th><th>Token</th><th>开始</th><th>输出</th></tr></thead>
            <tbody>
              {data.runs.map((run) => (
                <tr key={run.ID}>
                  <td className="mono">{run.ID}</td>
                  <td><Badge kind={run.State === "completed" ? "ok" : run.State === "failed" ? "danger" : "warn"}>{run.State}</Badge></td>
                  <td className="nowrap">{run.InputTokens}/{run.OutputTokens}</td>
                  <td className="nowrap muted">{run.StartedAt ? new Date(run.StartedAt).toLocaleString() : "—"}</td>
                  <td className="muted" style={{ maxWidth: 420 }}>{(run.Output || run.Error || "—").slice(0, 160)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}
