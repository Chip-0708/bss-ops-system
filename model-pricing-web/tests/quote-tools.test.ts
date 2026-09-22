import test from "node:test";
import assert from "node:assert/strict";
import { effectiveTimeWillClamp, filterPreviewByScope, importedPrice, includedPreviewItems, quoteScopeOptions } from "../src/domain/quoteImport.ts";

test("quote import derives stable vendor and family selectors from supplier SKUs", () => {
  const base = { model_name: "Model", model_type: "对话", native_currency: "CNY", context_window: null, tier_tag: null,
    has_official_price: false, official_price: null };
  const options = quoteScopeOptions([
    { ...base, id: 1, sku_code: "a-2", vendor_id: 10, vendor_name: "厂商甲", family_id: 101, family_name: "系列二" },
    { ...base, id: 2, sku_code: "a-1", vendor_id: 10, vendor_name: "厂商甲", family_id: 100, family_name: "系列一" },
    { ...base, id: 3, sku_code: "b-1", vendor_id: "20", vendor_name: "厂商乙", family_id: "200", family_name: "系列一" },
  ]);
  assert.deepEqual(options.vendors.map(item => [String(item.id), item.name]), [["10", "厂商甲"], ["20", "厂商乙"]]);
  assert.deepEqual(options.families.map(item => `${item.id}:${item.vendorId}:${item.name}`).sort(),
    ["100:10:系列一", "101:10:系列二", "200:20:系列一"]);
});

test("quote import filters a mixed CSV preview by the selected vendor or family", () => {
  const base = { model_name: "Model", model_type: "对话", native_currency: "CNY", context_window: null, tier_tag: null,
    has_official_price: false, official_price: null };
  const skus = [
    { ...base, id: 1, sku_code: "a-1", vendor_id: 10, vendor_name: "厂商甲", family_id: 100, family_name: "系列一" },
    { ...base, id: 2, sku_code: "a-2", vendor_id: 10, vendor_name: "厂商甲", family_id: 101, family_name: "系列二" },
    { ...base, id: 3, sku_code: "b-1", vendor_id: 20, vendor_name: "厂商乙", family_id: 200, family_name: "系列三" },
  ];
  const preview = { token: "digest", total: 3, ok_count: 1, warn_count: 1, error_count: 1,
    rows: [
      { line: 2, sku_id: 1, sku_code: "a-1", level: "OK" as const, messages: [] },
      { line: 3, sku_id: 2, sku_code: "a-2", level: "WARN" as const, messages: ["提示"] },
      { line: 4, sku_id: 3, sku_code: "b-1", level: "ERROR" as const, messages: ["错误"] },
    ],
    preview_items: [
      { sku_id: 1, currency: "CNY", fx_tier: null, constraints: {}, components: [] },
      { sku_id: 2, currency: "CNY", fx_tier: null, constraints: {}, components: [] },
    ] };
  const vendor = filterPreviewByScope(preview, skus, "vendor", "10", "");
  assert.equal(vendor.excluded, 1);
  assert.deepEqual(vendor.preview.rows.map(row => row.sku_code), ["a-1", "a-2"]);
  assert.deepEqual([vendor.preview.total, vendor.preview.ok_count, vendor.preview.warn_count, vendor.preview.error_count], [2, 1, 1, 0]);
  const family = filterPreviewByScope(preview, skus, "family", "10", "101");
  assert.deepEqual(family.preview.rows.map(row => row.sku_code), ["a-2"]);
  assert.deepEqual(family.preview.preview_items.map(item => item.sku_id), [2]);
});

test("quote import submits only non-excluded preview items", () => {
  const items = [
    { sku_id: "9007199254741001", currency: "USD", fx_tier: "6.8", constraints: {}, components: [] },
    { sku_id: 2, currency: "CNY", fx_tier: null, constraints: {}, components: [] },
  ];
  assert.deepEqual(includedPreviewItems(items, new Set(["9007199254741001"])).map(item => String(item.sku_id)), ["2"]);
  assert.equal(includedPreviewItems(items, new Set(["9007199254741001", "2"])).length, 0);
});

test("quote import multiplier uses current official price with fixed precision", () => {
  assert.equal(importedPrice("2.50000000", "0.8"), "2.00000000");
  assert.equal(importedPrice(undefined, "0.8"), "");
  assert.equal(importedPrice("2.5", null), "");
  assert.equal(importedPrice("2.5", "invalid"), "");
});

test("quote submit detects a past effective time before confirmation", () => {
  const now = Date.parse("2026-09-16T08:00:00+08:00");
  assert.equal(effectiveTimeWillClamp("2026-09-15T08:00:00+08:00", now), true);
  assert.equal(effectiveTimeWillClamp("2026-09-17T08:00:00+08:00", now), false);
  assert.equal(effectiveTimeWillClamp("invalid", now), false);
});
import { supplierQuoteRecords } from "../src/mocks/data/supplierQuotes.ts";
import { supplierQuoteRenewalMetadata } from "../src/mocks/data/supplierQuoteRenewal.ts";
import { validateSupplierQuoteConstraints } from "../src/mocks/data/supplierQuoteConstraints.ts";
import { customerQuoteTemplate } from "../src/mocks/data/customerQuoteTemplate.ts";
import { customerSeeds } from "../src/mocks/data/customers.ts";
import { priceBookRecords } from "../src/mocks/data/priceBooks.ts";

test("renewal preserves source and increments only the same supplier quote's version", () => {
  const records = structuredClone(supplierQuoteRecords); const source = records.find(row => row.status === "EFFECTIVE")!;
  const before = structuredClone(records);
  const metadata = supplierQuoteRenewalMetadata(records, source.supplierId, source.id);
  assert.equal(metadata.quoteNo, source.quoteNo); assert.equal(metadata.version, source.version + 1);
  assert.equal(metadata.previousQuoteId, source.id); assert.deepEqual(records, before);
  records.push({ ...structuredClone(source), id: "new-draft", version: metadata.version, status: "DRAFT" });
  assert.equal(supplierQuoteRenewalMetadata(records, source.supplierId, source.id).version, metadata.version + 1);
  assert.throws(() => supplierQuoteRenewalMetadata(records, "supplier-cloud", source.id), /不属于/);
  assert.throws(() => supplierQuoteRenewalMetadata(records, source.supplierId, "new-draft"), /审批中的/);
});

test("supply constraints validate positive integer strings and reject unknown fields", () => {
  validateSupplierQuoteConstraints(undefined); validateSupplierQuoteConstraints({});
  validateSupplierQuoteConstraints({ rpm: "120", tpm: "100000", maxConcurrency: "10", compatibilityNote: "支持工具调用" });
  for (const rpm of ["0", "-1", "1.5", "1e3", "10000000000"]) assert.throws(() => validateSupplierQuoteConstraints({ rpm }), /正整数/);
  assert.throws(() => validateSupplierQuoteConstraints({ rpm: 120 } as never), /正整数/);
  assert.throws(() => validateSupplierQuoteConstraints({ secret: "x" } as never), /不支持/);
  assert.throws(() => validateSupplierQuoteConstraints({ compatibilityNote: "x".repeat(301) }), /300/);
});

test("customer template uses the assigned effective price book and returns independent price strings", () => {
  const books = structuredClone(priceBookRecords); const customers = structuredClone(customerSeeds);
  const now = Date.parse("2026-09-14T10:00:00+08:00");
  const template = customerQuoteTemplate(customers, books, "customer-blue", now);
  assert.equal(template.priceBookCode, customers[0]?.currentPriceBookCode);
  assert.ok(template.items.length); assert.equal(typeof template.items[0]?.unitPrice, "string");
  template.items[0]!.unitPrice = "1";
  assert.notEqual(customerQuoteTemplate(customers, books, "customer-blue", now).items[0]?.unitPrice, "1");
  assert.throws(() => customerQuoteTemplate(customers, books, "customer-medical", now), /冻结/);
  assert.throws(() => customerQuoteTemplate(customers, books, "customer-lighthouse", now), /不可用/);
  const book = books.find(row => row.code === template.priceBookCode)!;
  book.status = "APPROVING"; assert.throws(() => customerQuoteTemplate(customers, books, "customer-blue", now), /不可用/);
  book.status = "EFFECTIVE"; book.effectiveFrom = "2099-01-01T00:00:00+08:00";
  assert.throws(() => customerQuoteTemplate(customers, books, "customer-blue", now), /不可用/);
});
