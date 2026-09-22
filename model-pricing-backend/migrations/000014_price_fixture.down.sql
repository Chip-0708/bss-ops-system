-- 000014_price_fixture.down：按 sku_code / code 删除（不用 id）。
-- 依赖顺序：组件 → 版本 → SKU → 系列 → 厂商。

DELETE FROM price_component WHERE price_version_id IN (
  SELECT id FROM price_version WHERE sku_id IN (
    SELECT id FROM model_sku WHERE sku_code IN (
      'gpt-5-2026-04-11','claude-opus-4-2026-05','qwen3-max-2026-06','qwen3-embedding-2026-03')
  )
);
DELETE FROM price_version WHERE sku_id IN (
  SELECT id FROM model_sku WHERE sku_code IN (
    'gpt-5-2026-04-11','claude-opus-4-2026-05','qwen3-max-2026-06','qwen3-embedding-2026-03')
);
DELETE FROM model_sku WHERE sku_code IN (
  'gpt-5-2026-04-11','claude-opus-4-2026-05','qwen3-max-2026-06','qwen3-embedding-2026-03'
);
DELETE FROM model_family WHERE name IN ('GPT-5','Claude Opus 4','Qwen3 Max');
DELETE FROM vendor WHERE code IN ('OPENAI','ANTHROPIC','ALIBABA');
