import { Button, Input } from "./ui";

type Props = { from: string; to: string; onChange: (from: string, to: string) => void };

const iso = (date: string, end = false) => date ? new Date(`${date}T${end ? "23:59:59" : "00:00:00"}`).toISOString() : "";

export function TimeFilter({ from, to, onChange }: Props) {
  const preset = (days: number) => {
    const end = new Date();
    const start = new Date(end.getTime() - days * 86400000);
    onChange(start.toISOString(), end.toISOString());
  };
  return (
    <div className="row" style={{ gap: 8, flexWrap: "wrap", alignItems: "center" }}>
      <span className="muted small">时间</span>
      <Input type="date" value={from ? from.slice(0, 10) : ""} onChange={(event) => onChange(iso(event.target.value), to)} style={{ width: 150 }} />
      <span className="muted small">至</span>
      <Input type="date" value={to ? to.slice(0, 10) : ""} onChange={(event) => onChange(from, iso(event.target.value, true))} style={{ width: 150 }} />
      <Button className="link" onClick={() => preset(1)}>24h</Button>
      <Button className="link" onClick={() => preset(7)}>7d</Button>
      <Button className="link" onClick={() => preset(30)}>30d</Button>
      <Button className="link" onClick={() => onChange("", "")}>全部</Button>
    </div>
  );
}
