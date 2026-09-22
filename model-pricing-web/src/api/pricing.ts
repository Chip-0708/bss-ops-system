import { assertContractId } from "../domain/contractId";
import { apiRequest } from "./http";
import type {
  GeneratePriceBookDraft,
  GeneratedPriceBookDTO,
  GeneratedPriceBookWireDTO,
  PriceBookApprovalDTO,
  PriceBookApprovalPayload,
  PriceBookApprovalWireDTO,
  PublishPriceBookDraft,
  PublishedPriceBookDTO,
  PublishedPriceBookWireDTO,
} from "./pricing.types";

const basePath = "/internal/price-books";

function responseId(value: unknown) {
  assertContractId(value);
  return String(value);
}

function requestId(value: string) {
  assertContractId(value);
  return value;
}

export const pricingApi = {
  generate: (draft: GeneratePriceBookDraft) => {
    const payload = {
      level_code: draft.levelCode.trim(),
      currency: draft.currency,
      ...(draft.policyIds?.length ? { policy_ids: draft.policyIds } : {}),
      ...(draft.skuIds?.length ? { sku_ids: draft.skuIds } : {}),
    };
    return apiRequest<GeneratedPriceBookWireDTO>({
      url: basePath,
      method: "POST",
      data: payload,
      idempotency: { scope: `price-book:generate:${draft.operationId || "default"}`, payload, lifecycle: "price-book:generate" },
    }).then((data): GeneratedPriceBookDTO => ({
      draftId: responseId(data.draft_id),
      levelCode: data.level_code,
      currency: data.currency,
      itemCount: data.item_count,
      blockedCount: data.blocked_count,
      diffReport: data.diff_report.map((item) => ({
        skuId: responseId(item.sku_id),
        skuCode: item.sku_code,
        oldPrice: item.old_price,
        newPrice: item.new_price,
        deltaPct: item.delta_pct,
        floorPrice: item.floor_price,
        floorViolation: item.floor_violation,
      })),
    }));
  },
  publish: (draft: PublishPriceBookDraft) => {
    const id = requestId(draft.draftId.trim());
    const payload = { effective_time: draft.effectiveTime, mode: "IMMEDIATE" as const };
    return apiRequest<PublishedPriceBookWireDTO>({
      url: `${basePath}/${id}/publish`,
      method: "POST",
      data: payload,
      idempotency: { scope: `price-book:publish:${id}`, payload },
    }).then((data): PublishedPriceBookDTO => ({
      priceBookId: responseId(data.price_book_id),
      versionNo: data.version_no,
      changeRequestId: responseId(data.change_request_id),
      stepCount: data.step_count,
      effectiveTime: data.effective_time,
      status: data.status,
    }));
  },
  approve: (changeRequestId: string, approval: PriceBookApprovalPayload) => {
    const id = requestId(changeRequestId.trim());
    const payload = {
      step_no: approval.stepNo,
      decision: "APPROVED" as const,
      comment: approval.comment,
    };
    return apiRequest<PriceBookApprovalWireDTO>({
      url: `/internal/approvals/${id}/decision`,
      method: "POST",
      data: payload,
      idempotency: { scope: `price-book:approval:${id}:step:${approval.stepNo}`, payload },
    }).then((data): PriceBookApprovalDTO => ({
      changeRequestId: responseId(data.change_request_id),
      stepNo: data.step_no,
      decision: data.decision,
      finalStatus: data.final_status,
    }));
  },
};
