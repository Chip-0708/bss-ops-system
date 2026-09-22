import Decimal from "decimal.js";
import { http, HttpResponse } from "msw";
import type {
  PricingPolicyUpsertWireDTO,
  PricingPolicyWireDTO,
} from "../../api/pricingPolicies.types";
import { ApiError } from "../../domain/common";

const basePath = "/api/internal/pricing/policies";
const readableIdentities = new Set(["PRICING_OP", "VIEWER"]);
const keys = new Map<string, { signature: string; value: PricingPolicyWireDTO }>();
let sequence = 4;
const requestId = () => `mock-${crypto.randomUUID()}`;
const copy = <T>(value: T): T => structuredClone(value);
const ok = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: requestId() });
function fail(error: unknown) {
  const candidate = error as { httpStatus?: number; message?: string };
  const status = candidate.httpStatus || 500;
  return HttpResponse.json({
    code: status === 409 ? "STATE_CONFLICT" : `HTTP_${status}`,
    message: candidate.message || "Mock 服务异常",
    requestId: requestId(),
  }, { status });
}
function identity(request: Request) { return request.headers.get("X-Mock-Identity") || "VIEWER"; }
function read(request: Request) {
  if (!readableIdentities.has(identity(request))) throw new ApiError(403, "当前身份无权查看定价策略");
}
function write(request: Request) {
  if (identity(request) !== "PRICING_OP") throw new ApiError(403, "当前身份没有定价策略维护权限");
}

// Keep mock storage identical to the current Go snake_case wire contract.
const policies: PricingPolicyWireDTO[] = [
  { id: 1, code: "PP-GOLD-004", name: "金牌客户目标毛利策略", scope_type: "ALL", scope_id: null, level_code: "GOLD", price_method: "MARGIN", param_value: "0.225000", priority: 100, status: "DRAFT" },
  { id: 2, code: "PP-STANDARD-008", name: "标准客户目标毛利策略", scope_type: "ALL", scope_id: null, level_code: "STANDARD", price_method: "MARGIN", param_value: "0.280000", priority: 100, status: "ACTIVE" },
  { id: 3, code: "PP-OFFICIAL-003", name: "官方价锚定策略", scope_type: "ALL", scope_id: null, level_code: "ECONOMY", price_method: "OFFICIAL_ANCHOR", param_value: "0.920000", priority: 100, status: "ACTIVE" },
  { id: 4, code: "PP-LEGACY-002", name: "历史长尾模型策略", scope_type: "ALL", scope_id: null, level_code: "STANDARD", price_method: "FIXED", param_value: "1.000000", priority: 200, status: "ARCHIVED" },
];

function validate(wire: PricingPolicyUpsertWireDTO) {
  if (!wire.code?.trim() || !wire.name?.trim() || !wire.scope_type || !wire.price_method || !wire.param_value || !wire.status) {
    throw new ApiError(400, "缺少定价策略必填字段");
  }
  if (wire.scope_type !== "ALL" || wire.scope_id !== null) throw new ApiError(400, "当前页面仅支持 ALL 范围");
  if (!wire.level_code) throw new ApiError(400, "当前页面必须选择一个客户等级");
  if (!(["DRAFT", "ACTIVE", "ARCHIVED"] as const).includes(wire.status)) {
    throw new ApiError(400, "status 必须是 DRAFT、ACTIVE 或 ARCHIVED");
  }
  if (!Number.isInteger(wire.priority)) throw new ApiError(400, "priority 必须为整数");
  try {
    const value = new Decimal(wire.param_value);
    if (!value.isFinite() || !value.isPositive() || (wire.price_method === "MARGIN" && value.greaterThanOrEqualTo(1))) throw new Error();
  } catch {
    throw new ApiError(400, "param_value 不符合当前定价方式约束");
  }
}

async function requestData(request: Request, signaturePrefix: string) {
  const key = request.headers.get("Idempotency-Key");
  if (!key) throw new ApiError(400, "缺少 Idempotency-Key");
  const wire = (await request.json()) as PricingPolicyUpsertWireDTO;
  const signature = JSON.stringify([signaturePrefix, wire]);
  const cached = keys.get(key);
  if (cached && cached.signature !== signature) throw new ApiError(409, "幂等键已用于其他操作");
  return { key, wire, signature, cached };
}

export const pricingPolicyHandlers = [
  http.get(basePath, ({ request }) => {
    try {
      read(request);
      const url = new URL(request.url);
      const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
      const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const offset = (page - 1) * size;
      return ok({ list: copy(policies.slice(offset, offset + size)), total: policies.length, page, size });
    } catch (error) { return fail(error); }
  }),
  http.post(basePath, async ({ request }) => {
    try {
      read(request);
      write(request);
      const { key, wire, signature, cached } = await requestData(request, "create");
      if (cached) return ok(copy(cached.value));
      validate(wire);
      if (wire.status !== "DRAFT") throw new ApiError(400, "新建策略状态必须为 DRAFT");
      sequence += 1;
      const value: PricingPolicyWireDTO = { id: sequence, ...wire, code: wire.code.trim(), name: wire.name.trim() };
      policies.unshift(value);
      keys.set(key, { signature, value });
      return ok(copy(value));
    } catch (error) { return fail(error); }
  }),
  http.put(`${basePath}/:id`, async ({ request, params }) => {
    try {
      read(request);
      write(request);
      const item = policies.find((policy) => String(policy.id) === String(params.id));
      if (!item) throw new ApiError(404, "定价策略不存在或当前账号不可见");
      const { key, wire, signature, cached } = await requestData(request, `update:${params.id}`);
      if (cached) return ok(copy(cached.value));
      validate(wire);
      Object.assign(item, wire, { id: item.id, code: wire.code.trim(), name: wire.name.trim() });
      keys.set(key, { signature, value: copy(item) });
      return ok(copy(item));
    } catch (error) { return fail(error); }
  }),
];
