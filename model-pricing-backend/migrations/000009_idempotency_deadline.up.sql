-- 000009_idempotency_deadline：PROCESSING 卡死防护（§8.0.1 补充决议）。
-- 命中 PROCESSING 且超过 processing_deadline → 视为上次执行已死，允许重入；
-- 未超时 → 409。只追加列，不动 000006 已提交结构。

ALTER TABLE idempotency_key
  ADD COLUMN processing_deadline timestamptz NULL;

-- 加速 cron 清理与卡死扫描（部分索引：只有 PROCESSING 才有 deadline 值）
CREATE INDEX idx_idem_deadline ON idempotency_key(processing_deadline)
  WHERE status = 'PROCESSING';
