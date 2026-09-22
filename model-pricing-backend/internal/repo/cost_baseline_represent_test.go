//go:build !short

// Package repo 的 cost_baseline_represent_test.go：把两条「代表组件」口径钉在一起。
//
// 背景（6b-4 复核 P2）：
// 代表组件口径（input 优先，无 input 取 component_type 字母序第一个）现在有两份实现：
//  1. Go 纯函数：internal/domain/cost/compare.go 的 RepresentativeComponent
//     （重算 Service 在内存里用它）；
//  2. SQL：internal/repo/cost_baseline.go 的 representCompJoin（LEFT JOIN ... DISTINCT ON），
//     读侧列表必须在 SQL 里聚合，拿不回 []Component 进 Go，所以读侧走这份。
//
// 两者语义必须恒等，但物理上是两份代码——改一处不改另一处会静默漂移，
// unit_cost_basis 列与重算的快照口径对不上还很难查。这个测试把两者放同一个事务里
// 对着同一批真实数据各跑一遍，断言结果 bit-for-bit 相等：
// 任何一侧改了口径，这里先炸。
//
// 用例覆盖两种决策路径：
//   - 含 input 的基线：input 必须压过字母序最前的 audio（验证「input 优先」真的生效）；
//   - 无 input 的基线：取字母序第一个（audio < output，验证字母序 fallback）。
//
// 写法与 cost_baseline_constraint_test.go 一致：连真库 + 显式事务 + 强制回滚 + 事务外 count 校验。
package repo

import (
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"model_bss/internal/domain/cost"
)

// representSQLRow 是 representCompJoin 对一单条基线的查询结果。
type representSQLRow struct {
	ComponentType string
	UnitCost      string
}

// sqlRepresent 在事务内对指定基线用 representCompJoin 的 SQL 取代表组件。
// 把常量里的 JOIN 片段直接拼进一个只查一条 cb 的单行查询——与生产列表共享同一份 SQL。
func sqlRepresent(t *testing.T, tx *gorm.DB, baselineID int64) representSQLRow {
	t.Helper()
	var row representSQLRow
	q := `SELECT rc.component_type, rc.unit_cost
		FROM cost_baseline cb ` + representCompJoin + `
		WHERE cb.id = ?`
	require.NoError(t, tx.Raw(q, baselineID).Scan(&row).Error,
		"representCompJoin 直查失败")
	return row
}

// goRepresent 用 Go 纯函数 RepresentativeComponent 对同一基线取代表组件，
// unit_cost 从库里按组件行组装成 []cost.Component（模拟重算侧的输入口径）。
func goRepresent(t *testing.T, tx *gorm.DB, baselineID int64) (string, string, bool) {
	t.Helper()
	var comps []struct {
		ComponentType string
		UnitCost      string
	}
	require.NoError(t, tx.Raw(`SELECT component_type, unit_cost::text
		FROM cost_component WHERE cost_baseline_id = ?`, baselineID).Scan(&comps).Error,
		"读取基线组件失败")

	cs := make([]cost.Component, 0, len(comps))
	for _, c := range comps {
		d, err := decimal.NewFromString(c.UnitCost)
		require.NoError(t, err)
		cs = append(cs, cost.Component{ComponentType: c.ComponentType, UnitCost: d})
	}

	typ, ok := cost.RepresentativeComponent(cs)
	if !ok {
		return "", "", false
	}
	var unit string
	for _, c := range cs {
		if c.ComponentType == typ {
			unit = c.UnitCost.StringFixed(8)
			break
		}
	}
	return typ, unit, true
}

// insertComponent 在当前事务里给基线塞一行组件（uk_cc 保证 (baseline, type) 唯一）。
func insertComponent(t *testing.T, tx *gorm.DB, baselineID int64, compType, unitCost string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, tx.Exec(`INSERT INTO cost_component
		(cost_baseline_id, component_type, unit_cost, supplier_cost, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, 1, 1)`,
		baselineID, compType, unitCost, unitCost, now, now).Error,
		"seed cost_component %s", compType)
}

// assertRepresentConsistent 对一条基线断言 SQL 与 Go 两条路径结果一致。
// expectType/expectUnit 是语义预期值（让测试本身说出「应该选谁」），顺带证两条路径的语义都对。
func assertRepresentConsistent(t *testing.T, tx *gorm.DB, baselineID int64, expectType, expectUnit string) {
	t.Helper()

	sql := sqlRepresent(t, tx, baselineID)
	goType, goUnit, ok := goRepresent(t, tx, baselineID)

	require.True(t, ok, "Go RepresentativeComponent 应有结果")

	assert.Equal(t, expectType, sql.ComponentType, "SQL 代表组件类型应=%s", expectType)
	assert.Equal(t, expectUnit, sql.UnitCost, "SQL 代表组件 unit_cost 应=%s", expectUnit)

	assert.Equal(t, sql.ComponentType, goType, "SQL 与 Go 的 component_type 必须一致")
	assert.Equal(t, sql.UnitCost, goUnit, "SQL 与 Go 的 unit_cost 必须一致（StringFixed(8) 口径）")
}

// TestRepresentComponentConsistency 连真库钉住两种决策路径下 SQL 与 Go 代表组件口径一致。
func TestRepresentComponentConsistency(t *testing.T) {
	db := openTestDB(t)
	var skuA, skuB, baseA, baseB int64

	err := db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()

		// 基线 A：含 input —— audio 在字母序最前，用来验证 input 优先真的压过字母序。
		skuA = seedSKU(t, tx, "rep-a")
		baseA = seedBaselineForRepresent(t, tx, skuA, now)
		insertComponent(t, tx, baseA, "audio", "9.00000000")
		insertComponent(t, tx, baseA, "input", "1.23456789")
		insertComponent(t, tx, baseA, "output", "5.50000000")

		// 基线 B：无 input —— 字母序第一个应是 audio（audio < output）。
		skuB = seedSKU(t, tx, "rep-b")
		baseB = seedBaselineForRepresent(t, tx, skuB, now)
		insertComponent(t, tx, baseB, "output", "5.50000000")
		insertComponent(t, tx, baseB, "audio", "7.00000000")

		// 决策路径 1：有 input 时选 input，无视 audio 更便宜/字母序更前。
		assertRepresentConsistent(t, tx, baseA, "input", "1.23456789")
		// 决策路径 2：无 input 时取字母序第一个 = audio。
		assertRepresentConsistent(t, tx, baseB, "audio", "7.00000000")

		return fmt.Errorf("force rollback")
	})
	require.ErrorContains(t, err, "force rollback")

	// 事务外双保险：两条种子基线及其组件都必须随回滚消失。
	var n int64
	require.NoError(t, db.Table("cost_baseline").Where("sku_id IN ?", []int64{skuA, skuB}).Count(&n).Error)
	assert.Zero(t, n, "事务应已回滚，不得残留")
	require.NoError(t, db.Table("cost_component").Where("cost_baseline_id IN ?", []int64{baseA, baseB}).Count(&n).Error)
	assert.Zero(t, n, "事务应已回滚，组件不得残留")
}

// seedBaselineForRepresent 用最小列集塞一行 cost_baseline（只服务本测试，避免依赖 insertBaseline 的列默认值差异）。
func seedBaselineForRepresent(t *testing.T, tx *gorm.DB, skuID int64, now time.Time) int64 {
	t.Helper()
	var id int64
	require.NoError(t, tx.Raw(`INSERT INTO cost_baseline
		(sku_id, version, currency, primary_supplier_id, loss_rate, channel_rate,
		 calc_snapshot, locked_manual, change_reason, valid_from, valid_to, is_current,
		 created_by, created_at, updated_at, updated_by)
		VALUES (?, 1, 'USD', 1, '0.0300', '0.0100', '{"formula_version":"6b-test"}'::jsonb,
		        false, 'QUOTE_EFFECTIVE', ?, NULL, true, 1, ?, ?, 1)
		RETURNING id`,
		skuID, now.UTC(), now.UTC(), now.UTC()).Scan(&id).Error,
		"seed cost_baseline")
	return id
}
