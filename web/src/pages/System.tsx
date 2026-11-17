import { useState } from "react";
import { PageHeader, Tabs } from "../components/ui";
import { Overview } from "./Overview";
import { Sessions } from "./Sessions";
import { History } from "./History";
import { Skills } from "./Skills";
import { MCPs } from "./MCPs";
import { Tasks } from "./Tasks";
import { Audits } from "./Audits";
import { Settings } from "./Settings";

type Tab = "overview" | "sessions" | "history" | "skills" | "mcps" | "tasks" | "audits" | "settings";

export function System() {
  const [tab, setTab] = useState<Tab>("overview");
  return (
    <div className="stack">
      <PageHeader title="系统" description="运行状态、会话、技能、MCP、定时任务、审计和设置。" />
      <Tabs
        value={tab}
        onChange={setTab}
        items={[
          { value: "overview", label: "运行概览" },
          { value: "sessions", label: "在线会话" },
          { value: "history", label: "历史会话" },
          { value: "skills", label: "Skill" },
          { value: "mcps", label: "MCP" },
          { value: "tasks", label: "定时任务" },
          { value: "audits", label: "审计" },
          { value: "settings", label: "设置" },
        ]}
      />
      {tab === "overview" ? <Overview /> : null}
      {tab === "sessions" ? <Sessions /> : null}
      {tab === "history" ? <History /> : null}
      {tab === "skills" ? <Skills /> : null}
      {tab === "mcps" ? <MCPs /> : null}
      {tab === "tasks" ? <Tasks /> : null}
      {tab === "audits" ? <Audits /> : null}
      {tab === "settings" ? <Settings /> : null}
    </div>
  );
}
