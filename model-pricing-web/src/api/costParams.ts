import { apiRequest } from "./http";
import type {
  CostParamsViewDTO,
  ReplaceCostParamsRequest,
  ReplaceCostParamsResultDTO,
} from "./costParams.types";

// 成本参数读写（06-cost.md §7）。GET 返回 defaults(GLOBAL 只读) + overrides[]；
// PUT 全量替换 overrides（后端事务内删除全部 MODEL/SUPPLIER 再逐行写入 + 触发受影响 SKU 重算）。
export const costParamsApi = {
  get: () => apiRequest<CostParamsViewDTO>({ url: "/internal/cost/params", method: "GET" }),
  replace: (payload: ReplaceCostParamsRequest) =>
    apiRequest<ReplaceCostParamsResultDTO>({
      url: "/internal/cost/params",
      method: "PUT",
      data: payload,
      idempotency: { scope: "cost-params:replace", payload },
    }),
};
