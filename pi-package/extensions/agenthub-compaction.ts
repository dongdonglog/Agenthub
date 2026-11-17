/**
 * AgentHub incident compaction: 压缩事故长上下文时保留事实、证据、假设和未闭环动作。
 */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { convertToLlm, serializeConversation } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  pi.on("session_before_compact", async (event, ctx) => {
    const model = ctx.model;
    if (!model) return;
    const { messagesToSummarize, turnPrefixMessages, tokensBefore, firstKeptEntryId, previousSummary } = event.preparation;
    const conversation = serializeConversation(convertToLlm([...messagesToSummarize, ...turnPrefixMessages]));
    const previous = previousSummary ? `\n\n已有摘要：\n${previousSummary}` : "";
    const prompt = `你是 SRE 事故上下文压缩器。把下面的事件记录压缩成可继续值班的结构化摘要。${previous}

必须保留：
1. 事故/案件 ID、服务、环境、严重级别、当前状态；
2. 影响面和用户可见症状；
3. 已确认的证据（查询、时间窗、结果）；
4. 已排除的假设和原因；
5. 尚未闭环的动作、负责人、风险；
6. 下一步。

丢弃重复日志、无关工具输出和大段原文。不要编造。

<conversation>
${conversation}
</conversation>`;
    try {
      const response = await ctx.modelRegistry.complete(model, {
        messages: [{ role: "user", content: [{ type: "text", text: prompt }], timestamp: Date.now() }],
      }, { maxTokens: 4096, signal: ctx.signal, cacheRetention: "none" });
      const summary = response.content
        .filter((item): item is { type: "text"; text: string } => item.type === "text")
        .map((item) => item.text)
        .join("\n");
      if (!summary.trim()) return;
      return { compaction: { summary, firstKeptEntryId, tokensBefore, usage: response.usage } };
    } catch {
      return;
    }
  });
}
