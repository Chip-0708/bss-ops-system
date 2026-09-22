import { http, HttpResponse } from "msw";
import type { AuditLogDetailDTO, AuditLogSummaryDTO } from "../../api/audit.types";
import { ApiError } from "../../domain/common";

const basePath = "/api/internal/audit-logs";
const readableIdentities = new Set(["MODEL_OPS", "VIEWER"]);
const logs: AuditLogDetailDTO[] = [
  { id: "audit-01", occurredAt: "2026-09-10T13:22:00+08:00", operatorName: "陈昊", operatorType: "USER", action: "通过供应商报价", module: "供应商报价", entityType: "QUOTE", entityId: "SQ-202609-001", result: "SUCCESS", source: "Internal Portal", requestId: "req-quote-1001", reason: "差异及毛利预演已复核", before: { status: "APPROVING" }, after: { status: "APPROVED_PENDING", effectiveFrom: "2026-09-15T00:00:00+08:00" }, impact: ["等待报价激活任务", "成本基线将在生效后重算"] },
  { id: "audit-02", occurredAt: "2026-09-10T12:10:00+08:00", operatorName: "林悦", operatorType: "USER", action: "人工验证模型", module: "模型", entityType: "MODEL", entityId: "gpt-4.1-mini", result: "SUCCESS", source: "Internal Portal", requestId: "req-model-2201", reason: "上下文和接口兼容性验证通过", before: { status: "PENDING_VERIFY" }, after: { status: "PURCHASABLE" }, impact: ["模型进入可采购状态"] },
  { id: "audit-03", occurredAt: "2026-09-10T11:42:00+08:00", operatorName: "官方价格采集任务", operatorType: "SYSTEM", action: "写入价格暂存", module: "官方价格", entityType: "PRICE_STAGING", entityId: "PS-20260910-18", result: "SUCCESS", source: "Scheduler", requestId: "job-price-0910", after: { input: "2.20000000", currency: "USD" }, impact: ["等待模型运营核对"] },
  { id: "audit-04", occurredAt: "2026-09-10T10:35:00+08:00", operatorName: "周婷", operatorType: "USER", action: "提交价目表审批", module: "价目表", entityType: "PRICE_BOOK", entityId: "PB-202609-03", result: "FAILURE", source: "Internal Portal", requestId: "req-book-8831", reason: "部分 SKU 低于后端价格红线", before: { status: "DRAFT" }, after: { status: "DRAFT" }, impact: ["价目表未进入审批"] },
  { id: "audit-05", occurredAt: "2026-09-10T09:50:00+08:00", operatorName: "成本重算任务", operatorType: "SYSTEM", action: "生成成本基线", module: "成本", entityType: "COST_BASELINE", entityId: "CB-gpt-4.1-0910", result: "SUCCESS", source: "Scheduler", requestId: "job-cost-0910", before: { version: 7 }, after: { version: 8 }, impact: ["更新当前成本查询结果"] },
  { id: "audit-06", occurredAt: "2026-09-09T16:28:00+08:00", operatorName: "林悦", operatorType: "USER", action: "发起模型退役", module: "模型", entityType: "MODEL_RETIREMENT", entityId: "MR-202609-02", result: "SUCCESS", source: "Internal Portal", requestId: "req-retire-0912", reason: "上游厂商停止维护", before: { status: "PUBLISHED" }, after: { retirementStatus: "APPROVING" }, impact: ["关联价目表 2 个", "客户合同 1 个"] },
];

const copy = <T>(value: T): T => structuredClone(value);
const requestId = () => `mock-${crypto.randomUUID()}`;
const ok = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: requestId() });
function fail(error: unknown) { const candidate = error as { httpStatus?: number; message?: string }; const status = candidate.httpStatus || 500; return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: requestId() }, { status }); }
function summary(log: AuditLogDetailDTO): AuditLogSummaryDTO { const { id, occurredAt, operatorName, operatorType, action, module, entityType, entityId, result, source, requestId: traceId } = log; return { id, occurredAt, operatorName, operatorType, action, module, entityType, entityId, result, source, requestId: traceId }; }
function matched(request: Request) {
  const url = new URL(request.url);
  const search = (url.searchParams.get("search") || "").toLowerCase();
  const module = url.searchParams.get("module") || "";
  const result = url.searchParams.get("result") || "";
  const from = url.searchParams.get("from") || "";
  const to = url.searchParams.get("to") || "";
  return logs.filter((log) => (!search || `${log.operatorName} ${log.action} ${log.entityId} ${log.requestId}`.toLowerCase().includes(search)) && (!module || log.module === module) && (!result || log.result === result) && (!from || log.occurredAt.slice(0, 10) >= from) && (!to || log.occurredAt.slice(0, 10) <= to));
}
function authorize(request: Request) { const identity = request.headers.get("X-Mock-Identity") || "VIEWER"; if (!readableIdentities.has(identity)) throw new ApiError(403, "当前身份没有审计日志查看权限"); }
function csvCell(value: unknown) { const text = String(value ?? ""); return `"${text.replaceAll('"', '""')}"`; }

export const auditHandlers = [
  http.get(`${basePath}/export`, ({ request }) => {
    try { authorize(request); const items = matched(request); const lines = [["时间", "操作人", "动作", "模块", "对象", "结果", "请求标识"], ...items.map((item) => [item.occurredAt, item.operatorName, item.action, item.module, `${item.entityType}:${item.entityId}`, item.result, item.requestId])]; return ok({ fileName: `audit-logs-${new Date().toISOString().slice(0, 10)}.csv`, content: `\uFEFF${lines.map((line) => line.map(csvCell).join(",")).join("\r\n")}` }); }
    catch (error) { return fail(error); }
  }),
  http.get(basePath, ({ request }) => {
    try { authorize(request); const url = new URL(request.url); const page = Math.max(1, Number(url.searchParams.get("page")) || 1); const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10)); const items = matched(request); const offset = (page - 1) * size; return ok({ list: items.slice(offset, offset + size).map(summary), total: items.length, page, size }); }
    catch (error) { return fail(error); }
  }),
  http.get(`${basePath}/*`, ({ request }) => {
    try { authorize(request); const id = decodeURIComponent(new URL(request.url).pathname.slice(basePath.length + 1)); const log = logs.find((item) => item.id === id); if (!log) throw new ApiError(404, "审计记录不存在或当前账号不可见"); return ok(copy(log)); }
    catch (error) { return fail(error); }
  }),
];
