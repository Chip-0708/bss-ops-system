import type { PageResult } from "../domain/common";
import { assertContractId } from "../domain/contractId";
import { apiRequest } from "./http";
import type { CustomerListItemDTO, CustomerListParams, CustomerTransferDraft, CustomerTransferImpactDTO, CustomerTransferResultDTO } from "./customers.types";

const basePath = "/internal/customers";

interface CustomerWireDTO {
  id: string | number;
  subject_id: string | number;
  legal_name: string;
  level_code: string;
  owner_sales_operator_id: string | number;
  owner_sales_name: string;
  status: string;
  credit_limit: string;
  credit_used: string;
  deposit_amount: string;
  deposit_status: string;
  created_at: string;
}

interface TransferImpactWireDTO {
  customer_id: string | number;
  from_operator_id: string | number;
  from_operator_name: string;
  to_operator_id: string | number;
  to_operator_name: string;
  quote_count: number;
  price_book_count: number;
  reason: string;
}

interface TransferResultWireDTO {
  customer_id: string | number;
  from_operator_id: string | number;
  to_operator_id: string | number;
  quotes_migrated: number;
  transferred_at: string;
}

function contractId(value: unknown) {
  assertContractId(value);
  return String(value);
}

function requestId(value: string) {
  assertContractId(value);
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw new Error("ID超出前端安全范围，请后端支持字符串ID请求。");
  return parsed;
}

function customerFromWire(row: CustomerWireDTO): CustomerListItemDTO {
  return {
    id: contractId(row.id), subjectId: contractId(row.subject_id), legalName: row.legal_name,
    levelCode: row.level_code, ownerSalesOperatorId: contractId(row.owner_sales_operator_id),
    ownerSalesName: row.owner_sales_name, status: row.status, creditLimit: row.credit_limit,
    creditUsed: row.credit_used, depositAmount: row.deposit_amount, depositStatus: row.deposit_status,
    createdAt: row.created_at,
  };
}

export const customersApi = {
  list: (params: CustomerListParams) => apiRequest<PageResult<CustomerWireDTO>>({ url: basePath, method: "GET", params }).then(data => ({
    ...data, list: data.list.map(customerFromWire),
  })),
  transferPreview: (customerId: string, draft: Omit<CustomerTransferDraft, "confirm">) => {
    const payload = { to_operator_id: requestId(draft.toOperatorId), reason: draft.reason, confirm: false };
    return apiRequest<TransferImpactWireDTO>({ url: `${basePath}/${requestId(customerId)}/transfer`, method: "POST", data: payload,
      idempotency: { scope: `customer:transfer-preview:${customerId}`, payload } }).then((data): CustomerTransferImpactDTO => ({
        customerId: contractId(data.customer_id), fromOperatorId: contractId(data.from_operator_id), fromOperatorName: data.from_operator_name,
        toOperatorId: contractId(data.to_operator_id), toOperatorName: data.to_operator_name, quoteCount: data.quote_count,
        priceBookCount: data.price_book_count, reason: data.reason,
      }));
  },
  transferConfirm: (customerId: string, draft: Omit<CustomerTransferDraft, "confirm">) => {
    const payload = { to_operator_id: requestId(draft.toOperatorId), reason: draft.reason, confirm: true };
    return apiRequest<TransferResultWireDTO>({ url: `${basePath}/${requestId(customerId)}/transfer`, method: "POST", data: payload,
      idempotency: { scope: `customer:transfer-confirm:${customerId}`, payload } }).then((data): CustomerTransferResultDTO => ({
        customerId: contractId(data.customer_id), fromOperatorId: contractId(data.from_operator_id), toOperatorId: contractId(data.to_operator_id),
        quotesMigrated: data.quotes_migrated, transferredAt: data.transferred_at,
      }));
  },
};
