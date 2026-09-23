//go:build !short

// Package repo 的 cost_baseline_constraint_test.go：连真库验证约束映射（6b-4 裁决第 2 条）。
//
// 为什么不进默认 go test：
//   - 依赖真实 PostgreSQL（docker model_bss_pg），约束与 EXCLUDE gist 只在真库存在；
//     sqlite/in-memory 无法复现 23P01 与 23505，也就无法证明错误映射对。
//   - 用 testing.Short() 隔离：默认测试（-short）跳过；E2E 显式用
//     go test ./internal/repo/ -run TestIsVersionConflictConstraint 触发。
//
// 每条用例都在显式事务里制造冲突并断言 SQLSTATE / 约束名后**回滚**——不污染业务数据。
//
// 连接信息：默认读 configs/config.yaml 的 db 段（host/port/user/password/dbname），
// 可用环境变量覆盖：MODEL_BSS_TEST_DSN="postgres://user:pass@host:port/dbname?sslmode=disable"。
package repo

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// testDSN 组装连接串：环境变量优先，否则读 configs/config.yaml。
func testDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("MODEL_BSS_TEST_DSN"); dsn != "" {
		return dsn
	}
	v := viper.New()
	v.SetConfigFile("../../configs/config.yaml")
	if err := v.ReadInConfig(); err != nil {
		t.Skipf("configs/config.yaml 不可读（%v），跳过真库约束测试", err)
	}
	port := v.GetString("db.port")
	if _, err := strconv.Atoi(port); err != nil {
		t.Skipf("db.port 非法（%q），跳过真库约束测试", port)
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&TimeZone=UTC",
		v.GetString("db.user"), v.GetString("db.password"),
		v.GetString("db.host"), port, v.GetString("db.dbname"),
		v.GetString("db.sslmode"))
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("-short：跳过连真库的约束映射测试")
	}
	db, err := gorm.Open(postgres.Open(testDSN(t)), &gorm.Config{})
	require.NoError(t, err, "连接测试库失败——docker model_bss_pg 是否在跑？")
	return db
}

// seedSKU 在当前事务里塞一条 model_sku（满足 cost_baseline 的 FK）。
// 用远离真实数据的 sku_code 前缀 SKU-6B4-，id 由 IDENTITY 自增再 SELECT 回来；
// 事务末尾 Rollback，不残留任何业务行。
// model_family.name 全局唯一：重复跑同一 tag 时用 ON CONFLICT 复用既有家族行。
func seedSKU(t *testing.T, tx *gorm.DB, tag string) int64 {
	t.Helper()
	now := time.Now().UTC()

	// model_family：vendor_id=1 来自 000003 种子；name 全局唯一由 vendor_id 决定。
	// 用 ON CONFLICT 让重复跑同测试也过（不污染真实名字空间）。
	var famID int64
	require.NoError(t, tx.Raw(`INSERT INTO model_family (vendor_id, name, created_at, updated_at, created_by, updated_by)
		VALUES (1, ?, ?, ?, 1, 1)
		ON CONFLICT (vendor_id, name) DO NOTHING
		RETURNING id`,
		"6B4-fam-"+tag, now, now).Scan(&famID).Error)
	if famID == 0 {
		// 冲突即已存在：取回既有家族 id，不新增名字。
		require.NoError(t, tx.Raw(`SELECT id FROM model_family WHERE vendor_id=1 AND name=?`,
			"6B4-fam-"+tag).Scan(&famID).Error, "lookup existing family")
	}

	// model_sku：tags 是 NOT NULL 且有默认 '[]'，显式给上，确保兼容。
	var skuID int64
	require.NoError(t, tx.Raw(`INSERT INTO model_sku
		(family_id, vendor_id, sku_code, model_type, native_currency, tags, created_at, updated_at, created_by, updated_by)
		VALUES (?, 1, ?, 'chat', 'USD', '[]'::jsonb, ?, ?, 1, 1)
		RETURNING id`,
		famID, "SKU-6B4-"+tag, now, now).Scan(&skuID).Error,
		"seed model_sku")
	return skuID
}

// insertBaseline 在当前事务里直接 INSERT 一行 cost_baseline，原样返回 DB 错误（不包装），
// 让调用方能拿到了 pgconn.PgError 断言 SQLSTATE 与 ConstraintName。
func insertBaseline(tx *gorm.DB, skuID int64, version int, isCurrent bool, validFrom time.Time, validTo *time.Time) error {
	now := time.Now().UTC()
	return tx.Exec(`INSERT INTO cost_baseline
		(sku_id, version, currency, primary_supplier_id, loss_rate, channel_rate,
		 calc_snapshot, locked_manual, change_reason, valid_from, valid_to, is_current,
		 created_by, created_at, updated_at, updated_by)
		VALUES (?, ?, 'USD', 1, '0.0300', '0.0100', '{"formula_version":"6b-test"}'::jsonb,
		        false, 'QUOTE_EFFECTIVE', ?, ?, ?, 1, ?, ?, 1)`,
		skuID, version, validFrom, validTo, isCurrent, now, now).Error
}

// TestIsVersionConflictConstraint_UKCostCurrent 连真库验证：
// 部分唯一索引 uk_cost_current 在「同 sku 两行 is_current=true」时触发 23505，
// IsVersionConflictConstraint 把它映射为 ("uk_cost_current", true)。
func TestIsVersionConflictConstraint_UKCostCurrent(t *testing.T) {
	db := openTestDB(t)
	var skuID int64
	err := db.Transaction(func(tx *gorm.DB) error {
		ts := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
		skuID = seedSKU(t, tx, "uk")
		later := ts.Add(time.Hour)

		// 两条有效期首尾相接，避免同时触发 ex_cost_no_overlap；本用例只验证 uk_cost_current。
		require.NoError(t, insertBaseline(tx, skuID, 1, true, ts, &later), "seed 第一条应成功")

		// 第二条同 sku is_current=true（version 区分开免撞 uk_cost_ver）→ 应命中 uk_cost_current(23505)。
		err := insertBaseline(tx, skuID, 2, true, later, nil)
		require.Error(t, err, "同 sku 双 is_current 应报错")

		name, ok := IsVersionConflictConstraint(err)
		assert.True(t, ok, "uk_cost_current 应被识别为版本冲突")
		assert.Equal(t, "uk_cost_current", name)
		var pgErr *pgconn.PgError
		require.True(t, errors.As(err, &pgErr))
		assert.Equal(t, "23505", pgErr.Code, "uk_cost_current 是部分唯一索引 → 23505")

		return fmt.Errorf("force rollback") // 显式回滚不污染数据
	})
	require.ErrorContains(t, err, "force rollback")
	// 双保险：事务外确认种子行没有残留
	var n int64
	require.NoError(t, db.Table("cost_baseline").Where("sku_id = ?", skuID).Count(&n).Error)
	assert.Zero(t, n, "事务应已回滚，种子行不得残留")
}

// TestIsVersionConflictConstraint_ExNoOverlap 连真库验证：
// EXCLUDE gist 约束 ex_cost_no_overlap 在「同 sku 有效区间相交」时触发 23P01，
// IsVersionConflictConstraint 把它映射为 ("ex_cost_no_overlap", true)。
func TestIsVersionConflictConstraint_ExNoOverlap(t *testing.T) {
	db := openTestDB(t)
	var skuID int64
	err := db.Transaction(func(tx *gorm.DB) error {
		ts := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
		skuID = seedSKU(t, tx, "ex")

		// 第一条：[2026-09-15 00:00, +∞)，is_current=true。
		require.NoError(t, insertBaseline(tx, skuID, 1, true, ts, nil), "seed 第一条应成功")

		// 第二条：is_current=false（绕开 uk_cost_current），valid_from 落在 [2026-09-15, +∞) 内部
		// → 与第一条区间相交 → 命中 ex_cost_no_overlap(23P01)。
		overlapFrom := ts.Add(12 * time.Hour)
		err := insertBaseline(tx, skuID, 2, false, overlapFrom, nil)
		require.Error(t, err, "区间相交应报错")

		name, ok := IsVersionConflictConstraint(err)
		assert.True(t, ok, "ex_cost_no_overlap 应被识别为版本冲突")
		assert.Equal(t, "ex_cost_no_overlap", name)
		var pgErr *pgconn.PgError
		require.True(t, errors.As(err, &pgErr))
		assert.Equal(t, "23P01", pgErr.Code, "EXCLUDE gist → 23P01 exclusion_violation")

		return fmt.Errorf("force rollback")
	})
	require.ErrorContains(t, err, "force rollback")
	var n int64
	require.NoError(t, db.Table("cost_baseline").Where("sku_id = ?", skuID).Count(&n).Error)
	assert.Zero(t, n)
}

func TestIsVersionConflictConstraint_UnrelatedError(t *testing.T) {
	// 普通错误（非 PgError）→ ("", false)
	_, ok := IsVersionConflictConstraint(errors.New("connection refused"))
	assert.False(t, ok)
	_, ok = IsVersionConflictConstraint(nil)
	assert.False(t, ok)
	// 非版本冲突的 PG 错误 → ("", false)。断言判据：约束名不在我们为成本基线准备的三条里。
	pgErr := &pgconn.PgError{Code: "23502", ConstraintName: "cost_baseline_pkey"}
	_, ok = IsVersionConflictConstraint(pgErr)
	assert.False(t, ok, "NOT NULL 违例不应被误识别")
	// 23505 但约束名不是成本基线那三条 → ("", false)（Judge by constraint name，不只看 code）
	pgErr = &pgconn.PgError{Code: "23505", ConstraintName: "uk_quote_sheet_id"}
	_, ok = IsVersionConflictConstraint(pgErr)
	assert.False(t, ok)
}
