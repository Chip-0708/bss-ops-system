-- 000014_price_fixture：官方价开发种子。
-- 000013 只造了供应商，官方价表是空的 → 报价的「倍率⇄价格双向自洽复核」没有基准可校，
-- 5a-2 的提交链路无法联调。这里补齐：3 个有官方价的 PUBLISHED SKU + 1 个无官方价的
-- （用于验证"强制绝对价模式"分支）+ 1 个 CNY 模型（验证汇率档位分支）。
-- 幂等：全部 ON CONFLICT DO NOTHING；id 一律子查询承接，不硬编码。

DO $$
DECLARE
  v_openai   bigint;
  v_anthro   bigint;
  v_alibaba  bigint;
  f_gpt5     bigint;
  f_opus     bigint;
  f_qwen     bigint;
  s_gpt5     bigint;
  s_claude   bigint;
  s_qwen     bigint;
  s_absol    bigint;
  pv         bigint;
BEGIN
  -- 厂商
  INSERT INTO vendor(code, name, country, updated_at, request_id, created_by, updated_by)
  VALUES ('OPENAI','OpenAI','US',now(),NULL,1,1)
  ON CONFLICT (code) DO NOTHING;
  INSERT INTO vendor(code, name, country, updated_at, request_id, created_by, updated_by)
  VALUES ('ANTHROPIC','Anthropic','US',now(),NULL,1,1)
  ON CONFLICT (code) DO NOTHING;
  INSERT INTO vendor(code, name, country, updated_at, request_id, created_by, updated_by)
  VALUES ('ALIBABA','阿里云','CN',now(),NULL,1,1)
  ON CONFLICT (code) DO NOTHING;

  SELECT id INTO v_openai  FROM vendor WHERE code='OPENAI';
  SELECT id INTO v_anthro  FROM vendor WHERE code='ANTHROPIC';
  SELECT id INTO v_alibaba FROM vendor WHERE code='ALIBABA';

  -- 系列（model_family 无唯一约束，用 WHERE NOT EXISTS 保证幂等）
  INSERT INTO model_family(vendor_id, name, updated_at, request_id, created_by, updated_by)
  SELECT v_openai,'GPT-5',now(),NULL,1,1
  WHERE NOT EXISTS (SELECT 1 FROM model_family WHERE vendor_id=v_openai AND name='GPT-5');
  SELECT id INTO f_gpt5 FROM model_family WHERE vendor_id=v_openai AND name='GPT-5';

  INSERT INTO model_family(vendor_id, name, updated_at, request_id, created_by, updated_by)
  SELECT v_anthro,'Claude Opus 4',now(),NULL,1,1
  WHERE NOT EXISTS (SELECT 1 FROM model_family WHERE vendor_id=v_anthro AND name='Claude Opus 4');
  SELECT id INTO f_opus FROM model_family WHERE vendor_id=v_anthro AND name='Claude Opus 4';

  INSERT INTO model_family(vendor_id, name, updated_at, request_id, created_by, updated_by)
  SELECT v_alibaba,'Qwen3 Max',now(),NULL,1,1
  WHERE NOT EXISTS (SELECT 1 FROM model_family WHERE vendor_id=v_alibaba AND name='Qwen3 Max');
  SELECT id INTO f_qwen FROM model_family WHERE vendor_id=v_alibaba AND name='Qwen3 Max';

  -- SKU（全部 PUBLISHED，供应商门户可见可报价）
  INSERT INTO model_sku(vendor_id, family_id, sku_code, model_type, native_currency,
                        context_window, capability, verify_status, tier_tag,
                        lifecycle_status, updated_at, request_id, created_by, updated_by)
  VALUES (v_openai, f_gpt5, 'gpt-5-2026-04-11', '对话', 'USD', 400000,
          '{"function_call":true,"vision":true,"reasoning":true,"streaming":true,"max_output_tokens":32768}'::jsonb,
          'MANUAL', '旗舰', 'PUBLISHED', now(), NULL, 1, 1)
  ON CONFLICT (sku_code) DO NOTHING;
  SELECT id INTO s_gpt5 FROM model_sku WHERE sku_code='gpt-5-2026-04-11';

  INSERT INTO model_sku(vendor_id, family_id, sku_code, model_type, native_currency,
                        context_window, capability, verify_status, tier_tag,
                        lifecycle_status, updated_at, request_id, created_by, updated_by)
  VALUES (v_anthro, f_opus, 'claude-opus-4-2026-05', '推理', 'USD', 200000,
          '{"function_call":true,"vision":true,"reasoning":true,"streaming":true,"max_output_tokens":64000}'::jsonb,
          'MANUAL', '旗舰', 'PUBLISHED', now(), NULL, 1, 1)
  ON CONFLICT (sku_code) DO NOTHING;
  SELECT id INTO s_claude FROM model_sku WHERE sku_code='claude-opus-4-2026-05';

  INSERT INTO model_sku(vendor_id, family_id, sku_code, model_type, native_currency,
                        context_window, capability, verify_status, tier_tag,
                        lifecycle_status, updated_at, request_id, created_by, updated_by)
  VALUES (v_alibaba, f_qwen, 'qwen3-max-2026-06', '对话', 'CNY', 131072,
          '{"function_call":true,"streaming":true,"max_output_tokens":16384}'::jsonb,
          'MANUAL', '主力', 'PUBLISHED', now(), NULL, 1, 1)
  ON CONFLICT (sku_code) DO NOTHING;
  SELECT id INTO s_qwen FROM model_sku WHERE sku_code='qwen3-max-2026-06';

  -- 第 4 个 SKU：**故意不给官方价**，用于验证「强制绝对价模式」（multiplier 必须为 null）
  INSERT INTO model_sku(vendor_id, family_id, sku_code, model_type, native_currency,
                        context_window, capability, verify_status, tier_tag,
                        lifecycle_status, updated_at, request_id, created_by, updated_by)
  VALUES (v_alibaba, f_qwen, 'qwen3-embedding-2026-03', '向量', 'CNY', 8192,
          '{"embedding":true,"streaming":false}'::jsonb,
          'UNVERIFIED', '经济', 'PUBLISHED', now(), NULL, 1, 1)
  ON CONFLICT (sku_code) DO NOTHING;
  SELECT id INTO s_absol FROM model_sku WHERE sku_code='qwen3-embedding-2026-03';

  -- 官方价：gpt-5（USD，NET）
  INSERT INTO price_version(sku_id, version_no, currency, tax_basis, effective_from,
                            is_current, source, created_by, updated_at, request_id, updated_by)
  VALUES (s_gpt5, 1, 'USD', 'NET', now() - interval '30 days', true, 'MANUAL', 1, now(), NULL, 1)
  ON CONFLICT (sku_id, version_no) DO NOTHING;
  SELECT id INTO pv FROM price_version WHERE sku_id=s_gpt5 AND version_no=1;
  INSERT INTO price_component(price_version_id, component_type, unit_price, updated_at, request_id, created_by, updated_by)
  VALUES (pv,'input',2.50000000,now(),NULL,1,1),
         (pv,'output',10.00000000,now(),NULL,1,1),
         (pv,'cached_input',1.25000000,now(),NULL,1,1)
  ON CONFLICT (price_version_id, component_type) DO NOTHING;

  -- 官方价：claude-opus-4（USD，NET，含缓存写入组件）
  INSERT INTO price_version(sku_id, version_no, currency, tax_basis, effective_from,
                            is_current, source, created_by, updated_at, request_id, updated_by)
  VALUES (s_claude, 1, 'USD', 'NET', now() - interval '30 days', true, 'MANUAL', 1, now(), NULL, 1)
  ON CONFLICT (sku_id, version_no) DO NOTHING;
  SELECT id INTO pv FROM price_version WHERE sku_id=s_claude AND version_no=1;
  INSERT INTO price_component(price_version_id, component_type, unit_price, updated_at, request_id, created_by, updated_by)
  VALUES (pv,'input',15.00000000,now(),NULL,1,1),
         (pv,'output',75.00000000,now(),NULL,1,1),
         (pv,'cached_input',1.50000000,now(),NULL,1,1),
         (pv,'cache_write_5m',18.75000000,now(),NULL,1,1)
  ON CONFLICT (price_version_id, component_type) DO NOTHING;

  -- 官方价：qwen3-max（CNY，GROSS，含税 → 验证 CNY 模型不出现汇率档位）
  INSERT INTO price_version(sku_id, version_no, currency, tax_basis, effective_from,
                            is_current, source, created_by, updated_at, request_id, updated_by)
  VALUES (s_qwen, 1, 'CNY', 'GROSS', now() - interval '30 days', true, 'MANUAL', 1, now(), NULL, 1)
  ON CONFLICT (sku_id, version_no) DO NOTHING;
  SELECT id INTO pv FROM price_version WHERE sku_id=s_qwen AND version_no=1;
  INSERT INTO price_component(price_version_id, component_type, unit_price, updated_at, request_id, created_by, updated_by)
  VALUES (pv,'input',8.00000000,now(),NULL,1,1),
         (pv,'output',32.00000000,now(),NULL,1,1)
  ON CONFLICT (price_version_id, component_type) DO NOTHING;

  -- s_absol 刻意不插 price_version。
END $$;
