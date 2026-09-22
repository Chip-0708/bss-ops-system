import type { IsoDateTime, PageResult } from "../domain/common";

export type ChangeRequestType = "DEPRECATE" | "PRICE_UP" | "PRICE_DOWN" | "PRICE_BOOK_PUBLISH" | "PRICE_BOOK_ROLLBACK" | "SPECIAL_PRICE";
export type ChangeRequestStatus = "PENDING" | "APPROVED" | "REJECTED";

export interface ChangeRequestListParams {
  type?: ChangeRequestType;
  status?: ChangeRequestStatus;
  sku_id?: string;
  page: number;
  size: number;
}

export interface ChangeRequestItemDTO {
  id: string;
  changeType: ChangeRequestType;
  status: ChangeRequestStatus;
  skuId: string | null;
  riskLevel: string;
  stepsTotal: number;
  stepsApproved: number;
  pendingStepNo: number | null;
  pendingRole: string | null;
  createdBy: string;
  createdAt: IsoDateTime;
  updatedAt: IsoDateTime;
}

export interface ApprovalStepDTO {
  stepNo: number;
  requiredRole: string;
  decision: "APPROVED" | "REJECTED" | null;
  approverId: string | null;
  comment: string | null;
  decidedAt: IsoDateTime | null;
}

export interface ChangeRequestDetailDTO extends ChangeRequestItemDTO {
  payload?: unknown;
  marginPreview?: unknown;
  requestId?: string | null;
  updatedBy?: string | null;
  steps: ApprovalStepDTO[];
}

export type ChangeRequestPageDTO = PageResult<ChangeRequestItemDTO>;
