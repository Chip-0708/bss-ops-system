export type StatusTagType = "success" | "warning" | "danger" | "info";

// Customer query-only states; billing is a PROVISIONAL read-only passthrough.
export const CUSTOMER_PORTAL_RECORD_STATUS = {
  EFFECTIVE: { label: "已生效", type: "success" }, ACTIVE: { label: "有效", type: "success" },
  EXPIRING: { label: "即将到期", type: "warning" }, ENDED: { label: "已结束", type: "info" },
  PAID: { label: "已结清", type: "success" }, UNPAID: { label: "待结清", type: "warning" },
  PUBLISHED: { label: "已发布", type: "info" },
} as const;

export const MODEL_STATUS = {
  DRAFT: { label: "草稿", type: "info" },
  PENDING_VERIFY: { label: "待验证", type: "warning" },
  PURCHASABLE: { label: "可采购", type: "warning" },
  PUBLISHED: { label: "已上架", type: "success" },
  DEPRECATING: { label: "即将下线", type: "danger" },
  OFFLINE: { label: "已下线", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type ModelStatus = keyof typeof MODEL_STATUS;
export const MODEL_VERIFY_STATUS_LABELS = {
  UNVERIFIED: "未验证", MANUAL: "人工验证", PROBED: "探测验证",
} as const;
export const MODEL_STATUS_LABELS = Object.fromEntries(
  Object.entries(MODEL_STATUS).map(([status, config]) => [status, config.label]),
) as Record<ModelStatus, string>;

export const SUPPLIER_QUOTE_STATUS = {
  DRAFT: { label: "草稿", type: "info" },
  SUBMITTED: { label: "已提交", type: "info" },
  APPROVING: { label: "审批中", type: "warning" },
  APPROVED_PENDING: { label: "已批准·待生效", type: "warning" },
  EFFECTIVE: { label: "已生效", type: "success" },
  EXPIRED: { label: "已结束", type: "info" },
  VOIDED: { label: "已作废", type: "info" },
  REJECTED: { label: "已驳回", type: "danger" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type SupplierQuoteStatus = keyof typeof SUPPLIER_QUOTE_STATUS;
export const SUPPLIER_QUOTE_STATUS_LABELS = Object.fromEntries(
  Object.entries(SUPPLIER_QUOTE_STATUS).map(([status, config]) => [status, config.label]),
) as Record<SupplierQuoteStatus, string>;

export function getSupplierQuoteStatusType(
  status: SupplierQuoteStatus,
): StatusTagType {
  return SUPPLIER_QUOTE_STATUS[status]?.type ?? "info";
}

export const PRICE_BOOK_STATUS = {
  DRAFT: { label: "草稿", type: "info" },
  APPROVING: { label: "审批中", type: "warning" },
  PENDING_EFFECTIVE: { label: "待生效", type: "warning" },
  EFFECTIVE: { label: "已生效", type: "success" },
  FROZEN: { label: "已冻结", type: "danger" },
  RETIRED: { label: "已替代", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type PriceBookStatus = keyof typeof PRICE_BOOK_STATUS;
export const PRICE_BOOK_STATUS_LABELS = Object.fromEntries(
  Object.entries(PRICE_BOOK_STATUS).map(([status, config]) => [status, config.label]),
) as Record<PriceBookStatus, string>;

export function getPriceBookStatusType(status: PriceBookStatus): StatusTagType {
  return PRICE_BOOK_STATUS[status]?.type ?? "info";
}

export function getModelStatusType(status: ModelStatus): StatusTagType {
  return MODEL_STATUS[status]?.type ?? "info";
}

export const ALERT_STATUS = {
  OPEN: { label: "待处理", type: "danger" },
  HANDLING: { label: "处理中", type: "warning" },
  RESOLVED: { label: "已解决", type: "success" },
  IGNORED: { label: "已忽略", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type AlertStatus = keyof typeof ALERT_STATUS;

export const ALERT_LEVEL = {
  CRITICAL: { label: "紧急", type: "danger" },
  HIGH: { label: "高", type: "danger" },
  MID: { label: "中", type: "warning" },
  LOW: { label: "低", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type AlertLevel = keyof typeof ALERT_LEVEL;

export const COST_BASELINE_STATUS = {
  CURRENT: { label: "当前有效", type: "success" },
  RECALCULATING: { label: "重算中", type: "warning" },
  STALE: { label: "待刷新", type: "danger" },
  MISSING: { label: "暂无基线", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type CostBaselineStatus = keyof typeof COST_BASELINE_STATUS;

export const PRICING_POLICY_STATUS = {
  DRAFT: { label: "草稿", type: "info" },
  ACTIVE: { label: "启用中", type: "success" },
  ARCHIVED: { label: "已归档", type: "warning" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type PricingPolicyStatus = keyof typeof PRICING_POLICY_STATUS;

export const SUPPLIER_STATUS = {
  PENDING_REVIEW: { label: "待审核", type: "warning" },
  ACTIVE: { label: "合作中", type: "success" },
  SUSPENDED: { label: "已暂停", type: "danger" },
  INACTIVE: { label: "已停用", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type SupplierStatus = keyof typeof SUPPLIER_STATUS;

export const QUALIFICATION_STATUS = {
  VALID: { label: "有效", type: "success" },
  EXPIRING: { label: "即将到期", type: "warning" },
  EXPIRED: { label: "已过期", type: "danger" },
  MISSING: { label: "待补充", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type QualificationStatus = keyof typeof QUALIFICATION_STATUS;

export const MODEL_APPLICATION_STATUS = {
  DRAFT: { label: "草稿", type: "info" },
  SUBMITTED: { label: "已提交", type: "warning" },
  REVIEWING: { label: "审核中", type: "warning" },
  APPROVED: { label: "已通过", type: "success" },
  MERGED: { label: "已合并", type: "success" },
  REJECTED: { label: "已驳回", type: "danger" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type ModelApplicationStatus = keyof typeof MODEL_APPLICATION_STATUS;

export const QUALIFICATION_SUBMISSION_STATUS = {
  SUBMITTED: { label: "已提交", type: "warning" },
  REVIEWING: { label: "审核中", type: "warning" },
  APPROVED: { label: "已通过", type: "success" },
  REJECTED: { label: "已驳回", type: "danger" },
  EXPIRED: { label: "已过期", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type QualificationSubmissionStatus = keyof typeof QUALIFICATION_SUBMISSION_STATUS;

export const RECONCILIATION_STATUS = {
  PENDING: { label: "待对账", type: "warning" },
  CHECKING: { label: "核对中", type: "warning" },
  DISPUTED: { label: "存在差异", type: "danger" },
  CONFIRMED: { label: "已确认", type: "success" },
  SETTLED: { label: "已结算", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type ReconciliationStatus = keyof typeof RECONCILIATION_STATUS;

export const CUSTOMER_STATUS = {
  ACTIVE: { label: "合作中", type: "success" },
  FROZEN: { label: "已冻结", type: "danger" },
  INACTIVE: { label: "已停用", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type CustomerStatus = keyof typeof CUSTOMER_STATUS;

export const CUSTOMER_QUOTE_STATUS = {
  DRAFT: { label: "草稿", type: "info" },
  TEMP: { label: "临时报价", type: "warning" },
  FORMAL: { label: "正式报价", type: "success" },
  CONTRACT: { label: "已转合同", type: "success" },
  EXPIRED: { label: "已过期", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type CustomerQuoteStatus = keyof typeof CUSTOMER_QUOTE_STATUS;

export const FX_RATE_STATUS = {
  PENDING_LOCK: { label: "待锁定", type: "warning" },
  LOCKED: { label: "已锁定", type: "success" },
  EXPIRED: { label: "已失效", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type FxRateStatus = keyof typeof FX_RATE_STATUS;

export const FINANCE_ACCOUNT_STATUS = {
  NORMAL: { label: "正常", type: "success" },
  WARNING: { label: "需关注", type: "warning" },
  FROZEN: { label: "已冻结", type: "danger" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type FinanceAccountStatus = keyof typeof FINANCE_ACCOUNT_STATUS;

export const INTEGRATION_STATUS = {
  AVAILABLE: { label: "可用", type: "success" },
  DEGRADED: { label: "需关注", type: "warning" },
  DISABLED: { label: "未启用", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type IntegrationStatus = keyof typeof INTEGRATION_STATUS;

export const JOB_RUN_STATUS = {
  SUCCESS: { label: "成功", type: "success" },
  RUNNING: { label: "运行中", type: "warning" },
  FAILED: { label: "失败", type: "danger" },
  WAITING: { label: "等待执行", type: "info" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type JobRunStatus = keyof typeof JOB_RUN_STATUS;

// 采集批次状态（后端 sync_job.status：RUNNING/SUCCESS/FAILED）。
export const SYNC_JOB_STATUS = {
  RUNNING: { label: "采集中", type: "warning" },
  SUCCESS: { label: "成功", type: "success" },
  FAILED: { label: "失败", type: "danger" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type SyncJobStatus = keyof typeof SYNC_JOB_STATUS;

// 暂存差异比对状态（后端读侧实时计算：NEW/CHANGED/UNCHANGED/UNMATCHED）。
export const STAGING_DIFF_STATUS = {
  NEW: { label: "新增", type: "success" },
  CHANGED: { label: "变更", type: "warning" },
  UNCHANGED: { label: "无变化", type: "info" },
  UNMATCHED: { label: "未匹配", type: "danger" },
} as const satisfies Record<string, { label: string; type: StatusTagType }>;

export type StagingDiffStatus = keyof typeof STAGING_DIFF_STATUS;
