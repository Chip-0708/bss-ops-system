import type { SupplierQuoteStatus } from "../domain/status";
export type QuoteId = string | number;
export const QUOTE_COMPONENTS = ["input", "output", "cached_input", "cache_write_5m", "cache_write_1h", "reasoning", "embedding", "request", "image_input", "image_output", "audio_input", "audio_output"] as const;
export type QuoteComponentType = typeof QUOTE_COMPONENTS[number];
export const QUOTE_FX_TIERS = ["6.5", "6.7", "6.75", "6.8", "6.85", "6.9", "6.95", "7.0"] as const;
export interface QuoteConstraints {
    rpm?: number | null;
    tpm?: number | null;
    concurrency?: number | null;
    daily_quota?: number | null;
    actual_context?: number | null;
    compatibility?: string | null;
}
export interface QuoteComponent {
    component_type: QuoteComponentType;
    multiplier: string | null;
    unit_price: string;
}
export interface QuoteWriteItem {
    sku_id: QuoteId;
    fx_tier?: string | null;
    constraints?: QuoteConstraints | null;
    components: QuoteComponent[];
}
export interface QuoteSubmitRequest {
    valid_from: string;
    valid_to: string;
    remark?: string;
    items: QuoteWriteItem[];
}
export interface SupplierSkuDTO {
    id: QuoteId;
    sku_code: string;
    model_name: string;
    vendor_id: QuoteId;
    vendor_name: string;
    family_id: QuoteId;
    family_name: string;
    model_type: string;
    native_currency: string;
    context_window: number | null;
    tier_tag: string | null;
    has_official_price: boolean;
    official_price: {
        version_no: number;
        currency: string;
        tax_basis: string;
        components: Array<{
            component_type: QuoteComponentType;
            unit_price: string;
        }>;
    } | null;
}
export interface QuoteDecision {
    result: "APPROVED" | "REJECTED";
    reason: string | null;
    decided_at: string;
}
export interface QuoteHistoryDTO {
    id: QuoteId;
    version_no: number;
    status: SupplierQuoteStatus;
    valid_from: string;
    valid_to: string;
    source: string;
    item_count: number;
    submitted_at: string | null;
    decision: QuoteDecision | null;
}
export interface QuoteDetailDTO extends QuoteHistoryDTO {
    supplier_id: QuoteId;
    remark?: string | null;
    audit_reason?: string | null;
    items: Array<QuoteWriteItem & {
        sku_code: string;
        model_name: string;
        currency: string;
    }>;
}
export interface QuoteSubmitResult extends Omit<QuoteHistoryDTO, "decision"> {
    supplier_id: QuoteId;
    status: "APPROVING";
    retroactive: boolean;
    clamped: boolean;
}
export interface QuotePendingDTO {
    id: QuoteId;
    supplier_id: QuoteId;
    supplier_name: string;
    version_no: number;
    item_count: number;
    source: string;
    retroactive: boolean;
    valid_from: string;
    valid_to: string;
    submitted_at: string;
    wait_hours: number;
    has_previous: boolean;
}
export interface QuoteDiffDTO {
    quote_sheet_id: QuoteId;
    supplier_id: QuoteId;
    version_no: number;
    distortion: boolean;
    distortion_note: string | null;
    items: Array<{
        sku_id: QuoteId;
        sku_code: string;
        currency: string;
        components: Array<QuoteComponent & {
            prev_price: string | null;
            prev_delta_pct: string | null;
            official_price: string | null;
            official_delta_pct: string | null;
            market_best: string | null;
            market_delta_pct: string | null;
        }>;
        margin_preview: {
            floor_price: string | null;
            reference_sell_price: string | null;
            margin_ok: boolean | null;
            note: string;
        };
    }>;
}
export interface QuoteImportPreviewDTO {
    token: string;
    total: number;
    ok_count: number;
    warn_count: number;
    error_count: number;
    rows: Array<{
        line: number;
        sku_id: QuoteId;
        sku_code: string;
        level: "OK" | "WARN" | "ERROR";
        messages: string[];
    }>;
    preview_items: Array<QuoteWriteItem & {
        currency: string;
    }>;
}
