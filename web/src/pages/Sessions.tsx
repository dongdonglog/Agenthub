import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api, qs, type Paged, type SessionInfo } from "../api/client";
import { Badge, Button, Card, CopyButton, Input, PageHeader, Pagination, Skeleton, Tabs, useAsync } from "../components/ui";

type Kind = "user" | "group" | "p2p" | "scheduled" | "all";

const KIND_LABEL: Record<string, { label: string; kind: string }> = {
  group: { label: "群聊", kind: "brand" },
  p2p: { label: "私聊", kind: "ok" },
  scheduled: { label: "定时任务", kind: "warn" },
  unknown: { label: "未知", kind: "" },
};

export function Sessions() {
  const [kind, setKind] = useState<Kind>("user");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const size = 20;

  const { data, loading, error } = useAsync<Paged<SessionInfo>>(
    () => api(`/sessions${qs({ kind, q: query, page, size })}`),
    [kind, query, page],
  );

  const tabs = useMemo(
    () => [
      { value: "user" as const, label: "用户会话" },
      { value: "group" as const, label: "群聊" },
      { value: "p2p" as const, label: "私聊" },
      { value: "scheduled" as const, label: "定时任务" },
      { value: "all" as const, label: "全部" },
    ],
    [],
  );

  return (
    <div className="stack">
      <PageHeader title="在线会话" description="按「人 × 群」隔离；定时任务默认单独归档，不混入用户会话。" />
      <Card>
        <Tabs value={kind} onChange={(value) => { setKind(value); setPage(1); }} items={tabs} />
        <div className="toolbar">
          <div className="toolbar-left">
            <Input className="search" placeholder="搜索显示名或会话 ID" value={query} onChange={(event) => { setQuery(event.target.value); setPage(1); }} />
          </div>
          <div className="toolbar-right muted small">{data ? `共 ${data.total} 个会话` : ""}</div>
        </div>

        {loading && !data ? <Skeleton height={220} /> : null}
        {error ? <div className="danger-text">{error}</div> : null}
        {data && data.items.length === 0 ? (
          <div className="empty"><div className="empty-icon">💬</div><div>没有匹配的会话</div></div>
        ) : null}
        {data && data.items.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead>
                <tr><th>会话</th><th>类型</th><th>消息</th><th>最近活跃</th><th>会话 ID</th><th /></tr>
              </thead>
              <tbody>
                {data.items.map((item) => {
                  const meta = KIND_LABEL[item.kind] ?? KIND_LABEL.unknown;
                  return (
                    <tr key={item.id}>
                      <td>
                        <div className="cell-main">{item.display_name}</div>
                        {item.chat_name && item.user_name ? <div className="cell-sub">{item.chat_name} · {item.user_name}</div> : null}
                      </td>
                      <td><Badge kind={meta.kind}>{meta.label}</Badge></td>
                      <td className="nowrap">{item.messages}</td>
                      <td className="nowrap muted">{item.last_active || "—"}</td>
                      <td>
                        <div className="row" style={{ gap: 8 }}>
                          <span className="mono muted" title={item.id}>{item.id.length > 30 ? `${item.id.slice(0, 30)}…` : item.id}</span>
                          <CopyButton value={item.id} />
                        </div>
                      </td>
                      <td className="nowrap"><Link className="btn link" to={`/sessions/${encodeURIComponent(item.id)}`}>打开</Link></td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
        {data ? <Pagination page={page} size={size} total={data.total} onChange={setPage} /> : null}
      </Card>
    </div>
  );
}
