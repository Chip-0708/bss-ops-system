// Package supplier 的 application.go：新模型申请（C5/C12，合同 docs/api/05-quotes.md §14
// 标注「属 M1 模型主数据域」，本文件补齐供应商侧提交/查询与内部侧审核的完整闭环）。
//
// 状态机（设计文档 §3.3 状态总表 + §9 状态流转）：
//
//	SUBMITTED ──APPROVE──▶ APPROVED   （模型已入库，target_sku_id 指向该 SKU）
//	          ──MERGE────▶ MERGED     （与已有 SKU 合并，target_sku_id 指向被合并的 SKU）
//	          ──REJECT───▶ REJECTED   （驳回，须带 reason）
//
// 终态（APPROVED / MERGED / REJECTED）不可再次审核。
//
// 红线：
//   - 供应商侧严格行级过滤：只能看自己（supplier_id = 登录主体）的申请。
//   - 提交时做查重（pg_trgm 相似度 top3）落 dup_top3，供内部审核参考；
//     查重只提示不拦截（同名不同版本是常见场景）。
package supplier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 申请状态常量（与 DDL 注释、设计文档 §3.3 一致）。
const (
	AppStatusSubmitted = "SUBMITTED" // 已提交
	AppStatusMerged    = "MERGED"    // 已合并到已有 SKU
	AppStatusApproved  = "APPROVED"  // 已入库（新建 SKU）
	AppStatusRejected  = "REJECTED"  // 已关闭（驳回）
)

// 审核动作常量。
const (
	AppActionApprove = "APPROVE"
	AppActionMerge   = "MERGE"
	AppActionReject  = "REJECT"
)

// 申请相关错误。
var (
	// ErrApplicationNotFound 申请不存在（或不属于当前供应商）。
	ErrApplicationNotFound = errors.New("model application not found")
	// ErrApplicationConflict 状态冲突：终态不可再审核（并发/重复提交）。
	ErrApplicationConflict = errors.New("model application already decided")
	// ErrApplicationInvalid 入参非法（缺 model_name / payload / target_sku_id / reason）。
	ErrApplicationInvalid = errors.New("invalid model application")
	// ErrTargetSKUNotFound 审核指定的目标 SKU 不存在。
	ErrTargetSKUNotFound = errors.New("target sku not found")
)

// ModelApplication 新模型申请（对外视图；不含内部字段）。
type ModelApplication struct {
	ID           int64           `json:"id"`
	SupplierID   int64           `json:"supplier_id"`
	ModelName    string          `json:"model_name"`
	VendorID     *int64          `json:"vendor_id"`
	Payload      json.RawMessage `json:"payload"`
	DupTop3      json.RawMessage `json:"dup_top3,omitempty"`
	Status       string          `json:"status"`
	MergedSKUID  *int64          `json:"merged_sku_id"`
	RejectReason *string         `json:"reject_reason"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// SubmitApplicationInput 供应商提交申请入参。
//
// Payload 为自由结构（模型能力/上下文长度/模态/期望定价等由前端定义），
// 后端只做"必须是 JSON 对象且非空"的最低校验，避免锁死前端演进。
type SubmitApplicationInput struct {
	ModelName string          `json:"model_name"`
	VendorID  *int64          `json:"vendor_id"`
	Payload   json.RawMessage `json:"payload"`
}

// ApplicationDecisionInput 内部审核入参。
type ApplicationDecisionInput struct {
	Action string `json:"action"` // APPROVE / MERGE / REJECT
	// TargetSKUID APPROVE / MERGE 时必填：指向已存在的 model_sku。
	//   APPROVE → 该 SKU 是为本申请新建/对应的 SKU
	//   MERGE   → 被合并到的已有 SKU
	TargetSKUID *int64  `json:"target_sku_id"`
	Reason      *string `json:"reason"` // REJECT 时必填
}

// DupCandidate 查重命中的候选（提交时算，供内部审核参考）。
//
// "模型名"在本系统分散在三处，故分别比对并标明命中来源：
//   - ALIAS     model_alias.alias（归一化后的模型别名，如 "gpt-5"）——最常见命中
//   - SKU_CODE  model_sku.sku_code（含版本的编码，如 "gpt-5-2026-04-11"）
//   - FAMILY    model_family.name（系列名，如 "GPT-5 系列"）
type DupCandidate struct {
	SKUID      int64   `json:"sku_id"`
	SkuCode    string  `json:"sku_code"`
	MatchedOn  string  `json:"matched_on"`    // ALIAS / SKU_CODE / FAMILY
	MatchedVal string  `json:"matched_value"` // 命中的具体文本
	Similarity float64 `json:"similarity"`
}

// ApplicationQuery 申请列表查询条件。
type ApplicationQuery struct {
	SupplierID *int64 // 供应商侧强制填自身 ID；内部侧可选（空=全部）
	Status     string // 空 = 全部
	Page       int
	Size       int
}

// ApplicationListResult 申请列表结果。
type ApplicationListResult struct {
	List  []ModelApplication `json:"list"`
	Total int                `json:"total"`
	Page  int                `json:"page"`
	Size  int                `json:"size"`
}

// IsTerminalApplicationStatus 是否为终态（不可再审核）。
func IsTerminalApplicationStatus(s string) bool {
	switch s {
	case AppStatusMerged, AppStatusApproved, AppStatusRejected:
		return true
	}
	return false
}

// ValidateSubmitApplication 提交入参校验（在进入事务前失败，避免空跑）。
func ValidateSubmitApplication(in *SubmitApplicationInput) error {
	in.ModelName = strings.TrimSpace(in.ModelName)
	if in.ModelName == "" {
		return fmt.Errorf("model_name 必填: %w", ErrApplicationInvalid)
	}
	if len([]rune(in.ModelName)) > 128 {
		return fmt.Errorf("model_name 超过 128 字符: %w", ErrApplicationInvalid)
	}
	if in.VendorID != nil && *in.VendorID <= 0 {
		return fmt.Errorf("vendor_id 非法: %w", ErrApplicationInvalid)
	}
	if len(in.Payload) == 0 {
		return fmt.Errorf("payload 必填: %w", ErrApplicationInvalid)
	}
	// payload 必须是 JSON 对象（不允许数组/标量，便于后续按字段演进）。
	var probe map[string]any
	if err := json.Unmarshal(in.Payload, &probe); err != nil {
		return fmt.Errorf("payload 必须是 JSON 对象: %w", ErrApplicationInvalid)
	}
	return nil
}

// ValidateApplicationDecision 审核入参校验（含动作与必填项的组合规则）。
func ValidateApplicationDecision(in *ApplicationDecisionInput) error {
	in.Action = strings.ToUpper(strings.TrimSpace(in.Action))
	switch in.Action {
	case AppActionApprove, AppActionMerge:
		if in.TargetSKUID == nil || *in.TargetSKUID <= 0 {
			return fmt.Errorf("%s 必须指定 target_sku_id: %w", in.Action, ErrApplicationInvalid)
		}
	case AppActionReject:
		if in.Reason == nil || strings.TrimSpace(*in.Reason) == "" {
			return fmt.Errorf("REJECT 必须填写 reason: %w", ErrApplicationInvalid)
		}
		if len([]rune(strings.TrimSpace(*in.Reason))) > 512 {
			return fmt.Errorf("reason 超过 512 字符: %w", ErrApplicationInvalid)
		}
	default:
		return fmt.Errorf("action 必须是 APPROVE / MERGE / REJECT: %w", ErrApplicationInvalid)
	}
	return nil
}

// ApplicationStatusOf 由审核动作推导目标状态。
func ApplicationStatusOf(action string) string {
	switch strings.ToUpper(strings.TrimSpace(action)) {
	case AppActionApprove:
		return AppStatusApproved
	case AppActionMerge:
		return AppStatusMerged
	case AppActionReject:
		return AppStatusRejected
	}
	return ""
}

// ------------------------------------------------------------
// Service 方法
// ------------------------------------------------------------

// SubmitModelApplication 供应商提交新模型申请（含查重 top3，幂等由 handler 的中间件保证）。
//
// 查重只提示不拦截：同名不同版本、同系列不同规格都是常见场景，最终由内部审核判断。
func (s *Service) SubmitModelApplication(ctx context.Context, supplierID int64, in SubmitApplicationInput, operatorID int64, requestID string) (*ModelApplication, error) {
	if supplierID <= 0 {
		return nil, ErrNotFound
	}
	if err := ValidateSubmitApplication(&in); err != nil {
		return nil, err
	}
	return s.store.SubmitModelApplication(ctx, supplierID, in, operatorID, requestID)
}

// ListModelApplications 供应商查询自己的申请（行级过滤：supplier_id = 登录主体）。
func (s *Service) ListModelApplications(ctx context.Context, supplierID int64, q ApplicationQuery) (*ApplicationListResult, error) {
	if supplierID <= 0 {
		return nil, ErrNotFound
	}
	// 强制行级过滤：不接受调用方传入的 supplier_id 覆盖。
	q.SupplierID = &supplierID
	return s.store.ListModelApplications(ctx, normalizeApplicationQuery(q))
}

// ListModelApplicationsInternal 内部列表（可跨供应商，按状态筛选）。
func (s *Service) ListModelApplicationsInternal(ctx context.Context, q ApplicationQuery) (*ApplicationListResult, error) {
	return s.store.ListModelApplications(ctx, normalizeApplicationQuery(q))
}

// DecideModelApplication 内部审核：APPROVE（入库）/ MERGE（合并）/ REJECT（驳回）。
//
// 终态守卫：只有 SUBMITTED 可被审核，其余状态返回 ErrApplicationConflict
// （避免并发双审或对已驳回单再次入库）。
func (s *Service) DecideModelApplication(ctx context.Context, id int64, in ApplicationDecisionInput, operatorID int64, requestID string) (*ModelApplication, error) {
	if id <= 0 {
		return nil, ErrApplicationNotFound
	}
	if err := ValidateApplicationDecision(&in); err != nil {
		return nil, err
	}
	return s.store.DecideModelApplication(ctx, id, in, operatorID, requestID)
}

// normalizeApplicationQuery 统一分页默认值与上限（与项目其他列表接口一致）。
func normalizeApplicationQuery(q ApplicationQuery) ApplicationQuery {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.Size <= 0 || q.Size > 200 {
		q.Size = 20
	}
	q.Status = strings.ToUpper(strings.TrimSpace(q.Status))
	return q
}
