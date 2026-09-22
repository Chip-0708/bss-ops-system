import { assertContractId } from "../domain/contractId";
import { apiFileRequest, apiRequest } from "./http";
import type { CustomerQuoteContextDTO, CustomerQuotePreviewItemDTO, FloorViolationDTO, GenerateCustomerQuoteDraft, GeneratedCustomerQuoteDTO, RefreshQuoteResultDTO, SpecialPriceResultDTO } from "./customerQuotes.types";

// Stage 9a real-backend write endpoint. Later customer-quote operations remain unimplemented.
const basePath = "/internal/customer-quotes";

function requestId(value: string) {
  assertContractId(value);
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw new Error("ID超出前端安全范围，请后端支持字符串ID请求。");
  return parsed;
}

function responseId(value: unknown) {
  assertContractId(value);
  return String(value);
}

interface GeneratedQuoteWireDTO {
  id: string | number;
  customer_id: string | number;
  version_no: number;
  status: "DRAFT";
  quote_type: GeneratedCustomerQuoteDTO["quoteType"];
  item_count: number;
  below_floor_count: number;
  floor_violations?: Array<{ sku_id: string | number; sku_code: string; unit_price: string; floor_price: string }>;
  price_book_version: number;
  valid_until?: string;
  owner_sales_operator_id: string | number;
  created_at: string;
}

interface QuotePreviewItemWireDTO {
  sku_id: string | number;
  sku_code: string;
  currency: string;
  unit_price: string;
}

const mapPreviewItem = (item: QuotePreviewItemWireDTO): CustomerQuotePreviewItemDTO => ({
  skuId: responseId(item.sku_id), skuCode: item.sku_code, currency: item.currency, unitPrice: item.unit_price,
});

export const customerQuotesApi = {
  context: (customerId: string, page = 1, size = 100) => apiRequest<{
    customer_id: string | number;
    level_code: string;
    price_book: null | { id: string | number; level_code: string; version_no: number; items: QuotePreviewItemWireDTO[] };
    history: { list: Array<{ id: string | number; version_no: number; status: string; quote_type: string; valid_until?: string;
      price_book_version: number; created_at: string; items: QuotePreviewItemWireDTO[] }>; total: number; page: number; size: number };
  }>({ url: `/internal/customers/${requestId(customerId)}/quote-context`, method: "GET", params: { page, size } }).then((data): CustomerQuoteContextDTO => ({
    customerId: responseId(data.customer_id), levelCode: data.level_code,
    priceBook: data.price_book ? { id: responseId(data.price_book.id), levelCode: data.price_book.level_code,
      versionNo: data.price_book.version_no, items: data.price_book.items.map(mapPreviewItem) } : undefined,
    history: { ...data.history, list: data.history.list.map(item => ({ id: responseId(item.id), versionNo: item.version_no,
      status: item.status, quoteType: item.quote_type, validUntil: item.valid_until, priceBookVersion: item.price_book_version,
      createdAt: item.created_at, items: item.items.map(mapPreviewItem) })) },
  })),
  generate: (draft: GenerateCustomerQuoteDraft) => {
    const payload = {
      customer_id: requestId(draft.customerId), quote_type: draft.quoteType,
      ...(draft.sourceQuoteId ? { source_quote_id: requestId(draft.sourceQuoteId) } : {}),
      ...(draft.validTo ? { valid_to: draft.validTo } : {}),
      ...(draft.items?.length ? { items: draft.items.map(item => ({ sku_id: requestId(item.skuId), unit_price: item.unitPrice })) } : {}),
    };
    return apiRequest<GeneratedQuoteWireDTO>({ url: basePath, method: "POST", data: payload,
      idempotency: { scope: "customer-quote:generate", payload } }).then((data): GeneratedCustomerQuoteDTO => ({
        id: responseId(data.id), customerId: responseId(data.customer_id), versionNo: data.version_no,
        status: data.status, quoteType: data.quote_type, itemCount: data.item_count, belowFloorCount: data.below_floor_count,
        floorViolations: (data.floor_violations || []).map((item): FloorViolationDTO => ({ skuId: responseId(item.sku_id), skuCode: item.sku_code,
          unitPrice: item.unit_price, floorPrice: item.floor_price })),
        priceBookVersion: data.price_book_version, validUntil: data.valid_until,
        ownerSalesOperatorId: responseId(data.owner_sales_operator_id), createdAt: data.created_at,
      }));
  },
  requestSpecialPrice: (id: string, reason: string, expectedMargin: string) => {
    const payload = { reason: reason.trim(), expected_margin: expectedMargin.trim() };
    return apiRequest<{ quote_id: string | number; change_request_id: string | number; step_count: number; status: string;
      margin_impact?: { current_price: string; current_margin: string; target_price: string; target_margin: string; delta_gap_distance: string } }>(
      { url: `${basePath}/${requestId(id)}/special-price`, method: "POST", data: payload,
        idempotency: { scope: `customer-quote:special-price:${id}`, payload } },
    ).then((data): SpecialPriceResultDTO => ({ quoteId: responseId(data.quote_id), changeRequestId: responseId(data.change_request_id),
      stepCount: data.step_count, status: data.status, marginImpact: data.margin_impact ? { currentPrice: data.margin_impact.current_price,
        currentMargin: data.margin_impact.current_margin, targetPrice: data.margin_impact.target_price,
        targetMargin: data.margin_impact.target_margin, deltaGapDistance: data.margin_impact.delta_gap_distance } : undefined }));
  },
  refresh: (id: string, reason: string) => {
    const payload = { reason: reason.trim() };
    return apiRequest<{ quote_id: string | number; old_version_no: number; new_version_no: number; unchanged: boolean; status: string;
      changed_items: Array<{ sku_id: string | number; old_floor_price: string; new_floor_price: string }> }>(
      { url: `${basePath}/${requestId(id)}/refresh`, method: "POST", data: payload,
        idempotency: { scope: `customer-quote:refresh:${id}`, payload } },
    ).then((data): RefreshQuoteResultDTO => ({ quoteId: responseId(data.quote_id), oldVersionNo: data.old_version_no,
      newVersionNo: data.new_version_no, unchanged: data.unchanged, status: data.status,
      changedItems: data.changed_items.map(item => ({ skuId: responseId(item.sku_id), oldFloorPrice: item.old_floor_price, newFloorPrice: item.new_floor_price })) }));
  },
  exportXlsx: (id: string) => apiFileRequest({ url: `${basePath}/${requestId(id)}/export`, method: "GET", params: { format: "xlsx" } }),
};
