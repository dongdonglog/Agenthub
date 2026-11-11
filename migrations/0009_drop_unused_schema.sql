-- 0009_drop_unused_schema.sql
-- 0001 建了一套面向未来多租户的完整 schema，但当前实现只使用 *_state / run_trace。
-- 删除从未被代码写入的表，避免维护两套模型；如后续需要再按新设计重建。

DROP TABLE IF EXISTS
    run_input,
    tool_call,
    reaction_projection,
    usage_record,
    audit_event,
    memory,
    message_attachment,
    message,
    conversation,
    chat,
    member,
    tenant,
    run,
    delivery,
    run_event
CASCADE;
