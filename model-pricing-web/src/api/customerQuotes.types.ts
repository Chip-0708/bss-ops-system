import type { IsoDateTime } from "../domain/common";
import type { Money } from "../domain/money";
import type { CustomerQuoteStatus } from "../domain/status";

export type CustomerQuoteType = "APPLY" | "CLONE" | "TEMP";

export interface GenerateCustomerQuoteItem {
  skuId: string;
  unitPrice: Money;
}

export interface GenerateCustomerQuoteDraft {
  customerId: string;
  quoteType: CustomerQuoteType;
  sourceQuoteId?: string;
  validTo?: IsoDateTime;
  items?: GenerateCustomerQuoteItem[];
}

export interface FloorViolationDTO {
  skuId: string;
  skuCode: string;
  unitPrice: Money;
  floorPrice: Money;
}

export interface GeneratedCustomerQuoteDTO {
  id: string;
  customerId: string;
  versionNo: number;
  status: "DRAFT";
  quoteType: CustomerQuoteType;
  itemCount: number;
  belowFloorCount: number;
  floorViolations: FloorViolationDTO[];
  priceBookVersion: number;
  validUntil?: IsoDateTime;
  ownerSalesOperatorId: string;
  createdAt: IsoDateTime;
}

export interface CustomerQuotePreviewItemDTO {
  skuId: string;
  skuCode: string;
  currency: string;
  unitPrice: Money;
}

export interface CustomerPriceBookPreviewDTO {
  id: string;
  levelCode: string;
  versionNo: number;
  items: CustomerQuotePreviewItemDTO[];
}

export interface CustomerQuoteHistoryPreviewDTO {
  id: string;
  versionNo: number;
  status: string;
  quoteType: string;
  validUntil?: IsoDateTime;
  priceBookVersion: number;
  createdAt: IsoDateTime;
  items: CustomerQuotePreviewItemDTO[];
}

export interface CustomerQuoteContextDTO {
  customerId: string;
  levelCode: string;
  priceBook?: CustomerPriceBookPreviewDTO;
  history: {
    list: CustomerQuoteHistoryPreviewDTO[];
    total: number;
    page: number;
    size: number;
  };
}

export interface SpecialPriceResultDTO {
  quoteId: string;
  changeRequestId: string;
  stepCount: number;
  status: string;
  marginImpact?: { currentPrice: Money; currentMargin: Money; targetPrice: Money; targetMargin: Money; deltaGapDistance: Money };
}

export interface RefreshQuoteResultDTO {
  quoteId: string;
  oldVersionNo: number;
  newVersionNo: number;
  changedItems: Array<{ skuId: string; oldFloorPrice: Money; newFloorPrice: Money }>;
  unchanged: boolean;
  status: string;
}

export interface CustomerQuoteListParams {
  page: number;
  size: number;
  search?: string;
  customerId?: string;
  status?: CustomerQuoteStatus | "";
}

export interface CustomerQuoteSummaryDTO {
  id: string;
  quoteNo: string;
  name: string;
  customerId: string;
  customerName: string;
  status: CustomerQuoteStatus;
  validTo?: IsoDateTime;
  itemCount: number;
  currency: string;
  ownerName: string;
  updatedAt: IsoDateTime;
}

export interface CustomerQuoteItemDTO {
  skuId: string;
  skuCode: string;
  skuName: string;
  component: "INPUT" | "OUTPUT";
  unitPrice: Money;
  referencePrice: Money;
  floorPrice: Money;
  currency: string;
  unit: string;
}

export interface CustomerQuoteDetailDTO extends CustomerQuoteSummaryDTO {
  previousQuoteId?: string;
  acceptedAt?: IsoDateTime;
  priceBookCode?: string;
  priceBookName?: string;
  validFrom?: IsoDateTime;
  reason: string;
  items: CustomerQuoteItemDTO[];
  canEdit: boolean;
}

export interface CustomerQuoteDraft {
  priceBookCode?: string;
  name: string;
  customerId: string;
  validTo?: IsoDateTime;
  reason: string;
  items: CustomerQuoteItemDTO[];
}

export interface CustomerQuoteTemplateDTO {
  priceBookCode: string;
  priceBookName: string;
  items: CustomerQuoteItemDTO[];
}

export interface CustomerQuoteSaveResultDTO {
  id: string;
  quoteNo: string;
  status: "DRAFT";
}
