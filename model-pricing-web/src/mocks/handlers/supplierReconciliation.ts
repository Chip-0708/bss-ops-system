import { http, HttpResponse } from "msw";
import type { SupplierReconciliationDetailDTO, SupplierReconciliationSummaryDTO } from "../../api/supplierReconciliation.types";
import { ApiError } from "../../domain/common";

const basePath = "/api/supplier/reconciliation";
const statements: SupplierReconciliationDetailDTO[] = [
  { id: "recon-202608", statementNo: "RC-202608-CLOUD", period: "2026-08", currency: "USD", usageAmount: "128450.36000000", adjustmentAmount: "-320.00000000", taxAmount: "0.00000000", payableAmount: "128130.36000000", status: "SETTLED", generatedAt: "2026-09-01T07:30:00+08:00", dueAt: "2026-09-30T23:59:59+08:00", settlementAccountMasked: "**** 6732", invoiceStatus: "NOT_REQUIRED", lines: [
    { id: "recon-line-1", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "输入 Token", usageQuantity: "48210000", unit: "百万 Token", unitPrice: "1.76000000", amount: "84849.60000000", adjustmentAmount: "-120.00000000", note: "已按对账差异调整" },
    { id: "recon-line-2", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "输出 Token", usageQuantity: "6141000", unit: "百万 Token", unitPrice: "7.10000000", amount: "43600.71000000", adjustmentAmount: "-200.00000000" },
  ], notes: ["该月结算已完成，实际入账结果以财务系统为准。"] },
  { id: "recon-202609", statementNo: "RC-202609-CLOUD", period: "2026-09", currency: "USD", usageAmount: "76320.88000000", adjustmentAmount: "0.00000000", taxAmount: "0.00000000", payableAmount: "76320.88000000", status: "CHECKING", generatedAt: "2026-09-10T08:00:00+08:00", dueAt: "2026-10-31T23:59:59+08:00", settlementAccountMasked: "**** 6732", invoiceStatus: "NOT_REQUIRED", lines: [
    { id: "recon-line-3", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "输入 Token", usageQuantity: "25810000", unit: "百万 Token", unitPrice: "1.76000000", amount: "45425.60000000", adjustmentAmount: "0.00000000" },
    { id: "recon-line-4", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "输出 Token", usageQuantity: "4351448", unit: "百万 Token", unitPrice: "7.10000000", amount: "30895.28000000", adjustmentAmount: "0.00000000" },
  ], notes: ["当前为月中对账快照，尚未进入最终结算。"] },
  { id: "recon-202607", statementNo: "RC-202607-CLOUD", period: "2026-07", currency: "USD", usageAmount: "119860.00000000", adjustmentAmount: "240.00000000", taxAmount: "0.00000000", payableAmount: "120100.00000000", status: "CONFIRMED", generatedAt: "2026-08-01T07:30:00+08:00", dueAt: "2026-08-31T23:59:59+08:00", settlementAccountMasked: "**** 6732", invoiceStatus: "NOT_REQUIRED", lines: [], notes: ["已确认，等待财务系统更新结算结果。"] },
];

const copy = <T>(value: T): T => structuredClone(value);
const requestId = () => `mock-${crypto.randomUUID()}`;
const result = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: requestId() });
function failure(error: unknown) { const candidate = error as { httpStatus?: number; status?: number; message?: string }; const status = candidate.httpStatus || candidate.status || 500; return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: requestId() }, { status }); }
function ensureSupplier(request: Request) { if ((request.headers.get("X-Mock-Identity") || "") !== "SUPPLIER") throw new ApiError(403, "当前身份无权访问供应商对账信息"); }
function summary(item: SupplierReconciliationDetailDTO): SupplierReconciliationSummaryDTO { return { id: item.id, statementNo: item.statementNo, period: item.period, currency: item.currency, usageAmount: item.usageAmount, adjustmentAmount: item.adjustmentAmount, taxAmount: item.taxAmount, payableAmount: item.payableAmount, status: item.status, generatedAt: item.generatedAt, dueAt: item.dueAt }; }

export const supplierReconciliationHandlers = [
  http.get(basePath, ({ request }) => {
    try {
      ensureSupplier(request);
      const url = new URL(request.url);
      const search = (url.searchParams.get("search") || "").trim().toLowerCase();
      const status = url.searchParams.get("status") || "";
      const period = url.searchParams.get("period") || "";
      const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
      const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const matched = statements.filter((item) => (!search || item.statementNo.toLowerCase().includes(search)) && (!status || item.status === status) && (!period || item.period === period));
      const offset = (page - 1) * size;
      return result({ list: matched.slice(offset, offset + size).map(summary), total: matched.length, page, size });
    } catch (error) { return failure(error); }
  }),
  http.get(`${basePath}/:id`, ({ request, params }) => {
    try {
      ensureSupplier(request);
      const item = statements.find((candidate) => candidate.id === String(params.id));
      if (!item) throw new ApiError(404, "对账单不存在或不属于当前供应商");
      return result(copy(item));
    } catch (error) { return failure(error); }
  }),
];
