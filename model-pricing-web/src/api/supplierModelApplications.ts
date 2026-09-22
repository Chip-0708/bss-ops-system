import { assertContractId } from "../domain/contractId";
import { apiRequest } from "./http";
import type { DecideModelApplicationRequest, DuplicateCandidateDTO, InternalModelApplicationListParams, ModelApplicationPayload, SubmitSupplierModelApplicationRequest, SupplierModelApplicationDTO, SupplierModelApplicationListParams, SupplierModelApplicationPageDTO } from "./supplierModelApplications.types";

interface ApplicationWireDTO {
  id: string | number; supplier_id: string | number; model_name: string; vendor_id: string | number | null;
  payload: ModelApplicationPayload;
  dup_top3?: Array<{ sku_id: string | number; sku_code: string; matched_on: DuplicateCandidateDTO["matchedOn"]; matched_value: string; similarity: number }> | null;
  status: SupplierModelApplicationDTO["status"]; merged_sku_id: string | number | null; reject_reason: string | null;
  created_at: string; updated_at: string;
}
function id(value: string | number) { assertContractId(value); return String(value); }
function mapApplication(row: ApplicationWireDTO): SupplierModelApplicationDTO {
  return { id: id(row.id), supplierId: id(row.supplier_id), modelName: row.model_name,
    vendorId: row.vendor_id === null ? null : id(row.vendor_id), payload: row.payload,
    duplicateCandidates: (row.dup_top3 || []).map(item => ({ skuId: id(item.sku_id), skuCode: item.sku_code,
      matchedOn: item.matched_on, matchedValue: item.matched_value, similarity: item.similarity })), status: row.status,
    mergedSkuId: row.merged_sku_id === null ? null : id(row.merged_sku_id), rejectReason: row.reject_reason,
    createdAt: row.created_at, updatedAt: row.updated_at };
}
function mapPage(data: { list: ApplicationWireDTO[]; total: number; page: number; size: number }): SupplierModelApplicationPageDTO {
  return { ...data, list: data.list.map(mapApplication) };
}
const supplierBase = "/supplier/model-applications";
const internalBase = "/internal/model-applications";
export const supplierModelApplicationsApi = {
  list: (params: SupplierModelApplicationListParams) => apiRequest<{ list: ApplicationWireDTO[]; total: number; page: number; size: number }>({ url: supplierBase, method: "GET", params }).then(mapPage),
  submit: (draft: SubmitSupplierModelApplicationRequest) => {
    const payload = { model_name: draft.modelName, vendor_id: draft.vendorId, payload: draft.payload };
    return apiRequest<ApplicationWireDTO>({ url: supplierBase, method: "POST", data: payload,
      idempotency: { scope: "supplier:model-application:submit", payload } }).then(mapApplication);
  },
};
export const internalModelApplicationsApi = {
  list: (params: InternalModelApplicationListParams) => apiRequest<{ list: ApplicationWireDTO[]; total: number; page: number; size: number }>({ url: internalBase, method: "GET", params }).then(mapPage),
  decide: (applicationId: string, draft: DecideModelApplicationRequest) => {
    const payload = { action: draft.action, target_sku_id: draft.targetSkuId, reason: draft.reason };
    return apiRequest<ApplicationWireDTO>({ url: `${internalBase}/${encodeURIComponent(applicationId)}/decision`, method: "POST", data: payload,
      idempotency: { scope: `internal:model-application:${applicationId}:decision`, payload } }).then(mapApplication);
  },
};
