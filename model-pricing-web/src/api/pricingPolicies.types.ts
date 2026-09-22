import type { PricingPolicyStatus } from "../domain/status";

export type PricingStrategyType = "TARGET_MARGIN" | "OFFICIAL_ANCHOR";
export type PricingPolicyWireMethod = "MARGIN" | "COST_UP" | "OFFICIAL_ANCHOR" | "FIXED";
export type PricingPolicyWireScope = "ALL" | "VENDOR" | "FAMILY" | "SKU";

export const DEFAULT_PRICING_POLICY_PRIORITY = 100;

export interface PricingPolicyListParams {
  page: number;
  size: number;
}

/** Go API / pricing_policy DDL wire contract. */
export interface PricingPolicyWireDTO {
  id: string | number;
  code: string;
  name: string;
  scope_type: PricingPolicyWireScope;
  scope_id: string | number | null;
  level_code: string | null;
  price_method: PricingPolicyWireMethod;
  param_value: string;
  priority: number;
  status: PricingPolicyStatus;
}

export type PricingPolicyUpsertWireDTO = Omit<PricingPolicyWireDTO, "id" | "scope_id"> & {
  scope_id: number | null;
};

/** Page-facing model. Unsupported backend methods remain visible but read-only. */
export interface PricingPolicySummaryDTO {
  id: string;
  code: string;
  name: string;
  strategyType: PricingStrategyType | "COST_UP" | "FIXED";
  scopeType: PricingPolicyWireScope;
  scopeId: string | null;
  levelCode: string | null;
  paramValue: string;
  targetMarginRate?: string;
  officialMultiplier?: string;
  priority: number;
  status: PricingPolicyStatus;
  canEdit: boolean;
}

/** Editable page draft, deliberately separate from the Go wire DTO. */
export interface PricingPolicyDraft {
  code: string;
  name: string;
  strategyType: PricingStrategyType;
  levelCode: string;
  targetMarginRate?: string;
  officialMultiplier?: string;
  priority: number;
}

export interface PricingPolicySaveResultDTO {
  id: string;
  status: PricingPolicyStatus;
}
