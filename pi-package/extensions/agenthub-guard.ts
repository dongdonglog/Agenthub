/**
 * AgentHub guard: 运维场景下阻止高风险 shell 命令，并把只读约束告诉模型。
 *
 * 这是 pi 原生扩展，不依赖 AgentHub 进程；通过环境变量控制：
 * - AGENTHUB_READONLY=1：阻断破坏性命令；
 * - AGENTHUB_CASE_ID：当前案件，写入状态提示。
 */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const destructive = [
  /\brm\s+(-[a-z]*r[a-z]*f?|-[a-z]*f[a-z]*r?)\b/i,
  /\bmkfs(\.[a-z0-9]+)?\b/i,
  /\bdd\s+if=/i,
  /\bshutdown\b/i,
  /\breboot\b/i,
  /\bkill\s+-9\s+1\b/i,
  /\bkubectl\s+(delete|apply|patch|edit|scale|rollout\s+undo)\b/i,
  /\bterraform\s+(apply|destroy)\b/i,
  /\bgit\s+push\s+--force\b/i,
  /\bdrop\s+(database|table)\b/i,
  /\btruncate\s+table\b/i,
];

export default function (pi: ExtensionAPI) {
  let evidenceNudged = false;

  pi.on("agent_before_settle", async (event, ctx) => {
    if (process.env.AGENTHUB_READONLY !== "1" || evidenceNudged) return undefined;
    const branch = ctx.sessionManager.getBranch();
    const usedQuery = branch.some((entry) => {
      if (entry.type !== "message" || entry.message.role !== "toolResult") return false;
      return /^(prom_|loki_|tempo_|k8s_|ops_investigate|mcp__ops)/.test(entry.message.toolName ?? "");
    });
    if (usedQuery) return undefined;
    evidenceNudged = true;
    return {
      entries: [{
        type: "custom_message" as const,
        customType: "agenthub-evidence-gate",
        content: "只读诊断还没有调用任何观测工具。请先查 Prometheus/Loki/Tempo/K8s 证据；如果数据源不可用，明确说明缺少哪个数据源，再给出带“未验证”标记的结论。",
        display: true,
      }],
      continue: true,
    };
  });

  pi.on("tool_call", async (event) => {
    if (process.env.AGENTHUB_READONLY !== "1") return undefined;
    if (event.toolName === "edit" || event.toolName === "write") {
      return { block: true, reason: "AgentHub 只读诊断模式禁止写文件；如需修改请转人工" };
    }
    if (event.toolName !== "bash") return undefined;
    const command = String((event.input as { command?: unknown }).command ?? "");
    if (destructive.some((pattern) => pattern.test(command))) {
      return { block: true, reason: "AgentHub 处于只读诊断模式，已阻断潜在破坏性命令；如需执行请转人工审批" };
    }
    return undefined;
  });

  pi.on("before_agent_start", async (event) => {
    if (process.env.AGENTHUB_READONLY !== "1") return undefined;
    const caseId = process.env.AGENTHUB_CASE_ID ?? "";
    return {
      systemPrompt: event.systemPrompt + "\n\n" + [
        "当前运行处于 AgentHub 只读诊断模式。",
        caseId ? `当前案件：${caseId}。` : "",
        "只允许 read/grep/find/ls 和只读查询；禁止重启、删除、扩容、回滚、改配置等写操作。",
        "每个结论必须引用实际查询结果；缺少证据时明确说明。",
      ].filter(Boolean).join("\n"),
    };
  });
}
