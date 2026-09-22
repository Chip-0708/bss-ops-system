import { mock } from "../../mock";
import { quoteContractHandlers, findWireQuote } from "./quoteContract";
import { http, HttpResponse } from "msw";
import type {
  SupplierQuoteActionResultDTO,
  SupplierQuoteDetailDTO,
  SupplierQuoteSummaryDTO,
  RetroEffectiveResultDTO,
} from "../../api/supplierQuotes.types";
import { ApiError } from "../../domain/common";
import { supplierQuoteRecords } from "../data/supplierQuotes";

const basePath = "/api/internal/quotes";
const readableIdentities = new Set(["PURCHASING", "PRICING_OP", "VIEWER", "RETRO_OP"]);
const quotes = supplierQuoteRecords;
const idempotencyResults = new Map<
  string,
  { signature: string; value: SupplierQuoteActionResultDTO }
>();

const requestId = () => `mock-${crypto.randomUUID()}`;
const copy = <T>(value: T): T => structuredClone(value);

function summary(quote: SupplierQuoteDetailDTO): SupplierQuoteSummaryDTO {
  return {
    id: quote.id,
    quoteNo: quote.quoteNo,
    version: quote.version,
    supplierId: quote.supplierId,
    supplierName: quote.supplierName,
    source: quote.source,
    submittedAt: quote.submittedAt,
    updatedAt: quote.updatedAt,
    effectiveFrom: quote.effectiveFrom,
    effectiveTo: quote.effectiveTo,
    skuCount: quote.skuCount,
    status: quote.status,
    ownerName: quote.ownerName,
    alert: quote.alert,
  };
}

function result(data: unknown) {
  return HttpResponse.json({
    code: 0,
    message: "ok",
    data,
    requestId: requestId(),
  });
}

function failure(error: unknown) {
  const candidate = error as { httpStatus?: number; status?: number; message?: string };
  const status = candidate.httpStatus || candidate.status || 500;
  return HttpResponse.json(
    {
      code: status === 409 ? "STATE_CONFLICT" : `HTTP_${status}`,
      message: candidate.message || "Mock 服务异常",
      requestId: requestId(),
    },
    { status },
  );
}

export const supplierQuoteHandlers = [
  ...quoteContractHandlers,
  http.get(`${basePath}/expiring`, ({ request }) => {
    const identity = request.headers.get("X-Mock-Identity") || "VIEWER";
    if (!readableIdentities.has(identity)) return failure(new ApiError(403, "当前身份无权查看到期报价"));
    return result({ list: [{ id: "mock-expiring-1", supplier_id: "1", supplier_name: "云桥科技", version_no: 4,
      valid_to: "2026-09-18T10:00:00+08:00", days_left: 1, grace_until: "2026-09-21T10:00:00+08:00",
      in_grace: false, remove_confirmed: false, single_point: true, alert_level: "URGENT", pending_remove: true }], total: 1, page: 1, size: 10 });
  }),
  http.get(`${basePath}/anomalies`, ({ request }) => {
    const identity = request.headers.get("X-Mock-Identity") || "VIEWER";
    if (!readableIdentities.has(identity)) return failure(new ApiError(403, "当前身份无权查看异常报价"));
    return result({ list: [{ quote_sheet_id: "mock-quote-1", supplier_name: "云桥科技", sku_id: "1", sku_code: "gpt-5-2026-04-11",
      component_type: "input", unit_price: "3.25000000", prev_price: "2.60000000", delta_pct: "25.00",
      market_best: "2.75000000", mkt_delta_pct: "18.18", reason: "BOTH", detected_at: new Date().toISOString() }], total: 1, page: 1, size: 10 });
  }),
  http.post(`${basePath}/retro-effective`, async ({ request }) => {
    try {
      if (request.headers.get("X-Mock-Identity") !== "RETRO_OP") throw new ApiError(403, "当前身份没有特权补录权限");
      if (!request.headers.get("Idempotency-Key")) throw new ApiError(400, "缺少 Idempotency-Key");
      const body = await request.json() as { effective_time?: string; audit_reason?: string; items?: unknown[] };
      if (!body.effective_time || Date.parse(body.effective_time) >= Date.now() || Array.from(body.audit_reason?.trim() || "").length < 10 || !body.items?.length)
        throw new ApiError(400, "补录参数不符合要求");
      const value: RetroEffectiveResultDTO = { id: `retro-${Date.now()}`, status: "EFFECTIVE", retroactive: true,
        closed_previous_id: null, cost_recalc_queued: true, retro_count_this_month: 1, alert_created: false };
      return result(value);
    } catch (error) { return failure(error); }
  }),
  http.post(`${basePath}/:id/confirm-remove`, async ({ request, params }) => {
    try {
      if (request.headers.get("X-Mock-Identity") !== "PURCHASING") throw new ApiError(403, "当前身份没有确认移除权限");
      if (!request.headers.get("Idempotency-Key")) throw new ApiError(400, "缺少 Idempotency-Key");
      const body = await request.json() as { confirm?: boolean; reason?: string };
      if (body.confirm !== true || !body.reason?.trim()) throw new ApiError(400, "请确认并填写移除原因");
      return result({ id: String(params.id), status: "EXPIRED", remove_confirmed: true, executed_at: new Date().toISOString() });
    } catch (error) { return failure(error); }
  }),
  http.all(`${basePath}/*`, async ({ request }) => {
    try {
      const identity = request.headers.get("X-Mock-Identity") || "VIEWER";
      if (!readableIdentities.has(identity)) {
        throw new ApiError(403, "当前身份无权查看供应商报价");
      }
      const url = new URL(request.url);
      const [id, action] = url.pathname.slice(basePath.length + 1).split("/");
      const quote = findWireQuote(decodeURIComponent(id || ""));
      if (!quote) throw new ApiError(404, "报价不存在或当前账号不可见");

      if (request.method === "GET" && !action) {
        return result(
          copy({
            ...quote,
            canApprove: identity === "PURCHASING" && quote.status === "APPROVING",
          }),
        );
      }

      if (request.method !== "POST" || !["approve", "reject"].includes(action || "")) {
        throw new ApiError(404, "接口不存在");
      }
      if (identity !== "PURCHASING") {
        throw new ApiError(403, "当前身份没有供应商报价审批权限");
      }
      const key = request.headers.get("Idempotency-Key");
      if (!key) throw new ApiError(400, "缺少 Idempotency-Key");
      const body = (await request.json()) as { reason?: string };
      if (!body || typeof body !== "object" || Array.isArray(body) ||
          Object.keys(body).some(field => action !== "reject" || field !== "reason") ||
          (body.reason !== undefined && typeof body.reason !== "string"))
        throw new ApiError(400, "审批请求字段不符合要求");
      const signature = JSON.stringify([identity, id, action, body]);
      const cached = idempotencyResults.get(key);
      if (cached) {
        if (cached.signature !== signature) throw new ApiError(409, "幂等键已用于其他操作");
        return result(copy(cached.value));
      }
      if (quote.status !== "APPROVING") {
        throw new ApiError(409, "该报价已被处理，请刷新后查看最新状态");
      }

      let actionResult: SupplierQuoteActionResultDTO;
      if (action === "approve") {
        if (!quote.comparisonReady) throw new ApiError(409, "差异材料尚未就绪，暂不能通过");
        const approvedAt = new Date().toISOString();
        const immediate = Date.parse(quote.effectiveFrom) <= Date.now();
        if (immediate) {
          for (const previous of quotes) {
            if (previous.id !== quote.id && previous.supplierId === quote.supplierId && previous.status === "EFFECTIVE") {
              previous.status = "EXPIRED";
              previous.effectiveTo = quote.effectiveFrom;
            }
          }
        }
        if (immediate) mock.markQuotedSkusPurchasable(quote.items.filter(item => item.skuId !== undefined).map(item => String(item.skuId)));
        quote.status = immediate ? "EFFECTIVE" : "APPROVED_PENDING";
        quote.updatedAt = approvedAt;
        quote.canApprove = false;
        actionResult = { id: id!, status: "APPROVED_PENDING", approved_at: approvedAt,
          activate_at: quote.effectiveFrom, immediate };
      } else {
        const reason = body.reason?.trim() || "";
        if (Array.from(reason).length < 10 || Array.from(reason).length > 500)
          throw new ApiError(400, "驳回原因须为10~500字");
        quote.status = "REJECTED";
        quote.updatedAt = new Date().toISOString();
        quote.rejectionReason = reason;
        quote.canApprove = false;
        actionResult = { id: id!, status: "REJECTED" };
      }
      idempotencyResults.set(key, { signature, value: actionResult });
      return result(copy(actionResult));
    } catch (error) {
      return failure(error);
    }
  }),
  http.get(basePath, ({ request }) => {
    try {
      const identity = request.headers.get("X-Mock-Identity") || "VIEWER";
      if (!readableIdentities.has(identity)) {
        throw new ApiError(403, "当前身份无权查看供应商报价");
      }
      const url = new URL(request.url);
      const search = (url.searchParams.get("search") || "").trim().toLowerCase();
      const supplierId = url.searchParams.get("supplierId") || "";
      const status = url.searchParams.get("status") || "";
      const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
      const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const matched = quotes.filter(
        (quote) =>
          (!search ||
            `${quote.quoteNo} ${quote.supplierName}`.toLowerCase().includes(search)) &&
          (!supplierId || quote.supplierId === supplierId) &&
          (!status || quote.status === status),
      );
      const offset = (page - 1) * size;
      return result({
        list: matched.slice(offset, offset + size).map(summary),
        total: matched.length,
        page,
        size,
      });
    } catch (error) {
      return failure(error);
    }
  }),
];
