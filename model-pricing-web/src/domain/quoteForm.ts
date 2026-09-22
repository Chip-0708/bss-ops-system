import Decimal from "decimal.js";
import type { QuoteSubmitRequest } from "../api/quoteContract.types";
import { QUOTE_COMPONENTS, QUOTE_FX_TIERS } from "../api/quoteContract.types";
import { isDecimalAmount } from "./money";
export function quoteFormError(payload: QuoteSubmitRequest): string {
    const zoned = /(?:Z|[+-]\d{2}:\d{2})$/;
    if (![payload.valid_from, payload.valid_to].every(value => zoned.test(value) && Number.isFinite(Date.parse(value))))
        return "请选择带时区的生效和结束时间。";
    if (Date.parse(payload.valid_to) <= Math.max(Date.parse(payload.valid_from), Date.now()))
        return "结束时间必须晚于生效时间和当前时间。";
    if (Array.from(payload.remark || "").length > 500)
        return "报价说明不能超过500字。";
    if (!payload.items.length || payload.items.length > 500)
        return "报价必须包含1—500个SKU。";
    if (new Set(payload.items.map(item => String(item.sku_id))).size !== payload.items.length)
        return "同一SKU只能出现一次。";
    for (const item of payload.items) {
        if (item.fx_tier && (!isDecimalAmount(item.fx_tier) || !QUOTE_FX_TIERS.some(tier => new Decimal(tier).eq(item.fx_tier!))))
            return "请选择有效汇率档位。";
        if (!item.components.length || new Set(item.components.map(component => component.component_type)).size !== item.components.length)
            return "每个SKU至少包含一个组件，组件不能重复。";
        for (const component of item.components) {
            if (!QUOTE_COMPONENTS.includes(component.component_type) || !isDecimalAmount(component.unit_price))
                return "组件供应价须为有效正数，最多8位小数。";
            if (component.multiplier !== null && !isDecimalAmount(component.multiplier))
                return "倍率须为有效正数，最多8位小数。";
        }
        for (const [key, value] of Object.entries(item.constraints || {})) {
            if (key !== "compatibility" && value !== null && (!Number.isSafeInteger(value) || Number(value) <= 0))
                return "供给约束须为安全范围内的正整数。";
        }
    }
    return "";
}
