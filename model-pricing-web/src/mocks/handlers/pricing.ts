import { http, HttpResponse } from "msw";
import type {
  GeneratePriceBookWireDTO,
  GeneratedPriceBookWireDTO,
  PublishPriceBookWireDTO,
  PublishedPriceBookWireDTO,
} from "../../api/pricing.types";
import { ApiError } from "../../domain/common";

const basePath = "/api/internal/price-books";
const keys = new Map<string, { signature: string; value: unknown }>();
const drafts = new Map<string, GeneratedPriceBookWireDTO>();
let draftSequence = 100;
let requestSequence = 500;
const requestId = () => `mock-${crypto.randomUUID()}`;
const copy = <T>(value: T): T => structuredClone(value);
const ok = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: requestId() });
function fail(error: unknown) { const candidate = error as { httpStatus?: number; message?: string }; const status = candidate.httpStatus || 500; return HttpResponse.json({ code: status === 409 ? 10005 : status === 400 ? 10001 : `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: requestId() }, { status }); }
function authorizeWrite(request: Request) { if ((request.headers.get("X-Mock-Identity") || "VIEWER") !== "PRICING_OP") throw new ApiError(403, "当前身份没有价目表维护权限"); }
function positiveIds(value: unknown) { return Array.isArray(value) && value.every((id) => Number.isSafeInteger(id) && Number(id) > 0); }

async function bodyWithKey<T>(request: Request) {
  const key = request.headers.get("Idempotency-Key");
  if (!key) throw new ApiError(400, "缺少 Idempotency-Key");
  return { key, body: await request.json() as T };
}

function replay(key: string, signature: string) {
  const cached = keys.get(key);
  if (!cached) return;
  if (cached.signature !== signature) throw new ApiError(409, "幂等键已用于其他操作");
  return ok(copy(cached.value));
}

export const pricingHandlers = [
  http.post(basePath, async ({ request }) => {
    try {
      authorizeWrite(request);
      const { key, body } = await bodyWithKey<GeneratePriceBookWireDTO>(request);
      const signature = JSON.stringify(["generate", body]);
      const cached = replay(key, signature); if (cached) return cached;
      if (!body.level_code?.trim() || !body.currency?.trim()) throw new ApiError(400, "level_code 和 currency 必填");
      if (body.policy_ids !== undefined && !positiveIds(body.policy_ids)) throw new ApiError(400, "policy_ids 必须是正整数数组");
      if (body.sku_ids !== undefined && !positiveIds(body.sku_ids)) throw new ApiError(400, "sku_ids 必须是正整数数组");
      draftSequence += 1;
      const skuIds = body.sku_ids?.length ? body.sku_ids : [40, 41];
      const diffReport = skuIds.map((skuId, index) => ({
        sku_id: skuId, sku_code: `SKU-${skuId}`, old_price: index ? "10.00000000" : null,
        new_price: index ? "11.20000000" : "3.20000000", delta_pct: index ? "12.0000" : null,
        floor_price: index ? "9.80000000" : "2.80000000", floor_violation: false,
      }));
      const value: GeneratedPriceBookWireDTO = { draft_id: draftSequence, level_code: body.level_code.trim(), currency: body.currency, item_count: diffReport.length, diff_report: diffReport, blocked_count: 0 };
      drafts.set(String(value.draft_id), value);
      keys.set(key, { signature, value });
      return ok(copy(value));
    } catch (error) { return fail(error); }
  }),
  http.post(`${basePath}/:id/publish`, async ({ request, params }) => {
    try {
      authorizeWrite(request);
      const id = String(params.id);
      const { key, body } = await bodyWithKey<PublishPriceBookWireDTO>(request);
      const signature = JSON.stringify(["publish", id, body]);
      const cached = replay(key, signature); if (cached) return cached;
      if (!drafts.has(id)) throw new ApiError(404, "价目表草稿不存在");
      if (body.mode !== "IMMEDIATE") throw new ApiError(400, "本批只支持 IMMEDIATE");
      const effectiveTime = Date.parse(body.effective_time);
      if (!Number.isFinite(effectiveTime) || effectiveTime > Date.now()) throw new ApiError(400, "IMMEDIATE 的 effective_time 必须是当前或过去时间");
      requestSequence += 1;
      const value: PublishedPriceBookWireDTO = { price_book_id: id, version_no: 1, change_request_id: requestSequence, step_count: 2, effective_time: body.effective_time, status: "APPROVING" };
      drafts.delete(id);
      keys.set(key, { signature, value });
      return ok(copy(value));
    } catch (error) { return fail(error); }
  }),
];
