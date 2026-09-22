import { test } from "node:test";
import assert from "node:assert/strict";
import { begin, current, finish, advance, pending, discardPending } from "../src/e2e/priceBookQuote";

test("PRICE_BOOK_QUOTE 保留旧 Run，未知结果重试同一创建，且只推进匹配对象", () => {
  const data = new Map<string, string>();
  const storage = { getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => { data.set(key, value); },
    removeItem: (key: string) => { data.delete(key); } };
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
  const fixture = { customer_id: "2", policy_id: "3", sku_ids: ["4"], currency: "USD" };
  const first = begin("admin", fixture);
  assert.equal(begin("admin", fixture).operation_id, first.operation_id);
  assert.equal(current(), null);
  assert.throws(() => finish({ draftId: "25", levelCode: "GOLD", currency: "USD", itemCount: 0,
    blockedCount: 0, diffReport: [] }, first));
  assert.equal(current(), null);
  const old = finish({ draftId: "25", levelCode: "GOLD", currency: "USD", itemCount: 1,
    blockedCount: 0, diffReport: [] }, first);
  assert.equal(old.price_book_id, "25");
  const retry = begin("admin", fixture);
  assert.equal(pending()?.operation_id, retry.operation_id);
  assert.equal(current()?.run_id, old.run_id);
  assert.throws(() => begin("buyer", fixture));
  discardPending();
  const second = begin("admin", fixture);
  finish({ draftId: "26", levelCode: "GOLD", currency: "USD", itemCount: 1,
    blockedCount: 0, diffReport: [] }, second);
  assert.notEqual(current()?.run_id, old.run_id);
  assert.equal(advance("CREATED", "PUBLISHED", run => run.price_book_id === "25"), null);
  assert.equal(current()?.stage, "CREATED");
  advance("CREATED", "PUBLISHED", run => run.price_book_id === "26", { change_request_id: "32" });
  assert.equal(advance("PUBLISHED", "PRICING_APPROVED", run => run.change_request_id === "31"), null);
  assert.equal(current()?.stage, "PUBLISHED");
});
