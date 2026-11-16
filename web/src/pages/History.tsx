import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, qs, type Generation, type Paged } from "../api/client";
import { Badge, Breadcrumbs, Card, PageHeader, Pagination, Skeleton, useAsync } from "../components/ui";

export function History() {
  const [page, setPage] = useState(1);
  const size = 20;
  const { data, loading, error } = useAsync<Paged<Generation>>(() => api(`/history${qs({ page, size })}`), [page]);

  return (
    <div className="stack">
      <PageHeader title="历史会话" description="按会话代聚合；/reset 后的旧代仍在这里。" />
      <Card>
        {loading && !data ? <Skeleton height={200} /> : null}
        {error ? <div className="danger-text">{error}</div> : null}
        {data && data.items.length === 0 ? <div className="empty"><div className="empty-icon">🗂️</div><div>还没有历史记录</div></div> : null}
        {data && data.items.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead><tr><th>会话</th><th>代</th><th>消息</th><th>最近活跃</th><th /></tr></thead>
              <tbody>
                {data.items.map((item) => (
                  <tr key={`${item.conversation_id}-${item.generation}`}>
                    <td className="mono">{item.conversation_id.length > 36 ? `${item.conversation_id.slice(0, 36)}…` : item.conversation_id}</td>
                    <td><Badge>第 {item.generation} 代</Badge></td>
                    <td>{item.messages}</td>
                    <td className="nowrap muted">{new Date(item.last_active).toLocaleString()}</td>
                    <td><Link className="btn link" to={`/history/${encodeURIComponent(item.conversation_id)}/${item.generation}`}>查看</Link></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        {data ? <Pagination page={page} size={size} total={data.total} onChange={setPage} /> : null}
      </Card>
    </div>
  );
}

type Message = { ID: string; Role: string; Text: string; CreatedAt: string };
type RunEvent = { kind: string; name?: string; detail?: string; duration_ms?: number };
type Run = { ID: string; State: string; Round: number; StartedAt: string; InputTokens: number; OutputTokens: number };
type HistoryRun = { run: Run; events: RunEvent[] };
type HistoryDetailData = Paged<Message> & { runs?: HistoryRun[] };

export function HistoryDetail() {
  const { conversation = "", generation = "1" } = useParams();
  const conv = decodeURIComponent(conversation);
  const { data, loading, error } = useAsync<HistoryDetailData>(
    () => api(`/history/${encodeURIComponent(conv)}/${generation}`),
    [conv, generation],
  );

  return (
    <div className="stack">
      <Breadcrumbs items={[{ label: "历史会话", to: "/history" }, { label: `第 ${generation} 代` }]} />
      <PageHeader title="历史详情" description={`${conv} · 第 ${generation} 代`} actions={<Link className="btn" to="/history">返回列表</Link>} />
      <Card>
        {loading && !data ? <Skeleton height={200} /> : null}
        {error ? <div className="danger-text">{error}</div> : null}
        {data && data.items.length === 0 ? <div className="empty"><div className="empty-icon">📭</div><div>没有消息</div></div> : null}
        <div className="stack" style={{ gap: 10 }}>
          {data?.items.map((message) => (
            <div key={message.ID} style={{ borderBottom: "1px solid var(--border)", paddingBottom: 10 }}>
              <div className="row" style={{ justifyContent: "space-between" }}>
                <Badge kind={message.Role === "assistant" ? "brand" : ""}>{message.Role}</Badge>
                <span className="muted small">{new Date(message.CreatedAt).toLocaleString()}</span>
              </div>
              <div style={{ marginTop: 6, whiteSpace: "pre-wrap" }}>{message.Text}</div>
            </div>
          ))}
        </div>
      </Card>
      <Card title="执行轨迹">
        {data?.runs?.length ? (
          <div className="stack">
            {data.runs.map(({ run, events }) => (
              <div key={run.ID} className="stack" style={{ gap: 6 }}>
                <div className="mono small">#{run.Round || "—"} · {run.ID} · {run.State}</div>
                {events.length === 0 ? <div className="muted small">没有轨迹事件</div> : (
                  <div className="table-wrap">
                    <table>
                      <thead><tr><th>类型</th><th>名称</th><th>耗时</th><th>详情</th></tr></thead>
                      <tbody>
                        {events.map((event, index) => (
                          <tr key={`${run.ID}-${index}`}>
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
            ))}
          </div>
        ) : <div className="muted">没有可查看的运行</div>}
      </Card>
    </div>
  );
}
