import { ApiError } from "../../types.ts";
import type { CustomerDetailDTO } from "../../api/customers.types";
import type { CustomerQuoteTemplateDTO } from "../../api/customerQuotes.types";
import type { LegacyPriceBookFixture } from "./priceBooks";

export function customerQuoteTemplate(customers: CustomerDetailDTO[], books: LegacyPriceBookFixture[], customerId: string, now = Date.now()): CustomerQuoteTemplateDTO {
  const customer = customers.find(row => row.id === customerId);
  if (!customer) throw new ApiError(404, "客户不存在或不可见");
  if (customer.status !== "ACTIVE") throw new ApiError(423, "客户已冻结或停用，不能生成报价");
  const book = books.find(row => row.code === customer.currentPriceBookCode);
  if (!book || book.status !== "EFFECTIVE" || !book.effectiveFrom || Date.parse(book.effectiveFrom) > now || !Number.isFinite(Date.parse(book.effectiveFrom))) throw new ApiError(409, "客户当前价目表尚不可用，请先完成价目表配置或发布，不能套用其他等级价格");
  const items = book.items.filter(row => row.component === "INPUT" || row.component === "OUTPUT").map(row => ({ skuId: row.skuId, skuCode: row.skuCode, skuName: row.skuName, component: row.component as "INPUT" | "OUTPUT", unitPrice: row.unitPrice, referencePrice: row.unitPrice, floorPrice: row.floorPrice, currency: row.currency, unit: row.unit }));
  if (!items.length) throw new ApiError(409, "当前价目表没有可报价组件");
  return structuredClone({ priceBookCode: book.code, priceBookName: book.name, items });
}
