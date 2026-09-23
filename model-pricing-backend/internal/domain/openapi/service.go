// Package openapi 的 service.go：11a 开放接口领域服务（§1 鉴权 + §2 七个只读接口）。
//
// 设计文档 §8.0.2 红线：
//   - 开放接口独立鉴权，client_id/client_secret 存 sys_config，**不**复用三大门户 login_session；
//   - 明文 token 仅下发一次，库中只存 sha256 hex；
//   - 只读接口不挂幂等中间件（Idempotency-Key 仅用于写操作）；
//   - 字段剔除在 SQL 层：不 SELECT cost/margin/floor_price 等内部字段（红线 6）。
package openapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ---- 领域错误 ----

var (
	// ErrInvalidClient client_id 或 client_secret 不匹配。
	ErrInvalidClient = errors.New("client_id 或 client_secret 错误")
	// ErrTokenExpired token 已过期或不存在。
	ErrTokenExpired = errors.New("token 已过期或不存在")
	// ErrSKUNotFound SKU 不存在（按 id 或 sku_code 都解析不到）。
	ErrSKUNotFound = errors.New("SKU 不存在")
	// ErrNoBaseline 该 SKU 无成本基线（routing / cost-snapshot 前置）。
	ErrNoBaseline = errors.New("该 SKU 暂无成本基线")
	// ErrNoPriceBook 该等级无生效价目表。
	ErrNoPriceBook = errors.New("该等级暂无生效价目表")
)

// TokenTTL 开放接口 token 有效期（硬编码 1h，阶段 F 再评估是否可配）。
const TokenTTL = time.Hour

// ---- DTO ----

// TokenResult 是 POST /auth/token 的 data 段。
type TokenResult struct {
	Token     string `json:"token"`      // 明文，仅本次返回
	ExpiresAt string `json:"expires_at"` // RFC3339
}

// AliasItem 是 GET /aliases 的单行。
type AliasItem struct {
	Alias   string `json:"alias"`
	SKUID   int64  `json:"sku_id"`
	SKUCode string `json:"sku_code"`
}

// AliasesResult 是 GET /aliases 的 data 段。
type AliasesResult struct {
	Version int64       `json:"version"`
	Items   []AliasItem `json:"items"`
}

// SellableModelItem 是 GET /sellable-models 的单行。
type SellableModelItem struct {
	SKUID          int64    `json:"sku_id"`
	SKUCode        string   `json:"sku_code"`
	ModelType      string   `json:"model_type"`
	NativeCurrency string   `json:"native_currency"`
	TierTag        *string  `json:"tier_tag"`
	LevelTags      []string `json:"level_tags"` // tags 字段透传；NULL → []
}

// SellableModelsResult 是 GET /sellable-models 的 data 段。
type SellableModelsResult struct {
	Items []SellableModelItem `json:"items"`
}

// RoutingBackup 是 GET /routing/{sku} 的备选供应商行。
type RoutingBackup struct {
	SupplierID int64 `json:"supplier_id"`
	Weight     int   `json:"weight"` // 0
}

// RoutingResult 是 GET /routing/{sku} 的 data 段。
type RoutingResult struct {
	SKUID   int64 `json:"sku_id"`
	Primary struct {
		SupplierID int64 `json:"supplier_id"`
		Weight     int   `json:"weight"` // 100
	} `json:"primary"`
	Backups  []RoutingBackup `json:"backups"`
	Currency string          `json:"currency"`
}

// PriceBookItem 是 GET /price-book 的单行。
type PriceBookItem struct {
	SKUID     int64  `json:"sku_id"`
	SKUCode   string `json:"sku_code"`
	Currency  string `json:"currency"`
	UnitPrice string `json:"unit_price"` // StringFixed(8)
}

// PriceBookResult 是 GET /price-book 的 data 段。
type PriceBookResult struct {
	LevelCode string          `json:"level_code"`
	VersionNo int             `json:"version_no"`
	Currency  string          `json:"currency"`
	Items     []PriceBookItem `json:"items"`
}

// CostSnapshotResult 是 GET /cost-snapshot 的 data 段。
type CostSnapshotResult struct {
	SKUID           int64  `json:"sku_id"`
	UnitCost        string `json:"unit_cost"` // StringFixed(8)
	Currency        string `json:"currency"`
	AsOf            string `json:"as_of"` // RFC3339
	BaselineVersion int    `json:"baseline_version"`
}

// EventItem 是 GET /events 的单行。
type EventItem struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"` // jsonb 全量透传（字段剔除例外，见 stage11a 裁决 6）
	CreatedAt string          `json:"created_at"`
}

// EventsResult 是 GET /events 的 data 段。
type EventsResult struct {
	LastID int64       `json:"last_id"`
	Events []EventItem `json:"events"`
}

// ---- Store 接口 ----

// Store 是开放接口仓储（只读 + token 读写）。
type Store interface {
	// LoadClientSecret 按 client_id 从 sys_config 读 client_secret。
	// 不存在 → ( "", false, nil )；config_value 为空串 → ( "", true, nil )（视为不匹配）。
	LoadClientSecret(ctx context.Context, clientID string) (string, bool, error)
	// SaveToken 写一条 open_api_token。
	SaveToken(ctx context.Context, clientID, tokenHash string, expiresAt time.Time, requestID string) error
	// LoadToken 按 token_hash 查未过期 token。不存在或已过期 → ( "", false, nil )。
	LoadToken(ctx context.Context, tokenHash string) (string, bool, error)

	// LoadCacheVersion 读 cache_version.version；key 不存在 → ( 0, false, nil )。
	LoadCacheVersion(ctx context.Context, cacheKey string) (int64, bool, error)
	// LoadAliases 全量别名（联 model_sku 拿 sku_code，按 alias 升序）。
	LoadAliases(ctx context.Context) ([]AliasItem, error)

	// LoadSellableModels 上架可售 SKU（lifecycle_status IN ('PUBLISHED','PURCHASABLE')）。
	LoadSellableModels(ctx context.Context) ([]SellableModelItem, error)

	// LoadCurrentBaseline 读当前成本基线（is_current）。
	// 不存在 → ( nil, false, nil )；Version 供 cost-snapshot 用。
	LoadCurrentBaseline(ctx context.Context, skuID int64) (*BaselineInfo, error)
	// LoadBaselineAt 读 asOf 时刻生效的成本基线（valid_from <= asOf AND (valid_to IS NULL OR valid_to > asOf)）。
	// 不存在 → ( nil, false, nil )。
	LoadBaselineAt(ctx context.Context, skuID int64, asOf time.Time) (*BaselineInfo, error)

	// LoadEffectivePriceBook 读当前生效价目表（联 model_sku + 代表组件 unit_price）。
	// 不存在 → ( nil, false, nil )。
	LoadEffectivePriceBook(ctx context.Context, levelCode string) (*PriceBookResult, error)

	// PullEvents 增量拉取 event_outbox（id > since，ORDER BY id LIMIT 100）。
	PullEvents(ctx context.Context, sinceID int64, limit int) ([]EventItem, error)

	// ResolveSKUID 把 {sku} 解析为 sku_id（纯数字按 id，否则按 sku_code）。
	// 不存在 → ( 0, false, nil )；sku_code 撞多行 → error。
	ResolveSKUID(ctx context.Context, sku string) (int64, bool, error)

	// LoadCalcInput 装配四因子评分输入（报价 + 参数 + 供应商状态）。
	LoadCalcInput(ctx context.Context, skuID int64) (*CalcInput, error)
}

// BaselineInfo 是成本基线的开放接口视图（不含 calc_snapshot / supplier_cost）。
type BaselineInfo struct {
	Version           int
	Currency          string
	PrimarySupplierID int64
	UnitCost          string // 代表组件完全成本，StringFixed(8)
	UnitCostBasis     string
}

// CalcInput 是四因子评分的输入（与 cost.RecalcInput 同构，由 repo 装配）。
type CalcInput struct {
	SKUID    int64
	SKUCode  string
	Currency string
	Quotes   []QuoteInfo
	Params   []ParamInfo
	Statuses []StatusInfo
}

// QuoteInfo 是一家供应商的报价输入。
type QuoteInfo struct {
	SupplierID   int64
	QuoteSheetID int64
	QuoteVersion int
	ValidFrom    time.Time
	Currency     string
	Constraints  map[string]any
	Components   []ComponentInfo
}

// ComponentInfo 是一个报价组件。
type ComponentInfo struct {
	ComponentType string
	UnitPrice     decimal.Decimal
}

// ParamInfo 是成本参数。
type ParamInfo struct {
	ScopeType   string
	ScopeID     int64
	LossRate    decimal.Decimal
	ChannelRate decimal.Decimal
}

// StatusInfo 是供应商冻结状态。
type StatusInfo struct {
	SupplierID   int64
	QualStatus   string
	SettleStatus string
	Status       string
}

// SupplierScore 是单家供应商的排序结果（供 backups 排序）。
type SupplierScore struct {
	SupplierID    int64
	TotalScore    decimal.Decimal
	RepresentCost decimal.Decimal
}

// CalcFunc 是四因子评分纯函数（由 main.go 注入 cost.Service.CalcSKU 的适配器）。
// 返回按 Total desc → RepresentCost asc → SupplierID asc 排序后的供应商列表。
type CalcFunc func(input *CalcInput, now time.Time) ([]SupplierScore, error)

// ---- Service ----

// Service 是开放接口领域服务。
type Service struct {
	store Store
	calc  CalcFunc
	now   func() time.Time
}

// NewService 构造。
func NewService(store Store, calc CalcFunc) *Service {
	return &Service{store: store, calc: calc, now: func() time.Time { return time.Now().UTC() }}
}

// IssueToken 校验 client_id/secret，成功则写 open_api_token 并返回明文。
func (s *Service) IssueToken(ctx context.Context, clientID, clientSecret, requestID string) (*TokenResult, error) {
	secret, ok, err := s.store.LoadClientSecret(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if !ok || secret == "" || secret != clientSecret {
		return nil, ErrInvalidClient
	}
	// 明文 token：32 字节随机 hex（64 字符），强度足够，避免 UUID 被猜。
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	plain := hex.EncodeToString(buf)
	expiresAt := s.now().Add(TokenTTL)
	if err := s.store.SaveToken(ctx, clientID, tokenHashOf(plain), expiresAt, requestID); err != nil {
		return nil, err
	}
	return &TokenResult{Token: plain, ExpiresAt: expiresAt.UTC().Format(time.RFC3339)}, nil
}

// ValidateToken 按 token_hash 查未过期 token，返回 client_id。
func (s *Service) ValidateToken(ctx context.Context, token string) (string, error) {
	clientID, ok, err := s.store.LoadToken(ctx, tokenHashOf(token))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrTokenExpired
	}
	return clientID, nil
}

// GetAliases 别名全量列表（since >= version → 空 items，否则全量）。
func (s *Service) GetAliases(ctx context.Context, since int64) (*AliasesResult, error) {
	version, ok, err := s.store.LoadCacheVersion(ctx, "model_alias")
	if err != nil {
		return nil, err
	}
	if !ok {
		// cache_version 无 model_alias 行：视为 version=1（首次初始化前）。
		version = 1
	}
	if since >= version {
		return &AliasesResult{Version: version, Items: []AliasItem{}}, nil
	}
	items, err := s.store.LoadAliases(ctx)
	if err != nil {
		return nil, err
	}
	return &AliasesResult{Version: version, Items: items}, nil
}

// GetSellableModels 上架可售模型（生命周期 PUBLISHED / PURCHASABLE）。
func (s *Service) GetSellableModels(ctx context.Context) (*SellableModelsResult, error) {
	items, err := s.store.LoadSellableModels(ctx)
	if err != nil {
		return nil, err
	}
	// LevelTags：tags NULL → []（不返回 null，契约一致）
	for i := range items {
		if items[i].LevelTags == nil {
			items[i].LevelTags = []string{}
		}
	}
	return &SellableModelsResult{Items: items}, nil
}

// GetRouting 主备路由（primary=当前成本基线主供应商，backups=按四因子排序的其余供应商）。
// 无成本基线 → 404；无 EFFECTIVE 报价 → 404（与 ErrNoBaseline 语义一致）。
func (s *Service) GetRouting(ctx context.Context, sku string) (*RoutingResult, error) {
	skuID, ok, err := s.store.ResolveSKUID(ctx, sku)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSKUNotFound
	}
	baseline, err := s.store.LoadCurrentBaseline(ctx, skuID)
	if err != nil {
		return nil, err
	}
	if baseline == nil {
		return nil, ErrNoBaseline
	}
	// 装配四因子输入，复用 cost 域纯函数排序。
	input, err := s.store.LoadCalcInput(ctx, skuID)
	if err != nil {
		return nil, err
	}
	scores, err := s.calc(input, s.now())
	if err != nil {
		return nil, err
	}
	// 剔除主供应商，其余按四因子排序作为 backups（weight=0）。
	backups := make([]RoutingBackup, 0, len(scores)-1)
	for _, sc := range scores {
		if sc.SupplierID == baseline.PrimarySupplierID {
			continue
		}
		backups = append(backups, RoutingBackup{SupplierID: sc.SupplierID, Weight: 0})
	}
	res := &RoutingResult{SKUID: skuID, Currency: baseline.Currency, Backups: backups}
	res.Primary.SupplierID = baseline.PrimarySupplierID
	res.Primary.Weight = 100
	return res, nil
}

// GetPriceBook 当前生效价目表（按等级）。
func (s *Service) GetPriceBook(ctx context.Context, levelCode string) (*PriceBookResult, error) {
	if levelCode == "" {
		return nil, ErrNoPriceBook
	}
	res, err := s.store.LoadEffectivePriceBook(ctx, levelCode)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, ErrNoPriceBook
	}
	return res, nil
}

// GetCostSnapshot 成本快照（聚合 unit_cost，无组件明细）。
// asOf=nil → 当前版本；asOf 非空 → 该时刻生效版本。
func (s *Service) GetCostSnapshot(ctx context.Context, sku string, asOf *time.Time) (*CostSnapshotResult, error) {
	skuID, ok, err := s.store.ResolveSKUID(ctx, sku)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSKUNotFound
	}
	var baseline *BaselineInfo
	if asOf == nil {
		baseline, err = s.store.LoadCurrentBaseline(ctx, skuID)
	} else {
		baseline, err = s.store.LoadBaselineAt(ctx, skuID, *asOf)
	}
	if err != nil {
		return nil, err
	}
	if baseline == nil {
		return nil, ErrNoBaseline
	}
	asOfStr := s.now().UTC().Format(time.RFC3339)
	if asOf != nil {
		asOfStr = asOf.UTC().Format(time.RFC3339)
	}
	return &CostSnapshotResult{
		SKUID:           skuID,
		UnitCost:        baseline.UnitCost,
		Currency:        baseline.Currency,
		AsOf:            asOfStr,
		BaselineVersion: baseline.Version,
	}, nil
}

// PullEvents 长轮询事件（30s 超时，每秒 tick 一次）。
// 超时返回空列表（非错误）；since 过滤 id > since；limit 默认 100 上限 100。
func (s *Service) PullEvents(ctx context.Context, sinceID int64, limit int) (*EventsResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	// 先查一次：有数据立即返回，不进入轮询。
	events, err := s.store.PullEvents(ctx, sinceID, limit)
	if err != nil {
		return nil, err
	}
	if len(events) > 0 {
		return &EventsResult{LastID: events[len(events)-1].ID, Events: events}, nil
	}
	// 长轮询：30s 超时，每秒 tick。
	deadline := s.now().Add(30 * time.Second)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if s.now().After(deadline) {
				return &EventsResult{LastID: sinceID, Events: []EventItem{}}, nil
			}
			events, err := s.store.PullEvents(ctx, sinceID, limit)
			if err != nil {
				return nil, err
			}
			if len(events) > 0 {
				return &EventsResult{LastID: events[len(events)-1].ID, Events: events}, nil
			}
		}
	}
}

// tokenHashOf 计算 token 的 sha256 hex。
func tokenHashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
