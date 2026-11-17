import { useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { Badge, Card, Empty, PageHeader, Skeleton, useAsync } from "../components/ui";
import { TimeFilter } from "../components/TimeFilter";
import type { CaseItem, CaseReport } from "../types";

type Item = { report: CaseReport; case: CaseItem | null };

export function Reports() {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const reports = useAsync<{ items: Item[]; total: number }>(() => api(`/reports${from || to ? `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}` : ""}`), [from, to]);
  return (
    <div className="stack">
      <PageHeader title="处置报告" description="每个需要闭环的案件，都要有根因、动作和验证结果。" />
      <Card>
        <TimeFilter from={from} to={to} onChange={(nextFrom, nextTo) => { setFrom(nextFrom); setTo(nextTo); }} />
        {reports.loading && !reports.data ? <Skeleton height={220} /> : null}
        {reports.error ? <div className="danger-text">{reports.error}</div> : null}
        {reports.data && reports.data.items.length === 0 ? <Empty icon="📄" text="还没有处置报告，先打开案件生成草稿" /> : null}
        {reports.data && reports.data.items.length > 0 ? (
          <div className="table-wrap"><table>
            <thead><tr><th>状态</th><th>案件</th><th>根因</th><th>验证</th><th>更新时间</th></tr></thead>
            <tbody>{reports.data.items.map(({ report, case: item }) => (
              <tr key={report.case_id}>
                <td><Badge kind={report.status === "final" ? "ok" : "warn"}>{report.status}</Badge></td>
                <td><Link className="cell-main" to={`/cases/${report.case_id}/report`}>{item?.title ?? report.case_id}</Link><div className="cell-sub mono">{report.case_id}</div></td>
                <td className="muted" style={{ maxWidth: 360 }}>{report.root_cause ? report.root_cause.slice(0, 90) : "未填写"}</td>
                <td className="muted" style={{ maxWidth: 320 }}>{report.verification ? report.verification.slice(0, 80) : "未验证"}</td>
                <td className="nowrap muted">{report.updated_at ? new Date(report.updated_at).toLocaleString() : "—"}</td>
              </tr>
            ))}</tbody>
          </table></div>
        ) : null}
      </Card>
    </div>
  );
}
