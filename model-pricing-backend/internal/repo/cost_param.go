// Package repo 的 cost_param.go：成本参数表的 GORM 实现（06-cost §7）。
// 与 QuoteRepo 同一纪律：写操作统一经 txOf(ctx) 取事务（幂等中间件注入的事务优先）。
package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/cost"
)

// CostParamRepo 是 cost_param 的 GORM 仓储（6b 只读；PUT 编辑在后续批次经同一类型落地）。
type CostParamRepo struct {
	*SupplierRepo
}

// NewCostParamRepo 构造参数仓储。
func NewCostParamRepo(base *gorm.DB) *CostParamRepo {
	return &CostParamRepo{SupplierRepo: NewSupplierRepo(base)}
}

type costParamRow struct {
	ID             int64     `gorm:"primaryKey"`
	ScopeType      string    `gorm:"column:scope_type"`
	ScopeID        int64     `gorm:"column:scope_id"`
	LossRate       string    `gorm:"column:loss_rate"`
	ChannelRate    string    `gorm:"column:channel_rate"`
	TaxInclusive   bool      `gorm:"column:tax_inclusive"`
	WithholdingTax string    `gorm:"column:withholding_tax"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (costParamRow) TableName() string { return "cost_param" }

// ListAllParams 取全部参数行（数据量恒小：1 全局 + N 覆盖，一次全取比分层查询简单且够快）。
// 返回 Param（decimal 形态）——仅供**数值消费**（重算引擎、审计 before 等）。
// API 读响应绝不能用它（decimal 已丢尾零），API 响应请走 ListStoredParams。
func (r *CostParamRepo) ListAllParams(ctx context.Context) ([]cost.Param, error) {
	var rows []costParamRow
	if err := r.txOf(ctx).Order("scope_type, scope_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list cost_param: %w", err)
	}
	out := make([]cost.Param, 0, len(rows))
	for i := range rows {
		p, err := paramFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ListStoredParams 取全部参数行的**原样字符串**形态（Task 2 Option A）。
// 比率三字段直接透传 costParamRow 的 string 列（PG numeric(8,4) → Go string 不丢尾零），
// 读侧 API 响应必须从这里取——绝不能经 decimal 中转（String() 会 trim 尾零）。
// 同时做一次轻量数值校验：numeric(8,4) 列既然存在就必然可解析，解析失败说明
// 数据被绕过应用层写脏，按错误上抛不静默（与 paramFromRow 同一纪律）。
func (r *CostParamRepo) ListStoredParams(ctx context.Context) ([]cost.StoredParam, error) {
	var rows []costParamRow
	if err := r.txOf(ctx).Order("scope_type, scope_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list cost_param (stored): %w", err)
	}
	out := make([]cost.StoredParam, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		// 数值合法性兜底（不丢字符串——只是为了尽早发现脏数据）。
		if _, err := decimal.NewFromString(row.LossRate); err != nil {
			return nil, fmt.Errorf("cost_param id=%d loss_rate %q 非法: %w", row.ID, row.LossRate, err)
		}
		if _, err := decimal.NewFromString(row.ChannelRate); err != nil {
			return nil, fmt.Errorf("cost_param id=%d channel_rate %q 非法: %w", row.ID, row.ChannelRate, err)
		}
		if _, err := decimal.NewFromString(row.WithholdingTax); err != nil {
			return nil, fmt.Errorf("cost_param id=%d withholding_tax %q 非法: %w", row.ID, row.WithholdingTax, err)
		}
		out = append(out, cost.StoredParam{
			ScopeType:      row.ScopeType,
			ScopeID:        row.ScopeID,
			LossRate:       row.LossRate,
			ChannelRate:    row.ChannelRate,
			TaxInclusive:   row.TaxInclusive,
			WithholdingTax: row.WithholdingTax,
		})
	}
	return out, nil
}

// paramFromRow 把字符串列转 decimal（numeric(8,4) 精度由 DB 保，转换必然成功——
// 失败说明数据被绕过应用层写脏，按错误上抛不静默）。
func paramFromRow(row *costParamRow) (cost.Param, error) {
	loss, err := decimal.NewFromString(row.LossRate)
	if err != nil {
		return cost.Param{}, fmt.Errorf("cost_param id=%d loss_rate %q 非法: %w", row.ID, row.LossRate, err)
	}
	channel, err := decimal.NewFromString(row.ChannelRate)
	if err != nil {
		return cost.Param{}, fmt.Errorf("cost_param id=%d channel_rate %q 非法: %w", row.ID, row.ChannelRate, err)
	}
	tax, err := decimal.NewFromString(row.WithholdingTax)
	if err != nil {
		return cost.Param{}, fmt.Errorf("cost_param id=%d withholding_tax %q 非法: %w", row.ID, row.WithholdingTax, err)
	}
	return cost.Param{
		ScopeType: row.ScopeType, ScopeID: row.ScopeID,
		LossRate: loss, ChannelRate: channel,
		TaxInclusive: row.TaxInclusive, WithholdingTax: tax,
	}, nil
}

// ---- 6d-2 写路径（PUT /cost/params 的仓储实现） ----

// SKUExists 校验 model_sku.id 存在（MODEL.scope_id 的权威表）。
func (r *CostParamRepo) SKUExists(ctx context.Context, skuID int64) (bool, error) {
	var n int64
	if err := r.txOf(ctx).Table("model_sku").Where("id = ?", skuID).Count(&n).Error; err != nil {
		return false, fmt.Errorf("check model_sku id=%d: %w", skuID, err)
	}
	return n > 0, nil
}

// SupplierExists 校验 supplier_profile.id 存在（SUPPLIER.scope_id 的权威表）。
func (r *CostParamRepo) SupplierExists(ctx context.Context, supplierID int64) (bool, error) {
	var n int64
	if err := r.txOf(ctx).Table("supplier_profile").Where("id = ?", supplierID).Count(&n).Error; err != nil {
		return false, fmt.Errorf("check supplier_profile id=%d: %w", supplierID, err)
	}
	return n > 0, nil
}

// ListParamAffectedSKUs 实现 cost.ParamStore.ListParamAffectedSKUs。
// 三分支各自去重升序（调用方再并集）：
//
//	GLOBAL   → cost_baseline 出现的全部 sku_id（历史+当前都在，重算该覆盖面）；
//	MODEL    → cost_baseline ∩ model_sku（scope_id 即 sku_id——遗留项 6d-2-②）；
//	SUPPLIER → cost_baseline ∩ 该供应商当前 EFFECTIVE 报价覆盖的 sku_id。
func (r *CostParamRepo) ListParamAffectedSKUs(ctx context.Context, scopeType string, scopeID int64) ([]int64, error) {
	tx := r.txOf(ctx)
	set := make(map[int64]struct{})
	switch scopeType {
	case cost.ScopeGlobal:
		var ids []int64
		if err := tx.Table("cost_baseline").Distinct().Pluck("sku_id", &ids).Error; err != nil {
			return nil, fmt.Errorf("list affected skus GLOBAL: %w", err)
		}
		for _, id := range ids {
			set[id] = struct{}{}
		}
	case cost.ScopeModel:
		var ids []int64
		if err := tx.Table("cost_baseline cb").
			Joins("JOIN model_sku ms ON ms.id = cb.sku_id").
			Where("cb.sku_id = ?", scopeID).
			Distinct().Pluck("cb.sku_id", &ids).Error; err != nil {
			return nil, fmt.Errorf("list affected skus MODEL scope_id=%d: %w", scopeID, err)
		}
		for _, id := range ids {
			set[id] = struct{}{}
		}
	case cost.ScopeSupplier:
		var ids []int64
		if err := tx.Table("cost_baseline cb").
			Joins("JOIN quote_sheet qs ON qs.supplier_id = ? AND qs.status = 'EFFECTIVE'", scopeID).
			Joins("JOIN quote_item qi ON qi.quote_sheet_id = qs.id AND qi.sku_id = cb.sku_id").
			Distinct().Pluck("cb.sku_id", &ids).Error; err != nil {
			return nil, fmt.Errorf("list affected skus SUPPLIER scope_id=%d: %w", scopeID, err)
		}
		for _, id := range ids {
			set[id] = struct{}{}
		}
	default:
		return nil, fmt.Errorf("未知 scope_type %q（已知 GLOBAL/MODEL/SUPPLIER）", scopeType)
	}
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// ReplaceOverridesTx 实现 cost.ParamStore.ReplaceOverridesTx。
// 单事务五连写：DELETE 覆盖行 → 逐行 INSERT → audit_log → 逐 SKU 入队 COST_RECALC。
// 事务边界在 repo（与 ApplyNewVersion / MarkFailed 同款纪律——同一纪律见 CLAUDE.md 红线 8）。
func (r *CostParamRepo) ReplaceOverridesTx(ctx context.Context, p cost.ReplaceTxParams) (*cost.ReplaceTxResult, error) {
	out := &cost.ReplaceTxResult{TaskIDs: []int64{}}
	err := r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1) 全量删除 override（GLOBAL 永远不在 deletes 里——服务层裁决，这里防御再收窄一层）。
		safeDeletes := make([]string, 0, len(p.Deletes))
		for _, st := range p.Deletes {
			if st == cost.ScopeGlobal {
				continue // 绝不删 GLOBAL（000016 种子行 + chk_cost_param_global_zero）
			}
			safeDeletes = append(safeDeletes, st)
		}
		if len(safeDeletes) > 0 {
			if err := tx.Where("scope_type IN ?", safeDeletes).Delete(&costParamRow{}).Error; err != nil {
				return fmt.Errorf("delete overrides scope_type in %v: %w", safeDeletes, err)
			}
		}

		// 2) 逐行 INSERT（不用 ON CONFLICT——裁决 2：语义直白 + 不在热点路径）。
		for _, it := range p.Inserts {
			row := costParamRow{
				ScopeType:      it.ScopeType,
				ScopeID:        it.ScopeID,
				LossRate:       it.LossRate,
				ChannelRate:    it.ChannelRate,
				TaxInclusive:   it.TaxInclusive,
				WithholdingTax: it.WithholdingTax,
				CreatedAt:      it.CreatedAt,
				UpdatedAt:      it.UpdatedAt,
			}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("insert override %s/%d: %w", it.ScopeType, it.ScopeID, err)
			}
		}

		// 3) audit_log（红线 10；action=COST_PARAM_UPDATE）。
		beforeJSON, err := json.Marshal(p.Before)
		if err != nil {
			return fmt.Errorf("marshal audit before: %w", err)
		}
		afterJSON, err := json.Marshal(p.After)
		if err != nil {
			return fmt.Errorf("marshal audit after: %w", err)
		}
		auditRow := auditLogRow{
			OperatorID:   p.OperatorID,
			OperatorRole: p.OperatorRole,
			Action:       "COST_PARAM_UPDATE",
			TargetType:   "COST_PARAM",
			TargetID:     0, // 全量替换不是单行操作，target_id 无法指一行——0 是占位（阶段 F 治理）
			BeforeValue:  beforeJSON,
			AfterValue:   afterJSON,
			SourceType:   "HUMAN",
			CreatedAt:    p.Now,
			UpdatedAt:    p.Now,
			RequestID:    strPtrIfNotEmpty(p.RequestID),
			CreatedBy:    int64Ptr(p.OperatorID),
			UpdatedBy:    int64Ptr(p.OperatorID),
		}
		if err := tx.Create(&auditRow).Error; err != nil {
			return fmt.Errorf("insert audit_log: %w", err)
		}
		out.AuditLogID = auditRow.ID

		// 4) 逐 SKU 入队 COST_RECALC（payload 带 sku_id，6d-2 消费端按 sku_id 直接 RecalcSKU）。
		for _, skuID := range p.AffectedSKUs {
			payload, err := json.Marshal(map[string]any{
				"sku_id": skuID,
				"reason": cost.ReasonParamChange,
			})
			if err != nil {
				return fmt.Errorf("marshal recalc payload sku=%d: %w", skuID, err)
			}
			job := taskJobRow{
				JobType:   "COST_RECALC",
				Payload:   payload,
				Status:    "PENDING",
				NextRunAt: p.Now,
				CreatedAt: p.Now,
				UpdatedAt: p.Now,
				RequestID: strPtrIfNotEmpty(p.RequestID),
				CreatedBy: int64Ptr(p.OperatorID),
				UpdatedBy: int64Ptr(p.OperatorID),
			}
			if err := tx.Create(&job).Error; err != nil {
				return fmt.Errorf("enqueue COST_RECALC sku=%d: %w", skuID, err)
			}
			out.TaskIDs = append(out.TaskIDs, job.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// strPtrIfNotEmpty 空串返回 nil（request_id 列可空，空字符串不如 NULL 真诚）。
func strPtrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return strPtr(s)
}
