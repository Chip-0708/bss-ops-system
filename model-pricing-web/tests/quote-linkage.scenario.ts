import test from "node:test";
import assert from "node:assert/strict";
import { setupServer } from "msw/node";
import { legacySupplierPortalHandlers as supplierPortalHandlers } from "../src/mocks/handlers/supplierPortal";
import { supplierQuoteHandlers } from "../src/mocks/handlers/supplierQuotes";
import { customerQuoteHandlers } from "../src/mocks/handlers/customerQuotes";
import { officialPriceHandlers } from "../src/mocks/handlers/officialPrices";
import { costHandlers } from "../src/mocks/handlers/cost";
import { supplierQuoteRecords } from "../src/mocks/data/supplierQuotes";
import { customerQuoteRecords } from "../src/mocks/data/customers";

test("HTTP quote renewal, constraints, template pricing and customer cloning share records safely", async () => {
  // Production handlers use browser-relative paths; supply that origin in Node.
  Object.defineProperty(globalThis, "location", { value: new URL("http://localhost/"), configurable: true });
  const server = setupServer(...supplierPortalHandlers, ...supplierQuoteHandlers, ...customerQuoteHandlers, ...officialPriceHandlers, ...costHandlers);
  server.listen({ onUnhandledRequest: "error" });
  async function request(path: string, identity: string, method = "GET", body?: unknown, key?: string) {
    const response = await fetch(`http://localhost/api${path}`, { method, headers: { "X-Mock-Identity": identity, "Content-Type": "application/json", ...(key ? { "Idempotency-Key": key } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) });
    return { status: response.status, ...(await response.json()) as { data: any; message: string } };
  }
  try {
    const source = supplierQuoteRecords.find(row => row.id === "q-1001")!;
    source.status = "REJECTED"; const before = structuredClone(source);
    const old = await request(`/supplier/quotes/${source.id}`, "SUPPLIER");
    const payload = { effectiveFrom: "2099-01-01T00:00:00+08:00", effectiveTo: "2099-02-01T00:00:00+08:00", remark: "续报", items: old.data.items.map(({ id: _id, ...item }: { id: string }) => item), supplyConstraints: { maxConcurrency: "20", rpm: "200", tpm: "100000", compatibilityNote: "支持工具调用" } };
    const renewed = await request(`/supplier/quotes/${source.id}/renew`, "SUPPLIER", "POST", payload, "renew-key");
    assert.equal(renewed.status, 200); assert.equal(renewed.data.status, "DRAFT"); assert.notEqual(renewed.data.id, source.id); assert.equal(renewed.data.version, source.version + 1);
    assert.equal(renewed.data.previousQuoteId, source.id); assert.deepEqual(source, before);
    const replay = await request(`/supplier/quotes/${source.id}/renew`, "SUPPLIER", "POST", payload, "renew-key");
    assert.deepEqual(replay.data, renewed.data);
    assert.equal(supplierQuoteRecords.filter(row => row.id === renewed.data.id).length, 1);
    assert.equal((await request(`/supplier/quotes/${source.id}/renew`, "VIEWER", "POST", payload, "denied")).status, 403);
    assert.equal((await request(`/supplier/quotes/${source.id}/renew`, "SUPPLIER", "POST", { ...payload, supplyConstraints: { rpm: "-1" } }, "invalid")).status, 400);
    await request(`/supplier/quotes/${renewed.data.id}`, "SUPPLIER", "PUT", payload, "update-key");
    const internal = await request(`/internal/quotes/${renewed.data.id}`, "PURCHASING");
    assert.equal(internal.status, 200); assert.equal(internal.data.supplyConstraints.rpm, "200");
    assert.ok(internal.data.constraints.some((item: { label: string; proposed: string }) => item.label === "最大并发" && item.proposed === "20"));
    assert.equal(internal.data.items[0].previousPrice, source.items[0]?.proposedPrice);
    assert.deepEqual(source, before);

    const options = await request("/supplier/quote-options", "SUPPLIER");
    const option = options.data.find((row: { skuCode: string; component: string }) => row.skuCode === "gpt-4.1" && row.component === "输入 Token");
    const { key: _key, lastUnitPrice: _lastPrice, officialPrice: _officialPrice, ...optionItem } = option;
    const multiplierPayload = { effectiveFrom: "2099-01-01T00:00:00+08:00", items: [{ ...optionItem, pricingMode: "MULTIPLIER", multiplier: "0.9", unitPrice: "1.80000000" }] };
    const multiplierQuote = await request("/supplier/quotes", "SUPPLIER", "POST", multiplierPayload, "multiplier-key");
    assert.equal(multiplierQuote.status, 200); assert.equal(multiplierQuote.data.items[0].multiplier, "0.9");
    const multiplierInternal = await request(`/internal/quotes/${multiplierQuote.data.id}`, "PURCHASING"); assert.equal(multiplierInternal.data.items[0].pricingMode, "MULTIPLIER");
    assert.equal((await request("/supplier/quotes", "SUPPLIER", "POST", { ...multiplierPayload, items: [{ ...multiplierPayload.items[0], unitPrice: "1.7" }] }, "wrong-conversion")).status, 400);
    assert.equal((await request("/supplier/quotes", "SUPPLIER", "POST", { ...multiplierPayload, items: [{ ...multiplierPayload.items[0], basisVersion: "stale" }] }, "stale-basis")).status, 409);
    const mini = options.data.find((row: { skuCode: string; component: string }) => row.skuCode === "gpt-4.1-mini" && row.component === "输入 Token");
    assert.equal((await request("/supplier/quotes", "SUPPLIER", "POST", { ...multiplierPayload, items: [{ ...mini, pricingMode: "MULTIPLIER", multiplier: "1.12345678", unitPrice: "0.44938271" }] }, "rounding-not-defined")).status, 400);

    const template = await request("/internal/customer-quotes/template?customerId=customer-blue", "PRICING_OP");
    assert.equal(template.status, 200);
    const draft = { customerId: "customer-blue", name: "套价草稿", reason: "客户需要", priceBookCode: template.data.priceBookCode, items: template.data.items.map((item: object) => ({ ...item, floorPrice: "0", referencePrice: "0" })) };
    const created = await request("/internal/customer-quotes", "PRICING_OP", "POST", draft, "template-key");
    assert.equal(created.status, 200);
    const saved = customerQuoteRecords.find(row => row.id === created.data.id)!;
    assert.equal(saved.items[0]?.floorPrice, template.data.items[0].floorPrice);
    assert.equal(saved.items[0]?.referencePrice, template.data.items[0].referencePrice);
    assert.equal((await request("/internal/customer-quotes/template?customerId=customer-medical", "PRICING_OP")).status, 423);

    const formal = customerQuoteRecords.find(row => row.id === "cq-blue-formal-demo")!; const original = structuredClone(formal);
    const cloneBody = { customerId: formal.customerId, name: "克隆草稿", reason: "新业务需求", items: structuredClone(formal.items) };
    const cloned = await request(`/internal/customer-quotes/${formal.id}/clone`, "PRICING_OP", "POST", cloneBody, "clone-key");
    assert.equal(cloned.status, 200); assert.equal(cloned.data.status, "DRAFT"); assert.deepEqual(formal, original);
    const clone = customerQuoteRecords.find(row => row.id === cloned.data.id)!;
    assert.equal(clone.previousQuoteId, formal.id); assert.equal(clone.acceptedAt, undefined); assert.equal(clone.validFrom, undefined);
    const cloneReplay = await request(`/internal/customer-quotes/${formal.id}/clone`, "PRICING_OP", "POST", cloneBody, "clone-key"); assert.deepEqual(cloneReplay.data, cloned.data);
    assert.equal((await request(`/internal/customer-quotes/${formal.id}/clone`, "PRICING_OP", "POST", { ...cloneBody, customerId: "customer-lighthouse" }, "foreign")).status, 400);

    // 官方价：建采集批次 → 批量录入暂存(带实时 diff) → 勾选确认建单（对齐真实后端契约）。
    const job = await request("/internal/price-sync/jobs", "MODEL_OPS", "POST", { job_type: "SYNC_PRICES", source: "manual" }, "job-key");
    assert.equal(job.status, 200); assert.equal(job.data.status, "SUCCESS");
    assert.equal((await request("/internal/price-sync/jobs", "VIEWER", "POST", { job_type: "SYNC_PRICES", source: "x" }, "denied-job")).status, 403);
    const stagingPayload = { sync_job_id: job.data.id, items: [{ sku_id: 40, currency: "USD", payload: { input: "2.50000000", output: "9.00000000" } }] };
    const entered = await request("/internal/staging-prices", "MODEL_OPS", "POST", stagingPayload, "staging-key");
    assert.equal(entered.status, 200); assert.equal(entered.data.created_count, 1);
    assert.deepEqual((await request("/internal/staging-prices", "MODEL_OPS", "POST", stagingPayload, "staging-key")).data, entered.data);
    const stagingList = await request(`/internal/staging-prices?sync_job_id=${job.data.id}`, "MODEL_OPS");
    const stagedRow = stagingList.data.list.find((row: { id: number }) => row.id === entered.data.staging_ids[0]);
    assert.equal(stagedRow.diff_status, "CHANGED"); // output 8→9 上涨
    const confirmed = await request("/internal/staging-prices/confirm", "MODEL_OPS", "POST", { sync_job_id: job.data.id, staging_ids: entered.data.staging_ids, effective_time: "2026-09-14T10:00:00Z" }, "confirm-key");
    assert.equal(confirmed.status, 200); assert.equal(confirmed.data.step_count, 2); // 涨价两级审批
    assert.ok(confirmed.data.change_request_id);
    assert.equal((await request("/internal/staging-prices/confirm", "MODEL_OPS", "POST", { sync_job_id: job.data.id, staging_ids: entered.data.staging_ids, effective_time: "2099-01-01T00:00:00Z" }, "future-confirm")).status, 400);

    const decreaseRows = await request("/internal/staging-prices", "MODEL_OPS", "POST", {
      sync_job_id: job.data.id,
      items: [
        { raw_sku_code: "gpt-4.1", currency: "USD", payload: { output: "9.00000000" } },
        { raw_sku_code: "gpt-4.1", currency: "USD", payload: { output: "7.00000000" } },
      ],
    }, "staging-decrease-key");
    const decreaseList = await request(`/internal/staging-prices?sync_job_id=${job.data.id}`, "MODEL_OPS");
    const matchedByCode = decreaseList.data.list.filter((row: { id: number }) => decreaseRows.data.staging_ids.includes(row.id));
    assert.ok(matchedByCode.every((row: { match_status: string; sku_id: number }) => row.match_status === "MATCHED" && row.sku_id === 40));
    const decreased = await request("/internal/staging-prices/confirm", "MODEL_OPS", "POST", {
      sync_job_id: job.data.id,
      staging_ids: decreaseRows.data.staging_ids,
      effective_time: "2026-09-14T10:00:00Z",
    }, "confirm-decrease-key");
    assert.equal(decreased.status, 200); assert.equal(decreased.data.step_count, 1);

    // 成本参数：GET defaults+overrides；PUT 全量替换 overrides。
    const params = await request("/internal/cost/params", "PRICING_OP");
    assert.equal(params.status, 200); assert.ok(params.data.defaults.loss_rate);
    const paramsPayload = { overrides: [{ scope_type: "MODEL", scope_id: 41, loss_rate: "0.0500", channel_rate: "0.0100", tax_inclusive: false, withholding_tax: "0.0000" }] };
    const savedParams = await request("/internal/cost/params", "PRICING_OP", "PUT", paramsPayload, "params-key");
    assert.equal(savedParams.status, 200); assert.equal(savedParams.data.overrides_count, 1);
    assert.deepEqual((await request("/internal/cost/params", "PRICING_OP", "PUT", paramsPayload, "params-key")).data, savedParams.data);
    assert.equal((await request("/internal/cost/params", "PURCHASING", "PUT", paramsPayload, "denied-params")).status, 403);
    assert.equal((await request("/internal/cost/params", "PRICING_OP", "PUT", { defaults: params.data.defaults, overrides: [] }, "readonly-defaults")).status, 400);
    assert.equal((await request("/internal/cost/params", "PRICING_OP", "PUT", { overrides: [{ scope_type: "MODEL", scope_id: 41, loss_rate: "9", channel_rate: "0.01", tax_inclusive: false, withholding_tax: "0" }] }, "bad-rate")).status, 400);

    const compare = await request("/internal/cost/baselines/40/compare", "PRICING_OP");
    assert.equal(compare.status, 200); assert.equal(typeof compare.data.suppliers[0].scores.total, "string");
    const historyAt = await request("/internal/cost/baselines/40/history?asOf=2026-08-15T00:00:00%2B08:00", "PRICING_OP");
    assert.equal(historyAt.status, 200); assert.equal(historyAt.data.list.length, 1); assert.equal(historyAt.data.list[0].version, 7);

    const historyTemplate = await request("/supplier/quotes/import/template?scope=HISTORY", "SUPPLIER");
    const allTemplate = await request("/supplier/quotes/import/template?scope=ALL", "SUPPLIER");
    assert.equal(historyTemplate.status, 200); assert.equal(allTemplate.data.content.trim().split("\n").length, 5);
    assert.equal((await request("/supplier/quotes/import/template?scope=ALL&search=gpt-4.1-mini", "SUPPLIER")).data.content.trim().split("\n").length, 3);
    const preview = await request("/supplier/quotes/import/validate", "SUPPLIER", "POST", { fileName: "template.csv", content: allTemplate.data.content });
    assert.equal(preview.status, 200); assert.equal(preview.data.invalidRows, 0);
    const committed = await request("/supplier/quotes/import/commit", "SUPPLIER", "POST", { batchId: preview.data.batchId }, "import-key");
    assert.equal(committed.status, 200); assert.equal(committed.data.status, "DRAFT");
    assert.deepEqual((await request("/supplier/quotes/import/commit", "SUPPLIER", "POST", { batchId: preview.data.batchId }, "import-key")).data, committed.data);

    const past = await request("/supplier/quotes", "SUPPLIER", "POST", { ...multiplierPayload, effectiveFrom: "2020-01-01T00:00:00Z", effectiveTo: "2099-01-01T00:00:00Z" }, "past-draft");
    const submitted = await request(`/supplier/quotes/${past.data.id}/submit`, "SUPPLIER", "POST", undefined, "past-submit");
    assert.equal(submitted.status, 200);
    const adjusted = await request(`/supplier/quotes/${past.data.id}`, "SUPPLIER"); assert.equal(adjusted.data.effectiveFrom, submitted.data.submittedAt);
    const expired = await request("/supplier/quotes", "SUPPLIER", "POST", { ...multiplierPayload, effectiveFrom: "2020-01-01T00:00:00Z", effectiveTo: "2020-02-01T00:00:00Z" }, "expired-draft");
    assert.equal((await request(`/supplier/quotes/${expired.data.id}/submit`, "SUPPLIER", "POST", undefined, "expired-submit")).status, 409);
    assert.equal((await request(`/supplier/quotes/${expired.data.id}`, "SUPPLIER")).data.status, "DRAFT");
  } finally { server.close(); Reflect.deleteProperty(globalThis, "location"); }
});
