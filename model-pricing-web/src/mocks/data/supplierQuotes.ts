import type { SupplierQuoteDetailDTO } from "../../api/supplierQuotes.types";

const baseItems = [
  {
    id: "item-input",
    skuId: "9007199254741000",
    skuCode: "gpt-4.1",
    skuName: "GPT 4.1",
    component: "输入 Token",
    currency: "USD",
    unit: "百万 Token",
    taxMode: "未税",
    proposedPrice: "1.76000000",
    previousPrice: "1.82000000",
    officialPrice: "2.00000000",
    bestPrice: "1.70000000",
    delta: "-0.06000000",
    changeRate: "-3.30",
  },
  {
    id: "item-output",
    skuId: "9007199254741000",
    skuCode: "gpt-4.1",
    skuName: "GPT 4.1",
    component: "输出 Token",
    currency: "USD",
    unit: "百万 Token",
    taxMode: "未税",
    proposedPrice: "7.10000000",
    previousPrice: "7.28000000",
    officialPrice: "8.00000000",
    bestPrice: "6.95000000",
    delta: "-0.18000000",
    changeRate: "-2.47",
  },
];

const definitions = [
  ["q-1001", "SQ-202609-001", "supplier-cloud", "云桥科技", "APPROVING", "2026-09-10T09:12:00+08:00", "2026-09-15T00:00:00+08:00", "PORTAL", "官方基准近期发生变化，请复核快照"],
  ["q-1002", "SQ-202609-002", "supplier-star", "星河算力", "APPROVING", "2026-09-10T10:06:00+08:00", "2026-09-18T00:00:00+08:00", "IMPORT", "同一模型多个组件倍率相同"],
  ["q-1003", "SQ-202609-003", "supplier-cloud", "云桥科技", "REJECTED", "2026-09-10T11:25:00+08:00", "2026-09-20T00:00:00+08:00", "PORTAL", "等待进入审批队列"],
  ["q-1004", "SQ-202609-004", "supplier-north", "北辰智能", "APPROVED_PENDING", "2026-09-09T15:40:00+08:00", "2026-09-16T00:00:00+08:00", "MANUAL", "已批准，等待后端任务激活"],
  ["q-1005", "SQ-202608-018", "supplier-star", "星河算力", "EFFECTIVE", "2026-08-22T14:18:00+08:00", "2026-09-01T00:00:00+08:00", "PORTAL", ""],
  ["q-1006", "SQ-202609-005", "supplier-north", "北辰智能", "REJECTED", "2026-09-10T13:02:00+08:00", "2026-09-22T00:00:00+08:00", "PORTAL", "毛利预演存在倒挂风险"],
] as const;

export function createSupplierQuoteSeeds(): SupplierQuoteDetailDTO[] {
  return definitions.map((definition, index) => {
    const [id, quoteNo, supplierId, supplierName, status, submittedAt, effectiveFrom, source, alert] = definition;
    return {
      id,
      quoteNo,
      version: [2, 3, 1, 2, 2, 1][index]!,
      supplierId,
      supplierName,
      supplierCode: supplierId.toUpperCase(),
      supplierContact: index % 2 ? "王经理 · 138****6521" : "李经理 · 139****3186",
      source,
      submittedAt,
      updatedAt: submittedAt,
      effectiveFrom,
      effectiveTo: "2026-12-31T23:59:59+08:00",
      skuCount: index % 2 ? 2 : 1,
      status,
      ownerName: "陈昊",
      alert: alert || undefined,
      items: baseItems.map((item) => ({
        ...item,
        id: `${id}-${item.id}`,
        proposedPrice:
          index === 5 && item.id === "item-output"
            ? "9.60000000"
            : item.proposedPrice,
      })),
      constraints: [
        { label: "RPM", previous: "1,000", proposed: "1,200" },
        { label: "并发数", previous: "50", proposed: "50" },
        { label: "结算周期", previous: "月结 30 天", proposed: "月结 30 天" },
      ],
      marginPreview: [
        {
          skuCode: "gpt-4.1",
          currentMargin: "24.50",
          proposedMargin: index === 5 ? "-3.20" : "26.10",
          risk: index === 5 ? "INVERTED" : "NORMAL",
        },
      ],
      warnings: alert ? [alert] : [],
      comparisonGeneratedAt: "2026-09-10T13:10:00+08:00",
      comparisonReady: true,
      canApprove: false,
    };
  });
}

// Shared in-memory backend records used by both supplier and internal portal handlers.
export const supplierQuoteRecords = createSupplierQuoteSeeds();
