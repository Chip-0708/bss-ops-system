// Package model 是模型管理 M1 的领域层：SKU / 系列 / 别名 / 退役影响清单。
// capability 结构按 docs/api/04-models.md §0.1 固定 key 集合，未知 key 拒绝写入。
package model

import (
	"errors"
	"fmt"
	"time"
)

// Capability 是 model_sku.capability 的固定结构（§0.1 已定稿）。
// 未知 key 一律拒绝（ErrUnknownCapabilityKey），避免 jsonb 变成黑盒。
type Capability struct {
	FunctionCall    *bool `json:"function_call,omitempty"`
	Vision          *bool `json:"vision,omitempty"`
	Audio           *bool `json:"audio,omitempty"`
	Video           *bool `json:"video,omitempty"`
	Embedding       *bool `json:"embedding,omitempty"`
	Reasoning       *bool `json:"reasoning,omitempty"`
	JSONMode        *bool `json:"json_mode,omitempty"`
	Streaming       *bool `json:"streaming,omitempty"`
	MaxOutputTokens *int  `json:"max_output_tokens,omitempty"`
}

// capabilityKeys 是 §0.1 允许的 9 个 key。
var capabilityKeys = map[string]struct{}{
	"function_call":     {},
	"vision":            {},
	"audio":             {},
	"video":             {},
	"embedding":         {},
	"reasoning":         {},
	"json_mode":         {},
	"streaming":         {},
	"max_output_tokens": {},
}

// ErrUnknownCapabilityKey capability 含未知 key。
var ErrUnknownCapabilityKey = errors.New("unknown capability key")

// ValidateCapabilityKeys 校验原始 map 的 key 集合；未知 key → ErrUnknownCapabilityKey。
func ValidateCapabilityKeys(raw map[string]interface{}) error {
	for k := range raw {
		if _, ok := capabilityKeys[k]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownCapabilityKey, k)
		}
	}
	return nil
}

// 生命周期状态（设计文档 §9 状态机，禁止自创）。
const (
	LifecycleDraft         = "DRAFT"
	LifecyclePendingVerify = "PENDING_VERIFY"
	LifecyclePurchasable   = "PURCHASABLE"
	LifecyclePublished     = "PUBLISHED"
	LifecycleDeprecating   = "DEPRECATING"
	LifecycleOffline       = "OFFLINE"
)

// 验证状态。
const (
	VerifyUnverified = "UNVERIFIED"
	VerifyManual     = "MANUAL"
	VerifyProbed     = "PROBED"
)

// SKU 是 model_sku 的领域模型。
type SKU struct {
	ID              int64       `json:"id"`
	VendorID        int64       `json:"vendor_id"`
	FamilyID        int64       `json:"family_id"`
	SkuCode         string      `json:"sku_code"`
	ModelType       string      `json:"model_type"`
	NativeCurrency  string      `json:"native_currency"`
	ContextWindow   *int        `json:"context_window"`
	Capability      *Capability `json:"capability"`
	VerifyStatus    string      `json:"verify_status"`
	TierTag         *string     `json:"tier_tag"`
	Tags            []string    `json:"tags"`
	IsSensitive     bool        `json:"is_sensitive"`
	CrossBorder     bool        `json:"cross_border"`
	LifecycleStatus string      `json:"lifecycle_status"`
	SunsetDate      *time.Time  `json:"sunset_date"`
	// 列表冗余展示字段
	VendorName string   `json:"vendor_name"`
	FamilyName string   `json:"family_name"`
	Aliases    []string `json:"aliases"`
}

// CreateSKUInput 是创建 SKU 的入参（§2）。
type CreateSKUInput struct {
	VendorID       int64                  `json:"vendor_id" binding:"required"`
	FamilyID       int64                  `json:"family_id" binding:"required"`
	SkuCode        string                 `json:"sku_code" binding:"required,min=1,max=128"`
	ModelType      string                 `json:"model_type" binding:"required"`
	NativeCurrency string                 `json:"native_currency" binding:"required,len=3"`
	ContextWindow  *int                   `json:"context_window"`
	Capability     map[string]interface{} `json:"capability"`
	TierTag        *string                `json:"tier_tag"`
	IsSensitive    bool                   `json:"is_sensitive"`
	CrossBorder    bool                   `json:"cross_border"`
	Aliases        []string               `json:"aliases"`
}

// UpdateSKUInput 是维护 SKU 的入参（§3）。
// lifecycle_status 不可直接改，须走 publish / deprecate / batch 流程。
type UpdateSKUInput struct {
	SkuCode        *string                `json:"sku_code"`
	ModelType      *string                `json:"model_type"`
	NativeCurrency *string                `json:"native_currency"`
	ContextWindow  *int                   `json:"context_window"`
	Capability     map[string]interface{} `json:"capability"`
	TierTag        *string                `json:"tier_tag"`
	IsSensitive    *bool                  `json:"is_sensitive"`
	CrossBorder    *bool                  `json:"cross_border"`
	Aliases        []string               `json:"aliases"`
}

// 领域错误。
var (
	// ErrDuplicateSkuCode sku_code 全局唯一冲突。
	ErrDuplicateSkuCode = errors.New("sku_code already exists")
	// ErrDuplicateAlias 别名全局唯一冲突。
	ErrDuplicateAlias = errors.New("alias already exists")
	// ErrFamilyVendorMismatch family 不属于该 vendor。
	ErrFamilyVendorMismatch = errors.New("family does not belong to vendor")
	// ErrNotFound SKU 不存在。
	ErrNotFound = errors.New("model sku not found")
	// ErrMergeSameSKU 合并时 target 与 source 相同。
	ErrMergeSameSKU = errors.New("target and source sku must differ")
	// ErrSourceReferenced 被合并 SKU 仍被价目表/报价引用。
	ErrSourceReferenced = errors.New("source sku is referenced")
	// ErrBatchTooLarge 批量超过 200。
	ErrBatchTooLarge = errors.New("batch exceeds 200 skus")
	// ErrBatchActionInvalid 批量动作非法（含上架/退役走批量）。
	ErrBatchActionInvalid = errors.New("batch action not allowed")
	// ErrPublishStateInvalid 上架前置状态非 PURCHASABLE。
	ErrPublishStateInvalid = errors.New("sku is not PURCHASABLE")
	// ErrPublishConflict 并发上架冲突（条件更新 0 行）。
	ErrPublishConflict = errors.New("publish conflict: state changed concurrently")
	// ErrSnapshotInvalid impact snapshot 不存在/过期/不属于该 sku。
	ErrSnapshotInvalid = errors.New("impact snapshot invalid or expired")
	// ErrDeprecateStateInvalid 发起退役时状态非 PUBLISHED/PURCHASABLE。
	ErrDeprecateStateInvalid = errors.New("deprecate requires PUBLISHED or PURCHASABLE")
	// ErrApproverRoleMismatch 审批人角色不匹配 required_role。
	ErrApproverRoleMismatch = errors.New("approver role mismatch")
	// ErrSelfApproval 同一操作员完成同一单的两个审批步骤（§3.3 禁止）。
	ErrSelfApproval = errors.New("self-approval prohibited: same operator on both steps")
	// ErrDeprecateConflict 并发转 DEPRECATING 冲突。
	ErrDeprecateConflict = errors.New("deprecate conflict: state changed concurrently")
)

// AliasInput 是别名维护（§4）请求体：全量覆盖语义。
type AliasInput struct {
	Aliases []string `json:"aliases" binding:"required"`
	Source  string   `json:"source"`
}

// MergeInput 是一键合并为别名（§5）请求体。
type MergeInput struct {
	TargetSkuID int64  `json:"target_sku_id" binding:"required"`
	SourceSkuID int64  `json:"source_sku_id" binding:"required"`
	Alias       string `json:"alias" binding:"required,min=1,max=128"`
}

// SuggestResult 是查重建议（§4）返回。
type SuggestResult struct {
	Suggestions []Suggestion `json:"suggestions"`
}

// Suggestion 是单条查重建议。
type Suggestion struct {
	SkuID   int64   `json:"sku_id"`
	SkuCode string  `json:"sku_code"`
	Score   float64 `json:"score"`
}

// BatchAction 批量动作枚举。
const (
	BatchSubmitVerify = "SUBMIT_VERIFY"
	BatchSetTier      = "SET_TIER"
	BatchAddTag       = "ADD_TAG"
	BatchRemoveTag    = "REMOVE_TAG"
)

// BatchInput 是批量改状态/打标签（§6）请求体。
type BatchInput struct {
	SkuIDs  []int64                `json:"sku_ids" binding:"required,min=1"`
	Action  string                 `json:"action" binding:"required"`
	Payload map[string]interface{} `json:"payload"`
}

// BatchResult 是批量结果（部分成功语义）。
type BatchResult struct {
	Total     int           `json:"total"`
	Succeeded int           `json:"succeeded"`
	Failed    []BatchFailed `json:"failed"`
}

// BatchFailed 是单项失败明细。
type BatchFailed struct {
	SkuID   int64  `json:"sku_id"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// PublishResult 是上架（§9）返回。
type PublishResult struct {
	SkuID           int64     `json:"sku_id"`
	LifecycleStatus string    `json:"lifecycle_status"`
	PublishedAt     time.Time `json:"published_at"`
}

// ---- 阶段 4b-2：退役链路 ----

// change_type 与 risk_level 常量（设计文档 §2.4.6）。
const (
	ChangeDeprecate = "DEPRECATE"
	RiskLow         = "LOW"
	RiskMid         = "MID"
	RiskHigh        = "HIGH"
)

// change_request.status 常量。
const (
	CRPending   = "PENDING"
	CRApproved  = "APPROVED"
	CRRejected  = "REJECTED"
	CREffective = "EFFECTIVE"
)

// ApprovalDecision 审批动作值。
const (
	DecisionApproved = "APPROVED"
	DecisionRejected = "REJECTED"
)

// DeprecationImpactResult 是影响分析（§7）返回。
type DeprecationImpactResult struct {
	SkuID          int64            `json:"sku_id"`
	SnapshotID     string           `json:"snapshot_id"`
	References     map[string][]any `json:"references"`
	ReferenceCount int              `json:"reference_count"`
	Replacements   []Replacement    `json:"replacements"`
	GeneratedAt    time.Time        `json:"generated_at"`
}

// Replacement 是推荐替代 SKU。
type Replacement struct {
	SkuID   int64  `json:"sku_id"`
	SkuCode string `json:"sku_code"`
	Reason  string `json:"reason"`
}

// DeprecateInput 是发起退役（§8）请求体。
type DeprecateInput struct {
	ImpactSnapshotID string `json:"impact_snapshot_id" binding:"required"`
	SunsetDate       string `json:"sunset_date" binding:"required"`
	Reason           string `json:"reason" binding:"required,min=1,max=500"`
	ReplacementSkuID *int64 `json:"replacement_sku_id"`
}

// DeprecateResult 是发起退役返回。
type DeprecateResult struct {
	SkuID           int64  `json:"sku_id"`
	ApprovalID      int64  `json:"approval_id"` // = change_request.id
	SunsetDate      string `json:"sunset_date"`
	LifecycleStatus string `json:"lifecycle_status"`
}

// DecisionInput 是审批动作请求体。
type DecisionInput struct {
	StepNo   int    `json:"step_no" binding:"required"`
	Decision string `json:"decision" binding:"required,oneof=APPROVED REJECTED"`
	Comment  string `json:"comment" binding:"max=512"`
}

// DecisionResult 是审批动作返回。
type DecisionResult struct {
	ChangeRequestID int64  `json:"change_request_id"`
	StepNo          int    `json:"step_no"`
	Decision        string `json:"decision"`
	FinalStatus     string `json:"final_status"` // change_request.status
}
