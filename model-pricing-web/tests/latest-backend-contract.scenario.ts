import test from "node:test";
import assert from "node:assert/strict";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { createPinia, setActivePinia } from "pinia";
import { changeRequestsApi } from "../src/api/changeRequests";
import { customerPortalApi } from "../src/api/customerPortal";
import { customerQuotesApi } from "../src/api/customerQuotes";
import { internalModelApplicationsApi, supplierModelApplicationsApi } from "../src/api/supplierModelApplications";
import { useSessionStore } from "../src/stores/session";

const storage = new Map<string, string>();
Object.defineProperty(globalThis, "sessionStorage", { configurable: true, value: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key) } });
const ok = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", requestId: "latest-contract", data });

test("latest change-request, customer kind and model-application contracts map without browser state", async () => {
  setActivePinia(createPinia()); const session = useSessionStore(); session.user = { id: "5", name: "tester", roleLabel: "ops" }; session.portal = "internal"; session.token = "synthetic";
  const server = setupServer(
    http.get("http://localhost/api/internal/change-requests", ({ request }) => { const url = new URL(request.url); assert.equal(url.searchParams.get("type"), "PRICE_BOOK_PUBLISH"); return ok({ list: [{ id: 31, change_type: "PRICE_BOOK_PUBLISH", status: "PENDING", sku_id: null, risk_level: "HIGH", steps_total: 2, steps_approved: 1, pending_step_no: 2, pending_role: "FINANCE", created_by: "5", created_at: "2026-09-21T00:00:00Z", updated_at: "2026-09-21T00:01:00Z" }], total: 1, page: 1, size: 10 }); }),
    http.get("http://localhost/api/internal/change-requests/:id", () => ok({ id: 31, change_type: "PRICE_BOOK_PUBLISH", status: "PENDING", sku_id: null, risk_level: "HIGH", steps_total: 2, steps_approved: 1, pending_step_no: 2, pending_role: "FINANCE", created_by: "5", created_at: "2026-09-21T00:00:00Z", updated_at: "2026-09-21T00:01:00Z", payload: { price_book_id: 9, level_code: "GOLD", version_no: 3, diff_report: [{ sku_id: 40, sku_code: "gpt-4.1", old_price: null, new_price: "2.7", delta_pct: null, floor_price: "2.4", floor_violation: false }] }, steps: [{ step_no: 1, required_role: "PRICING_OP", decision: "APPROVED", approver_id: 5, comment: null, decided_at: "2026-09-21T00:01:00Z" }, { step_no: 2, required_role: "FINANCE", decision: null, approver_id: null, comment: null, decided_at: null }] })),
    http.get("http://localhost/api/internal/customers/:id/quote-context", ({ request, params }) => { const url = new URL(request.url); assert.equal(params.id, "12"); assert.equal(url.searchParams.get("size"), "100"); return ok({ customer_id: 12, level_code: "GOLD", price_book: { id: 9, level_code: "GOLD", version_no: 3, items: [{ sku_id: 40, sku_code: "gpt-4.1", currency: "USD", unit_price: "2.70000000" }] }, history: { list: [{ id: 22, version_no: 2, status: "DRAFT", quote_type: "APPLY", price_book_version: 3, created_at: "2026-09-21T00:02:00Z", items: [{ sku_id: 40, sku_code: "gpt-4.1", currency: "USD", unit_price: "2.65000000" }] }], total: 1, page: 1, size: 100 } }); }),
    http.get("http://localhost/api/customer/quotes", ({ request }) => { const url = new URL(request.url); assert.equal(url.searchParams.get("kind"), "QUOTE"); return ok({ list: [{ id: 21, version_no: 1, status: "DRAFT", quote_type: "SPECIAL", valid_until: null, item_count: 1, total_amount: "1.00000000", currency: "CNY", source_kind: "QUOTE", can_accept: true }], total: 1, page: 1, size: 10 }); }),
    http.post("http://localhost/api/supplier/model-applications", async ({ request }) => { assert.ok(request.headers.get("Idempotency-Key")); assert.deepEqual(await request.json(), { model_name: "Kimi K3", vendor_id: 8, payload: { model_code: "kimi-k3" } }); return ok({ id: 101, supplier_id: 7, model_name: "Kimi K3", vendor_id: 8, payload: { model_code: "kimi-k3" }, dup_top3: [{ sku_id: 40, sku_code: "kimi-old", matched_on: "ALIAS", matched_value: "kimi", similarity: 0.8 }], status: "SUBMITTED", merged_sku_id: null, reject_reason: null, created_at: "2026-09-21T00:00:00Z", updated_at: "2026-09-21T00:00:00Z" }); }),
    http.get("http://localhost/api/internal/model-applications", () => ok({ list: [], total: 0, page: 1, size: 10 })),
    http.post("http://localhost/api/internal/model-applications/:id/decision", async ({ request }) => { assert.ok(request.headers.get("Idempotency-Key")); assert.deepEqual(await request.json(), { action: "MERGE", target_sku_id: 40 }); return ok({ id: 101, supplier_id: 7, model_name: "Kimi K3", vendor_id: 8, payload: { model_code: "kimi-k3" }, dup_top3: [], status: "MERGED", merged_sku_id: 40, reject_reason: null, created_at: "2026-09-21T00:00:00Z", updated_at: "2026-09-21T00:05:00Z" }); }),
  );
  server.listen({ onUnhandledRequest: "error" });
  try {
    const list = await changeRequestsApi.list({ type: "PRICE_BOOK_PUBLISH", page: 1, size: 10 }); assert.equal(list.list[0]?.pendingRole, "FINANCE");
    const detail = await changeRequestsApi.get("31"); assert.equal(detail.steps[0]?.approverId, "5"); assert.equal((detail.payload as { level_code: string }).level_code, "GOLD");
    const context = await customerQuotesApi.context("12"); assert.equal(context.priceBook?.items[0]?.unitPrice, "2.70000000"); assert.equal(context.history.list[0]?.id, "22");
    const quote = (await customerPortalApi.quotes({ page: 1, size: 10, kind: "QUOTE" })).list[0]!; assert.equal(quote.canAccept, true); assert.equal(quote.status, "DRAFT");
    session.portal = "supplier"; const submitted = await supplierModelApplicationsApi.submit({ modelName: "Kimi K3", vendorId: 8, payload: { model_code: "kimi-k3" } }); assert.equal(submitted.duplicateCandidates[0]?.matchedOn, "ALIAS");
    session.portal = "internal"; assert.equal((await internalModelApplicationsApi.list({ page: 1, size: 10 })).total, 0); assert.equal((await internalModelApplicationsApi.decide("101", { action: "MERGE", targetSkuId: 40 })).status, "MERGED");
  } finally { server.close(); storage.clear(); }
});
