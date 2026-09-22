import type { IsoDateTime } from "../domain/common";
import type { Money } from "../domain/money";

/** Go generateDraftBody wire contract. */
export interface GeneratePriceBookWireDTO {
  level_code: string;
  currency: string;
  policy_ids?: number[];
  sku_ids?: number[];
}

/** Go pricing.DiffItem wire contract. */
export interface PriceBookDiffWireDTO {
  sku_id: string | number;
  sku_code: string;
  old_price: Money | null;
  new_price: Money;
  delta_pct: string | null;
  floor_price: Money;
  floor_violation: boolean;
}

/** Go pricing.PriceBookDraft wire contract. */
export interface GeneratedPriceBookWireDTO {
  draft_id: string | number;
  level_code: string;
  currency: string;
  item_count: number;
  diff_report: PriceBookDiffWireDTO[];
  blocked_count: number;
}

export interface GeneratePriceBookDraft {
  levelCode: string;
  currency: string;
  policyIds?: number[];
  skuIds?: number[];
  operationId?: string;
}

export interface PriceBookDiffDTO {
  skuId: string;
  skuCode: string;
  oldPrice: Money | null;
  newPrice: Money;
  deltaPct: string | null;
  floorPrice: Money;
  floorViolation: boolean;
}

export interface GeneratedPriceBookDTO {
  draftId: string;
  levelCode: string;
  currency: string;
  itemCount: number;
  diffReport: PriceBookDiffDTO[];
  blockedCount: number;
}

export interface PublishPriceBookWireDTO {
  effective_time: IsoDateTime;
  mode: "IMMEDIATE";
}

/** Go pricing.PublishResult wire contract. */
export interface PublishedPriceBookWireDTO {
  price_book_id: string | number;
  version_no: number;
  change_request_id: string | number;
  step_count: number;
  effective_time: IsoDateTime;
  status: "APPROVING";
}

export interface PublishPriceBookDraft {
  draftId: string;
  effectiveTime: IsoDateTime;
}

export interface PublishedPriceBookDTO {
  priceBookId: string;
  versionNo: number;
  changeRequestId: string;
  stepCount: number;
  effectiveTime: IsoDateTime;
  status: "APPROVING";
}

export interface PriceBookApprovalPayload {
  stepNo: 1 | 2;
  comment: string;
}

export interface PriceBookApprovalWireDTO {
  change_request_id: string | number;
  step_no: number;
  decision: "APPROVED";
  final_status: "PENDING" | "APPROVED";
}

export interface PriceBookApprovalDTO {
  changeRequestId: string;
  stepNo: number;
  decision: "APPROVED";
  finalStatus: "PENDING" | "APPROVED";
}
