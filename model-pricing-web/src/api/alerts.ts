import type { PageResult } from "../domain/common";
import { apiRequest } from "./http";
import type {
  AlertActionResultDTO,
  AlertDetailDTO,
  AlertListParams,
  AlertSummaryDTO,
} from "./alerts.types";

const basePath = "/internal/alerts";

interface AlertWireDTO {
  id: string | number;
  alert_type: string;
  severity: AlertSummaryDTO["level"];
  target_type: string;
  target_id: string | number;
  message: string;
  status: AlertSummaryDTO["status"];
  assigned_role: string;
  handle_note: string;
  resolved_at?: string | null;
  created_at: string;
  updated_at: string;
}

interface AlertActionWireDTO {
  alert_id: string | number;
  status: AlertActionResultDTO["status"];
  todo_id?: string | number;
}

const mapAlert = (row: AlertWireDTO): AlertDetailDTO => ({
  id: String(row.id), title: row.alert_type, module: row.target_type, level: row.severity, status: row.status,
  entityType: row.target_type, entityId: String(row.target_id), triggeredAt: row.created_at, ownerName: row.assigned_role,
  description: row.message, evidence: [`告警类型：${row.alert_type}`], suggestedAction: row.handle_note || "请按业务流程核查并处理。",
  history: row.handle_note ? [{ at: row.updated_at, actor: row.assigned_role || "处理人", action: row.status, note: row.handle_note }] : [],
  canHandle: row.status === "OPEN" || row.status === "HANDLING",
});

function action(id: string, actionName: "HANDLE" | "RESOLVE" | "IGNORE" | "TO_TICKET", note = "", createTodo = false) {
  const payload = { alert_id: Number(id), action: actionName, note: note.trim(), create_todo: createTodo };
  return apiRequest<AlertActionWireDTO>({ url: basePath, method: "POST", data: payload,
    idempotency: { scope: `alert:${actionName.toLowerCase()}:${id}`, payload } }).then((result): AlertActionResultDTO => ({
      id: String(result.alert_id), status: result.status,
      todoId: result.todo_id === undefined ? undefined : String(result.todo_id),
    }));
}

export const alertsApi = {
  list: (params: AlertListParams) =>
    apiRequest<PageResult<AlertWireDTO>>({ url: basePath, method: "GET", params: {
      page: params.page, size: params.size, alert_type: params.alertType, severity: params.severity, status: params.status,
    } }).then(result => ({ ...result, list: result.list.map(mapAlert) })),
  acknowledge: (id: string) => action(id, "HANDLE"),
  resolve: (id: string, reason: string) => action(id, "RESOLVE", reason),
};
