import { redactSensitive } from "../domain/sensitive";
import type { PageResult } from "../domain/common";
import { apiFileRequest, apiRequest } from "./http";
import type { AuditExportDTO, AuditListParams, AuditLogDetailDTO, AuditLogSummaryDTO } from "./audit.types";

const basePath = "/internal/audit-logs";

interface AuditWireDTO {
  id: string | number;
  operator_id: string | number;
  operator_name: string;
  operator_role: string;
  action: string;
  target_type: string;
  target_id: string | number;
  before_value: string;
  after_value: string;
  source_type: string;
  source_id: string;
  request_id: string;
  created_at: string;
}

const jsonObject = (value: string) => {
  if (!value) return undefined;
  try { const parsed = JSON.parse(value); return parsed && typeof parsed === "object" ? parsed as Record<string, unknown> : { value: parsed }; }
  catch { return { raw: value }; }
};

const toRFC3339 = (value: string | undefined, endExclusive = false) => {
  if (!value) return undefined;
  const date = new Date(`${value}T00:00:00+08:00`);
  if (endExclusive) date.setDate(date.getDate() + 1);
  return date.toISOString();
};

const serializeParams = (value: AuditListParams) => ({
  page: value.page, size: value.size, action: value.action, target_type: value.targetType, target_id: value.targetId,
  operator_id: value.operatorId, operator_role: value.operatorRole, source_type: value.sourceType,
  from: toRFC3339(value.from), to: toRFC3339(value.to, true),
});

const mapAudit = (row: AuditWireDTO): AuditLogDetailDTO => ({
  id: String(row.id), occurredAt: row.created_at, operatorName: row.operator_name,
  operatorType: row.source_type === "SYSTEM" || row.source_type === "WORKER" ? "SYSTEM" : "USER",
  action: row.action, module: row.target_type, entityType: row.target_type, entityId: String(row.target_id),
  source: row.source_type, requestId: row.request_id,
  before: jsonObject(row.before_value), after: jsonObject(row.after_value), impact: [],
});

export const auditApi = {
  list: (params: AuditListParams) =>
    apiRequest<{ items: AuditWireDTO[]; total: number }>({ url: basePath, method: "GET", params: serializeParams(params) })
      .then(result => redactSensitive({ list: result.items.map(mapAudit), total: result.total, page: params.page, size: params.size }) as PageResult<AuditLogSummaryDTO>),
  export: async (range: { from: string; to: string; format?: "csv" | "xlsx" }): Promise<AuditExportDTO> => {
    const format = range.format || "csv";
    const blob = await apiFileRequest({ url: `${basePath}/export`, method: "GET", params: {
      from: toRFC3339(range.from), to: toRFC3339(range.to, true), format,
    } });
    return { fileName: `audit-logs.${format}`, blob };
  },
};
