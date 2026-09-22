import Decimal from "decimal.js";
import type { PageResult } from "../domain/common";
import { assertContractId } from "../domain/contractId";
import { apiRequest } from "./http";
import type {
  PricingPolicyDraft,
  PricingPolicyListParams,
  PricingPolicySaveResultDTO,
  PricingPolicySummaryDTO,
  PricingPolicyUpsertWireDTO,
  PricingPolicyWireDTO,
} from "./pricingPolicies.types";
import type { PricingPolicyStatus } from "../domain/status";

const basePath = "/internal/pricing/policies";

export function toPricingPolicyWire(draft: PricingPolicyDraft): PricingPolicyUpsertWireDTO {
  const paramValue = draft.strategyType === "TARGET_MARGIN"
    ? new Decimal(draft.targetMarginRate ?? "").div(100).toFixed(6)
    : new Decimal(draft.officialMultiplier ?? "").toFixed(6);

  return {
    code: draft.code.trim(),
    name: draft.name.trim(),
    scope_type: "ALL",
    scope_id: null,
    level_code: draft.levelCode,
    price_method: draft.strategyType === "TARGET_MARGIN" ? "MARGIN" : "OFFICIAL_ANCHOR",
    param_value: paramValue,
    priority: draft.priority,
    status: "DRAFT",
  };
}

export function fromPricingPolicyWire(wire: PricingPolicyWireDTO): PricingPolicySummaryDTO {
  assertContractId(wire.id);
  const strategyType = wire.price_method === "MARGIN" ? "TARGET_MARGIN" : wire.price_method;
  return {
    id: String(wire.id),
    code: wire.code,
    name: wire.name,
    strategyType,
    scopeType: wire.scope_type,
    scopeId: wire.scope_id === null ? null : String(wire.scope_id),
    levelCode: wire.level_code,
    paramValue: wire.param_value,
    targetMarginRate: wire.price_method === "MARGIN"
      ? new Decimal(wire.param_value).times(100).toFixed(2)
      : undefined,
    officialMultiplier: wire.price_method === "OFFICIAL_ANCHOR" ? wire.param_value : undefined,
    priority: wire.priority,
    status: wire.status,
    canEdit: wire.status === "DRAFT"
      && wire.scope_type === "ALL"
      && wire.scope_id === null
      && wire.level_code !== null
      && (wire.price_method === "MARGIN" || wire.price_method === "OFFICIAL_ANCHOR"),
  };
}

export function toPricingPolicyStatusWire(
  policy: PricingPolicySummaryDTO,
  status: Extract<PricingPolicyStatus, "ACTIVE" | "ARCHIVED">,
): PricingPolicyUpsertWireDTO {
  const allowed = (policy.status === "DRAFT" && status === "ACTIVE")
    || (policy.status === "ACTIVE" && status === "ARCHIVED");
  if (!allowed) throw new Error(`不支持定价策略从 ${policy.status} 变更为 ${status}`);
  const scopeId = policy.scopeId === null ? null : Number(policy.scopeId);
  if (scopeId !== null && !Number.isSafeInteger(scopeId)) throw new Error("策略 scope_id 非法");
  return {
    code: policy.code,
    name: policy.name,
    scope_type: policy.scopeType,
    scope_id: scopeId,
    level_code: policy.levelCode,
    price_method: policy.strategyType === "TARGET_MARGIN" ? "MARGIN" : policy.strategyType,
    param_value: policy.paramValue,
    priority: policy.priority,
    status,
  };
}

export function pricingPolicyStatusIdempotency(
  policy: PricingPolicySummaryDTO,
  status: Extract<PricingPolicyStatus, "ACTIVE" | "ARCHIVED">,
) {
  const wire = toPricingPolicyStatusWire(policy, status);
  return {
    wire,
    options: {
      scope: `pricing-policy:status:${policy.id}:${policy.status}->${status}`,
      payload: wire,
      lifecycle: `pricing-policy:status:${policy.id}`,
    },
  };
}

function toSaveResult(wire: PricingPolicyWireDTO): PricingPolicySaveResultDTO {
  assertContractId(wire.id);
  return { id: String(wire.id), status: wire.status };
}

export const pricingPoliciesApi = {
  list: (params: PricingPolicyListParams) =>
    apiRequest<PageResult<PricingPolicyWireDTO>>({ url: basePath, method: "GET", params })
      .then((data) => ({ ...data, list: data.list.map(fromPricingPolicyWire) })),
  create: (draft: PricingPolicyDraft) => {
    const wire = toPricingPolicyWire(draft);
    return apiRequest<PricingPolicyWireDTO>({
      url: basePath,
      method: "POST",
      data: wire,
      idempotency: { scope: "pricing-policy:create", payload: wire },
    }).then(toSaveResult);
  },
  update: (id: string, draft: PricingPolicyDraft) => {
    const wire = toPricingPolicyWire(draft);
    return apiRequest<PricingPolicyWireDTO>({
      url: `${basePath}/${id}`,
      method: "PUT",
      data: wire,
      idempotency: { scope: `pricing-policy:update:${id}`, payload: wire },
    }).then(toSaveResult);
  },
  updateStatus: (
    policy: PricingPolicySummaryDTO,
    status: Extract<PricingPolicyStatus, "ACTIVE" | "ARCHIVED">,
  ) => {
    const { wire, options } = pricingPolicyStatusIdempotency(policy, status);
    return apiRequest<PricingPolicyWireDTO>({
      url: `${basePath}/${policy.id}`,
      method: "PUT",
      data: wire,
      idempotency: options,
    }).then(toSaveResult);
  },
};
