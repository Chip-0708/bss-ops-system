//go:build !short

// Package repo 的 price_confirm_test.go：连真库钉住生效连锁里「倍率静默跟随重算」的锚点。
//
// 背景（7b 复核 P2）：
// copyQuoteItems 的重算规则是「倍率组件 unit_price = 新官方价 × multiplier；绝对价组件原样
// 保留」。这条规则只在 price_confirm.go 的写库路径里存在，confirm_service 层的 17 条单测
// 都到不了 repo 这一层——若有人误把 off.Mul(mult) 改成 off（忘乘 multiplier），没有任何
// 测试会红，倍率静默跟随会静默出错。
//
// 本测试连真库（docker model_bss_pg）走完整 ApplyOfficialPriceChange，断言翻版后的
// quote_component.unit_price 精确等于手算锚点（不许只测"非零"），把这条口径钉死。
//
// 写法与 cost_baseline_represent_test.go 一致：连真库 + 显式事务 + 强制回滚 + 事务外 count 校验，
// 不残留任何业务行。ApplyOfficialPriceChange 内部走 txOf(ctx)（db.FromContext 优先），
// 本测试用 db.WithDB 把外层事务注入 ctx，连锁内的 .Transaction 在外层事务上开 savepoint，
// 末尾强制回滚连同 savepoint 一起撤销。
package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"model_bss/internal/infra/db"
)

// seedLegalSubject 在当前事务里塞一行 legal_subject（COMPANY，USCC 唯一），返回 id。
// USCC 用测试前缀远离真实数据；重复跑同测试时 ON CONFLICT 复用既有行。
func seedLegalSubject(t *testing.T, tx *gorm.DB, tag string) int64 {
	t.Helper()
	now := time.Now().UTC()
	// uscc 列是 varchar(18)，测试 USCC 必须 ≤18 字符。
	uscc := "7B" + tag
	var id int64
	require.NoError(t, tx.Raw(`INSERT INTO legal_subject
		(subject_type, uscc, legal_name, created_at, updated_at, created_by, updated_by)
		VALUES ('COMPANY', ?, ?, ?, ?, 1, 1)
		ON CONFLICT (uscc) WHERE subject_type='COMPANY' DO NOTHING
		RETURNING id`,
		uscc, "7B测试供应商-"+tag, now, now).Scan(&id).Error)
	if id == 0 {
		require.NoError(t, tx.Raw(`SELECT id FROM legal_subject
			WHERE subject_type='COMPANY' AND uscc=?`, uscc).Scan(&id).Error,
			"lookup existing legal_subject")
	}
	return id
}

// seedSupplierProfile 在当前事务里塞一行 supplier_profile（subject_id 唯一），返回 id。
// owner_procurement_operator_id 使用迁移种子中的首个 internal_staff，不依赖自增 ID。
func seedSupplierProfile(t *testing.T, tx *gorm.DB, tag string) int64 {
	t.Helper()
	var ownerID int64
	require.NoError(t, tx.Raw(`SELECT id FROM internal_staff ORDER BY id LIMIT 1`).Scan(&ownerID).Error)
	require.NotZero(t, ownerID, "supplier_profile 测试需要 internal_staff 迁移种子")
	now := time.Now().UTC()
	subjectID := seedLegalSubject(t, tx, tag)
	var id int64
	require.NoError(t, tx.Raw(`INSERT INTO supplier_profile
		(subject_id, channel_type, owner_procurement_operator_id, created_at, updated_at, created_by, updated_by)
		VALUES (?, 'DIRECT', ?, ?, ?, 1, 1)
		ON CONFLICT (subject_id) DO NOTHING
		RETURNING id`,
		subjectID, ownerID, now, now).Scan(&id).Error)
	if id == 0 {
		require.NoError(t, tx.Raw(`SELECT id FROM supplier_profile WHERE subject_id=?`, subjectID).Scan(&id).Error,
			"lookup existing supplier_profile")
	}
	return id
}

// seedEffectiveQuoteSheet 塞一张 EFFECTIVE 报价单（supplier_id 唯一生效，故必须用专用供应商），
// 含一行 quote_item + 两个组件：input 倍率 0.8（旧价 12.00=15×0.8）、freight 绝对价 2.30。
// 返回 (sheetID, itemID)。
func seedEffectiveQuoteSheet(t *testing.T, tx *gorm.DB, supplierID, skuID int64, validFrom time.Time) (int64, int64) {
	t.Helper()
	now := time.Now().UTC()
	far := validFrom.AddDate(0, 3, 0) // valid_to 三个月后

	var sheetID int64
	require.NoError(t, tx.Raw(`INSERT INTO quote_sheet
		(supplier_id, version_no, status, valid_from, valid_to, source, retroactive,
		 created_at, updated_at, created_by, updated_by)
		VALUES (?, 1, 'EFFECTIVE', ?, ?, 'MANUAL', false, ?, ?, 1, 1)
		RETURNING id`,
		supplierID, validFrom, far, now, now).Scan(&sheetID).Error,
		"seed quote_sheet")

	var itemID int64
	require.NoError(t, tx.Raw(`INSERT INTO quote_item
		(quote_sheet_id, sku_id, currency, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, 'USD', ?, ?, 1, 1)
		RETURNING id`,
		sheetID, skuID, now, now).Scan(&itemID).Error,
		"seed quote_item")

	// 倍率组件：multiplier=0.8，旧价 = 旧官方价 15 × 0.8 = 12.00。
	require.NoError(t, tx.Exec(`INSERT INTO quote_component
		(quote_item_id, component_type, multiplier, unit_price, created_at, updated_at, created_by, updated_by)
		VALUES (?, 'input', '0.8', '12.00000000', ?, ?, 1, 1)`,
		itemID, now, now).Error, "seed quote_component input(倍率)")
	// 绝对价组件：multiplier=NULL，价格 2.30，官方价变动不应跟随。
	require.NoError(t, tx.Exec(`INSERT INTO quote_component
		(quote_item_id, component_type, multiplier, unit_price, created_at, updated_at, created_by, updated_by)
		VALUES (?, 'freight', NULL, '2.30000000', ?, ?, 1, 1)`,
		itemID, now, now).Error, "seed quote_component freight(绝对价)")

	return sheetID, itemID
}

// TestApplyOfficialPriceChange_MultiplierAnchor 连真库钉住倍率静默跟随重算：
// sku 官方价 input 15→10，倍率组件(0.8)新价必须 = 10×0.8 = 8.00000000，
// 绝对价组件(2.30)必须原样保留（不跟随）。
//
// 变异钩子：若把 copyQuoteItems 里的 newPrice = off.Mul(mult) 改成 newPrice = off
// （忘乘 multiplier），本测试拿到的 input 新价会是 10.00000000 而非 8.00000000 → 断言红。
func TestApplyOfficialPriceChange_MultiplierAnchor(t *testing.T) {
	dbx := openTestDB(t)
	var sheetID, oldItemID, skuID, supplierID int64

	effectiveTime := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	validFrom := effectiveTime.AddDate(0, -1, 0) // 报价单一个月前生效

	err := dbx.Transaction(func(tx *gorm.DB) error {
		skuID = seedSKU(t, tx, "silent-follow")
		supplierID = seedSupplierProfile(t, tx, "silent-follow")
		sheetID, oldItemID = seedEffectiveQuoteSheet(t, tx, supplierID, skuID, validFrom)

		// 构造 change_request.payload：sku 官方价 input 改成 10（旧 15）。
		payload, err := json.Marshal(map[string]any{
			"direction":      "PRICE_DOWN",
			"effective_time": effectiveTime.Format(time.RFC3339),
			"staging_ids":    []int64{1},
			"sku_ids":        []int64{skuID},
			"prices": map[string]map[string]string{
				fmt.Sprint(skuID): {"input": "10.00000000"},
			},
		})
		require.NoError(t, err)

		// 外层事务注入 ctx，连锁在其上开 savepoint。
		ctx := db.WithDB(context.Background(), tx)
		repo := NewPriceConfirmRepo(NewSupplierRepo(tx))
		require.NoError(t, repo.ApplyOfficialPriceChange(ctx, ApplyOfficialPriceChangeParams{
			ChangeRequestID: 900001,
			Payload:         payload,
			OperatorID:      1,
			OperatorRole:    "INTERNAL",
			RequestID:       "7b-test-silent-follow",
		}))

		// ---- 断言（必须在 force rollback 之前、事务内部，回滚会撤销全部写入） ----

		// 1. 旧单已 EXPIRED，且生成了 SILENT_FOLLOW 新单（EFFECTIVE、submitted_by=NULL）。
		var oldStatus string
		require.NoError(t, tx.Raw(`SELECT status FROM quote_sheet WHERE id=?`, sheetID).Scan(&oldStatus).Error)
		assert.Equal(t, "EXPIRED", oldStatus, "旧报价单应置 EXPIRED")

		var newSheetID int64
		var newSource, newStatus string
		var newSubmittedBy *int64
		require.NoError(t, tx.Raw(`SELECT id, source, status, submitted_by FROM quote_sheet
			WHERE supplier_id=? AND version_no=2`, supplierID).
			Row().Scan(&newSheetID, &newSource, &newStatus, &newSubmittedBy))
		assert.Equal(t, "SILENT_FOLLOW", newSource)
		assert.Equal(t, "EFFECTIVE", newStatus)
		assert.Nil(t, newSubmittedBy, "静默跟随单 submitted_by 必须为 NULL")

		// 2. 核心锚点：新单明细的组件价。
		type comp struct {
			ComponentType string
			UnitPrice     string
		}
		var comps []comp
		require.NoError(t, tx.Raw(`SELECT qc.component_type, qc.unit_price::text
			FROM quote_item qi JOIN quote_component qc ON qc.quote_item_id=qi.id
			WHERE qi.quote_sheet_id=? ORDER BY qc.component_type`, newSheetID).Scan(&comps).Error)
		require.Len(t, comps, 2, "新单应翻版出 2 个组件")
		priceOf := func(ct string) string {
			for _, c := range comps {
				if c.ComponentType == ct {
					return c.UnitPrice
				}
			}
			return ""
		}
		// 倍率组件：新官方价 10 × multiplier 0.8 = 8.00000000（变异 off.Mul(mult)→off 会得 10.00000000）。
		assert.Equal(t, "8.00000000", priceOf("input"),
			"倍率组件必须重算为 新官方价×multiplier（10×0.8=8.00）")
		// 绝对价组件：不跟随，原样保留 2.30。
		assert.Equal(t, "2.30000000", priceOf("freight"),
			"绝对价组件必须原样保留（不跟随官方价变动）")

		// 3. price_version 关旧开新（本测试 SKU 首次设价，is_current=true 且 source=SYNC）。
		var pvSource string
		var pvCurrent bool
		require.NoError(t, tx.Raw(`SELECT source, is_current FROM price_version
			WHERE sku_id=? ORDER BY version_no DESC LIMIT 1`, skuID).Row().Scan(&pvSource, &pvCurrent))
		assert.Equal(t, "SYNC", pvSource)
		assert.True(t, pvCurrent)
		var officialInput string
		require.NoError(t, tx.Raw(`SELECT pc.unit_price::text FROM price_version pv
			JOIN price_component pc ON pc.price_version_id=pv.id
			WHERE pv.sku_id=? AND pv.is_current AND pc.component_type='input'`, skuID).
			Row().Scan(&officialInput))
		assert.Equal(t, "10.00000000", officialInput, "官方价新版本 input 应为 10.00")

		return fmt.Errorf("force rollback")
	})
	require.ErrorContains(t, err, "force rollback")

	// 事务外双保险：种子与连锁写入全部回滚，不得残留。
	var n int64
	require.NoError(t, dbx.Table("quote_sheet").Where("supplier_id = ?", supplierID).Count(&n).Error)
	assert.Zero(t, n, "事务应已回滚，quote_sheet 不得残留")
	require.NoError(t, dbx.Table("price_version").Where("sku_id = ?", skuID).Count(&n).Error)
	assert.Zero(t, n, "事务应已回滚，price_version 不得残留")
	require.NoError(t, dbx.Table("model_sku").Where("id = ?", skuID).Count(&n).Error)
	assert.Zero(t, n, "事务应已回滚，model_sku 不得残留")
	_ = oldItemID
}
