import { useState } from "react";
import { api, type Overview as OverviewData, type QueueItem } from "../api/client";
import { useAsync, Badge, Button, Card, PageHeader, Skeleton, useToast } from "../components/ui";

export function Overview() {
  const { data, loading, error, reload } = useAsync<OverviewData>(() => api("/overview"), []);
  const queue = useAsync<{ items: QueueItem[] }>(() => api("/queue"), []);
  const [text, setText] = useState("");
  const [answer, setAnswer] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const { show } = useToast();

  const runTest = async () => {
    if (!text.trim()) return;
    setBusy(true);
    setAnswer(null);
    try {
      const result = await api<{ output: string; latency_ms: number }>("/test", {
        method: "POST",
        body: JSON.stringify({ text }),
      });
      setAnswer(`${result.output}\n\n（${result.latency_ms} ms）`);
    } catch (err) {
      show((err as Error).message, true);
    } finally {
      setBusy(false);
    }
  };

  if (loading && !data) {
    return (
      <div className="stack">
        <PageHeader title="运行概览" />
        <div className="grid grid-4">
          {[0, 1, 2, 3].map((index) => <Skeleton key={index} height={92} />)}
        </div>
      </div>
    );
  }
  if (error) return <Card title="加载失败"><p className="danger-text">{error}</p><Button onClick={reload}>重试</Button></Card>;
  if (!data) return null;

  const counts = data.counts ?? {};
  const stats = [
    { label: "会话", value: counts.conversations ?? 0 },
    { label: "运行", value: counts.runs ?? 0 },
    { label: "技能", value: counts.skills ?? 0 },
    { label: "MCP", value: counts.mcps ?? 0 },
  ];

  return (
    <div className="stack">
      <PageHeader title="运行概览" description={`版本 ${data.version} · 通道 ${data.channel} · 运行时 ${data.runtime} ${data.pi_version}`} />
      <div className="grid grid-4">
        {stats.map((stat) => (
          <div key={stat.label} className="card">
            <div className="stat-label">{stat.label}</div>
            <div className="stat-value">{stat.value}</div>
          </div>
        ))}
      </div>

      <Card title="模型" actions={<Badge kind={data.model.echo ? "warn" : "ok"}>{data.model.echo ? "回显模式" : "已配置"}</Badge>}>
        <div className="row" style={{ justifyContent: "space-between", flexWrap: "wrap" }}>
          <div>
            <div className="cell-main">{data.model.model}</div>
            <div className="cell-sub mono">{data.model.base_url}</div>
          </div>
          <a className="btn" href="#/settings">去配置</a>
        </div>
      </Card>

      {queue.data && queue.data.items.length > 0 ? (
        <Card title="调度队列">
          <div className="table-wrap">
            <table>
              <thead><tr><th>会话</th><th>排队</th><th>基础优先级</th><th>有效优先级</th></tr></thead>
              <tbody>
                {queue.data.items.slice(0, 8).map((item) => (
                  <tr key={item.conversation_id}>
                    <td className="mono">{item.conversation_id}</td>
                    <td>{item.queued}</td>
                    <td>{item.priority}</td>
                    <td>{item.effective}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      ) : null}

      <Card title="在线试跑" actions={<span className="muted small">直接调用当前 pi 运行时</span>}>
        <textarea className="textarea" placeholder="输入一句话，验证模型与技能是否生效" value={text} onChange={(event) => setText(event.target.value)} />
        <div className="row" style={{ marginTop: 12 }}>
          <Button className="primary" onClick={runTest} disabled={busy}>{busy ? "执行中…" : "试跑"}</Button>
        </div>
        {answer ? <pre className="code-block" style={{ marginTop: 12, whiteSpace: "pre-wrap" }}>{answer}</pre> : null}
      </Card>
    </div>
  );
}
