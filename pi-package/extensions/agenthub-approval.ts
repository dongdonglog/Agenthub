/**
 * AgentHub approval: 让 pi 在只读诊断中「申请」受控动作，而不是直接执行。
 *
 * 工具只把申请写成 JSON 文件；Go 侧读取后创建 approval_request 并推送审批卡片。
 * 是否允许执行仍由人审批 + namespace allowlist 决定，模型无法绕过。
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { Type } from "@earendil-works/pi-ai";
import { defineTool, type ExtensionAPI } from "@earendil-works/pi-coding-agent";

const requestApproval = defineTool({
  name: "request_approval",
  label: "申请受控动作",
  description:
    "为当前案件申请一次受控运维动作（K8s scale/restart），由人审批后执行。只在已查到证据、明确需要变更时调用；描述里写清依据与影响面。",
  parameters: Type.Object({
    action: Type.String({ description: "动作类型：k8s_scale 或 k8s_restart" }),
    namespace: Type.String({ description: "目标 namespace（必须在部署 allowlist 内）" }),
    deployment: Type.String({ description: "目标 deployment 名称" }),
    replicas: Type.Optional(Type.Number({ description: "k8s_scale 的目标副本数，1-20" })),
    reason: Type.String({ description: "申请理由：证据、影响面、预期效果、回滚方式" }),
  }),
  async execute(_id, params) {
    const dir = process.env.AGENTHUB_APPROVAL_DIR;
    if (!dir) {
      return { content: [{ type: "text", text: "未配置审批目录，无法提交申请" }], isError: true };
    }
    if (!process.env.AGENTHUB_CASE_ID) {
      return { content: [{ type: "text", text: "当前会话没有关联案件，无法申请受控动作；请联系值班同学" }], isError: true };
    }
    mkdirSync(dir, { recursive: true });
    // 文件名带序号与时间戳，避免同一次运行多次申请互相覆盖。
    const name = `approval-${Date.now()}-${Math.random().toString(36).slice(2, 8)}.json`;
    writeFileSync(join(dir, name), JSON.stringify({
      action: params.action,
      namespace: params.namespace,
      deployment: params.deployment,
      replicas: params.replicas ?? 0,
      reason: params.reason,
    }, null, 2));
    return {
      content: [{ type: "text", text: "已提交动作申请，等待人工审批；在获得批准前不要声称动作已执行。" }],
    };
  },
});

export default function (pi: ExtensionAPI) {
  pi.registerTool(requestApproval);
}
