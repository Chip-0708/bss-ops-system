-- 000021: change_request.sku_id 从 NOT NULL 改为 NULL（8b-1 裁决 A1）。
-- 背景：价目表发布/回滚是 price_book 级（多 SKU）变更，change_request 无单一 sku_id 可填。
-- 只放宽约束，不删列——向后兼容（7b 的 PRICE_UP/PRICE_DOWN 仍填 sku_id）。
ALTER TABLE change_request ALTER COLUMN sku_id DROP NOT NULL;
