package supplier

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeSupplierStore 为单测的内存供应商仓储。
type fakeSupplierStore struct {
	byOperator map[int64]*Supplier
	skus       []SKU
}

func (f *fakeSupplierStore) FindByOperator(ctx context.Context, operatorID int64) (*Supplier, error) {
	return f.byOperator[operatorID], nil
}

func (f *fakeSupplierStore) ListSupplierSKUs(ctx context.Context, q ListSupplierSKUQuery) (*ListSupplierSKUResult, error) {
	return &ListSupplierSKUResult{List: f.skus, Total: int64(len(f.skus)), Page: q.Page, Size: q.Size}, nil
}

func TestResolveByOperator_Hit(t *testing.T) {
	store := &fakeSupplierStore{byOperator: map[int64]*Supplier{
		1: {ID: 10, SubjectID: 100, LegalName: "供应商甲", OwnerProcurementOperatorID: 4},
	}}
	svc := NewService(store)
	sup, err := svc.ResolveByOperator(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, int64(10), sup.ID)
	require.Equal(t, "供应商甲", sup.LegalName)
	require.Equal(t, int64(4), sup.OwnerProcurementOperatorID)
}

func TestResolveByOperator_NotFound(t *testing.T) {
	store := &fakeSupplierStore{byOperator: map[int64]*Supplier{}}
	svc := NewService(store)
	_, err := svc.ResolveByOperator(context.Background(), 999)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestListSupplierSKUs_Normal(t *testing.T) {
	store := &fakeSupplierStore{
		byOperator: map[int64]*Supplier{},
		skus: []SKU{
			{ID: 1, SkuCode: "gpt-5", VendorID: 10, VendorName: "OpenAI", FamilyID: 100, FamilyName: "GPT-5", ModelType: "对话", NativeCurrency: "USD"},
			{ID: 2, SkuCode: "claude-4", VendorID: 20, VendorName: "Anthropic", FamilyID: 200, FamilyName: "Claude 4", ModelType: "推理", NativeCurrency: "USD"},
		},
	}
	svc := NewService(store)
	res, err := svc.ListSupplierSKUs(context.Background(), ListSupplierSKUQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	require.Equal(t, int64(2), res.Total)
	require.Len(t, res.List, 2)
	require.Equal(t, int64(10), res.List[0].VendorID)
	require.Equal(t, int64(100), res.List[0].FamilyID)
}

// TestListSupplierSKUs_PagingNormalize 只覆盖 service 层的分页参数规整。
// 契约要求的「lifecycle_status ∈ (PUBLISHED,PURCHASABLE,PENDING_VERIFY)」过滤
// 落在 repo 的 SQL 里，本单测无法覆盖，改由 E2E 验证（建一个 DRAFT 的 SKU 断言其不出现）。
func TestListSupplierSKUs_PagingNormalize(t *testing.T) {
	store := &fakeSupplierStore{byOperator: map[int64]*Supplier{}, skus: []SKU{}}
	svc := NewService(store)
	res, err := svc.ListSupplierSKUs(context.Background(), ListSupplierSKUQuery{Page: 0, Size: 999})
	require.NoError(t, err)
	require.Equal(t, 1, res.Page, "page<1 应规整为 1")
	require.Equal(t, 20, res.Size, "size>100 应规整为 20")
}
