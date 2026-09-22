import test from "node:test";
import assert from "node:assert/strict";
import { createMock } from "../src/mock.ts";
import { modelOptions, queryModelCatalog } from "../src/mocks/data/modelCatalog.ts";
import { modelContractId, toModelListItem } from "../src/api/models.catalog.ts";
import type { Model } from "../src/types.ts";
import type { ModelSkuContractDTO, ModelFamilyContractDTO, ModelContractListParams } from "../src/api/models.types.ts";

const models = () => createMock().handle({ path: "/api/internal/models", method: "GET", role: "PRICING_OP" }) as Model[];

test("catalog paginates SKUs and complete families using contract fields", () => {
  const data = models();
  const skuPage = queryModelCatalog(data, { view: "sku", page: 2, size: 2 });
  assert.equal(skuPage.total, 6);
  assert.equal((skuPage.list[0] as ModelSkuContractDTO).sku_code, data[2]!.code);
  const familyPage = queryModelCatalog(data, { view: "family", size: 1 });
  assert.equal(familyPage.total, 3);
  const family = familyPage.list[0] as ModelFamilyContractDTO;
  assert.equal(family.sku_count, 2);
  assert.equal(family.children.length, 2);
  for (const sku of family.children) {
    assert.equal(String(sku.family_id), String(family.family_id));
    assert.equal("input" in sku, false);
    assert.equal("cost" in sku, false);
    assert.equal("verification" in sku, false);
    assert.equal("protocol" in sku, false);
    assert.equal("name" in sku, false);
    assert.equal(Array.isArray(sku.capability), false);
  }
});

test("catalog filters by IDs, keyword, type, status and tier before pagination", () => {
  const data = models();
  const options = modelOptions(data);
  const vendor = options.vendors.find((item) => item.name === "OpenAI")!;
  const family = options.families.find((item) => item.vendor_id === vendor.id)!;
  const page = queryModelCatalog(data, { view: "sku", vendor_id: vendor.id, family_id: family.id, keyword: "mini", model_type: "对话", lifecycle_status: "PENDING_VERIFY", tier_tag: "经济", size: 1 });
  assert.equal(page.total, 1);
  assert.equal((page.list[0] as ModelSkuContractDTO).sku_code, "gpt-4.1-mini");
  assert.equal(queryModelCatalog(data, { keyword: "not-found" }).total, 0);
  assert.deepEqual(modelOptions([...data].reverse()).vendors.sort((a, b) => a.id.localeCompare(b.id)), [...options.vendors].sort((a, b) => a.id.localeCompare(b.id)));
});

test("catalog rejects invalid query and preserves ID/null fields without inventing metadata", () => {
  for (const params of [{ page: 0 }, { size: 101 }, { keyword: "x".repeat(65) }, { view: "bad" }, { vendor_id: "abc" }, { search: "legacy" }]) {
    assert.throws(() => queryModelCatalog(models(), params as ModelContractListParams));
  }
  const sku = queryModelCatalog(models(), { view: "sku" }).list[0] as ModelSkuContractDTO;
  const row = toModelListItem({ ...sku, capability: null, context_window: null, tier_tag: null });
  assert.equal(row.id, sku.id);
  assert.equal(row.context, null);
  assert.equal(row.tier, null);
  assert.equal(row.capabilitiesProvided, false);
  assert.deepEqual(row.capabilities, []);
  assert.deepEqual(toModelListItem({ ...sku, aliases: null }).aliases, []);
  assert.equal("input" in row, false);
  assert.equal("protocol" in row, false);
  assert.throws(() => modelContractId(9007199254740992), /安全范围/);
});

test("model reads ignore unknown capability fields but keep known-field validation", () => {
  const sku = queryModelCatalog(models(), { view: "sku" }).list[0] as ModelSkuContractDTO;
  const capability = { vision: true, future_capability: "server-only" } as ModelSkuContractDTO["capability"];
  const row = toModelListItem({ ...sku, capability });
  assert.deepEqual(row.capability, { vision: true });
  assert.deepEqual(row.capabilities, ["视觉"]);
  assert.throws(() => toModelListItem({ ...sku, capability: { vision: "yes" } as unknown as ModelSkuContractDTO["capability"] }), /非布尔值/);
});
