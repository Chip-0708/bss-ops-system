package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/pricing"
	"model_bss/internal/infra/db"
)

// ============================================================
// 8b-1 价目表发布 + 回滚（08-pricing.md §3/§4）
//
// 事务纪律（红线 3/8）：
//   - Publish 单事务：change_request + 2 步 approval_step + 草稿 DRAFT → APPROVING。
//   - ApplyPriceBookPublish 单事务 5 连写：旧版先 RETIRED，草稿再 APPROVING → EFFECTIVE +
//     旧版 EFFECTIVE → RETIRED + valid_to + event_outbox('price.effective') +
//     cache_version('price_book')+1 + audit_log('PRICE_BOOK_PUBLISH')。
//     由 DecideApproval 全步 APPROVED 时回调触发（同事务，与审批状态推进一致）。
//   - 回滚 = 复制历史版本的 items+components 成新版本（rollback_of=target_version_no），
//     走与发布完全相同的流程（change_request + 2 步审批 + 生效连锁）。
// ============================================================

// PricingPublishRepo 实现 pricing.PublishStore。
type PricingPublishRepo struct {
	base  *gorm.DB
	audit *AuditRepo
}

// NewPricingPublishRepo 构造仓储。
func NewPricingPublishRepo(base *gorm.DB) *PricingPublishRepo {
	return &PricingPublishRepo{base: base, audit: NewAuditRepo(base)}
}

var _ pricing.PublishStore = (*PricingPublishRepo)(nil)

func (r *PricingPublishRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// ---- 读侧 ----

// LoadPriceBookByID 按 id 读 price_book。
func (r *PricingPublishRepo) LoadPriceBookByID(ctx context.Context, id int64) (*pricing.PriceBookInfo, error) {
	var row priceBookRow
	if err := r.txOf(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pricing.ErrPriceBookNotFound
		}
		return nil, fmt.Errorf("load price_book %d: %w", id, err)
	}
	return priceBookRowToInfo(row), nil
}

// LoadPriceBookByVersion 按 level_code + version_no 读 price_book。
func (r *PricingPublishRepo) LoadPriceBookByVersion(ctx context.Context, levelCode string, versionNo int) (*pricing.PriceBookInfo, error) {
	var row priceBookRow
	if err := r.txOf(ctx).Where("level_code = ? AND version_no = ?", levelCode, versionNo).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pricing.ErrRollbackTargetNotFound
		}
		return nil, fmt.Errorf("load price_book level=%s ver=%d: %w", levelCode, versionNo, err)
	}
	return priceBookRowToInfo(row), nil
}

// LoadCurrentEffective 读该 level_code 当前生效版（uk_pb_effective 保证最多一个）。
func (r *PricingPublishRepo) LoadCurrentEffective(ctx context.Context, levelCode string) (*pricing.PriceBookInfo, error) {
	var row priceBookRow
	if err := r.txOf(ctx).Where("level_code = ? AND status = ?", levelCode, pricing.BookStatusEffective).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pricing.ErrPriceBookNotFound
		}
		return nil, fmt.Errorf("load current effective level=%s: %w", levelCode, err)
	}
	return priceBookRowToInfo(row), nil
}

// LoadPriceBookItems 读 price_book 的全部 items。
func (r *PricingPublishRepo) LoadPriceBookItems(ctx context.Context, priceBookID int64) ([]pricing.PriceBookItemInfo, error) {
	var rows []priceBookItemRow
	if err := r.txOf(ctx).Where("price_book_id = ?", priceBookID).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load price_book_items book=%d: %w", priceBookID, err)
	}
	out := make([]pricing.PriceBookItemInfo, 0, len(rows))
	for _, rr := range rows {
		out = append(out, pricing.PriceBookItemInfo{
			ID: rr.ID, PriceBookID: rr.PriceBookID, SkuID: rr.SkuID, Currency: rr.Currency,
			FloorPrice: rr.FloorPrice, PolicyID: rr.PolicyID, BaselineVersion: rr.BaselineVersion,
		})
	}
	return out, nil
}

// LoadPriceBookComponents 读 price_book_item 的全部 components。
func (r *PricingPublishRepo) LoadPriceBookComponents(ctx context.Context, priceBookItemIDs []int64) ([]pricing.PriceBookComponentInfo, error) {
	if len(priceBookItemIDs) == 0 {
		return nil, nil
	}
	var rows []priceBookComponentRow
	if err := r.txOf(ctx).Where("price_book_item_id IN ?", priceBookItemIDs).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load price_book_components: %w", err)
	}
	out := make([]pricing.PriceBookComponentInfo, 0, len(rows))
	for _, rr := range rows {
		out = append(out, pricing.PriceBookComponentInfo{
			ID: rr.ID, PriceBookItemID: rr.PriceBookItemID,
			ComponentType: rr.ComponentType, UnitPrice: rr.UnitPrice,
		})
	}
	return out, nil
}

// LoadCurrentUnitCosts 复用 8a 的实现（代表组件 unit_cost + baseline version）。
func (r *PricingPublishRepo) LoadCurrentUnitCosts(ctx context.Context, skuIDs []int64) (map[int64]pricing.UnitCostInfo, error) {
	return NewPricingRepo(r.base).LoadCurrentUnitCosts(ctx, skuIDs)
}

// LoadMinGrossMargin 复用 8a 的实现。
func (r *PricingPublishRepo) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	return NewPricingRepo(r.base).LoadMinGrossMargin(ctx)
}

// ---- 写侧 ----

// Publish 单事务：change_request + 2 步 approval_step + 草稿 DRAFT → APPROVING。
// 回滚时额外：复制历史版本的 items+components 成新版本（rollback_of=target_version_no）。
func (r *PricingPublishRepo) Publish(ctx context.Context, in pricing.PublishTxInput, operatorID int64, requestID string) (*pricing.PublishResult, error) {
	now := time.Now().UTC()
	var result *pricing.PublishResult
	err := r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 加载草稿（校验状态 + 拿 level_code/version_no）。
		var book priceBookRow
		if err := tx.Where("id = ?", in.PriceBookID).Take(&book).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return pricing.ErrPriceBookNotFound
			}
			return fmt.Errorf("load price_book %d: %w", in.PriceBookID, err)
		}

		// 2. 回滚：复制历史版本的 items+components 成新版本。
		if in.ChangeType == pricing.ChangePriceBookRollback && in.RollbackOf != nil {
			newBookID, err := r.copyPriceBookVersion(ctx, tx, book, *in.RollbackOf, operatorID, requestID, now)
			if err != nil {
				return fmt.Errorf("copy price_book version: %w", err)
			}
			// 回滚后，原草稿（当前 EFFECTIVE）不变，新版本是 DRAFT。
			// 但发布流程要求草稿 → APPROVING，所以这里把新版本也置为 APPROVING。
			if err := tx.Model(&priceBookRow{}).Where("id = ?", newBookID).
				Update("status", pricing.BookStatusApproving).Error; err != nil {
				return fmt.Errorf("update new version to APPROVING: %w", err)
			}
			// 回滚的 price_book_id 用新版本 id（不是原草稿 id）。
			in.PriceBookID = newBookID
			book.ID = newBookID
			// 重新加载新版本（拿 version_no）。
			if err := tx.Where("id = ?", newBookID).Take(&book).Error; err != nil {
				return fmt.Errorf("reload new version %d: %w", newBookID, err)
			}
		} else {
			// 3. 发布：草稿 DRAFT → APPROVING（原地升格，version_no 不变）。
			if book.Status != pricing.BookStatusDraft {
				return pricing.ErrPriceBookNotDraft
			}
			if err := tx.Model(&priceBookRow{}).Where("id = ?", book.ID).
				Updates(map[string]any{
					"status":     pricing.BookStatusApproving,
					"updated_at": now,
					"updated_by": operatorID,
					"request_id": requestID,
				}).Error; err != nil {
				return fmt.Errorf("update price_book to APPROVING: %w", err)
			}
		}

		// 4. change_request（sku_id=NULL，change_type=PRICE_BOOK_PUBLISH/ROLLBACK）。
		// GORM 零值 int64 会写 0 而不是 NULL——用 map 绕开。
		crMap := map[string]any{
			"change_type": in.ChangeType,
			"sku_id":      nil, // 8b-1 裁决 A1：非 SKU 粒度变更
			"risk_level":  "MID",
			"payload":     in.Payload,
			"status":      "PENDING",
			"created_by":  fmt.Sprintf("staff:%d", operatorID),
			"created_at":  now,
			"updated_at":  now,
			"request_id":  requestID,
		}
		if err := tx.Model(&changeRequestRow{}).Create(crMap).Error; err != nil {
			return fmt.Errorf("insert change_request: %w", err)
		}
		// 拿自增 id（GORM map Create 不回填 id，用 RETURNING 或查最后一条）。
		var crID int64
		if err := tx.Raw(`SELECT id FROM change_request WHERE request_id = ? ORDER BY id DESC LIMIT 1`, requestID).
			Row().Scan(&crID); err != nil {
			return fmt.Errorf("load change_request id: %w", err)
		}

		// 5. 2 步 approval_step（PRICING_OP → FINANCE，biz_type=change_type）。
		for i, role := range []string{"PRICING_OP", "FINANCE"} {
			step := approvalStepRow{
				BizType:      in.ChangeType,
				BizID:        crID,
				StepNo:       i + 1,
				RequiredRole: role,
				CreatedAt:    now,
				UpdatedAt:    now,
				RequestID:    strPtr(requestID),
				CreatedBy:    int64Ptr(operatorID),
			}
			if err := tx.Create(&step).Error; err != nil {
				return fmt.Errorf("insert approval_step %d: %w", i+1, err)
			}
		}

		// 6. 构造返回。
		result = &pricing.PublishResult{
			PriceBookID:     book.ID,
			VersionNo:       book.VersionNo,
			ChangeRequestID: crID,
			StepCount:       2,
			EffectiveTime:   in.EffectiveTime,
			Status:          pricing.BookStatusApproving,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// copyPriceBookVersion 复制历史版本的 items+components 成新版本（回滚用）。
// 返回新版本 price_book.id。
func (r *PricingPublishRepo) copyPriceBookVersion(ctx context.Context, tx *gorm.DB, current priceBookRow, targetVersionID int64, operatorID int64, requestID string, now time.Time) (int64, error) {
	// 1. 加载目标版本。
	var target priceBookRow
	if err := tx.Where("id = ?", targetVersionID).Take(&target).Error; err != nil {
		return 0, fmt.Errorf("load target version %d: %w", targetVersionID, err)
	}

	// 2. 下一个可用版本号。
	var maxVer *int
	if err := tx.Raw(`SELECT MAX(version_no) FROM price_book WHERE level_code = ?`, current.LevelCode).
		Row().Scan(&maxVer); err != nil {
		return 0, fmt.Errorf("load max version_no: %w", err)
	}
	nextVer := 1
	if maxVer != nil {
		nextVer = *maxVer + 1
	}

	// 3. 复制 price_book（rollback_of=target.version_no）。
	targetVerNo := int64(target.VersionNo)
	newBook := priceBookRow{
		VersionNo:  nextVer,
		LevelCode:  current.LevelCode,
		Status:     pricing.BookStatusDraft, // 先 DRAFT，后面会置 APPROVING
		RollbackOf: &targetVerNo,
		CreatedBy:  operatorID,
		CreatedAt:  now,
		UpdatedAt:  now,
		RequestID:  strPtr(requestID),
		UpdatedBy:  int64Ptr(operatorID),
	}
	if err := tx.Create(&newBook).Error; err != nil {
		return 0, fmt.Errorf("insert new price_book: %w", err)
	}

	// 4. 复制 items + components。
	var targetItems []priceBookItemRow
	if err := tx.Where("price_book_id = ?", target.ID).Find(&targetItems).Error; err != nil {
		return 0, fmt.Errorf("load target items: %w", err)
	}
	for _, item := range targetItems {
		newItem := priceBookItemRow{
			PriceBookID:     newBook.ID,
			SkuID:           item.SkuID,
			Currency:        item.Currency,
			FloorPrice:      item.FloorPrice,
			PolicyID:        item.PolicyID,
			BaselineVersion: item.BaselineVersion,
			CreatedAt:       now,
			UpdatedAt:       now,
			RequestID:       strPtr(requestID),
			CreatedBy:       int64Ptr(operatorID),
			UpdatedBy:       int64Ptr(operatorID),
		}
		if err := tx.Create(&newItem).Error; err != nil {
			return 0, fmt.Errorf("insert new item sku=%d: %w", item.SkuID, err)
		}
		// 复制该 item 的 components。
		var comps []priceBookComponentRow
		if err := tx.Where("price_book_item_id = ?", item.ID).Find(&comps).Error; err != nil {
			return 0, fmt.Errorf("load components for item %d: %w", item.ID, err)
		}
		for _, comp := range comps {
			newComp := priceBookComponentRow{
				PriceBookItemID: newItem.ID,
				ComponentType:   comp.ComponentType,
				UnitPrice:       comp.UnitPrice,
				CreatedAt:       now,
				UpdatedAt:       now,
				RequestID:       strPtr(requestID),
				CreatedBy:       int64Ptr(operatorID),
				UpdatedBy:       int64Ptr(operatorID),
			}
			if err := tx.Create(&newComp).Error; err != nil {
				return 0, fmt.Errorf("insert new component: %w", err)
			}
		}
	}
	return newBook.ID, nil
}

// ApplyPriceBookPublish 审批全通过后执行生效连锁（5 连写单事务）。
// 由 model_deprecate.go DecideApproval 的 onApproved 回调触发（同事务）。
func (r *PricingPublishRepo) ApplyPriceBookPublish(ctx context.Context, p pricing.ApplyPublishParams) error {
	var payload struct {
		PriceBookID     int64  `json:"price_book_id"`
		EffectiveTime   string `json:"effective_time"`
		Mode            string `json:"mode"`
		ChangeType      string `json:"change_type"`       // 回滚时 payload 里没有，从 change_request 读
		TargetVersionNo int    `json:"target_version_no"` // 回滚专用
	}
	if err := json.Unmarshal(p.Payload, &payload); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	effectiveTime, err := time.Parse(time.RFC3339, payload.EffectiveTime)
	if err != nil {
		return fmt.Errorf("decode effective_time %q: %w", payload.EffectiveTime, err)
	}
	now := time.Now().UTC()

	return r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 校验待生效草稿。旧版必须先退役，否则会撞 uk_pb_effective。
		var book priceBookRow
		if err := tx.Where("id = ?", payload.PriceBookID).Take(&book).Error; err != nil {
			return fmt.Errorf("load price_book %d: %w", payload.PriceBookID, err)
		}
		if book.Status != pricing.BookStatusApproving {
			return fmt.Errorf("price_book %d status=%s, expect APPROVING", payload.PriceBookID, book.Status)
		}
		// 2. 旧版本（当前 EFFECTIVE 的）→ RETIRED + valid_to = effective_time。
		// 注意：回滚时，当前 EFFECTIVE 是被回滚的版本；发布时，当前 EFFECTIVE 是上一版。
		var oldEffective priceBookRow
		err := tx.Where("level_code = ? AND status = ? AND id != ?", book.LevelCode, pricing.BookStatusEffective, book.ID).
			Take(&oldEffective).Error
		if err == nil {
			// 有旧版，关闭它。
			if err := tx.Model(&priceBookRow{}).Where("id = ?", oldEffective.ID).
				Updates(map[string]any{
					"status":     pricing.BookStatusRetired,
					"valid_to":   effectiveTime,
					"updated_at": now,
					"updated_by": p.OperatorID,
					"request_id": p.RequestID,
				}).Error; err != nil {
				return fmt.Errorf("retire old effective %d: %w", oldEffective.ID, err)
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("load old effective: %w", err)
		}
		if err := tx.Model(&priceBookRow{}).Where("id = ? AND status = ?", book.ID, pricing.BookStatusApproving).
			Updates(map[string]any{
				"status": pricing.BookStatusEffective, "effective_time": effectiveTime,
				"updated_at": now, "updated_by": p.OperatorID, "request_id": p.RequestID,
			}).Error; err != nil {
			return fmt.Errorf("update price_book to EFFECTIVE: %w", err)
		}

		// 3. event_outbox('price.effective')。
		eventPayload, _ := json.Marshal(map[string]any{
			"price_book_id":     book.ID,
			"level_code":        book.LevelCode,
			"version_no":        book.VersionNo,
			"effective_time":    payload.EffectiveTime,
			"change_request_id": p.ChangeRequestID,
		})
		if err := tx.Create(&eventOutboxRow{
			EventType: "price.effective", Payload: eventPayload, Status: "PENDING", NextRunAt: now,
			CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
		}).Error; err != nil {
			return fmt.Errorf("enqueue price.effective: %w", err)
		}

		// 4. cache_version('price_book') +1（upsert）。
		if err := tx.Exec(`
			INSERT INTO cache_version (cache_key, version, updated_at)
			VALUES ('price_book', 1, ?)
			ON CONFLICT (cache_key) DO UPDATE SET version = cache_version.version + 1, updated_at = EXCLUDED.updated_at`,
			now).Error; err != nil {
			return fmt.Errorf("bump cache_version: %w", err)
		}

		// 5. audit_log（红线 10）。
		action := "PRICE_BOOK_PUBLISH"
		if payload.TargetVersionNo > 0 {
			action = "PRICE_BOOK_ROLLBACK"
		}
		if err := r.audit.Record(ctx, AuditEntry{
			OperatorID: p.OperatorID, OperatorRole: p.OperatorRole,
			Action: action, TargetType: "PRICE_BOOK", TargetID: book.ID,
			SourceType: "INTERNAL", RequestID: p.RequestID,
			AfterValue: map[string]any{
				"price_book_id": book.ID, "level_code": book.LevelCode,
				"version_no": book.VersionNo, "effective_time": payload.EffectiveTime,
				"change_request_id": p.ChangeRequestID,
			},
		}); err != nil {
			return fmt.Errorf("audit price_book=%d: %w", book.ID, err)
		}
		return nil
	})
}

// ---- 转换 ----

func priceBookRowToInfo(r priceBookRow) *pricing.PriceBookInfo {
	return &pricing.PriceBookInfo{
		ID: r.ID, VersionNo: r.VersionNo, LevelCode: r.LevelCode, Status: r.Status,
		EffectiveTime: r.EffectiveTime, ValidTo: r.ValidTo, RollbackOf: r.RollbackOf,
		DiffReport: r.DiffReport, CreatedBy: r.CreatedBy,
	}
}
