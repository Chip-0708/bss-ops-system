import test from "node:test";
import assert from "node:assert/strict";
import { createMock } from "../src/mock.ts";
import { modelDetail } from "../src/mocks/data/modelCatalog.ts";
import { toModelDetailItem } from "../src/api/models.catalog.ts";
import type { Model } from "../src/types.ts";
import type { ModelDetailDTO } from "../src/api/models.types.ts";

test("detail separates SKU from provisional extensions and keeps null, zero and complete capability", () => {
  const db = createMock();
  const model = (db.handle({ path: "/api/internal/models", method: "GET", role: "PRICING_OP" }) as Model[])[0]!;
  model.context = 0;
  model.capability = { vision: true, streaming: false, max_output_tokens: 0 };
  const response = modelDetail(model);
  assert.deepEqual(Object.keys(response).sort(), ["mock_extensions", "sku"]);
  for (const forbidden of ["input", "output", "cost", "margin", "protocol", "verification"])
    assert.equal(forbidden in response.sku, false);
  const row = toModelDetailItem(response, model.id);
  assert.equal(row.context, 0);
  assert.deepEqual(row.capability, model.capability);
  response.mock_extensions.verification.push({ by: "preview", at: "2026-09-13T00:00:00+08:00", result: "PASS", note: "test" });
  assert.equal(model.verification.length, 0);
  const withoutExtensions = { sku: { ...response.sku, capability: null, context_window: null, tier_tag: null } };
  assert.equal(toModelDetailItem(withoutExtensions, model.id).capabilitiesProvided, false);
  assert.equal(toModelDetailItem(withoutExtensions, model.id).tier, null);
});

test("detail rejects wrong object, malformed capability, states and extensions", () => {
  const db = createMock();
  const model = (db.handle({ path: "/api/internal/models", method: "GET", role: "MODEL_OPS" }) as Model[])[0]!;
  const response = modelDetail(model);
  assert.throws(() => toModelDetailItem(response, "other"), /不一致/);
  const invalid = [
    { ...response, sku: { ...response.sku, capability: { vision: "true" } } },
    { ...response, sku: { ...response.sku, capability: { max_output_tokens: -1 } } },
    { ...response, sku: { ...response.sku, lifecycle_status: "已生效" } },
    { ...response, sku: { ...response.sku, verify_status: "UNKNOWN" } },
    { ...response, mock_extensions: null },
    { ...response, mock_extensions: { ...response.mock_extensions, verification: [{ at: "invalid" }] } },
  ];
  for (const item of invalid) assert.throws(() => toModelDetailItem(item as unknown as ModelDetailDTO, model.id));
});
