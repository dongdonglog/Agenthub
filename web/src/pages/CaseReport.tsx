import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "../api/client";
import { Badge, Breadcrumbs, Button, Card, Field, PageHeader, Skeleton, Textarea, useAsync, useToast } from "../components/ui";
import type { CaseReport as Report } from "../types";

type Response = { case: { id: string; title: string; status: string }; report: Report };

const FIELDS: { key: keyof Report; label: string; hint?: string }[] = [
  { key: "problem", label: "问题是什么" },
  { key: "impact", label: "影响范围" },
  { key: "evidence", label: "证据", hint: "指标 / 日志 / trace / 变更；没有证据就写未验证" },
  { key: "root_cause", label: "根因" },
  { key: "actions", label: "已采取动作", hint: "审批、扩容、重启、回滚、切流" },
  { key: "verification", label: "验证结果", hint: "怎么确认问题已恢复，例如成功率、错误率、Pod 就绪数" },
  { key: "prevention", label: "后续改进", hint: "监控、告警、容量、发布流程" },
];

export function CaseReport() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { show } = useToast();
  const data = useAsync<Response>(() => api(`/cases/${encodeURIComponent(id)}/report`), [id]);
  const [report, setReport] = useState<Report | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => { if (data.data?.report) setReport(data.data.report); }, [data.data]);

  const save = async (status: "draft" | "final") => {
    if (!report) return;
    if (status === "final" && (!report.root_cause.trim() || !report.verification.trim())) {
      show("定稿前必须填写根因和验证结果", true);
      return;
    }
    setBusy(true);
    try {
      const saved = await api<Report>(`/cases/${encodeURIComponent(id)}/report`, {
        method: "PUT",
        body: JSON.stringify({ ...report, status }),
      });
      setReport(saved);
      show(status === "final" ? "报告已定稿，案件已关闭" : "草稿已保存");
      if (status === "final") data.reload();
    } catch (err) { show((err as Error).message, true); } finally { setBusy(false); }
  };

  const markdown = () => {
    if (!report) return "";
    return `# ${data.data?.case.title ?? "处置报告"}\n\n` +
      `- 案件：${id}\n- 状态：${report.status}\n- 影响：${report.impact}\n\n` +
      `## 问题\n\n${report.problem}\n\n## 证据\n\n${report.evidence}\n\n## 根因\n\n${report.root_cause}\n\n` +
      `## 已采取动作\n\n${report.actions}\n\n## 验证\n\n${report.verification}\n\n## 后续改进\n\n${report.prevention}\n`;
  };
  const download = () => {
    const blob = new Blob([markdown()], { type: "text/markdown" });
    const url = URL.createObjectURL(blob); const a = document.createElement("a");
    a.href = url; a.download = `${id}-report.md`; a.click(); URL.revokeObjectURL(url);
  };

  if (data.loading && !data.data) return <div className="stack"><PageHeader title="处置报告" /><Skeleton height={300} /></div>;
  if (data.error) return <Card title="加载失败"><div className="danger-text">{data.error}</div><Button onClick={data.reload}>重试</Button></Card>;
  if (!report) return <Card title="报告不存在"><Button onClick={() => navigate(`/cases/${id}`)}>返回案件</Button></Card>;

  return (
    <div className="stack">
      <Breadcrumbs items={[{ label: "案件", to: "/cases" }, { label: id, to: `/cases/${id}` }, { label: "处置报告" }]} />
      <PageHeader title="处置报告" description={<span>{data.data?.case.title} · <span className="mono">{id}</span> · <Badge kind={report.status === "final" ? "ok" : "warn"}>{report.status}</Badge></span>}
        actions={<div className="row"><Button onClick={download}>下载 Markdown</Button><Button onClick={async () => { try { await api(`/cases/${encodeURIComponent(id)}/report/send`, { method: "POST" }); show("报告已发送到飞书"); } catch (err) { show((err as Error).message, true); } }}>发送到飞书</Button><Button onClick={() => save("draft")} disabled={busy}>保存草稿</Button><Button className="primary" onClick={() => save("final")} disabled={busy}>定稿并关闭案件</Button></div>} />
      <Card title="报告内容">
        <div className="form-grid">
          {FIELDS.map((field) => (
            <Field key={field.key} label={field.label} hint={field.hint}>
              <Textarea rows={field.key === "evidence" || field.key === "actions" ? 7 : 4} value={String(report[field.key] ?? "")} onChange={(event) => setReport({ ...report, [field.key]: event.target.value })} />
            </Field>
          ))}
        </div>
      </Card>
      <Card title="预览"><pre className="code-block" style={{ whiteSpace: "pre-wrap" }}>{markdown()}</pre></Card>
    </div>
  );
}
