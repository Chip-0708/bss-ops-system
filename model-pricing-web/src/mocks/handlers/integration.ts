import { http, HttpResponse } from "msw";
import type { IntegrationOverviewDTO } from "../../api/integration";
import { ApiError } from "../../domain/common";

const readableIdentities = new Set(["MODEL_OPS", "FINANCE_OP", "ADMIN_OP", "VIEWER"]);

const overview: IntegrationOverviewDTO = {
  environment: "开发环境 · MSW",
  generatedAt: "2026-09-11T17:00:00+08:00",
  channels: [
    { id: "channel-go-api", name: "Go Backend API", category: "BACKEND", description: "业务环境中的统一后端入口。", status: "DISABLED", basePath: "/api/internal", authentication: "Bearer Token", note: "当前开发环境由 MSW 响应，尚未完成真实联调。" },
    { id: "channel-model-vendor", name: "模型厂商价格源", category: "MODEL_VENDOR", description: "采集官方模型与价格信息。", status: "DEGRADED", authentication: "服务端凭据", lastCheckedAt: "2026-09-11T04:05:00+08:00", note: "当前仅展示 Legacy Mock，不代表真实采集成功。" },
    { id: "channel-notification", name: "内部通知通道", category: "NOTIFICATION", description: "推送审批、告警和到期提醒。", status: "DISABLED", authentication: "服务端应用身份", note: "通知提供方与模板尚未确认。" },
    { id: "channel-mcp", name: "MCP 业务能力", category: "MCP", description: "面向受控调用方暴露查询和业务操作能力。", status: "DISABLED", basePath: "/mcp", authentication: "调用方身份 + 权限", note: "当前仅保留能力边界说明，未连接生产服务。" },
  ],
  jobs: [
    { id: "job-model-sync", name: "厂商模型清单同步", schedule: "每日 03:00", status: "SUCCESS", lastStartedAt: "2026-09-11T03:00:00+08:00", lastFinishedAt: "2026-09-11T03:04:12+08:00", affectedRecords: 4, ownerModule: "模型管理", message: "Mock 最近一次执行成功。" },
    { id: "job-price-sync", name: "官方价格采集", schedule: "每日 04:00 / 事件触发", status: "SUCCESS", lastStartedAt: "2026-09-11T04:00:00+08:00", lastFinishedAt: "2026-09-11T04:05:00+08:00", affectedRecords: 2, ownerModule: "官方价格", message: "发现 2 项暂存差异。" },
    { id: "job-cost", name: "成本基线重算", schedule: "每日 06:00", status: "RUNNING", lastStartedAt: "2026-09-11T16:58:00+08:00", affectedRecords: 1, ownerModule: "成本管理", message: "报价变更触发单 SKU 重算。" },
    { id: "job-quote", name: "预约报价生效", schedule: "每分钟", status: "WAITING", ownerModule: "供应商报价", message: "等待下一调度周期。" },
    { id: "job-alert", name: "供应商资质到期检查", schedule: "每日 08:00", status: "FAILED", lastStartedAt: "2026-09-11T08:00:00+08:00", lastFinishedAt: "2026-09-11T08:00:22+08:00", ownerModule: "告警中心", message: "Mock 失败样例：通知通道未启用。" },
  ],
  events: [
    { id: "event-quote-approved", eventType: "supplier_quote.approved", direction: "OUTBOUND", producer: "供应商报价", consumers: ["成本重算", "审计日志"], status: "AVAILABLE", description: "报价审批通过后通知成本域；不代表报价已生效。" },
    { id: "event-cost-updated", eventType: "cost_baseline.updated", direction: "OUTBOUND", producer: "成本管理", consumers: ["定价预警", "价目表校验"], status: "AVAILABLE", description: "新成本版本生成后触发定价影响检查。" },
    { id: "event-price-changed", eventType: "official_price.changed", direction: "INBOUND", producer: "模型厂商价格源", consumers: ["官方价格暂存区"], status: "DEGRADED", description: "正式事件格式和签名校验待确认。" },
    { id: "event-customer-quote", eventType: "customer_quote.formalized", direction: "OUTBOUND", producer: "客户报价", consumers: ["客户门户", "合同管理"], status: "DISABLED", description: "客户报价正式化流程尚未接入。" },
  ],
  mcpCapabilities: [
    { id: "mcp-model-query", name: "模型与价格查询", mode: "READ", permission: "M1:V / M7:V", status: "DISABLED", boundary: "按调用方数据域返回，财务字段默认剔除。" },
    { id: "mcp-cost-query", name: "成本基线查询", mode: "READ", permission: "M5:V", status: "DISABLED", boundary: "只返回服务端已保存基线，不允许调用方提供前端计算结果。" },
    { id: "mcp-quote-action", name: "报价审批操作", mode: "WRITE", permission: "M4:A", status: "DISABLED", boundary: "必须携带调用方身份、幂等键和审批理由，并写入审计。" },
  ],
};

export const integrationHandlers = [
  http.get("/api/internal/integration/overview", ({ request }) => {
    try {
      const identity = request.headers.get("X-Mock-Identity") || "VIEWER";
      if (!readableIdentities.has(identity)) throw new ApiError(403, "当前身份无权查看系统对接信息");
      return HttpResponse.json({ code: 0, message: "ok", data: structuredClone(overview), requestId: `mock-${crypto.randomUUID()}` });
    } catch (error) {
      const candidate = error as { httpStatus?: number; message?: string }; const status = candidate.httpStatus || 500;
      return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: `mock-${crypto.randomUUID()}` }, { status });
    }
  }),
];
