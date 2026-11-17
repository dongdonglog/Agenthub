import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, type Paged } from "../api/client";
import { Badge, Button, Card, Field, PageHeader, Select, Skeleton, Textarea, useAsync, useToast } from "../components/ui";
import { statusKind, type CaseDetail, type CaseItem, type CaseReview } from "../types";

type Score = { evidence: number; root_cause: number; safety: number; usefulness: number; honesty: number; note: string };
const EMPTY: Score = { evidence: 0, root_cause: 0, safety: 0, usefulness: 0, honesty: 0, note: "" };

export function Review() {
  const cases = useAsync<Paged<CaseItem>>(() => api("/cases?source=alertmanager&limit=50"), []);
  const [saved, setSaved] = useState<Record<string, Score>>({});
  const { show } = useToast();

  const exportCSV = () => {
    const rows = [["case_id", "evidence", "root_cause", "safety", "usefulness", "honesty", "note"]];
    for (const [id, score] of Object.entries(saved)) rows.push([id, String(score.evidence), String(score.root_cause), String(score.safety), String(score.usefulness), String(score.honesty), score.note.replace(/\n/g, " ")]);
    const blob = new Blob([rows.map((row) => row.map((cell) => `"${cell.replace(/"/g, '""')}"`).join(",")).join("\n")], { type: "text/csv" });
    const url = URL.createObjectURL(blob); const a = document.createElement("a"); a.href = url; a.download = "agenthub-review.csv"; a.click(); URL.revokeObjectURL(url);
    show("已导出评分 CSV");
  };

  return (
    <div className="stack">
      <PageHeader title="人工评审" description="对 AIOps 案件逐条评审：证据、根因、动作安全、有用性、诚实度。评分写入服务端。" actions={<Button onClick={exportCSV}>导出本次 CSV</Button>} />
      {cases.loading && !cases.data ? <Skeleton height={240} /> : null}
      {cases.error ? <div className="danger-text">{cases.error}</div> : null}
      {cases.data?.items.map((item) => (
        <ReviewCard key={item.id} item={item} onSaved={(score) => setSaved((prev) => ({ ...prev, [item.id]: score }))} />
      ))}
      {cases.data?.items.length === 0 ? <Card><div className="muted">没有可评审的 Alertmanager 案件</div></Card> : null}
    </div>
  );
}

function ReviewCard({ item, onSaved }: { item: CaseItem; onSaved: (score: Score) => void }) {
  const detail = useAsync<CaseDetail>(() => api(`/cases/${item.id}`), [item.id]);
  const [score, setScore] = useState<Score>(EMPTY);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const { show } = useToast();

  useEffect(() => {
    const existing = detail.data?.reviews?.find((review: CaseReview) => review.reviewer === "admin");
    if (existing) setScore({ evidence: existing.evidence, root_cause: existing.root_cause, safety: existing.safety, usefulness: existing.usefulness, honesty: existing.honesty, note: existing.note });
  }, [detail.data]);

  const submit = async () => {
    setBusy(true);
    try {
      await api(`/cases/${encodeURIComponent(item.id)}/review`, { method: "POST", body: JSON.stringify({ ...score, reviewer: "admin" }) });
      onSaved(score); show("评审已保存");
    } catch (err) { show((err as Error).message, true); } finally { setBusy(false); }
  };

  const reviews = detail.data?.reviews ?? [];
  const avg = reviews.length ? reviews.reduce((sum, review) => sum + (review.evidence + review.root_cause + review.safety + review.usefulness + review.honesty) / 5, 0) / reviews.length : 0;

  return (
    <Card title={<span>{item.title} <Badge kind={statusKind(item.status)}>{item.status}</Badge></span>} actions={<div className="row"><Link className="btn" to={`/cases/${item.id}`}>打开案件</Link><Button className="link" onClick={() => setOpen((value) => !value)}>{open ? "收起诊断" : "展开诊断"}</Button></div>}>
      <div className="muted small">{item.service || "—"} · {item.severity} · {item.id} {reviews.length ? `· 已评 ${reviews.length} 人 · 均分 ${avg.toFixed(1)}` : ""}</div>
      {open ? detail.data?.run ? <pre className="code-block" style={{ whiteSpace: "pre-wrap", maxHeight: 360, overflow: "auto", marginTop: 10 }}>{detail.data.run.Output || "（无输出）"}</pre> : <div className="muted">暂无诊断输出</div> : null}
      <div className="grid grid-4" style={{ marginTop: 12 }}>
        <ScoreSelect label="证据" value={score.evidence} onChange={(value) => setScore({ ...score, evidence: value })} />
        <ScoreSelect label="根因" value={score.root_cause} onChange={(value) => setScore({ ...score, root_cause: value })} />
        <ScoreSelect label="动作安全" value={score.safety} onChange={(value) => setScore({ ...score, safety: value })} />
        <ScoreSelect label="有用性" value={score.usefulness} onChange={(value) => setScore({ ...score, usefulness: value })} />
        <ScoreSelect label="诚实度" value={score.honesty} onChange={(value) => setScore({ ...score, honesty: value })} />
      </div>
      <Field label="备注"><Textarea rows={2} value={score.note} onChange={(event) => setScore({ ...score, note: event.target.value })} /></Field>
      <Button className="primary" disabled={busy} onClick={submit}>{busy ? "保存中…" : "保存评分"}</Button>
    </Card>
  );
}

function ScoreSelect({ label, value, onChange }: { label: string; value: number; onChange: (value: number) => void }) {
  return <label className="field"><span>{label}</span><Select value={value} onChange={(event) => onChange(Number(event.target.value))}>{[0, 1, 2, 3].map((n) => <option key={n} value={n}>{n}</option>)}</Select></label>;
}
