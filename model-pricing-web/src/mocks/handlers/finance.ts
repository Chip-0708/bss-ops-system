import { http, HttpResponse } from "msw";
import type { FinanceAccountDetailDTO, FinanceAccountSummaryDTO, FxRateDTO } from "../../api/finance.types";
import { ApiError } from "../../domain/common";

const basePath = "/api/internal/finance";
const readableIdentities = new Set(["FINANCE_OP", "VIEWER"]);

const fxRates: FxRateDTO[] = [
  { id: "fx-202609-usd-cny", month: "2026-09", baseCurrency: "USD", quoteCurrency: "CNY", rate: "7.10850000", source: "财务月度基准", status: "LOCKED", lockedAt: "2026-09-01T09:05:00+08:00", lockedBy: "许宁", updatedAt: "2026-09-01T09:05:00+08:00" },
  { id: "fx-202609-eur-cny", month: "2026-09", baseCurrency: "EUR", quoteCurrency: "CNY", rate: "8.32610000", source: "财务月度基准", status: "LOCKED", lockedAt: "2026-09-01T09:06:00+08:00", lockedBy: "许宁", updatedAt: "2026-09-01T09:06:00+08:00" },
  { id: "fx-202610-usd-cny", month: "2026-10", baseCurrency: "USD", quoteCurrency: "CNY", rate: "7.12000000", source: "待财务确认", status: "PENDING_LOCK", updatedAt: "2026-09-10T16:00:00+08:00" },
  { id: "fx-202610-eur-cny", month: "2026-10", baseCurrency: "EUR", quoteCurrency: "CNY", rate: "8.35000000", source: "待财务确认", status: "PENDING_LOCK", updatedAt: "2026-09-10T16:00:00+08:00" },
  { id: "fx-202608-usd-cny", month: "2026-08", baseCurrency: "USD", quoteCurrency: "CNY", rate: "7.15680000", source: "财务月度基准", status: "EXPIRED", lockedAt: "2026-08-01T09:00:00+08:00", lockedBy: "许宁", updatedAt: "2026-09-01T00:05:00+08:00" },
  { id: "fx-202608-eur-cny", month: "2026-08", baseCurrency: "EUR", quoteCurrency: "CNY", rate: "8.21150000", source: "财务月度基准", status: "EXPIRED", lockedAt: "2026-08-01T09:01:00+08:00", lockedBy: "许宁", updatedAt: "2026-09-01T00:05:00+08:00" },
];

const accounts: FinanceAccountDetailDTO[] = [
  { customerId: "customer-blue", customerCode: "CUS-BLUE-001", customerName: "蓝海电商", levelCode: "GOLD", status: "NORMAL", currency: "CNY", creditLimit: "2000000.00", usedCredit: "720000.00", availableCredit: "1280000.00", depositBalance: "300000.00", paymentTerms: "月结 30 天", updatedAt: "2026-09-10T14:30:00+08:00", billingEntity: "上海蓝海电子商务有限公司", invoiceTitle: "上海蓝海电子商务有限公司", taxNoMasked: "9131**********7Q", settlementCycle: "每月 5 日出账，30 日内结清", warningThresholdRate: "80.00", recentTransactions: [{ id: "txn-blue-1", occurredAt: "2026-09-09T18:00:00+08:00", type: "CREDIT_USAGE", amount: "126000.00", currency: "CNY", referenceNo: "BILL-202609-0091", note: "模型 API 用量入账" }, { id: "txn-blue-2", occurredAt: "2026-09-02T10:20:00+08:00", type: "DEPOSIT_IN", amount: "100000.00", currency: "CNY", referenceNo: "RCPT-202609-0018", note: "押金补充" }], notes: ["当前授信占用未达到预警线。"] },
  { customerId: "customer-lighthouse", customerCode: "CUS-LIGHT-002", customerName: "灯塔教育", levelCode: "SILVER", status: "WARNING", currency: "CNY", creditLimit: "800000.00", usedCredit: "690000.00", availableCredit: "110000.00", depositBalance: "80000.00", paymentTerms: "月结 15 天", updatedAt: "2026-09-10T09:15:00+08:00", billingEntity: "北京灯塔在线教育科技有限公司", invoiceTitle: "北京灯塔在线教育科技有限公司", taxNoMasked: "9111**********3D", settlementCycle: "每月 10 日出账，15 日内结清", warningThresholdRate: "80.00", recentTransactions: [{ id: "txn-light-1", occurredAt: "2026-09-10T08:45:00+08:00", type: "CREDIT_USAGE", amount: "95000.00", currency: "CNY", referenceNo: "BILL-202609-0102", note: "活动期用量入账" }], notes: ["授信占用超过预警线，新增正式报价需后端复核。"] },
  { customerId: "customer-medical", customerCode: "CUS-MED-003", customerName: "康澜医疗", levelCode: "STANDARD", status: "FROZEN", currency: "CNY", creditLimit: "500000.00", usedCredit: "500000.00", availableCredit: "0.00", depositBalance: "50000.00", paymentTerms: "预付", updatedAt: "2026-09-08T10:20:00+08:00", billingEntity: "杭州康澜医疗科技有限公司", invoiceTitle: "杭州康澜医疗科技有限公司", taxNoMasked: "9133**********6R", settlementCycle: "预付余额结算", warningThresholdRate: "90.00", recentTransactions: [{ id: "txn-med-1", occurredAt: "2026-09-08T09:50:00+08:00", type: "CREDIT_USAGE", amount: "50000.00", currency: "CNY", referenceNo: "BILL-202609-0077", note: "达到授信上限" }], notes: ["账户已由服务端冻结，前端不可自行解除。"] },
  { customerId: "customer-studio", customerCode: "CUS-STUDIO-004", customerName: "像素工场", levelCode: "ECONOMY", status: "NORMAL", currency: "USD", creditLimit: "100000.00", usedCredit: "12500.00", availableCredit: "87500.00", depositBalance: "10000.00", paymentTerms: "预付", updatedAt: "2026-09-07T16:35:00+08:00", billingEntity: "深圳像素工场创意有限公司", invoiceTitle: "深圳像素工场创意有限公司", taxNoMasked: "9144**********2H", settlementCycle: "预付余额结算", warningThresholdRate: "90.00", recentTransactions: [], notes: [] },
  { customerId: "customer-old", customerCode: "CUS-OLD-005", customerName: "远山传媒", levelCode: "STANDARD", status: "NORMAL", currency: "CNY", creditLimit: "0.00", usedCredit: "0.00", availableCredit: "0.00", depositBalance: "0.00", paymentTerms: "已结清", updatedAt: "2026-08-20T09:15:00+08:00", billingEntity: "广州远山传媒有限公司", invoiceTitle: "广州远山传媒有限公司", taxNoMasked: "9144**********9L", settlementCycle: "合作已结束", warningThresholdRate: "80.00", recentTransactions: [{ id: "txn-old-1", occurredAt: "2026-08-20T08:30:00+08:00", type: "DEPOSIT_OUT", amount: "50000.00", currency: "CNY", referenceNo: "REFUND-202608-0006", note: "合作结束退回押金" }], notes: ["历史账户仅供查询。"] },
  { customerId: "customer-fin", customerCode: "CUS-FIN-006", customerName: "青禾金融科技", levelCode: "GOLD", status: "NORMAL", currency: "CNY", creditLimit: "3000000.00", usedCredit: "980000.00", availableCredit: "2020000.00", depositBalance: "500000.00", paymentTerms: "月结 30 天", updatedAt: "2026-09-06T12:25:00+08:00", billingEntity: "南京青禾金融科技有限公司", invoiceTitle: "南京青禾金融科技有限公司", taxNoMasked: "9132**********1F", settlementCycle: "每月 1 日出账，30 日内结清", warningThresholdRate: "75.00", recentTransactions: [{ id: "txn-fin-1", occurredAt: "2026-09-05T20:10:00+08:00", type: "CREDIT_RELEASE", amount: "240000.00", currency: "CNY", referenceNo: "PAY-202609-0028", note: "回款释放授信" }], notes: [] },
];

const copy = <T>(value: T): T => structuredClone(value);
const accountSummary = ({ billingEntity: _billingEntity, invoiceTitle: _invoiceTitle, taxNoMasked: _taxNoMasked, settlementCycle: _settlementCycle, warningThresholdRate: _warningThresholdRate, recentTransactions: _recentTransactions, notes: _notes, ...row }: FinanceAccountDetailDTO): FinanceAccountSummaryDTO => row;
const result = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: `mock-${crypto.randomUUID()}` });
const failure = (error: unknown) => { const candidate = error as { httpStatus?: number; status?: number; message?: string }; const status = candidate.httpStatus || candidate.status || 500; return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: `mock-${crypto.randomUUID()}` }, { status }); };
const ensureReadable = (request: Request) => { const identity = request.headers.get("X-Mock-Identity") || "VIEWER"; if (!readableIdentities.has(identity)) throw new ApiError(403, "当前身份无权查看财务信息"); };

export const financeHandlers = [
  http.get(`${basePath}/fx-rates`, ({ request }) => {
    try {
      ensureReadable(request); const url = new URL(request.url); const search = (url.searchParams.get("search") || "").trim().toLowerCase(); const month = url.searchParams.get("month") || ""; const status = url.searchParams.get("status") || ""; const page = Math.max(1, Number(url.searchParams.get("page")) || 1); const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const matched = fxRates.filter((rate) => (!search || `${rate.baseCurrency} ${rate.quoteCurrency} ${rate.source}`.toLowerCase().includes(search)) && (!month || rate.month === month) && (!status || rate.status === status)); const offset = (page - 1) * size;
      return result({ list: copy(matched.slice(offset, offset + size)), total: matched.length, page, size });
    } catch (error) { return failure(error); }
  }),
  http.get(`${basePath}/accounts`, ({ request }) => {
    try {
      ensureReadable(request); const url = new URL(request.url); const search = (url.searchParams.get("search") || "").trim().toLowerCase(); const status = url.searchParams.get("status") || ""; const page = Math.max(1, Number(url.searchParams.get("page")) || 1); const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const matched = accounts.filter((account) => (!search || `${account.customerCode} ${account.customerName}`.toLowerCase().includes(search)) && (!status || account.status === status)); const offset = (page - 1) * size;
      return result({ list: matched.slice(offset, offset + size).map((item) => accountSummary(copy(item))), total: matched.length, page, size });
    } catch (error) { return failure(error); }
  }),
  http.get(`${basePath}/accounts/:customerId`, ({ request, params }) => {
    try { ensureReadable(request); const account = accounts.find((item) => item.customerId === String(params.customerId)); if (!account) throw new ApiError(404, "客户财务账户不存在或当前账号不可见"); return result(copy(account)); }
    catch (error) { return failure(error); }
  }),
];
