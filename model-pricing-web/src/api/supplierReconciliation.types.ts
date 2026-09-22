import type { IsoDateTime } from "../domain/common";
import type { Money } from "../domain/money";
import type { ReconciliationStatus } from "../domain/status";

export interface SupplierReconciliationListParams {
  page: number;
  size: number;
  search?: string;
  status?: ReconciliationStatus | "";
  period?: string;
}

export interface SupplierReconciliationSummaryDTO {
  id: string;
  statementNo: string;
  period: string;
  currency: string;
  usageAmount: Money;
  adjustmentAmount: Money;
  taxAmount: Money;
  payableAmount: Money;
  status: ReconciliationStatus;
  generatedAt: IsoDateTime;
  dueAt?: IsoDateTime;
}

export interface SupplierReconciliationLineDTO {
  id: string;
  skuCode: string;
  skuName: string;
  component: string;
  usageQuantity: string;
  unit: string;
  unitPrice: Money;
  amount: Money;
  adjustmentAmount: Money;
  note?: string;
}

export interface SupplierReconciliationDetailDTO extends SupplierReconciliationSummaryDTO {
  settlementAccountMasked: string;
  invoiceStatus: "NOT_REQUIRED" | "PENDING" | "RECEIVED";
  lines: SupplierReconciliationLineDTO[];
  notes: string[];
}
