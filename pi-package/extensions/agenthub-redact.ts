/**
 * AgentHub redaction: 在工具结果进入模型/会话前脱敏常见密钥，并截断超大输出。
 */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const patterns: Array<[RegExp, string]> = [
  [/\b(Bearer\s+)[A-Za-z0-9._~+/=-]{12,}/gi, "$1***"],
  [/\b(ghp_|gho_|ghu_|ghs_|ghr_)[A-Za-z0-9]{20,}/g, "$1***"],
  [/\bglpat-[A-Za-z0-9_-]{20,}/g, "glpat-***"],
  [/\bAKIA[0-9A-Z]{16}\b/g, "AKIA***"],
  [/\b(token|api[_-]?key|password|passwd|secret|authorization)\s*[:=]\s*["']?[^"'\s,;]{6,}/gi, "$1=***"],
  [/-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/g, "-----PRIVATE KEY REDACTED-----"],
];

function redact(value: string): string {
  let out = value;
  for (const [pattern, replacement] of patterns) out = out.replace(pattern, replacement);
  return out;
}

function redactJson(value: unknown): unknown {
  if (typeof value === "string") return redact(value);
  if (Array.isArray(value)) return value.map(redactJson);
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
      out[key] = /token|password|secret|api[_-]?key|authorization/i.test(key) ? "***" : redactJson(item);
    }
    return out;
  }
  return value;
}

function truncate(value: string, limit = 24_000): string {
  return value.length > limit ? value.slice(0, limit) + "\n…(工具输出已截断)" : value;
}

export default function (pi: ExtensionAPI) {
  pi.on("tool_result", async (event) => {
    const content = event.content.map((item) =>
      item.type === "text" ? { ...item, text: truncate(redact(item.text)) } : item,
    );
    const structuredContent = event.structuredContent === undefined ? undefined : redactJson(event.structuredContent);
    const details = event.details === undefined ? undefined : redactJson(event.details);
    return { content, structuredContent, details };
  });
}
