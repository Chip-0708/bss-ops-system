-- 000015_quote_rejected_at：报价单补 rejected_at 列。
-- 背景：05-quotes.md §4 的 decision.decided_at 需要区分「通过时间」与「驳回时间」，
-- 复用 approved_at 会让驳回单的时间语义错位。本轮（5a-2）只读该列（恒为 null），
-- 5b 审批驳回时写入。

ALTER TABLE quote_sheet ADD COLUMN rejected_at timestamptz NULL;
