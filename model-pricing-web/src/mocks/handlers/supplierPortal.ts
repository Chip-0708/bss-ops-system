import { quoteOptions } from "../data/supplierQuoteOptions";
import { quoteContractHandlers } from "./quoteContract";
import { http, HttpResponse } from "msw";
import Decimal from "decimal.js";
import type {
  CreateSupplierPortalQuoteRequest,
  SupplierPortalQuoteDetailDTO,
  SupplierPortalQuoteSummaryDTO,
  SupplierQuoteImportPreviewDTO,
  SupplierQuoteOptionDTO,
} from "../../api/supplierPortal.types";
import type { SupplierQuoteDetailDTO } from "../../api/supplierQuotes.types";
import { ApiError } from "../../domain/common";
import { supplierQuoteRecords } from "../data/supplierQuotes";
import { supplierQuoteRenewalMetadata } from "../data/supplierQuoteRenewal";
import { validateSupplierQuoteConstraints } from "../data/supplierQuoteConstraints";

const basePath = "/api/supplier";
const QuoteDecimal = Decimal.clone({ precision: 64 });
const supplierId = "supplier-cloud";
const supplierName = "云桥科技";
const quotes = supplierQuoteRecords;
const idempotencyResults = new Map<string, { signature: string; value: unknown }>();
const importBatches = new Map<string, SupplierQuoteImportPreviewDTO>();
const importHeaders = ["skuCode", "skuName", "component", "currency", "unit", "taxMode", "unitPrice", "effectiveFrom", "effectiveTo"];

const copy = <T>(value: T): T => structuredClone(value);
const requestId = () => `mock-${crypto.randomUUID()}`;
const result = (data: unknown) =>
  HttpResponse.json({ code: 0, message: "ok", data, requestId: requestId() });

function failure(error: unknown) {
  const candidate = error as { httpStatus?: number; status?: number; message?: string };
  const status = candidate.httpStatus || candidate.status || 500;
  return HttpResponse.json(
    {
      code: status === 409 ? "STATE_CONFLICT" : `HTTP_${status}`,
      message: candidate.message || "Mock 服务异常",
      requestId: requestId(),
    },
    { status },
  );
}

function ensureSupplier(request: Request) {
  if ((request.headers.get("X-Mock-Identity") || "") !== "SUPPLIER") {
    throw new ApiError(403, "当前身份无权访问供应商门户数据");
  }
}

const ownQuotes = () => quotes.filter((quote) => quote.supplierId === supplierId);
const optionKey = (item: { skuCode: string; component: string }) => `${item.skuCode}:${item.component}`;

function portalDetail(quote: SupplierQuoteDetailDTO): SupplierPortalQuoteDetailDTO {
  return {
    id: quote.id,
    previousQuoteId: quote.previousQuoteId,
    supplyConstraints: quote.supplyConstraints,
    quoteNo: quote.quoteNo,
    version: quote.version,
    status: quote.status,
    skuCount: quote.items.length,
    effectiveFrom: quote.effectiveFrom,
    effectiveTo: quote.effectiveTo,
    submittedAt: quote.submittedAt,
    updatedAt: quote.updatedAt,
    rejectionReason: quote.rejectionReason,
    approvalComment: quote.approvalComment,
    remark: quote.alert,
    items: quote.items.map((item) => ({ id: item.id, pricingMode: item.pricingMode, multiplier: item.multiplier, basisVersion: item.basisVersion, skuCode: item.skuCode, skuName: item.skuName, component: item.component, currency: item.currency, unit: item.unit, taxMode: item.taxMode, unitPrice: item.proposedPrice })),
  };
}

function summary(quote: SupplierQuoteDetailDTO): SupplierPortalQuoteSummaryDTO {
  return {
    id: quote.id,
    quoteNo: quote.quoteNo,
    version: quote.version,
    status: quote.status,
    skuCount: quote.items.length,
    effectiveFrom: quote.effectiveFrom,
    effectiveTo: quote.effectiveTo,
    submittedAt: quote.submittedAt,
    updatedAt: quote.updatedAt,
    rejectionReason: quote.rejectionReason,
  };
}

function requireIdempotency(request: Request, signature: string) {
  const key = request.headers.get("Idempotency-Key");
  if (!key) throw new ApiError(400, "缺少 Idempotency-Key");
  const cached = idempotencyResults.get(key);
  if (cached && cached.signature !== signature) {
    throw new ApiError(409, "幂等键已用于其他操作");
  }
  return { key, cached };
}

function validateCreate(body: CreateSupplierPortalQuoteRequest) {
  if (!body || typeof body !== "object") throw new ApiError(400, "报价资料格式不正确");
  validateSupplierQuoteConstraints(body.supplyConstraints);
  if (!body.effectiveFrom) throw new ApiError(400, "请选择计划生效时间");
  if (Number.isNaN(Date.parse(body.effectiveFrom))) throw new ApiError(400, "生效时间格式不正确");
  if (body.effectiveTo && (Number.isNaN(Date.parse(body.effectiveTo)) || Date.parse(body.effectiveTo) <= Date.parse(body.effectiveFrom))) throw new ApiError(400, "结束时间必须晚于生效时间");
  if (!Array.isArray(body.items) || !body.items.length) throw new ApiError(400, "请至少填写一个 SKU 报价");
  if (body.items.some(item => !item || typeof item !== "object" || typeof item.unitPrice !== "string")) throw new ApiError(400, "价格项必须包含十进制字符串金额");
  const keys = body.items.map(optionKey);
  if (new Set(keys).size !== keys.length) throw new ApiError(400, "同一 SKU 组件不能重复报价");
  for (const item of body.items) {
    const option = quoteOptions.find((candidate) => candidate.skuCode === item.skuCode && candidate.component === item.component);
    if (!option || option.skuName !== item.skuName || option.currency !== item.currency || option.unit !== item.unit || option.taxMode !== item.taxMode) throw new ApiError(400, "报价项与当前可报价 SKU 清单不一致，请刷新后重新选择");
    if (!/^\d+(\.\d{1,8})?$/.test(item.unitPrice.trim()) || /^0*(\.0+)?$/.test(item.unitPrice.trim())) throw new ApiError(400, "报价必须是大于 0 且最多 8 位小数的十进制字符串");
    if (item.pricingMode !== undefined && item.pricingMode !== "ABSOLUTE" && item.pricingMode !== "MULTIPLIER") throw new ApiError(400, "不支持的报价模式");
    if (item.pricingMode === "MULTIPLIER") {
      if (item.basisVersion !== option.basisVersion) throw new ApiError(409, "官方价基准已变化，请刷新后重新核对倍率");
      if (typeof item.multiplier !== "string" || !/^\d{1,20}(\.\d{1,8})?$/.test(item.multiplier) || !new QuoteDecimal(item.multiplier).isPositive()) throw new ApiError(400, "倍率必须是正十进制字符串，最多20位整数和8位小数");
      const expected = new QuoteDecimal(option.officialPrice).times(item.multiplier);
      if (expected.decimalPlaces() > 8) throw new ApiError(400, "换算价格超过8位小数，请减少倍率精度；正式舍入规则待确认");
      if (!expected.equals(item.unitPrice)) throw new ApiError(400, "倍率与供应价不一致，请重新换算");
    }
  }
}

function createRecord(
  quoteNo: string,
  source: "PORTAL" | "IMPORT",
  body: CreateSupplierPortalQuoteRequest,
): SupplierQuoteDetailDTO {
  const now = new Date().toISOString();
  return {
    id: `supplier-quote-${crypto.randomUUID()}`,
    quoteNo,
    version: 1,
    supplierId,
    supplierName,
    supplierCode: "SUP-CLOUD-001",
    supplierContact: "李经理 · 139****3186",
    source,
    effectiveFrom: body.effectiveFrom,
    effectiveTo: body.effectiveTo,
    skuCount: body.items.length,
    status: "DRAFT",
    ownerName: supplierName,
    updatedAt: now,
    alert: body.remark?.trim() || undefined,
    items: body.items.map((item) => {
      const option = quoteOptions.find((candidate) => candidate.skuCode === item.skuCode && candidate.component === item.component);
      return {
        id: crypto.randomUUID(),
        skuCode: item.skuCode,
        skuName: item.skuName,
        component: item.component,
        currency: item.currency,
        unit: item.unit,
        taxMode: item.taxMode,
        proposedPrice: item.unitPrice.trim(),
        pricingMode: item.pricingMode || "ABSOLUTE",
        multiplier: item.pricingMode === "MULTIPLIER" ? item.multiplier : undefined,
        basisVersion: item.pricingMode === "MULTIPLIER" ? item.basisVersion : undefined,
        previousPrice: option?.lastUnitPrice,
        officialPrice: option?.officialPrice,
      };
    }),
    supplyConstraints: body.supplyConstraints ? copy(body.supplyConstraints) : undefined,
    constraints: Object.entries(body.supplyConstraints || {}).filter(([, value]) => value !== undefined).map(([key, value]) => ({ label: ({ maxConcurrency: "最大并发", rpm: "RPM", tpm: "TPM", actualContextWindow: "实际上下文", compatibilityNote: "兼容说明" } as Record<string, string>)[key], proposed: value })),
    marginPreview: [],
    warnings: [],
    comparisonGeneratedAt: now,
    comparisonReady: false,
    canApprove: false,
  };
}

function parseImport(fileName: string, content: string): SupplierQuoteImportPreviewDTO {
  const lines = content.replace(/^\uFEFF/, "").split(/\r?\n/).filter((line) => line.trim());
  if (lines.length < 2) throw new ApiError(400, "导入文件没有可校验的数据行");
  const headers = lines[0].split(",").map((value) => value.trim());
  if (headers.join(",") !== importHeaders.join(",")) {
    throw new ApiError(400, "CSV 列名或顺序与最新模板不一致，请重新下载模板");
  }
  const rows = lines.slice(1).map((line, index) => {
    const values = line.split(",").map((value) => value.trim());
    const [skuCode = "", skuName = "", component = "", currency = "", unit = "", taxMode = "", unitPrice = "", effectiveFrom = "", effectiveTo = ""] = values;
    const errors: string[] = [];
    if (values.length !== importHeaders.length) errors.push("列数与模板不一致");
    if (!skuCode || !skuName || !component) errors.push("SKU 编码、名称和组件必填");
    if (!/^(USD|CNY)$/.test(currency)) errors.push("币种仅支持 USD 或 CNY");
    if (!unit || !taxMode) errors.push("计价单位和税费口径必填");
    if (!/^\d+(\.\d{1,8})?$/.test(unitPrice) || /^0*(\.0+)?$/.test(unitPrice)) errors.push("供应价必须大于 0 且最多 8 位小数");
    if (!effectiveFrom || Number.isNaN(Date.parse(effectiveFrom))) errors.push("生效时间不是有效 ISO 8601 时间");
    if (effectiveTo && Number.isNaN(Date.parse(effectiveTo))) errors.push("结束时间不是有效 ISO 8601 时间");
    if (effectiveFrom && effectiveTo && !Number.isNaN(Date.parse(effectiveFrom)) && !Number.isNaN(Date.parse(effectiveTo)) && Date.parse(effectiveTo) <= Date.parse(effectiveFrom)) errors.push("结束时间必须晚于生效时间");
    const option = quoteOptions.find((candidate) => candidate.skuCode === skuCode && candidate.component === component);
    if (!option || option.skuName !== skuName || option.currency !== currency || option.unit !== unit || option.taxMode !== taxMode) errors.push("与当前可报价 SKU 清单不一致");
    return { rowNumber: index + 2, skuCode, skuName, component, currency, unit, taxMode, unitPrice, effectiveFrom, effectiveTo: effectiveTo || undefined, errors };
  });
  const seenKeys = new Set<string>();
  for (const row of rows) {
    const key = optionKey(row);
    if (seenKeys.has(key)) row.errors.push("同一 SKU 组件重复");
    seenKeys.add(key);
  }
  const batchId = `import-${crypto.randomUUID()}`;
  const invalidRows = rows.filter((row) => row.errors.length).length;
  return { batchId, fileName, totalRows: rows.length, validRows: rows.length - invalidRows, invalidRows, rows };
}

// Legacy routes are retained only for historical Mock linkage tests.
export const legacySupplierPortalHandlers = [
  http.post(`${basePath}/quotes/:id/renew`, async ({ request, params }) => {
    try {
      ensureSupplier(request);
      const body = await request.json() as CreateSupplierPortalQuoteRequest;
      const signature = JSON.stringify([String(params.id), "renew", body]);
      const { key, cached } = requireIdempotency(request, signature);
      if (cached) return result(copy(cached.value));
      validateCreate(body);
      const metadata = supplierQuoteRenewalMetadata(quotes, supplierId, String(params.id));
      const quote = createRecord(metadata.quoteNo, "PORTAL", body);
      quote.version = metadata.version; quote.previousQuoteId = metadata.previousQuoteId;
      for (const constraint of quote.constraints) constraint.previous = metadata.source.constraints.find(previous => previous.label === constraint.label)?.proposed;
      for (const item of quote.items) item.previousPrice = metadata.source.items.find(previous => previous.skuCode === item.skuCode && previous.component === item.component)?.proposedPrice;
      quotes.unshift(quote);
      const response = portalDetail(quote); idempotencyResults.set(key, { signature, value: copy(response) });
      return result(response);
    } catch (error) { return failure(error); }
  }),
  http.get(`${basePath}/quote-options`, ({ request }) => {
    try {
      ensureSupplier(request);
      return result(copy(quoteOptions));
    } catch (error) {
      return failure(error);
    }
  }),
  http.get(`${basePath}/quotes`, ({ request }) => {
    try {
      ensureSupplier(request);
      const url = new URL(request.url);
      const search = (url.searchParams.get("search") || "").trim().toLowerCase();
      const status = url.searchParams.get("status") || "";
      const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
      const size = Math.min(100, Math.max(1, Number(url.searchParams.get("size")) || 10));
      const matched = ownQuotes().filter(
        (quote) => (!search || quote.quoteNo.toLowerCase().includes(search)) && (!status || quote.status === status),
      );
      const offset = (page - 1) * size;
      return result({ list: matched.slice(offset, offset + size).map(summary), total: matched.length, page, size });
    } catch (error) {
      return failure(error);
    }
  }),
  http.post(`${basePath}/quotes`, async ({ request }) => {
    try {
      ensureSupplier(request);
      const body = (await request.json()) as CreateSupplierPortalQuoteRequest;
      validateCreate(body);
      const signature = JSON.stringify(body);
      const { key, cached } = requireIdempotency(request, signature);
      if (cached) return result(copy(cached.value));
      const sequence = String(quotes.length + 1).padStart(3, "0");
      const quote = createRecord(`SQ-DRAFT-${sequence}`, "PORTAL", body);
      quotes.unshift(quote);
      const response = portalDetail(quote);
      idempotencyResults.set(key, { signature, value: copy(response) });
      return result(copy(response));
    } catch (error) {
      return failure(error);
    }
  }),
  http.get(`${basePath}/quotes/import/template`, ({ request }) => {
    try {
      ensureSupplier(request);
      const url = new URL(request.url); const scope = url.searchParams.get("scope") || "HISTORY"; const search = (url.searchParams.get("search") || "").trim().toLowerCase();
      if (scope !== "HISTORY" && scope !== "ALL") throw new ApiError(400, "不支持的模板范围");
      const history = new Set(ownQuotes().flatMap(quote => quote.items.map(optionKey)));
      const options = quoteOptions.filter(option => (scope === "ALL" || history.has(optionKey(option))) && `${option.skuCode} ${option.skuName}`.toLowerCase().includes(search));
      if (!options.length) throw new ApiError(400, "筛选范围内没有可报价SKU，请调整条件");
      const defaultStart = new Date(Date.now() + 86_400_000).toISOString();
      return result({
        fileName: "supplier-quote-template.csv",
        content: `${importHeaders.join(",")}\n${options.map(option => [option.skuCode, option.skuName, option.component, option.currency, option.unit, option.taxMode, option.lastUnitPrice || "", defaultStart, ""].join(",")).join("\n")}\n`,
      });
    } catch (error) {
      return failure(error);
    }
  }),
  http.post(`${basePath}/quotes/import/validate`, async ({ request }) => {
    try {
      ensureSupplier(request);
      const body = (await request.json()) as { fileName?: string; content?: string };
      if (!body.fileName?.toLowerCase().endsWith(".csv")) throw new ApiError(400, "仅支持 CSV 报价文件");
      if (!body.content || body.content.length > 1_000_000) throw new ApiError(400, "文件内容为空或超过 1 MB");
      const preview = parseImport(body.fileName, body.content);
      importBatches.set(preview.batchId, copy(preview));
      return result(preview);
    } catch (error) {
      return failure(error);
    }
  }),
  http.post(`${basePath}/quotes/import/commit`, async ({ request }) => {
    try {
      ensureSupplier(request);
      const body = (await request.json()) as { batchId?: string };
      const signature = JSON.stringify([body.batchId, "commit"]);
      const { key, cached } = requireIdempotency(request, signature);
      if (cached) return result(copy(cached.value));
      const preview = body.batchId ? importBatches.get(body.batchId) : undefined;
      if (!preview) throw new ApiError(404, "导入预检批次不存在或已失效");
      if (preview.invalidRows) throw new ApiError(409, "仍有未通过校验的数据行，不能确认导入");
      const sequence = String(quotes.length + 1).padStart(3, "0");
      const firstRow = preview.rows[0];
      if (!firstRow) throw new ApiError(409, "导入批次没有可用数据");
      const payload: CreateSupplierPortalQuoteRequest = { effectiveFrom: firstRow.effectiveFrom, effectiveTo: firstRow.effectiveTo, remark: `由 ${preview.fileName} 导入，待核对后提交。`, items: preview.rows.map((row) => ({ skuCode: row.skuCode, skuName: row.skuName, component: row.component, currency: row.currency, unit: row.unit, taxMode: row.taxMode, unitPrice: row.unitPrice })) };
      validateCreate(payload);
      const quote = createRecord(`SQ-IMPORT-${sequence}`, "IMPORT", payload);
      quotes.unshift(quote);
      const importResult = { batchId: preview.batchId, quoteId: quote.id, quoteNo: quote.quoteNo, status: "DRAFT" as const, importedRows: preview.rows.length };
      idempotencyResults.set(key, { signature, value: copy(importResult) });
      importBatches.delete(preview.batchId);
      return result(importResult);
    } catch (error) {
      return failure(error);
    }
  }),
  http.get(`${basePath}/quotes/:id`, ({ request, params }) => {
    try {
      ensureSupplier(request);
      const quote = quotes.find((item) => item.id === String(params.id) && item.supplierId === supplierId);
      if (!quote) throw new ApiError(404, "报价不存在或不属于当前供应商");
      return result(copy(portalDetail(quote)));
    } catch (error) {
      return failure(error);
    }
  }),
  http.put(`${basePath}/quotes/:id`, async ({ request, params }) => {
    try {
      ensureSupplier(request);
      const quote = quotes.find((item) => item.id === String(params.id) && item.supplierId === supplierId);
      if (!quote) throw new ApiError(404, "报价不存在或不属于当前供应商");
      const body = (await request.json()) as CreateSupplierPortalQuoteRequest;
      const signature = JSON.stringify([quote.id, "update", body]);
      const { key, cached } = requireIdempotency(request, signature);
      if (cached) return result(copy(cached.value));
      if (quote.status !== "DRAFT") throw new ApiError(409, "报价已提交，不能修改；请刷新后查看最新状态");
      validateCreate(body);
      const revised = createRecord(quote.quoteNo, quote.source === "IMPORT" ? "IMPORT" : "PORTAL", body);
      quote.effectiveFrom = revised.effectiveFrom;
      quote.effectiveTo = revised.effectiveTo;
      quote.alert = revised.alert;
      quote.items = revised.items;
      quote.constraints = revised.constraints;
      quote.supplyConstraints = revised.supplyConstraints;
      if (quote.previousQuoteId) {
        const previous = quotes.find(row => row.id === quote.previousQuoteId && row.supplierId === supplierId);
        for (const item of quote.items) item.previousPrice = previous?.items.find(row => row.skuCode === item.skuCode && row.component === item.component)?.proposedPrice;
      }
      quote.skuCount = revised.skuCount;
      quote.updatedAt = revised.updatedAt;
      const response = portalDetail(quote);
      idempotencyResults.set(key, { signature, value: copy(response) });
      return result(copy(response));
    } catch (error) {
      return failure(error);
    }
  }),
  http.post(`${basePath}/quotes/:id/submit`, ({ request, params }) => {
    try {
      ensureSupplier(request);
      const quote = quotes.find((item) => item.id === String(params.id) && item.supplierId === supplierId);
      if (!quote) throw new ApiError(404, "报价不存在或不属于当前供应商");
      const signature = JSON.stringify([quote.id, "submit"]);
      const { key, cached } = requireIdempotency(request, signature);
      if (cached) return result(copy(cached.value));
      if (quote.status !== "DRAFT") throw new ApiError(409, "当前报价已提交，请刷新后查看最新状态");
      validateCreate({ effectiveFrom: quote.effectiveFrom, effectiveTo: quote.effectiveTo, supplyConstraints: quote.supplyConstraints, items: portalDetail(quote).items });
      const submittedAt = new Date().toISOString();
      if (quote.effectiveTo && Date.parse(quote.effectiveTo) <= Date.parse(submittedAt)) throw new ApiError(409, "报价结束时间已过，请编辑草稿后重新提交");
      if (Date.parse(quote.effectiveFrom) < Date.parse(submittedAt)) { quote.effectiveFrom = submittedAt; quote.warnings.push("普通提交不允许倒填生效时间，已调整为提交时刻；特权补录另走流程。"); }
      quote.status = "SUBMITTED";
      quote.submittedAt = submittedAt;
      quote.updatedAt = quote.submittedAt;
      // Mock backend synchronously initializes the approval workflow after accepting submission.
      quote.status = "APPROVING";
      quote.comparisonReady = true;
      quote.comparisonGeneratedAt = quote.submittedAt;
      const action = { id: quote.id, status: quote.status, submittedAt: quote.submittedAt };
      idempotencyResults.set(key, { signature, value: copy(action) });
      return result(action);
    } catch (error) {
      return failure(error);
    }
  }),
];

export const supplierPortalHandlers = quoteContractHandlers;
