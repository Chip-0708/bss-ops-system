import type { SupplierQualificationSubmissionDTO } from "../../api/supplierAccount.types";
export const qualifications: SupplierQualificationSubmissionDTO[] = [
  { id: "supplier-qual-001", qualificationType: "BUSINESS_LICENSE", qualificationName: "营业执照", documentNoMasked: "9144**********8X", validFrom: "2025-07-01T00:00:00+08:00", validTo: "2027-06-30T23:59:59+08:00", status: "APPROVED", attachmentName: "business-license.pdf", submittedAt: "2026-06-27T10:00:00+08:00", reviewedAt: "2026-06-28T10:20:00+08:00", reviewComment: "资料齐全，审核通过。" },
  { id: "supplier-qual-002", qualificationType: "PARTNER_AUTHORIZATION", qualificationName: "云服务合作授权", documentNoMasked: "AUTH-****-0618", validTo: "2027-06-18T23:59:59+08:00", status: "APPROVED", attachmentName: "partner-authorization.pdf", submittedAt: "2026-06-19T09:10:00+08:00", reviewedAt: "2026-06-20T14:10:00+08:00" },
  { id: "supplier-qual-003", qualificationType: "SECURITY_REPORT", qualificationName: "数据安全说明", documentNoMasked: "SEC-****-0901", status: "REVIEWING", attachmentName: "security-report-2026.pdf", submittedAt: "2026-09-10T13:40:00+08:00" },
];
import type { SupplierModelApplicationDTO } from "../../api/supplierModelApplications.types";
export const applications: SupplierModelApplicationDTO[] = [
  { id: "101", supplierId: "7", modelName: "GPT 5.6 Mini", vendorId: "1", payload: { model_code: "gpt-5.6-mini", model_type: "TEXT", capabilities: ["文本生成", "工具调用"], business_reason: "补充低延迟模型供给。" }, duplicateCandidates: [{ skuId: "40", skuCode: "gpt-5-2026-04-11", matchedOn: "FAMILY", matchedValue: "GPT-5", similarity: 0.91 }], status: "SUBMITTED", mergedSkuId: null, rejectReason: null, createdAt: "2026-09-10T11:20:00+08:00", updatedAt: "2026-09-10T11:20:00+08:00" },
  { id: "102", supplierId: "7", modelName: "Vision Pro", vendorId: null, payload: { model_code: "vision-pro", model_type: "MULTIMODAL", business_reason: "补充多模态供给。" }, duplicateCandidates: [], status: "REJECTED", mergedSkuId: null, rejectReason: "官方计价资料不完整，请补充后重新申请。", createdAt: "2026-08-25T13:40:00+08:00", updatedAt: "2026-08-27T16:20:00+08:00" },
];
