package model

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Store 是模型管理的仓储接口（GORM 实现见 internal/repo/model.go）。
type Store interface {
	// FindVendorByID 厂商存在性校验。
	FindVendorByID(ctx context.Context, id int64) (bool, error)
	// FindFamilyByID 系列存在性 + 所属 vendor 校验。
	FindFamilyByID(ctx context.Context, id int64) (vendorID int64, ok bool, err error)
	// ExistsSkuCode sku_code 全局唯一检查。
	ExistsSkuCode(ctx context.Context, skuCode string) (bool, error)
	// ExistsAlias 别名全局唯一检查。
	ExistsAlias(ctx context.Context, alias string) (bool, error)
	// CreateSKU 新建 SKU（同事务写别名）。
	CreateSKU(ctx context.Context, in CreateSKUInput, operatorID int64, requestID string) (*SKU, error)
	// UpdateSKU 维护 SKU（lifecycle_status 不可直接改）。
	UpdateSKU(ctx context.Context, id int64, in UpdateSKUInput, operatorID int64, requestID string) (*SKU, error)
	// GetSKU 按 ID 取 SKU（含 vendor/family 名与别名）。
	GetSKU(ctx context.Context, id int64) (*SKU, error)
	// ListSKUs 列表查询：view=family 按系列折叠、view=sku 展开。
	ListSKUs(ctx context.Context, q ListQuery) (*ListResult, error)
	// ListFamilies 按系列分页，并返回每个系列下的完整 SKU。
	ListFamilies(ctx context.Context, q ListQuery) (*FamilyListResult, error)
	// ListOptions 返回模型表单与筛选使用的全部厂商和系列选项。
	ListOptions(ctx context.Context) (*OptionsResult, error)

	// ---- 阶段 4b-1：别名 / 合并 / 批量 / 上架 ----

	// ReplaceAliases 全量覆盖该 SKU 的别名（差集增删）。
	ReplaceAliases(ctx context.Context, skuID int64, aliases []string, source string, operatorID int64) error
	// SuggestAliases 按 trigram 相似度返回 Top3 查重建议。
	SuggestAliases(ctx context.Context, keyword string, limit int) ([]Suggestion, error)
	// MergeAlias 把 source_sku 的名称作为别名挂到 target_sku（source='MERGE'）。
	// 返回别名 ID。
	MergeAlias(ctx context.Context, targetSkuID, sourceSkuID int64, alias string, operatorID int64) (int64, error)
	// IsSkuReferenced 校验 SKU 是否被价目表/报价引用（合并前置）。
	IsSkuReferenced(ctx context.Context, skuID int64) (bool, error)
	// BatchAction 批量动作：返回每项成功/失败（部分成功语义）。
	BatchAction(ctx context.Context, in BatchInput, operatorID int64) (*BatchResult, error)
	// Publish 上架：条件更新 WHERE lifecycle_status='PURCHASABLE' → PUBLISHED。
	// 更新行数为 0 时返回 ErrPublishConflict。
	Publish(ctx context.Context, skuID int64, operatorID int64) (*PublishResult, error)

	// ---- 阶段 4b-2：退役链路 ----

	// AnalyzeDeprecation 影响分析（§7）：统计引用 + 推荐替代，写入 deprecation_impact。
	// sku 由 Service 层已加载（含 FamilyID 等）。
	AnalyzeDeprecation(ctx context.Context, sku *SKU, operatorID int64) (*DeprecationImpactResult, error)
	// StartDeprecate 发起退役（§8）：校验 snapshot，创建 change_request + 2 步审批。
	// skuID 来自 URL 路径（/models/{id}/deprecate），由 handler 传入。
	StartDeprecate(ctx context.Context, skuID int64, in DeprecateInput, operatorID int64, requestID string) (*DeprecateResult, error)
	// DecideApproval 审批动作：角色匹配 + 禁止自审 + 状态机推进。
	// onApproved 全步 APPROVED 时回调（7b：PRICE_UP/PRICE_DOWN 注入生效连锁；退役传 nil）。
	// 签名含 operatorID/requestID（连锁自身的审计与 task_job 归属需要）。
	DecideApproval(ctx context.Context, changeRequestID int64, in DecisionInput, operatorID int64, roles []string, requestID string, onApproved func(context.Context, int64, int64, string) error) (*DecisionResult, error)
	// LoadChangeType 读 change_request.change_type（7b：通用审批入口按类型分发 onApproved）。
	LoadChangeType(ctx context.Context, changeRequestID int64) (string, error)

	// ListChangeRequests 变更单列表（含审批进度摘要）；联调 P1-4。
	ListChangeRequests(ctx context.Context, q ChangeRequestQuery) (*ChangeRequestListResult, error)
	// GetChangeRequest 变更单详情（含完整审批步骤链）；联调 P1-4 / P1-5。
	GetChangeRequest(ctx context.Context, id int64) (*ChangeRequestDetail, error)
}

// ListQuery 是模型库列表查询条件。
type ListQuery struct {
	View            string // family（默认）/ sku
	Keyword         string
	VendorID        *int64
	FamilyID        *int64
	ModelType       string
	LifecycleStatus string
	TierTag         string
	Page            int
	Size            int
}

// ListResult 是列表返回。
// json tag 必须与 docs/api/README.md「分页」约定一致：list / total / page / size（全小写）。
type ListResult struct {
	List  []SKU `json:"list"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
	Size  int   `json:"size"`
}

// FamilyView 是 view=family 时的系列聚合视图。
type FamilyView struct {
	FamilyID   int64  `json:"family_id"`
	FamilyName string `json:"family_name"`
	VendorID   int64  `json:"vendor_id"`
	VendorName string `json:"vendor_name"`
	SkuCount   int    `json:"sku_count"`
	Children   []SKU  `json:"children"`
}

// FamilyListResult 是 view=family 的分页返回。
type FamilyListResult struct {
	List  []FamilyView `json:"list"`
	Total int64        `json:"total"`
	Page  int          `json:"page"`
	Size  int          `json:"size"`
}

// VendorOption 是厂商下拉选项。
type VendorOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// FamilyOption 是系列下拉选项。
type FamilyOption struct {
	ID       int64  `json:"id"`
	VendorID int64  `json:"vendor_id"`
	Name     string `json:"name"`
}

// OptionsResult 是模型筛选和新建表单共用的选项集合。
type OptionsResult struct {
	Vendors  []VendorOption `json:"vendors"`
	Families []FamilyOption `json:"families"`
}

// Service 是模型管理的领域服务。
type Service struct {
	store Store
	now   func() time.Time
	// ApprovedHook 按 change_type 取全步 APPROVED 回调（7b：main.go 注入 price.ApplyOfficialPriceChange
	// 的分发函数；退役走 store 内建分支不经此 hook；未注册的类型返回 nil 表示无回调）。
	ApprovedHook func(changeType string) func(context.Context, int64, int64, string) error
}

// NewService 构造模型服务。
func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now}
}

// Create 创建 SKU：lifecycle_status 固定为 DRAFT，capability 未知 key 拒绝。
func (s *Service) Create(ctx context.Context, in CreateSKUInput, operatorID int64, requestID string) (*SKU, error) {
	if err := s.validateCreate(ctx, in); err != nil {
		return nil, err
	}

	sku, err := s.store.CreateSKU(ctx, in, operatorID, requestID)
	if err != nil {
		return nil, err
	}
	return s.store.GetSKU(ctx, sku.ID)
}

// Update 维护 SKU：lifecycle_status 不允许直接改。
func (s *Service) Update(ctx context.Context, id int64, in UpdateSKUInput, operatorID int64, requestID string) (*SKU, error) {
	if in.Capability != nil {
		if err := ValidateCapabilityKeys(in.Capability); err != nil {
			return nil, err
		}
	}
	if in.SkuCode != nil {
		exists, err := s.store.ExistsSkuCode(ctx, *in.SkuCode)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, ErrDuplicateSkuCode
		}
	}
	for _, alias := range in.Aliases {
		exists, err := s.store.ExistsAlias(ctx, alias)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, ErrDuplicateAlias
		}
	}

	sku, err := s.store.UpdateSKU(ctx, id, in, operatorID, requestID)
	if err != nil {
		return nil, err
	}
	return s.store.GetSKU(ctx, sku.ID)
}

// Get 按 ID 取 SKU。
func (s *Service) Get(ctx context.Context, id int64) (*SKU, error) {
	sku, err := s.store.GetSKU(ctx, id)
	if err != nil {
		return nil, err
	}
	if sku == nil {
		return nil, ErrNotFound
	}
	return sku, nil
}

// List 列表查询。
func (s *Service) List(ctx context.Context, q ListQuery) (*ListResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	if q.View == "" {
		q.View = "family"
	}
	return s.store.ListSKUs(ctx, q)
}

// ListFamilies 按系列聚合。分页单位是系列，children 始终包含命中系列下的全部 SKU。
func (s *Service) ListFamilies(ctx context.Context, q ListQuery) (*FamilyListResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	q.View = "family"
	return s.store.ListFamilies(ctx, q)
}

// Options 返回全部厂商和系列选项。
func (s *Service) Options(ctx context.Context) (*OptionsResult, error) {
	return s.store.ListOptions(ctx)
}

// ReplaceAliases 别名全量覆盖（§4 / §10 决策 4）：
// 前端提交该 SKU 当前全部别名，服务端做差集增删；传空数组 = 清空。
func (s *Service) ReplaceAliases(ctx context.Context, skuID int64, in AliasInput, operatorID int64) (*SKU, error) {
	if _, err := s.Get(ctx, skuID); err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	for _, a := range in.Aliases {
		a = strings.TrimSpace(a)
		if a == "" || len(a) > 128 {
			return nil, fmt.Errorf("alias must be 1~128 chars: %w", ErrDuplicateAlias)
		}
		if _, dup := seen[a]; dup {
			return nil, ErrDuplicateAlias
		}
		seen[a] = struct{}{}
	}

	source := in.Source
	if source == "" {
		source = "MANUAL"
	}
	if source != "MANUAL" && source != "IMPORT" {
		return nil, fmt.Errorf("invalid alias source %q", source)
	}

	if err := s.store.ReplaceAliases(ctx, skuID, in.Aliases, source, operatorID); err != nil {
		return nil, err
	}
	return s.store.GetSKU(ctx, skuID)
}

// SuggestAliases 查重建议（§4）：Top3，基于 pg_trgm 相似度。
func (s *Service) SuggestAliases(ctx context.Context, keyword string) (*SuggestResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" || len(keyword) > 64 {
		return nil, fmt.Errorf("keyword must be 1~64 chars")
	}
	list, err := s.store.SuggestAliases(ctx, keyword, 3)
	if err != nil {
		return nil, err
	}
	return &SuggestResult{Suggestions: list}, nil
}

// Merge 一键合并为别名（§5）：target ≠ source；source 未被引用；
// 被合并方名称作为别名写入（source='MERGE'）。不做物理删除。
func (s *Service) Merge(ctx context.Context, in MergeInput, operatorID int64) (map[string]interface{}, error) {
	if in.TargetSkuID == in.SourceSkuID {
		return nil, ErrMergeSameSKU
	}
	if _, err := s.Get(ctx, in.TargetSkuID); err != nil {
		return nil, err
	}
	if _, err := s.Get(ctx, in.SourceSkuID); err != nil {
		return nil, err
	}
	referenced, err := s.store.IsSkuReferenced(ctx, in.SourceSkuID)
	if err != nil {
		return nil, err
	}
	if referenced {
		return nil, ErrSourceReferenced
	}
	exists, err := s.store.ExistsAlias(ctx, in.Alias)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrDuplicateAlias
	}

	aliasID, err := s.store.MergeAlias(ctx, in.TargetSkuID, in.SourceSkuID, in.Alias, operatorID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"target_sku_id": in.TargetSkuID,
		"merged_sku_id": in.SourceSkuID,
		"alias_id":      aliasID,
		"alias":         in.Alias,
	}, nil
}

// Batch 批量改状态/打标签（§6）：
// 单次 ≤200；禁止上架/退役动作；部分成功语义。
func (s *Service) Batch(ctx context.Context, in BatchInput, operatorID int64) (*BatchResult, error) {
	if len(in.SkuIDs) > 200 {
		return nil, ErrBatchTooLarge
	}
	switch in.Action {
	case BatchSubmitVerify, BatchSetTier, BatchAddTag, BatchRemoveTag:
	default:
		// PUBLISH / DEPRECATE 等一律拒绝：必须逐个走 publish / deprecate
		return nil, ErrBatchActionInvalid
	}
	if in.Action == BatchSetTier {
		if v, ok := in.Payload["tier_tag"].(string); !ok || v == "" {
			return nil, fmt.Errorf("payload.tier_tag required for SET_TIER")
		}
	}
	if in.Action == BatchAddTag || in.Action == BatchRemoveTag {
		if v, ok := in.Payload["tag"].(string); !ok || v == "" {
			return nil, fmt.Errorf("payload.tag required for %s", in.Action)
		}
	}
	return s.store.BatchAction(ctx, in, operatorID)
}

// Publish 上架（§9）：前置 PURCHASABLE，条件更新做并发占位。
func (s *Service) Publish(ctx context.Context, skuID int64, operatorID int64) (*PublishResult, error) {
	if _, err := s.Get(ctx, skuID); err != nil {
		return nil, err
	}
	return s.store.Publish(ctx, skuID, operatorID)
}

// ---- 阶段 4b-2：退役链路 ----

// AnalyzeDeprecation 影响分析（§7）：
// 统计 price_book_item / customer_quote_item 引用；同 family PUBLISHED 的前 3 个替代 SKU；
// 写入 deprecation_impact 表（snapshot_id=UUID，expires_at=now()+24h）。
func (s *Service) AnalyzeDeprecation(ctx context.Context, skuID int64, operatorID int64) (*DeprecationImpactResult, error) {
	sku, err := s.Get(ctx, skuID)
	if err != nil {
		return nil, err
	}
	return s.store.AnalyzeDeprecation(ctx, sku, operatorID)
}

// StartDeprecate 发起退役（§8）：

// StartDeprecate 发起退役（§8）：
// 前置 PUBLISHED / PURCHASABLE；校验 snapshot 存在、sku 匹配、未过期；
// 创建 change_request（risk 按 reference_count 分档）+ 2 步审批；SKU 状态不变。
// skuID 来自 URL 路径（/models/{id}/deprecate），由 handler 传入。
func (s *Service) StartDeprecate(ctx context.Context, skuID int64, in DeprecateInput, operatorID int64, requestID string) (*DeprecateResult, error) {
	sku, err := s.store.GetSKU(ctx, skuID)
	if err != nil {
		return nil, err
	}
	if sku == nil {
		return nil, ErrNotFound
	}
	if sku.LifecycleStatus != LifecyclePublished && sku.LifecycleStatus != LifecyclePurchasable {
		return nil, ErrDeprecateStateInvalid
	}
	if t, err := time.Parse("2006-01-02", in.SunsetDate); err != nil || !t.After(s.now()) {
		return nil, fmt.Errorf("sunset_date must be YYYY-MM-DD and later than today")
	}
	return s.store.StartDeprecate(ctx, skuID, in, operatorID, requestID)
}

// DecideApproval 审批动作（§3.3）：
// 角色匹配 required_role；同一操作员不得完成同一单的两个步骤；
// 两步 APPROVED → change_request APPROVED + SKU 条件更新 DEPRECATING；
// 任一 REJECTED → change_request REJECTED，SKU 不变。
// onApproved 全步 APPROVED 时回调（7b：退役传 nil；PRICE_UP/PRICE_DOWN 由调用方按 change_type 注入）。
// 调用方无法预知 change_type 时，用 Service.DecideApprovalByType（先查 change_type 再分发）。
func (s *Service) DecideApproval(ctx context.Context, changeRequestID int64, in DecisionInput, operatorID int64, roles []string, requestID string, onApproved func(context.Context, int64, int64, string) error) (*DecisionResult, error) {
	return s.store.DecideApproval(ctx, changeRequestID, in, operatorID, roles, requestID, onApproved)
}

// DecideApprovalByType 按 change_request.change_type 自动分发 onApproved（7b 通用审批入口）。
// 读 change_type 走 store（复用同一连接），再按 ApprovedHook 取回调；未注册类型传 nil。
func (s *Service) DecideApprovalByType(ctx context.Context, changeRequestID int64, in DecisionInput, operatorID int64, roles []string, requestID string) (*DecisionResult, error) {
	changeType, err := s.store.LoadChangeType(ctx, changeRequestID)
	if err != nil {
		return nil, err
	}
	var hook func(context.Context, int64, int64, string) error
	if s.ApprovedHook != nil {
		hook = s.ApprovedHook(changeType)
	}
	return s.store.DecideApproval(ctx, changeRequestID, in, operatorID, roles, requestID, hook)
}

// ListChangeRequests 变更单列表（联调 P1-4：官方价变更单与价目表发布进度统一查询）。
func (s *Service) ListChangeRequests(ctx context.Context, q ChangeRequestQuery) (*ChangeRequestListResult, error) {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.Size <= 0 || q.Size > 200 {
		q.Size = 20
	}
	return s.store.ListChangeRequests(ctx, q)
}

// GetChangeRequest 变更单详情（含完整审批步骤链）。
// 联调 P1-5：前端按 change_request_id 恢复审批状态，不再依赖浏览器缓存。
func (s *Service) GetChangeRequest(ctx context.Context, id int64) (*ChangeRequestDetail, error) {
	if id <= 0 {
		return nil, ErrNotFound
	}
	return s.store.GetChangeRequest(ctx, id)
}

// validateCreate 创建前的字段校验。
func (s *Service) validateCreate(ctx context.Context, in CreateSKUInput) error {
	if in.Capability != nil {
		if err := ValidateCapabilityKeys(in.Capability); err != nil {
			return err
		}
	}

	ok, err := s.store.FindVendorByID(ctx, in.VendorID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("vendor %d not found", in.VendorID)
	}

	famVendorID, ok, err := s.store.FindFamilyByID(ctx, in.FamilyID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("family %d not found", in.FamilyID)
	}
	if famVendorID != in.VendorID {
		return ErrFamilyVendorMismatch
	}

	exists, err := s.store.ExistsSkuCode(ctx, in.SkuCode)
	if err != nil {
		return err
	}
	if exists {
		return ErrDuplicateSkuCode
	}

	for _, alias := range in.Aliases {
		if strings.TrimSpace(alias) == "" {
			return fmt.Errorf("alias must not be empty")
		}
		exists, err := s.store.ExistsAlias(ctx, alias)
		if err != nil {
			return err
		}
		if exists {
			return ErrDuplicateAlias
		}
	}
	return nil
}
