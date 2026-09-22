import type { IsoDateTime } from "../domain/common";
import type { Money } from "../domain/money";
import type { SupplierQuoteStatus } from "../domain/status";

// Legacy Mock/display shapes below are not stage-5 Go DTOs. Use quoteContract.types for live quote calls.
export interface SupplierPortalQuoteListParams {
  page: number;
  size: number;
  search?: string;
  status?: SupplierQuoteStatus | "";
}

export interface SupplierPortalQuoteSummaryDTO {
  id: string;
  quoteNo: string;
  version: number;
  status: SupplierQuoteStatus;
  skuCount: number;
  effectiveFrom: IsoDateTime;
  effectiveTo?: IsoDateTime;
  submittedAt?: IsoDateTime;
  updatedAt: IsoDateTime;
  rejectionReason?: string;
}

export interface SupplierPortalQuoteItemDTO {
  pricingMode?: "ABSOLUTE" | "MULTIPLIER";
  multiplier?: string;
  basisVersion?: string;
  id: string;
  skuCode: string;
  skuName: string;
  component: string;
  currency: string;
  unit: string;
  taxMode: string;
  unitPrice: Money;
}

export interface SupplierQuoteOptionDTO {
  skuId?: import("./quoteContract.types").QuoteId;
  officialPrice: Money;
  basisVersion: string;
  key: string;
  skuCode: string;
  skuName: string;
  component: string;
  currency: string;
  unit: string;
  taxMode: string;
  lastUnitPrice?: Money;
}

export interface SupplierPortalQuoteDetailDTO extends SupplierPortalQuoteSummaryDTO {
  supplyConstraints?: SupplierQuoteSupplyConstraints;
  previousQuoteId?: string;
  items: SupplierPortalQuoteItemDTO[];
  remark?: string;
  approvalComment?: string;
}

export interface CreateSupplierPortalQuoteRequest {
  supplyConstraints?: SupplierQuoteSupplyConstraints;
  effectiveFrom: IsoDateTime;
  effectiveTo?: IsoDateTime;
  remark?: string;
  items: Array<Omit<SupplierPortalQuoteItemDTO, "id">>;
}

export interface SupplierQuoteSupplyConstraints {
  maxConcurrency?: string;
  rpm?: string;
  tpm?: string;
  actualContextWindow?: string;
  compatibilityNote?: string;
}

export interface SupplierPortalQuoteActionDTO {
  id: string;
  status: SupplierQuoteStatus;
  submittedAt?: IsoDateTime;
}

export interface SupplierQuoteImportTemplateDTO {
  fileName: string;
  content: string;
}

export interface ValidateSupplierQuoteImportRequest {
  fileName: string;
  content: string;
}

export interface SupplierQuoteImportPreviewRowDTO {
  rowNumber: number;
  skuCode: string;
  skuName: string;
  component: string;
  currency: string;
  unit: string;
  taxMode: string;
  unitPrice: Money;
  effectiveFrom: IsoDateTime;
  effectiveTo?: IsoDateTime;
  errors: string[];
}

export interface SupplierQuoteImportPreviewDTO {
  batchId: string;
  fileName: string;
  totalRows: number;
  validRows: number;
  invalidRows: number;
  rows: SupplierQuoteImportPreviewRowDTO[];
}

export interface SupplierQuoteImportResultDTO {
  batchId: string;
  quoteId: string;
  quoteNo: string;
  status: "DRAFT";
  importedRows: number;
}
