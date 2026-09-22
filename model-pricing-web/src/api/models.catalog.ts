import { assertContractId } from "../domain/contractId.ts";
import type { ModelCapabilityDTO, ModelContractId, ModelDetailDTO, ModelListItem, ModelSkuContractDTO } from "./models.types";
import { MODEL_STATUS_LABELS, MODEL_VERIFY_STATUS_LABELS } from "../domain/status.ts";

export const capabilityLabels: Record<Exclude<keyof ModelCapabilityDTO, "max_output_tokens">, string> = {
  function_call: "工具调用", vision: "视觉", audio: "音频", video: "视频",
  embedding: "向量", reasoning: "推理", json_mode: "结构化输出", streaming: "流式响应",
};

export function modelContractId(id: ModelContractId): string {
  assertContractId(id);
  return String(id);
}

export function toModelListItem(sku: ModelSkuContractDTO): ModelListItem {
  if (!sku || typeof sku.sku_code !== "string" || typeof sku.vendor_name !== "string" ||
      typeof sku.family_name !== "string" || (sku.aliases !== null && !Array.isArray(sku.aliases)) ||
      (sku.context_window !== null && (!Number.isInteger(sku.context_window) || sku.context_window < 0)))
    throw new Error("模型列表响应字段不符合约定，请联系技术人员。");
  if (!Object.hasOwn(MODEL_STATUS_LABELS, sku.lifecycle_status) ||
      !Object.hasOwn(MODEL_VERIFY_STATUS_LABELS, sku.verify_status) ||
      typeof sku.is_sensitive !== "boolean" || typeof sku.cross_border !== "boolean" ||
      (sku.aliases ?? []).some(alias => typeof alias !== "string") ||
      (sku.capability !== null && (typeof sku.capability !== "object" || Array.isArray(sku.capability))))
    throw new Error("模型状态或能力响应不符合约定，请联系技术人员。");
  const capability: ModelCapabilityDTO = {};
  for (const [key, value] of Object.entries(sku.capability ?? {})) {
    if (key === "max_output_tokens") {
      if (value !== null && (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0))
        throw new Error("模型最大输出长度响应不符合约定。");
      capability.max_output_tokens = value;
    } else if (Object.hasOwn(capabilityLabels, key)) {
      if (typeof value !== "boolean") throw new Error("模型能力响应含非布尔值。");
      capability[key as keyof typeof capabilityLabels] = value;
    }
  }
  return {
    id: modelContractId(sku.id), code: sku.sku_code,
    // SKU contract has no separate display name: show its code, not fabricated metadata.
    name: sku.sku_code, vendor: sku.vendor_name, family: sku.family_name,
    vendorId: modelContractId(sku.vendor_id), familyId: modelContractId(sku.family_id),
    type: sku.model_type, context: sku.context_window,
    capabilities: (Object.keys(capabilityLabels) as Array<keyof typeof capabilityLabels>)
      .filter((key) => sku.capability?.[key] === true).map((key) => capabilityLabels[key]),
    tier: sku.tier_tag, sensitive: sku.is_sensitive, crossBorder: sku.cross_border,
    currency: sku.native_currency, status: sku.lifecycle_status,
    aliases: sku.aliases ?? [], verifyStatus: sku.verify_status,
    capabilitiesProvided: sku.capability !== null,
    capability: sku.capability === null ? null : capability,
  };
}

export const mockModelTypes = ["对话", "推理", "多模态", "向量", "嵌入", "图像", "语音"];

export function toModelDetailItem(response: ModelDetailDTO, requestedId: string): ModelListItem {
  const item = toModelListItem(response?.sku);
  if (item.id !== requestedId) throw new Error("模型详情与请求对象不一致，请刷新核对。");
  if (response.sku.sunset_date !== null && !/^\d{4}-\d{2}-\d{2}$/.test(response.sku.sunset_date))
    throw new Error("模型下线日期响应不符合约定。");
  const extension = response.mock_extensions;
  if (extension !== undefined && (!extension || typeof extension.protocol !== "string" ||
      typeof extension.pending_retirement !== "boolean" || !Array.isArray(extension.verification) ||
      extension.verification.some(record => !record || typeof record.by !== "string" ||
        typeof record.at !== "string" || !Number.isFinite(Date.parse(record.at)) ||
        !["PASS", "FAIL"].includes(record.result) || typeof record.note !== "string")))
    throw new Error("模型详情暂定扩展响应不符合约定，请重新查询。");
  return item;
}

// Structural validation only. Existence, uniqueness and lifecycle authority belong to backend.
export function validateModelSkuRequest(body: unknown, update = false): void {
  if (!body || typeof body !== "object" || Array.isArray(body)) throw new Error("模型请求必须是一个对象。");
  const value = body as Record<string, unknown>;
  const allowed = new Set(["sku_code", "model_type", "native_currency", "context_window", "capability", "tier_tag", "is_sensitive", "cross_border", "aliases", ...(update ? [] : ["vendor_id", "family_id"])]);
  if (Object.keys(value).some((key) => !allowed.has(key))) throw new Error("请求含不允许维护的字段；厂商/系列迁移与状态变更须走独立流程。");
  if (typeof value.sku_code !== "string" || !value.sku_code.trim() || value.sku_code.trim().length > 128) throw new Error("SKU 编码必须是 1~128 个字符。");
  if (typeof value.model_type !== "string" || !mockModelTypes.includes(value.model_type)) throw new Error("请选择支持的模型类型。");
  if (typeof value.native_currency !== "string" || !/^[A-Z]{3}$/.test(value.native_currency)) throw new Error("原厂币种必须是三位大写字母。");
  if (!update) {
    modelContractId(value.vendor_id as ModelContractId);
    modelContractId(value.family_id as ModelContractId);
  }
  if (value.context_window !== undefined && (!Number.isSafeInteger(value.context_window) || (value.context_window as number) < 0)) throw new Error("上下文长度必须是非负整数。");
  if (value.tier_tag !== undefined && !["旗舰", "主力", "经济", "长尾"].includes(String(value.tier_tag))) throw new Error("请选择合法的分级标签。");
  for (const key of ["is_sensitive", "cross_border"]) if (value[key] !== undefined && typeof value[key] !== "boolean") throw new Error("运营标记必须是布尔值。");
  if (value.aliases !== undefined && (!Array.isArray(value.aliases) || value.aliases.some((alias) => typeof alias !== "string" || !alias.trim() || alias.trim().length > 128))) throw new Error("每个别名必须是 1~128 个字符。");
  if (Array.isArray(value.aliases) && new Set(value.aliases.map((alias: string) => alias.trim().toLowerCase())).size !== value.aliases.length) throw new Error("别名不能重复。");
  if (value.capability !== undefined) {
    if (!value.capability || typeof value.capability !== "object" || Array.isArray(value.capability)) throw new Error("能力参数必须是固定字段对象。");
    for (const [key, item] of Object.entries(value.capability)) {
      if (key === "max_output_tokens") {
        if (item !== null && (!Number.isSafeInteger(item) || (item as number) < 0)) throw new Error("最大输出长度必须是非负整数或空值。");
      } else if (!Object.hasOwn(capabilityLabels, key) || typeof item !== "boolean") throw new Error("能力参数存在未知字段或非布尔值。");
    }
  }
}
