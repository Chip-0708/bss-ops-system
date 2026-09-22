import { apiRequest } from "./http";
import type {
  CreateSupplierQualificationRequest,
  SupplierPortalProfileDTO,
  SupplierQualificationSubmissionDTO,
  UpdateSupplierPortalProfileRequest,
} from "./supplierAccount.types";

// PROVISIONAL: paths, editable fields and upload flow must be confirmed with the Go backend.
export const supplierAccountApi = {
  getProfile: () =>
    apiRequest<SupplierPortalProfileDTO>({ url: "/supplier/profile", method: "GET" }),
  updateProfile: (payload: UpdateSupplierPortalProfileRequest) =>
    apiRequest<SupplierPortalProfileDTO>({
      url: "/supplier/profile",
      method: "PUT",
      data: payload,
      idempotency: { scope: "supplier:profile:update", payload },
    }),
  listQualifications: () =>
    apiRequest<SupplierQualificationSubmissionDTO[]>({
      url: "/supplier/qualifications",
      method: "GET",
    }),
  submitQualification: (payload: CreateSupplierQualificationRequest) =>
    apiRequest<SupplierQualificationSubmissionDTO>({
      url: "/supplier/qualifications",
      method: "POST",
      data: payload,
      idempotency: { scope: "supplier:qualification:submit", payload },
    }),
};
