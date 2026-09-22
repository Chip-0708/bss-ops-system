// Package supplier 提供供应商身份解析：从 subject_operator 到 supplier_profile 的链路。
// 供应商门户登录后 Operator.OperatorType='SUPPLIER'、OperatorID = subject_operator.id。
package supplier

import (
	"context"
	"errors"
	"fmt"
)

// Supplier 是供应商身份的最小视图（不含商务敏感字段）。
type Supplier struct {
	ID                         int64  `json:"id"`
	SubjectID                  int64  `json:"subject_id"`
	LegalName                  string `json:"legal_name"`
	OwnerProcurementOperatorID int64  `json:"owner_procurement_operator_id"`
	QualStatus                 string `json:"qual_status"`
	SettleStatus               string `json:"settle_status"`
	Status                     string `json:"status"`
}

// ErrNotFound 供应商档案不存在。
var ErrNotFound = errors.New("supplier profile not found")

// ListSupplierSKUQuery 是供应商可报价 SKU 列表查询条件。
type ListSupplierSKUQuery struct {
	Keyword  string
	VendorID *int64
	FamilyID *int64
	Page     int
	Size     int
}

// ListSupplierSKUResult 是供应商可报价 SKU 列表结果（json tag 全小写）。
type ListSupplierSKUResult struct {
	List  []SKU `json:"list"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
	Size  int   `json:"size"`
}

// SKU 是供应商视角的可报价 SKU（含官方价基准只读列）。
type SKU struct {
	ID               int64          `json:"id"`
	SkuCode          string         `json:"sku_code"`
	ModelName        string         `json:"model_name"`
	VendorID         int64          `json:"vendor_id"`
	VendorName       string         `json:"vendor_name"`
	FamilyID         int64          `json:"family_id"`
	FamilyName       string         `json:"family_name"`
	ModelType        string         `json:"model_type"`
	NativeCurrency   string         `json:"native_currency"`
	ContextWindow    *int           `json:"context_window"`
	TierTag          *string        `json:"tier_tag"`
	OfficialPrice    *OfficialPrice `json:"official_price"`
	HasOfficialPrice bool           `json:"has_official_price"`
}

// OfficialPrice 是当前官方价基准（只读，不含他人报价/平台成本/毛利）。
type OfficialPrice struct {
	VersionNo  int              `json:"version_no"`
	Currency   string           `json:"currency"`
	TaxBasis   string           `json:"tax_basis"`
	Components []PriceComponent `json:"components"`
}

// PriceComponent 是逐组件官方价。
type PriceComponent struct {
	ComponentType string `json:"component_type"`
	UnitPrice     string `json:"unit_price"`
}

// Store 是供应商身份查询接口。
type Store interface {
	FindByOperator(ctx context.Context, operatorID int64) (*Supplier, error)
	// ListSupplierSKUs 返回 lifecycle_status ∈ (PUBLISHED, PURCHASABLE, PENDING_VERIFY) 的 SKU。
	ListSupplierSKUs(ctx context.Context, q ListSupplierSKUQuery) (*ListSupplierSKUResult, error)
}

// Service 是供应商身份解析服务。
type Service struct {
	store Store
}

// NewService 构造供应商服务。
func NewService(store Store) *Service {
	return &Service{store: store}
}

// ResolveByOperator 按 subject_operator.id 解析供应商身份。
// 链路：subject_operator.id → subject_id → supplier_profile.subject_id → supplier_profile.id。
func (s *Service) ResolveByOperator(ctx context.Context, operatorID int64) (*Supplier, error) {
	sup, err := s.store.FindByOperator(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	if sup == nil {
		return nil, fmt.Errorf("operator %d: %w", operatorID, ErrNotFound)
	}
	return sup, nil
}

// ListSupplierSKUs 供应商可报价 SKU 列表（05-quotes.md §2）。
// 返回 lifecycle_status ∈ (PUBLISHED, PURCHASABLE, PENDING_VERIFY) 的 SKU，
// 含官方价基准只读列；不含他人报价/平台成本/毛利。
func (s *Service) ListSupplierSKUs(ctx context.Context, q ListSupplierSKUQuery) (*ListSupplierSKUResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	return s.store.ListSupplierSKUs(ctx, q)
}
