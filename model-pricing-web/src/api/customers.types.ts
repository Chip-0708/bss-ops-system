import type { IsoDateTime } from "../domain/common";
import type { CustomerStatus } from "../domain/status";

export interface CustomerListParams {
  page: number;
  size: number;
  keyword?: string;
}

// Stage 9a real-backend list shape after API-layer ID normalization.
export interface CustomerListItemDTO {
  id: string;
  subjectId: string;
  legalName: string;
  levelCode: string;
  ownerSalesOperatorId: string;
  ownerSalesName: string;
  status: string;
  creditLimit: string;
  creditUsed: string;
  depositAmount: string;
  depositStatus: string;
  createdAt: IsoDateTime;
}

export interface CustomerTransferDraft {
  toOperatorId: string;
  reason: string;
  confirm: boolean;
}

export interface CustomerTransferImpactDTO {
  customerId: string;
  fromOperatorId: string;
  fromOperatorName: string;
  toOperatorId: string;
  toOperatorName: string;
  quoteCount: number;
  priceBookCount: number;
  reason: string;
}

export interface CustomerTransferResultDTO {
  customerId: string;
  fromOperatorId: string;
  toOperatorId: string;
  quotesMigrated: number;
  transferredAt: IsoDateTime;
}

export interface CustomerSummaryDTO {
  id: string;
  code: string;
  name: string;
  levelCode: string;
  status: CustomerStatus;
  ownerName: string;
  currentPriceBookName?: string;
  activeContractCount: number;
  quoteCount: number;
  updatedAt: IsoDateTime;
}

export interface CustomerContractDTO {
  id: string;
  contractNo: string;
  name: string;
  status: "ACTIVE" | "EXPIRING" | "ENDED";
  validFrom: IsoDateTime;
  validTo: IsoDateTime;
}

export interface CustomerDetailDTO extends CustomerSummaryDTO {
  legalName: string;
  industry: string;
  contactName: string;
  contactPhoneMasked: string;
  contactEmailMasked: string;
  billingCurrency: string;
  serviceRegions: string[];
  currentPriceBookCode?: string;
  contracts: CustomerContractDTO[];
  notes: string[];
}
