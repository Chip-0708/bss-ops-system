import type { IsoDateTime } from "../domain/common";
import type { Money } from "../domain/money";
import type { ModelStatus } from "../domain/status";

export interface VerificationDTO {
  by: string;
  at: IsoDateTime;
  result: "PASS" | "FAIL";
  note: string;
}

export interface InternalModelDTO {
  id: string;
  code: string;
  name: string;
  vendor: string;
  family: string;
  type: string;
  context: number | null;
  capabilities: string[];
  protocol: string;
  tier: string;
  sensitive: boolean;
  crossBorder: boolean;
  currency: string;
  input: Money;
  output: Money;
  status: ModelStatus;
  aliases: string[];
  verification: VerificationDTO[];
  pendingRetirement?: boolean;
}

export interface PricingInternalModelDTO extends InternalModelDTO {
  cost: Money;
  margin: string;
}

export type Model = InternalModelDTO | PricingInternalModelDTO;

export type ModelListParams = ModelContractListParams;

// List projection has no official price, protocol or verification history fields.
export interface ModelListItem extends Pick<InternalModelDTO,
  "id" | "code" | "name" | "vendor" | "family" | "type" | "capabilities" |
  "sensitive" | "crossBorder" | "currency" | "status" | "aliases"> {
  vendorId: string;
  familyId: string;
  context: number | null;
  tier: string | null;
  verifyStatus: ModelSkuContractDTO["verify_status"];
  capabilitiesProvided: boolean;
  capability: ModelCapabilityDTO | null;
}

export interface ModelFamilyContractDTO {
  family_id: ModelContractId;
  family_name: string;
  vendor_id: ModelContractId;
  vendor_name: string;
  sku_count: number;
  children: ModelSkuContractDTO[];
}

export interface ModelOptionsDTO {
  vendors: Array<{ id: string; name: string }>;
  families: Array<{ id: string; vendor_id: string; name: string }>;
}

export type Draft = Pick<
  InternalModelDTO,
  | "code"
  | "name"
  | "vendor"
  | "family"
  | "type"
  | "context"
  | "capabilities"
  | "protocol"
  | "tier"
  | "sensitive"
  | "crossBorder"
>;

export interface Impact {
  id: string;
  modelId: string;
  books: string[];
  contracts: string[];
  quotes: string[];
  alternatives: string[];
  notifiedAt: IsoDateTime;
}

export interface Retirement {
  id: string;
  modelId: string;
  status: "APPROVING";
  firstApprover: string;
  offlineAt: string;
  reason: string;
}

export interface BatchResult {
  id: string;
  code: string;
  ok: boolean;
  reason: string;
}

export interface ModelSuggestionDTO {
  id: string;
  code: string;
  name: string;
}

// Backend wire DTOs from supplied 04-models.md (2026-09-11).
// int64 JSON encoding is TBD: never coerce an ID string into a JS number.
export type ModelContractId = number | string;

export interface ModelCapabilityDTO {
  function_call?: boolean;
  vision?: boolean;
  audio?: boolean;
  video?: boolean;
  embedding?: boolean;
  reasoning?: boolean;
  json_mode?: boolean;
  streaming?: boolean;
  max_output_tokens?: number | null;
}

export interface ModelSkuContractDTO {
  id: ModelContractId;
  vendor_id: ModelContractId;
  vendor_name: string;
  family_id: ModelContractId;
  family_name: string;
  sku_code: string;
  model_type: string;
  native_currency: string;
  context_window: number | null;
  capability: ModelCapabilityDTO | null;
  verify_status: "UNVERIFIED" | "MANUAL" | "PROBED";
  tier_tag: string | null;
  is_sensitive: boolean;
  cross_border: boolean;
  lifecycle_status: ModelStatus;
  sunset_date: string | null;
  aliases: string[] | null;
}

export interface ModelContractListParams {
  view?: "family" | "sku";
  keyword?: string;
  vendor_id?: ModelContractId;
  family_id?: ModelContractId;
  model_type?: string;
  lifecycle_status?: ModelStatus;
  tier_tag?: string;
  page?: number;
  size?: number;
}

// PROVISIONAL detail envelope; extensions are outside the confirmed SKU contract.
export interface ModelDetailDTO {
  sku: ModelSkuContractDTO;
  mock_extensions?: {
    protocol: string;
    verification: VerificationDTO[];
    pending_retirement: boolean;
  };
}

// Family view is explicitly a Mock provisional schema until backend confirmation.
export interface CreateModelSkuRequest {
  vendor_id: ModelContractId;
  family_id: ModelContractId;
  sku_code: string;
  model_type: string;
  native_currency: string;
  context_window?: number;
  capability?: ModelCapabilityDTO;
  tier_tag?: string;
  is_sensitive?: boolean;
  cross_border?: boolean;
  aliases?: string[];
}

export type UpdateModelSkuRequest = Omit<CreateModelSkuRequest, "vendor_id" | "family_id">;

export interface ModelAliasSuggestionsResponseDTO {
  suggestions: Array<{ sku_id: ModelContractId; sku_code: string; score: number }>;
}

export interface MergeModelAliasRequest {
  target_sku_id: ModelContractId;
  source_sku_id: ModelContractId;
  alias: string;
}

export interface MergeModelAliasResponseDTO {
  target_sku_id: ModelContractId;
  merged_sku_id: ModelContractId;
  alias_id: ModelContractId;
  alias: string;
}

export interface ModelBatchRequest {
  sku_ids: ModelContractId[];
  action: "SUBMIT_VERIFY" | "SET_TIER" | "ADD_TAG" | "REMOVE_TAG";
  payload?: { tier_tag?: string; tag?: string };
}

export interface ModelBatchResponseDTO {
  total: number;
  succeeded: number;
  failed: Array<{ sku_id: ModelContractId; code: number; message: string }>;
}

export interface ModelDeprecationImpactDTO {
  // Required by section 10's final decision, though omitted from section 7's example.
  snapshot_id: string;
  sku_id: ModelContractId;
  references: {
    price_books: Array<{ id: ModelContractId; name: string; level_code: string }>;
    contracts: Array<{ id: ModelContractId; customer_name: string; expire_at: string }>;
    customer_quotes: Array<{ id: ModelContractId; status: string }>;
  };
  reference_count: number;
  replacements: Array<{ sku_id: ModelContractId; sku_code: string; reason: string }>;
  generated_at: IsoDateTime;
}

export interface DeprecateModelRequest {
  impact_snapshot_id: string;
  sunset_date: string;
  reason: string;
  replacement_sku_id?: ModelContractId;
}

export interface DeprecateModelResponseDTO {
  sku_id: ModelContractId;
  lifecycle_status: "PUBLISHED";
  approval_id: ModelContractId;
  sunset_date: string;
}

export interface PublishModelResponseDTO {
  sku_id: ModelContractId;
  lifecycle_status: "PUBLISHED";
  published_at: IsoDateTime;
}
