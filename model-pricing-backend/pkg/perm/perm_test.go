package perm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type fakePermStore struct {
	points []string
	err    error
}

func (f fakePermStore) AllPoints() ([]string, error) {
	return f.points, f.err
}

func TestValidateInDB_Consistent(t *testing.T) {
	store := fakePermStore{points: All()}
	require.NoError(t, ValidateInDB(store))
}

func TestValidateInDB_MissingCodeConstant(t *testing.T) {
	// DB 缺少代码中的一个常量 → 应报错并指出缺少项
	points := All()
	store := fakePermStore{points: points[1:]} // 去掉第一个 M1:V
	err := ValidateInDB(store)
	require.Error(t, err)
	require.Contains(t, err.Error(), "M1:V")
}

func TestValidateInDB_UnknownDBPoint(t *testing.T) {
	// DB 多出一个代码未声明的权限点 → 应报错并指出多余项
	points := append(All(), "M99:X")
	store := fakePermStore{points: points}
	err := ValidateInDB(store)
	require.Error(t, err)
	require.Contains(t, err.Error(), "M99:X")
}

func TestValidateInDB_StoreError(t *testing.T) {
	store := fakePermStore{err: errBoom}
	err := ValidateInDB(store)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load permission points")
}

var errBoom = fakeError("boom")

type fakeError string

func (e fakeError) Error() string { return string(e) }

// ---- 6b-1：CanEditCostParam / CanLockPrimary / CanLockFx 按角色 code 收敛 ----
//
// 背景（06-cost §11-1）：M5:E 权限点在 000007_seed 里被 6 个角色持有，
// 但 MODEL_OPS / RETRO_OP 是历史越权（补录专员能改成本参数就是越权）。
// 必须锁在 pkg/perm 一处，并用表驱动测试把允许/禁止集合钉死。

// allSeedRoles 列出 000007_seed 里存在的全部角色 code（用于穷举断言，防漏判）。
var allSeedRoles = []string{
	RolePlatformAdmin,
	RoleModelOps,
	RoleProcurement,
	RolePricingOp,
	RoleSales,
	RoleFinance,
	RoleRetroOp,
	RoleSupplier,
	RoleCustomer,
}

func TestCanEditCostParam(t *testing.T) {
	for _, role := range allSeedRoles {
		got := CanEditCostParam(role)
		want := role == RolePricingOp
		require.Equalf(t, want, got, "CanEditCostParam(%s)", role)
	}
	// 显式断言越权角色（红线条款）
	require.False(t, CanEditCostParam(RoleModelOps), "MODEL_OPS 不能改成本参数（越权持有 M5:E）")
	require.False(t, CanEditCostParam(RoleRetroOp), "RETRO_OP 不能改成本参数（越权持有 M5:E）")
	require.False(t, CanEditCostParam(""), "空 role 必须拒绝")
}

func TestCanLockPrimary(t *testing.T) {
	for _, role := range allSeedRoles {
		got := CanLockPrimary(role)
		want := role == RoleProcurement
		require.Equalf(t, want, got, "CanLockPrimary(%s)", role)
	}
	require.False(t, CanLockPrimary(RoleModelOps), "MODEL_OPS 不能锁主供应商")
	require.False(t, CanLockPrimary(RoleRetroOp), "RETRO_OP 不能锁主供应商")
	require.False(t, CanLockPrimary(""), "空 role 必须拒绝")
}

func TestCanLockFx(t *testing.T) {
	for _, role := range allSeedRoles {
		got := CanLockFx(role)
		want := role == RoleFinance
		require.Equalf(t, want, got, "CanLockFx(%s)", role)
	}
	require.False(t, CanLockFx(RoleModelOps), "MODEL_OPS 不能锁汇率")
	require.False(t, CanLockFx(RoleRetroOp), "RETRO_OP 不能锁汇率")
	require.False(t, CanLockFx(""), "空 role 必须拒绝")
}

func TestAnyRoleCan(t *testing.T) {
	cases := []struct {
		name  string
		roles []string
		want  bool
	}{
		{"smoke_admin 真值形状：多角色命中一个即放行", []string{RolePlatformAdmin, RoleModelOps, RolePricingOp}, true},
		{"只有越权角色：拒绝", []string{RoleModelOps, RoleRetroOp}, false},
		{"空列表：拒绝", nil, false},
		{"全部角色都不命中：拒绝", []string{RoleSales, RoleFinance, RoleProcurement}, false},
		{"单角色命中：放行", []string{RolePricingOp}, true},
		{"空串角色：拒绝", []string{""}, false},
		{"命中不在首位也要看到", []string{RoleSales, RolePricingOp}, true},
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, AnyRoleCan(tc.roles, CanEditCostParam),
			"AnyRoleCan(%v)", tc.roles)
	}
	// single 为 nil 绝不调用（防御：不能把 nil 函数当判定器）
	require.False(t, AnyRoleCan([]string{RolePricingOp}, nil))
}
