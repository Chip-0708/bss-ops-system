import type { IsoDateTime, PageResult } from "../domain/common";

export type ModelApplicationStatus = "SUBMITTED" | "APPROVED" | "MERGED" | "REJECTED";
export type ModelApplicationDecision = "APPROVE" | "MERGE" | "REJECT";
export interface ModelApplicationPayload { model_code?: string; model_type?: string; official_url?: string; api_docs_url?: string; capabilities?: string[]; context_window?: string; business_reason?: string; [key: string]: unknown }
export interface DuplicateCandidateDTO { skuId: string; skuCode: string; matchedOn: "ALIAS" | "SKU_CODE" | "FAMILY"; matchedValue: string; similarity: number }
export interface SupplierModelApplicationDTO {
  id: string; supplierId: string; modelName: string; vendorId: string | null; payload: ModelApplicationPayload;
  duplicateCandidates: DuplicateCandidateDTO[]; status: ModelApplicationStatus; mergedSkuId: string | null;
  rejectReason: string | null; createdAt: IsoDateTime; updatedAt: IsoDateTime;
}
export interface SupplierModelApplicationListParams { page: number; size: number; status?: ModelApplicationStatus | "" }
export interface InternalModelApplicationListParams extends SupplierModelApplicationListParams { supplier_id?: string }
export interface SubmitSupplierModelApplicationRequest { modelName: string; vendorId?: number; payload: ModelApplicationPayload }
export interface DecideModelApplicationRequest { action: ModelApplicationDecision; targetSkuId?: number; reason?: string }
export type SupplierModelApplicationPageDTO = PageResult<SupplierModelApplicationDTO>;
