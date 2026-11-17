export type Employee = {
  id: string;
  name: string;
  title: string;
  role: string;
  mission: string;
  status: string;
  chat_id: string;
  allowed_tools?: string[];
};

export type CaseItem = {
  id: string;
  employee_id: string;
  type: string;
  source: string;
  source_id: string;
  title: string;
  summary: string;
  severity: string;
  status: string;
  requester: string;
  service: string;
  environment: string;
  chat_id: string;
  sla_due_at?: string;
  created_at: string;
  updated_at: string;
  resolved_at?: string;
};

export type CaseEvent = { id: number; kind: string; actor: string; text: string; created_at: string };
export type Feedback = { id: number; verdict: string; note: string; actor: string; csat: number; created_at: string };
export type Approval = {
  id: string;
  case_id: string;
  action: string;
  params: unknown;
  status: string;
  requested_by: string;
  approved_by: string;
  result: string;
  created_at: string;
};
export type CaseReport = {
  case_id: string;
  problem: string;
  impact: string;
  evidence: string;
  root_cause: string;
  actions: string;
  verification: string;
  prevention: string;
  status: string;
  author: string;
  created_at?: string;
  updated_at?: string;
};
export type CaseReview = { id: number; case_id: string; reviewer: string; evidence: number; root_cause: number; safety: number; usefulness: number; honesty: number; note: string; created_at: string };
export type Run = { ID: string; State: string; Output: string; Error: string; InputTokens: number; OutputTokens: number };
export type CaseDetail = {
  case: CaseItem;
  events: CaseEvent[];
  feedback?: Feedback[];
  reviews?: CaseReview[];
  report?: CaseReport | null;
  approvals?: Approval[];
  run?: Run | null;
};

export type Incident = {
  id: string;
  fingerprint: string;
  status: string;
  severity: string;
  title: string;
  summary: string;
  service: string;
  environment: string;
  chat_id: string;
  alert_count: number;
  acknowledged_by: string;
  created_at: string;
  updated_at: string;
  resolved_at?: string;
};
export type IncidentEvent = { id: number; kind: string; actor: string; text: string; created_at: string };
export type IncidentDetail = {
  incident: Incident;
  events: IncidentEvent[];
  case?: CaseItem | null;
  run?: Run | null;
};

export const statusKind = (status: string) =>
  status === "resolved" || status === "closed" ? "ok" : status === "waiting_human" ? "warn" : status === "acknowledged" ? "brand" : "danger";
export const severityKind = (severity: string) =>
  severity === "critical" || severity === "page" ? "danger" : severity === "warning" || severity === "high" ? "warn" : "";
