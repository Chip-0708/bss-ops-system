// Package repo 的 cost_baseline.go：成本基线不可变版本的 GORM 实现（06-cost §0.1/§0.2/§9）。
//
// 并发三道防线（§10-7）：
//  1. LoadCurrentBaselineForUpdate 用 SELECT ... FOR UPDATE 行锁串行化同 SKU 并发重算；
//  2. 值未变跳过（domain 层判定）；
//  3. uk_cost_current(23505) / ex_cost_no_overlap(23P01) 约束兜底——撞了是预期行为，
//     映射 cost.ErrVersionConflict 上抛，由消费者记 FAILED 重试，不"修"逻辑（交接 §4）。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"model_bss/internal/domain/cost"
)

// CostBaselineRepo 是 cost.Store 的 GORM 实现。
type CostBaselineRepo struct {
	*SupplierRepo
	audit *AuditRepo
}

// NewCostBaselineRepo 构造基线仓储。
func NewCostBaselineRepo(base *gorm.DB) *CostBaselineRepo {
	return &CostBaselineRepo{SupplierRepo: NewSupplierRepo(base), audit: NewAuditRepo(base)}
}

var _ cost.Store = (*CostBaselineRepo)(nil)

// ---- 行模型（与 CLAUDE.md 纪律一致：写模型只含真实列，JOIN 别名只进查询专用结构） ----

type costBaselineRow struct {
	ID                int64      `gorm:"primaryKey"`
	SkuID             int64      `gorm:"column:sku_id"`
	Version           int        `gorm:"column:version"`
	Currency          string     `gorm:"column:currency"`
	PrimarySupplierID int64      `gorm:"column:primary_supplier_id"`
	LossRate          string     `gorm:"column:loss_rate"`
	ChannelRate       string     `gorm:"column:channel_rate"`
	BackupSequence    []byte     `gorm:"column:backup_sequence"`
	CalcSnapshot      []byte     `gorm:"column:calc_snapshot"`
	LockedManual      bool       `gorm:"column:locked_manual"`
	ChangeReason      string     `gorm:"column:change_reason"`
	ValidFrom         time.Time  `gorm:"column:valid_from"`
	ValidTo           *time.Time `gorm:"column:valid_to"`
	IsCurrent         bool       `gorm:"column:is_current"`
	CreatedBy         int64      `gorm:"column:created_by"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
	RequestID         *string    `gorm:"column:request_id"`
	UpdatedBy         *int64     `gorm:"column:updated_by"`
}

func (costBaselineRow) TableName() string { return "cost_baseline" }

type costComponentRow struct {
	ID             int64     `gorm:"primaryKey"`
	CostBaselineID int64     `gorm:"column:cost_baseline_id"`
	ComponentType  string    `gorm:"column:component_type"`
	UnitCost       string    `gorm:"column:unit_cost"`
	SupplierCost   string    `gorm:"column:supplier_cost"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
	RequestID      *string   `gorm:"column:request_id"`
	CreatedBy      *int64    `gorm:"column:created_by"`
	UpdatedBy      *int64    `gorm:"column:updated_by"`
}

func (costComponentRow) TableName() string { return "cost_component" }

// isVersionConflictErr 识别 23505（唯一）/ 23P01（排他）——成本域的预期并发信号。
func isVersionConflictErr(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" || pgErr.Code == "23P01"
	}
	return false
}

// ListRecalcTargets 实现 cost.Store.ListRecalcTargets（该 sheet 的 SKU ∪ 该供应商 EFFECTIVE 的 SKU）。
func (r *CostBaselineRepo) ListRecalcTargets(ctx context.Context, supplierID, quoteSheetID int64) ([]int64, error) {
	tx := r.txOf(ctx)
	set := make(map[int64]struct{})
	// 1) 该 sheet 覆盖的 SKU（不依赖 sheet 当前状态——交接 §6-3）
	var fromSheet []int64
	if err := tx.Table("quote_item").Where("quote_sheet_id = ?", quoteSheetID).
		Pluck("sku_id", &fromSheet).Error; err != nil {
		return nil, fmt.Errorf("list sku by sheet=%d: %w", quoteSheetID, err)
	}
	for _, id := range fromSheet {
		set[id] = struct{}{}
	}
	// 2) 该供应商当前 EFFECTIVE 报价覆盖的 SKU
	var fromEffective []int64
	if err := tx.Table("quote_item qi").
		Joins("JOIN quote_sheet qs ON qs.id = qi.quote_sheet_id").
		Where("qs.supplier_id = ? AND qs.status = 'EFFECTIVE'", supplierID).
		Pluck("qi.sku_id", &fromEffective).Error; err != nil {
		return nil, fmt.Errorf("list effective sku supplier=%d: %w", supplierID, err)
	}
	for _, id := range fromEffective {
		set[id] = struct{}{}
	}
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	// 排序保证消费顺序确定（审计/日志可读性）
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// ---- LoadRecalcInput ----

type quoteLoadRow struct {
	SupplierID   int64     `gorm:"column:supplier_id"`
	QuoteSheetID int64     `gorm:"column:quote_sheet_id"`
	QuoteVersion int       `gorm:"column:version_no"`
	ValidFrom    time.Time `gorm:"column:valid_from"`
	Currency     string    `gorm:"column:currency"`
	Constraints  []byte    `gorm:"column:constraints_"`
	SkuCode      string    `gorm:"column:sku_code"`
	SkuCurrency  string    `gorm:"column:sku_currency"`
	OfficialVer  *int      `gorm:"column:official_ver"`
	ItemID       int64     `gorm:"column:item_id"`
	CompType     string    `gorm:"column:component_type"`
	Multiplier   *string   `gorm:"column:multiplier"`
	UnitPrice    string    `gorm:"column:unit_price"`
}

// LoadRecalcInput 实现 cost.Store.LoadRecalcInput（多供应商全集 + 参数 + 冻结状态 + 官方价版本）。
func (r *CostBaselineRepo) LoadRecalcInput(ctx context.Context, skuID int64) (*cost.RecalcInput, error) {
	tx := r.txOf(ctx)
	var rows []quoteLoadRow
	err := tx.Table("quote_sheet qs").
		Select(`qs.supplier_id, qs.id AS quote_sheet_id, qs.version_no, qs.valid_from,
			qi.id AS item_id, qi.currency, qi.constraints_,
			ms.sku_code, ms.native_currency AS sku_currency,
			pv.version_no AS official_ver,
			qc.component_type, qc.multiplier, qc.unit_price`).
		Joins("JOIN quote_item qi ON qi.quote_sheet_id = qs.id AND qi.sku_id = ?", skuID).
		Joins("JOIN model_sku ms ON ms.id = qi.sku_id").
		Joins("LEFT JOIN price_version pv ON pv.sku_id = qi.sku_id AND pv.is_current").
		Joins("LEFT JOIN quote_component qc ON qc.quote_item_id = qi.id").
		Where("qs.status = 'EFFECTIVE'").
		Order("qs.supplier_id, qc.component_type").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load quotes sku=%d: %w", skuID, err)
	}

	input := &cost.RecalcInput{SKUID: skuID, Quotes: []cost.QuoteInput{}}
	quoteIdx := make(map[int64]int) // quote_sheet_id → input.Quotes 下标
	for i := range rows {
		rw := &rows[i]
		if input.SKUCode == "" {
			input.SKUCode = rw.SkuCode
			input.Currency = rw.SkuCurrency
			input.OfficialVerNo = rw.OfficialVer
		}
		idx, ok := quoteIdx[rw.QuoteSheetID]
		if !ok {
			var constraints map[string]any
			if len(rw.Constraints) > 0 {
				if err := json.Unmarshal(rw.Constraints, &constraints); err != nil {
					return nil, fmt.Errorf("parse constraints sheet=%d: %w", rw.QuoteSheetID, err)
				}
			}
			input.Quotes = append(input.Quotes, cost.QuoteInput{
				SupplierID: rw.SupplierID, QuoteSheetID: rw.QuoteSheetID, QuoteVersion: rw.QuoteVersion,
				ValidFrom: rw.ValidFrom, Currency: rw.Currency, Constraints: constraints,
				Components: []cost.QuoteComponentInput{},
			})
			idx = len(input.Quotes) - 1
			quoteIdx[rw.QuoteSheetID] = idx
		}
		if rw.CompType != "" {
			price, perr := decimal.NewFromString(rw.UnitPrice)
			if perr != nil {
				return nil, fmt.Errorf("quote_component unit_price %q 非法: %w", rw.UnitPrice, perr)
			}
			var mult *decimal.Decimal
			if rw.Multiplier != nil {
				m, merr := decimal.NewFromString(*rw.Multiplier)
				if merr != nil {
					return nil, fmt.Errorf("quote_component multiplier %q 非法: %w", *rw.Multiplier, merr)
				}
				mult = &m
			}
			q := &input.Quotes[idx]
			q.Components = append(q.Components, cost.QuoteComponentInput{
				ComponentType: rw.CompType, UnitPrice: price, Multiplier: mult,
			})
		}
	}

	if input.SKUCode == "" {
		// 无 EFFECTIVE 报价行——SKU 本身可能仍存在（NO_QUOTE 场景），补 sku_code/currency
		var sku struct {
			SKUCode  string `gorm:"column:sku_code"`
			Currency string `gorm:"column:native_currency"`
		}
		if err := tx.Table("model_sku").Select("sku_code, native_currency").
			Where("id = ?", skuID).Take(&sku).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return input, nil // SKU 不存在：保持空，domain 视为 NO_QUOTE
			}
			return nil, fmt.Errorf("load sku=%d: %w", skuID, err)
		}
		input.SKUCode = sku.SKUCode
		input.Currency = sku.Currency
		var ov *int
		if err := tx.Table("price_version").Select("version_no").
			Where("sku_id = ? AND is_current", skuID).Scan(&ov).Error; err != nil {
			return nil, fmt.Errorf("load official ver sku=%d: %w", skuID, err)
		}
		input.OfficialVerNo = ov
	}

	// 生效历史区间（6d 稳定性起算）：按 (supplier_id, sku_id) 取 EFFECTIVE+EXPIRED 的
	// valid_from/valid_to，单条查询按供应商分组挂回——禁止逐供应商 N+1（6b-4 查询纪律）。
	// 必须 JOIN quote_item 过滤本 SKU：一张单覆盖多个 SKU（uk_quote_effective 约束在
	// supplier 维度不在 sku 维度，如 sheet 19 同时含 sku40/sku43），只用 supplier_id 会串数据。
	if len(input.Quotes) > 0 {
		supplierIDs := make([]int64, 0, len(input.Quotes))
		seen := make(map[int64]bool)
		for _, q := range input.Quotes {
			if !seen[q.SupplierID] {
				seen[q.SupplierID] = true
				supplierIDs = append(supplierIDs, q.SupplierID)
			}
		}
		var rangeRows []struct {
			SupplierID int64     `gorm:"column:supplier_id"`
			ValidFrom  time.Time `gorm:"column:valid_from"`
			ValidTo    time.Time `gorm:"column:valid_to"`
		}
		if err := tx.Table("quote_sheet qs").
			Select("DISTINCT qs.supplier_id, qs.valid_from, qs.valid_to").
			Joins("JOIN quote_item qi ON qi.quote_sheet_id = qs.id AND qi.sku_id = ?", skuID).
			Where("qs.supplier_id IN ? AND qs.status IN ('EFFECTIVE','EXPIRED')", supplierIDs).
			Scan(&rangeRows).Error; err != nil {
			return nil, fmt.Errorf("load effective ranges sku=%d: %w", skuID, err)
		}
		rangeBySupplier := make(map[int64][]cost.EffectiveRange, len(supplierIDs))
		for _, rw := range rangeRows {
			rangeBySupplier[rw.SupplierID] = append(rangeBySupplier[rw.SupplierID],
				cost.EffectiveRange{From: rw.ValidFrom, To: rw.ValidTo})
		}
		for i := range input.Quotes {
			input.Quotes[i].EffectiveRanges = rangeBySupplier[input.Quotes[i].SupplierID]
		}
	}

	// 参数全集（一次取出，domain 层 ResolveParams 做三级解析）
	paramRepo := NewCostParamRepo(r.baseOf())
	params, err := paramRepo.ListAllParams(ctx)
	if err != nil {
		return nil, err
	}
	input.Params = params

	// 涉及供应商的冻结状态（§10-6：计算时排除）
	if len(input.Quotes) > 0 {
		supplierIDs := make([]int64, 0, len(input.Quotes))
		seen := make(map[int64]bool)
		for _, q := range input.Quotes {
			if !seen[q.SupplierID] {
				seen[q.SupplierID] = true
				supplierIDs = append(supplierIDs, q.SupplierID)
			}
		}
		var statuses []cost.SupplierStatus
		if err := tx.Table("supplier_profile").
			Select("id AS supplier_id, qual_status, settle_status, status").
			Where("id IN ?", supplierIDs).Scan(&statuses).Error; err != nil {
			return nil, fmt.Errorf("load supplier status sku=%d: %w", skuID, err)
		}
		input.Statuses = statuses
	}
	return input, nil
}

// baseOf 暴露基础 DB（CostParamRepo 构造用，与 txOf 约定其他仓储一致）。
func (r *CostBaselineRepo) baseOf() *gorm.DB { return r.base }

// LoadCurrentBaselineForUpdate 实现 cost.Store.LoadCurrentBaselineForUpdate（SELECT ... FOR UPDATE）。
func (r *CostBaselineRepo) LoadCurrentBaselineForUpdate(ctx context.Context, skuID int64) (*cost.Baseline, error) {
	tx := r.txOf(ctx)
	var row costBaselineRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("sku_id = ? AND is_current", skuID).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("lock baseline sku=%d: %w", skuID, err)
	}
	b, err := baselineFromRow(&row)
	if err != nil {
		return nil, err
	}
	var comps []costComponentRow
	if err := tx.Where("cost_baseline_id = ?", row.ID).Order("component_type").Find(&comps).Error; err != nil {
		return nil, fmt.Errorf("load components baseline=%d: %w", row.ID, err)
	}
	for i := range comps {
		uc, err := decimal.NewFromString(comps[i].UnitCost)
		if err != nil {
			return nil, fmt.Errorf("cost_component unit_cost %q 非法: %w", comps[i].UnitCost, err)
		}
		sc, err := decimal.NewFromString(comps[i].SupplierCost)
		if err != nil {
			return nil, fmt.Errorf("cost_component supplier_cost %q 非法: %w", comps[i].SupplierCost, err)
		}
		b.Components = append(b.Components, cost.Component{
			ComponentType: comps[i].ComponentType, UnitCost: uc, SupplierCost: sc,
		})
	}
	return b, nil
}

// baselineFromRow 行转领域（calc_snapshot.formula_version 回填 Baseline.FormulaVersion——
// 它是判定字段，读侧必须从快照还原，否则旧版本永远判 changed）。
func baselineFromRow(row *costBaselineRow) (*cost.Baseline, error) {
	loss, err := decimal.NewFromString(row.LossRate)
	if err != nil {
		return nil, fmt.Errorf("baseline id=%d loss_rate %q 非法: %w", row.ID, row.LossRate, err)
	}
	channel, err := decimal.NewFromString(row.ChannelRate)
	if err != nil {
		return nil, fmt.Errorf("baseline id=%d channel_rate %q 非法: %w", row.ID, row.ChannelRate, err)
	}
	formulaVersion := ""
	if len(row.CalcSnapshot) > 0 {
		var snap struct {
			FormulaVersion string `json:"formula_version"`
		}
		if err := json.Unmarshal(row.CalcSnapshot, &snap); err != nil {
			return nil, fmt.Errorf("baseline id=%d calc_snapshot 解析失败: %w", row.ID, err)
		}
		formulaVersion = snap.FormulaVersion
	}
	return &cost.Baseline{
		Version: row.Version, Currency: row.Currency, PrimarySupplierID: row.PrimarySupplierID,
		LossRate: loss, ChannelRate: channel, LockedManual: row.LockedManual,
		ChangeReason: row.ChangeReason, Components: []cost.Component{},
		FormulaVersion: formulaVersion,
	}, nil
}

// ApplyNewVersion 实现 cost.Store.ApplyNewVersion（旧关+新插+组件+审计+事件，单事务）。
func (r *CostBaselineRepo) ApplyNewVersion(ctx context.Context, p cost.ApplyParams) (int64, int, error) {
	var newID int64
	newVersion := p.PreviousVersion + 1
	err := r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 关旧版本（条件更新当前版本；0 行 = 已被并发事务关闭/换版 → 预期冲突）
		if p.HasPrevious {
			res := tx.Model(&costBaselineRow{}).
				Where("sku_id = ? AND is_current", p.SKUID).
				Updates(map[string]any{
					"is_current": false, "valid_to": p.EventTime,
					"updated_at": p.EventTime, "updated_by": p.Identity.OperatorID,
				})
			if res.Error != nil {
				if isVersionConflictErr(res.Error) {
					return cost.ErrVersionConflict
				}
				return fmt.Errorf("close previous baseline sku=%d: %w", p.SKUID, res.Error)
			}
			if res.RowsAffected == 0 {
				return cost.ErrVersionConflict
			}
		}
		// 2. 新版本 INSERT——door NOT NULL 列全部显式初始化（CLAUDE.md 编码约定）。
		//    unit_cost 等金额列存 StringFixed(8) 规范化 8 位（与 numeric(20,8) 对齐）。
		snapshot, err := json.Marshal(p.CalcSnapshot)
		if err != nil {
			return fmt.Errorf("marshal calc_snapshot: %w", err)
		}
		var backup []byte
		if len(p.BackupSequence) > 0 {
			if backup, err = json.Marshal(p.BackupSequence); err != nil {
				return fmt.Errorf("marshal backup_sequence: %w", err)
			}
		}
		row := costBaselineRow{
			SkuID: p.SKUID, Version: newVersion, Currency: p.Baseline.Currency,
			PrimarySupplierID: p.Baseline.PrimarySupplierID,
			LossRate:          p.Baseline.LossRate.StringFixed(4),
			ChannelRate:       p.Baseline.ChannelRate.StringFixed(4),
			BackupSequence:    backup, CalcSnapshot: snapshot,
			LockedManual: p.Baseline.LockedManual, ChangeReason: p.Baseline.ChangeReason,
			ValidFrom: p.EventTime, ValidTo: nil, IsCurrent: true,
			CreatedBy: p.Identity.OperatorID, CreatedAt: p.EventTime, UpdatedAt: p.EventTime,
			RequestID: strPtr(p.RequestID), UpdatedBy: int64Ptr(p.Identity.OperatorID),
		}
		if err := tx.Create(&row).Error; err != nil {
			if isVersionConflictErr(err) {
				return cost.ErrVersionConflict
			}
			return fmt.Errorf("insert baseline sku=%d ver=%d: %w", p.SKUID, newVersion, err)
		}
		newID = row.ID
		// 3. 组件批量 INSERT（单事务内）
		comps := make([]costComponentRow, 0, len(p.Baseline.Components))
		for _, c := range p.Baseline.Components {
			comps = append(comps, costComponentRow{
				CostBaselineID: newID, ComponentType: c.ComponentType,
				UnitCost: c.UnitCost.StringFixed(8), SupplierCost: c.SupplierCost.StringFixed(8),
				CreatedAt: p.EventTime, UpdatedAt: p.EventTime,
				RequestID: strPtr(p.RequestID),
				CreatedBy: int64Ptr(p.Identity.OperatorID), UpdatedBy: int64Ptr(p.Identity.OperatorID),
			})
		}
		if len(comps) > 0 {
			if err := tx.Create(&comps).Error; err != nil {
				return fmt.Errorf("insert components baseline=%d: %w", newID, err)
			}
		}
		// 4. audit（红线 10：价格相关操作 100% 写审计）
		if err := r.audit.Record(ctx, AuditEntry{
			OperatorID: p.Identity.OperatorID, OperatorRole: p.Identity.OperatorRole,
			Action: "COST_BASELINE_RECALC", TargetType: "COST_BASELINE", TargetID: newID,
			SourceType: p.Identity.SourceType, RequestID: p.RequestID,
			AfterValue: map[string]any{
				"sku_id": p.SKUID, "version": newVersion,
				"change_reason":       p.Baseline.ChangeReason,
				"primary_supplier_id": p.Baseline.PrimarySupplierID,
				"event_time":          p.EventTime.UTC().Format(time.RFC3339),
			},
		}); err != nil {
			return fmt.Errorf("audit baseline=%d: %w", newID, err)
		}
		// 5. event_outbox（cost.baseline.changed，异步出站——红线 8）
		eventPayload, _ := json.Marshal(map[string]any{
			"baseline_id": newID, "sku_id": p.SKUID, "version": newVersion,
			"change_reason":       p.Baseline.ChangeReason,
			"primary_supplier_id": p.Baseline.PrimarySupplierID,
		})
		if err := tx.Create(&eventOutboxRow{
			EventType: "cost.baseline.changed", Payload: eventPayload, Status: "PENDING",
			NextRunAt: p.EventTime, CreatedAt: p.EventTime, UpdatedAt: p.EventTime,
			RequestID: strPtr(p.RequestID),
			CreatedBy: int64Ptr(p.Identity.OperatorID), UpdatedBy: int64Ptr(p.Identity.OperatorID),
		}).Error; err != nil {
			return fmt.Errorf("enqueue baseline changed event sku=%d: %w", p.SKUID, err)
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return newID, newVersion, nil
}

// ============================================================
// 6b-4 读侧：cost.BaselineReadStore 的 GORM 实现（契约 §2/§3，只读，不走 txOf 写路径）。
// 纪律：
//   - 固定 2 发查询（列表主体 + supplier 计数），禁止逐行 N+1（5a 踩坑）；
//   - 行模型与写模型 costBaselineRow 分离：查询专用结构含 JOIN 别名列，绝不入写路径；
//   - floor 的 margin 由 sys_config 读——与 ApproveRepo.GetSysConfigDecimal 同款模式。
// ============================================================

// supplierNameExpr 一次给出供应商展示名（若 supplier_profile/legal_subject 缺行，退化为空串别 NULL）。
const supplierNameExpr = `COALESCE((SELECT ls.legal_name FROM supplier_profile sp
	JOIN legal_subject ls ON ls.id = sp.subject_id WHERE sp.id = cb.primary_supplier_id), '')`

// representCompJoin 是代表组件的 LEFT JOIN 片段：优先 input，无则该基线组件字母序第一个
// （契约 §10-2）。DISTINCT ON 每个 baseline 取一行，ORDER 与 compareRepresentRank 一致。
//
// 【与 Go 侧 compare.go.RepresentativeComponent 是两份实现、语义必须恒等】
// 读侧必须在 SQL 里聚合，拿不回 []Component 进 Go，所以这里复制了同样的口径；
// 一致性由 internal/repo/cost_baseline_represent_test.go（!short，真库）钉住——
// 改这一处前先看那个测试，Go/SQL 两侧必须同步改，否则测试先炸。
//
// 【性能遗留（阶段 F）】当前是全表子查询 JOIN（扫一遍 cost_component 再回贴），
// 数据量上来后改 LEFT JOIN LATERAL（... WHERE cc.cost_baseline_id = cb.id
// ORDER BY CASE ... LIMIT 1），把聚合限制在每条 cb 命中的分组内。当前规模不是瓶颈。
const representCompJoin = `LEFT JOIN (
	SELECT DISTINCT ON (cost_baseline_id) cost_baseline_id, component_type, unit_cost
	FROM cost_component
	ORDER BY cost_baseline_id,
	         CASE WHEN component_type = 'input' THEN 0 ELSE 1 END,
	         component_type
) rc ON rc.cost_baseline_id = cb.id`

type costBaselineListRow struct {
	SkuID               int64     `gorm:"column:sku_id"`
	SKUCode             string    `gorm:"column:sku_code"`
	Version             int       `gorm:"column:version"`
	ValidFrom           time.Time `gorm:"column:valid_from"`
	Currency            string    `gorm:"column:currency"`
	PrimarySupplierID   int64     `gorm:"column:primary_supplier_id"`
	PrimarySupplierName string    `gorm:"column:primary_supplier_name"`
	UnitCostBasis       string    `gorm:"column:unit_cost_basis"`
	UnitCost            string    `gorm:"column:unit_cost"`
	SupplierCount       int       `gorm:"column:supplier_count"`
}

// ListBaselines 实现 cost.BaselineReadStore.ListBaselines（固定 2 发查询）。
func (r *CostBaselineRepo) ListBaselines(ctx context.Context, q cost.BaselineListQuery) ([]cost.BaselineListItem, int64, error) {
	tx := r.txOf(ctx)

	// 计数子查询：该 SKU 当前 EFFECTIVE 报价的去重供应商数（DB 侧聚合，不 N+1）。
	countJoin := `LEFT JOIN (
		SELECT qi.sku_id, COUNT(DISTINCT qs.supplier_id) AS cnt
		FROM quote_sheet qs JOIN quote_item qi ON qi.quote_sheet_id = qs.id
		WHERE qs.status = 'EFFECTIVE'
		GROUP BY qi.sku_id
	) sc ON sc.sku_id = cb.sku_id`

	base := func() *gorm.DB {
		d := tx.Table("cost_baseline cb").Where("cb.is_current")
		if q.OnlySinglePoint {
			d = d.Where("COALESCE(sc.cnt, 0) = 1")
		}
		return d
	}

	var total int64
	countQ := base().Joins(countJoin)
	if q.Keyword != "" {
		countQ = countQ.Joins("JOIN model_sku ms ON ms.id = cb.sku_id").
			Where("ms.sku_code ILIKE ?", "%"+q.Keyword+"%")
	}
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count baselines: %w", err)
	}
	if total == 0 {
		return []cost.BaselineListItem{}, 0, nil
	}

	var rows []costBaselineListRow
	listQ := base().
		Select(`cb.sku_id, ms.sku_code, cb.version, cb.valid_from, cb.currency,
			cb.primary_supplier_id, ` + supplierNameExpr + ` AS primary_supplier_name,
			rc.component_type AS unit_cost_basis, rc.unit_cost,
			COALESCE(sc.cnt, 0) AS supplier_count`).
		Joins("JOIN model_sku ms ON ms.id = cb.sku_id").
		Joins(countJoin).
		Joins(representCompJoin).
		Order("cb.sku_id").
		Limit(q.Size).Offset((q.Page - 1) * q.Size)
	if q.Keyword != "" {
		listQ = listQ.Where("ms.sku_code ILIKE ?", "%"+q.Keyword+"%")
	}
	if err := listQ.Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list baselines: %w", err)
	}

	items := make([]cost.BaselineListItem, 0, len(rows))
	for i := range rows {
		rw := &rows[i]
		unitCost, basis := normalizeRepresent(rw.UnitCost, rw.UnitCostBasis)
		items = append(items, cost.BaselineListItem{
			SKUID: rw.SkuID, SKUCode: rw.SKUCode, Version: rw.Version,
			ValidFrom: rw.ValidFrom.UTC().Format(time.RFC3339), Currency: rw.Currency,
			PrimarySupplierID: rw.PrimarySupplierID, PrimarySupplierName: rw.PrimarySupplierName,
			UnitCost: unitCost, UnitCostBasis: basis,
			SupplierCount: rw.SupplierCount, SinglePoint: rw.SupplierCount == 1,
		})
	}
	return items, total, nil
}

// normalizeRepresent 把代表组件列规整成响应值。组件缺失（数据空洞）→ 空串，不静默造数。
func normalizeRepresent(unitCost, basis string) (string, string) {
	if unitCost == "" || basis == "" {
		return "", ""
	}
	d, err := decimal.NewFromString(unitCost)
	if err != nil {
		return "", ""
	}
	return d.StringFixed(8), basis
}

// ResolveSKUID 实现 cost.BaselineReadStore.ResolveSKUID（纯数字 → id，否则 → sku_code）。
func (r *CostBaselineRepo) ResolveSKUID(ctx context.Context, sku string) (int64, bool, error) {
	tx := r.txOf(ctx)
	if id, err := strconv.ParseInt(sku, 10, 64); err == nil && id > 0 {
		var n int64
		if err := tx.Table("model_sku").Where("id = ?", id).Count(&n).Error; err != nil {
			return 0, false, fmt.Errorf("resolve sku id=%d: %w", id, err)
		}
		return id, n > 0, nil
	}
	var ids []int64
	if err := tx.Table("model_sku").Where("sku_code = ?", sku).Pluck("id", &ids).Error; err != nil {
		return 0, false, fmt.Errorf("resolve sku code=%s: %w", sku, err)
	}
	switch len(ids) {
	case 0:
		return 0, false, nil
	case 1:
		return ids[0], true, nil
	default:
		return 0, false, fmt.Errorf("sku_code %s 对应 %d 行（数据事故）", sku, len(ids))
	}
}

// costBaselineHistoryRow 是 §3 时间线的查询专用行（含 JOIN 别名）。
type costBaselineHistoryRow struct {
	Version             int        `gorm:"column:version"`
	ValidFrom           time.Time  `gorm:"column:valid_from"`
	ValidTo             *time.Time `gorm:"column:valid_to"`
	ChangeReason        string     `gorm:"column:change_reason"`
	PrimarySupplierID   int64      `gorm:"column:primary_supplier_id"`
	PrimarySupplierName string     `gorm:"column:primary_supplier_name"`
	UnitCostBasis       string     `gorm:"column:unit_cost_basis"`
	UnitCost            string     `gorm:"column:unit_cost"`
	CalcSnapshot        []byte     `gorm:"column:calc_snapshot"`
}

// ListBaselineHistory 实现 cost.BaselineReadStore.ListBaselineHistory。
// asOf=nil → 全量时间线 version DESC；asOf 非空 → 区间包含的那一刻的单版本（空 = 200 空 list）。
// 契约 §3："传了返回该时刻生效的那一个版本"，空是唯一合法响应。
func (r *CostBaselineRepo) ListBaselineHistory(ctx context.Context, skuID int64, asOf *time.Time) ([]cost.BaselineHistoryItem, error) {
	tx := r.txOf(ctx)
	q := tx.Table("cost_baseline cb").
		Select(`cb.version, cb.valid_from, cb.valid_to, cb.change_reason,
			cb.primary_supplier_id, `+supplierNameExpr+` AS primary_supplier_name,
			rc.component_type AS unit_cost_basis, rc.unit_cost, cb.calc_snapshot`).
		Joins(representCompJoin).
		Where("cb.sku_id = ?", skuID)
	if asOf != nil {
		// 半开区间 [valid_from, valid_to)：asOf 恰等于旧版本 valid_to 时属于新版本起刻。
		q = q.Where("cb.valid_from <= ? AND (cb.valid_to IS NULL OR cb.valid_to > ?)", *asOf, *asOf).
			Order("cb.version DESC").Limit(1)
	} else {
		q = q.Order("cb.version DESC")
	}
	var rows []costBaselineHistoryRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list baseline history sku=%d: %w", skuID, err)
	}
	items := make([]cost.BaselineHistoryItem, 0, len(rows))
	for i := range rows {
		rw := &rows[i]
		unitCost, basis := normalizeRepresent(rw.UnitCost, rw.UnitCostBasis)
		var validTo *string
		if rw.ValidTo != nil {
			s := rw.ValidTo.UTC().Format(time.RFC3339)
			validTo = &s
		}
		snap := rw.CalcSnapshot
		if len(snap) == 0 {
			snap = []byte("{}")
		}
		items = append(items, cost.BaselineHistoryItem{
			Version:   rw.Version,
			ValidFrom: rw.ValidFrom.UTC().Format(time.RFC3339), ValidTo: validTo,
			ChangeReason:      rw.ChangeReason,
			PrimarySupplierID: rw.PrimarySupplierID, PrimarySupplierName: rw.PrimarySupplierName,
			UnitCost: unitCost, UnitCostBasis: basis,
			CalcSnapshot: json.RawMessage(snap),
		})
	}
	return items, nil
}

// LoadMinGrossMargin 实现 cost.BaselineReadStore.LoadMinGrossMargin。
// 与 ApproveRepo.GetSysConfigDecimal 同款：config_value 存字符串，decimal 解析。
func (r *CostBaselineRepo) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	var val string
	if err := r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", "min_gross_margin").
		Select("config_value").Scan(&val).Error; err != nil {
		return decimal.Zero, fmt.Errorf("read sys_config min_gross_margin: %w", err)
	}
	if val == "" {
		return decimal.Zero, errors.New("sys_config min_gross_margin not found")
	}
	d, err := decimal.NewFromString(val)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sys_config min_gross_margin %q 非法: %w", val, err)
	}
	return d, nil
}

// ============================================================
// 6d-4 读侧（§4 比价 / §5 议价机会）追加实现。
// ============================================================

// baselineTrendRow 是 trend 按天聚合的查询专用行。
type baselineTrendRow struct {
	Date     string `gorm:"column:date"`
	UnitCost string `gorm:"column:unit_cost"`
	Version  int    `gorm:"column:version"`
}

// ListBaselineTrend 实现 cost.BaselineReadStore.ListBaselineTrend（§10-8）。
//
// 【陷阱 4：日历日边界必须是 db.timezone，不是 UTC】
// valid_from 是 timestamptz，按天聚合的分组键 = valid_from AT TIME ZONE 'Asia/Shanghai'
// 的 YYYY-MM-DD。窗口下界同理用 (NOW() AT TIME ZONE tz DATE − days) 再转回去比较，
// 保证「近 90 天」按上海日历日而不是 UTC 日切分（E2E 实测会撞 UTC 日界偏差一天）。
//
// 【每日最新版本】DISTINCT ON (date) + 子查询 + ORDER BY date, version DESC, id DESC：
// 同一天多版本取 version 最大的那条；version 并列（不可能，UNIQUE(sku_id, version)）
// 再以 id 兜底，确定性输出。**SELECT 里的 date 别名通过子查询暴露给 DISTINCT ON**——
// PostgreSQL DISTINCT ON 要求与 ORDER BY 表达式物理一致，且 ORDER BY 不能直接用
// SELECT 别名；子查询方案两者兼得（42P10 在真库 SQL 拼装上踩过一次）。
//
// 【代表组件口径 = representCompJoin 同一份实现】
// 与 ListBaselines/ListBaselineHistory 用的是同一个常量 representCompJoin，不是新复制；
// 钉住纪律照样由 cost_baseline_represent_test.go 承担（该测试钉 SQL 常量与 Go 函数恒等，
// 本方法只是再消费这个常量，不产生第四份实现）。
func (r *CostBaselineRepo) ListBaselineTrend(ctx context.Context, skuID int64, days int) ([]cost.TrendPoint, error) {
	tx := r.txOf(ctx)
	const tz = "Asia/Shanghai"
	// 窗口下界：当天（含）往前推 days 天，含端点。「近 90 天」= 今天 − 89 天零点起——
	// 用 NOW() AT TIME ZONE tz 的当天 DATE 减 days，保证天数口径与契约 days 参数对齐。
	// 子查询先暴露 date 别名，外层 DISTINCT ON + ORDER BY 用它（避开 42P10）。
	inner := `SELECT to_char(cb.valid_from AT TIME ZONE ?, 'YYYY-MM-DD') AS date,
			rc.unit_cost AS unit_cost,
			cb.version AS version,
			cb.id AS id
		FROM cost_baseline cb ` + representCompJoin + `
		WHERE cb.sku_id = ?
		  AND rc.unit_cost IS NOT NULL
		  AND cb.valid_from >= ((NOW() AT TIME ZONE ?)::date - ?::int)::timestamp AT TIME ZONE ?`
	q := `SELECT DISTINCT ON (date) date, unit_cost, version
		FROM (` + inner + `) sub
		ORDER BY date, version DESC, id DESC`
	var rows []baselineTrendRow
	if err := tx.Raw(q, tz, skuID, tz, days, tz).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list baseline trend sku=%d: %w", skuID, err)
	}
	out := make([]cost.TrendPoint, 0, len(rows))
	for i := range rows {
		d, err := decimal.NewFromString(rows[i].UnitCost)
		if err != nil {
			return nil, fmt.Errorf("trend unit_cost %q 非法: %w", rows[i].UnitCost, err)
		}
		out = append(out, cost.TrendPoint{
			Date:     rows[i].Date,
			UnitCost: d,
			Version:  rows[i].Version,
		})
	}
	return out, nil
}

// ListSKUIDs 实现 cost.BaselineReadStore.ListSKUIDs（bargain 的遍历域）。
// 有基线（含历史版本）的 SKU 全集——不用 is_current 过滤：bargain 判断的是当前 EFFECTIVE
// 报价，is_current=true 的基线代表"上一次重算还活着"，而 LoadRecalcInput 才是真正决定
// 可不可算的权威。用全部 sku_id 让 NO_QUOTE 分支自然过滤掉已断供的旧基线，不漏不误。
func (r *CostBaselineRepo) ListSKUIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	if err := r.txOf(ctx).Table("cost_baseline").
		Where("is_current").
		Distinct().Order("sku_id").
		Pluck("sku_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list sku ids from baseline: %w", err)
	}
	return ids, nil
}

// LoadSysConfigDecimal 实现 cost.BaselineReadStore.LoadSysConfigDecimal。
// 与 LoadMinGrossMargin 同款（只是 key 参数化），维持成本域对 sys_config 的单一读取入口。
func (r *CostBaselineRepo) LoadSysConfigDecimal(ctx context.Context, key string) (decimal.Decimal, error) {
	var val string
	if err := r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", key).
		Select("config_value").Scan(&val).Error; err != nil {
		return decimal.Zero, fmt.Errorf("read sys_config %s: %w", key, err)
	}
	if val == "" {
		return decimal.Zero, fmt.Errorf("sys_config %s not found", key)
	}
	d, err := decimal.NewFromString(val)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sys_config %s %q 非法: %w", key, val, err)
	}
	return d, nil
}

// ListPrimarySupplierNames 实现 cost.BaselineReadStore.ListPrimarySupplierNames。
// 单条查询把 (sku_id → primary_supplier_id) 与 (primary_supplier_id → legal_name)
// 一次性带回，返回 supplier_id → legal_name 映射（bargain 只需要名字维度去重）——
// 绝不逐 SKU N+1。
func (r *CostBaselineRepo) ListPrimarySupplierNames(ctx context.Context, skuIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		PrimarySupplierID   int64  `gorm:"column:primary_supplier_id"`
		PrimarySupplierName string `gorm:"column:primary_supplier_name"`
	}
	if err := r.txOf(ctx).Table("cost_baseline cb").
		Select(`cb.primary_supplier_id, `+supplierNameExpr+` AS primary_supplier_name`).
		Where("cb.sku_id IN ? AND cb.is_current", skuIDs).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list primary supplier names: %w", err)
	}
	for _, rw := range rows {
		out[rw.PrimarySupplierID] = rw.PrimarySupplierName
	}
	return out, nil
}

// ListPrimarySupplierIDs 实现 cost.BaselineReadStore.ListPrimarySupplierIDs。
// 与 ListPrimarySupplierNames 同源（同一条 cost_baseline 当前行扫描），
// 只是少挂 legal_subject JOIN——名字/id 双方法独立存在是为调用方各取所需不重复查。
func (r *CostBaselineRepo) ListPrimarySupplierIDs(ctx context.Context, skuIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SkuID             int64 `gorm:"column:sku_id"`
		PrimarySupplierID int64 `gorm:"column:primary_supplier_id"`
	}
	if err := r.txOf(ctx).Table("cost_baseline cb").
		Select("cb.sku_id, cb.primary_supplier_id").
		Where("cb.sku_id IN ? AND cb.is_current", skuIDs).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list primary supplier ids: %w", err)
	}
	for _, rw := range rows {
		out[rw.SkuID] = rw.PrimarySupplierID
	}
	return out, nil
}

// 编译期断言：CostBaselineRepo 同时是读写两个接口的实现。
var _ cost.BaselineReadStore = (*CostBaselineRepo)(nil)
