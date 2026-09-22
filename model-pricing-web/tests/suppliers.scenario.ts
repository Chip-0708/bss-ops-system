import test from "node:test";
import assert from "node:assert/strict";
import { createPinia, setActivePinia } from "pinia";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { suppliersApi } from "../src/api/suppliers";
import { useSessionStore } from "../src/stores/session";

const ok = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", requestId: "supplier-test", data });
const base = {
  id: 7, subject_id: 70, legal_name: "供应商甲",
  settlement_currency: "CNY", qual_status: "FROZEN", settle_status: "WARNING", status: "INACTIVE",
  owner_procurement_operator_id: 9, owner_procurement_name: "采购员甲", updated_at: "2026-09-22T00:00:00Z",
};

test("supplier list and detail use real wire fields and preserve omitted commercial fields", async () => {
  let includeCommercialFields = false;
  setActivePinia(createPinia());
  const session = useSessionStore();
  session.user = { id: "5", name: "tester", roleLabel: "ops" };
  session.portal = "internal";
  session.token = "synthetic";
  const server = setupServer(
    http.get("http://localhost/api/internal/suppliers", ({ request }) => {
      const query = new URL(request.url).searchParams;
      assert.equal(query.get("keyword"), "供应商");
      assert.equal(query.get("qual_status"), "FROZEN");
      assert.equal(query.get("status"), "INACTIVE");
      assert.equal(query.has("search"), false);
      return ok({ list: [{ ...base, sku_count: 2, effective_quote_count: 1, expiring_soon: 0 }], total: 1, page: 1, size: 10 });
    }),
    http.get("http://localhost/api/internal/suppliers/:id", ({ params }) => {
      assert.equal(params.id, "7");
      return ok({ ...base, created_at: "2026-01-01T00:00:00Z", settle_type: "MONTHLY", billing_cycle: 30,
        ...(includeCommercialFields ? { min_recharge: "0.00", credit_line: "100000.25", credit_used: "12.50", deposit_amount: "9.00" } : {}) });
    }),
  );
  server.listen({ onUnhandledRequest: "error" });
  try {
    const list = await suppliersApi.list({ page: 1, size: 10, keyword: "供应商", qual_status: "FROZEN", status: "INACTIVE" });
    assert.equal(list.list[0]?.id, "7");
    assert.equal(list.list[0]?.settlementCurrency, "CNY");
    assert.equal(list.list[0]?.skuCount, 2);
    const detail = await suppliersApi.get("7");
    assert.equal(detail.subjectId, "70");
    assert.equal(detail.settleType, "MONTHLY");
    assert.equal(detail.minRecharge, undefined);
    assert.equal(detail.creditLine, undefined);
    includeCommercialFields = true;
    const commercial = await suppliersApi.get("7");
    assert.equal(commercial.minRecharge, "0.00");
    assert.equal(commercial.creditLine, "100000.25");
    assert.throws(() => suppliersApi.get("supplier-cloud"), /ID格式不正确/);
  } finally { server.close(); }
});
