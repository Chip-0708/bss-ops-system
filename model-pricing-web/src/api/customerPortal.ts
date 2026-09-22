import { apiRequest } from "./http";
import type {
  AcceptCustomerQuoteResult, CustomerBilling, CustomerHome, CustomerListParams, CustomerNotification,
  CustomerPriceBook, CustomerQuoteListParams, PortalCustomerQuote,
} from "./customerPortal.types";

const base = "/customer";
const id = (value: string | number) => String(value);

export const customerPortalApi = {
  home: () => apiRequest<{
    balance: { credit_limit: string; credit_used: string; deposit_amount: string; deposit_status: string };
    pending_count: number;
    unread_notifications: number;
    common_models: Array<{ sku_id: string | number; sku_code: string; currency: string }>;
  }>({ url: `${base}/home`, method: "GET" }).then((data): CustomerHome => ({
    balance: { creditLimit: data.balance.credit_limit, creditUsed: data.balance.credit_used,
      depositAmount: data.balance.deposit_amount, depositStatus: data.balance.deposit_status },
    pendingCount: data.pending_count,
    unreadNotifications: data.unread_notifications,
    commonModels: data.common_models.map(item => ({ skuId: id(item.sku_id), skuCode: item.sku_code, currency: item.currency })),
  })),
  priceBook: () => apiRequest<{
    level_code: string; version_no: number; currency: string;
    items: Array<{ sku_id: string | number; sku_code: string; currency: string; unit_price: string }>;
  }>({ url: `${base}/price-book`, method: "GET" }).then((data): CustomerPriceBook => ({
    levelCode: data.level_code, versionNo: data.version_no, currency: data.currency,
    items: data.items.map(item => ({ skuId: id(item.sku_id), skuCode: item.sku_code, currency: item.currency, unitPrice: item.unit_price })),
  })),
  quotes: (params: CustomerQuoteListParams) => apiRequest<{
    list: Array<{ id: string | number; version_no: number; status: string; quote_type: string; valid_until?: string | null;
      item_count: number; total_amount: string; currency: string; source_kind: "QUOTE" | "CONTRACT";
      contract_from?: string | null; contract_to?: string | null; can_accept?: boolean }>;
    total: number; page: number; size: number;
  }>({ url: `${base}/quotes`, method: "GET", params }).then(data => ({
    ...data,
    list: data.list.map((item): PortalCustomerQuote => ({ id: id(item.id), versionNo: item.version_no, status: item.status,
      quoteType: item.quote_type, validUntil: item.valid_until, itemCount: item.item_count, totalAmount: item.total_amount,
      currency: item.currency, sourceKind: item.source_kind, contractFrom: item.contract_from, contractTo: item.contract_to,
      canAccept: item.can_accept })),
  })),
  billing: () => apiRequest<{ credit_limit: string; credit_used: string; deposit_amount: string; deposit_status: string; billing_cycle: number; bills: unknown[] }>(
    { url: `${base}/billing`, method: "GET" },
  ).then((data): CustomerBilling => ({ creditLimit: data.credit_limit, creditUsed: data.credit_used, depositAmount: data.deposit_amount,
    depositStatus: data.deposit_status, billingCycle: data.billing_cycle, bills: data.bills })),
  notifications: (params: CustomerListParams) => apiRequest<{
    list: Array<{ id: string | number; type: string; title: string; content: string; read_at?: string | null; created_at: string }>;
    total: number; page: number; size: number;
  }>({ url: `${base}/notifications`, method: "GET", params }).then(data => ({
    ...data,
    list: data.list.map((item): CustomerNotification => ({ id: id(item.id), type: item.type, title: item.title,
      content: item.content, readAt: item.read_at, createdAt: item.created_at })),
  })),
  acceptQuote: (quoteId: string) => apiRequest<{ quote_id: string | number; new_status: string; contract_cnt: number }>({
    url: `${base}/quotes/${encodeURIComponent(quoteId)}/accept`, method: "POST", data: {},
    idempotency: { scope: `customer:quote-accept:${quoteId}`, payload: { quoteId } },
  }).then((data): AcceptCustomerQuoteResult => ({ quoteId: id(data.quote_id), newStatus: data.new_status, contractCount: data.contract_cnt })),
};
