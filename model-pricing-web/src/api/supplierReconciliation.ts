import type { PageResult } from "../domain/common";
import { apiRequest } from "./http";
import type {
  SupplierReconciliationDetailDTO,
  SupplierReconciliationListParams,
  SupplierReconciliationSummaryDTO,
} from "./supplierReconciliation.types";

// PROVISIONAL: paths, amount fields and statement rules must be confirmed with the Go backend.
const basePath = "/supplier/reconciliation";

export const supplierReconciliationApi = {
  list: (params: SupplierReconciliationListParams) =>
    apiRequest<PageResult<SupplierReconciliationSummaryDTO>>({
      url: basePath,
      method: "GET",
      params,
    }),
  get: (id: string) =>
    apiRequest<SupplierReconciliationDetailDTO>({
      url: `${basePath}/${id}`,
      method: "GET",
    }),
};
