import { apiRequest } from "./http";
import type { SupplierQualificationSubmissionDTO } from "./supplierAccount.types";

export type ReviewKind = "qualifications";
export interface SupplierReview {
  id: string;
  supplierName: string;
  name: string;
  status: SupplierQualificationSubmissionDTO["status"];
  submittedAt?: string;
  material: SupplierQualificationSubmissionDTO;
  history: { result: "APPROVED" | "REJECTED"; reason: string; at: string; reviewer: string }[];
}
// PROVISIONAL: review URLs and M1:A / M3:A permission contract await backend confirmation.
export const supplierReviewsApi = {
  get: (kind: ReviewKind, id: string) => apiRequest<SupplierReview>({ url: `/internal/supplier-reviews/${kind}/${id}` }),
  list: (kind: ReviewKind, params: { page: number; size: number; search: string; status: string }) => apiRequest<{ list: SupplierReview[]; total: number }>({ url: `/internal/supplier-reviews/${kind}`, params }),
  review: (kind: ReviewKind, id: string, payload: { result: "APPROVED" | "REJECTED"; reason: string }) => apiRequest<SupplierReview>({ url: `/internal/supplier-reviews/${kind}/${id}/review`, method: "POST", data: payload, idempotency: { scope: `supplier-review:${kind}:${id}`, payload } }),
};
