import { useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { api, type Overview } from "../api/client";
import { useAsync } from "../components/ui";
import { useTheme } from "../theme";

const NAV = [
  { to: "/", label: "运行概览", icon: "📊", end: true },
  { to: "/sessions", label: "在线会话", icon: "💬" },
  { to: "/history", label: "历史会话", icon: "🗂️" },
  { to: "/skills", label: "Skill", icon: "🧩" },
  { to: "/mcps", label: "MCP", icon: "🔌" },
  { to: "/tasks", label: "定时任务", icon: "⏰" },
  { to: "/audits", label: "审计", icon: "📜" },
  { to: "/settings", label: "设置", icon: "⚙️" },
];

export function AppShell() {
  const { theme, toggle } = useTheme();
  const [open, setOpen] = useState(false);
  const { data } = useAsync<Overview>(() => api("/overview"), []);

  const online = data?.feishu === "connected";
  const channelLabel = data?.channel === "dingtalk" ? "钉钉" : "飞书";

  return (
    <div className="app">
      <aside className={`sidebar ${open ? "open" : ""}`}>
        <div className="brand">
          <div className="brand-mark">AH</div>
          <div>
            <div className="brand-name">AgentHub</div>
            <div className="brand-sub">团队 AI 助手 · 管理台</div>
          </div>
        </div>
        <nav className="nav" onClick={() => setOpen(false)}>
          {NAV.map((item) => (
            <NavLink key={item.to} to={item.to} end={item.end} className={({ isActive }) => (isActive ? "active" : "")}>
              <span className="ico">{item.icon}</span>
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div style={{ marginTop: "auto" }} className="muted small">
          {data ? `${data.runtime || "pi"} ${data.pi_version || ""}` : "加载中…"}
        </div>
      </aside>

      <div className="main">
        <header className="topbar">
          <div className="row">
            <button className="btn ghost" onClick={() => setOpen((value) => !value)} aria-label="菜单">
              ☰
            </button>
            <span className="muted small">{data ? `版本 ${data.version}` : ""}</span>
          </div>
          <div className="row">
            {data ? (
              <span className={`badge ${online ? "ok" : "danger"}`}>
                {channelLabel}：{data.feishu}
              </span>
            ) : null}
            <button className="btn ghost" onClick={toggle} aria-label="切换主题">
              {theme === "light" ? "🌙 深色" : "☀️ 浅色"}
            </button>
          </div>
        </header>
        <main className="content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
