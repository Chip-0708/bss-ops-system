import type { PageResult } from "../domain/common";
import { assertContractId } from "../domain/contractId";
import { apiRequest } from "./http";
import type { SupplierDetailDTO, SupplierListParams, SupplierSummaryDTO } from "./suppliers.types";

const basePath = "/internal/suppliers";

interface SupplierBaseWireDTO {
  id: string | number;
  subject_id: string | number;
  legal_name: string;
  settlement_currency: "CNY" | "USD";
  qual_status: string;
  settle_status: string;
  status: string;
  owner_procurement_operator_id: string | number;
  owner_procurement_name: string;
  updated_at: string;
}

interface SupplierSummaryWireDTO extends SupplierBaseWireDTO {
  sku_count: number;
  effective_quote_count: number;
  expiring_soon: number;
}

interface SupplierDetailWireDTO extends SupplierBaseWireDTO {
  created_at: string;
  settle_type?: string;
  billing_cycle?: number;
  min_recharge?: string;
  credit_line?: string;
  credit_used?: string;
  deposit_amount?: string;
}

function contractId(value: unknown): string {
  assertContractId(value);
  return String(value);
}

function requestId(value: string): number {
  assertContractId(value);
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw new Error("ID超出前端安全范围，请后端支持字符串ID请求。");
  return parsed;
}

function fromWire(row: SupplierBaseWireDTO) {
  return {
    id: contractId(row.id), subjectId: contractId(row.subject_id), legalName: row.legal_name,
    settlementCurrency: row.settlement_currency,
    qualStatus: row.qual_status, settleStatus: row.settle_status, status: row.status,
    ownerProcurementOperatorId: contractId(row.owner_procurement_operator_id),
    ownerProcurementName: row.owner_procurement_name, updatedAt: row.updated_at,
  };
}

export const suppliersApi = {
  list: (params: SupplierListParams) => apiRequest<PageResult<SupplierSummaryWireDTO>>({
    url: basePath, method: "GET", params,
  }).then((data): PageResult<SupplierSummaryDTO> => ({
    ...data, list: data.list.map(row => ({ ...fromWire(row), skuCount: row.sku_count,
      effectiveQuoteCount: row.effective_quote_count, expiringSoon: row.expiring_soon })),
  })),
  get: (id: string) => apiRequest<SupplierDetailWireDTO>({
    url: `${basePath}/${requestId(id)}`, method: "GET",
  }).then((data): SupplierDetailDTO => ({
    ...fromWire(data), createdAt: data.created_at,
    settleType: data.settle_type, billingCycle: data.billing_cycle,
    minRecharge: data.min_recharge, creditLine: data.credit_line,
    creditUsed: data.credit_used, depositAmount: data.deposit_amount,
  })),
};
