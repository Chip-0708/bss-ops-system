import test from "node:test";
import assert from "node:assert/strict";
import { createCustomerPortalMock } from "../src/mocks/data/customerPortal.ts";
import { customerQuoteRecords } from "../src/mocks/data/customers.ts";
test("customer acceptance can share its receipt with internal queries without altering quote status", () => {
  const receipts = new Map<string, string>(); const records = structuredClone(customerQuoteRecords);
  const db = createCustomerPortalMock(records, () => Date.parse("2026-09-13T10:00:00+08:00"), receipts);
  const id = "cq-blue-formal-demo";
  db.accept("CUSTOMER", id, "shared-receipt", {});
  assert.ok(receipts.get(id)); assert.equal(records.find(row => row.id === id)?.status, "FORMAL");
  db.accept("CUSTOMER", id, "shared-receipt", {}); assert.equal(receipts.size, 1);
});
import type { LegacyPortalCustomerQuote } from "../src/api/customerPortal.types";

const params = { page: 1, size: 100 };
const fixedNow = () => Date.parse("2026-09-13T00:00:00+08:00");
test("customer queries filter drafts and foreign records and project out internal fields", () => {
  const records = structuredClone(customerQuoteRecords), db = createCustomerPortalMock(records, fixedNow);
  assert.throws(() => db.list("VIEWER", "quotes", params), /无权/);
  assert.throws(() => db.home(null), /无权/);
  const page = db.list("CUSTOMER", "quotes", params);
  assert.ok(page.list.length >= 2);
  assert.ok(page.list.every(item => item.kind === "quotes" && item.status !== "DRAFT"));
  for (const record of records.filter(item => item.customerId !== "customer-blue" || item.status === "DRAFT"))
    assert.throws(() => db.get("CUSTOMER", "quotes", record.id), /不可见/);
  const serialized = JSON.stringify(page);
  for (const key of ["floorPrice", "referencePrice", "cost", "margin", "reason", "ownerName", "canEdit", "customerId"])
    assert.equal(serialized.includes(`"${key}"`), false);
  assert.equal(db.list("CUSTOMER", "quotes", { ...params, search: "not-found" }).total, 0);
  assert.equal(db.list("CUSTOMER", "quotes", { page: 1, size: 1 }).list.length, 1);
  assert.throws(() => db.list("CUSTOMER", "quotes", { page: 0, size: 1 }), /分页/);
  assert.throws(() => db.list("CUSTOMER", "billing", { page: 1, size: 101 }), /分页/);
});

test("quote acceptance is time checked and idempotent without changing lifecycle or creating contracts", () => {
  const records = structuredClone(customerQuoteRecords), before = structuredClone(records);
  const db = createCustomerPortalMock(records, fixedNow), id = "cq-blue-formal-demo";
  const contracts = db.list("CUSTOMER", "contracts", params);
  assert.equal((db.get("CUSTOMER", "quotes", id) as LegacyPortalCustomerQuote).canAccept, true);
  for (const [identity, key, body] of [["PRICING_OP", "a", {}], ["CUSTOMER", null, {}], ["CUSTOMER", "a", { status: "CONTRACT" }], ["CUSTOMER", "a", []]] as const)
    assert.throws(() => db.accept(identity, id, key, body));
  assert.deepEqual(records, before);
  const accepted = db.accept("CUSTOMER", id, "once", {});
  assert.deepEqual(db.accept("CUSTOMER", id, "once", {}), accepted);
  const latest = db.get("CUSTOMER", "quotes", id) as LegacyPortalCustomerQuote;
  assert.equal(latest.canAccept, false);
  assert.equal(latest.acceptedAt, accepted.acceptedAt);
  assert.equal(latest.status, "FORMAL");
  assert.throws(() => db.accept("CUSTOMER", id, "new", {}), /尚未接受/);
  assert.throws(() => db.accept("CUSTOMER", "cq-1003", "once", {}), /幂等/);
  assert.deepEqual(db.list("CUSTOMER", "contracts", params), contracts);
  assert.deepEqual(records, before);
});

test("future or expired formal quotes cannot be accepted, read-only pages return independent string-money samples", () => {
  const records = structuredClone(customerQuoteRecords), id = "cq-blue-formal-demo";
  const quote = records.find(item => item.id === id)!;
  quote.validFrom = "2026-10-01T00:00:00+08:00";
  const db = createCustomerPortalMock(records, fixedNow);
  assert.throws(() => db.accept("CUSTOMER", id, "future", {}), /有效期/);
  quote.validFrom = "2026-09-01T00:00:00+08:00"; quote.validTo = "2026-09-13T00:00:00+08:00";
  assert.throws(() => db.accept("CUSTOMER", id, "expired", {}), /有效期/);
  for (const section of ["price-book", "contracts", "billing", "notifications"] as const) {
    const page = db.list("CUSTOMER", section, params);
    assert.ok(page.list.length);
    const original = db.get("CUSTOMER", section, page.list[0]!.id);
    page.list[0]!.name = "changed";
    assert.deepEqual(db.get("CUSTOMER", section, original.id), original);
    if (original.kind === "billing") assert.equal(typeof original.totalAmount, "string");
    if (original.kind === "price-book") assert.equal(typeof original.items[0]!.unitPrice, "string");
  }
  assert.equal(typeof db.home("CUSTOMER").account.balance, "string");
});
