import type { IsoDateTime } from "../domain/common";
import type { Money } from "../domain/money";
import type { SupplierQuoteStatus } from "../domain/status";
import type { QuoteId, QuoteWriteItem } from "./quoteContract.types";

export interface SupplierQuoteListParams {
  page: number;
  size: number;
  search?: string;
  supplierId?: string;
  status?: SupplierQuoteStatus | "";
}

export interface SupplierQuoteSummaryDTO {
  id: string;
  quoteNo: string;
  version: number;
  supplierId: string;
  supplierName: string;
  source: "PORTAL" | "IMPORT" | "MANUAL";
  submittedAt?: IsoDateTime;
  updatedAt: IsoDateTime;
  effectiveFrom: IsoDateTime;
  effectiveTo?: IsoDateTime;
  skuCount: number;
  status: SupplierQuoteStatus;
  ownerName: string;
  alert?: string;
}

export interface SupplierQuotePriceComparisonDTO {
  skuId?: import("./quoteContract.types").QuoteId;
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
  proposedPrice: Money;
  previousPrice?: Money;
  officialPrice?: Money;
  bestPrice?: Money;
  delta?: Money;
  changeRate?: string;
}

export interface SupplierQuoteConstraintDTO {
  label: string;
  previous?: string;
  proposed?: string;
}

export interface SupplierQuoteMarginPreviewDTO {
  skuCode: string;
  currentMargin: string;
  proposedMargin: string;
  risk: "NORMAL" | "WARNING" | "INVERTED";
}

export interface SupplierQuoteDetailDTO extends SupplierQuoteSummaryDTO {
  supplyConstraints?: import("./supplierPortal.types").SupplierQuoteSupplyConstraints;
  previousQuoteId?: string;
  supplierCode: string;
  supplierContact: string;
  items: SupplierQuotePriceComparisonDTO[];
  constraints: SupplierQuoteConstraintDTO[];
  marginPreview: SupplierQuoteMarginPreviewDTO[];
  warnings: string[];
  comparisonGeneratedAt: IsoDateTime;
  comparisonReady: boolean;
  canApprove: boolean;
  approvalComment?: string;
  rejectionReason?: string;
}

export interface SupplierQuoteApproveResultDTO {
  id: string;
  status: "APPROVED_PENDING";
  approved_at: IsoDateTime;
  activate_at: IsoDateTime;
  immediate: boolean;
}

export interface SupplierQuoteRejectResultDTO {
  id: string;
  status: "REJECTED";
}

export type SupplierQuoteActionResultDTO = SupplierQuoteApproveResultDTO | SupplierQuoteRejectResultDTO;

export interface ExpiringQuoteDTO {
  id: QuoteId;
  supplier_id: QuoteId;
  supplier_name: string;
  version_no: number;
  valid_to: IsoDateTime;
  days_left: number;
  grace_until: IsoDateTime;
  in_grace: boolean;
  remove_confirmed: boolean;
  single_point: boolean;
  alert_level: "NORMAL" | "HIGH" | "URGENT";
  pending_remove: boolean;
}

export interface QuoteAnomalyDTO {
  quote_sheet_id: QuoteId;
  supplier_name: string;
  sku_id: QuoteId;
  sku_code: string;
  component_type: string;
  unit_price: string;
  prev_price: string | null;
  delta_pct: string | null;
  market_best: string | null;
  mkt_delta_pct: string | null;
  reason: "PREV_DEVIATION" | "MARKET_DEVIATION" | "BOTH";
  detected_at: IsoDateTime;
}

export interface ConfirmRemoveResultDTO {
  id: QuoteId;
  status: "EXPIRED";
  remove_confirmed: boolean;
  executed_at: IsoDateTime;
}

export interface RetroEffectiveRequestDTO {
  supplier_id: QuoteId;
  effective_time: IsoDateTime;
  valid_to: IsoDateTime;
  audit_reason: string;
  items: QuoteWriteItem[];
}

export interface RetroEffectiveResultDTO {
  id: QuoteId;
  status: "EFFECTIVE";
  retroactive: true;
  closed_previous_id: QuoteId | null;
  cost_recalc_queued: boolean;
  retro_count_this_month: number;
  alert_created: boolean;
}
