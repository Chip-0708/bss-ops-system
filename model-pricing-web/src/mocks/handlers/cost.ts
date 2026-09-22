import { http, HttpResponse } from "msw";
import Decimal from "decimal.js";
import type { CurrentCostBaselineDTO } from "../../api/cost.types";
import type { CostParamOverrideDTO, CostParamsViewDTO } from "../../api/costParams.types";
import { ApiError } from "../../domain/common";

const readableIdentities = new Set(["PURCHASING", "PRICING_OP", "VIEWER"]);
const requestId = () => `mock-${crypto.randomUUID()}`;
const copy = <T>(value: T): T => structuredClone(value);
const ok = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: requestId() });
function fail(error: unknown) { const candidate = error as { httpStatus?: number; message?: string }; const status = candidate.httpStatus || 500; return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: requestId() }, { status }); }
function authorize(request: Request) { const identity = request.headers.get("X-Mock-Identity") || "VIEWER"; if (!readableIdentities.has(identity)) throw new ApiError(403, "当前身份无权查看成本数据"); }

const baselines: CurrentCostBaselineDTO[] = [
  { sku_id: 40, sku_code: "gpt-4.1", version: 8, valid_from: "2026-09-11T00:00:00+08:00", currency: "USD", primary_supplier_id: 9001, primary_supplier_name: "云桥科技", unit_cost: "1.99800000", unit_cost_basis: "input", supplier_count: 3, single_point: false, floor_price: "2.35058824" },
  { sku_id: 41, sku_code: "claude-sonnet-4", version: 4, valid_from: "2026-09-11T00:00:00+08:00", currency: "USD", primary_supplier_id: 9002, primary_supplier_name: "星河算力", unit_cost: "3.10800000", unit_cost_basis: "input", supplier_count: 1, single_point: true, floor_price: "3.65647059" },
  { sku_id: 42, sku_code: "deepseek-v3", version: 6, valid_from: "2026-09-11T00:00:00+08:00", currency: "CNY", primary_supplier_id: 9003, primary_supplier_name: "北辰智能", unit_cost: "1.33200000", unit_cost_basis: "input", supplier_count: 2, single_point: false, floor_price: "1.56705882" },
];

// defaults(GLOBAL) 只读 + overrides(MODEL/SUPPLIER)。比率为 numeric(8,4) 原样字符串。
const defaults = { loss_rate: "0.0300", channel_rate: "0.0100", tax_inclusive: false, withholding_tax: "0.0000" };
let overrides: CostParamOverrideDTO[] = [
  { scope_type: "MODEL", scope_id: 41, loss_rate: "0.0500", channel_rate: "0.0100", tax_inclusive: false, withholding_tax: "0.0000" },
];
const paramKeys = new Map<string, { signature: string; value: unknown }>();
const rateOk = (v: unknown) => typeof v === "string" && /^\d+(\.\d{1,4})?$/.test(v.trim()) && new Decimal(v).greaterThanOrEqualTo(0) && new Decimal(v).lessThanOrEqualTo(1);

export const costHandlers = [
  http.get("/api/internal/cost/baselines/:sku/compare", ({ request, params }) => {
    try {
      authorize(request);
      const baseline = baselines.find((row) => String(row.sku_id) === String(params.sku));
      if (!baseline) throw new ApiError(404, "成本基线不存在");
      return ok({
        suppliers: [{
          supplier_id: baseline.primary_supplier_id,
          supplier_name: baseline.primary_supplier_name,
          currency: baseline.currency,
          unit_cost: baseline.unit_cost,
          components: [],
          constraints: [],
          scores: { price: "1.000000", stability: "0.900000", quota: "0.800000", compatibility: "1.000000", total: "0.925000" },
          is_primary: true,
          is_backup: false,
        }],
        range: { cost_min: baseline.unit_cost, cost_max: baseline.unit_cost, cost_weighted: baseline.unit_cost },
        trend: [{ date: baseline.valid_from.slice(0, 10), unit_cost: baseline.unit_cost, version: baseline.version }],
        market_best: baseline.unit_cost,
      });
    } catch (error) { return fail(error); }
  }),
  http.get("/api/internal/cost/baselines/:sku/history", ({ request, params }) => {
    try {
      authorize(request);
      const baseline = baselines.find((row) => String(row.sku_id) === String(params.sku));
      if (!baseline) throw new ApiError(404, "成本基线不存在");
      const asOf = new URL(request.url).searchParams.get("asOf");
      const historical = { version: 7, valid_from: "2026-08-01T00:00:00+08:00", valid_to: baseline.valid_from, change_reason: "QUOTE_EFFECTIVE", primary_supplier_name: baseline.primary_supplier_name, unit_cost: baseline.unit_cost, calc_snapshot: { formula_version: "mock-v1" } };
      const list = asOf ? [historical] : [{ ...historical, version: baseline.version, valid_from: baseline.valid_from, valid_to: null }, historical];
      return ok({ list: copy(list) });
    } catch (error) { return fail(error); }
  }),
  http.get("/api/internal/cost/baselines", ({ request }) => {
    try {
      authorize(request);
      const url = new URL(request.url);
      const keyword = (url.searchParams.get("keyword") || "").toLowerCase();
      const onlySingle = url.searchParams.get("only_single_point") === "true";
      const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
      const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 20));
      const matched = baselines.filter((row) => (!keyword || row.sku_code.toLowerCase().includes(keyword)) && (!onlySingle || row.single_point));
      const offset = (page - 1) * size;
      return ok({ list: copy(matched.slice(offset, offset + size)), total: matched.length, page, size });
    } catch (error) { return fail(error); }
  }),
  http.get("/api/internal/cost/params", ({ request }) => {
    try {
      authorize(request);
      const view: CostParamsViewDTO = { defaults: copy(defaults), overrides: copy(overrides) };
      return ok(view);
    } catch (error) { return fail(error); }
  }),
  http.put("/api/internal/cost/params", async ({ request }) => {
    try {
      authorize(request);
      if (request.headers.get("X-Mock-Identity") !== "PRICING_OP") throw new ApiError(403, "仅定价运营可维护成本参数");
      const key = request.headers.get("Idempotency-Key"); if (!key) throw new ApiError(400, "缺少 Idempotency-Key");
      const body = await request.json() as { defaults?: unknown; overrides?: CostParamOverrideDTO[] };
      const signature = JSON.stringify(body);
      const cached = paramKeys.get(key);
      if (cached) { if (cached.signature !== signature) throw new ApiError(409, "幂等键已用于其他操作"); return ok(copy(cached.value)); }
      if (body.defaults != null) throw new ApiError(400, "defaults 为只读，不允许提交");
      if (!Array.isArray(body.overrides)) throw new ApiError(400, "overrides 必填（可为空数组）");
      const seen = new Set<string>();
      for (const item of body.overrides) {
        if (item.scope_type !== "MODEL" && item.scope_type !== "SUPPLIER") throw new ApiError(400, "scope_type 必须是 MODEL 或 SUPPLIER");
        if (!Number.isSafeInteger(Number(item.scope_id)) || Number(item.scope_id) <= 0) throw new ApiError(400, "scope_id 必须是正整数");
        const dupKey = `${item.scope_type}:${item.scope_id}`;
        if (seen.has(dupKey)) throw new ApiError(400, `${dupKey} 重复`);
        seen.add(dupKey);
        if (!rateOk(item.loss_rate) || !rateOk(item.channel_rate)) throw new ApiError(400, "费率必须是 0~1、最多 4 位小数");
      }
      overrides = body.overrides.map((item) => ({ ...item, scope_id: Number(item.scope_id) }));
      const value = { overrides_count: overrides.length, submitted_tasks: overrides.length, task_ids: overrides.map((_, i) => i + 1), audit_log_id: crypto.randomUUID() };
      paramKeys.set(key, { signature, value: copy(value) });
      return ok(copy(value));
    } catch (error) { return fail(error); }
  }),
];
