import { http, HttpResponse } from "msw";
import { ApiError } from "../../domain/common";

const basePath = "/api/internal/suppliers";
const readableIdentities = new Set(["MODEL_OPS", "PURCHASING", "PRICING_OP", "VIEWER"]);
const suppliers = [
  {
    id: 1, subject_id: 101, legal_name: "云桥科技有限公司",
    settlement_currency: "USD", settle_type: "MONTHLY", billing_cycle: 30,
    min_recharge: "0.00", credit_line: "100000.00", credit_used: "25000.00", deposit_amount: "10000.00",
    qual_status: "VALID", settle_status: "NORMAL", status: "ACTIVE",
    owner_procurement_operator_id: 7, owner_procurement_name: "采购员甲",
    sku_count: 18, effective_quote_count: 7, expiring_soon: 1,
    created_at: "2025-07-01T09:00:00+08:00", updated_at: "2026-09-09T16:20:00+08:00",
  },
  {
    id: 2, subject_id: 102, legal_name: "星河算力有限公司",
    settlement_currency: "CNY", settle_type: "PREPAID", billing_cycle: 0,
    min_recharge: "1000.00", credit_line: "0.00", credit_used: "0.00", deposit_amount: "0.00",
    qual_status: "EXPIRING", settle_status: "WARNING", status: "ACTIVE",
    owner_procurement_operator_id: 8, owner_procurement_name: "采购员乙",
    sku_count: 12, effective_quote_count: 4, expiring_soon: 2,
    created_at: "2025-08-01T09:00:00+08:00", updated_at: "2026-09-10T08:05:00+08:00",
  },
  {
    id: 3, subject_id: 103, legal_name: "远望数据有限公司",
    settlement_currency: "CNY", settle_type: "MONTHLY", billing_cycle: 30,
    min_recharge: "0.00", credit_line: "0.00", credit_used: "0.00", deposit_amount: "0.00",
    qual_status: "FROZEN", settle_status: "FROZEN", status: "INACTIVE",
    owner_procurement_operator_id: 7, owner_procurement_name: "采购员甲",
    sku_count: 6, effective_quote_count: 0, expiring_soon: 0,
    created_at: "2025-06-01T09:00:00+08:00", updated_at: "2026-09-01T09:00:00+08:00",
  },
];
const result = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: `mock-${crypto.randomUUID()}` });
const failure = (error: unknown) => {
  const candidate = error as { httpStatus?: number; status?: number; message?: string };
  const status = candidate.httpStatus || candidate.status || 500;
  return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: `mock-${crypto.randomUUID()}` }, { status });
};
const identity = (request: Request) => {
  const value = request.headers.get("X-Mock-Identity") || "VIEWER";
  if (!readableIdentities.has(value)) throw new ApiError(403, "当前身份无权查看供应商档案");
  return value;
};
const summary = ({ settle_type: _settleType, billing_cycle: _billingCycle, min_recharge: _minRecharge,
  credit_line: _creditLine, credit_used: _creditUsed, deposit_amount: _depositAmount,
  created_at: _createdAt, ...row }: (typeof suppliers)[number]) => row;
const detail = (row: (typeof suppliers)[number]) => {
  const { sku_count: _skuCount, effective_quote_count: _effectiveQuoteCount,
    expiring_soon: _expiringSoon, ...profile } = row;
  return profile;
};

export const supplierHandlers = [
  http.get(basePath, ({ request }) => {
    try {
      identity(request);
      const url = new URL(request.url);
      const keyword = (url.searchParams.get("keyword") || "").trim().toLowerCase();
      const status = url.searchParams.get("status") || "";
      const qualStatus = url.searchParams.get("qual_status") || "";
      const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
      const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const matched = suppliers.filter(row => (!keyword || row.legal_name.toLowerCase().includes(keyword))
        && (!status || row.status === status) && (!qualStatus || row.qual_status === qualStatus));
      return result({ list: matched.slice((page - 1) * size, page * size).map(summary), total: matched.length, page, size });
    } catch (error) { return failure(error); }
  }),
  http.get(`${basePath}/:id`, ({ request, params }) => {
    try {
      identity(request);
      const supplier = suppliers.find(row => row.id === Number(params.id));
      if (!supplier) throw new ApiError(404, "供应商不存在或当前账号不可见");
      return result(detail(supplier));
    } catch (error) { return failure(error); }
  }),
];
