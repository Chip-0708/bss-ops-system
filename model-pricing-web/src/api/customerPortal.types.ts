import type { IsoDateTime, PageResult } from "../domain/common";
import type { Money } from "../domain/money";
import type { CustomerQuoteStatus } from "../domain/status";

export type CustomerSection = "price-book" | "quotes" | "contracts" | "billing" | "notifications";
export interface CustomerListParams { page: number; size: number }
export interface CustomerQuoteListParams extends CustomerListParams { kind: "QUOTE" | "CONTRACT"; status?: string }

export interface CustomerPriceItem {
  skuId: string;
  skuCode: string;
  currency: string;
  unitPrice: Money;
}

export interface CustomerPriceBook {
  levelCode: string;
  versionNo: number;
  currency: string;
  items: CustomerPriceItem[];
}

export interface PortalCustomerQuote {
  id: string;
  versionNo: number;
  status: string;
  quoteType: string;
  validUntil?: IsoDateTime | null;
  itemCount: number;
  totalAmount: Money;
  currency: string;
  sourceKind: "QUOTE" | "CONTRACT";
  contractFrom?: IsoDateTime | null;
  contractTo?: IsoDateTime | null;
  canAccept?: boolean;
  acceptedAt?: IsoDateTime;
}

export interface CustomerBilling {
  creditLimit: Money;
  creditUsed: Money;
  depositAmount: Money;
  depositStatus: string;
  billingCycle: number;
  bills: unknown[];
}

export interface CustomerNotification {
  id: string;
  type: string;
  title: string;
  content: string;
  readAt?: IsoDateTime | null;
  createdAt: IsoDateTime;
}

export interface CustomerHome {
  balance: Omit<CustomerBilling, "billingCycle" | "bills">;
  pendingCount: number;
  unreadNotifications: number;
  commonModels: Array<{ skuId: string; skuCode: string; currency: string }>;
}

export interface AcceptCustomerQuoteResult { quoteId: string; newStatus: string; contractCount: number }

// Legacy Mock-only records kept for isolated UI tests. Real API calls use the wire-aligned types above.
export interface CustomerAccount { currency: string; balance: Money; creditLimit: Money; usedCredit: Money; deposit: Money }
export interface CustomerRecord { id: string; code: string; name: string; status: string; updatedAt: IsoDateTime }
export interface LegacyCustomerPriceItem { skuCode: string; skuName: string; component: string; unitPrice: Money; currency: string; unit: string }
export interface LegacyCustomerPriceBook extends CustomerRecord { kind: "price-book"; items: LegacyCustomerPriceItem[]; effectiveFrom: IsoDateTime; sourceLabel: string }
export interface LegacyPortalCustomerQuote extends CustomerRecord { kind: "quotes"; status: CustomerQuoteStatus; items: LegacyCustomerPriceItem[]; validFrom?: IsoDateTime; validTo?: IsoDateTime; canAccept: boolean; acceptedAt?: IsoDateTime }
export interface PortalCustomerContract extends CustomerRecord { kind: "contracts"; items: LegacyCustomerPriceItem[]; validFrom: IsoDateTime; validTo: IsoDateTime }
export interface CustomerBill extends CustomerRecord { kind: "billing"; period: string; currency: string; totalAmount: Money; items: Array<{ skuCode: string; description: string; amount: Money }> }
export interface LegacyCustomerNotification extends CustomerRecord { kind: "notifications"; content: string; category: "PRICE_CHANGE" | "MODEL_RETIREMENT" }
export type CustomerPortalDetail = LegacyCustomerPriceBook | LegacyPortalCustomerQuote | PortalCustomerContract | CustomerBill | LegacyCustomerNotification;
export interface CustomerBillingPage extends PageResult<CustomerBill> { account: CustomerAccount }
