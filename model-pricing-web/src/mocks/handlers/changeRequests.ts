import { http, HttpResponse } from "msw";

const records = [
  { id: 501, change_type: "PRICE_BOOK_PUBLISH", status: "PENDING", sku_id: null, risk_level: "HIGH", steps_total: 2, steps_approved: 1, pending_step_no: 2, pending_role: "FINANCE", created_by: "5", created_at: "2026-09-20T09:00:00Z", updated_at: "2026-09-20T09:10:00Z",
    payload: { price_book_id: 101, level_code: "GOLD", version_no: 3, sku_ids: [40, 41], mode: "IMMEDIATE", effective_time: "2026-09-20T09:00:00Z",
      diff_report: [{ sku_id: 40, sku_code: "gpt-4.1", old_price: "2.60000000", new_price: "2.72000000", delta_pct: "0.04615385", floor_price: "2.40000000", floor_violation: false },
        { sku_id: 41, sku_code: "claude-sonnet", old_price: null, new_price: "10.50000000", delta_pct: null, floor_price: "9.60000000", floor_violation: false }] },
    margin_preview: null, request_id: "mock-price-book", updated_by: "5", steps: [{ step_no: 1, required_role: "PRICING_OP", decision: "APPROVED", approver_id: 5, comment: "approved", decided_at: "2026-09-20T09:10:00Z" }, { step_no: 2, required_role: "FINANCE", decision: null, approver_id: null, comment: null, decided_at: null }] },
  { id: 502, change_type: "PRICE_UP", status: "PENDING", sku_id: 40, risk_level: "HIGH", steps_total: 2, steps_approved: 0, pending_step_no: 1, pending_role: "PRICING_OP", created_by: "5", created_at: "2026-09-20T10:00:00Z", updated_at: "2026-09-20T10:00:00Z",
    payload: { sku_id: 40 }, margin_preview: null, request_id: "mock-price-up", updated_by: null, steps: [{ step_no: 1, required_role: "PRICING_OP", decision: null, approver_id: null, comment: null, decided_at: null }, { step_no: 2, required_role: "FINANCE", decision: null, approver_id: null, comment: null, decided_at: null }] },
] as const;
const respond = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: `mock-${crypto.randomUUID()}` });
export const changeRequestHandlers = [
  http.get("/api/internal/change-requests", ({ request }) => { const url = new URL(request.url), type = url.searchParams.get("type"), status = url.searchParams.get("status"), sku = url.searchParams.get("sku_id"), page = Math.max(1, Number(url.searchParams.get("page")) || 1), size = Math.min(200, Math.max(1, Number(url.searchParams.get("size")) || 20)); const rows = records.filter(row => (!type || row.change_type === type) && (!status || row.status === status) && (!sku || String(row.sku_id) === sku)); return respond({ list: rows.slice((page - 1) * size, page * size).map(({ payload: _p, margin_preview: _m, request_id: _r, updated_by: _u, steps: _s, ...row }) => row), total: rows.length, page, size }); }),
  http.get("/api/internal/change-requests/:id", ({ params }) => { const row = records.find(item => String(item.id) === String(params.id)); return row ? respond(row) : HttpResponse.json({ code: 10004, message: "变更单不存在", data: null }, { status: 404 }); }),
];
