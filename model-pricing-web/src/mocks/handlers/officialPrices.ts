import Decimal from "decimal.js";
import { http, HttpResponse } from "msw";
import type {
  CreateStagingRequest,
  CreateSyncJobRequest,
  ConfirmStagingRequest,
  StagingPriceDTO,
  SyncJobDTO,
} from "../../api/officialPrices.types";
import { ApiError } from "../../domain/common";

const editableIdentities = new Set(["MODEL_OPS"]);
const readableIdentities = new Set(["MODEL_OPS", "PRICING_OP", "VIEWER"]);
const requestId = () => `mock-${crypto.randomUUID()}`;
const copy = <T>(value: T): T => structuredClone(value);
const ok = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: requestId() });
function fail(error: unknown) { const candidate = error as { httpStatus?: number; message?: string }; const status = candidate.httpStatus || 500; return HttpResponse.json({ code: status === 409 ? "STATE_CONFLICT" : `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: requestId() }, { status }); }
const identity = (request: Request) => request.headers.get("X-Mock-Identity") || "VIEWER";
function ensureReadable(request: Request) { if (!readableIdentities.has(identity(request))) throw new ApiError(403, "当前身份无权查看官方价格"); }
function ensureEditable(request: Request) { if (!editableIdentities.has(identity(request))) throw new ApiError(403, "仅模型运营可维护官方价格采集"); }

// 当前官方价（用于实时 diff 比对）：sku_id → currency → component → price。
const currentPrices: Record<number, { currency: string; components: Record<string, string> }> = {
  40: { currency: "USD", components: { input: "2.50000000", output: "8.00000000" } },
  41: { currency: "USD", components: { input: "3.00000000", output: "15.00000000" } },
};
const skuIdsByCode: Record<string, number> = {
  "gpt-4.1": 40,
};

let jobSeq = 2;
const jobs: SyncJobDTO[] = [
  { id: 1, job_type: "SYNC_PRICES", source: "manual", status: "SUCCESS", started_at: "2026-09-11T04:00:00+08:00", finished_at: "2026-09-11T04:00:00+08:00", error_msg: null, item_count: 2 },
];
let stagingSeq = 0;
const staging: Array<StagingPriceDTO & { _syncJobId: number }> = [];
const idempotency = new Map<string, { signature: string; value: unknown }>();

function withIdempotency(request: Request, signatureParts: unknown[], action: () => unknown) {
  const key = request.headers.get("Idempotency-Key"); if (!key) throw new ApiError(400, "缺少 Idempotency-Key");
  const signature = JSON.stringify(signatureParts); const cached = idempotency.get(key);
  if (cached) { if (cached.signature !== signature) throw new ApiError(409, "幂等键已用于其他操作"); return cached.value; }
  const value = action(); idempotency.set(key, { signature, value }); return value;
}

// 与后端 ComputeDiff 同口径：cur 缺失→NEW；否则逐组件数值比较，有差异→CHANGED，否则 UNCHANGED。
function computeDiff(payload: Record<string, string>, skuId: number | null, currency: string): Pick<StagingPriceDTO, "diff_status" | "diff_detail"> {
  if (skuId === null) return { diff_status: "UNMATCHED", diff_detail: [] };
  const cur = currentPrices[skuId];
  const cts = Object.keys(payload).sort();
  if (!cur || cur.currency.toUpperCase() !== currency.toUpperCase()) {
    return { diff_status: "NEW", diff_detail: cts.map((ct) => ({ component_type: ct as never, old_price: null, new_price: payload[ct], delta_pct: null })) };
  }
  const detail = [];
  for (const ct of cts) {
    const oldRaw = cur.components[ct]; if (oldRaw === undefined) continue;
    const oldDec = new Decimal(oldRaw); const newDec = new Decimal(payload[ct]);
    if (newDec.equals(oldDec)) continue;
    detail.push({ component_type: ct as never, old_price: oldRaw, new_price: payload[ct], delta_pct: oldDec.isZero() ? null : newDec.minus(oldDec).div(oldDec).toFixed(6) });
  }
  return detail.length ? { diff_status: "CHANGED", diff_detail: detail } : { diff_status: "UNCHANGED", diff_detail: [] };
}

const paginate = <T>(request: Request, rows: T[]) => { const url = new URL(request.url); const page = Math.max(1, Number(url.searchParams.get("page")) || 1); const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10)); const offset = (page - 1) * size; return { list: copy(rows.slice(offset, offset + size)), total: rows.length, page, size }; };
const publicStaging = ({ _syncJobId: _drop, ...row }: StagingPriceDTO & { _syncJobId: number }): StagingPriceDTO => row;

export const officialPriceHandlers = [
  http.get("/api/internal/price-sync/jobs", ({ request }) => {
    try { ensureReadable(request); return ok(paginate(request, [...jobs].sort((a, b) => Number(b.id) - Number(a.id)))); } catch (error) { return fail(error); }
  }),
  http.post("/api/internal/price-sync/jobs", async ({ request }) => {
    try {
      ensureEditable(request);
      const body = await request.json() as CreateSyncJobRequest;
      if (!["SYNC_MODELS", "SYNC_PRICES", "SYNC_COMMUNITY"].includes(body.job_type)) throw new ApiError(400, "job_type 非法");
      const source = (body.source || "").trim(); if (!source || source.length > 32) throw new ApiError(400, "source 必填且不超过 32 字符");
      for (const id of body.sku_ids ?? []) if (!Number.isSafeInteger(id) || id <= 0) throw new ApiError(400, "sku_ids 含非正整数");
      const now = new Date().toISOString();
      const job: SyncJobDTO = { id: ++jobSeq, job_type: body.job_type, source, status: "SUCCESS", started_at: now, finished_at: now, error_msg: null, item_count: (body.sku_ids ?? []).length };
      jobs.push(job);
      return ok(copy(job));
    } catch (error) { return fail(error); }
  }),
  http.get("/api/internal/staging-prices", ({ request }) => {
    try {
      ensureReadable(request);
      const jobFilter = Number(new URL(request.url).searchParams.get("sync_job_id")) || undefined;
      const rows = staging.filter((row) => !jobFilter || row._syncJobId === jobFilter).map(publicStaging).sort((a, b) => Number(a.id) - Number(b.id));
      return ok(paginate(request, rows));
    } catch (error) { return fail(error); }
  }),
  http.post("/api/internal/staging-prices", async ({ request }) => {
    try {
      ensureEditable(request);
      const body = await request.json() as CreateStagingRequest;
      return ok(withIdempotency(request, ["staging", body], () => {
        const job = jobs.find((item) => Number(item.id) === body.sync_job_id);
        if (!job) throw new ApiError(404, "采集批次不存在");
        if (!body.items?.length) throw new ApiError(400, "items 必填且不能为空");
        const ids: number[] = [];
        for (const item of body.items) {
          const currency = (item.currency || "").trim().toUpperCase();
          if (currency.length !== 3) throw new ApiError(400, "currency 必须是 3 位币种码");
          const payload = item.payload as Record<string, string>;
          if (!payload || !Object.keys(payload).length) throw new ApiError(400, "payload 必填且不能为空");
          for (const [ct, v] of Object.entries(payload)) { if (typeof v !== "string" || !/^\d+(\.\d{1,8})?$/.test(v.trim())) throw new ApiError(400, `组件 ${ct} 价格必须是非负十进制字符串`); }
          const rawSkuCode = item.raw_sku_code?.trim().toLowerCase();
          const skuId = item.sku_id ?? (rawSkuCode ? skuIdsByCode[rawSkuCode] ?? null : null);
          const id = ++stagingSeq;
          ids.push(id);
          staging.push({ id, _syncJobId: body.sync_job_id, sync_job_id: body.sync_job_id, sku_id: skuId, raw_sku_code: item.raw_sku_code ?? null, currency, payload: copy(payload), match_status: skuId === null ? "UNMATCHED" : "MATCHED", processed: false, created_at: new Date().toISOString(), ...computeDiff(payload, skuId, currency) });
        }
        return { created_count: ids.length, staging_ids: ids };
      }));
    } catch (error) { return fail(error); }
  }),
  http.post("/api/internal/staging-prices/confirm", async ({ request }) => {
    try {
      ensureEditable(request);
      const body = await request.json() as ConfirmStagingRequest;
      return ok(withIdempotency(request, ["confirm", body], () => {
        if (!body.staging_ids?.length) throw new ApiError(400, "staging_ids 必填且不能为空");
        if (Date.parse(body.effective_time) > Date.now()) throw new ApiError(400, "effective_time 不允许未来时点");
        const rows = body.staging_ids.map((id) => { const row = staging.find((item) => Number(item.id) === id); if (!row) throw new ApiError(404, `暂存行不存在：${id}`); return row; });
        for (const row of rows) {
          if (row.processed) throw new ApiError(409, `暂存行已确认：${row.id}`);
          if (row._syncJobId !== body.sync_job_id) throw new ApiError(400, `暂存行不属于该采集批次：${row.id}`);
          if (row.sku_id === null) throw new ApiError(400, `暂存行未匹配 SKU：${row.id}`);
        }
        const isUp = rows.some((row) => row.diff_status === "NEW" || row.diff_detail.some((detail) => detail.delta_pct !== null && new Decimal(detail.delta_pct).greaterThan(0)));
        rows.forEach((row) => {
          row.processed = true;
          if (row.sku_id === null) return;
          const skuId = Number(row.sku_id);
          const current = currentPrices[skuId] ?? { currency: row.currency, components: {} };
          currentPrices[skuId] = { currency: row.currency, components: { ...current.components, ...row.payload } };
        });
        return { change_request_id: Math.floor(Math.random() * 9000) + 1000, step_count: isUp ? 2 : 1 };
      }));
    } catch (error) { return fail(error); }
  }),
];
