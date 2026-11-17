/**
 * AgentHub report: 让 pi 在诊断结束后直接提交结构化处置报告。
 *
 * Go 侧读取 AGENTHUB_REPORT_DIR/report.json，保存到案件报告；不在 Go 里重新分析。
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { Type } from "@earendil-works/pi-ai";
import { defineTool, type ExtensionAPI } from "@earendil-works/pi-coding-agent";

const submitReport = defineTool({
  name: "submit_report",
  label: "提交处置报告",
  description: "提交结构化的案件处置报告。只有在你已经查过证据、明确区分已验证/未验证后调用。",
  parameters: Type.Object({
    problem: Type.String({ description: "问题是什么" }),
    impact: Type.String({ description: "影响范围" }),
    evidence: Type.String({ description: "具体查询、日志、指标、变更；没有证据写未验证" }),
    root_cause: Type.String({ description: "根因；不确定写未确认" }),
    actions: Type.String({ description: "已执行动作及其审批状态；未执行写未执行" }),
    verification: Type.String({ description: "如何验证恢复，例如错误率、Pod 就绪数" }),
    prevention: Type.String({ description: "后续改进项" }),
  }),
  async execute(_id, params) {
    const dir = process.env.AGENTHUB_REPORT_DIR;
    if (!dir) {
      return { content: [{ type: "text", text: "未配置报告目录，无法提交" }], isError: true };
    }
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, "report.json"), JSON.stringify(params, null, 2));
    return { content: [{ type: "text", text: "处置报告已提交" }] };
  },
});

export default function (pi: ExtensionAPI) {
  pi.registerTool(submitReport);
}
