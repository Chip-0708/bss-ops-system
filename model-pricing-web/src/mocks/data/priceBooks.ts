import Decimal from "decimal.js";
import type { PriceBookStatus } from "../../domain/status";

export interface LegacyPriceBookItemFixture {
  id: string;
  skuId: string;
  skuCode: string;
  skuName: string;
  component: "INPUT" | "OUTPUT" | "CACHE";
  unitPrice: string;
  floorPrice: string;
  currency: string;
  unit: string;
  costBaselineId: string;
  costVersion: number;
  validationStatus: "OK" | "BELOW_FLOOR";
  validationMessage?: string;
}

export interface LegacyPriceBookFixture {
  id: string;
  code: string;
  name: string;
  levelCode: string;
  currency: string;
  status: PriceBookStatus;
  version: number;
  itemCount: number;
  effectiveFrom?: string;
  updatedAt: string;
  ownerName: string;
  description: string;
  items: LegacyPriceBookItemFixture[];
  canEdit: boolean;
  canSubmit: boolean;
}

const baseItems = [
  { skuId: "9007199254741000", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "INPUT" as const, floorPrice: "2.40000000", costBaselineId: "baseline-gpt-4.1-v8", costVersion: 8 },
  { skuId: "9007199254741000", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "OUTPUT" as const, floorPrice: "9.60000000", costBaselineId: "baseline-gpt-4.1-v8", costVersion: 8 },
  { skuId: "9007199254741002", skuCode: "claude-sonnet-4", skuName: "Claude Sonnet 4", component: "INPUT" as const, floorPrice: "3.80000000", costBaselineId: "baseline-claude-sonnet-4-v4", costVersion: 4 },
];

function items(prices: string[], bookId: string): LegacyPriceBookItemFixture[] {
  return baseItems.map((item, index) => {
    const unitPrice = prices[index] || item.floorPrice;
    const below = new Decimal(unitPrice).lessThan(item.floorPrice);
    return { ...item, id: `${bookId}-item-${index + 1}`, unitPrice, currency: "USD", unit: "百万 Token", validationStatus: below ? "BELOW_FLOOR" : "OK", validationMessage: below ? "当前售价低于服务端返回的 floor，不能提交审批" : undefined };
  });
}

function book(id: string, code: string, name: string, levelCode: string, status: PriceBookStatus, version: number, prices: string[], effectiveFrom?: string): LegacyPriceBookFixture {
  const detailItems = items(prices, id);
  return { id, code, name, levelCode, currency: "USD", status, version, itemCount: detailItems.length, effectiveFrom, updatedAt: "2026-09-11T09:30:00+08:00", ownerName: "周婷", description: `${levelCode} 客户等级基础价目表`, items: detailItems, canEdit: false, canSubmit: false };
}

export const priceBookRecords: LegacyPriceBookFixture[] = [
  // Explicit sample assigned to customer-blue; not created by an approval action.
  book("pb-customer-gold-demo", "PB-GOLD-202609", "金牌客户标准价（开发样例）", "GOLD", "EFFECTIVE", 1, ["2.80000000", "10.80000000", "4.26000000"], "2026-09-01T00:00:00+08:00"),
  book("pb-001", "PB-GOLD-004", "金牌客户价目表", "GOLD", "DRAFT", 4, ["2.78000000", "10.80000000", "4.26000000"], "2026-09-20T00:00:00+08:00"),
  book("pb-002", "PB-SILVER-006", "银牌客户价目表", "SILVER", "APPROVING", 6, ["3.00000000", "11.20000000", "4.50000000"], "2026-09-18T00:00:00+08:00"),
  book("pb-003", "PB-STANDARD-009", "标准客户价目表", "STANDARD", "PENDING_EFFECTIVE", 9, ["3.20000000", "12.00000000", "4.80000000"], "2026-09-15T00:00:00+08:00"),
  book("pb-004", "PB-ECONOMY-012", "经济型价目表", "ECONOMY", "EFFECTIVE", 12, ["2.90000000", "11.00000000", "4.30000000"], "2026-09-01T00:00:00+08:00"),
  book("pb-005", "PB-LEGACY-003", "历史合同参考价", "LEGACY", "FROZEN", 3, ["2.65000000", "10.20000000", "4.00000000"]),
];
