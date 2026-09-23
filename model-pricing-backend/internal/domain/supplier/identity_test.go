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

	// 新模型申请（P1-6）：内存态 + 计数，供 application_test.go 使用。
	apps      []*ModelApplication
	nextAppID int64
}

func (f *fakeSupplierStore) FindByOperator(ctx context.Context, operatorID int64) (*Supplier, error) {
	return f.byOperator[operatorID], nil
}

func (f *fakeSupplierStore) ListSupplierSKUs(ctx context.Context, q ListSupplierSKUQuery) (*ListSupplierSKUResult, error) {
	return &ListSupplierSKUResult{List: f.skus, Total: int64(len(f.skus)), Page: q.Page, Size: q.Size}, nil
}

// ---- 新模型申请（P1-6）----
// 注意：fake 复刻了 repo 的两条关键语义——行级过滤与"仅 SUBMITTED 可推进"的状态守卫，
// 使 Service 层的行为断言有意义（真实守卫在 repo 的条件更新里）。

func (f *fakeSupplierStore) SubmitModelApplication(_ context.Context, supplierID int64, in SubmitApplicationInput, _ int64, _ string) (*ModelApplication, error) {
	f.nextAppID++
	app := &ModelApplication{
		ID:         f.nextAppID,
		SupplierID: supplierID,
		ModelName:  in.ModelName,
		VendorID:   in.VendorID,
		Payload:    in.Payload,
		Status:     AppStatusSubmitted,
	}
	f.apps = append(f.apps, app)
	return app, nil
}

func (f *fakeSupplierStore) ListModelApplications(_ context.Context, q ApplicationQuery) (*ApplicationListResult, error) {
	out := make([]ModelApplication, 0, len(f.apps))
	for _, a := range f.apps {
		if q.SupplierID != nil && a.SupplierID != *q.SupplierID {
			continue // 行级过滤
		}
		if q.Status != "" && a.Status != q.Status {
			continue
		}
		out = append(out, *a)
	}
	return &ApplicationListResult{List: out, Total: len(out), Page: q.Page, Size: q.Size}, nil
}

func (f *fakeSupplierStore) DecideModelApplication(_ context.Context, id int64, in ApplicationDecisionInput, _ int64, _ string) (*ModelApplication, error) {
	for _, a := range f.apps {
		if a.ID != id {
			continue
		}
		if a.Status != AppStatusSubmitted {
			return nil, ErrApplicationConflict // 终态守卫
		}
		a.Status = ApplicationStatusOf(in.Action)
		a.MergedSKUID = in.TargetSKUID
		a.RejectReason = in.Reason
		return a, nil
	}
	return nil, ErrApplicationNotFound
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
