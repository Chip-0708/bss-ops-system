import type { IsoDateTime } from "../domain/common";

// 成本参数（后端 GET/PUT /internal/cost/params，06-cost.md §7）。
// 比率字段是 numeric(8,4) 原样字符串（含尾零，如 "0.0300"）：必须按数值比较，勿按字符串相等。
export type CostParamScopeType = "MODEL" | "SUPPLIER";

export interface CostParamRatesDTO {
  loss_rate: string;
  channel_rate: string;
  tax_inclusive: boolean;
  withholding_tax: string;
}

export interface CostParamOverrideDTO extends CostParamRatesDTO {
  scope_type: CostParamScopeType;
  scope_id: number | string;
}

export interface CostParamsViewDTO {
  defaults: CostParamRatesDTO; // GLOBAL 行，本阶段只读
  overrides: CostParamOverrideDTO[];
}

// PUT 全量替换：overrides 是替换后的完整集（[] 合法清空）；defaults 只读，不得传。
export interface ReplaceCostParamsRequest {
  overrides: CostParamOverrideDTO[];
}

export interface ReplaceCostParamsResultDTO {
  overrides_count: number;
  submitted_tasks: number;
  task_ids: number[];
  audit_log_id: number | string;
}

export interface CostParamAuditFields {
  updatedAt?: IsoDateTime;
}
