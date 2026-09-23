-- PostgreSQL 14 / model_bss / schema_migrations v27, dirty=false.
-- Run with: psql -X -v ON_ERROR_STOP=1 -d model_bss -f prepare-e2e-fixture.sql
-- Only reusable E2E master data is written. All costs below are synthetic fixture costs,
-- never vendor official prices. No price book, approval or customer quote is created.
\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
  v_version bigint;
  v_dirty boolean;
  v_staff bigint;
  v_role bigint;
  v_sales bigint;
  v_sales_b bigint;
  v_supplier bigint;
  v_vendor bigint;
  v_family bigint;
  v_subject bigint;
  v_baseline bigint;
  v_created_by bigint;
  v_margin numeric;
  v_sku bigint;
  v_level text;
  v_name text;
  v_currency text;
  v_count integer;
  v_current integer;
  v_policy record;
  v_customer record;
  v_model record;
  v_cost record;
BEGIN
  -- 1. Preconditions
  IF current_database()<>'model_bss' OR current_setting('server_version_num')::integer/10000<>14 THEN
    RAISE EXCEPTION '需要 PostgreSQL 14 / model_bss；实际版本=%，数据库=%',
      current_setting('server_version'),current_database();
  END IF;
  SELECT version, dirty INTO v_version, v_dirty FROM schema_migrations;
  IF NOT FOUND OR v_version <> 27 OR v_dirty IS DISTINCT FROM false THEN
    RAISE EXCEPTION '需要 schema_migrations version=27, dirty=false；实际 version=%, dirty=%', v_version, v_dirty;
  END IF;

  -- 2. Roles / SALES lookup. Existing accounts are required; never touch passwords.
  FOR v_name, v_level IN
    SELECT * FROM (VALUES ('smoke_admin','PRICING_OP'), ('buyer_b','FINANCE')) AS x(login_id, role_code)
  LOOP
    SELECT a.owner_id INTO v_staff FROM account a
      JOIN internal_staff s ON s.id = a.owner_id
      WHERE a.portal_type='INTERNAL' AND a.login_id=v_name
        AND a.owner_type='STAFF' AND a.status='ACTIVE' AND s.status='ACTIVE';
    SELECT id INTO v_role FROM role WHERE code=v_level;
    IF v_staff IS NULL OR v_role IS NULL THEN
      RAISE EXCEPTION '缺少有效账号 % 或角色 %', v_name, v_level;
    END IF;
    INSERT INTO role_grant(staff_id,role_id,granted_by)
      SELECT v_staff,v_role,v_staff
      WHERE NOT EXISTS (SELECT 1 FROM role_grant
                        WHERE staff_id=v_staff AND role_id=v_role
                          AND (expires_at IS NULL OR expires_at>now()));
  END LOOP;

  SELECT a.owner_id INTO v_sales FROM account a JOIN internal_staff s ON s.id=a.owner_id
    WHERE a.portal_type='INTERNAL' AND a.owner_type='STAFF' AND a.login_id='smoke_sales'
      AND a.status='ACTIVE' AND s.status='ACTIVE'
      AND EXISTS (SELECT 1 FROM role_grant g JOIN role r ON r.id=g.role_id
                  WHERE g.staff_id=s.id AND r.code='SALES'
                    AND (g.expires_at IS NULL OR g.expires_at>now()));
  SELECT a.owner_id INTO v_sales_b FROM account a JOIN internal_staff s ON s.id=a.owner_id
    WHERE a.portal_type='INTERNAL' AND a.owner_type='STAFF' AND a.login_id='smoke_sales_b'
      AND a.status='ACTIVE' AND s.status='ACTIVE'
      AND EXISTS (SELECT 1 FROM role_grant g JOIN role r ON r.id=g.role_id
                  WHERE g.staff_id=s.id AND r.code='SALES'
                    AND (g.expires_at IS NULL OR g.expires_at>now()));
  IF v_sales IS NULL OR v_sales_b IS NULL OR v_sales=v_sales_b THEN
    RAISE EXCEPTION '需要两个不同的 ACTIVE SALES 账号 smoke_sales / smoke_sales_b';
  END IF;
  SELECT sp.id INTO v_supplier FROM supplier_profile sp
    JOIN legal_subject ls ON ls.id=sp.subject_id
    WHERE sp.status='ACTIVE' AND sp.qual_status='VALID' AND sp.settle_status='NORMAL'
      AND ls.verification_status='VERIFIED'
    ORDER BY sp.id LIMIT 1;
  IF v_supplier IS NULL THEN
    RAISE EXCEPTION '缺少合法的 ACTIVE/VALID 供应商，不能注入 synthetic fixture cost';
  END IF;
  SELECT a.owner_id INTO v_created_by FROM account a
    WHERE a.portal_type='INTERNAL' AND a.owner_type='STAFF'
      AND a.login_id='smoke_admin' AND a.status='ACTIVE';

  -- 3. Vendors
  INSERT INTO vendor(code,name) VALUES ('moonshot','Moonshot AI'),('zhipu','智谱 AI')
    ON CONFLICT (code) DO NOTHING;
  IF EXISTS (SELECT 1 FROM vendor WHERE (code='moonshot' AND name<>'Moonshot AI')
                                  OR (code='zhipu' AND name<>'智谱 AI')) THEN
    RAISE EXCEPTION 'moonshot / zhipu vendor.code 已存在但名称冲突';
  END IF;
  IF (SELECT count(*) FROM vendor WHERE name='Moonshot AI')<>1
     OR (SELECT count(*) FROM vendor WHERE name='智谱 AI')<>1 THEN
    RAISE EXCEPTION 'Moonshot AI / 智谱 AI 厂商名称不唯一';
  END IF;

  -- 4. Families
  INSERT INTO model_family(vendor_id,name)
    SELECT v.id,x.family FROM (VALUES ('moonshot','Kimi K3'),('zhipu','GLM-5.3')) AS x(code,family)
      JOIN vendor v ON v.code=x.code
    ON CONFLICT (vendor_id,name) DO NOTHING;

  -- 5. Customers / legal subjects. The seven old named customers stay with smoke_sales;
  -- GOLD-04 makes the intended ownership split 8 / 7 without reassigning them.
  FOR v_customer IN
    SELECT * FROM (VALUES
      ('E2E-GOLD-01',  'GOLD',  'E2E000000000000001',v_sales),
      ('E2E-GOLD-02',  'GOLD',  'E2E000000000000002',v_sales),
      ('E2E-GOLD-03',  'GOLD',  'E2E000000000000003',v_sales),
      ('E2E-GOLD-04',  'GOLD',  'E2E000000000000009',v_sales),
      ('E2E-GOLD-05',  'GOLD',  'E2E000000000000010',v_sales_b),
      ('E2E-BASIC-01', 'BASIC', 'E2E000000000000006',v_sales),
      ('E2E-BASIC-02', 'BASIC', 'E2E000000000000007',v_sales),
      ('E2E-BASIC-03', 'BASIC', 'E2E000000000000011',v_sales_b),
      ('E2E-BASIC-04', 'BASIC', 'E2E000000000000012',v_sales_b),
      ('E2E-SILVER-01','SILVER','E2E000000000000004',v_sales),
      ('E2E-SILVER-02','SILVER','E2E000000000000005',v_sales),
      ('E2E-SILVER-03','SILVER','E2E000000000000013',v_sales_b),
      ('E2E-SILVER-04','SILVER','E2E000000000000014',v_sales_b),
      ('E2E-GLOBAL-01','GLOBAL','E2E000000000000015',v_sales_b),
      ('E2E-GLOBAL-02','GLOBAL','E2E000000000000016',v_sales_b)
    ) AS x(name, level_code, uscc, sales_id)
  LOOP
    IF EXISTS (SELECT 1 FROM legal_subject WHERE legal_name=v_customer.name
                 AND (subject_type<>'COMPANY' OR uscc IS DISTINCT FROM v_customer.uscc))
       OR (SELECT count(*) FROM legal_subject WHERE legal_name=v_customer.name)>1 THEN
      RAISE EXCEPTION 'E2E 客户名称 % 已对应其他主体', v_customer.name;
    END IF;
    SELECT id INTO v_subject FROM legal_subject
      WHERE subject_type='COMPANY' AND uscc=v_customer.uscc;
    IF v_subject IS NULL THEN
      INSERT INTO legal_subject(subject_type,legal_name,uscc,verification_status)
        VALUES('COMPANY',v_customer.name,v_customer.uscc,'VERIFIED')
        RETURNING id INTO v_subject;
    ELSIF NOT EXISTS (SELECT 1 FROM legal_subject WHERE id=v_subject
                      AND legal_name=v_customer.name AND verification_status='VERIFIED') THEN
      RAISE EXCEPTION 'E2E 主体 % 关键字段冲突', v_customer.name;
    END IF;

    INSERT INTO customer_profile(subject_id,level_code,owner_sales_operator_id,status)
      SELECT v_subject,v_customer.level_code,v_customer.sales_id,'ACTIVE'
      WHERE NOT EXISTS (SELECT 1 FROM customer_profile WHERE subject_id=v_subject);
    IF NOT EXISTS (
      SELECT 1 FROM customer_profile cp
        WHERE cp.subject_id=v_subject AND cp.level_code=v_customer.level_code
          AND cp.status='ACTIVE' AND cp.owner_sales_operator_id=v_customer.sales_id) THEN
      RAISE EXCEPTION 'E2E 客户 % 等级、状态或 SALES 归属冲突', v_customer.name;
    END IF;
  END LOOP;

  -- 6. SKUs. Unnamed families use the first existing family of that vendor.
  -- Fictional variants always carry an e2e- prefix; the four named codes are
  -- model test fixtures, not an assertion about official vendor availability.
  FOR v_model IN
    SELECT * FROM (VALUES
      ('kimi-k3',                 'Moonshot AI','Kimi K3', 'CNY',1000000,       'PUBLISHED', NULL::date),
      ('kimi-k3-256k',            'Moonshot AI','Kimi K3', 'CNY',262144,        'PUBLISHED', NULL::date),
      ('glm-5.3',                 '智谱 AI',     'GLM-5.3', 'CNY',NULL::integer, 'PUBLISHED', NULL::date),
      ('glm-5.3-flash',           '智谱 AI',     'GLM-5.3', 'CNY',NULL::integer, 'PUBLISHED', NULL::date),
      ('e2e-openai-load-01',     'OpenAI',     NULL::text, 'USD',NULL::integer, 'PUBLISHED', NULL::date),
      ('e2e-openai-load-02',     'OpenAI',     NULL::text, 'USD',NULL::integer, 'PUBLISHED', NULL::date),
      ('e2e-anthropic-load-01',  'Anthropic',  NULL::text, 'USD',NULL::integer, 'PUBLISHED', NULL::date),
      ('e2e-aliyun-load-01',     '阿里云',     NULL::text, 'CNY',NULL::integer, 'PUBLISHED', NULL::date),
      ('e2e-moonshot-load-01',   'Moonshot AI','Kimi K3', 'CNY',NULL::integer, 'PUBLISHED', NULL::date),
      ('e2e-openai-draft-01',    'OpenAI',     NULL::text, 'USD',NULL::integer, 'DRAFT',     NULL::date),
      ('e2e-anthropic-deprecating-01','Anthropic',NULL::text,'USD',NULL::integer,'DEPRECATING',DATE '2027-12-31'),
      ('e2e-aliyun-no-cost-01',  '阿里云',     NULL::text, 'CNY',NULL::integer, 'PUBLISHED', NULL::date)
    ) AS x(code,vendor_name,family_name,currency,context_window,lifecycle_status,sunset_date)
  LOOP
    SELECT count(*),min(id) INTO v_count,v_vendor FROM vendor WHERE name=v_model.vendor_name;
    IF v_count<>1 THEN RAISE EXCEPTION '厂商 % 缺失或重名', v_model.vendor_name; END IF;
    SELECT id INTO v_family FROM model_family
      WHERE vendor_id=v_vendor AND (v_model.family_name IS NULL OR name=v_model.family_name)
      ORDER BY id LIMIT 1;
    IF v_family IS NULL THEN RAISE EXCEPTION '厂商 % 缺少所需系列 %', v_model.vendor_name,v_model.family_name; END IF;
    SELECT id INTO v_sku FROM model_sku WHERE sku_code=v_model.code;
    IF v_sku IS NULL THEN
      INSERT INTO model_sku(vendor_id,family_id,sku_code,model_type,native_currency,
                            context_window,verify_status,is_sensitive,cross_border,
                            lifecycle_status,sunset_date,created_by)
        VALUES(v_vendor,v_family,v_model.code,'对话',v_model.currency,
               v_model.context_window,'MANUAL',false,false,
               v_model.lifecycle_status,v_model.sunset_date,v_created_by)
        RETURNING id INTO v_sku;
    ELSIF NOT EXISTS (
      SELECT 1 FROM model_sku s WHERE s.id=v_sku AND s.vendor_id=v_vendor
        AND s.family_id=v_family AND s.model_type='对话'
        AND s.native_currency=v_model.currency AND s.verify_status='MANUAL'
        AND s.is_sensitive=false AND s.cross_border=false
        AND s.lifecycle_status=v_model.lifecycle_status
        AND (v_model.context_window IS NULL OR s.context_window=v_model.context_window)
        AND s.sunset_date IS NOT DISTINCT FROM v_model.sunset_date) THEN
      RAISE EXCEPTION 'SKU % 关键字段冲突', v_model.code;
    END IF;
  END LOOP;

  -- 7. Cost baselines/components. All numbers here are synthetic fixture cost,
  -- NOT official vendor prices. Unit and supplier costs use the SKU currency.
  FOR v_cost IN
    SELECT * FROM (VALUES
      ('kimi-k3',                1.20000000::numeric, 2.40000000::numeric),
      ('kimi-k3-256k',           1.40000000::numeric, 2.80000000::numeric),
      ('glm-5.3',                0.90000000::numeric, 1.80000000::numeric),
      ('glm-5.3-flash',          0.30000000::numeric, 0.60000000::numeric),
      ('e2e-openai-load-01',    0.80000000::numeric, 1.60000000::numeric),
      ('e2e-openai-load-02',    1.00000000::numeric, 2.00000000::numeric),
      ('e2e-anthropic-load-01', 1.10000000::numeric, 2.20000000::numeric),
      ('e2e-aliyun-load-01',    0.70000000::numeric, 1.40000000::numeric),
      ('e2e-moonshot-load-01',  0.60000000::numeric, 1.20000000::numeric)
    ) AS x(code,input_cost,output_cost)
  LOOP
    SELECT id,trim(native_currency) INTO v_sku,v_currency FROM model_sku WHERE sku_code=v_cost.code;
    SELECT count(*) INTO v_count FROM cost_baseline WHERE sku_id=v_sku;
    IF v_count=0 THEN
      INSERT INTO cost_baseline(sku_id,version,currency,primary_supplier_id,
                                loss_rate,channel_rate,calc_snapshot,change_reason,
                                valid_from,valid_to,is_current,created_by)
        VALUES(v_sku,1,v_currency,v_supplier,0,0,
               jsonb_build_object('source','synthetic_fixture_cost','formula_version','e2e-synthetic-v1'),
               'PARAM_CHANGE',TIMESTAMPTZ '2026-09-01 00:00:00+08',NULL,true,v_created_by)
        RETURNING id INTO v_baseline;
      INSERT INTO cost_component(cost_baseline_id,component_type,unit_cost,supplier_cost)
        VALUES(v_baseline,'input',v_cost.input_cost,v_cost.input_cost),
              (v_baseline,'output',v_cost.output_cost,v_cost.output_cost);
    ELSE
      SELECT count(*),min(id) INTO v_current,v_baseline
        FROM cost_baseline WHERE sku_id=v_sku AND is_current;
      IF v_current<>1 THEN
        RAISE EXCEPTION 'SKU % 已有历史 baseline 但没有唯一 current；不覆盖现有成本', v_cost.code;
      END IF;
      IF NOT EXISTS (
        SELECT 1 FROM cost_baseline cb WHERE cb.id=v_baseline
          AND cb.currency=v_currency AND cb.valid_to IS NULL
          AND EXISTS (SELECT 1 FROM cost_component cc
                      WHERE cc.cost_baseline_id=cb.id AND cc.component_type='input'
                        AND cc.unit_cost>0 AND cc.supplier_cost>0)
          AND EXISTS (SELECT 1 FROM cost_component cc
                      WHERE cc.cost_baseline_id=cb.id AND cc.component_type='output'
                        AND cc.unit_cost>0 AND cc.supplier_cost>0)
          AND (v_cost.code NOT LIKE 'e2e-%'
               OR cb.calc_snapshot->>'source'='synthetic_fixture_cost')) THEN
        RAISE EXCEPTION 'SKU % 的现有 current baseline 不满足价目表成本条件', v_cost.code;
      END IF;
    END IF;
  END LOOP;
  IF EXISTS (SELECT 1 FROM model_sku s JOIN cost_baseline cb ON cb.sku_id=s.id AND cb.is_current
             WHERE s.sku_code='e2e-aliyun-no-cost-01') THEN
    RAISE EXCEPTION '边界 SKU e2e-aliyun-no-cost-01 已有 current cost';
  END IF;

  -- 8. Pricing policies. Read the existing business margin floor; do not seed config.
  SELECT config_value INTO v_name FROM sys_config WHERE config_key='min_gross_margin';
  IF v_name IS NULL OR v_name !~ '^[0-9]+(\.[0-9]+)?$' THEN
    RAISE EXCEPTION 'min_gross_margin 缺失或非数字';
  END IF;
  v_margin := v_name::numeric;
  IF v_margin < 0 OR v_margin >= 0.95 THEN
    RAISE EXCEPTION 'min_gross_margin=% 超出 E2E MARGIN 策略可用范围', v_margin;
  END IF;

  -- A priced draft requires a published SKU with a positive representative cost.
  SELECT count(*),min(s.id) INTO v_count,v_sku FROM model_sku s
    JOIN cost_baseline cb ON cb.sku_id=s.id AND cb.is_current
    JOIN LATERAL (SELECT unit_cost FROM cost_component
                  WHERE cost_baseline_id=cb.id
                  ORDER BY CASE WHEN component_type='input' THEN 0 ELSE 1 END,component_type LIMIT 1) cc ON true
    WHERE s.lifecycle_status='PUBLISHED' AND s.native_currency=cb.currency AND cc.unit_cost>0;
  IF v_count=0 THEN
    RAISE EXCEPTION '没有 PUBLISHED 且有正数当前成本的 SKU';
  END IF;

  SELECT count(*) INTO v_count FROM pricing_policy WHERE code='E2E-GOLD-FLOOR';
  IF v_count>1 THEN RAISE EXCEPTION '策略 code=E2E-GOLD-FLOOR 有重复记录'; END IF;
  IF v_count=0 THEN
    INSERT INTO pricing_policy(code,name,scope_type,scope_id,level_code,
                               price_method,param_value,priority,status)
      VALUES('E2E-GOLD-FLOOR','E2E GOLD near floor','SKU',v_sku,'GOLD',
             'MARGIN',round(v_margin+LEAST(0.001,(1-v_margin)/2),6),5,'ACTIVE');
  ELSIF NOT EXISTS (
    SELECT 1 FROM pricing_policy p JOIN model_sku s ON s.id=p.scope_id
      JOIN cost_baseline cb ON cb.sku_id=s.id AND cb.is_current
      JOIN LATERAL (SELECT unit_cost FROM cost_component WHERE cost_baseline_id=cb.id
                    ORDER BY CASE WHEN component_type='input' THEN 0 ELSE 1 END,component_type LIMIT 1) cc ON true
      WHERE p.code='E2E-GOLD-FLOOR' AND p.name='E2E GOLD near floor'
        AND p.scope_type='SKU' AND p.customer_id IS NULL AND p.level_code='GOLD'
        AND p.price_method='MARGIN'
        AND p.param_value=round(v_margin+LEAST(0.001,(1-v_margin)/2),6)
        AND p.rounding_rule='CEIL' AND p.priority=5 AND p.status='ACTIVE'
        AND s.lifecycle_status='PUBLISHED' AND s.native_currency=cb.currency
        AND cc.unit_cost>0) THEN
    RAISE EXCEPTION 'E2E-GOLD-FLOOR 策略关键字段或当前成本冲突';
  END IF;

  -- No unique constraint exists on pricing_policy.code: explicitly reject duplicates.
  FOR v_policy IN
    SELECT * FROM (VALUES
      ('E2E-GOLD-MARGIN','E2E GOLD margin','GOLD'),
      ('E2E-BASIC-MARGIN','E2E BASIC margin','BASIC'),
      ('E2E-SILVER-MARGIN','E2E SILVER margin','SILVER'),
      ('E2E-GLOBAL-MARGIN','E2E GLOBAL margin','GLOBAL')
    ) AS x(code,name,level_code)
  LOOP
    SELECT count(*) INTO v_count FROM pricing_policy WHERE code=v_policy.code;
    IF v_count>1 THEN RAISE EXCEPTION '策略 code=% 有重复记录', v_policy.code; END IF;
    IF v_count=0 THEN
      INSERT INTO pricing_policy(code,name,scope_type,level_code,price_method,param_value,priority,status)
        VALUES(v_policy.code,v_policy.name,'ALL',v_policy.level_code,'MARGIN',
               round(GREATEST(0.25,(v_margin+1)/2),6),10,'ACTIVE');
    ELSIF NOT EXISTS (
      SELECT 1 FROM pricing_policy WHERE code=v_policy.code AND name=v_policy.name
        AND scope_type='ALL' AND scope_id IS NULL AND customer_id IS NULL
        AND level_code=v_policy.level_code AND price_method='MARGIN'
        AND param_value=round(GREATEST(0.25,(v_margin+1)/2),6)
        AND rounding_rule='CEIL' AND priority=10 AND status='ACTIVE') THEN
      RAISE EXCEPTION '策略 code=% 关键字段冲突', v_policy.code;
    END IF;
  END LOOP;
END $$;

-- 9. Read-only summary, inside the same transaction.
WITH fixture_names AS (
  SELECT 'E2E-' || x.level_code || '-' || lpad(n::text,2,'0') AS legal_name
    FROM (VALUES ('GOLD',5),('BASIC',4),('SILVER',4),('GLOBAL',2)) AS x(level_code,total)
    CROSS JOIN LATERAL generate_series(1,x.total) AS n
)
SELECT (SELECT count(*) FROM legal_subject all_ls
          JOIN customer_profile all_cp ON all_cp.subject_id=all_ls.id
          WHERE all_ls.legal_name LIKE 'E2E-%') AS all_e2e_customers,
       count(*) AS target_e2e_customers,
       count(*) FILTER (WHERE cp.level_code='GOLD') AS gold,
       count(*) FILTER (WHERE cp.level_code='BASIC') AS basic,
       count(*) FILTER (WHERE cp.level_code='SILVER') AS silver,
       count(*) FILTER (WHERE cp.level_code='GLOBAL') AS global_count
  FROM fixture_names x JOIN legal_subject ls ON ls.legal_name=x.legal_name
    JOIN customer_profile cp ON cp.subject_id=ls.id;

WITH fixture_names AS (
  SELECT 'E2E-' || x.level_code || '-' || lpad(n::text,2,'0') AS legal_name
    FROM (VALUES ('GOLD',5),('BASIC',4),('SILVER',4),('GLOBAL',2)) AS x(level_code,total)
    CROSS JOIN LATERAL generate_series(1,x.total) AS n
)
SELECT a.login_id,count(*) AS e2e_customers
  FROM fixture_names x JOIN legal_subject ls ON ls.legal_name=x.legal_name
    JOIN customer_profile cp ON cp.subject_id=ls.id
    JOIN account a ON a.owner_id=cp.owner_sales_operator_id
      AND a.portal_type='INTERNAL' AND a.owner_type='STAFF'
  WHERE a.login_id IN ('smoke_sales','smoke_sales_b')
  GROUP BY a.login_id ORDER BY a.login_id;

SELECT (SELECT count(*) FROM vendor) AS total_vendors,
       (SELECT count(*) FROM model_family) AS total_families,
       (SELECT count(*) FROM vendor WHERE code IN ('moonshot','zhipu')) AS fixture_vendors,
       (SELECT count(*) FROM model_family f JOIN vendor v ON v.id=f.vendor_id
         WHERE (v.code='moonshot' AND f.name='Kimi K3')
            OR (v.code='zhipu' AND f.name='GLM-5.3')) AS fixture_families;

SELECT s.id,s.sku_code,v.name AS vendor,f.name AS family,s.lifecycle_status,
       trim(s.native_currency) AS currency,(cb.id IS NOT NULL) AS has_current_cost,
       (cb.calc_snapshot->>'source'='synthetic_fixture_cost') AS synthetic_fixture_cost
  FROM model_sku s JOIN vendor v ON v.id=s.vendor_id
    JOIN model_family f ON f.id=s.family_id
    LEFT JOIN cost_baseline cb ON cb.sku_id=s.id AND cb.is_current
  WHERE s.lifecycle_status='PUBLISHED' AND cb.id IS NOT NULL
    AND EXISTS (SELECT 1 FROM cost_component cc WHERE cc.cost_baseline_id=cb.id
                AND cc.component_type='input' AND cc.unit_cost>0 AND cc.supplier_cost>0)
  ORDER BY v.name,f.name,s.sku_code;

SELECT s.id,s.sku_code,v.name AS vendor,f.name AS family,s.lifecycle_status,
       (cb.id IS NOT NULL) AS has_current_cost
  FROM model_sku s JOIN vendor v ON v.id=s.vendor_id
    JOIN model_family f ON f.id=s.family_id
    LEFT JOIN cost_baseline cb ON cb.sku_id=s.id AND cb.is_current
  WHERE s.sku_code IN ('e2e-openai-draft-01','e2e-anthropic-deprecating-01',
                       'e2e-aliyun-no-cost-01')
  ORDER BY s.sku_code;

SELECT id,code,level_code,scope_type,scope_id,price_method,param_value,status
  FROM pricing_policy
  WHERE code IN ('E2E-GOLD-FLOOR','E2E-GOLD-MARGIN','E2E-BASIC-MARGIN',
                 'E2E-SILVER-MARGIN','E2E-GLOBAL-MARGIN') AND status='ACTIVE'
  ORDER BY code;
COMMIT;
