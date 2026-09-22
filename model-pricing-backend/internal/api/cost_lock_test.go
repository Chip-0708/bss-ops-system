// Package api_test 的 cost_lock_test.go：锁定主供应商 handler 的服务层角色收敛钉死（变异 #3 防线）。
//
// 变异验证 #3 把 internal/api/cost_lock.go 里的
//
//	if !perm.AnyRoleCan(op.Roles, perm.CanLockPrimary) {
//
// 改成
//
//	if !perm.CanLockPrimary(op.Roles[0]) {
//
// 这种变异**不影响**任何 LockService 单测（单测根本不过 handler 的角色判断），
// 但会让「Roles 中 PROCUREMENT 不在首位」的操作员被错误拒绝/放行。
//
// 本文件用真 handler + 真 middleware.Operator 上下文打真实 HTTP 调用：
//   - 任何不含 PROCUREMENT 的角色组合 → 403；
//   - 含 PROCUREMENT 但不为首元素 → 必须放行（AnyRoleCan 扫全角色的实证）。
//
// 唯一已知会绕过本测试的写法是 handler 完全跳过 perm.AnyRoleCan/CanLockPrimary，
// 那种重构不属于本钉子的覆盖范围。
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"model_bss/internal/api"
	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/cost"
)

// fakeLockReadStore 是 cost.BaselineReadStore 的最小实现（只为 ResolveSKUID 让路，
// 其余方法返回零值/错误——本测试不触碰它们）。
type fakeLockReadStore struct{}

func (fakeLockReadStore) ListBaselines(context.Context, cost.BaselineListQuery) ([]cost.BaselineListItem, int64, error) {
	return nil, 0, errors.New("fakeLockReadStore.ListBaselines: not implemented")
}
func (fakeLockReadStore) ResolveSKUID(_ context.Context, sku string) (int64, bool, error) {
	if sku == "40" {
		return 40, true, nil
	}
	return 0, false, nil
}
func (fakeLockReadStore) ListBaselineHistory(context.Context, int64, *time.Time) ([]cost.BaselineHistoryItem, error) {
	return nil, nil
}
func (fakeLockReadStore) LoadMinGrossMargin(context.Context) (decimal.Decimal, error) {
	return decimal.Zero, nil
}

// 6d-4 追加的 BaselineReadStore 方法——本测试不触碰，返回零值让编译过。
func (fakeLockReadStore) ListBaselineTrend(context.Context, int64, int) ([]cost.TrendPoint, error) {
	return nil, nil
}
func (fakeLockReadStore) ListSKUIDs(context.Context) ([]int64, error) { return nil, nil }
func (fakeLockReadStore) LoadSysConfigDecimal(context.Context, string) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (fakeLockReadStore) ListPrimarySupplierNames(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{}, nil
}
func (fakeLockReadStore) ListPrimarySupplierIDs(context.Context, []int64) (map[int64]int64, error) {
	return map[int64]int64{}, nil
}

// performLockPrimary 用真实 handler 起 gin 路由，注入指定角色，发真实 HTTP 请求。
// svc 传 nil：本测试断言的全部是「角色校验阶段就被拒」，根本到不了 svc 调用。
// PROCUREMENT 放行场景里 svc 是 nil，穿门后 LockService.LockPrimary 会以 ErrLockStoreNil
// 走 default 分支返回 500——**这恰好是「已穿过 AnyRoleCan 那一道门」的强证据**：
// 若角色门被拒，拿到的将是 403 而不是 500。
func performLockPrimary(t *testing.T, roles []string, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 注入 middleware.Operator（与 AuthN 同一 ctxKey），跳过 token 校验。
	r.Use(func(c *gin.Context) {
		c.Set("auth.operator", &middleware.Operator{
			PortalType:   "INTERNAL",
			OperatorType: "STAFF",
			OperatorID:   42,
			Roles:        roles,
		})
		c.Next()
	})
	h := api.NewCostLockHandler(nil, cost.NewReadService(fakeLockReadStore{}))
	r.POST("/x/:sku/lock-primary", h.LockPrimary)

	req := httptest.NewRequest(http.MethodPost, "/x/40/lock-primary", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestLockPrimary_RoleGate_ProcurementAllowed PROCUREMENT 角色必须放行（通过角色门，
// 进 svc 才失败——svc 是 nil 时报 ErrLockStoreNil→500，这恰好证明已穿过 AnyRoleCan 门）。
func TestLockPrimary_RoleGate_ProcurementAllowed(t *testing.T) {
	rec := performLockPrimary(t, []string{"PROCUREMENT"}, `{"supplier_id":1,"reason":"x"}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code, "穿过角色门后 nil svc 应报 500")
	require.NotContains(t, rec.Body.String(), `"code":10003`, "绝不能是 403")
}

// TestLockPrimary_RoleGate_ProcurementSecondAlsoAllowed 关键防线：PROCUREMENT 不在 Roles[0]
// 时也必须放行——这正是 AnyRoleCan 与 Roles[0] 的分水岭。变异 #3 在这里必然红。
func TestLockPrimary_RoleGate_ProcurementSecondAlsoAllowed(t *testing.T) {
	rec := performLockPrimary(t, []string{"PLATFORM_ADMIN", "PROCUREMENT"}, `{"supplier_id":1,"reason":"x"}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"AnyRoleCan 应放行；若 handler 用了 Roles[0]，PLATFORM_ADMIN 不在白名单会导致 403，变异暴露")
}

// TestLockPrimary_RoleGate_PlatformAdminDenied smoke_admin 的真值形状必须被拒
// （PLATFORM_ADMIN 持 M5:E 但不含 PROCUREMENT）。
func TestLockPrimary_RoleGate_PlatformAdminDenied(t *testing.T) {
	rec := performLockPrimary(t, []string{"PLATFORM_ADMIN", "MODEL_OPS", "PRICING_OP"}, `{"supplier_id":1,"reason":"x"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, float64(10003), body["code"])
}

// TestLockPrimary_RoleGate_FinanceDenied FINANCE 持 M5:E 但不在 CanLockPrimary 白名单。
func TestLockPrimary_RoleGate_FinanceDenied(t *testing.T) {
	rec := performLockPrimary(t, []string{"FINANCE"}, `{"supplier_id":1,"reason":"x"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestLockPrimary_RoleGate_ModelOpsDenied MODEL_OPS 同样持 M5:E 但必须被拒。
func TestLockPrimary_RoleGate_ModelOpsDenied(t *testing.T) {
	rec := performLockPrimary(t, []string{"MODEL_OPS"}, `{"supplier_id":1,"reason":"x"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestLockPrimary_RoleGate_EmptyRoles 空角色必须拒（防御 nil/空切片）。
func TestLockPrimary_RoleGate_EmptyRoles(t *testing.T) {
	rec := performLockPrimary(t, []string{}, `{"supplier_id":1,"reason":"x"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
}
