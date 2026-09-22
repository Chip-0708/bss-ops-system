import Decimal from "decimal.js";
import { http, HttpResponse } from "msw";
import type { CustomerQuoteDetailDTO, CustomerQuoteDraft, CustomerQuoteSaveResultDTO, CustomerQuoteSummaryDTO } from "../../api/customerQuotes.types";
import { ApiError } from "../../domain/common";
import { customerQuoteRecords, customerSeeds, customerQuoteAcceptances } from "../data/customers";
import { customerQuoteTemplate } from "../data/customerQuoteTemplate";
import { priceBookRecords } from "../data/priceBooks";

const basePath = "/api/internal/customer-quotes";
const readableIdentities = new Set(["PRICING_OP", "VIEWER"]);
const quotes = customerQuoteRecords;
const idempotencyResults = new Map<string, { signature: string; value: CustomerQuoteSaveResultDTO }>();
const stage9Results = new Map<string, { signature: string; value: Record<string, unknown> }>();
const copy = <T>(value: T): T => structuredClone(value);
const summary = ({ priceBookCode: _priceBookCode, priceBookName: _priceBookName, validFrom: _validFrom, reason: _reason, items: _items, canEdit: _canEdit, ...row }: CustomerQuoteDetailDTO): CustomerQuoteSummaryDTO => row;
const result = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: `mock-${crypto.randomUUID()}` });
const failure = (error: unknown) => { const candidate = error as { httpStatus?: number; status?: number; message?: string }; const status = candidate.httpStatus || candidate.status || 500; return HttpResponse.json({ code: status === 409 ? "STATE_CONFLICT" : `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: `mock-${crypto.randomUUID()}` }, { status }); };
const identity = (request: Request) => request.headers.get("X-Mock-Identity") || "VIEWER";
const ensureReadable = (request: Request) => { if (!readableIdentities.has(identity(request))) throw new ApiError(403, "当前身份无权查看客户报价"); };
const ensureWritable = (request: Request) => { if (identity(request) !== "PRICING_OP") throw new ApiError(403, "当前身份没有客户报价草稿维护权限"); };

function validateDraft(draft: CustomerQuoteDraft) {
  if (!draft.name?.trim() || !draft.customerId || !draft.reason?.trim()) throw new ApiError(400, "请填写报价名称、客户和报价原因");
  const customer = customerSeeds.find((item) => item.id === draft.customerId);
  if (!customer) throw new ApiError(404, "客户不存在或当前账号不可见");
  if (customer.status !== "ACTIVE") throw new ApiError(423, "当前客户已冻结或停用，不能新建报价");
  if (!draft.items?.length) throw new ApiError(400, "客户报价至少需要一个 SKU 价格项");
  for (const item of draft.items) {
    try { if (typeof item.unitPrice !== "string" || !/^\d+(\.\d{1,8})?$/.test(item.unitPrice) || !new Decimal(item.unitPrice).isFinite() || !new Decimal(item.unitPrice).isPositive()) throw new Error(); }
    catch { throw new ApiError(400, "客户报价金额必须是大于 0 的十进制字符串"); }
  }
  if (new Set(draft.items.map(row => `${row.skuId}:${row.component}`)).size !== draft.items.length) throw new ApiError(400, "SKU组件不能重复");
  if (draft.validTo && (!Number.isFinite(Date.parse(draft.validTo)) || Date.parse(draft.validTo) <= Date.now())) throw new ApiError(400, "报价截止时间必须晚于当前时间");
  return customer;
}

async function save(request: Request, id?: string, sourceId?: string) {
  ensureReadable(request);
  ensureWritable(request);
  const key = request.headers.get("Idempotency-Key");
  if (!key) throw new ApiError(400, "缺少 Idempotency-Key");
  const draft = await request.json() as CustomerQuoteDraft;
  const signature = JSON.stringify([id || "create", sourceId || "", draft]);
  const cached = idempotencyResults.get(key);
  if (cached) {
    if (cached.signature !== signature) throw new ApiError(409, "幂等键已用于其他操作");
    return cached.value;
  }
  const customer = validateDraft(draft);
  const source = sourceId ? quotes.find(row => row.id === sourceId) : undefined;
  if (sourceId && !source) throw new ApiError(404, "来源报价不存在或不可见");
  if (source && source.customerId !== customer.id) throw new ApiError(400, "克隆报价不能更换客户，请为其他客户单独创建报价");
  if (!id && !source && !draft.priceBookCode) draft.priceBookCode = customer.currentPriceBookCode;
  if (source && !draft.priceBookCode) {
    draft.items = draft.items.map(item => {
      const reference = source.items.find(row => row.skuId === item.skuId && row.component === item.component);
      if (!reference) throw new ApiError(400, "克隆不能注入来源报价不存在的组件");
      return { ...copy(reference), unitPrice: item.unitPrice };
    });
  }
  if (draft.priceBookCode) {
    const template = customerQuoteTemplate(customerSeeds, priceBookRecords, customer.id);
    if (template.priceBookCode !== draft.priceBookCode) throw new ApiError(409, "客户价目表已变化，请重新带价后保存");
    draft.items = draft.items.map(item => {
      const reference = template.items.find(row => row.skuId === item.skuId && row.component === item.component);
      if (!reference) throw new ApiError(409, "报价组件已不在当前价目表中，请重新带价");
      return { ...reference, unitPrice: item.unitPrice };
    });
  }
  let quote = id ? quotes.find((item) => item.id === id) : undefined;
  if (id && !quote) throw new ApiError(404, "客户报价不存在或当前账号不可见");
  if (quote && quote.status !== "DRAFT") throw new ApiError(409, "只有草稿状态的客户报价可以编辑");
  const now = new Date().toISOString();
  if (!quote) {
    const sequence = String(quotes.length + 1).padStart(3, "0");
    quote = { id: `cq-${crypto.randomUUID()}`, quoteNo: `CQ-202609-${sequence}`, status: "DRAFT", ownerName: "周婷", updatedAt: now, itemCount: 0, currency: draft.items[0]?.currency || customer.billingCurrency, customerId: customer.id, customerName: customer.name, name: "", reason: "", items: [], canEdit: true };
    quotes.unshift(quote);
    if (source) quote.previousQuoteId = source.id;
  }
  Object.assign(quote, { name: draft.name.trim(), customerId: customer.id, customerName: customer.name, validTo: draft.validTo || undefined, itemCount: draft.items.length, currency: draft.items[0]?.currency || customer.billingCurrency, updatedAt: now, priceBookCode: customer.currentPriceBookCode, priceBookName: customer.currentPriceBookName, reason: draft.reason.trim(), items: copy(draft.items), canEdit: true });
  const value: CustomerQuoteSaveResultDTO = { id: quote.id, quoteNo: quote.quoteNo, status: "DRAFT" };
  idempotencyResults.set(key, { signature, value });
  return value;
}

async function generateStage9a(request: Request, body: Record<string, unknown>) {
  ensureReadable(request);
  ensureWritable(request);
  const key = request.headers.get("Idempotency-Key");
  if (!key) throw new ApiError(400, "缺少 Idempotency-Key");
  const signature = JSON.stringify(body);
  const cached = stage9Results.get(key);
  if (cached) {
    if (cached.signature !== signature) throw new ApiError(409, "幂等键已用于其他操作");
    return cached.value;
  }
  const customerId = Number(body.customer_id);
  const customer = Number.isSafeInteger(customerId) ? customerSeeds[customerId - 1] : undefined;
  if (!customer) throw new ApiError(404, "客户不存在或当前账号不可见");
  if (customer.status !== "ACTIVE") throw new ApiError(423, "当前客户已冻结或停用，不能生成报价");
  const quoteType = String(body.quote_type || "");
  if (!new Set(["APPLY", "CLONE", "TEMP"]).has(quoteType)) throw new ApiError(400, "quote_type 非法");
  if (quoteType === "CLONE" && (!Number.isSafeInteger(body.source_quote_id) || Number(body.source_quote_id) <= 0))
    throw new ApiError(400, "CLONE 必须提供来源报价 ID");
  const items = Array.isArray(body.items) ? body.items as Array<Record<string, unknown>> : [];
  if (quoteType === "TEMP") {
    if (typeof body.valid_to !== "string" || !Number.isFinite(Date.parse(body.valid_to))) throw new ApiError(400, "TEMP 必须提供有效期");
    if (!items.length) throw new ApiError(400, "TEMP 至少需要一个 SKU 价格项");
    for (const item of items) if (!Number.isSafeInteger(item.sku_id) || typeof item.unit_price !== "string" || !/^\d+(\.\d{1,8})?$/.test(item.unit_price))
      throw new ApiError(400, "SKU ID 或报价金额不合法");
  }
  const now = new Date().toISOString();
  const value = { id: 1000 + stage9Results.size + 1, customer_id: customerId, version_no: 1, status: "DRAFT", quote_type: quoteType,
    item_count: quoteType === "TEMP" ? items.length : 2, below_floor_count: 0, price_book_version: quoteType === "APPLY" ? 1 : 0,
    ...(typeof body.valid_to === "string" ? { valid_until: body.valid_to } : {}), owner_sales_operator_id: customer.ownerName === "周婷" ? 7 : 8, created_at: now };
  stage9Results.set(key, { signature, value });
  return value;
}

export const customerQuoteHandlers = [
  http.get(`${basePath}/template`, ({ request }) => { try { ensureReadable(request); ensureWritable(request); return result(customerQuoteTemplate(customerSeeds, priceBookRecords, new URL(request.url).searchParams.get("customerId") || "")); } catch (error) { return failure(error); } }),
  http.post(`${basePath}/:id/clone`, async ({ request, params }) => { try { return result(await save(request, undefined, String(params.id))); } catch (error) { return failure(error); } }),
  http.get(basePath, ({ request }) => {
    try {
      ensureReadable(request);
      const url = new URL(request.url);
      const search = (url.searchParams.get("search") || "").trim().toLowerCase();
      const customerId = url.searchParams.get("customerId") || "";
      const status = url.searchParams.get("status") || "";
      const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
      const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const matched = quotes.filter((quote) => (!search || `${quote.quoteNo} ${quote.name} ${quote.customerName}`.toLowerCase().includes(search)) && (!customerId || quote.customerId === customerId) && (!status || quote.status === status));
      const offset = (page - 1) * size;
      return result({ list: matched.slice(offset, offset + size).map((item) => summary(copy(item))), total: matched.length, page, size });
    } catch (error) { return failure(error); }
  }),
  http.get(`${basePath}/:id`, ({ request, params }) => {
    try { ensureReadable(request); const quote = quotes.find((item) => item.id === String(params.id)); if (!quote) throw new ApiError(404, "客户报价不存在或当前账号不可见"); return result(copy({ ...quote, acceptedAt: customerQuoteAcceptances.get(quote.id), canEdit: identity(request) === "PRICING_OP" && quote.status === "DRAFT" })); }
    catch (error) { return failure(error); }
  }),
  http.post(basePath, async ({ request }) => {
    try {
      const body = await request.clone().json() as Record<string, unknown>;
      return result(copy("quote_type" in body ? await generateStage9a(request, body) : await save(request)));
    } catch (error) { return failure(error); }
  }),
  http.put(`${basePath}/:id`, async ({ request, params }) => { try { return result(copy(await save(request, String(params.id)))); } catch (error) { return failure(error); } }),
];
