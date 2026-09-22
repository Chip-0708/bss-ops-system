-- 000020_cost_factor_demo：6d 四因子评分的演示/验收装置（**不是业务数据**）。
--
-- 背景：迁移前真库每个 SKU 都只有 1 家有效报价（supplier_count=1、single_point=true），
-- 四因子（价格/稳定性/配额/兼容）算出来分不出高下，无法验证 6d-1 真的接上了真实算法。
-- 本迁移为 sku40（gpt-5-chat）构造「双供应商对照组」：
--
--   供应商 1（A，已有报价）：贵但有配额约束、且 9-10 起连续供货 → 四因子总分高；
--   供应商 2（B，本迁移新增）：便宜但无约束数据、首次生效 9-14 → 四因子总分低。
--
-- 预期验收结论（6d-1 交接 §8）：四因子口径下 primary_supplier_id 仍为 1（贵的当选），
-- 与 6b 「完全成本最低者」口径（primary 会切成 2）不同——两个口径结论不同才证明切换生效。
--
-- 三步：
--   1. quote_item id=39（sheet 27 / supplier 1 / sku40，当前 EFFECTIVE）补 constraints_；
--      迁移前实采 constraints_ IS NULL（E2E 与手写 SQL 双确认），down 直接置回 NULL。
--      若本条 0 行受影响直接报错——说明装置前提被破坏，中止比静默通过好。
--   2. sheet 29（supplier 2 / EFFECTIVE，原只含 sku41 item 41）下新增 sku40 明细；
--      uk_quote_item(quote_sheet_id, sku_id) 防重。
--   3. 新明细挂 input 组件 2.30000000（uk_qc(quote_item_id, component_type) 防重；
--      multiplier 留 NULL——完全成本公式不乘倍率）。
--   created_by/updated_by=1：与 000013 supplier fixture 同源（NULL 语义在两表历史上混用，不踩 NULL）。
--
-- down 迁移完整反向：先删 component（外键子表），再删 item，最后还原 item 39 constraints_=NULL。

DO $$
DECLARE
  new_item_id bigint;
BEGIN
  -- 该迁移只补已有演示报价；全新库没有报价夹具时安全跳过，不能阻断建库。
  IF NOT EXISTS (SELECT 1 FROM quote_item WHERE id = 39 AND quote_sheet_id = 27 AND sku_id = 40) THEN
    RAISE NOTICE '000020：未找到成本因子演示报价，跳过可选装置';
    RETURN;
  END IF;

  -- 步骤 1：供应商 1 的当前 EFFECTIVE 明细补配额约束
  UPDATE quote_item
     SET constraints_ = '{"rpm":3000,"tpm":2000000,"concurrency":64}'::jsonb,
         updated_at   = now()
   WHERE id = 39 AND quote_sheet_id = 27 AND sku_id = 40;

  -- 步骤 2：supplier 2 的 EFFECTIVE 单 sheet 29 下新增 sku40 明细（RESTRICT 违反 = 重复执行，幂等跳过）
  INSERT INTO quote_item(quote_sheet_id, sku_id, currency, constraints_, created_by, updated_by)
  VALUES (29, 40, 'USD', NULL, 1, 1)
  ON CONFLICT ON CONSTRAINT uk_quote_item DO NOTHING
  RETURNING id INTO new_item_id;

  -- 步骤 3：新明细挂 input 组件（幂等：任何一条不满足都跳过，不覆盖既有数据）
  IF new_item_id IS NOT NULL
     AND NOT EXISTS (SELECT 1 FROM quote_component
                      WHERE quote_item_id = new_item_id AND component_type = 'input'
                        AND unit_price = 2.30000000) THEN
    INSERT INTO quote_component(quote_item_id, component_type, unit_price, created_by, updated_by)
    VALUES (new_item_id, 'input', 2.30000000, 1, 1)
    ON CONFLICT ON CONSTRAINT uk_qc DO NOTHING;
  END IF;
END $$;
