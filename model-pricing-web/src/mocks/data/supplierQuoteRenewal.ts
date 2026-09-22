import { ApiError } from "../../types.ts";
import type { SupplierQuoteDetailDTO } from "../../api/supplierQuotes.types";

// PROVISIONAL: eligible renewal states and quote-number versioning await backend confirmation.
export function supplierQuoteRenewalMetadata(records: SupplierQuoteDetailDTO[], supplierId: string, sourceId: string) {
  const source = records.find(row => row.id === sourceId && row.supplierId === supplierId);
  if (!source) throw new ApiError(404, "原报价不存在或不属于当前供应商");
  if (!["EFFECTIVE", "EXPIRED", "VOIDED", "REJECTED"].includes(source.status)) throw new ApiError(409, "仅已生效、已结束或已驳回报价可续报；草稿请直接编辑，审批中的报价请等待结果");
  const version = Math.max(...records.filter(row => row.supplierId === supplierId && row.quoteNo === source.quoteNo).map(row => row.version)) + 1;
  return { source, quoteNo: source.quoteNo, version, previousQuoteId: source.id };
}
