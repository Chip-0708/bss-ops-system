import { http, HttpResponse } from "msw";
import type { ReviewKind } from "../../api/supplierReviews";
import { qualifications } from "../data/supplierReviews";
import { createSupplierReviewWorkflow } from "../data/supplierReviewWorkflow";

const workflow = createSupplierReviewWorkflow(qualifications);
function kindOf(value: unknown): ReviewKind { if (value !== "qualifications") throw Object.assign(new Error("审核类别不存在"), { status: 404 }); return value; }
async function respond(action: () => unknown | Promise<unknown>) {
  try { return HttpResponse.json({ code: 0, message: "ok", data: await action(), requestId: crypto.randomUUID() }); }
  catch (error) { const value = error as { status?: number; message?: string }; const status = value.status || 500; return HttpResponse.json({ code: `HTTP_${status}`, message: value.message || "审核失败", requestId: crypto.randomUUID() }, { status }); }
}
export const supplierReviewHandlers = [
  http.get("/api/internal/supplier-reviews/:kind/:id", ({ request, params }) => respond(() => {
    const record = workflow.list(request.headers.get("X-Mock-Identity"), kindOf(params.kind)).find(row => row.id === String(params.id));
    if (!record) throw Object.assign(new Error("申请不存在或不可见"), { status: 404 }); return record;
  })),
  http.get("/api/internal/supplier-reviews/:kind", ({ request, params }) => respond(() => {
    const url = new URL(request.url); const page = Math.max(1, Number(url.searchParams.get("page")) || 1); const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 5));
    const search = (url.searchParams.get("search") || "").toLowerCase(); const status = url.searchParams.get("status");
    const rows = workflow.list(request.headers.get("X-Mock-Identity"), kindOf(params.kind)).filter(row => (!status || row.status === status) && `${row.name} ${row.supplierName}`.toLowerCase().includes(search));
    return { list: rows.slice((page - 1) * size, page * size), total: rows.length };
  })),
  http.post("/api/internal/supplier-reviews/:kind/:id/review", ({ request, params }) => respond(async () => workflow.review(request.headers.get("X-Mock-Identity"), kindOf(params.kind), String(params.id), request.headers.get("Idempotency-Key"), await request.json() as { result: string; reason: string }))),
];
