import type { IsoDateTime } from "../domain/common";
import type {
  QualificationStatus,
  QualificationSubmissionStatus,
  SupplierStatus,
} from "../domain/status";

export interface SupplierPortalProfileDTO {
  id: string;
  code: string;
  legalName: string;
  shortName: string;
  registrationNoMasked: string;
  status: SupplierStatus;
  qualificationStatus: QualificationStatus;
  contactName: string;
  contactPhoneMasked: string;
  contactEmail: string;
  registeredAddressMasked: string;
  serviceRegions: string[];
  settlementCurrency: string;
  paymentTerms: string;
  updatedAt: IsoDateTime;
}

export interface UpdateSupplierPortalProfileRequest {
  contactName: string;
  contactPhone?: string;
  contactEmail: string;
  serviceRegions: string[];
  contactAddress?: string;
}

export interface SupplierQualificationSubmissionDTO {
  id: string;
  qualificationType: string;
  qualificationName: string;
  documentNoMasked: string;
  validFrom?: IsoDateTime;
  validTo?: IsoDateTime;
  status: QualificationSubmissionStatus;
  attachmentName: string;
  submittedAt: IsoDateTime;
  reviewedAt?: IsoDateTime;
  reviewComment?: string;
}

export interface CreateSupplierQualificationRequest {
  qualificationType: string;
  qualificationName: string;
  documentNo: string;
  validFrom?: IsoDateTime;
  validTo?: IsoDateTime;
  attachmentName: string;
  remark?: string;
}
