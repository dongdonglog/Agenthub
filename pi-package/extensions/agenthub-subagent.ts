/**
 * AgentHub subagent: 为事故诊断并行运行只读调查线（指标/日志/变更/链路）。
 *
 * 每个 lane 是独立 pi 进程，拥有隔离上下文；主 agent 只接收摘要和证据。
 */
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { Type } from "@earendil-works/pi-ai";
import { defineTool, type ExtensionAPI } from "@earendil-works/pi-coding-agent";

const execFileAsync = promisify(execFile);

type Lane = "metrics" | "logs" | "changes" | "traces";

const lanes: Record<Lane, { prompt: string; tools: string }> = {
  metrics: {
    prompt: "你只负责指标调查：用 PromQL 查影响面、错误率、延迟、饱和度和实例健康。只返回证据和结论，不要写操作。",
    tools: "read,bash,grep,find,ls,tool_search",
  },
  logs: {
    prompt: "你只负责日志调查：用 LogQL/文件搜索查错误、异常堆栈和时间窗。只返回证据和结论，不要写操作。",
    tools: "read,bash,grep,find,ls,tool_search",
  },
  changes: {
    prompt: "你只负责变更调查：查 Git/部署记录/最近发布，判断是否与告警时间相关。只返回证据和结论，不要写操作。",
    tools: "read,bash,grep,find,ls",
  },
  traces: {
    prompt: "你只负责链路调查：查慢调用、错误传播和上下游依赖。只返回证据和结论，不要写操作。",
    tools: "read,bash,grep,find,ls,tool_search",
  },
};

const investigate = defineTool({
  name: "ops_investigate",
  label: "并行事故调查",
  description: "并行启动只读子调查（metrics/logs/changes/traces），每个子调查有独立上下文；用于事故诊断时汇总证据。",
  parameters: Type.Object({
    question: Type.String({ description: "要调查的问题/告警摘要" }),
    lanes: Type.Optional(Type.Array(Type.String(), { description: "可选：metrics/logs/changes/traces" })),
  }),
  async execute(_id, params, signal) {
    const requested = (params.lanes ?? ["metrics", "logs", "changes"]).filter((lane): lane is Lane => lane in lanes).slice(0, 4);
    if (requested.length === 0) requested.push("metrics");
    const bin = process.env.PI_BIN || "pi";
    const provider = process.env.PI_PROVIDER || "";
    const model = process.env.PI_MODEL || "";
    const tasks = requested.map(async (lane) => {
      const args = ["-p", "--mode", "text", "--no-context-files", "--no-session", "--no-extensions", "--no-skills", "--no-prompt-templates",
        "--tools", lanes[lane].tools, "--append-system-prompt", lanes[lane].prompt];
      if (process.env.AGENTHUB_GUARD_EXTENSION) {
        args.push("--extension", process.env.AGENTHUB_GUARD_EXTENSION);
      }
      if (provider) args.push("--provider", provider);
      if (model) args.push("--model", model);
      args.push(params.question);
      const { stdout } = await execFileAsync(bin, args, {
        env: { ...process.env, AGENTHUB_READONLY: "1" },
        signal,
        timeout: 90_000,
        maxBuffer: 512 * 1024,
      });
      return `### ${lane}\n${stdout.trim().slice(0, 20_000)}`;
    });
    const results = await Promise.all(tasks);
    return {
      content: [{ type: "text", text: results.join("\n\n") }],
      details: { lanes: requested },
    };
  },
});

export default function (pi: ExtensionAPI) {
  pi.registerTool(investigate);
}
