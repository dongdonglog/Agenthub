-- 0036_scheduled_task_claim.sql — 定时任务领取时间
--
-- ClaimDue 会先把 next_run_at 置空再执行，FinishTask 才回填。若进程在两步之间
-- 崩溃，任务会永久停摆。claimed_at 记录领取时刻，供恢复扫描判断是否为僵尸领取。

ALTER TABLE scheduled_task ADD COLUMN IF NOT EXISTS claimed_at timestamptz;

CREATE INDEX IF NOT EXISTS scheduled_task_claim_idx ON scheduled_task (claimed_at)
    WHERE claimed_at IS NOT NULL;
