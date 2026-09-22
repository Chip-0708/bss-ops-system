import type { QuoteSubmitRequest } from "../src/api/quoteContract.types";
import { http, HttpResponse } from "msw";
import { createPinia, setActivePinia } from "pinia";
import { supplierQuoteApi } from "../src/api/quoteContract";
import { apiFileRequest } from "../src/api/http";
import { useSessionStore } from "../src/stores/session";
import test from "node:test";
import assert from "node:assert/strict";
import { setupServer } from "msw/node";
import { quoteContractHandlers } from "../src/mocks/handlers/quoteContract";
import { supplierQuoteHandlers } from "../src/mocks/handlers/supplierQuotes";
import { modelHandlers } from "../src/mocks/handlers/models";
import { supplierQuoteRecords } from "../src/mocks/data/supplierQuotes";
import { mock } from "../src/mock";
Object.defineProperty(globalThis, "location", { value: new URL("http://localhost/"), configurable: true });
const server = setupServer(...quoteContractHandlers, ...supplierQuoteHandlers, ...modelHandlers);
async function request(path: string, method = "GET", body?: unknown, identity = "SUPPLIER", key: string = crypto.randomUUID()) {
    const response = await fetch(`http://localhost/api${path}`, { method, headers: { "Content-Type": "application/json", "X-Mock-Identity": identity, "Idempotency-Key": key }, body: body === undefined ? undefined : JSON.stringify(body) });
    return { status: response.status, ...(await response.json()) as {
            data: any;
            message: string;
        } };
}
function clearPending() { for (const quote of supplierQuoteRecords)
    if (quote.supplierId === "supplier-cloud" && ["SUBMITTED", "APPROVING", "APPROVED_PENDING"].includes(quote.status))
        quote.status = "REJECTED"; }
async function catalog() { return (await request("/supplier/skus?page=1&size=100")).data.list as any[]; }
function payload(id: string | number): QuoteSubmitRequest { return { valid_from: "2099-01-01T00:00:00+08:00", valid_to: "2099-04-01T00:00:00+08:00", items: [{ sku_id: id, fx_tier: "6.80", constraints: { rpm: 3000, daily_quota: 10000 }, components: [{ component_type: "input", multiplier: "0.8", unit_price: "0.32" }, { component_type: "output", multiplier: null, unit_price: "7.1" }] }] }; }
test("stage-5 SKU and quote writes preserve IDs, aggregate components, enforce isolation and reject legacy payloads atomically", async () => {
    const backup = structuredClone(supplierQuoteRecords);
    server.listen({ onUnhandledRequest: "error" });
    try {
        const skus = await catalog(), sku = skus.find(sku => sku.sku_code === "gpt-4.1-mini");
        assert.ok(sku);
        assert.equal(typeof sku.id, "string");
        assert.ok(BigInt(sku.id) > BigInt(Number.MAX_SAFE_INTEGER));
        assert.ok(skus.every(sku => !('cost' in sku) && !('margin' in sku)));
        assert.equal((await request("/supplier/skus", "GET", undefined, "VIEWER")).status, 403);
        assert.equal((await request("/supplier/quotes/1002")).status, 404);
        const valid = payload(sku.id), length = supplierQuoteRecords.length;
        assert.equal((await request("/supplier/quotes", "POST", { effectiveFrom: valid.valid_from, items: [] })).status, 400);
        assert.equal((await request("/supplier/quotes", "POST", { ...valid, items: [{ ...valid.items[0], model_name: 'arbitrary' }] })).status, 400);
        assert.equal((await request("/supplier/quotes", "POST", { ...valid, items: [{ ...valid.items[0], currency: 'CNY' }] })).status, 400);
        assert.equal((await request("/supplier/quotes", "POST", { ...valid, valid_to: undefined })).status, 400);
        assert.equal((await request("/supplier/quotes", "POST", { ...valid, items: [valid.items[0], valid.items[0]] })).status, 400);
        assert.equal((await request("/supplier/quotes", "POST", { ...valid, items: [{ ...valid.items[0], constraints: { maxConcurrency: 1 } }] })).status, 400);
        assert.equal((await request("/supplier/quotes", "POST", { ...valid, items: [{ ...valid.items[0], components: [{ component_type: 'input', multiplier: '0.8', unit_price: '1.7' }] }] })).status, 400);
        assert.equal((await request("/supplier/quotes", "POST", valid)).status, 409);
        assert.equal(supplierQuoteRecords.length, length);
        clearPending();
        const created = await request("/supplier/quotes", "POST", valid, "SUPPLIER", "create-wire");
        assert.equal(created.status, 200);
        assert.equal(created.data.status, "APPROVING");
        assert.equal(created.data.item_count, 1);
        assert.equal(created.data.source, "MANUAL");
        assert.deepEqual((await request("/supplier/quotes", "POST", valid, "SUPPLIER", "create-wire")).data, created.data);
        assert.equal((await request("/supplier/quotes", "POST", { ...valid, remark: 'changed' }, "SUPPLIER", "create-wire")).status, 400);
        const detail = (await request(`/supplier/quotes/${created.data.id}`)).data;
        assert.equal(detail.items.length, 1);
        assert.equal(detail.items[0].components.length, 2);
        assert.equal(detail.items[0].fx_tier, "6.8");
        assert.equal(detail.items[0].constraints.daily_quota, 10000);
        assert.ok(!('marginPreview' in detail));
        const history = (await request(`/supplier/quotes/history?sku_id=${sku.id}&status=APPROVING`)).data;
        assert.ok(history.list.some((row: any) => row.id === created.data.id));
        for (const status of ["APPROVING", "EFFECTIVE", "REJECTED"]) {
            const page = (await request(`/supplier/quotes/history?page=1&size=1&status=${status}`)).data;
            assert.equal(page.total, supplierQuoteRecords.filter(quote => quote.supplierId === "supplier-cloud" && quote.status === status).length);
            assert.ok(page.list.length <= 1);
        }
        const pending = (await request("/internal/quotes/pending?supplier_id=1", "GET", undefined, "PURCHASING")).data;
        assert.equal(pending.list.length, 1);
        const diff = (await request(`/internal/quotes/${created.data.id}/diff`, "GET", undefined, "PURCHASING")).data;
        assert.equal(String(diff.quote_sheet_id), String(created.data.id));
        assert.equal(diff.items[0].components.length, 2);
        assert.equal(diff.items[0].margin_preview.margin_ok, null);
        const renamed = await request(`/internal/models/${sku.id}`, 'PUT', { sku_code: 'contract-quote-renamed', model_type: '对话', native_currency: 'USD' }, 'MODEL_OPS');
    assert.equal(renamed.status, 200);
    const renamedQuote = (await request(`/supplier/quotes/${created.data.id}`)).data;
    assert.equal(renamedQuote.items[0].sku_code, 'contract-quote-renamed');
    assert.equal(renamedQuote.items[0].sku_id, sku.id);
    const reason = '供应价超过当前市场，请复核后重新提交';
        assert.equal((await request(`/internal/quotes/${created.data.id}/reject`, "POST", { reason }, "PURCHASING")).status, 200);
        const rejected = (await request(`/supplier/quotes/${created.data.id}`)).data;
        assert.equal(rejected.status, "REJECTED");
        assert.equal(rejected.decision.reason, reason);
        const past = await request('/supplier/quotes', 'POST', { ...valid, valid_from: '2020-01-01T00:00:00Z' });
        assert.equal(past.data.clamped, true);
        assert.ok(Date.parse(past.data.valid_from) > Date.parse('2020-01-01T00:00:00Z'));
    }
    finally {
        supplierQuoteRecords.splice(0, supplierQuoteRecords.length, ...backup);
        mock.reset();
        server.close();
    }
});
test("CSV stream, multipart preview and full import confirmation share current price validation without batch persistence", async () => {
    const backup = structuredClone(supplierQuoteRecords);
    server.listen({ onUnhandledRequest: "error" });
    try {
        const response = await fetch('http://localhost/api/supplier/quotes/template?scope=active', { headers: { 'X-Mock-Identity': 'SUPPLIER' } });
        assert.equal(response.status, 200);
        assert.match(response.headers.get('content-type')!, /text\/csv/);
        const csv = await response.text();
        assert.ok(csv.includes('official_input'));
        assert.ok(csv.includes('price_output'));
        assert.ok(!csv.split('\n')[0].includes('valid_from'));
        assert.ok(!csv.includes('margin'));
        const sku = (await catalog()).find(sku => sku.sku_code === 'gpt-4.1-mini');
        const content = `sku_id,sku_code,model_name,currency,official_input,fx_tier,multiplier_input,price_input,rpm,compatibility\n${sku.id},gpt-4.1-mini,ignored display,USD,1.9,6.80,0.8,,3000,"OpenAI, compatible"\n999,missing,Missing,USD,,6.8,,1,,`;
        const form = new FormData();
        form.append('file', new File([content], 'quotes.csv', { type: 'text/csv' }));
        const before = supplierQuoteRecords.length, previewResponse = await fetch('http://localhost/api/supplier/quotes/import/preview', { method: 'POST', headers: { 'X-Mock-Identity': 'SUPPLIER' }, body: form });
        const preview = (await previewResponse.json()).data;
        assert.equal(previewResponse.status, 200);
        assert.equal(preview.total, 2);
        assert.equal(preview.warn_count, 1);
        assert.equal(preview.error_count, 1);
        assert.equal(preview.preview_items.length, 1);
        assert.match(preview.token, /^[a-f0-9]{64}$/);
        assert.equal(supplierQuoteRecords.length, before);
        assert.equal(preview.preview_items[0].components[0].unit_price, '0.32000000');
        assert.equal(preview.preview_items[0].constraints.compatibility, 'OpenAI, compatible');
        clearPending();
        const { currency: _, ...item } = preview.preview_items[0], valid = { ...payload(sku.id), items: [item] };
        const bad = { ...valid, items: [{ ...item, components: [{ ...item.components[0], unit_price: '2' }] }] };
        assert.equal((await request('/supplier/quotes/import/confirm', 'POST', bad)).status, 400);
        assert.equal(supplierQuoteRecords.length, before);
        const saved = await request('/supplier/quotes/import/confirm', 'POST', valid, 'SUPPLIER', 'import-wire');
        assert.equal(saved.data.status, 'APPROVING');
        assert.equal(saved.data.source, 'IMPORT');
        assert.ok(!('batchId' in saved.data));
        assert.deepEqual((await request('/supplier/quotes/import/confirm', 'POST', valid, 'SUPPLIER', 'import-wire')).data, saved.data);
        assert.equal((await request('/supplier/quotes/import/confirm', 'POST', { batchId: preview.token })).status, 400);
    }
    finally {
        supplierQuoteRecords.splice(0, supplierQuoteRecords.length, ...backup);
        mock.reset();
        server.close();
    }
});
test("model HTTP bridge uses documented update and alias suggestion paths and optional publication remark", async () => {
    server.listen({ onUnhandledRequest: 'error' });
    mock.reset();
    try {
        const defaultList = await request('/internal/models', 'GET', undefined, 'MODEL_OPS');
        assert.equal(defaultList.status, 200); assert.ok(defaultList.data.list.every((family: any) => Array.isArray(family.children)));
        const list = (await request('/internal/models?view=sku&page=1&size=100', 'GET', undefined, 'MODEL_OPS')).data.list;
        const source = list.find((sku: any) => sku.sku_code === 'gpt-4.1-mini');
        const created = await request('/internal/models', 'POST', { vendor_id: source.vendor_id, family_id: source.family_id, sku_code: 'contract-new', model_type: '对话', native_currency: 'USD' }, 'MODEL_OPS');
        assert.equal(created.status, 200);
        assert.equal(created.data.lifecycle_status, 'DRAFT');
        const updated = await request(`/internal/models/${created.data.id}`, 'PUT', { sku_code: 'contract-new-renamed', model_type: '对话', native_currency: 'USD', context_window: 0 }, 'MODEL_OPS');
        assert.equal(updated.status, 200);
        assert.equal(updated.data.context_window, 0);
        const suggestions = await request(`/internal/models/${created.data.id}/aliases/suggest?keyword=gpt`, 'GET', undefined, 'MODEL_OPS');
        assert.equal(suggestions.status, 200);
        assert.ok(Array.isArray(suggestions.data.suggestions));
        assert.ok(suggestions.data.suggestions.every((row: any) => row.sku_id && row.sku_code));
        const purchasable = list.find((sku: any) => sku.lifecycle_status === 'PURCHASABLE');
        const published = await request(`/internal/models/${purchasable.id}/publish`, 'POST', { remark: 'contract publication' }, 'PRICING_OP');
        assert.equal(published.status, 200);
        assert.equal(published.data.lifecycle_status, 'PUBLISHED');
        const target = list.find((sku: any) => sku.lifecycle_status === 'PUBLISHED');
        const impact = (await request(`/internal/models/${target.id}/deprecation-impact`, 'GET', undefined, 'MODEL_OPS')).data;
        const retirement = { impact_snapshot_id: impact.snapshot_id, sunset_date: '2099-01-01', reason: '契约验证退役并选择替代SKU', replacement_sku_id: purchasable.id };
        assert.equal((await request(`/internal/models/${target.id}/deprecate`, 'POST', { ...retirement, replacement_sku_id: '999' }, 'MODEL_OPS')).status, 400);
        assert.equal((await request(`/internal/models/${target.id}/deprecate`, 'POST', retirement, 'MODEL_OPS')).data.lifecycle_status, 'PUBLISHED');
        const applications = mock.handle({ path: '/api/internal/models/retirements', method: 'GET', role: 'MODEL_OPS' }) as any[];
        assert.equal(applications[0].replacementId, String(purchasable.id));
    }
    finally {
        mock.reset();
        server.close();
    }
});

test("Axios quote calls use documented paths, nested write DTOs, multipart and stream responses without Mock identity", async () => {
  const entries = new Map<string,string>();
  Object.defineProperty(globalThis, 'sessionStorage', { value: { getItem: (key: string) => entries.get(key) || null, setItem: (key: string,value: string) => entries.set(key,value), removeItem: (key: string) => entries.delete(key) }, configurable: true });
  setActivePinia(createPinia()); const session = useSessionStore(); session.user = { id: 'contract-user', name: 'test', roleLabel: 'custom' }; session.portal = 'supplier'; session.token = 'synthetic-contract-token';
  const sent = payload('9007199254741001'); let posts = 0;
  const wireSku = { id: '9007199254741001', sku_code: 'gpt-4.1-mini', model_name: 'GPT', vendor_id: 10, vendor_name: 'OpenAI',
    family_id: 100, family_name: 'GPT', model_type: '对话', native_currency: 'USD', context_window: null, tier_tag: null,
    official_price: null, has_official_price: false };
  function auth(request: Request) { assert.equal(request.headers.get('Authorization'), 'Bearer synthetic-contract-token'); assert.equal(request.headers.get('X-Mock-Identity'), null); }
  server.use(
    http.get('http://localhost/api/supplier/skus', ({ request }) => { auth(request); assert.equal(new URL(request.url).searchParams.get('keyword'), 'mini'); return HttpResponse.json({ code: 0, data: { list: [wireSku], total: 1, page: 1, size: 20 } }); }),
    http.post('http://localhost/api/supplier/quotes', async ({ request }) => { auth(request); assert.ok(request.headers.get('Idempotency-Key')); assert.deepEqual(await request.json(), sent); posts++; return HttpResponse.json({ code: 0, data: { id: 31, status: 'APPROVING', item_count: 1 } }); }),
    http.get('http://localhost/api/supplier/quotes/history', ({ request }) => { auth(request); assert.equal(new URL(request.url).searchParams.get('sku_id'), '9007199254741001'); return HttpResponse.json({ code: 0, data: { list: [{ id: 31 }], total: 1 } }); }),
    http.post('http://localhost/api/supplier/quotes/import/preview', async ({ request }) => { auth(request); assert.match(request.headers.get('content-type')!, /multipart\/form-data; boundary=/); const file = (await request.formData()).get('file'); assert.ok(file && typeof file !== 'string'); assert.equal(await file.text(), 'synthetic csv'); return HttpResponse.json({ code: 0, data: { token: 'file-digest', preview_items: [{ sku_id: wireSku.id, components: [] }] } }); }),
    http.get('http://localhost/api/supplier/quotes/template', ({ request }) => { auth(request); return new HttpResponse('\uFEFFsku_id,price_input\n1,2', { headers: { 'content-type': 'text/csv; charset=utf-8' } }); }),
  );
  server.listen({ onUnhandledRequest: 'error' });
  try {
    assert.equal((await supplierQuoteApi.skus({ page: 1, size: 20, keyword: 'mini' })).list[0].id, wireSku.id);
    assert.equal((await supplierQuoteApi.submit(sent)).status, 'APPROVING'); assert.equal(posts,1);
    await supplierQuoteApi.history({ page: 1,size: 20,sku_id: wireSku.id });
    assert.equal((await supplierQuoteApi.preview(new File(['synthetic csv'],'quotes.csv'))).token,'file-digest');
    const file = await apiFileRequest({ url: '/supplier/quotes/template', adapter: 'fetch' }); assert.ok((await file.text()).includes('price_input'));
    server.use(http.get('http://localhost/api/supplier/skus', () => HttpResponse.json({ code: 0, data: { list: [{ ...wireSku, id: Number.MAX_SAFE_INTEGER + 1 }] } })));
    await assert.rejects(() => supplierQuoteApi.skus({ page: 1,size: 20 }), /安全范围/);
  } finally { session.clear(); server.close(); server.resetHandlers(); }
});
