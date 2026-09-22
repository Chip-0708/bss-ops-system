import { http, HttpResponse } from "msw";
import type { CustomerDetailDTO } from "../../api/customers.types";
import { ApiError } from "../../domain/common";
import { customerSeeds } from "../data/customers";

const basePath = "/api/internal/customers";
const readableIdentities = new Set(["PRICING_OP", "ADMIN_OP", "VIEWER"]);
const customers = structuredClone(customerSeeds);
const copy = <T>(value: T): T => structuredClone(value);
const result = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: `mock-${crypto.randomUUID()}` });
const failure = (error: unknown) => { const candidate = error as { httpStatus?: number; status?: number; message?: string }; const status = candidate.httpStatus || candidate.status || 500; return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: `mock-${crypto.randomUUID()}` }, { status }); };
const ensureReadable = (request: Request) => { const identity = request.headers.get("X-Mock-Identity") || "VIEWER"; if (!readableIdentities.has(identity)) throw new ApiError(403, "当前身份无权查看客户档案"); };
const wire = (customer: CustomerDetailDTO, index: number) => ({
  id: index + 1, subject_id: index + 101, legal_name: customer.legalName, level_code: customer.levelCode,
  owner_sales_operator_id: customer.ownerName === "周婷" ? 7 : 8, owner_sales_name: customer.ownerName,
  status: customer.status, credit_limit: "100000.00000000", credit_used: "0.00000000",
  deposit_amount: "0.00000000", deposit_status: "NONE", created_at: customer.updatedAt,
});

export const customerHandlers = [
  http.get(basePath, ({ request }) => {
    try {
      ensureReadable(request);
      const url = new URL(request.url);
      const keyword = (url.searchParams.get("keyword") || "").trim().toLowerCase();
      const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
      const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const matched = customers.map(wire).filter((customer) => !keyword || customer.legal_name.toLowerCase().includes(keyword));
      const offset = (page - 1) * size;
      return result({ list: matched.slice(offset, offset + size), total: matched.length, page, size });
    } catch (error) { return failure(error); }
  }),
  http.post(`${basePath}/:id/transfer`, async ({ request, params }) => {
    try {
      ensureReadable(request);
      if (!request.headers.get("Idempotency-Key")) throw new ApiError(400, "缺少 Idempotency-Key");
      const customer = customers[Number(params.id) - 1];
      if (!customer) throw new ApiError(404, "客户不存在或当前账号不可见");
      const body = await request.json() as { to_operator_id?: number; reason?: string; confirm?: boolean };
      if (!Number.isSafeInteger(body.to_operator_id) || Number(body.to_operator_id) <= 0) throw new ApiError(400, "目标销售人员 ID 不合法");
      if (body.confirm) return result({ customer_id: Number(params.id), from_operator_id: customer.ownerName === "周婷" ? 7 : 8,
        to_operator_id: body.to_operator_id, quotes_migrated: customer.quoteCount, transferred_at: new Date().toISOString() });
      return result({ customer_id: Number(params.id), from_operator_id: customer.ownerName === "周婷" ? 7 : 8,
        from_operator_name: customer.ownerName, to_operator_id: body.to_operator_id, to_operator_name: `销售 ${body.to_operator_id}`,
        quote_count: customer.quoteCount, price_book_count: customer.currentPriceBookCode ? 1 : 0, reason: body.reason || "" });
    } catch (error) { return failure(error); }
  }),
  http.get(`${basePath}/:id/quote-context`, ({ request, params }) => {
    try {
      ensureReadable(request);
      const customerId = Number(params.id);
      const customer = customers[customerId - 1];
      if (!customer) throw new ApiError(404, "客户不存在或当前账号不可见");
      const items = [
        { sku_id: 40, sku_code: "gpt-4.1", currency: "USD", unit_price: "2.72000000" },
        { sku_id: 41, sku_code: "claude-sonnet", currency: "USD", unit_price: "10.50000000" },
      ];
      return result({ customer_id: customerId, level_code: customer.levelCode,
        price_book: customer.currentPriceBookCode ? { id: 100 + customerId, level_code: customer.levelCode, version_no: 1, items } : null,
        history: { list: customer.quoteCount ? [{ id: 900 + customerId, version_no: 1, status: "DRAFT", quote_type: "APPLY",
          price_book_version: 1, created_at: customer.updatedAt, items }] : [], total: customer.quoteCount ? 1 : 0, page: 1, size: 100 } });
    } catch (error) { return failure(error); }
  }),
  http.get(`${basePath}/:id`, ({ request, params }) => {
    try { ensureReadable(request); const customer = customers.find((item) => item.id === String(params.id)); if (!customer) throw new ApiError(404, "客户不存在或当前账号不可见"); return result(copy(customer)); }
    catch (error) { return failure(error); }
  }),
];
