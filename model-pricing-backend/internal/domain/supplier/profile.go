package supplier

import (
	"context"
	"errors"
	"time"
)

var ErrProfileOutOfScope = errors.New("供应商不在当前操作员的数据域内")

// ProfileQuery 是内部供应商档案列表的筛选与分页条件。
type ProfileQuery struct {
	Keyword    string
	QualStatus string
	Status     string
	Page       int
	Size       int
}

// ProfileSummary 是供应商列表行。统计字段按报价/SKU 当前数据计算。
type ProfileSummary struct {
	ID                         int64     `json:"id"`
	SubjectID                  int64     `json:"subject_id"`
	LegalName                  string    `json:"legal_name"`
	SettlementCurrency         string    `json:"settlement_currency"`
	QualStatus                 string    `json:"qual_status"`
	SettleStatus               string    `json:"settle_status"`
	Status                     string    `json:"status"`
	OwnerProcurementOperatorID int64     `json:"owner_procurement_operator_id"`
	OwnerProcurementName       string    `json:"owner_procurement_name"`
	SKUCount                   int64     `json:"sku_count"`
	EffectiveQuoteCount        int64     `json:"effective_quote_count"`
	ExpiringSoon               int64     `json:"expiring_soon"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

type ProfileListResult struct {
	List  []ProfileSummary `json:"list"`
	Total int64            `json:"total"`
	Page  int              `json:"page"`
	Size  int              `json:"size"`
}

// ProfileDetail 只包含 supplier_profile、legal_subject 和归属采购的真实字段。
type ProfileDetail struct {
	ID                         int64     `json:"id"`
	SubjectID                  int64     `json:"subject_id"`
	LegalName                  string    `json:"legal_name"`
	SettlementCurrency         string    `json:"settlement_currency"`
	SettleType                 string    `json:"settle_type"`
	BillingCycle               int       `json:"billing_cycle"`
	MinRecharge                string    `json:"min_recharge"`
	CreditLine                 string    `json:"credit_line"`
	CreditUsed                 string    `json:"credit_used"`
	DepositAmount              string    `json:"deposit_amount"`
	SettleStatus               string    `json:"settle_status"`
	QualStatus                 string    `json:"qual_status"`
	OwnerProcurementOperatorID int64     `json:"owner_procurement_operator_id"`
	OwnerProcurementName       string    `json:"owner_procurement_name"`
	Status                     string    `json:"status"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

type ProfileStore interface {
	ListProfiles(ctx context.Context, scope OwnerScope, q ProfileQuery) (*ProfileListResult, error)
	GetProfile(ctx context.Context, scope OwnerScope, id int64) (*ProfileDetail, error)
}

type ProfileService struct{ store ProfileStore }

func NewProfileService(store ProfileStore) *ProfileService { return &ProfileService{store: store} }

func (s *ProfileService) List(ctx context.Context, scope OwnerScope, q ProfileQuery) (*ProfileListResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	return s.store.ListProfiles(ctx, scope, q)
}

func (s *ProfileService) Get(ctx context.Context, scope OwnerScope, id int64) (*ProfileDetail, error) {
	return s.store.GetProfile(ctx, scope, id)
}
