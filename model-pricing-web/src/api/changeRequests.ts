import { assertContractId } from "../domain/contractId";
import { apiRequest } from "./http";
import type { ApprovalStepDTO, ChangeRequestDetailDTO, ChangeRequestItemDTO, ChangeRequestListParams, ChangeRequestPageDTO } from "./changeRequests.types";

interface ChangeRequestWireDTO {
  id: string | number;
  change_type: ChangeRequestItemDTO["changeType"];
  status: ChangeRequestItemDTO["status"];
  sku_id: string | number | null;
  risk_level: string;
  steps_total: number;
  steps_approved: number;
  pending_step_no: number | null;
  pending_role: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
}

interface ChangeRequestDetailWireDTO extends ChangeRequestWireDTO {
  payload?: unknown;
  margin_preview?: unknown;
  request_id?: string | null;
  updated_by?: string | null;
  steps: Array<{ step_no: number; required_role: string; decision: ApprovalStepDTO["decision"];
    approver_id: string | number | null; comment: string | null; decided_at: string | null }>;
}

function id(value: string | number) { assertContractId(value); return String(value); }
function item(row: ChangeRequestWireDTO): ChangeRequestItemDTO {
  return { id: id(row.id), changeType: row.change_type, status: row.status,
    skuId: row.sku_id === null ? null : id(row.sku_id), riskLevel: row.risk_level,
    stepsTotal: row.steps_total, stepsApproved: row.steps_approved,
    pendingStepNo: row.pending_step_no, pendingRole: row.pending_role,
    createdBy: row.created_by, createdAt: row.created_at, updatedAt: row.updated_at };
}

export const changeRequestsApi = {
  list: (params: ChangeRequestListParams) => apiRequest<{ list: ChangeRequestWireDTO[]; total: number; page: number; size: number }>({
    url: "/internal/change-requests", method: "GET", params,
  }).then((data): ChangeRequestPageDTO => ({ ...data, list: data.list.map(item) })),
  get: (requestId: string) => apiRequest<ChangeRequestDetailWireDTO>({
    url: `/internal/change-requests/${encodeURIComponent(requestId)}`, method: "GET",
  }).then((data): ChangeRequestDetailDTO => ({ ...item(data), payload: data.payload, marginPreview: data.margin_preview,
    requestId: data.request_id, updatedBy: data.updated_by,
    steps: data.steps.map(step => ({ stepNo: step.step_no, requiredRole: step.required_role, decision: step.decision,
      approverId: step.approver_id === null ? null : id(step.approver_id), comment: step.comment, decidedAt: step.decided_at })) })),
};
