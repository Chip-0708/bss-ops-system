import { assertContractId } from "../domain/contractId";
import type { PageResult } from "../domain/common";
import { apiRequest } from "./http";
import type {
  SupplierQuoteApproveResultDTO,
  SupplierQuoteRejectResultDTO,
  ExpiringQuoteDTO,
  QuoteAnomalyDTO,
  ConfirmRemoveResultDTO,
  RetroEffectiveRequestDTO,
  RetroEffectiveResultDTO,
} from "./supplierQuotes.types";

// Pending/diff queries and actions follow the supplied stage-5 contract.
const basePath = "/internal/quotes";

export const supplierQuotesApi = {
  list: (params: { page: number; size: number; supplier_id?: import("./quoteContract.types").QuoteId }) =>
    apiRequest<PageResult<import("./quoteContract.types").QuotePendingDTO>>({ url: `${basePath}/pending`, method: "GET", params }).then(data => { data.list.forEach(quote => { assertContractId(quote.id); assertContractId(quote.supplier_id); }); return data; }),
  get: (id: string) => apiRequest<import("./quoteContract.types").QuoteDiffDTO>({ url: `${basePath}/${id}/diff`, method: "GET" }),
  approve: (id: string) =>
    apiRequest<SupplierQuoteApproveResultDTO>({
      url: `${basePath}/${id}/approve`, method: "POST", data: {},
      idempotency: { scope: `supplier-quote:approve:${id}`, payload: {} },
    }),
  reject: (id: string, reason: string) => {
    const payload = { reason: reason.trim() };
    return apiRequest<SupplierQuoteRejectResultDTO>({
      url: `${basePath}/${id}/reject`, method: "POST", data: payload,
      idempotency: { scope: `supplier-quote:reject:${id}`, payload },
    });
  },
  listExpiring: (params: { days: number; only_single_point?: boolean; page: number; size: number }) =>
    apiRequest<PageResult<ExpiringQuoteDTO>>({ url: `${basePath}/expiring`, method: "GET", params }),
  listAnomalies: (params: { days: number; page: number; size: number }) =>
    apiRequest<PageResult<QuoteAnomalyDTO>>({ url: `${basePath}/anomalies`, method: "GET", params }),
  confirmRemove: (id: string, reason: string) => {
    const payload = { confirm: true, reason: reason.trim() };
    return apiRequest<ConfirmRemoveResultDTO>({
      url: `${basePath}/${id}/confirm-remove`, method: "POST", data: payload,
      idempotency: { scope: `supplier-quote:confirm-remove:${id}`, payload },
    });
  },
  retroEffective: (payload: RetroEffectiveRequestDTO) =>
    apiRequest<RetroEffectiveResultDTO>({
      url: `${basePath}/retro-effective`, method: "POST", data: payload,
      idempotency: { scope: "supplier-quote:retro-effective", payload },
    }),
};
