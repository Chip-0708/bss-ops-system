import { apiRequest } from "./http";
import { ApiError } from "../domain/common";
import { assertContractId } from "../domain/contractId";
import type { PageResult } from "../domain/common";
import type {
  BatchResult,
  CreateModelSkuRequest,
  UpdateModelSkuRequest,
  ModelDeprecationImpactDTO,
  DeprecateModelRequest,
  DeprecateModelResponseDTO,
  Model,
  ModelSuggestionDTO,
  ModelListParams,
  ModelSkuContractDTO,
  ModelFamilyContractDTO,
  ModelOptionsDTO,
  ModelAliasSuggestionsResponseDTO,
  ModelBatchRequest,
  ModelBatchResponseDTO,
  PublishModelResponseDTO,
  MergeModelAliasResponseDTO,
  ModelDetailDTO,

} from "./models.types";

// List/create/update, aliases and batch follow the supplied SKU wire contract.
// Detail/options/verification remain provisional; retirement follows requirements and the reported lifecycle correction.
const basePath = "/internal/models";

export const modelsApi = {
  list: (params: ModelListParams) =>
    apiRequest<PageResult<ModelSkuContractDTO | ModelFamilyContractDTO>>({ url: basePath, method: "GET", params }),
  // PROVISIONAL lookup endpoint for Mock development only, not a confirmed backend path.
  getOptions: async (): Promise<ModelOptionsDTO> => {
    const data = await apiRequest<{
      vendors: Array<{ id: string | number; name: string }>;
      families: Array<{ id: string | number; vendor_id: string | number; name: string }>;
    }>({ url: `${basePath}/options`, method: "GET" });
    data.vendors.forEach((item) => assertContractId(item.id));
    data.families.forEach((item) => {
      assertContractId(item.id);
      assertContractId(item.vendor_id);
    });
    return {
      vendors: data.vendors.map((item) => ({ id: String(item.id), name: item.name })),
      families: data.families.map((item) => ({
        id: String(item.id), vendor_id: String(item.vendor_id), name: item.name,
      })),
    };
  },
  get: (id: string) =>
    apiRequest<ModelDetailDTO>({ url: `${basePath}/${id}`, method: "GET" }),
  create: (draft: CreateModelSkuRequest) =>
    apiRequest<ModelSkuContractDTO>({
      url: basePath,
      method: "POST",
      data: draft,
      idempotency: { scope: "model:create", payload: draft },
    }),
  update: (id: string, draft: UpdateModelSkuRequest) =>
    apiRequest<ModelSkuContractDTO>({
      url: `${basePath}/${id}`,
      method: "PUT",
      data: draft,
      idempotency: { scope: `model:update:${id}`, payload: draft },
    }),
  // PROVISIONAL verification endpoint and transition; note limit is a Mock convention.
  verify: (id: string, body: { result: "PASS" | "FAIL"; note: string }) =>
    apiRequest<{ id: string }>({
      url: `${basePath}/${id}/verify`,
      method: "POST",
      data: body,
      idempotency: { scope: `model:verify:${id}`, payload: body },
    }),
  setAliases: (id: string, aliases: string[]) =>
    apiRequest<unknown>({
      url: `${basePath}/${id}/aliases`,
      method: "POST",
      data: { aliases, source: "MANUAL" },
      idempotency: { scope: `model:alias:${id}`, payload: { aliases, source: "MANUAL" } },
    }),
  suggestions: async (_id: string, keyword: string): Promise<ModelSuggestionDTO[]> => {
    const data = await apiRequest<ModelAliasSuggestionsResponseDTO>({
      url: `${basePath}/aliases/suggest`,
      method: "GET",
      params: { keyword },
    });
    return data.suggestions.map((item) => {
      if (typeof item.sku_id === "number" && !Number.isSafeInteger(item.sku_id)) {
        throw new Error("接口返回的 SKU ID 超出安全整数范围，请后端以字符串返回 ID。");
      }
      return { id: String(item.sku_id), code: item.sku_code, name: item.sku_code };
    });
  },
  mergeAlias: (targetId: string, sourceId: string, alias: string) =>
    apiRequest<MergeModelAliasResponseDTO>({
      url: `${basePath}/aliases/merge`,
      method: "POST",
      data: { target_sku_id: targetId, source_sku_id: sourceId, alias },
      idempotency: { scope: `model:alias-merge:${targetId}`, payload: { target_sku_id: targetId, source_sku_id: sourceId, alias } },
    }),
  batch: async (body: { ids: string[]; action: string; value: string }): Promise<BatchResult[]> => {
    if (!body.ids.length || body.ids.length > 200 || new Set(body.ids).size !== body.ids.length)
      throw new ApiError(400, "请选择 1~200 个不重复的模型。");
    let payload: ModelBatchRequest;
    if (body.action === "tier" && ["旗舰", "主力", "经济", "长尾"].includes(body.value)) {
      payload = { sku_ids: body.ids, action: "SET_TIER", payload: { tier_tag: body.value } };
    } else if (body.action === "status" && body.value === "PENDING_VERIFY") {
      payload = { sku_ids: body.ids, action: "SUBMIT_VERIFY" };
    } else {
      throw new ApiError(400, "不支持此批量操作；上架和退役必须逐个处理。");
    }
    const result = await apiRequest<ModelBatchResponseDTO>({
      url: `${basePath}/batch`,
      method: "POST",
      data: payload,
      idempotency: { scope: "model:batch", payload },
    });
    if (!result || !Array.isArray(result.failed) || result.total !== body.ids.length ||
        result.succeeded !== result.total - result.failed.length) {
      throw new ApiError(0, "批量响应数量不符合约定，请刷新后核对实际结果。", "INVALID_RESPONSE");
    }
    const failed = new Map<string, { message: string }>();
    for (const item of result.failed) {
      if (!item || typeof item.code !== "number" || typeof item.message !== "string" || !item.message.trim())
        throw new ApiError(0, "批量失败清单不符合约定，请刷新后核对实际结果。", "INVALID_RESPONSE");
      if (typeof item.sku_id === "number" && !Number.isSafeInteger(item.sku_id))
        throw new ApiError(0, "返回的 SKU ID 超出安全范围，请联系后端核对。", "INVALID_RESPONSE");
      const id = String(item.sku_id);
      if (!body.ids.includes(id) || failed.has(id))
        throw new ApiError(0, "批量失败清单不符合约定，请刷新后核对实际结果。", "INVALID_RESPONSE");
      failed.set(id, item);
    }
    return body.ids.map((id) => ({ id, code: id, ok: !failed.has(id), reason: failed.get(id)?.message || "已更新" }));
  },
  publish: (id: string, remark?: string) =>
    apiRequest<PublishModelResponseDTO>({
      url: `${basePath}/${id}/publish`,
      method: "POST",
      data: remark?.trim() ? { remark: remark.trim() } : {},
      idempotency: { scope: `model:publish:${id}`, payload: remark?.trim() ? { remark: remark.trim() } : {} },
    }),
  getDeprecationImpact: (id: string) =>
    apiRequest<ModelDeprecationImpactDTO>({
      url: `${basePath}/${id}/deprecation-impact`,
      method: "GET",
    }),
  deprecate: (
    id: string,
    body: DeprecateModelRequest,
  ) =>
    apiRequest<DeprecateModelResponseDTO>({
      url: `${basePath}/${id}/deprecate`,
      method: "POST",
      data: body,
      idempotency: { scope: `model:deprecate:${id}`, payload: body },
    }),
};
