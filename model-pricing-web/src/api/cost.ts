import type { PageResult } from "../domain/common";
import { apiRequest } from "./http";
import type { CurrentCostBaselineDTO, CurrentCostListParams } from "./cost.types";

// 成本基线只读列表（06-cost.md §2）。详情/比价接口后端尚未提供，本模块只保留列表。
const basePath = "/internal/cost/baselines";

export const costApi = {
  currentList: (params: CurrentCostListParams) =>
    apiRequest<PageResult<CurrentCostBaselineDTO>>({ url: basePath, method: "GET", params }),
};
