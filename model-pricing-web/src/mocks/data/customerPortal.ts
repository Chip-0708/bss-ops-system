import { ApiError } from "../../types.ts";
import type { CustomerQuoteDetailDTO } from "../../api/customerQuotes.types";
import type { CustomerAccount, CustomerPortalDetail, CustomerSection } from "../../api/customerPortal.types";
import { customerQuoteRecords } from "./customers.ts";

const customerId = "customer-blue";
const updatedAt = "2026-09-13T09:00:00+08:00";
const account: CustomerAccount = { currency: "CNY", balance: "18500.00000000", creditLimit: "50000.00000000", usedCredit: "12000.00000000", deposit: "10000.00000000" };
const publicItems = (items: CustomerQuoteDetailDTO["items"]) => items.map(item => ({ skuCode: item.skuCode, skuName: item.skuName, component: item.component, unitPrice: item.unitPrice, currency: item.currency, unit: item.unit }));
const samplePrices = [{ skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "INPUT", unitPrice: "2.80000000", currency: "USD", unit: "百万 Token" },
  { skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "OUTPUT", unitPrice: "10.80000000", currency: "USD", unit: "百万 Token" }];
const readonlyRecords: CustomerPortalDetail[] = [
  { kind: "price-book", id: "customer-blue-price-book", code: "PB-GOLD-202609", name: "金牌客户标准价（Mock）", status: "EFFECTIVE", updatedAt, items: samplePrices, effectiveFrom: "2026-09-01T00:00:00+08:00", sourceLabel: "Mock等级价展示；不代表合同优先取价结果" },
  { kind: "contracts", id: "contract-blue-1", code: "CT-2026-0188", name: "大模型API年度服务合同（Mock快照）", status: "ACTIVE", updatedAt,
    validFrom: "2026-04-01T00:00:00+08:00", validTo: "2027-03-31T23:59:59+08:00", items: samplePrices.map(item => ({ ...item })) },
  { kind: "billing", id: "bill-blue-202608", code: "BILL-202608-001", name: "2026年8月账单（Mock）", status: "PAID", updatedAt, period: "2026-08", currency: "CNY", totalAmount: "3200.00000000",
    items: [{ skuCode: "gpt-4.1", description: "模型调用费用（账单透传样例）", amount: "3200.00000000" }] },
  { kind: "billing", id: "bill-blue-202609", code: "BILL-202609-001", name: "2026年9月账单（Mock）", status: "UNPAID", updatedAt, period: "2026-09", currency: "CNY", totalAmount: "1800.00000000",
    items: [{ skuCode: "gpt-4.1", description: "模型调用费用（账单透传样例）", amount: "1800.00000000" }] },
  { kind: "notifications", id: "notice-price-1", code: "NOTICE-PRICE-001", name: "模型价格调整通知（Mock）", status: "PUBLISHED", updatedAt, category: "PRICE_CHANGE", content: "这是价格变更通知样例，具体生效时间和最终价格以服务端通知及取价结果为准。" },
  { kind: "notifications", id: "notice-retire-1", code: "NOTICE-RETIRE-001", name: "模型退役通知（Mock）", status: "PUBLISHED", updatedAt, category: "MODEL_RETIREMENT", content: "这是退役通知样例，请联系负责人核对替代方案；查看通知不会改变模型或合同状态。" },
];

export function createCustomerPortalMock(quotes = customerQuoteRecords, now = () => Date.now(), accepted = new Map<string, string>()) {
  const keys = new Map<string, { id: string; acceptedAt: string }>();
  const authorize = (identity: string | null) => { if (identity !== "CUSTOMER") throw new ApiError(403, "当前身份无权访问客户门户数据"); };
  const visibleQuote = (id: string) => {
    const quote = quotes.find(item => item.id === id && item.customerId === customerId && item.status !== "DRAFT");
    if (!quote) throw new ApiError(404, "报价不存在或当前客户不可见");
    return quote;
  };
  const canAccept = (quote: CustomerQuoteDetailDTO) => quote.status === "FORMAL" && !accepted.has(quote.id) &&
    !!quote.validFrom && !!quote.validTo && Date.parse(quote.validFrom) <= now() && now() < Date.parse(quote.validTo);
  const projectQuote = (quote: CustomerQuoteDetailDTO): CustomerPortalDetail => ({ kind: "quotes", id: quote.id, code: quote.quoteNo,
    name: quote.name, status: quote.status, updatedAt: quote.updatedAt, items: publicItems(quote.items),
    validFrom: quote.validFrom, validTo: quote.validTo, canAccept: canAccept(quote), acceptedAt: accepted.get(quote.id) });
  const records = (section: CustomerSection) => section === "quotes"
    ? quotes.filter(item => item.customerId === customerId && item.status !== "DRAFT").map(projectQuote)
    : readonlyRecords.filter(item => item.kind === section);
  return {
    home(identity: string | null) {
      authorize(identity);
      return { customerName: "蓝海电商", quoteCount: records("quotes").length, contractCount: records("contracts").length,
        notificationCount: records("notifications").length, account: structuredClone(account) };
    },
    list(identity: string | null, section: CustomerSection, params: { page: number; size: number; search?: string; status?: string }) {
      authorize(identity);
      if (!Number.isInteger(params.page) || params.page < 1 || !Number.isInteger(params.size) || params.size < 1 || params.size > 100)
        throw new ApiError(400, "分页参数不正确，页大小须为1~100");
      const search = (params.search || "").trim().toLowerCase();
      const matched = records(section).filter(item => (!search || `${item.code} ${item.name}`.toLowerCase().includes(search)) && (!params.status || item.status === params.status));
      return structuredClone({ list: matched.slice((params.page - 1) * params.size, params.page * params.size), total: matched.length,
        page: params.page, size: params.size, ...(section === "billing" ? { account } : {}) });
    },
    get(identity: string | null, section: CustomerSection, id: string) {
      authorize(identity);
      const record = section === "quotes" ? projectQuote(visibleQuote(id)) : records(section).find(item => item.id === id);
      if (!record) throw new ApiError(404, "记录不存在或当前客户不可见");
      return structuredClone(record);
    },
    accept(identity: string | null, id: string, key: string | null, body: unknown) {
      authorize(identity);
      visibleQuote(id);
      if (!key) throw new ApiError(400, "缺少Idempotency-Key");
      if (!body || typeof body !== "object" || Array.isArray(body) || Object.keys(body).length) throw new ApiError(400, "接受报价仅允许提交空对象");
      const cached = keys.get(key);
      if (cached) {
        if (cached.id !== id) throw new ApiError(409, "幂等键已用于其他报价");
        return { quoteId: cached.id, acceptedAt: cached.acceptedAt };
      }
      const quote = visibleQuote(id);
      if (!canAccept(quote)) throw new ApiError(409, "仅可接受有效期内且尚未接受的正式报价（Mock暂定规则）");
      const acceptedAt = new Date(now()).toISOString();
      accepted.set(id, acceptedAt);
      keys.set(key, { id, acceptedAt });
      // Acceptance acknowledges a quote; it does not create a contract or mutate lifecycle.
      return { quoteId: id, acceptedAt };
    },
  };
}
