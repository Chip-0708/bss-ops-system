-- 000020_cost_factor_demo.down：完整回滚 6d 演示/验收装置。
-- 顺序约束：先删 component（对 quote_item 有外键），再删 item，最后还原 item 39 的 constraints_。
-- item 39 的 constraints_ 迁移前实采为 NULL（E2E 与手写 SQL 双确认），故直接置回 NULL。

-- 步骤 3 反向：删新明细的 input 组件（值限定 2.30000000，防误删后续真实报价改动）
DELETE FROM quote_component
 WHERE component_type = 'input'
   AND unit_price = 2.30000000
   AND quote_item_id IN (SELECT id FROM quote_item WHERE quote_sheet_id = 29 AND sku_id = 40);

-- 步骤 2 反向：删 sheet 29 下的 sku40 明细
DELETE FROM quote_item WHERE quote_sheet_id = 29 AND sku_id = 40;

-- 步骤 1 反向：还原供应商 1 明细的 constraints_ 为 NULL
UPDATE quote_item
   SET constraints_ = NULL,
       updated_at   = now()
 WHERE id = 39 AND quote_sheet_id = 27 AND sku_id = 40;
