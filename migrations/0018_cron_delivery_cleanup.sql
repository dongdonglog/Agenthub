-- 0018_cron_delivery_cleanup.sql
-- 早期定时任务把 cron_ 外部 ID 当作回复目标，产生永久失败的投递；一次性清理。

UPDATE delivery_state
SET state = 'failed', error = 'legacy cron reply target'
WHERE state IN ('pending', 'retrying')
  AND reply_to LIKE 'cron\_%';
