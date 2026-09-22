import test from "node:test";
import assert from "node:assert/strict";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { createPinia, setActivePinia } from "pinia";
import { pricingHandlers } from "../src/mocks/handlers/pricing";
import { supplierQuoteHandlers } from "../src/mocks/handlers/supplierQuotes";
import { modelHandlers } from "../src/mocks/handlers/models";
import { supplierQuoteRecords } from "../src/mocks/data/supplierQuotes";
import { apiRequest } from "../src/api/http";
import { pricingApi } from "../src/api/pricing";
import { useSessionStore } from "../src/stores/session";
import { usePermissionStore } from "../src/stores/permission";
import { PERMISSIONS } from "../src/domain/permissions";
import { auditApi } from "../src/api/audit";

Object.defineProperty(globalThis, "location", { value: new URL("http://localhost/"), configurable: true });
const entries = new Map<string, string>();
Object.defineProperty(globalThis, "sessionStorage", { value: {
  getItem: (key: string) => entries.get(key) ?? null,
  setItem: (key: string, value: string) => entries.set(key, value),
  removeItem: (key: string) => entries.delete(key),
}, configurable: true });

const server = setupServer(...pricingHandlers, ...supplierQuoteHandlers, ...modelHandlers);
async function request(path: string, method = "GET", body?: unknown, identity = "PRICING_OP", key = crypto.randomUUID()) {
  const response = await fetch(`http://localhost/api${path}`, { method,
    headers: { "Content-Type": "application/json", "X-Mock-Identity": identity, "Idempotency-Key": key },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return { status: response.status, ...(await response.json()) as { data: any; message: string } };
}

test("price book mock exposes only real generate and IMMEDIATE publish contracts", async () => {
  server.listen({ onUnhandledRequest: "error" });
  try {
    const key = crypto.randomUUID();
    const payload = { level_code: "GOLD", currency: "USD", policy_ids: [1], sku_ids: [40, 41] };
    const generated = await request("/internal/price-books", "POST", payload, "PRICING_OP", key);
    assert.equal(generated.status, 200);
    assert.equal(generated.data.item_count, 2);
    assert.equal(generated.data.blocked_count, 0);
    assert.equal(generated.data.diff_report.length, 2);
    assert.deepEqual((await request("/internal/price-books", "POST", payload, "PRICING_OP", key)).data, generated.data);
    const future = new Date(Date.now() + 60_000).toISOString();
    assert.equal((await request(`/internal/price-books/${generated.data.draft_id}/publish`, "POST", { effective_time: future, mode: "IMMEDIATE" })).status, 400);
    const published = await request(`/internal/price-books/${generated.data.draft_id}/publish`, "POST", { effective_time: new Date(Date.now() - 1000).toISOString(), mode: "IMMEDIATE" });
    assert.equal(published.data.status, "APPROVING");
    assert.equal(published.data.step_count, 2);
    assert.ok(published.data.change_request_id);
  } finally { server.close(); }
});

test("price book API maps page fields to Go wire DTOs and uses idempotency", async () => {
  setActivePinia(createPinia());
  const session = useSessionStore();
  session.user = { id: "pricing-user", name: "test", roleLabel: "pricing" };
  session.portal = "internal";
  session.token = "pricing-token";
  const seenKeys: string[] = [];
  server.use(
    http.post("http://localhost/api/internal/price-books", async ({ request }) => {
      seenKeys.push(request.headers.get("Idempotency-Key") || "");
      assert.deepEqual(await request.json(), { level_code: "GOLD", currency: "USD", policy_ids: [1], sku_ids: [40] });
      return HttpResponse.json({ code: 0, data: { draft_id: 88, level_code: "GOLD", currency: "USD", item_count: 1, blocked_count: 0,
        diff_report: [{ sku_id: 40, sku_code: "SKU-40", old_price: null, new_price: "3.2", delta_pct: null, floor_price: "2.8", floor_violation: false }] } });
    }),
    http.post("http://localhost/api/internal/price-books/88/publish", async ({ request }) => {
      seenKeys.push(request.headers.get("Idempotency-Key") || "");
      const body = await request.json() as { effective_time: string; mode: string };
      assert.equal(body.mode, "IMMEDIATE");
      return HttpResponse.json({ code: 0, data: { price_book_id: 88, version_no: 3, change_request_id: 99, step_count: 2, effective_time: body.effective_time, status: "APPROVING" } });
    }),
  );
  server.listen({ onUnhandledRequest: "error" });
  try {
    const generated = await pricingApi.generate({ levelCode: "GOLD", currency: "USD", policyIds: [1], skuIds: [40] });
    assert.equal(generated.draftId, "88");
    assert.equal(generated.diffReport[0]?.skuId, "40");
    const published = await pricingApi.publish({ draftId: generated.draftId, effectiveTime: new Date(Date.now() - 1000).toISOString() });
    assert.equal(published.status, "APPROVING");
    assert.equal(published.changeRequestId, "99");
    assert.equal(published.stepCount, 2);
    assert.equal(seenKeys.length, 2);
    assert.ok(seenKeys.every(Boolean));
    assert.notEqual(seenKeys[0], seenKeys[1]);
  } finally { server.close(); server.resetHandlers(); }
});

test("price book approvals use the real decision contract and a new key for each step", async () => {
  setActivePinia(createPinia());
  const session = useSessionStore();
  session.user = { id: "pricing-operator", name: "smoke_admin", roleLabel: "PRICING_OP", roleCodes: ["PRICING_OP"] };
  session.portal = "internal";
  session.token = "pricing-token";
  const requests: Array<{ authorization: string; key: string; body: unknown }> = [];
  server.use(
    http.post("http://localhost/api/internal/approvals/11/decision", async ({ request }) => {
      const body = await request.json() as { step_no: number };
      requests.push({
        authorization: request.headers.get("Authorization") || "",
        key: request.headers.get("Idempotency-Key") || "",
        body,
      });
      return HttpResponse.json({ code: 0, data: {
        change_request_id: 11,
        step_no: body.step_no,
        decision: "APPROVED",
        final_status: body.step_no === 1 ? "PENDING" : "APPROVED",
      } });
    }),
  );
  server.listen({ onUnhandledRequest: "error" });
  try {
    const first = await pricingApi.approve("11", { stepNo: 1, comment: "pricing approved" });
    assert.equal(first.finalStatus, "PENDING");
    session.user = { id: "finance-operator", name: "buyer_b", roleLabel: "FINANCE", roleCodes: ["FINANCE"] };
    session.token = "finance-token";
    const second = await pricingApi.approve("11", { stepNo: 2, comment: "finance approved" });
    assert.equal(second.finalStatus, "APPROVED");
    assert.deepEqual(requests.map(item => item.body), [
      { step_no: 1, decision: "APPROVED", comment: "pricing approved" },
      { step_no: 2, decision: "APPROVED", comment: "finance approved" },
    ]);
    assert.deepEqual(requests.map(item => item.authorization), ["Bearer pricing-token", "Bearer finance-token"]);
    assert.ok(requests.every(item => item.key));
    assert.notEqual(requests[0]?.key, requests[1]?.key);
  } finally { server.close(); server.resetHandlers(); }
});

test("single-step quote approval enforces wire actions, rejection length, replay and actual activation state", async () => {
  server.listen({ onUnhandledRequest: "error" });
  const before = structuredClone(supplierQuoteRecords);
  const quote = supplierQuoteRecords[0]!;
  try {
    quote.status = "APPROVING";
    quote.effectiveFrom = new Date(Date.now() + 86400000).toISOString();
    const path = `/internal/quotes/${quote.id}`;
    assert.equal((await request(`${path}/approve`, "POST", { stepId: "unused" }, "PURCHASING")).status, 400);
    assert.equal(quote.status, "APPROVING");
    assert.equal((await request(`${path}/approve`, "POST", {}, "PRICING_OP")).status, 403);
    const key = crypto.randomUUID();
    const first = await request(`${path}/approve`, "POST", {}, "PURCHASING", key);
    assert.equal(first.data.status, "APPROVED_PENDING");
    assert.equal(first.data.immediate, false);
    assert.ok(first.data.approved_at);
    assert.equal(first.data.activate_at, quote.effectiveFrom);
    assert.deepEqual((await request(`${path}/approve`, "POST", {}, "PURCHASING", key)).data, first.data);
    assert.equal((await request(`${path}/approve`, "POST", {}, "PURCHASING")).status, 409);
    const detail = await request(path, "GET", undefined, "PURCHASING");
    assert.equal(detail.data.approvalSteps, undefined);
    assert.equal(detail.data.currentStepId, undefined);

    const rejected = supplierQuoteRecords[1]!;
    rejected.status = "APPROVING";
    const rejectPath = `/internal/quotes/${rejected.id}/reject`;
    for (const reason of ["价".repeat(9), "价".repeat(501)]) {
      assert.equal((await request(rejectPath, "POST", { reason }, "PURCHASING")).status, 400);
      assert.equal(rejected.status, "APPROVING");
    }
    const rejectKey = crypto.randomUUID(), reason = "价".repeat(10);
    const rejection = await request(rejectPath, "POST", { reason }, "PURCHASING", rejectKey);
    assert.equal(rejection.data.status, "REJECTED");
    assert.equal(rejected.rejectionReason, reason);
    assert.deepEqual((await request(rejectPath, "POST", { reason }, "PURCHASING", rejectKey)).data, rejection.data);
    const immediate = supplierQuoteRecords[2]!;
    immediate.status = "APPROVING";
    immediate.effectiveFrom = new Date(Date.now() - 86400000).toISOString();
    const activated = await request(`/internal/quotes/${immediate.id}/approve`, "POST", {}, "PURCHASING");
    assert.equal(activated.data.status, "APPROVED_PENDING");
    assert.equal(activated.data.immediate, true);
    assert.equal((await request(`/internal/quotes/${immediate.id}`, "GET", undefined, "PURCHASING")).data.status, "EFFECTIVE");
  } finally { supplierQuoteRecords.forEach((row, index) => Object.assign(row, before[index])); server.close(); }
});

test("retirement uses wire field names while Mock keeps approval-first lifecycle", async () => {
  server.listen({ onUnhandledRequest: "error" });
  try {
    const catalog = await request("/internal/models?view=sku&lifecycle_status=PUBLISHED", "GET", undefined, "MODEL_OPS");
    const id = String(catalog.data.list[0].id);
    const report = await request(`/internal/models/${id}/deprecation-impact`, "GET", undefined, "MODEL_OPS");
    assert.equal(String(report.data.sku_id), id);
    assert.ok(report.data.snapshot_id);
    assert.equal(report.data.notifiedAt, undefined);
    const body = { impact_snapshot_id: report.data.snapshot_id, sunset_date: "2099-01-01", reason: "scenario" };
    const key = crypto.randomUUID();
    const result = await request(`/internal/models/${id}/deprecate`, "POST", body, "MODEL_OPS", key);
    assert.equal(result.status, 200);
    assert.equal(result.data.lifecycle_status, "PUBLISHED");
    assert.ok(result.data.approval_id);
    const sku = await request(`/internal/models/${id}`, "GET", undefined, "MODEL_OPS");
    assert.equal(sku.data.sku.lifecycle_status, "PUBLISHED");
    assert.deepEqual((await request(`/internal/models/${id}/deprecate`, "POST", body, "MODEL_OPS", key)).data, result.data);
    assert.equal((await request(`/internal/models/${id}/deprecate`, "POST", { offlineAt: "2099-01-01" }, "MODEL_OPS")).status, 400);
  } finally { server.close(); }
});

test("real API module/Axios path retains timeout keys, masks diagnostic data and clears 401 sessions", async () => {
  setActivePinia(createPinia());
  const session = useSessionStore();
  session.user = { id: "real-user-test", name: "test", roleLabel: "custom" };
  session.portal = "internal";
  session.token = "synthetic-bearer";
  usePermissionStore().rebuild([PERMISSIONS.MODEL_EDIT]);
  assert.equal(usePermissionStore().canAction(PERMISSIONS.MODEL_EDIT), true);
  assert.equal(session.user.identityKey, undefined);
  const keys: string[] = [];
  let calls = 0;
  server.use(
    http.post("http://localhost/api/probe", ({ request }) => {
      assert.equal(request.headers.get("Authorization"), "Bearer synthetic-bearer");
      assert.equal(request.headers.get("X-Mock-Identity"), null);
      keys.push(request.headers.get("Idempotency-Key")!);
      return ++calls === 1 ? HttpResponse.error() : HttpResponse.json({ code: 0, data: { id: "saved" } });
    }),
    http.post("http://localhost/api/rejected", ({ request }) => {
      keys.push(request.headers.get("Idempotency-Key")!);
      return HttpResponse.json({ code: 10001, message: "明确失败" }, { status: 400 });
    }),
    http.get("http://localhost/api/internal/audit-logs", () => HttpResponse.json({ code: 0, data: { items: [{ id: 1, operator_id: 1, operator_name: "test", operator_role: "ADMIN", action: "PROBE", target_type: "TEST", target_id: 1, before_value: JSON.stringify({ apiKey: "sk-" + "synthetic-key", nested: [{ password: "synthetic-password" }] }), after_value: JSON.stringify({ token: "synthetic-token" }), source_type: "INTERNAL", source_id: "", request_id: "req-1", created_at: "2026-09-17T00:00:00Z" }], total: 1 } })),
    http.get("http://localhost/api/unauthorized", () => HttpResponse.json({ code: 10002, message: "expired" }, { status: 401 })),
  );
  server.listen({ onUnhandledRequest: "error" });
  try {
    const submit = () => apiRequest({ url: "/probe", method: "POST", data: { name: "Aa" }, idempotency: { scope: "probe", payload: { name: "Aa" } } });
    await assert.rejects(submit);
    assert.deepEqual(await submit(), { id: "saved" });
    assert.equal(keys[0], keys[1]);
    assert.deepEqual(await submit(), { id: "saved" });
    assert.notEqual(keys[2], keys[1]);
    const reject = () => apiRequest({ url: "/rejected", method: "POST", data: {}, idempotency: { scope: "rejected", payload: {} } });
    await assert.rejects(reject);
    await assert.rejects(reject);
    assert.notEqual(keys[3], keys[4]);
    const diagnostic = await auditApi.list({ page: 1, size: 20 });
    assert.ok(!JSON.stringify(diagnostic).includes("synthetic"));
    await assert.rejects(() => apiRequest({ url: "/unauthorized" }));
    assert.equal(session.token, null);
    assert.equal(session.user, null);
  } finally { server.close(); server.resetHandlers(); }
});

test("price-book generic idempotency conflict keeps the key, but a specific business conflict clears it", async () => {
  setActivePinia(createPinia());
  const session = useSessionStore();
  session.user = { id: "price-book-key-test", name: "test", roleLabel: "pricing" };
  session.portal = "internal";
  session.token = "synthetic-bearer";
  const keys: string[] = [];
  let calls = 0;
  server.use(http.post("http://localhost/api/internal/price-books", ({ request }) => {
    keys.push(request.headers.get("Idempotency-Key")!);
    calls += 1;
    return HttpResponse.json({ code: 10005, message: calls < 3 ? "幂等冲突" : "同内容草稿已存在" }, { status: 409 });
  }));
  server.listen({ onUnhandledRequest: "error" });
  try {
    const generate = () => pricingApi.generate({ levelCode: "GOLD", currency: "USD", operationId: "conflict-test" });
    await assert.rejects(generate);
    await assert.rejects(generate);
    assert.equal(keys[0], keys[1]);
    await assert.rejects(generate);
    await assert.rejects(generate);
    assert.equal(keys[1], keys[2]);
    assert.notEqual(keys[2], keys[3]);
  } finally { server.close(); server.resetHandlers(); }
});

test("FINANCE can open the price-book approval page without gaining create or publish permission", () => {
  setActivePinia(createPinia());
  const permission = usePermissionStore();
  permission.rebuild([], ["FINANCE"]);
  assert.equal(permission.canRoute("internal.priceBooks"), true);
  assert.equal(permission.canAction(PERMISSIONS.PRICE_BOOK_EDIT), false);
  permission.rebuild([], ["VIEWER"]);
  assert.equal(permission.canRoute("internal.priceBooks"), false);
});
