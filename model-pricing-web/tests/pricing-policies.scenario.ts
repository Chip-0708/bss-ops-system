import test from "node:test";
import assert from "node:assert/strict";
import { prepareIdempotency } from "../src/api/idempotency";
import { fromPricingPolicyWire, pricingPolicyStatusIdempotency, toPricingPolicyStatusWire, toPricingPolicyWire } from "../src/api/pricingPolicies";
import { DEFAULT_PRICING_POLICY_PRIORITY } from "../src/api/pricingPolicies.types";

test("pricing policy draft maps to the Go wire contract", () => {
  assert.deepEqual(toPricingPolicyWire({
    code: " PP-GOLD-NEW ",
    name: " 金牌客户策略 ",
    strategyType: "TARGET_MARGIN",
    levelCode: "GOLD",
    targetMarginRate: "25.00",
    priority: DEFAULT_PRICING_POLICY_PRIORITY,
  }), {
    code: "PP-GOLD-NEW",
    name: "金牌客户策略",
    scope_type: "ALL",
    scope_id: null,
    level_code: "GOLD",
    price_method: "MARGIN",
    param_value: "0.250000",
    priority: 100,
    status: "DRAFT",
  });

  assert.equal(toPricingPolicyWire({
    code: "PP-OFFICIAL",
    name: "官方价策略",
    strategyType: "OFFICIAL_ANCHOR",
    levelCode: "STANDARD",
    officialMultiplier: "0.95",
    priority: 100,
  }).param_value, "0.950000");
});

test("Go list rows map back to the page model", () => {
  const row = fromPricingPolicyWire({
    id: 12,
    code: "PP-GOLD-NEW",
    name: "金牌客户策略",
    scope_type: "ALL",
    scope_id: null,
    level_code: "GOLD",
    price_method: "MARGIN",
    param_value: "0.250000",
    priority: 100,
    status: "DRAFT",
  });
  assert.equal(row.id, "12");
  assert.equal(row.strategyType, "TARGET_MARGIN");
  assert.equal(row.targetMarginRate, "25.00");
  assert.equal(row.canEdit, true);
});

test("pricing policy status updates expose only DRAFT to ACTIVE and ACTIVE to ARCHIVED", () => {
  const draft = fromPricingPolicyWire({
    id: 12,
    code: "PP-GOLD-NEW",
    name: "金牌客户策略",
    scope_type: "ALL",
    scope_id: null,
    level_code: "GOLD",
    price_method: "MARGIN",
    param_value: "0.250000",
    priority: 100,
    status: "DRAFT",
  });
  const activeWire = toPricingPolicyStatusWire(draft, "ACTIVE");
  assert.equal(activeWire.status, "ACTIVE");
  assert.equal(activeWire.param_value, "0.250000");

  const active = { ...draft, status: "ACTIVE" as const, canEdit: false };
  assert.equal(toPricingPolicyStatusWire(active, "ARCHIVED").status, "ARCHIVED");
  assert.throws(() => toPricingPolicyStatusWire(draft, "ARCHIVED"));
  assert.throws(() => toPricingPolicyStatusWire(active, "ACTIVE"));

  const archived = { ...draft, status: "ARCHIVED" as const, canEdit: false };
  assert.throws(() => toPricingPolicyStatusWire(archived, "ACTIVE"));
  assert.throws(() => toPricingPolicyStatusWire(archived, "ARCHIVED"));

  const scoped = { ...draft, scopeType: "SKU" as const, scopeId: "42" };
  assert.equal(toPricingPolicyStatusWire(scoped, "ACTIVE").scope_id, 42);
});

test("pricing policy idempotency key belongs to one exact status transition", async () => {
  const entries = new Map<string, string>();
  const storage = {
    getItem: (key: string) => entries.get(key) ?? null,
    setItem: (key: string, value: string) => { entries.set(key, value); },
    removeItem: (key: string) => { entries.delete(key); },
  };
  const draft = fromPricingPolicyWire({
    id: 12,
    code: "PP-GOLD-NEW",
    name: "金牌客户策略",
    scope_type: "ALL",
    scope_id: null,
    level_code: "GOLD",
    price_method: "MARGIN",
    param_value: "0.250000",
    priority: 100,
    status: "DRAFT",
  });
  const activate = pricingPolicyStatusIdempotency(draft, "ACTIVE").options;
  const first = await prepareIdempotency(storage, "internal:1", activate, {
    randomUUID: () => "11111111-1111-4111-8111-111111111111",
  });
  const retry = await prepareIdempotency(storage, "internal:1", activate, {
    randomUUID: () => { throw new Error("同一状态变更应复用原 Key"); },
  });

  const active = { ...draft, status: "ACTIVE" as const, canEdit: false };
  const archive = pricingPolicyStatusIdempotency(active, "ARCHIVED").options;
  const nextOperation = await prepareIdempotency(storage, "internal:1", archive, {
    randomUUID: () => "22222222-2222-4222-8222-222222222222",
  });

  assert.equal(activate.scope, "pricing-policy:status:12:DRAFT->ACTIVE");
  assert.equal(archive.scope, "pricing-policy:status:12:ACTIVE->ARCHIVED");
  assert.equal(retry.idempotencyKey, first.idempotencyKey);
  assert.notEqual(nextOperation.idempotencyKey, first.idempotencyKey);
  assert.equal(entries.has(first.storageKey), false);
});
