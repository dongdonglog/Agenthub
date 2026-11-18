-- 0039_conversation_lease.sql — 会话执行租约
--
-- active[conv] 只在进程内有效。两个实例连同一个库时互不可见：
-- 一条消息落库为 queued，两个实例各自的 Restore/Dispatch 都可能把它拉进
-- 本地队列，导致同一条消息被执行两次。
--
-- 租约把「谁有权执行这个会话」变成数据库事实，跨实例互斥。
-- 与 delivery_state 的租约同一套思路：owner + 过期时间，过期即视为持有者已死。

CREATE TABLE IF NOT EXISTS conversation_lease (
    conversation_id text PRIMARY KEY,
    owner           text NOT NULL,
    expires_at      timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS conversation_lease_expiry_idx ON conversation_lease (expires_at);
