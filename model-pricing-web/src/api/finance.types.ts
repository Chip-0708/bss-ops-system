import type { IsoDateTime } from "../domain/common";
import type { Money } from "../domain/money";
import type { FinanceAccountStatus, FxRateStatus } from "../domain/status";

export interface FxRateListParams {
  page: number;
  size: number;
  search?: string;
  month?: string;
  status?: FxRateStatus | "";
}

export interface FxRateDTO {
  id: string;
  month: string;
  baseCurrency: string;
  quoteCurrency: string;
  rate: string;
  source: string;
  status: FxRateStatus;
  lockedAt?: IsoDateTime;
  lockedBy?: string;
  updatedAt: IsoDateTime;
}

export interface FinanceAccountListParams {
  page: number;
  size: number;
  search?: string;
  status?: FinanceAccountStatus | "";
}

export interface FinanceAccountSummaryDTO {
  customerId: string;
  customerCode: string;
  customerName: string;
  levelCode: string;
  status: FinanceAccountStatus;
  currency: string;
  creditLimit: Money;
  usedCredit: Money;
  availableCredit: Money;
  depositBalance: Money;
  paymentTerms: string;
  updatedAt: IsoDateTime;
}

export interface FinanceTransactionDTO {
  id: string;
  occurredAt: IsoDateTime;
  type: "CREDIT_USAGE" | "CREDIT_RELEASE" | "DEPOSIT_IN" | "DEPOSIT_OUT";
  amount: Money;
  currency: string;
  referenceNo: string;
  note: string;
}

export interface FinanceAccountDetailDTO extends FinanceAccountSummaryDTO {
  billingEntity: string;
  invoiceTitle: string;
  taxNoMasked: string;
  settlementCycle: string;
  warningThresholdRate: string;
  recentTransactions: FinanceTransactionDTO[];
  notes: string[];
}
