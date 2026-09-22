import type { SupplierQuoteOptionDTO } from "../../api/supplierPortal.types";
export const quoteOptions: SupplierQuoteOptionDTO[] = [
    { skuId: "9007199254741000", key: "gpt-4.1:input", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "输入 Token", currency: "USD", unit: "百万 Token", taxMode: "未税", lastUnitPrice: "1.76000000", basisVersion: "mock-official-basis-v1", officialPrice: "2.00000000" },
    { skuId: "9007199254741000", key: "gpt-4.1:output", skuCode: "gpt-4.1", skuName: "GPT 4.1", component: "输出 Token", currency: "USD", unit: "百万 Token", taxMode: "未税", lastUnitPrice: "7.10000000", basisVersion: "mock-official-basis-v1", officialPrice: "8.00000000" },
    { skuId: "9007199254741001", key: "gpt-4.1-mini:input", skuCode: "gpt-4.1-mini", skuName: "GPT 4.1 Mini", component: "输入 Token", currency: "USD", unit: "百万 Token", taxMode: "未税", lastUnitPrice: "0.36000000", basisVersion: "mock-official-basis-v1", officialPrice: "0.40000000" },
    { skuId: "9007199254741001", key: "gpt-4.1-mini:output", skuCode: "gpt-4.1-mini", skuName: "GPT 4.1 Mini", component: "输出 Token", currency: "USD", unit: "百万 Token", taxMode: "未税", lastUnitPrice: "1.45000000", basisVersion: "mock-official-basis-v1", officialPrice: "1.60000000" },
];
