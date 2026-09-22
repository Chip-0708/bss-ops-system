import type { IsoDateTime } from "../domain/common";

export interface AuditListParams {
  page: number;
  size: number;
  action?: string;
  targetType?: string;
  targetId?: string;
  operatorId?: string;
  operatorRole?: string;
  sourceType?: string;
  from?: string;
  to?: string;
}

export interface AuditLogSummaryDTO {
  id: string;
  occurredAt: IsoDateTime;
  operatorName: string;
  operatorType: "USER" | "SYSTEM";
  action: string;
  module: string;
  entityType: string;
  entityId: string;
  result?: "SUCCESS" | "FAILURE";
  source: string;
  requestId: string;
}

export interface AuditLogDetailDTO extends AuditLogSummaryDTO {
  reason?: string;
  before?: Record<string, unknown>;
  after?: Record<string, unknown>;
  impact: string[];
}

export interface AuditExportDTO {
  fileName: string;
  blob: Blob;
}
