export type SessionInfo = {
  id: string;
  kind: "p2p" | "group" | "scheduled" | "unknown";
  chat_id: string;
  user_id: string;
  display_name: string;
  chat_name: string;
  user_name: string;
  messages: number;
  last_active: string;
  internal: boolean;
};

export type Paged<T> = { items: T[]; total: number; page?: number; size?: number };

export type Skill = {
  name: string;
  description: string;
  content: string;
  status: "active" | "disabled" | "archived";
  files: number;
  bytes: number;
  executable: boolean;
  flat: boolean;
  updated_at: string;
  references: number;
};

export type MCP = {
  name: string;
  transport: string;
  command?: string;
  args?: string[];
  url?: string;
  enabled: boolean;
};

export type Task = {
  id: string;
  name: string;
  cron: string;
  timezone: string;
  chat_id: string;
  channel: string;
  prompt: string;
  enabled: boolean;
  next_run_at?: string;
  last_run_at?: string;
  last_status: string;
};

export type Overview = {
  version: string;
  runtime: string;
  pi_version: string;
  channel: string;
  transport: string;
  feishu: string;
  active_profile?: string;
  counts: Record<string, number>;
  model: { provider: string; model: string; base_url: string; has_key: boolean; echo: boolean };
  started_at?: string;
};

export type Generation = { conversation_id: string; generation: number; messages: number; last_active: string };
export type AuditEntry = { id: number; actor: string; action: string; target: string; detail: string; created_at: string };
export type QueueItem = { conversation_id: string; queued: number; priority: number; oldest: string; effective: number };

export class ApiError extends Error {}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await fetch(`/admin/api/v1${path}`, {
    credentials: "same-origin",
    ...options,
    headers: {
      "Content-Type": "application/json",
      "X-Requested-With": "agenthub",
      ...(options.headers ?? {}),
    },
  });
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    const message = (data as { error?: string } | null)?.error ?? `请求失败（${res.status}）`;
    throw new ApiError(message);
  }
  return data as T;
}

export async function uploadZip(file: File): Promise<{ imported: string[] }> {
  const form = new FormData();
  form.append("file", file);
  const res = await fetch("/admin/api/v1/skills/import?overwrite=1", {
    method: "POST",
    body: form,
    credentials: "same-origin",
    headers: { "X-Requested-With": "agenthub" },
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(data.error ?? "导入失败");
  return data;
}

export const qs = (params: Record<string, string | number | undefined>) => {
  const search = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== "") search.set(key, String(value));
  });
  const text = search.toString();
  return text ? `?${text}` : "";
};
