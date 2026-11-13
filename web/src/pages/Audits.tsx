import { useState } from "react";
import { api, qs, type AuditEntry, type Paged } from "../api/client";
import { Card, PageHeader, Pagination, Skeleton, useAsync } from "../components/ui";

export function Audits() {
  const [page, setPage] = useState(1);
  const size = 20;
  const { data, loading, error } = useAsync<Paged<AuditEntry>>(() => api(`/audits${qs({ page, size })}`), [page]);

  return (
    <div className="stack">
      <PageHeader title="审计" description="管理面写操作只追加记录，用于追溯。" />
      <Card>
        {loading && !data ? <Skeleton height={200} /> : null}
        {error ? <div className="danger-text">{error}</div> : null}
        {data && data.items.length === 0 ? <div className="empty"><div className="empty-icon">📜</div><div>还没有审计记录</div></div> : null}
        {data && data.items.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>对象</th><th>详情</th></tr></thead>
              <tbody>
                {data.items.map((entry) => (
                  <tr key={entry.id}>
                    <td className="nowrap muted">{new Date(entry.created_at).toLocaleString()}</td>
                    <td>{entry.actor || "—"}</td>
                    <td className="mono">{entry.action}</td>
                    <td className="mono">{entry.target}</td>
                    <td className="muted">{entry.detail || "—"}</td>
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
