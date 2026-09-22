import { quoteOptions } from "../data/supplierQuoteOptions";
import { http, HttpResponse } from "msw";
import Decimal from "decimal.js";
import { mock } from "../../mock";
import type { Model } from "../../types";
import { modelSku } from "../data/modelCatalog";
import { supplierQuoteRecords, createSupplierQuoteSeeds } from "../data/supplierQuotes";
import type { SupplierQuoteDetailDTO } from "../../api/supplierQuotes.types";
import { ApiError } from "../../domain/common";
import { isDecimalAmount } from "../../domain/money";
import { QUOTE_COMPONENTS, QUOTE_FX_TIERS } from "../../api/quoteContract.types";
import type { QuoteComponentType, QuoteDetailDTO, QuoteDiffDTO, QuoteHistoryDTO, QuoteImportPreviewDTO, QuoteSubmitRequest, QuoteSubmitResult, QuoteWriteItem, SupplierSkuDTO } from "../../api/quoteContract.types";
const D = Decimal.clone({ precision: 64 });
const ownSupplier = "supplier-cloud";
const supplierIds: Record<string, number> = { "supplier-cloud": 1, "supplier-star": 2, "supplier-north": 3 };
const keys = new Map<string, {
    signature: string;
    value: QuoteSubmitResult;
}>();
const metadata = new Map<string, Array<Pick<QuoteWriteItem, "sku_id" | "fx_tier" | "constraints">>>();
const copy = <T>(value: T): T => structuredClone(value);
const result = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: crypto.randomUUID() });
const fail = (error: unknown) => { const value = error as {
    httpStatus?: number;
    message?: string;
}; return HttpResponse.json({ code: value.httpStatus || 500, message: value.message || "Mock失败" }, { status: value.httpStatus || 500 }); };
function supplier(request: Request) { if (request.headers.get("X-Mock-Identity") !== "SUPPLIER")
    throw new ApiError(403, "供应商身份必需"); }
function internal(request: Request) { if (!["PURCHASING", "PRICING_OP", "VIEWER"].includes(request.headers.get("X-Mock-Identity") || ""))
    throw new ApiError(403, "无报价读取权限"); }
function models() { return mock.handle({ path: "/api/internal/models", method: "GET", role: "MODEL_OPS" }) as Model[]; }
export function quoteWireId(quote: SupplierQuoteDetailDTO) {
    const id = /^q-\d+$/.test(quote.id) ? quote.id.slice(2) : quote.id;
    return /^[1-9]\d*$/.test(id) && Number.isSafeInteger(Number(id)) ? Number(id) : id;
}
export function findWireQuote(id: string) { return supplierQuoteRecords.find(quote => String(quoteWireId(quote)) === id || quote.id === id); }
function catalog(): SupplierSkuDTO[] {
    return models().filter(model => ["PENDING_VERIFY", "PURCHASABLE", "PUBLISHED"].includes(model.status)).map(model => {
        const sku = modelSku(model);
        const basis = quoteOptions.filter(item => String(item.skuId) === String(sku.id));
        return { id: sku.id, sku_code: sku.sku_code, model_name: `${sku.family_name} · ${sku.sku_code}`,
            vendor_id: sku.vendor_id, vendor_name: sku.vendor_name, family_id: sku.family_id, family_name: sku.family_name,
            model_type: sku.model_type, native_currency: sku.native_currency, context_window: sku.context_window, tier_tag: sku.tier_tag,
            has_official_price: basis.length > 0, official_price: basis.length ? { version_no: 1, currency: sku.native_currency, tax_basis: "NET",
                components: basis.map(item => ({ component_type: componentType(item.component), unit_price: item.officialPrice! })) } : null };
    });
}
function componentType(label: string): QuoteComponentType { return label === "输入 Token" ? "input" : label === "输出 Token" ? "output" : label as QuoteComponentType; }
function wireItems(quote: SupplierQuoteDetailDTO): QuoteDetailDTO["items"] {
    const modelList = models();
    const grouped = new Map<string, QuoteDetailDTO["items"][number]>();
    for (const row of quote.items) {
        const model = modelList.find(model => row.skuId !== undefined ? model.id === String(row.skuId) : model.code === row.skuCode);
        if (!model)
            continue;
        let item = grouped.get(model.id);
        if (!item) {
            const saved = metadata.get(quote.id)?.find(item => String(item.sku_id) === model.id);
            item = { sku_id: model.id, sku_code: model.code, model_name: `${model.family} · ${model.code}`, currency: model.currency,
                fx_tier: saved?.fx_tier ?? (model.currency === "USD" ? "6.8" : null), constraints: saved?.constraints || null, components: [] };
            grouped.set(model.id, item);
        }
        item.components.push({ component_type: componentType(row.component), multiplier: row.pricingMode === "MULTIPLIER" ? row.multiplier || null : null, unit_price: row.proposedPrice });
    }
    return [...grouped.values()];
}
function history(quote: SupplierQuoteDetailDTO): QuoteHistoryDTO {
    return { id: quoteWireId(quote), version_no: quote.version, status: quote.status, valid_from: quote.effectiveFrom, valid_to: quote.effectiveTo!,
        source: quote.source === "PORTAL" ? "MANUAL" : quote.source, item_count: wireItems(quote).length, submitted_at: quote.submittedAt || null,
        decision: quote.status === "REJECTED" ? { result: "REJECTED", reason: quote.rejectionReason || null, decided_at: quote.updatedAt } :
            ["APPROVED_PENDING", "EFFECTIVE", "EXPIRED"].includes(quote.status) ? { result: "APPROVED", reason: null, decided_at: quote.updatedAt } : null };
}
function pagination(url: URL) {
    const page = Number(url.searchParams.get("page") || 1), size = Number(url.searchParams.get("size") || 20);
    if (!Number.isInteger(page) || page < 1 || !Number.isInteger(size) || size < 1 || size > 100)
        throw new ApiError(400, "分页参数错误");
    return { page, size };
}
function pageResult<T>(list: T[], url: URL) { const { page, size } = pagination(url); return { list: list.slice((page - 1) * size, page * size), total: list.length, page, size }; }
function object(value: unknown, allowed: string[]) {
    if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some(key => !allowed.includes(key)))
        throw new ApiError(400, "请求含未知字段或结构错误");
}
function validateItems(items: QuoteWriteItem[]) {
    if (!Array.isArray(items) || !items.length || items.length > 500)
        throw new ApiError(400, "报价需1—500个SKU");
    const available = catalog(), seen = new Set<string>();
    for (const item of items) {
        object(item, ["sku_id", "fx_tier", "constraints", "components"]);
        const sku = available.find(sku => String(sku.id) === String(item.sku_id));
        if (!sku || seen.has(String(item.sku_id)))
            throw new ApiError(400, "SKU不可报价或重复");
        seen.add(String(item.sku_id));
        if (sku.native_currency === "USD") {
            if (typeof item.fx_tier !== "string" || !isDecimalAmount(item.fx_tier) || !QUOTE_FX_TIERS.some(tier => new D(tier).eq(item.fx_tier!)))
                throw new ApiError(400, "USD汇率档位错误");
            item.fx_tier = QUOTE_FX_TIERS.find(tier => new D(tier).eq(item.fx_tier!))!;
        }
        else
            item.fx_tier = null;
        if (item.constraints != null) {
            object(item.constraints, ["rpm", "tpm", "concurrency", "daily_quota", "actual_context", "compatibility"]);
            for (const [key, value] of Object.entries(item.constraints)) {
                if (value === null)
                    continue;
                if (key === "compatibility" ? typeof value !== "string" : !Number.isSafeInteger(value) || Number(value) <= 0)
                    throw new ApiError(400, "约束字段类型错误");
            }
        }
        if (!Array.isArray(item.components) || !item.components.length)
            throw new ApiError(400, "组件不能为空");
        const types = new Set<string>();
        for (const component of item.components) {
            object(component, ["component_type", "multiplier", "unit_price"]);
            if (!QUOTE_COMPONENTS.includes(component.component_type) || types.has(component.component_type) || !isDecimalAmount(component.unit_price))
                throw new ApiError(400, "组件类型、重复或价格错误");
            types.add(component.component_type);
            if (component.multiplier !== null) {
                const official = sku.official_price?.components.find(row => row.component_type === component.component_type)?.unit_price;
                if (!official || !isDecimalAmount(component.multiplier) || new D(component.unit_price).minus(new D(official).times(component.multiplier)).abs().gt("0.0001"))
                    throw new ApiError(400, "倍率与当前官方价不自洽");
            }
        }
    }
}
async function submit(request: Request, source: "MANUAL" | "IMPORT") {
    supplier(request);
    const body = await request.json() as QuoteSubmitRequest;
    object(body, ["valid_from", "valid_to", "remark", "items"]);
    const key = request.headers.get("Idempotency-Key");
    if (!key)
        throw new ApiError(400, "缺少幂等键");
    const signature = JSON.stringify([ownSupplier, source, body]), cached = keys.get(key);
    if (cached) {
        if (cached.signature !== signature)
            throw new ApiError(400, "同一幂等键请求体不同");
        return result(copy(cached.value));
    }
    validateItems(body.items);
    const zoned = /(?:Z|[+-]\d{2}:\d{2})$/;
    if (typeof body.valid_from !== "string" || typeof body.valid_to !== "string" || ![body.valid_from, body.valid_to].every(value => zoned.test(value) && Number.isFinite(Date.parse(value))))
        throw new ApiError(400, "有效期必须带时区");
    if (body.remark !== undefined && (typeof body.remark !== "string" || Array.from(body.remark).length > 500))
        throw new ApiError(400, "备注最多500字");
    const now = new Date().toISOString(), clamped = Date.parse(body.valid_from) < Date.parse(now), start = clamped ? now : body.valid_from;
    if (Date.parse(body.valid_to) <= Date.parse(start))
        throw new ApiError(400, "有效期结束须晚于生效时间");
    const own = supplierQuoteRecords.filter(quote => quote.supplierId === ownSupplier);
    if (own.some(quote => ["SUBMITTED", "APPROVING", "APPROVED_PENDING"].includes(quote.status)))
        throw new ApiError(409, "已有待审批或待生效版本，请先处理");
    const id = String(Math.max(6000, ...supplierQuoteRecords.map(quote => Number(quoteWireId(quote)) || 0)) + 1);
    const version = Math.max(0, ...own.map(quote => quote.version)) + 1, available = catalog();
    const quote: SupplierQuoteDetailDTO = { ...copy(createSupplierQuoteSeeds()[0]), id, quoteNo: `MOCK-${id}`, version, source: source === "MANUAL" ? "PORTAL" : "IMPORT",
        status: "APPROVING", effectiveFrom: start, effectiveTo: body.valid_to, submittedAt: now, updatedAt: now, alert: body.remark,
        items: body.items.flatMap(item => {
            const sku = available.find(sku => String(sku.id) === String(item.sku_id))!;
            return item.components.map(component => ({
                id: `${id}-${item.sku_id}-${component.component_type}`, skuId: item.sku_id, skuCode: sku.sku_code, skuName: sku.model_name, component: component.component_type, currency: sku.native_currency,
                unit: component.component_type === "request" ? "次" : "百万 Token", taxMode: sku.official_price?.tax_basis || "NET", proposedPrice: component.unit_price,
                pricingMode: component.multiplier === null ? "ABSOLUTE" as const : "MULTIPLIER" as const, multiplier: component.multiplier || undefined,
                officialPrice: sku.official_price?.components.find(row => row.component_type === component.component_type)?.unit_price,
            }));
        }), constraints: [], marginPreview: [], warnings: [], comparisonReady: true, canApprove: true };
    quote.skuCount = body.items.length;
    metadata.set(id, body.items.map(({ sku_id, fx_tier, constraints }) => copy({ sku_id, fx_tier, constraints })));
    supplierQuoteRecords.unshift(quote);
    const { decision: _decision, ...header } = history(quote);
    const value: QuoteSubmitResult = { ...header, status: "APPROVING", supplier_id: 1, retroactive: false, clamped };
    keys.set(key, { signature, value });
    return result(copy(value));
}
function diff(quote: SupplierQuoteDetailDTO): QuoteDiffDTO {
    const previous = supplierQuoteRecords.filter(row => row.supplierId === quote.supplierId && row.version < quote.version).sort((a, b) => b.version - a.version)[0];
    const available = catalog();
    const delta = (price: string, basis?: string | null) => basis && new D(basis).gt(0) ? new D(price).minus(basis).div(basis).toFixed(4) : null;
    const items = wireItems(quote), components = items.flatMap(item => item.components);
    const distortion = components.length > 0 && components.every(component => component.multiplier !== null) &&
      new Set(components.map(component => new D(component.multiplier!).toString())).size === 1;
    return { quote_sheet_id: quoteWireId(quote), supplier_id: supplierIds[quote.supplierId], version_no: quote.version,
        distortion, distortion_note: distortion ? "各组件倍率相同，请复核缓存维度" : null,
        items: items.map(item => ({ sku_id: item.sku_id, sku_code: item.sku_code, currency: item.currency,
            components: item.components.map(component => {
                const prev = previous?.items.find(row => (row.skuId !== undefined ? String(row.skuId) === String(item.sku_id) : row.skuCode === item.sku_code) && componentType(row.component) === component.component_type)?.proposedPrice || null;
                const official = available.find(sku => String(sku.id) === String(item.sku_id))?.official_price?.components.find(row => row.component_type === component.component_type)?.unit_price || null;
                const market = supplierQuoteRecords.filter(row => row.status === "EFFECTIVE").flatMap(row => row.items).filter(row => (row.skuId !== undefined ? String(row.skuId) === String(item.sku_id) : row.skuCode === item.sku_code) && componentType(row.component) === component.component_type).map(row => row.proposedPrice).sort((a, b) => new D(a).cmp(b))[0] || null;
                return { ...component, prev_price: prev, prev_delta_pct: delta(component.unit_price, prev), official_price: official, official_delta_pct: delta(component.unit_price, official), market_best: market, market_delta_pct: delta(component.unit_price, market) };
            }), margin_preview: { floor_price: null, reference_sell_price: null, margin_ok: null, note: "演示未实现Go成本引擎，不伪造毛利结论" } })) };
}
function parseCsv(content: string): string[][] {
    const rows: string[][] = [], row: string[] = [];
    let value = "", quoted = false;
    for (let i = 0; i < content.length; i++) {
        const char = content[i];
        if (char === '"') {
            if (quoted && content[i + 1] === '"') {
                value += '"';
                i++;
            }
            else
                quoted = !quoted;
        }
        else if (!quoted && (char === ',' || char === '\n')) {
            row.push(value.replace(/\r$/, ""));
            value = "";
            if (char === '\n') {
                if (row.some(cell => cell.trim()))
                    rows.push([...row]);
                row.length = 0;
            }
        }
        else
            value += char;
    }
    if (quoted)
        throw new ApiError(400, "CSV引号未闭合");
    row.push(value.replace(/\r$/, ""));
    if (row.some(cell => cell.trim()))
        rows.push(row);
    return rows;
}
const csvCell = (value: unknown) => '"' + String(value ?? "").replaceAll('"', '""') + '"';
function template(url: URL) {
    const scope = url.searchParams.get("scope") || "active";
    if (!["active", "history", "vendor", "family"].includes(scope) || url.searchParams.get("format") && url.searchParams.get("format") !== "csv")
        throw new ApiError(400, "模板范围或格式错误");
    let available = catalog();
    if (scope === "history") {
        const ids = new Set(supplierQuoteRecords.filter(quote => quote.supplierId === ownSupplier).flatMap(quote => wireItems(quote).map(item => String(item.sku_id))));
        available = available.filter(sku => ids.has(String(sku.id)));
    }
    if (scope === "vendor" || scope === "family") {
        const id = url.searchParams.get(scope + "_id");
        if (!id)
            throw new ApiError(400, "厂商/系列ID必填");
        const matching = new Set(models().filter(model => String(modelSku(model)[scope === "vendor" ? "vendor_id" : "family_id"]) === id).map(model => model.id));
        available = available.filter(sku => matching.has(String(sku.id)));
    }
    const types = [...new Set(available.flatMap(sku => sku.official_price?.components.map(component => component.component_type) || []))].sort();
    const headers = ["sku_id", "sku_code", "model_name", "currency", ...types.map(type => `official_${type}`), "fx_tier", ...types.flatMap(type => [`multiplier_${type}`, `price_${type}`]), "rpm", "tpm", "concurrency", "daily_quota", "actual_context", "compatibility", "last_valid_to"];
    const rows = available.map(sku => {
        const values: Record<string, unknown> = { sku_id: sku.id, sku_code: sku.sku_code, model_name: sku.model_name, currency: sku.native_currency, fx_tier: sku.native_currency === "USD" ? "6.8" : "" };
        for (const type of types)
            values[`official_${type}`] = sku.official_price?.components.find(component => component.component_type === type)?.unit_price || "";
        values.last_valid_to = supplierQuoteRecords.filter(quote => quote.supplierId === ownSupplier && quote.items.some(item => item.skuCode === sku.sku_code)).sort((a, b) => b.version - a.version)[0]?.effectiveTo || "";
        return headers.map(header => csvCell(values[header])).join(",");
    });
    return new HttpResponse("\uFEFF" + [headers.join(","), ...rows].join("\r\n"), { headers: { "Content-Type": "text/csv; charset=utf-8", "Content-Disposition": 'attachment; filename="supplier-quotes.csv"' } });
}
async function preview(request: Request) {
    supplier(request);
    const form = await request.formData(), file = form.get("file");
    if (!file || typeof file === "string" || !file.name.toLowerCase().endsWith(".csv") || file.size > 2 * 1024 * 1024)
        throw new ApiError(400, "请选择2MB以内CSV文件");
    const bytes = await file.arrayBuffer(), content = new TextDecoder().decode(bytes).replace(/^\uFEFF/, ""), csv = parseCsv(content);
    if (csv.length < 2 || csv.length - 1 > 500)
        throw new ApiError(400, "文件必须有1—500行数据");
    const headers = csv[0];
    if (new Set(headers).size !== headers.length || !["sku_id", "sku_code", "currency"].every(key => headers.includes(key)))
        throw new ApiError(400, "CSV列缺失或重复");
    if (headers.some(header => /^(official|multiplier|price)_/.test(header) && !QUOTE_COMPONENTS.includes(header.replace(/^(official|multiplier|price)_/, "") as QuoteComponentType)))
        throw new ApiError(400, "CSV含未知组件列");
    const rows: QuoteImportPreviewDTO["rows"] = [], previewItems: QuoteImportPreviewDTO["preview_items"] = [], seen = new Set<string>();
    const available = catalog();
    for (let index = 1; index < csv.length; index++) {
        const cells = csv[index], row = Object.fromEntries(headers.map((key, i) => [key, cells[i]?.trim() || ""]));
        const sku = available.find(sku => String(sku.id) === row.sku_id), messages: string[] = [];
        let warning = false;
        try {
            if (cells.length !== headers.length)
                throw new ApiError(400, "列数不匹配");
            if (!sku || seen.has(row.sku_id) || sku.sku_code !== row.sku_code || sku.native_currency !== row.currency)
                throw new ApiError(400, "SKU不存在、重复或锁定列被修改");
            seen.add(row.sku_id);
            const components = QUOTE_COMPONENTS.flatMap(type => {
                const multiplier = row[`multiplier_${type}`] || null, price = row[`price_${type}`] || "";
                if (!multiplier && !price)
                    return [];
                const official = sku.official_price?.components.find(component => component.component_type === type)?.unit_price;
                if (multiplier && (!official || !isDecimalAmount(multiplier)))
                    throw new ApiError(400, "倍率缺少官方基准或格式错误");
                const basisChanged = !!multiplier && !!row[`official_${type}`] && !new D(row[`official_${type}`]).eq(official!);
                if (basisChanged) {
                    warning = true;
                    messages.push("官方价基准已变动，已按最新官方价重算");
                }
                return [{ component_type: type, multiplier, unit_price: multiplier && (basisChanged || !price) ? new D(official!).times(multiplier).toFixed(8) : price }];
            });
            const constraints: Record<string, number | string> = {};
            for (const key of ["rpm", "tpm", "concurrency", "daily_quota", "actual_context", "compatibility"])
                if (row[key])
                    constraints[key] = key === "compatibility" ? row[key] : Number(row[key]);
            const item: QuoteWriteItem = { sku_id: sku.id, fx_tier: row.fx_tier || null, constraints, components };
            validateItems([item]);
            previewItems.push({ ...item, currency: sku.native_currency });
        }
        catch (error) {
            messages.push(error instanceof Error ? error.message : "该行错误");
            rows.push({ line: index + 1, sku_id: row.sku_id, sku_code: row.sku_code, level: "ERROR", messages });
            continue;
        }
        rows.push({ line: index + 1, sku_id: sku!.id, sku_code: sku!.sku_code, level: warning ? "WARN" : "OK", messages });
    }
    const token = [...new Uint8Array(await crypto.subtle.digest("SHA-256", bytes))].map(value => value.toString(16).padStart(2, "0")).join("");
    return result({ token, total: rows.length, ok_count: rows.filter(row => row.level === "OK").length, warn_count: rows.filter(row => row.level === "WARN").length, error_count: rows.filter(row => row.level === "ERROR").length, rows, preview_items: previewItems });
}
export const quoteContractHandlers = [
    http.get("/api/supplier/skus", ({ request }) => {
        try {
            supplier(request);
            const url = new URL(request.url), keyword = (url.searchParams.get("keyword") || "").toLowerCase();
            let list = catalog().filter(sku => `${sku.model_name} ${sku.sku_code}`.toLowerCase().includes(keyword));
            for (const key of ["vendor_id", "family_id"] as const)
                if (url.searchParams.get(key)) {
                    const ids = new Set(models().filter(model => String(modelSku(model)[key]) === url.searchParams.get(key)).map(model => model.id));
                    list = list.filter(sku => ids.has(String(sku.id)));
                }
            return result(pageResult(list, url));
        }
        catch (error) {
            return fail(error);
        }
    }),
    http.get("/api/supplier/quotes/history", ({ request }) => {
        try {
            supplier(request);
            const url = new URL(request.url);
            const list = supplierQuoteRecords.filter(quote => quote.supplierId === ownSupplier).filter(quote => !url.searchParams.get("status") || quote.status === url.searchParams.get("status")).filter(quote => !url.searchParams.get("sku_id") || wireItems(quote).some(item => String(item.sku_id) === url.searchParams.get("sku_id"))).filter(quote => !url.searchParams.get("from") || Date.parse(quote.effectiveFrom) >= Date.parse(url.searchParams.get("from")!)).filter(quote => !url.searchParams.get("to") || Date.parse(quote.effectiveFrom) <= Date.parse(url.searchParams.get("to")!));
            return result(pageResult(list.map(history), url));
        }
        catch (error) {
            return fail(error);
        }
    }),
    http.get("/api/supplier/quotes/template", ({ request }) => { try {
        supplier(request);
        return template(new URL(request.url));
    }
    catch (error) {
        return fail(error);
    } }),
    http.post("/api/supplier/quotes/import/preview", async ({ request }) => { try {
        return await preview(request);
    }
    catch (error) {
        return fail(error);
    } }),
    http.post("/api/supplier/quotes/import/confirm", async ({ request }) => { try {
        return await submit(request, "IMPORT");
    }
    catch (error) {
        return fail(error);
    } }),
    http.post("/api/supplier/quotes", async ({ request }) => { try {
        return await submit(request, "MANUAL");
    }
    catch (error) {
        return fail(error);
    } }),
    http.get("/api/supplier/quotes/:id", ({ request, params }) => {
        try {
            supplier(request);
            const quote = findWireQuote(String(params.id));
            if (!quote || quote.supplierId !== ownSupplier)
                throw new ApiError(404, "报价不存在");
            return result({ ...history(quote), supplier_id: 1, audit_reason: quote.alert || null, items: wireItems(quote) });
        }
        catch (error) {
            return fail(error);
        }
    }),
    http.get("/api/internal/quotes/pending", ({ request }) => {
        try {
            internal(request);
            const url = new URL(request.url);
            const list = supplierQuoteRecords.filter(quote => quote.status === "APPROVING" && (!url.searchParams.get("supplier_id") || String(supplierIds[quote.supplierId]) === url.searchParams.get("supplier_id"))).map(quote => ({ id: quoteWireId(quote), supplier_id: supplierIds[quote.supplierId], supplier_name: quote.supplierName, version_no: quote.version, item_count: wireItems(quote).length, source: quote.source === "PORTAL" ? "MANUAL" : quote.source, retroactive: false, valid_from: quote.effectiveFrom, valid_to: quote.effectiveTo, submitted_at: quote.submittedAt, wait_hours: Math.max(0, Math.round((Date.now() - Date.parse(quote.submittedAt || quote.updatedAt)) / 360000) / 10), has_previous: supplierQuoteRecords.some(row => row.supplierId === quote.supplierId && row.version < quote.version) }));
            return result(pageResult(list, url));
        }
        catch (error) {
            return fail(error);
        }
    }),
    http.get("/api/internal/quotes/:id/diff", ({ request, params }) => { try {
        internal(request);
        const quote = findWireQuote(String(params.id));
        if (!quote)
            throw new ApiError(404, "报价不存在");
        return result(diff(quote));
    }
    catch (error) {
        return fail(error);
    } }),
];
