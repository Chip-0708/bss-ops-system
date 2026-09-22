import { apiRequest, apiFileRequest } from "./http";
import type { PageResult } from "../domain/common";
import { assertContractId } from "../domain/contractId";
import type { QuoteDetailDTO, QuoteHistoryDTO, QuoteId, QuoteImportPreviewDTO, QuoteSubmitRequest, QuoteSubmitResult, SupplierSkuDTO } from "./quoteContract.types";
export const supplierQuoteApi = {
    skus: (params: {
        page: number;
        size: number;
        keyword?: string;
        vendor_id?: QuoteId;
        family_id?: QuoteId;
    }) => apiRequest<PageResult<SupplierSkuDTO>>({ url: "/supplier/skus", method: "GET", params }).then(data => {
        data.list.forEach(sku => {
            assertContractId(sku.id);
            assertContractId(sku.vendor_id);
            assertContractId(sku.family_id);
        });
        return data;
    }),
    history: (params: {
        page: number;
        size: number;
        status?: string;
        sku_id?: QuoteId;
        from?: string;
        to?: string;
    }) => apiRequest<PageResult<QuoteHistoryDTO>>({ url: "/supplier/quotes/history", method: "GET", params }).then(data => {
        data.list.forEach(quote => assertContractId(quote.id)); return data;
    }),
    detail: (id: QuoteId) => apiRequest<QuoteDetailDTO>({ url: `/supplier/quotes/${id}`, method: "GET" }).then(data => {
        assertContractId(data.id); data.items.forEach(item => assertContractId(item.sku_id)); return data;
    }),
    submit: (payload: QuoteSubmitRequest) => apiRequest<QuoteSubmitResult>({ url: "/supplier/quotes", method: "POST", data: payload,
        idempotency: { scope: "supplier-quote:submit", payload } }),
    template: (params: {
        scope: "history" | "active" | "vendor" | "family";
        vendor_id?: QuoteId;
        family_id?: QuoteId;
    }) => apiFileRequest({ url: "/supplier/quotes/template", method: "GET", params }),
    preview: (file: File) => {
        const data = new FormData();
        data.append("file", file);
        return apiRequest<QuoteImportPreviewDTO>({ url: "/supplier/quotes/import/preview", method: "POST", data }).then(preview => {
            preview.preview_items.forEach(item => assertContractId(item.sku_id)); return preview;
        });
    },
    confirm: (payload: QuoteSubmitRequest) => apiRequest<QuoteSubmitResult>({ url: "/supplier/quotes/import/confirm", method: "POST", data: payload,
        idempotency: { scope: "supplier-quote:import-confirm", payload } }),
};
