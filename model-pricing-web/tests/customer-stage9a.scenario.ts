import test from "node:test";
import assert from "node:assert/strict";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createPinia, setActivePinia } from "pinia";
import { customersApi } from "../src/api/customers";
import { customerQuotesApi } from "../src/api/customerQuotes";
import { ApiError } from "../src/domain/common";
import { useSessionStore } from "../src/stores/session";

const entries = new Map<string, string>();
Object.defineProperty(globalThis, "sessionStorage", { configurable: true, value: {
  getItem: (key: string) => entries.get(key) ?? null,
  setItem: (key: string, value: string) => entries.set(key, value),
  removeItem: (key: string) => entries.delete(key),
} });

test("stage 9a customer APIs use the real wire contract, permissions boundary and structured floor errors", async () => {
  setActivePinia(createPinia());
  const session = useSessionStore();
  session.user = { id: "operator-7", name: "test", roleLabel: "custom" };
  session.portal = "internal";
  session.token = "synthetic-customer-token";
  const writes: Array<Record<string, unknown>> = [];
  function auth(request: Request) {
    assert.equal(request.headers.get("Authorization"), "Bearer synthetic-customer-token");
    assert.equal(request.headers.get("X-Mock-Identity"), null);
  }
  const server = setupServer(
    http.get("http://localhost/api/internal/customers", ({ request }) => {
      auth(request);
      const url = new URL(request.url);
      assert.equal(url.searchParams.get("page"), "1");
      assert.equal(url.searchParams.get("size"), "20");
      assert.equal(url.searchParams.get("keyword"), "星河");
      assert.equal(url.searchParams.has("search"), false);
      return HttpResponse.json({ code: 0, message: "ok", requestId: "r-list", data: { list: [{
        id: 3, subject_id: 13, legal_name: "星河科技", level_code: "GOLD", owner_sales_operator_id: 7,
        owner_sales_name: "销售甲", status: "ACTIVE", credit_limit: "1000.00", credit_used: "25.00",
        deposit_amount: "100.00", deposit_status: "PAID", created_at: "2026-09-17T00:00:00Z",
      }], total: 1, page: 1, size: 20 } });
    }),
    http.post("http://localhost/api/internal/customers/:id/transfer", async ({ request, params }) => {
      auth(request); assert.equal(params.id, "3"); assert.ok(request.headers.get("Idempotency-Key"));
      const body = await request.json() as { to_operator_id: number; reason: string; confirm: boolean };
      writes.push(body); assert.deepEqual(body, { to_operator_id: 9, reason: "组织调整", confirm: body.confirm });
      return HttpResponse.json({ code: 0, message: "ok", requestId: "r-transfer", data: body.confirm
        ? { customer_id: 3, from_operator_id: 7, to_operator_id: 9, quotes_migrated: 2, transferred_at: "2026-09-17T01:00:00Z" }
        : { customer_id: 3, from_operator_id: 7, from_operator_name: "销售甲", to_operator_id: 9, to_operator_name: "销售乙", quote_count: 2, price_book_count: 1, reason: body.reason } });
    }),
    http.post("http://localhost/api/internal/customer-quotes", async ({ request }) => {
      auth(request); assert.ok(request.headers.get("Idempotency-Key"));
      const body = await request.json() as Record<string, unknown>;
      writes.push(body);
      if (body.customer_id === 99) return HttpResponse.json({ code: 10005, message: "客户报价低于 floor（违规 1 项）", requestId: "r-floor", data: {
        floor_violations: [{ sku_id: 40, sku_code: "gpt-test", unit_price: "0.5", floor_price: "1.0" }],
      } }, { status: 409 });
      return HttpResponse.json({ code: 0, message: "ok", requestId: "r-quote", data: {
        id: 21, customer_id: body.customer_id, version_no: 2, status: "DRAFT", quote_type: body.quote_type,
        item_count: Array.isArray(body.items) ? body.items.length : 3, below_floor_count: 0, price_book_version: 4,
        valid_until: body.valid_to, owner_sales_operator_id: 7, created_at: "2026-09-17T01:00:00Z",
      } });
    }),
  );
  server.listen({ onUnhandledRequest: "error" });
  try {
    const page = await customersApi.list({ page: 1, size: 20, keyword: "星河" });
    assert.equal(page.list[0]?.id, "3"); assert.equal(page.list[0]?.ownerSalesOperatorId, "7");
    const impact = await customersApi.transferPreview("3", { toOperatorId: "9", reason: "组织调整" });
    assert.equal(impact.toOperatorName, "销售乙");
    const transferred = await customersApi.transferConfirm("3", { toOperatorId: "9", reason: "组织调整" });
    assert.equal(transferred.quotesMigrated, 2);

    const applied = await customerQuotesApi.generate({ customerId: "3", quoteType: "APPLY" });
    assert.equal(applied.id, "21"); assert.equal(applied.quoteType, "APPLY");
    await customerQuotesApi.generate({ customerId: "3", quoteType: "CLONE", sourceQuoteId: "18" });
    await customerQuotesApi.generate({ customerId: "3", quoteType: "TEMP", validTo: "2026-12-31T00:00:00Z", items: [{ skuId: "40", unitPrice: "1.25" }] });
    assert.deepEqual(writes.slice(-3), [
      { customer_id: 3, quote_type: "APPLY" },
      { customer_id: 3, quote_type: "CLONE", source_quote_id: 18 },
      { customer_id: 3, quote_type: "TEMP", valid_to: "2026-12-31T00:00:00Z", items: [{ sku_id: 40, unit_price: "1.25" }] },
    ]);
    await assert.rejects(() => customerQuotesApi.generate({ customerId: "99", quoteType: "TEMP", validTo: "2026-12-31T00:00:00Z", items: [{ skuId: "40", unitPrice: "0.5" }] }), (error: unknown) => {
      assert.ok(error instanceof ApiError); assert.equal(error.httpStatus, 409);
      assert.deepEqual(error.details, { floor_violations: [{ sku_id: 40, sku_code: "gpt-test", unit_price: "0.5", floor_price: "1.0" }] });
      return true;
    });
  } finally {
    session.clear(); server.close(); server.resetHandlers(); entries.clear();
  }
});
