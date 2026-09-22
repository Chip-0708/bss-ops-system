import type { IsoDateTime } from "../domain/common";
import type { Money } from "../domain/money";
import type { QuoteComponentType } from "./quoteContract.types";

// 采集批次（后端 GET /internal/price-sync/jobs）。MVP 人工录入模式：创建即 SUCCESS。
export type SyncJobType = "SYNC_MODELS" | "SYNC_PRICES" | "SYNC_COMMUNITY";
export type SyncJobStatus = "RUNNING" | "SUCCESS" | "FAILED";

export interface SyncJobDTO {
  id: number | string;
  job_type: SyncJobType;
  source: string;
  status: SyncJobStatus;
  started_at: IsoDateTime;
  finished_at: IsoDateTime | null;
  error_msg: string | null;
  item_count: number | null;
}

export interface CreateSyncJobRequest {
  job_type: SyncJobType;
  source: string;
  sku_ids?: number[];
}

// 暂存区（后端 GET /internal/staging-prices）。diff 由后端读侧实时计算。
export type StagingDiffStatus = "NEW" | "CHANGED" | "UNCHANGED" | "UNMATCHED";
export type StagingMatchStatus = "MATCHED" | "UNMATCHED";

export interface StagingDiffDetailDTO {
  component_type: QuoteComponentType;
  old_price: Money | null;
  new_price: Money;
  delta_pct: string | null;
}

export interface StagingPriceDTO {
  id: number | string;
  sync_job_id: number | string;
  sku_id: number | string | null;
  raw_sku_code: string | null;
  currency: string;
  payload: Partial<Record<QuoteComponentType, Money>>;
  match_status: StagingMatchStatus;
  diff_status: StagingDiffStatus;
  diff_detail: StagingDiffDetailDTO[];
  processed: boolean;
  created_at: IsoDateTime;
}

// 录入一行（后端 POST /internal/staging-prices）。sku_id 与 raw_sku_code 二选一。
export interface StagingItemInput {
  sku_id?: number;
  raw_sku_code?: string;
  currency: string;
  payload: Partial<Record<QuoteComponentType, Money>>;
}

export interface CreateStagingRequest {
  sync_job_id: number;
  items: StagingItemInput[];
}

export interface CreateStagingResultDTO {
  created_count: number;
  staging_ids: number[];
}

// 确认建单（后端 POST /internal/staging-prices/confirm）。effective_time 只允许 ≤now。
export interface ConfirmStagingRequest {
  sync_job_id: number;
  staging_ids: number[];
  effective_time: string;
}

export interface ConfirmStagingResultDTO {
  change_request_id: number | string;
  step_count: number;
}
