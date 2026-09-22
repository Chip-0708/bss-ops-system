import type { CustomerDetailDTO } from "../../api/customers.types";
import type { CustomerQuoteDetailDTO } from "../../api/customerQuotes.types";

export const customerSeeds: CustomerDetailDTO[] = [
  {
    id: "customer-blue", code: "CUS-BLUE-001", name: "蓝海电商", levelCode: "GOLD", status: "ACTIVE", ownerName: "周婷", currentPriceBookName: "金牌客户标准价", currentPriceBookCode: "PB-GOLD-202609", activeContractCount: 1, quoteCount: 3, updatedAt: "2026-09-10T14:25:00+08:00",
    legalName: "上海蓝海电子商务有限公司", industry: "电商零售", contactName: "顾经理", contactPhoneMasked: "138****2719", contactEmailMasked: "gu***@blueocean.example", billingCurrency: "CNY", serviceRegions: ["中国大陆", "新加坡"],
    contracts: [{ id: "contract-blue-1", contractNo: "CT-2026-0188", name: "大模型 API 年度服务合同", status: "ACTIVE", validFrom: "2026-04-01T00:00:00+08:00", validTo: "2027-03-31T23:59:59+08:00" }],
    notes: ["当前使用金牌客户标准价。", "临时报价不得覆盖已生效合同价格。"],
  },
  {
    id: "customer-lighthouse", code: "CUS-LIGHT-002", name: "灯塔教育", levelCode: "SILVER", status: "ACTIVE", ownerName: "周婷", currentPriceBookName: "银牌客户标准价", currentPriceBookCode: "PB-SILVER-202609", activeContractCount: 0, quoteCount: 2, updatedAt: "2026-09-09T17:40:00+08:00",
    legalName: "北京灯塔在线教育科技有限公司", industry: "在线教育", contactName: "许老师", contactPhoneMasked: "136****5082", contactEmailMasked: "xu***@lighthouse.example", billingCurrency: "CNY", serviceRegions: ["中国大陆"], contracts: [],
    notes: ["正在评估新一轮正式报价。"],
  },
  {
    id: "customer-medical", code: "CUS-MED-003", name: "康澜医疗", levelCode: "STANDARD", status: "FROZEN", ownerName: "沈嘉", currentPriceBookName: "标准客户价", currentPriceBookCode: "PB-STANDARD-202609", activeContractCount: 1, quoteCount: 1, updatedAt: "2026-09-08T10:15:00+08:00",
    legalName: "杭州康澜医疗科技有限公司", industry: "医疗科技", contactName: "罗经理", contactPhoneMasked: "137****4266", contactEmailMasked: "luo***@kanglan.example", billingCurrency: "CNY", serviceRegions: ["中国大陆"],
    contracts: [{ id: "contract-med-1", contractNo: "CT-2026-0112", name: "模型推理服务合同", status: "EXPIRING", validFrom: "2026-01-01T00:00:00+08:00", validTo: "2026-09-30T23:59:59+08:00" }],
    notes: ["客户已冻结，新报价操作应由后端拒绝。"],
  },
  {
    id: "customer-studio", code: "CUS-STUDIO-004", name: "像素工场", levelCode: "ECONOMY", status: "ACTIVE", ownerName: "沈嘉", currentPriceBookName: "经济型客户价", currentPriceBookCode: "PB-ECONOMY-202609", activeContractCount: 0, quoteCount: 0, updatedAt: "2026-09-07T16:30:00+08:00",
    legalName: "深圳像素工场创意有限公司", industry: "内容创意", contactName: "何经理", contactPhoneMasked: "135****1190", contactEmailMasked: "he***@pixelstudio.example", billingCurrency: "USD", serviceRegions: ["中国大陆", "美国西部"], contracts: [],
    notes: ["当前仅使用标准价目表。"],
  },
  {
    id: "customer-old", code: "CUS-OLD-005", name: "远山传媒", levelCode: "STANDARD", status: "INACTIVE", ownerName: "沈嘉", activeContractCount: 0, quoteCount: 2, updatedAt: "2026-08-20T09:10:00+08:00",
    legalName: "广州远山传媒有限公司", industry: "数字传媒", contactName: "郑经理", contactPhoneMasked: "134****9027", contactEmailMasked: "zheng***@farhill.example", billingCurrency: "CNY", serviceRegions: ["中国大陆"], contracts: [],
    notes: ["合作已结束，历史报价与合同仅供查询。"],
  },
  {
    id: "customer-fin", code: "CUS-FIN-006", name: "青禾金融科技", levelCode: "GOLD", status: "ACTIVE", ownerName: "周婷", currentPriceBookName: "金牌客户标准价", currentPriceBookCode: "PB-GOLD-202609", activeContractCount: 1, quoteCount: 1, updatedAt: "2026-09-06T12:20:00+08:00",
    legalName: "南京青禾金融科技有限公司", industry: "金融科技", contactName: "魏经理", contactPhoneMasked: "133****6182", contactEmailMasked: "wei***@greenfin.example", billingCurrency: "CNY", serviceRegions: ["中国大陆"],
    contracts: [{ id: "contract-fin-1", contractNo: "CT-2026-0201", name: "智能客服模型服务合同", status: "ACTIVE", validFrom: "2026-06-01T00:00:00+08:00", validTo: "2027-05-31T23:59:59+08:00" }], notes: [],
  },
];

const quoteItems = [
  { skuId: "gpt-4.1", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "INPUT" as const, unitPrice: "2.72000000", referencePrice: "2.80000000", floorPrice: "2.40000000", currency: "USD", unit: "百万 Token" },
  { skuId: "gpt-4.1", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "OUTPUT" as const, unitPrice: "10.50000000", referencePrice: "10.80000000", floorPrice: "9.60000000", currency: "USD", unit: "百万 Token" },
];

export const customerQuoteSeeds: CustomerQuoteDetailDTO[] = [
  { id: "cq-1001", quoteNo: "CQ-202609-001", name: "蓝海电商十一月扩容报价", customerId: "customer-blue", customerName: "蓝海电商", status: "DRAFT", validTo: "2026-11-30T23:59:59+08:00", itemCount: 2, currency: "USD", ownerName: "周婷", updatedAt: "2026-09-10T15:10:00+08:00", priceBookCode: "PB-GOLD-202609", priceBookName: "金牌客户标准价", validFrom: "2026-11-01T00:00:00+08:00", reason: "预计用量增长，申请阶段性价格。", items: quoteItems.map((item) => ({ ...item })), canEdit: true },
  { id: "cq-1002", quoteNo: "CQ-202608-012", name: "灯塔教育秋季活动临时报价", customerId: "customer-lighthouse", customerName: "灯塔教育", status: "TEMP", validTo: "2026-09-30T23:59:59+08:00", itemCount: 2, currency: "USD", ownerName: "周婷", updatedAt: "2026-08-28T11:20:00+08:00", priceBookCode: "PB-SILVER-202609", priceBookName: "银牌客户标准价", validFrom: "2026-09-01T00:00:00+08:00", reason: "秋季活动临时放量。", items: quoteItems.map((item) => ({ ...item, unitPrice: item.component === "INPUT" ? "2.76000000" : "10.65000000" })), canEdit: false },
  { id: "cq-1003", quoteNo: "CQ-202607-006", name: "蓝海电商年度正式报价", customerId: "customer-blue", customerName: "蓝海电商", status: "CONTRACT", validTo: "2027-03-31T23:59:59+08:00", itemCount: 2, currency: "USD", ownerName: "周婷", updatedAt: "2026-07-02T09:35:00+08:00", priceBookCode: "PB-GOLD-202607", priceBookName: "金牌客户标准价", validFrom: "2026-07-01T00:00:00+08:00", reason: "年度合同价格。", items: quoteItems.map((item) => ({ ...item })), canEdit: false },
  { id: "cq-1004", quoteNo: "CQ-202606-018", name: "远山传媒项目报价", customerId: "customer-old", customerName: "远山传媒", status: "EXPIRED", validTo: "2026-07-31T23:59:59+08:00", itemCount: 2, currency: "USD", ownerName: "沈嘉", updatedAt: "2026-06-18T14:00:00+08:00", reason: "历史项目报价。", items: quoteItems.map((item) => ({ ...item })), canEdit: false },
  { id: "cq-1005", quoteNo: "CQ-202609-002", name: "青禾金融正式报价", customerId: "customer-fin", customerName: "青禾金融科技", status: "FORMAL", validTo: "2026-12-31T23:59:59+08:00", itemCount: 2, currency: "USD", ownerName: "周婷", updatedAt: "2026-09-09T10:40:00+08:00", priceBookCode: "PB-GOLD-202609", priceBookName: "金牌客户标准价", validFrom: "2026-10-01T00:00:00+08:00", reason: "正式商务报价。", items: quoteItems.map((item) => ({ ...item })), canEdit: false },
  { id: "cq-1006", quoteNo: "CQ-202609-003", name: "灯塔教育新学期报价草稿", customerId: "customer-lighthouse", customerName: "灯塔教育", status: "DRAFT", itemCount: 2, currency: "USD", ownerName: "周婷", updatedAt: "2026-09-10T16:05:00+08:00", priceBookCode: "PB-SILVER-202609", priceBookName: "银牌客户标准价", reason: "待确认预计用量。", items: quoteItems.map((item) => ({ ...item })), canEdit: true },
];

// Shared across internal and customer handlers; draft records remain invisible to customers.
export const customerQuoteRecords = structuredClone(customerQuoteSeeds);
customerQuoteRecords.push({ ...structuredClone(customerQuoteSeeds[0]!), id: "cq-blue-formal-demo",
  quoteNo: "CQ-BLUE-DEMO-ACCEPT", name: "蓝海电商可接受正式报价（Mock）", status: "FORMAL",
  validFrom: "2026-09-01T00:00:00+08:00", validTo: "2099-12-31T23:59:59+08:00", canEdit: false });
export const customerQuoteAcceptances = new Map<string, string>();
