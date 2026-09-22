import test from "node:test";
import assert from "node:assert/strict";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createPinia, setActivePinia } from "pinia";
import { alertsApi } from "../src/api/alerts";
import { auditApi } from "../src/api/audit";
import type { AuditLogDetailDTO } from "../src/api/audit.types";
import { customerPortalApi } from "../src/api/customerPortal";
import { customerQuotesApi } from "../src/api/customerQuotes";
import { workbenchApi } from "../src/api/workbench";
import { useSessionStore } from "../src/stores/session";

const entries = new Map<string, string>();
Object.defineProperty(globalThis, "sessionStorage", { configurable: true, value: {
  getItem: (key: string) => entries.get(key) ?? null,
  setItem: (key: string, value: string) => entries.set(key, value),
  removeItem: (key: string) => entries.delete(key),
} });

const ok = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", requestId: "req-stage10", data });

test("stage 9b/9c and stage 10 APIs use the merged backend wire contract", async () => {
  setActivePinia(createPinia());
  const session = useSessionStore();
  session.user = { id: "7", name: "tester", roleLabel: "custom" };
  session.portal = "internal";
  session.token = "synthetic-stage10-token";
  const server = setupServer(
    http.post("http://localhost/api/internal/customer-quotes/:id/special-price", async ({ request, params }) => {
      assert.equal(params.id, "21"); assert.ok(request.headers.get("Idempotency-Key"));
      assert.deepEqual(await request.json(), { reason: "战略客户", expected_margin: "0.08" });
      return ok({ quote_id: 21, change_request_id: 31, step_count: 2, status: "PENDING", margin_impact: {
        current_price: "1.00", current_margin: "0.10", target_price: "1.00", target_margin: "0.08", delta_gap_distance: "-0.02",
      } });
    }),
    http.post("http://localhost/api/internal/customer-quotes/:id/refresh", async ({ request }) => {
      assert.ok(request.headers.get("Idempotency-Key")); assert.deepEqual(await request.json(), { reason: "季度刷新" });
      return ok({ quote_id: 21, old_version_no: 1, new_version_no: 2, unchanged: false, status: "DRAFT",
        changed_items: [{ sku_id: 40, old_floor_price: "0.8", new_floor_price: "0.9" }] });
    }),
    http.get("http://localhost/api/internal/customer-quotes/:id/export", ({ request }) => {
      assert.equal(new URL(request.url).searchParams.get("format"), "xlsx");
      return new HttpResponse(new Uint8Array([80, 75]), { headers: { "Content-Type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" } });
    }),
    http.get("http://localhost/api/internal/workbench/todos", ({ request }) => {
      assert.equal(new URL(request.url).searchParams.get("status"), "OPEN");
      return ok({ list: [{ id: 1, biz_type: "QUOTE", biz_id: 8, title: "待审批报价", priority: "HIGH", status: "OPEN",
        created_at: "2026-09-18T00:00:00Z", deeplink: "/quotes/8/approval", assignee_name: "采购甲" }], total: 1, page: 1, size: 20 });
    }),
    http.get("http://localhost/api/internal/workbench/metrics", () => ok({ cards: [{ key: "pending", title: "待审批", value: "1", unit: "条", trend: "NA", deeplink: "/quotes" }] })),
    http.get("http://localhost/api/internal/alerts", ({ request }) => {
      const url = new URL(request.url); assert.equal(url.searchParams.get("severity"), "HIGH"); assert.equal(url.searchParams.get("alert_type"), "QUOTE_EXPIRE");
      return ok({ list: [{ id: 2, alert_type: "QUOTE_EXPIRE", severity: "HIGH", target_type: "QUOTE", target_id: 8,
        message: "即将到期", status: "OPEN", assigned_role: "PROCUREMENT", handle_note: "", resolved_at: null,
        created_at: "2026-09-18T00:00:00Z", updated_at: "2026-09-18T00:00:00Z" }], total: 1, page: 1, size: 20 });
    }),
    http.post("http://localhost/api/internal/alerts", async ({ request }) => {
      assert.ok(request.headers.get("Idempotency-Key"));
      assert.deepEqual(await request.json(), { alert_id: 2, action: "RESOLVE", note: "已续报", create_todo: false });
      return ok({ alert_id: 2, status: "RESOLVED" });
    }),
    http.get("http://localhost/api/internal/audit-logs", ({ request }) => {
      const url = new URL(request.url); assert.equal(url.searchParams.get("target_type"), "QUOTE");
      return ok({ items: [{ id: 3, operator_id: 7, operator_name: "采购甲", operator_role: "PROCUREMENT", action: "QUOTE_APPROVED",
        target_type: "QUOTE", target_id: 8, before_value: "{}", after_value: "{\"status\":\"APPROVED\"}", source_type: "INTERNAL",
        source_id: "", request_id: "req-audit", created_at: "2026-09-18T00:00:00Z" }], total: 1 });
    }),
    http.get("http://localhost/api/internal/audit-logs/export", ({ request }) => {
      const url = new URL(request.url); assert.equal(url.searchParams.get("format"), "csv"); assert.ok(Number.isFinite(Date.parse(url.searchParams.get("from") || "")));
      return new HttpResponse("id,action\n3,QUOTE_APPROVED", { headers: { "Content-Type": "text/csv" } });
    }),
    http.get("http://localhost/api/customer/home", () => ok({ balance: { credit_limit: "100", credit_used: "10", deposit_amount: "20", deposit_status: "PAID" }, pending_count: 1, unread_notifications: 2, common_models: [{ sku_id: 40, sku_code: "sku-40", currency: "USD" }] })),
    http.get("http://localhost/api/customer/price-book", () => ok({ level_code: "GOLD", version_no: 2, currency: "USD", items: [{ sku_id: 40, sku_code: "sku-40", currency: "USD", unit_price: "1.25" }] })),
    http.get("http://localhost/api/customer/quotes", () => ok({ list: [{ id: 21, version_no: 2, status: "APPROVED", quote_type: "TEMP", valid_until: "2026-12-31T00:00:00Z", item_count: 1, total_amount: "1.25", currency: "USD", source_kind: "QUOTE" }], total: 1, page: 1, size: 20 })),
    http.get("http://localhost/api/customer/billing", () => ok({ credit_limit: "100", credit_used: "10", deposit_amount: "20", deposit_status: "PAID", billing_cycle: 30, bills: [] })),
    http.get("http://localhost/api/customer/notifications", () => ok({ list: [{ id: 4, type: "PRICE_CHANGE", title: "调价", content: "下月生效", read_at: null, created_at: "2026-09-18T00:00:00Z" }], total: 1, page: 1, size: 20 })),
    http.post("http://localhost/api/customer/quotes/:id/accept", ({ request }) => {
      assert.ok(request.headers.get("Idempotency-Key")); return ok({ quote_id: 21, new_status: "EFFECTIVE", contract_cnt: 1 });
    }),
  );
  server.listen({ onUnhandledRequest: "error" });
  try {
    assert.equal((await customerQuotesApi.requestSpecialPrice("21", "战略客户", "0.08")).changeRequestId, "31");
    assert.equal((await customerQuotesApi.refresh("21", "季度刷新")).changedItems[0]?.skuId, "40");
    assert.ok(await customerQuotesApi.exportXlsx("21"));
    const workbench = await workbenchApi.get(); assert.equal(workbench.tasks[0]?.level, "URGENT"); assert.equal(workbench.metrics[0]?.value, "1");
    const alerts = await alertsApi.list({ page: 1, size: 20, alertType: "QUOTE_EXPIRE", severity: "HIGH", status: "OPEN" });
    assert.equal(alerts.list[0]?.description, "即将到期"); assert.equal((await alertsApi.resolve("2", "已续报")).status, "RESOLVED");
    const audits = await auditApi.list({ page: 1, size: 20, targetType: "QUOTE" }); assert.equal((audits.list[0] as AuditLogDetailDTO | undefined)?.after?.status, "APPROVED");
    assert.ok((await auditApi.export({ from: "2026-09-01", to: "2026-09-18" })).blob);
    session.portal = "customer";
    assert.equal((await customerPortalApi.home()).pendingCount, 1);
    assert.equal((await customerPortalApi.priceBook()).items[0]?.skuId, "40");
    assert.equal((await customerPortalApi.quotes({ page: 1, size: 20, kind: "QUOTE" })).list[0]?.sourceKind, "QUOTE");
    assert.equal((await customerPortalApi.billing()).billingCycle, 30);
    assert.equal((await customerPortalApi.notifications({ page: 1, size: 20 })).list[0]?.id, "4");
    assert.equal((await customerPortalApi.acceptQuote("21")).contractCount, 1);
  } finally { server.close(); entries.clear(); }
});
