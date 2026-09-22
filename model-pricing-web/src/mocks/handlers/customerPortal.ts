import { http, HttpResponse } from "msw";
import { ApiError } from "../../domain/common";
import { createCustomerPortalMock } from "../data/customerPortal";
import { customerQuoteRecords, customerQuoteAcceptances } from "../data/customers";
import type { CustomerSection } from "../../api/customerPortal.types";

const db = createCustomerPortalMock(customerQuoteRecords, () => Date.now(), customerQuoteAcceptances);
const sections = new Set(["price-book", "quotes", "contracts", "billing", "notifications"]);
const sectionOf = (value: unknown) => { if (!sections.has(String(value))) throw new ApiError(404, "客户页面接口不存在"); return String(value) as CustomerSection; };
const identity = (request: Request) => request.headers.get("X-Mock-Identity");
async function respond(action: () => unknown | Promise<unknown>) {
  const requestId = `mock-${crypto.randomUUID()}`;
  try { return HttpResponse.json({ code: 0, message: "ok", data: await action(), requestId }, { headers: { "X-Request-Id": requestId } }); }
  catch (error) { const candidate = error as { httpStatus?: number; status?: number; message?: string }; const status = candidate.httpStatus || candidate.status || 500;
    return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "客户门户服务异常", data: null, requestId }, { status }); }
}
export const customerPortalHandlers = [
  http.get("/api/customer/home", ({ request }) => respond(() => db.home(identity(request)))),
  http.get("/api/customer/quotes", ({ request }) => respond(() => {
    const url = new URL(request.url);
    const kind = url.searchParams.get("kind") || "QUOTE";
    if (kind !== "QUOTE" && kind !== "CONTRACT") throw new ApiError(400, "kind 仅支持 QUOTE 或 CONTRACT");
    const section = kind === "CONTRACT" ? "contracts" : "quotes";
    const page = Number(url.searchParams.get("page") ?? 1);
    const size = Number(url.searchParams.get("size") ?? 10);
    const result = db.list(identity(request), section, { page: 1, size: 100 });
    const status = url.searchParams.get("status");
    const list = result.list.map(item => ({ ...item, status: customerQuoteAcceptances.has(item.id) ? "EFFECTIVE" : item.status }))
      .filter(item => !status || item.status === status);
    return {
      page,
      size,
      total: list.length,
      list: list.slice((page - 1) * size, page * size).map(item => ({
        id: item.id,
        version_no: 1,
        status: item.status,
        quote_type: kind === "CONTRACT" ? "CONTRACT" : "APPLY",
        valid_until: "validTo" in item ? item.validTo : null,
        item_count: "items" in item ? item.items.length : 0,
        total_amount: "items" in item ? item.items.reduce((sum, price) => sum + Number("unitPrice" in price ? price.unitPrice : 0), 0).toFixed(8) : "0.00000000",
        currency: "items" in item && item.items[0] && "currency" in item.items[0] ? item.items[0].currency : "CNY",
        source_kind: kind,
        contract_from: kind === "CONTRACT" && "validFrom" in item ? item.validFrom : null,
        contract_to: kind === "CONTRACT" && "validTo" in item ? item.validTo : null,
        can_accept: kind === "QUOTE" && item.status !== "EFFECTIVE" && "canAccept" in item ? item.canAccept : false,
      })),
    };
  })),
  http.get("/api/customer/:section", ({ request, params }) => respond(() => {
    const url = new URL(request.url);
    return db.list(identity(request), sectionOf(params.section), { page: Number(url.searchParams.get("page") ?? 1), size: Number(url.searchParams.get("size") ?? 10), search: url.searchParams.get("search") || "", status: url.searchParams.get("status") || "" });
  })),
  http.get("/api/customer/:section/:id", ({ request, params }) => respond(() => db.get(identity(request), sectionOf(params.section), String(params.id)))),
  http.post("/api/customer/quotes/:id/accept", ({ request, params }) => respond(async () => {
    const result = db.accept(identity(request), String(params.id), request.headers.get("Idempotency-Key"), await request.json());
    const quote = customerQuoteRecords.find(item => item.id === result.quoteId);
    return { quote_id: result.quoteId, new_status: "EFFECTIVE", contract_cnt: quote?.items.length ?? 0 };
  })),
];
