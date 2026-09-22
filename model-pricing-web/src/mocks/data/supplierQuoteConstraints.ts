import { ApiError } from "../../types.ts";
import type { SupplierQuoteSupplyConstraints } from "../../api/supplierPortal.types";

export function validateSupplierQuoteConstraints(value: SupplierQuoteSupplyConstraints | undefined) {
  if (value === undefined) return;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ApiError(400, "供给约束格式不正确");
  const allowed = ["maxConcurrency", "rpm", "tpm", "actualContextWindow", "compatibilityNote"];
  if (Object.keys(value).some(key => !allowed.includes(key))) throw new ApiError(400, "供给约束包含不支持的字段");
  for (const key of allowed.slice(0, 4)) {
    const field = value[key as keyof SupplierQuoteSupplyConstraints];
    if (field !== undefined && (typeof field !== "string" || !/^[1-9]\d{0,9}$/.test(field))) throw new ApiError(400, "并发、RPM、TPM和上下文必须是最多10位的正整数字符串；不限定请留空");
  }
  if (value.compatibilityNote !== undefined && (typeof value.compatibilityNote !== "string" || value.compatibilityNote.length > 300)) throw new ApiError(400, "兼容说明最多300字");
}
