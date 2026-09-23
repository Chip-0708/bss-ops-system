package supplier

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- 纯函数：提交校验 ----

func TestValidateSubmitApplication_Happy(t *testing.T) {
	in := SubmitApplicationInput{
		ModelName: "  gpt-5-mini  ", // 前后空白应被 Trim
		Payload:   json.RawMessage(`{"context_window": 128000}`),
	}
	require.NoError(t, ValidateSubmitApplication(&in))
	require.Equal(t, "gpt-5-mini", in.ModelName, "model_name 应被 Trim")
}

func TestValidateSubmitApplication_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		in   SubmitApplicationInput
	}{
		{"空 model_name", SubmitApplicationInput{ModelName: "   ", Payload: json.RawMessage(`{}`)}},
		{"空 payload", SubmitApplicationInput{ModelName: "m", Payload: nil}},
		{"payload 非 JSON 对象（数组）", SubmitApplicationInput{ModelName: "m", Payload: json.RawMessage(`[1,2]`)}},
		{"payload 非法 JSON", SubmitApplicationInput{ModelName: "m", Payload: json.RawMessage(`{`)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			require.ErrorIs(t, ValidateSubmitApplication(&in), ErrApplicationInvalid)
		})
	}
}

func TestValidateSubmitApplication_RejectsBadVendorID(t *testing.T) {
	zero := int64(0)
	in := SubmitApplicationInput{ModelName: "m", Payload: json.RawMessage(`{}`), VendorID: &zero}
	require.ErrorIs(t, ValidateSubmitApplication(&in), ErrApplicationInvalid)
}

func TestValidateSubmitApplication_RejectsOverlongName(t *testing.T) {
	long := make([]rune, 129)
	for i := range long {
		long[i] = 'a'
	}
	in := SubmitApplicationInput{ModelName: string(long), Payload: json.RawMessage(`{}`)}
	require.ErrorIs(t, ValidateSubmitApplication(&in), ErrApplicationInvalid)
}

// ---- 纯函数：审核校验 ----

func TestValidateApplicationDecision_ApproveMergeRequireTargetSKU(t *testing.T) {
	for _, action := range []string{AppActionApprove, AppActionMerge} {
		t.Run(action, func(t *testing.T) {
			in := ApplicationDecisionInput{Action: action}
			require.ErrorIs(t, ValidateApplicationDecision(&in), ErrApplicationInvalid,
				"%s 缺 target_sku_id 必须拒绝", action)

			sku := int64(40)
			ok := ApplicationDecisionInput{Action: action, TargetSKUID: &sku}
			require.NoError(t, ValidateApplicationDecision(&ok))
		})
	}
}

func TestValidateApplicationDecision_RejectRequiresReason(t *testing.T) {
	in := ApplicationDecisionInput{Action: AppActionReject}
	require.ErrorIs(t, ValidateApplicationDecision(&in), ErrApplicationInvalid)

	blank := "   "
	in2 := ApplicationDecisionInput{Action: AppActionReject, Reason: &blank}
	require.ErrorIs(t, ValidateApplicationDecision(&in2), ErrApplicationInvalid, "纯空白 reason 视为未填")

	reason := "与已有 SKU 重复"
	ok := ApplicationDecisionInput{Action: AppActionReject, Reason: &reason}
	require.NoError(t, ValidateApplicationDecision(&ok))
}

func TestValidateApplicationDecision_RejectsUnknownAction(t *testing.T) {
	sku := int64(1)
	in := ApplicationDecisionInput{Action: "DELETE", TargetSKUID: &sku}
	require.ErrorIs(t, ValidateApplicationDecision(&in), ErrApplicationInvalid)
}

func TestValidateApplicationDecision_ActionCaseInsensitive(t *testing.T) {
	sku := int64(40)
	in := ApplicationDecisionInput{Action: "  approve ", TargetSKUID: &sku}
	require.NoError(t, ValidateApplicationDecision(&in))
	require.Equal(t, AppActionApprove, in.Action, "action 应被归一为大写去空白")
}

// ---- 纯函数：状态映射 ----

func TestApplicationStatusOf(t *testing.T) {
	require.Equal(t, AppStatusApproved, ApplicationStatusOf(AppActionApprove))
	require.Equal(t, AppStatusMerged, ApplicationStatusOf(AppActionMerge))
	require.Equal(t, AppStatusRejected, ApplicationStatusOf(AppActionReject))
	require.Equal(t, "", ApplicationStatusOf("NOPE"))
}

func TestIsTerminalApplicationStatus(t *testing.T) {
	require.False(t, IsTerminalApplicationStatus(AppStatusSubmitted), "SUBMITTED 非终态")
	for _, s := range []string{AppStatusApproved, AppStatusMerged, AppStatusRejected} {
		require.True(t, IsTerminalApplicationStatus(s), "%s 应为终态", s)
	}
}

// ---- Service 层：提交 / 行级过滤 / 审核流转 ----

func newAppService() (*Service, *fakeSupplierStore) {
	store := &fakeSupplierStore{byOperator: map[int64]*Supplier{}}
	return NewService(store), store
}

func TestSubmitModelApplication_StartsAsSubmitted(t *testing.T) {
	svc, _ := newAppService()
	got, err := svc.SubmitModelApplication(context.Background(), 10, SubmitApplicationInput{
		ModelName: "qwen3-next",
		Payload:   json.RawMessage(`{"modality":"text"}`),
	}, 7, "req-1")
	require.NoError(t, err)
	require.Equal(t, AppStatusSubmitted, got.Status)
	require.Equal(t, int64(10), got.SupplierID)
	require.Equal(t, "qwen3-next", got.ModelName)
}

func TestSubmitModelApplication_RejectsInvalidBeforeStore(t *testing.T) {
	svc, store := newAppService()
	_, err := svc.SubmitModelApplication(context.Background(), 10, SubmitApplicationInput{
		ModelName: "m", Payload: json.RawMessage(`[]`), // 非对象
	}, 7, "req-2")
	require.ErrorIs(t, err, ErrApplicationInvalid)
	require.Empty(t, store.apps, "校验失败不应落库")
}

// TestListModelApplications_RowFiltered 是本域的红线用例：
// 供应商侧查询必须只返回本主体（supplier_id）的申请。
func TestListModelApplications_RowFiltered(t *testing.T) {
	svc, store := newAppService()
	store.apps = []*ModelApplication{
		{ID: 1, SupplierID: 10, ModelName: "a", Status: AppStatusSubmitted},
		{ID: 2, SupplierID: 20, ModelName: "b", Status: AppStatusSubmitted},
	}
	res, err := svc.ListModelApplications(context.Background(), 10, ApplicationQuery{})
	require.NoError(t, err)
	require.Len(t, res.List, 1, "只应看到自己（supplier 10）的申请")
	require.Equal(t, int64(10), res.List[0].SupplierID)

	// 内部侧不过滤 supplier_id → 两条都可见。
	all, err := svc.ListModelApplicationsInternal(context.Background(), ApplicationQuery{})
	require.NoError(t, err)
	require.Len(t, all.List, 2)
}

func TestListModelApplications_StatusFilterAndPagingDefaults(t *testing.T) {
	svc, store := newAppService()
	store.apps = []*ModelApplication{
		{ID: 1, SupplierID: 10, Status: AppStatusSubmitted},
		{ID: 2, SupplierID: 10, Status: AppStatusRejected},
	}
	res, err := svc.ListModelApplications(context.Background(), 10, ApplicationQuery{Status: "rejected"})
	require.NoError(t, err)
	require.Len(t, res.List, 1)
	require.Equal(t, 1, res.Page, "分页默认 page=1")
	require.Equal(t, 20, res.Size, "分页默认 size=20")
}

func TestDecideModelApplication_ApproveAdvancesStatus(t *testing.T) {
	svc, store := newAppService()
	store.apps = []*ModelApplication{{ID: 1, SupplierID: 10, Status: AppStatusSubmitted}}
	sku := int64(40)

	got, err := svc.DecideModelApplication(context.Background(), 1,
		ApplicationDecisionInput{Action: AppActionApprove, TargetSKUID: &sku}, 7, "req-3")
	require.NoError(t, err)
	require.Equal(t, AppStatusApproved, got.Status)
	require.NotNil(t, got.MergedSKUID)
	require.Equal(t, int64(40), *got.MergedSKUID)
}

func TestDecideModelApplication_RejectRecordsReason(t *testing.T) {
	svc, store := newAppService()
	store.apps = []*ModelApplication{{ID: 1, SupplierID: 10, Status: AppStatusSubmitted}}
	reason := "已有同厂商同规格 SKU"

	got, err := svc.DecideModelApplication(context.Background(), 1,
		ApplicationDecisionInput{Action: AppActionReject, Reason: &reason}, 7, "req-4")
	require.NoError(t, err)
	require.Equal(t, AppStatusRejected, got.Status)
	require.NotNil(t, got.RejectReason)
	require.Equal(t, reason, *got.RejectReason)
}

// TestDecideModelApplication_TerminalConflict 覆盖终态守卫：
// 已审批/已合并/已驳回的申请不可再次审核（真实守卫在 repo 的条件更新）。
func TestDecideModelApplication_TerminalConflict(t *testing.T) {
	svc, store := newAppService()
	store.apps = []*ModelApplication{{ID: 1, SupplierID: 10, Status: AppStatusApproved}}
	sku := int64(41)

	_, err := svc.DecideModelApplication(context.Background(), 1,
		ApplicationDecisionInput{Action: AppActionMerge, TargetSKUID: &sku}, 7, "req-5")
	require.ErrorIs(t, err, ErrApplicationConflict)
}

func TestDecideModelApplication_NotFound(t *testing.T) {
	svc, _ := newAppService()
	sku := int64(1)
	_, err := svc.DecideModelApplication(context.Background(), 999,
		ApplicationDecisionInput{Action: AppActionApprove, TargetSKUID: &sku}, 7, "req-6")
	require.ErrorIs(t, err, ErrApplicationNotFound)
}

func TestDecideModelApplication_InvalidID(t *testing.T) {
	svc, _ := newAppService()
	sku := int64(1)
	_, err := svc.DecideModelApplication(context.Background(), 0,
		ApplicationDecisionInput{Action: AppActionApprove, TargetSKUID: &sku}, 7, "req-7")
	require.ErrorIs(t, err, ErrApplicationNotFound)
}
