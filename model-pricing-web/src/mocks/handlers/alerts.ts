import { http, HttpResponse } from "msw";
import type { AlertDetailDTO, AlertSummaryDTO } from "../../api/alerts.types";
import { ApiError } from "../../domain/common";

const basePath = "/api/internal/alerts";
const handleIdentities = new Set(["MODEL_OPS", "PURCHASING", "PRICING_OP"]);
const readableIdentities = new Set([...handleIdentities, "VIEWER"]);
const keys = new Map<string, unknown>();

const alerts: AlertDetailDTO[] = [
  ["alert-01", "供应商报价毛利倒挂", "供应商报价", "CRITICAL", "OPEN", "QUOTE", "SQ-202609-005", "2026-09-10T13:12:00+08:00", "采纳本次报价后，GPT 4.1 预计毛利为 -3.20%。", ["报价输出价格 9.60000000 USD", "预演版本 2026-09-10 13:10"], "核对报价口径，必要时驳回供应商报价。"],
  ["alert-02", "官方价格基准发生变化", "官方价格", "HIGH", "OPEN", "MODEL", "gpt-4.1", "2026-09-10T11:40:00+08:00", "报价审批期间官方基准发生变化，原差异材料可能过期。", ["旧基准 2.00000000 USD", "新采集值 2.20000000 USD"], "刷新差异材料后再进行审批。"],
  ["alert-03", "供应商资质将在 15 天内到期", "供应商", "MEDIUM", "ACKNOWLEDGED", "SUPPLIER", "supplier-star", "2026-09-09T08:00:00+08:00", "星河算力的服务资质即将到期。", ["资质有效期至 2026-09-25"], "联系供应商补充最新资质文件。"],
  ["alert-04", "价目表存在待生效版本", "价目表", "LOW", "OPEN", "PRICE_BOOK", "PB-202609-03", "2026-09-09T16:30:00+08:00", "价目表已批准但尚未到生效时间。", ["计划生效 2026-09-15 00:00"], "确认发布任务状态，无需前端手工改状态。"],
  ["alert-05", "成本重算任务已恢复", "成本", "LOW", "RESOLVED", "TASK", "cost-recalc-0910", "2026-09-09T07:10:00+08:00", "成本重算曾短暂失败，重试后已完成。", ["重试次数 1", "完成时间 2026-09-09 07:18"], "无需继续处理。"],
  ["alert-06", "报价即将在 7 天内到期", "供应商报价", "MEDIUM", "OPEN", "QUOTE", "SQ-202608-018", "2026-09-10T07:00:00+08:00", "当前生效报价即将结束。", ["结束时间 2026-09-17 23:59"], "联系供应商续报或确认替代报价。"],
].map(([id, title, module, level, status, entityType, entityId, triggeredAt, description, evidence, suggestedAction]) => ({
  id: id as string,
  title: title as string,
  module: module as string,
  level: level as AlertDetailDTO["level"],
  status: status as AlertDetailDTO["status"],
  entityType: entityType as string,
  entityId: entityId as string,
  triggeredAt: triggeredAt as string,
  ownerName: "业务运营组",
  description: description as string,
  evidence: evidence as string[],
  suggestedAction: suggestedAction as string,
  history: [],
  canHandle: false,
}));

const copy = <T>(value: T): T => structuredClone(value);
const requestId = () => `mock-${crypto.randomUUID()}`;
const ok = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: requestId() });
function fail(error: unknown) {
  const candidate = error as { httpStatus?: number; message?: string };
  const status = candidate.httpStatus || 500;
  return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: requestId() }, { status });
}
function summary(alert: AlertDetailDTO): AlertSummaryDTO {
  const { id, title, module, level, status, entityType, entityId, triggeredAt, ownerName } = alert;
  return { id, title, module, level, status, entityType, entityId, triggeredAt, ownerName };
}

export const alertHandlers = [
  http.get(basePath, ({ request }) => {
    try {
      const identity = request.headers.get("X-Mock-Identity") || "VIEWER";
      if (!readableIdentities.has(identity)) throw new ApiError(403, "当前身份无权查看业务告警");
      const url = new URL(request.url);
      const search = (url.searchParams.get("search") || "").toLowerCase();
      const module = url.searchParams.get("module") || "";
      const level = url.searchParams.get("level") || "";
      const status = url.searchParams.get("status") || "";
      const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
      const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const matched = alerts.filter((alert) =>
        (!search || `${alert.title} ${alert.entityId}`.toLowerCase().includes(search)) &&
        (!module || alert.module === module) && (!level || alert.level === level) && (!status || alert.status === status));
      const offset = (page - 1) * size;
      return ok({ list: matched.slice(offset, offset + size).map(summary), total: matched.length, page, size });
    } catch (error) { return fail(error); }
  }),
  http.all(`${basePath}/*`, async ({ request }) => {
    try {
      const identity = request.headers.get("X-Mock-Identity") || "VIEWER";
      if (!readableIdentities.has(identity)) throw new ApiError(403, "当前身份无权查看业务告警");
      const url = new URL(request.url);
      const [id, action] = url.pathname.slice(basePath.length + 1).split("/");
      const alert = alerts.find((item) => item.id === id);
      if (!alert) throw new ApiError(404, "告警不存在或当前账号不可见");
      if (request.method === "GET" && !action) return ok(copy({ ...alert, canHandle: handleIdentities.has(identity) }));
      if (request.method !== "POST" || !["acknowledge", "resolve"].includes(action || "")) throw new ApiError(404, "接口不存在");
      if (!handleIdentities.has(identity)) throw new ApiError(403, "当前身份没有告警处理权限");
      const key = request.headers.get("Idempotency-Key");
      if (!key) throw new ApiError(400, "缺少 Idempotency-Key");
      if (keys.has(key)) return ok(copy(keys.get(key)));
      if (alert.status === "RESOLVED") throw new ApiError(409, "该告警已经解决，请刷新页面");
      const body = (await request.json()) as { reason?: string };
      if (action === "resolve" && !body.reason?.trim()) throw new ApiError(400, "请填写处理结论");
      alert.status = action === "resolve" ? "RESOLVED" : "HANDLING";
      alert.history.push({ at: new Date().toISOString(), actor: "当前开发身份", action: action === "resolve" ? "解决告警" : "确认告警", note: body.reason?.trim() });
      const value = { id: alert.id, status: alert.status };
      keys.set(key, value);
      return ok(copy(value));
    } catch (error) { return fail(error); }
  }),
];
