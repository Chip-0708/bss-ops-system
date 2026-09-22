import type { IsoDateTime } from "../domain/common";
import type { Money } from "../domain/money";

export interface SupplierListParams {
  page: number;
  size: number;
  keyword?: string;
  status?: string;
  qual_status?: string;
}

export interface SupplierSummaryDTO {
  id: string;
  subjectId: string;
  legalName: string;
  settlementCurrency: "CNY" | "USD";
  qualStatus: string;
  settleStatus: string;
  status: string;
  ownerProcurementOperatorId: string;
  ownerProcurementName: string;
  skuCount: number;
  effectiveQuoteCount: number;
  expiringSoon: number;
  updatedAt: IsoDateTime;
}

export interface SupplierDetailDTO {
  id: string;
  subjectId: string;
  legalName: string;
  settlementCurrency: "CNY" | "USD";
  settleStatus: string;
  qualStatus: string;
  status: string;
  ownerProcurementOperatorId: string;
  ownerProcurementName: string;
  createdAt: IsoDateTime;
  updatedAt: IsoDateTime;
  // The backend physically omits restricted commercial fields.
  settleType?: string;
  billingCycle?: number;
  minRecharge?: Money;
  creditLine?: Money;
  creditUsed?: Money;
  depositAmount?: Money;
}
