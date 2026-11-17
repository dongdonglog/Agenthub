-- 0032_employee_tools.sql — 员工级工具授权

ALTER TABLE employee ADD COLUMN IF NOT EXISTS allowed_tools jsonb NOT NULL DEFAULT '[]'::jsonb;
