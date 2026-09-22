import type { IsoDateTime } from "../domain/common";
import type { AlertLevel, AlertStatus } from "../domain/status";

export interface AlertListParams {
  page: number;
  size: number;
  alertType?: string;
  severity?: AlertLevel | "";
  status?: AlertStatus | "";
}

export interface AlertSummaryDTO {
  id: string;
  title: string;
  module: string;
  level: AlertLevel;
  status: AlertStatus;
  entityType: string;
  entityId: string;
  triggeredAt: IsoDateTime;
  ownerName?: string;
}

export interface AlertHistoryDTO {
  at: IsoDateTime;
  actor: string;
  action: string;
  note?: string;
}

export interface AlertDetailDTO extends AlertSummaryDTO {
  description: string;
  evidence: string[];
  suggestedAction: string;
  history: AlertHistoryDTO[];
  canHandle: boolean;
}

export interface AlertActionResultDTO {
  id: string;
  status: AlertStatus;
  todoId?: string;
}
