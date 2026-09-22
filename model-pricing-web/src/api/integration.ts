import { redactSensitive } from "../domain/sensitive";
import type { IsoDateTime } from "../domain/common";
import type { IntegrationStatus, JobRunStatus } from "../domain/status";
import { apiRequest } from "./http";

export interface IntegrationChannelDTO {
  id: string;
  name: string;
  category: "BACKEND" | "MODEL_VENDOR" | "NOTIFICATION" | "MCP";
  description: string;
  status: IntegrationStatus;
  basePath?: string;
  authentication: string;
  lastCheckedAt?: IsoDateTime;
  note: string;
}

export interface IntegrationJobDTO {
  id: string;
  name: string;
  schedule: string;
  status: JobRunStatus;
  lastStartedAt?: IsoDateTime;
  lastFinishedAt?: IsoDateTime;
  affectedRecords?: number;
  ownerModule: string;
  message: string;
}

export interface IntegrationEventDTO {
  id: string;
  eventType: string;
  direction: "INBOUND" | "OUTBOUND";
  producer: string;
  consumers: string[];
  status: IntegrationStatus;
  description: string;
}

export interface McpCapabilityDTO {
  id: string;
  name: string;
  mode: "READ" | "WRITE";
  permission: string;
  status: IntegrationStatus;
  boundary: string;
}

export interface IntegrationOverviewDTO {
  channels: IntegrationChannelDTO[];
  jobs: IntegrationJobDTO[];
  events: IntegrationEventDTO[];
  mcpCapabilities: McpCapabilityDTO[];
  generatedAt: IsoDateTime;
  environment: string;
}

// PROVISIONAL: operational status source and production observability contract remain TBD.
export const integrationApi = {
  getOverview: () => apiRequest<IntegrationOverviewDTO>({ url: "/internal/integration/overview", method: "GET" }).then(redactSensitive),
};
