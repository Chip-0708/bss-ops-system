import { http, HttpResponse } from "msw";
import { applications } from "../data/supplierReviews";

const supplierBase = "/api/supplier/model-applications";
const internalBase = "/api/internal/model-applications";
const result = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: `mock-${crypto.randomUUID()}` });
const wire = (item: (typeof applications)[number]) => ({ id: Number(item.id), supplier_id: Number(item.supplierId), model_name: item.modelName,
  vendor_id: item.vendorId ? Number(item.vendorId) : null, payload: item.payload,
  dup_top3: item.duplicateCandidates.map(row => ({ sku_id: Number(row.skuId), sku_code: row.skuCode, matched_on: row.matchedOn, matched_value: row.matchedValue, similarity: row.similarity })),
  status: item.status, merged_sku_id: item.mergedSkuId ? Number(item.mergedSkuId) : null, reject_reason: item.rejectReason,
  created_at: item.createdAt, updated_at: item.updatedAt });
function page(request: Request, internal: boolean) {
  const url = new URL(request.url), page = Math.max(1, Number(url.searchParams.get("page")) || 1), size = Math.min(200, Math.max(1, Number(url.searchParams.get("size")) || 20));
  const status = url.searchParams.get("status") || "", supplierId = url.searchParams.get("supplier_id") || "";
  const list = applications.filter(row => (!status || row.status === status) && (!internal || !supplierId || row.supplierId === supplierId));
  return { list: list.slice((page - 1) * size, page * size).map(wire), total: list.length, page, size };
}
function requireKey(request: Request) { if (!request.headers.get("Idempotency-Key")) throw new Error("缺少 Idempotency-Key"); }

export const supplierModelApplicationHandlers = [
  http.get(supplierBase, ({ request }) => result(page(request, false))),
  http.post(supplierBase, async ({ request }) => {
    try {
      requireKey(request); const body = await request.json() as { model_name?: string; vendor_id?: number; payload?: Record<string, unknown> };
      if (!body.model_name?.trim() || !body.payload || Array.isArray(body.payload) || !Object.keys(body.payload).length) return HttpResponse.json({ code: 10001, message: "参数非法", data: null }, { status: 400 });
      const now = new Date().toISOString();
      const item: (typeof applications)[number] = { id: String(100 + applications.length + 1), supplierId: "7", modelName: body.model_name.trim(), vendorId: body.vendor_id ? String(body.vendor_id) : null,
        payload: body.payload, duplicateCandidates: [], status: "SUBMITTED", mergedSkuId: null, rejectReason: null, createdAt: now, updatedAt: now };
      applications.unshift(item); return result(wire(item));
    } catch (error) { return HttpResponse.json({ code: 10000, message: error instanceof Error ? error.message : "系统错误", data: null }, { status: 500 }); }
  }),
  http.get(internalBase, ({ request }) => result(page(request, true))),
  http.post(`${internalBase}/:id/decision`, async ({ request, params }) => {
    requireKey(request); const item = applications.find(row => row.id === String(params.id));
    if (!item) return HttpResponse.json({ code: 10004, message: "申请不存在", data: null }, { status: 404 });
    if (item.status !== "SUBMITTED") return HttpResponse.json({ code: 10005, message: "申请已终结", data: null }, { status: 409 });
    const body = await request.json() as { action?: string; target_sku_id?: number; reason?: string };
    if (!body.action || (body.action !== "REJECT" && !body.target_sku_id) || (body.action === "REJECT" && !body.reason?.trim())) return HttpResponse.json({ code: 10001, message: "参数非法", data: null }, { status: 400 });
    item.status = body.action === "REJECT" ? "REJECTED" : body.action === "MERGE" ? "MERGED" : "APPROVED";
    item.mergedSkuId = body.target_sku_id ? String(body.target_sku_id) : null; item.rejectReason = body.action === "REJECT" ? body.reason!.trim() : null; item.updatedAt = new Date().toISOString();
    return result(wire(item));
  }),
];
