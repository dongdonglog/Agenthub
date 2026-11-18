-- 0038_run_heartbeat.sql — 运行心跳与卡片收尾
--
-- 旧实现有两个缺口会让卡片一直停在「处理中」：
--   1. 进程被 kill 后没人回收该运行，卡片没有任何人再更新；
--   2. 卡片句柄只在内存里，进程一死就再也改不了那张卡。
--
-- heartbeat_at：进程存活期间每 10 秒刷新；停止刷新即说明执行者已消失。
-- card_handle：卡片消息 ID，供任意实例在回收时改写这张卡。
-- card_closed：卡片是否已收尾；未收尾的会被反复重试，
--   覆盖「运行结束了但改卡片失败」这条路径。

ALTER TABLE run_state ADD COLUMN IF NOT EXISTS heartbeat_at timestamptz;
ALTER TABLE run_state ADD COLUMN IF NOT EXISTS card_handle text NOT NULL DEFAULT '';
ALTER TABLE run_state ADD COLUMN IF NOT EXISTS card_closed boolean NOT NULL DEFAULT false;
ALTER TABLE run_state ADD COLUMN IF NOT EXISTS card_close_attempts integer NOT NULL DEFAULT 0;

-- 历史运行没有心跳记录，用开始时间兜底。
UPDATE run_state SET heartbeat_at = started_at WHERE heartbeat_at IS NULL AND started_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS run_state_heartbeat_idx ON run_state (heartbeat_at NULLS FIRST)
    WHERE state IN ('running', 'stopping');

CREATE INDEX IF NOT EXISTS run_state_unclosed_card_idx ON run_state (ended_at DESC NULLS FIRST)
    WHERE card_handle <> '' AND card_closed = false;
