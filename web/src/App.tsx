import { HashRouter, Navigate, Route, Routes } from "react-router-dom";
import { ConfirmProvider, ToastProvider } from "./components/ui";
import { AppShell } from "./layout/AppShell";
import { ThemeProvider } from "./theme";
import { Overview } from "./pages/Overview";
import { Sessions } from "./pages/Sessions";
import { SessionDetail } from "./pages/SessionDetail";
import { History, HistoryDetail } from "./pages/History";
import { Skills, SkillDetail } from "./pages/Skills";
import { MCPs, MCPDetail } from "./pages/MCPs";
import { Tasks, TaskDetail } from "./pages/Tasks";
import { Audits } from "./pages/Audits";
import { Settings } from "./pages/Settings";

export function App() {
  return (
    <ThemeProvider>
      <ToastProvider>
        <ConfirmProvider>
          <HashRouter>
            <Routes>
              <Route element={<AppShell />}>
                <Route path="/" element={<Overview />} />
                <Route path="/sessions" element={<Sessions />} />
                <Route path="/sessions/:id" element={<SessionDetail />} />
                <Route path="/history" element={<History />} />
                <Route path="/history/:conversation/:generation" element={<HistoryDetail />} />
                <Route path="/skills" element={<Skills />} />
                <Route path="/skills/:name" element={<SkillDetail />} />
                <Route path="/mcps" element={<MCPs />} />
                <Route path="/mcps/:name" element={<MCPDetail />} />
                <Route path="/tasks" element={<Tasks />} />
                <Route path="/tasks/:id" element={<TaskDetail />} />
                <Route path="/audits" element={<Audits />} />
                <Route path="/settings" element={<Settings />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Route>
            </Routes>
          </HashRouter>
        </ConfirmProvider>
      </ToastProvider>
    </ThemeProvider>
  );
}
