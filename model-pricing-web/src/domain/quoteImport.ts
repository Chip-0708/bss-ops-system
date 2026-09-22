import Decimal from "decimal.js";
import type { QuoteId, QuoteImportPreviewDTO, SupplierSkuDTO } from "../api/quoteContract.types";

export interface QuoteVendorOption {
  id: QuoteId;
  name: string;
}

export interface QuoteFamilyOption {
  id: QuoteId;
  name: string;
  vendorId: QuoteId;
  vendorName: string;
}

export function quoteScopeOptions(skus: SupplierSkuDTO[]): { vendors: QuoteVendorOption[]; families: QuoteFamilyOption[] } {
  const vendors = new Map<string, QuoteVendorOption>();
  const families = new Map<string, QuoteFamilyOption>();
  for (const sku of skus) {
    vendors.set(String(sku.vendor_id), { id: sku.vendor_id, name: sku.vendor_name });
    families.set(String(sku.family_id), {
      id: sku.family_id,
      name: sku.family_name,
      vendorId: sku.vendor_id,
      vendorName: sku.vendor_name,
    });
  }
  const byName = <T extends { name: string }>(left: T, right: T) => left.name.localeCompare(right.name, "zh-CN");
  return { vendors: [...vendors.values()].sort(byName), families: [...families.values()].sort(byName) };
}

export function filterPreviewByScope(
  preview: QuoteImportPreviewDTO,
  skus: SupplierSkuDTO[],
  scope: "history" | "active" | "vendor" | "family",
  vendorId: string,
  familyId: string,
): { preview: QuoteImportPreviewDTO; excluded: number } {
  if (scope !== "vendor" && scope !== "family") return { preview, excluded: 0 };
  const allowed = new Set(skus
    .filter(sku => scope === "vendor" ? String(sku.vendor_id) === vendorId : String(sku.family_id) === familyId)
    .map(sku => String(sku.id)));
  const rows = preview.rows.filter(row => allowed.has(String(row.sku_id)));
  const previewItems = preview.preview_items.filter(item => allowed.has(String(item.sku_id)));
  return {
    preview: {
      ...preview,
      total: rows.length,
      ok_count: rows.filter(row => row.level === "OK").length,
      warn_count: rows.filter(row => row.level === "WARN").length,
      error_count: rows.filter(row => row.level === "ERROR").length,
      rows,
      preview_items: previewItems,
    },
    excluded: preview.rows.length - rows.length,
  };
}

export function includedPreviewItems(items: QuoteImportPreviewDTO["preview_items"], excludedSkuIds: ReadonlySet<string>) {
  return items.filter(item => !excludedSkuIds.has(String(item.sku_id)));
}

export function importedPrice(officialPrice: string | undefined, multiplier: string | null): string {
  if (!officialPrice || multiplier === null) return "";
  try {
    const value = new Decimal(multiplier);
    return value.isFinite() && value.gt(0) ? new Decimal(officialPrice).times(value).toFixed(8) : "";
  } catch {
    return "";
  }
}

export function effectiveTimeWillClamp(value: string, now = Date.now()): boolean {
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) && timestamp < now;
}
