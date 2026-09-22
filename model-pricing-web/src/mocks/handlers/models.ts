import { http, HttpResponse, type HttpResponseResolver } from "msw";
import { ApiError } from "../../domain/common";
import type { Model } from "../../api/models.types";
import { mock } from "../../mock";
import { role } from "../../session";
import { modelOptions, modelDetail, queryModelCatalog } from "../data/modelCatalog";
import type { ModelContractListParams } from "../../api/models.types";
import type { Impact, Retirement, Role } from "../../types";

const requestId = () => `mock-${crypto.randomUUID()}`;
const basePath = "/api/internal/models";

function visibleModels(identity: Role = role.value) {
  return mock.handle({
    path: basePath,
    method: "GET",
    role: identity,
  }) as Model[];
}

const resolver: HttpResponseResolver = async ({ request }) => {
  const url = new URL(request.url);
  const method = request.method as "GET" | "POST" | "PUT";
  const identityHeader = request.headers.get("X-Mock-Identity");
  const identity: Role = identityHeader === "MODEL_OPS" || identityHeader === "PRICING_OP"
    ? identityHeader : identityHeader ? "VIEWER" : role.value;
  let body: unknown;
  if (method === "GET") {
    if (url.pathname.endsWith("/suggestions")) {
      body = { name: url.searchParams.get("name") || "" };
    }
  }
  try {
    if (method !== "GET") body = await request.json();
    let data: unknown;
    if (method === "GET" && url.pathname === `${basePath}/options`) {
      data = modelOptions(visibleModels(identity));
    } else if (method === "GET" && url.pathname === basePath) {
      const params = Object.fromEntries(url.searchParams) as ModelContractListParams;
      params.page = Number(url.searchParams.get("page") ?? 1);
      params.size = Number(url.searchParams.get("size") ?? 20);
      try { data = queryModelCatalog(visibleModels(identity), params); }
      catch (error) { throw new ApiError(400, error instanceof Error ? error.message : "模型查询参数错误"); }
    } else if (method === "GET" && url.pathname.endsWith("/aliases/suggest")) {
      const rows = mock.handle({ path: url.pathname.replace(/\/aliases\/suggest$/, "/suggestions"), method, role: identity,
        body: { name: url.searchParams.get("keyword") || "" } }) as Array<{ id: string; code: string; score: number }>;
      data = { suggestions: rows.map(row => ({ sku_id: row.id, sku_code: row.code, score: row.score })) };
    } else if (method === "PUT" && new RegExp(`^${basePath}/[^/]+$`).test(url.pathname)) {
      if (!body || typeof body !== "object" || Array.isArray(body) || Object.hasOwn(body, "id")) throw new ApiError(400, "模型维护请求不接受id字段");
      data = mock.handle({ path: basePath, method, role: identity, key: request.headers.get("Idempotency-Key") || undefined,
        body: { ...body, id: decodeURIComponent(url.pathname.slice(basePath.length + 1)) } });
    } else if (method === "GET" && url.pathname.endsWith("/deprecation-impact")) {
      const report = mock.handle({ path: url.pathname, method, role: identity }) as Impact;
      data = { sku_id: report.modelId, snapshot_id: report.id, references: {
        price_books: report.books.map((name,index) => ({ id: String(index+1), name, level_code: "DEMO" })),
        contracts: report.contracts.map((customer_name,index) => ({ id: String(index+1), customer_name, expire_at: "2099-01-01" })),
        customer_quotes: report.quotes.map((name,index) => ({ id: String(index+1), status: "FORMAL" })),
      }, reference_count: report.books.length+report.contracts.length+report.quotes.length,
        replacements: visibleModels(identity).filter(model => report.alternatives.includes(model.code)).map(model => ({ sku_id: model.id, sku_code: model.code, reason: "演示候选，需人工核对" })),
        generated_at: new Date().toISOString() };
    } else if (method === "POST" && url.pathname.endsWith("/deprecate")) {
      const value = body as Record<string,unknown>;
      if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some(key => !["impact_snapshot_id","sunset_date","reason","replacement_sku_id"].includes(key))) throw new ApiError(400, "退役请求字段不符合契约");
      const retirement = mock.handle({ path: url.pathname, method, role: identity, key: request.headers.get("Idempotency-Key") || undefined,
        body: { impactId: value.impact_snapshot_id, offlineAt: value.sunset_date, reason: value.reason, replacementId: value.replacement_sku_id } }) as Retirement;
      data = { sku_id: retirement.modelId, approval_id: retirement.id, sunset_date: retirement.offlineAt, lifecycle_status: "PUBLISHED" };
    } else if (
      method === "GET" &&
      new RegExp(`^${basePath}/[^/]+$`).test(url.pathname)
    ) {
      const id = decodeURIComponent(url.pathname.slice(basePath.length + 1));
      const model = visibleModels(identity).find((model) => model.id === id);
      if (!model) throw new ApiError(404, "模型不存在或当前账号不可见");
      data = modelDetail(model);
    } else {
      data = mock.handle({
        path: url.pathname,
        method,
        body,
        key: request.headers.get("Idempotency-Key") || undefined,
        role: identity,
      });
    }
    return HttpResponse.json({
      code: 0,
      message: "ok",
      data,
      requestId: requestId(),
    });
  } catch (error) {
    const candidate = error as { status?: number; httpStatus?: number; message?: string };
    const reportedStatus = candidate.httpStatus || candidate.status || 500;
    const status = reportedStatus === 422 ? 400 : reportedStatus;
    return HttpResponse.json(
      {
        code: status === 409 ? "STATE_CONFLICT" : `HTTP_${status}`,
        message: candidate.message || "Mock 服务异常",
        requestId: requestId(),
      },
      { status },
    );
  }
};

export const modelHandlers = [
  http.all("/api/internal/models", resolver),
  http.all("/api/internal/models/*", resolver),
];
