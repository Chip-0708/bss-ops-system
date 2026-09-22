import type { PageResult } from "../domain/common";
import { apiRequest } from "./http";
import type {
  ConfirmStagingRequest,
  ConfirmStagingResultDTO,
  CreateStagingRequest,
  CreateStagingResultDTO,
  CreateSyncJobRequest,
  StagingPriceDTO,
  SyncJobDTO,
} from "./officialPrices.types";

// 官方价采集/暂存/确认（07-supplier-and-price-change.md §5/§6/§7）。
// 后端只提供「建批次 → 批量录入暂存(带实时 diff) → 勾选确认建单」；
// 变更单列表/详情/审批查询后端暂无接口，页面不在此调用。
export const officialPricesApi = {
  listJobs: (params: { page: number; size: number }) =>
    apiRequest<PageResult<SyncJobDTO>>({ url: "/internal/price-sync/jobs", method: "GET", params }),
  createJob: (payload: CreateSyncJobRequest) =>
    apiRequest<SyncJobDTO>({ url: "/internal/price-sync/jobs", method: "POST", data: payload }),
  listStaging: (params: { page: number; size: number; sync_job_id?: number }) =>
    apiRequest<PageResult<StagingPriceDTO>>({ url: "/internal/staging-prices", method: "GET", params }),
  createStaging: (payload: CreateStagingRequest) =>
    apiRequest<CreateStagingResultDTO>({
      url: "/internal/staging-prices",
      method: "POST",
      data: payload,
      idempotency: { scope: "staging-prices:create", payload },
    }),
  confirmStaging: (payload: ConfirmStagingRequest) =>
    apiRequest<ConfirmStagingResultDTO>({
      url: "/internal/staging-prices/confirm",
      method: "POST",
      data: payload,
      idempotency: { scope: "staging-prices:confirm", payload },
    }),
};
