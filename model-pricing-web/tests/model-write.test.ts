import test from "node:test";
import assert from "node:assert/strict";
import { createMock } from "../src/mock.ts";
import { modelOptions, modelSku } from "../src/mocks/data/modelCatalog.ts";
import type { Model } from "../src/types.ts";
import type { CreateModelSkuRequest, ModelSkuContractDTO } from "../src/api/models.types.ts";

function setup() {
  const db = createMock();
  const list = () => db.handle({ path: "/api/internal/models", method: "GET", role: "MODEL_OPS" }) as Model[];
  const options = modelOptions(list());
  const family = options.families[0]!;
  const payload: CreateModelSkuRequest = { vendor_id: family.vendor_id, family_id: family.id,
    sku_code: "wire-created", model_type: "多模态", native_currency: "USD", context_window: 0,
    capability: { vision: true, function_call: true, streaming: false, max_output_tokens: 32768 }, aliases: ["wire-alias"] };
  let counter = 0;
  const send = (body: unknown, id?: string, key?: string, role: "MODEL_OPS" | "VIEWER" = "MODEL_OPS") =>
    db.handle({ path: "/api/internal/models", method: id ? "PUT" : "POST", role,
      body: id ? { ...(body as object), id } : body, key: key ?? String(++counter) });
  return { db, list, payload, options, send };
}

test("wire create returns a full draft SKU and preserves zero context, currency and capability precision", () => {
  const { send, payload, list } = setup();
  const created = send(payload, undefined, "create-once") as ModelSkuContractDTO;
  assert.equal(created.lifecycle_status, "DRAFT");
  assert.equal(created.native_currency, "USD");
  assert.equal(created.context_window, 0);
  assert.deepEqual(created.capability, payload.capability);
  assert.deepEqual(created.aliases, ["wire-alias"]);
  assert.equal(typeof created.id, "string");
  assert.equal("input" in created, false);
  assert.equal("protocol" in created, false);
  assert.equal("name" in created, false);
  assert.deepEqual(send(payload, undefined, "create-once"), created);
  assert.equal(list().filter((model) => model.code === payload.sku_code).length, 1);
  assert.deepEqual(modelSku(list().find((model) => model.id === created.id)!), created);
});

test("optional write fields remain null and omitted update fields preserve existing capability and aliases", () => {
  const { send, payload, list } = setup();
  const minimal = { vendor_id: payload.vendor_id, family_id: payload.family_id, sku_code: "minimal-sku", model_type: "对话", native_currency: "CNY" };
  const created = send(minimal) as ModelSkuContractDTO;
  assert.equal(created.context_window, null);
  assert.equal(created.capability, null);
  assert.equal(created.tier_tag, null);
  const populated = send(payload) as ModelSkuContractDTO;
  const updated = send({ sku_code: "wire-renamed", model_type: "推理", native_currency: "CNY" }, String(populated.id)) as ModelSkuContractDTO;
  assert.equal(updated.id, populated.id);
  assert.equal(updated.native_currency, "CNY");
  assert.equal(updated.lifecycle_status, "DRAFT");
  assert.deepEqual(updated.capability, payload.capability);
  assert.deepEqual(updated.aliases, payload.aliases);
  assert.deepEqual(modelSku(list().find((model) => model.id === populated.id)!), updated);
});

test("wire validation rejects invalid ownership, duplicate identifiers and unknown fields without partial writes", () => {
  const { send, payload, list, options } = setup();
  const original = list();
  const otherFamily = options.families.find((family) => family.vendor_id !== String(payload.vendor_id))!;
  for (const invalid of [
    { ...payload, family_id: otherFamily.id }, { ...payload, sku_code: original[0]!.code },
    { ...payload, aliases: [original[0]!.code] }, { ...payload, context_window: -1 },
    { ...payload, native_currency: "usd" }, { ...payload, capability: { unknown_key: true } },
    { ...payload, capability: { vision: "true" } }, { ...payload, capability: { max_output_tokens: 1.5 } },
    { ...payload, lifecycle_status: "PUBLISHED" }, { ...payload, aliases: ["same", "SAME"] },
  ]) {
    assert.throws(() => send(invalid));
    assert.deepEqual(list(), original);
  }
  assert.throws(() => send(payload, undefined, undefined, "VIEWER"), /只读|权限|仅限/);
  const current = original[3]!;
  const update = { sku_code: current.code, model_type: current.type, native_currency: current.currency };
  assert.throws(() => send({ ...update, vendor_id: payload.vendor_id }, current.id), /不允许/);
  assert.throws(() => send({ ...update, lifecycle_status: "OFFLINE" }, current.id), /不允许/);
  assert.deepEqual(list(), original);
  const updated = send({ ...update, capability: { audio: true, max_output_tokens: null } }, current.id) as ModelSkuContractDTO;
  assert.equal(updated.lifecycle_status, "PUBLISHED");
  assert.deepEqual(updated.capability, { audio: true, max_output_tokens: null });
});
