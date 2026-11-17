import { useState } from "react";
import { Link } from "react-router-dom";
import { api, type Paged } from "../api/client";
import { Badge, Button, Card, PageHeader, Select, Skeleton, useAsync, useToast } from "../components/ui";
import { statusKind, type CaseDetail, type CaseItem } from "../types";

type Score = { evidence: number; root: number; safety: number; useful: number; honesty: number; note: string };
const EMPTY: Score = { evidence: 0, root: 0, safety: 0, useful: 0, honesty: 0, note: "" };
const KEY = "agenthub.review.v1";

function loadScores(): Record<string, Score> {
  try { return JSON.parse(localStorage.getItem(KEY) ?? "{}") as Record<string, Score>; } catch { return {}; }
}

export function Review() {
  const cases = useAsync<Paged<CaseItem>>(() => api("/cases?source=alertmanager&limit=50"), []);
  const [scores, setScores] = useState<Record<string, Score>>(loadScores);
  const { show } = useToast();

  const update = (id: string, patch: Partial<Score>) => {
    const next = { ...scores, [id]: { ...(scores[id] ?? EMPTY), ...patch } };
    setScores(next); localStorage.setItem(KEY, JSON.stringify(next));
  };

  const exportCSV = () => {
    const rows = [["case_id", "evidence", "root", "safety", "useful", "honesty", "note"]];
    for (const [id, score] of Object.entries(scores)) rows.push([id, String(score.evidence), String(score.root), String(score.safety), String(score.useful), String(score.honesty), score.note.replace(/\n/g, " ")]);
    const blob = new Blob([rows.map((row) => row.map((cell) => `"${cell.replace(/"/g, '""')}"`).join(",")).join("\n")], { type: "text/csv" });
    const url = URL.createObjectURL(blob); const a = document.createElement("a"); a.href = url; a.download = "agenthub-review.csv"; a.click(); URL.revokeObjectURL(url);
    show("已导出评分 CSV");
  };

  return (
    <div className="stack">
      <PageHeader title="人工评审" description="对 AIOps 案件逐条评审：证据、根因、动作安全、有用性、诚实度。评分只保存在本机浏览器。" actions={<Button onClick={exportCSV}>导出 CSV</Button>} />
      {cases.loading && !cases.data ? <Skeleton height={240} /> : null}
      {cases.error ? <div className="danger-text">{cases.error}</div> : null}
      {cases.data?.items.map((item) => (
        <ReviewCard key={item.id} item={item} score={scores[item.id] ?? EMPTY} onChange={(patch) => update(item.id, patch)} />
      ))}
      {cases.data?.items.length === 0 ? <Card><div className="muted">没有可评审的 Alertmanager 案件</div></Card> : null}
    </div>
  );
}

function ReviewCard({ item, score, onChange }: { item: CaseItem; score: Score; onChange: (patch: Partial<Score>) => void }) {
  const detail = useAsync<CaseDetail>(() => api(`/cases/${item.id}`), [item.id]);
  const [open, setOpen] = useState(false);
  return (
    <Card title={<span>{item.title} <Badge kind={statusKind(item.status)}>{item.status}</Badge></span>} actions={<div className="row"><Link className="btn" to={`/cases/${item.id}`}>打开案件</Link><Button className="link" onClick={() => setOpen((value) => !value)}>{open ? "收起" : "展开诊断"}</Button></div>}>
      <div className="muted small">{item.service || "—"} · {item.severity} · {item.id}</div>
      {open ? detail.data?.run ? <pre className="code-block" style={{ whiteSpace: "pre-wrap", maxHeight: 360, overflow: "auto", marginTop: 10 }}>{detail.data.run.Output || "（无输出）"}</pre> : <div className="muted">暂无诊断输出</div> : null}
      <div className="grid grid-4" style={{ marginTop: 12 }}>
        <ScoreSelect label="证据" value={score.evidence} onChange={(value) => onChange({ evidence: value })} />
        <ScoreSelect label="根因" value={score.root} onChange={(value) => onChange({ root: value })} />
        <ScoreSelect label="动作安全" value={score.safety} onChange={(value) => onChange({ safety: value })} />
        <ScoreSelect label="有用性" value={score.useful} onChange={(value) => onChange({ useful: value })} />
        <ScoreSelect label="诚实度" value={score.honesty} onChange={(value) => onChange({ honesty: value })} />
      </div>
      <textarea className="textarea" style={{ marginTop: 10 }} rows={2} placeholder="评审备注" value={score.note} onChange={(event) => onChange({ note: event.target.value })} />
    </Card>
  );
}

function ScoreSelect({ label, value, onChange }: { label: string; value: number; onChange: (value: number) => void }) {
  return <label className="field"><span>{label}</span><Select value={value} onChange={(event) => onChange(Number(event.target.value))}>{[0, 1, 2, 3].map((n) => <option key={n} value={n}>{n}</option>)}</Select></label>;
}
