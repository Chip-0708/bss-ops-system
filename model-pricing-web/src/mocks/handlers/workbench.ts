import { http, HttpResponse } from "msw";
import type { WorkbenchDTO, WorkbenchMetricDTO, WorkbenchTaskDTO } from "../../modules/workbench/api/workbench";
import { ApiError } from "../../domain/common";

const basePath = "/api/internal/workbench";
const internalIdentities = new Set(["MODEL_OPS", "PURCHASING", "PRICING_OP", "FINANCE_OP", "VIEWER"]);

const metrics: Record<string, WorkbenchMetricDTO[]> = {
  MODEL_OPS: [
    { id: "model-total", label: "模型主数据", value: "24", suffix: "个", hint: "含草稿和下线版本", tone: "NORMAL" },
    { id: "model-pending", label: "待验证模型", value: "3", suffix: "个", hint: "需要模型运营处理", tone: "WARNING" },
    { id: "official-price", label: "官方价待核对", value: "2", suffix: "项", hint: "暂存区差异", tone: "WARNING" },
    { id: "model-alert", label: "本域告警", value: "4", suffix: "条", hint: "其中 1 条紧急", tone: "DANGER" },
  ],
  PURCHASING: [
    { id: "quote-approving", label: "待审批报价", value: "3", suffix: "份", hint: "供应商已提交", tone: "WARNING" },
    { id: "quote-pending", label: "已批准待生效", value: "1", suffix: "份", hint: "等待后端激活", tone: "NORMAL" },
    { id: "cost-stale", label: "成本待刷新", value: "1", suffix: "个", hint: "等待成本重算", tone: "DANGER" },
    { id: "supplier-expiring", label: "资质临期", value: "1", suffix: "家", hint: "15 天内到期", tone: "WARNING" },
  ],
  PRICING_OP: [
    { id: "policy-draft", label: "策略草稿", value: "2", suffix: "个", hint: "可继续编辑", tone: "NORMAL" },
    { id: "book-approving", label: "价目表审批中", value: "1", suffix: "个", hint: "等待审批结论", tone: "WARNING" },
    { id: "customer-draft", label: "客户报价草稿", value: "2", suffix: "份", hint: "尚未正式化", tone: "NORMAL" },
    { id: "pricing-alert", label: "定价告警", value: "2", suffix: "条", hint: "包含毛利风险", tone: "DANGER" },
  ],
  FINANCE_OP: [
    { id: "fx-pending", label: "待锁定汇率", value: "2", suffix: "组", hint: "下月汇率", tone: "WARNING" },
    { id: "account-warning", label: "授信预警", value: "1", suffix: "家", hint: "超过预警阈值", tone: "WARNING" },
    { id: "account-frozen", label: "冻结账户", value: "1", suffix: "家", hint: "已达授信上限", tone: "DANGER" },
    { id: "deposit", label: "押金待核对", value: "0", suffix: "笔", hint: "当前无待办", tone: "SUCCESS" },
  ],
  VIEWER: [
    { id: "models", label: "模型", value: "24", suffix: "个", hint: "只读概览", tone: "NORMAL" },
    { id: "suppliers", label: "供应商", value: "6", suffix: "家", hint: "只读概览", tone: "NORMAL" },
    { id: "customers", label: "客户", value: "6", suffix: "家", hint: "只读概览", tone: "NORMAL" },
    { id: "alerts", label: "待处理告警", value: "4", suffix: "条", hint: "只读概览", tone: "WARNING" },
  ],
};

const tasks: Record<string, WorkbenchTaskDTO[]> = {
  MODEL_OPS: [
    { id: "task-model-verify", title: "核对待验证模型", module: "模型管理", description: "确认模型能力、协议和采购状态。", count: 3, level: "WARNING", routeKey: "internal.models", routePath: "/internal/models" },
    { id: "task-price-sync", title: "处理官方价格差异", module: "官方价格", description: "检查暂存价格与当前版本的差异。", count: 2, level: "URGENT", routeKey: "internal.officialPrices", routePath: "/internal/official-prices?tab=staging" },
  ],
  PURCHASING: [
    { id: "task-quote", title: "审批供应商报价", module: "供应商报价", description: "核对 SKU 差异、供给约束和毛利预演。", count: 3, level: "URGENT", routeKey: "internal.supplierQuotes", routePath: "/internal/supplier-quotes" },
    { id: "task-cost", title: "关注待刷新成本", module: "成本管理", description: "报价生效后等待服务端生成新成本基线。", count: 1, level: "WARNING", routeKey: "internal.cost", routePath: "/internal/cost" },
  ],
  PRICING_OP: [
    { id: "task-book", title: "完善价目表草稿", module: "价目表", description: "补齐 SKU 售价和计划生效时间。", count: 2, level: "WARNING", routeKey: "internal.priceBooks", routePath: "/internal/price-books" },
    { id: "task-customer", title: "处理客户报价草稿", module: "客户与报价", description: "核对客户、价目表参考与报价原因。", count: 2, level: "NORMAL", routeKey: "internal.customers", routePath: "/internal/customers" },
  ],
  FINANCE_OP: [
    { id: "task-fx", title: "核对下月汇率", module: "财务信息", description: "确认来源后再通过后端锁定。", count: 2, level: "WARNING", routeKey: "internal.finance", routePath: "/internal/finance" },
    { id: "task-credit", title: "复核授信预警", module: "财务信息", description: "检查客户回款和授信占用。", count: 1, level: "URGENT", routeKey: "internal.finance", routePath: "/internal/finance" },
  ],
  VIEWER: [],
};

export const workbenchHandlers = [
  http.get(basePath, ({ request }) => {
    try {
      const identity = request.headers.get("X-Mock-Identity") || "VIEWER";
      if (!internalIdentities.has(identity)) throw new ApiError(403, "当前身份无权访问内部工作台");
      const data: WorkbenchDTO = {
        metrics: structuredClone(metrics[identity] || metrics.VIEWER),
        tasks: structuredClone(tasks[identity] || []),
        notices: [
          { id: "notice-1", title: "核心金额以服务端结果为准", description: "前端预览和 Mock 数据不作为正式成本、floor 或售价依据。", occurredAt: "2026-09-11T09:00:00+08:00" },
          { id: "notice-2", title: "当前为开发联调环境", description: "MSW 已启用，真实数据库和登录尚未接入。", occurredAt: "2026-09-11T08:30:00+08:00" },
        ],
        generatedAt: "2026-09-11T16:30:00+08:00",
      };
      return HttpResponse.json({ code: 0, message: "ok", data, requestId: `mock-${crypto.randomUUID()}` });
    } catch (error) {
      const candidate = error as { httpStatus?: number; message?: string }; const status = candidate.httpStatus || 500;
      return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: `mock-${crypto.randomUUID()}` }, { status });
    }
  }),
];
