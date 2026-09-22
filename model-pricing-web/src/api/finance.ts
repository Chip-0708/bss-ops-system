import type { PageResult } from "../domain/common";
import { apiRequest } from "./http";
import type { FinanceAccountDetailDTO, FinanceAccountListParams, FinanceAccountSummaryDTO, FxRateDTO, FxRateListParams } from "./finance.types";

// PROVISIONAL: paths, query names and DTO fields must be confirmed with the Go backend.
const basePath = "/internal/finance";

export const financeApi = {
  listFxRates: (params: FxRateListParams) => apiRequest<PageResult<FxRateDTO>>({ url: `${basePath}/fx-rates`, method: "GET", params }),
  listAccounts: (params: FinanceAccountListParams) => apiRequest<PageResult<FinanceAccountSummaryDTO>>({ url: `${basePath}/accounts`, method: "GET", params }),
  getAccount: (customerId: string) => apiRequest<FinanceAccountDetailDTO>({ url: `${basePath}/accounts/${customerId}`, method: "GET" }),
};
