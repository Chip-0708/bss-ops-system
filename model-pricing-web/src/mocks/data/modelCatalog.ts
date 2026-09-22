import type { Model } from "../../types";
import { ApiError, statuses } from "../../types.ts";
import type { CreateModelSkuRequest, UpdateModelSkuRequest, ModelCapabilityDTO, ModelContractListParams, ModelFamilyContractDTO, ModelOptionsDTO, ModelSkuContractDTO } from "../../api/models.types";
import { capabilityLabels, validateModelSkuRequest } from "../../api/models.catalog.ts";

const vendorIds = new Map<string, string>();
const familyIds = new Map<string, string>();
const identifier = (map: Map<string, string>, key: string) => {
  if (!map.has(key)) map.set(key, String(map.size + 1));
  return map.get(key)!;
};

export function modelOptions(models: Model[]): ModelOptionsDTO {
  const vendors = new Map<string, ModelOptionsDTO["vendors"][number]>();
  const families = new Map<string, ModelOptionsDTO["families"][number]>();
  for (const model of models) {
    const vendor_id = identifier(vendorIds, model.vendor);
    const id = identifier(familyIds, `${model.vendor}\0${model.family}`);
    vendors.set(vendor_id, { id: vendor_id, name: model.vendor });
    families.set(id, { id, vendor_id, name: model.family });
  }
  return { vendors: [...vendors.values()], families: [...families.values()] };
}

export function modelSku(model: Model): ModelSkuContractDTO {
  modelOptions([model]);
  const legacyCapability: ModelCapabilityDTO = {};
  for (const key of Object.keys(capabilityLabels) as Array<keyof typeof capabilityLabels>)
    legacyCapability[key] = model.capabilities.includes(capabilityLabels[key]) || (key === "reasoning" && model.type === "推理");
  return {
    id: model.id, vendor_id: vendorIds.get(model.vendor)!, vendor_name: model.vendor,
    family_id: familyIds.get(`${model.vendor}\0${model.family}`)!, family_name: model.family,
    sku_code: model.code, model_type: model.type, native_currency: model.currency,
    context_window: model.context,
    capability: model.capability === undefined ? legacyCapability : structuredClone(model.capability),
    verify_status: model.verification.some((item) => item.result === "PASS") ? "MANUAL" : "UNVERIFIED",
    tier_tag: model.tier || null, is_sensitive: model.sensitive, cross_border: model.crossBorder,
    lifecycle_status: model.status, sunset_date: null, aliases: [...model.aliases],
  };
}

export function modelDetail(model: Model) {
  return {
    sku: modelSku(model),
    mock_extensions: {
      protocol: model.protocol, verification: structuredClone(model.verification),
      pending_retirement: !!model.pendingRetirement,
    },
  };
}

export function modelWriteFields(body: unknown, models: Model[], current?: Model) {
  try { validateModelSkuRequest(body, !!current); }
  catch (error) { throw new ApiError(400, error instanceof Error ? error.message : "模型请求不合法"); }
  const value = body as CreateModelSkuRequest & UpdateModelSkuRequest;
  const options = modelOptions(models);
  const vendor = current ? { name: current.vendor } : options.vendors.find((item) => item.id === String(value.vendor_id));
  const family = current ? { name: current.family } : options.families.find((item) => item.id === String(value.family_id) && item.vendor_id === String(value.vendor_id));
  if (!vendor || !family) throw new ApiError(400, "厂商或系列不存在，或系列不属于所选厂商");
  const code = value.sku_code.trim();
  const aliases = value.aliases === undefined ? [...(current?.aliases ?? [])] : value.aliases.map((item) => item.trim());
  const names = [code, ...aliases].map((item) => item.toLowerCase());
  if (new Set(names).size !== names.length || models.some((model) => model.id !== current?.id &&
      [model.code, ...model.aliases].some((name) => names.includes(name.toLowerCase())))) throw new ApiError(400, "SKU 编码或别名已存在");
  const capability = value.capability === undefined ? current?.capability ?? (current ? modelSku(current).capability : null) : structuredClone(value.capability);
  return {
    code, name: current?.name ?? code, vendor: vendor.name, family: family.name,
    type: value.model_type, currency: value.native_currency,
    context: value.context_window ?? current?.context ?? null,
    capability,
    capabilities: (Object.keys(capabilityLabels) as Array<keyof typeof capabilityLabels>)
      .filter((key) => capability?.[key] === true).map((key) => capabilityLabels[key]),
    protocol: current?.protocol ?? "未提供（SKU 契约无此字段）",
    tier: value.tier_tag ?? current?.tier ?? "",
    sensitive: value.is_sensitive ?? current?.sensitive ?? false,
    crossBorder: value.cross_border ?? current?.crossBorder ?? false,
    aliases,
  };
}

export function queryModelCatalog(models: Model[], params: ModelContractListParams) {
  const allowed = new Set(["view", "keyword", "vendor_id", "family_id", "model_type", "lifecycle_status", "tier_tag", "page", "size"]);
  if (Object.keys(params).some((key) => !allowed.has(key))) throw new Error("列表请求包含不支持的筛选参数。");
  const page = params.page ?? 1;
  const size = params.size ?? 20;
  if (!Number.isInteger(page) || page < 1 || !Number.isInteger(size) || size < 1 || size > 100)
    throw new Error("页码必须大于 0，每页数量必须是 1~100。");
  if (params.view !== undefined && !["sku", "family"].includes(params.view)) throw new Error("列表视图不合法。");
  if (params.keyword !== undefined && (!params.keyword.trim() || params.keyword.length > 64)) throw new Error("搜索关键字必须是 1~64 个字符。");
  if (params.lifecycle_status && !Object.hasOwn(statuses, params.lifecycle_status)) throw new Error("模型状态不合法。");
  if (params.tier_tag && !["旗舰", "主力", "经济", "长尾"].includes(params.tier_tag)) throw new Error("分级标签不合法。");
  for (const id of [params.vendor_id, params.family_id]) {
    if (id !== undefined && (!/^[1-9]\d*$/.test(String(id)) || (typeof id === "number" && !Number.isSafeInteger(id))))
      throw new Error("厂商或系列 ID 不合法。");
  }
  modelOptions(models);
  const skus = models.map(modelSku);
  const keyword = params.keyword?.toLowerCase();
  const matched = skus.filter((sku) =>
    (!keyword || `${sku.sku_code} ${sku.family_name} ${(sku.aliases ?? []).join(" ")}`.toLowerCase().includes(keyword)) &&
    (!params.vendor_id || String(sku.vendor_id) === String(params.vendor_id)) &&
    (!params.family_id || String(sku.family_id) === String(params.family_id)) &&
    (!params.model_type || sku.model_type === params.model_type) &&
    (!params.lifecycle_status || sku.lifecycle_status === params.lifecycle_status) &&
    (!params.tier_tag || sku.tier_tag === params.tier_tag));
  const families = new Map<string, ModelFamilyContractDTO>();
  for (const sku of matched) {
    const key = String(sku.family_id);
    if (!families.has(key)) families.set(key, { family_id: sku.family_id, family_name: sku.family_name,
      vendor_id: sku.vendor_id, vendor_name: sku.vendor_name, sku_count: 0, children: [] });
    const family = families.get(key)!;
    family.children.push(sku);
    family.sku_count++;
  }
  const entries = (params.view ?? "family") === "family" ? [...families.values()] : matched;
  const offset = (page - 1) * size;
  return { list: entries.slice(offset, offset + size), total: entries.length, page, size };
}
