// Package cost 的 read.go：成本基线只读视图（06-cost.md §2 列表 / §3 历史）。
//
// 为什么独立成 ReadService（不与重算 Service 合并）：
//   - 读路径不需要 TaskStore（任务队列）与 *zap.Logger（重算日志），依赖最小化；
//   - 6c 还要往读侧加 §4 比价 / §5 议价机会，独立出来更好长；
//   - 不改动 65 条存量重算测试（NewService 签名保持原样）。
//
// 【裁决 1 硬要求：与重算 Service 共用同一份纯函数，禁止各写一份】
//   - Floor(unitCost, margin)             → baseline.go（含除零保护；读侧唯一 floor 来源，
//     读侧真的会调它——ListBaselines 逐行用它算 floor_price）；
//   - 金额格式化 StringFixed(8)           → shopspring/decimal。
//
// 将来改 floor 口径时，**只许改 baseline.go 那一处**，两个 service 都去调它——
// 否则 unit_cost 与 floor_price 会悄悄对不上（最怕查的漂移）。
//
// 【但注意：代表组件口径这里不共用，是两份实现、由测试钉住】
// 读侧的代表组件（unit_cost_basis）必须在 SQL 里做聚合（每基线一行 JOIN 进列表），
// 拿不回 []Component 进 Go，所以读侧由 internal/repo/cost_baseline.go 的 representCompJoin
// 承担，Go 纯函数 compare.go 的 RepresentativeComponent 从未被读侧调用。
// 两者语义必须恒等：input 优先，无 input 取 component_type 字母序第一个。
// 一致性由 internal/repo/cost_baseline_represent_test.go（!short，真库）钉住——
// 改任何一侧前先看那个测试，两侧必须同步改，否则测试先炸。
//
// 查询纪律（6b-4 实现裁决）：
//   - 不做归属过滤（设计 §3.2 决议：成本与比价类放开 ALL，红线 7）；
//   - 列表固定 2 发查询：paged 行（JOIN model_sku + 代表组件）+ 按 sku 数 EFFECTIVE 供应商数
//     （GROUP BY 一次），**避免 N+1**；
//   - history 的 asOf 为空 = 全量时间线 version DESC；非空 = 区间包含的那一条版本
//     （查不到返回空 list——时间线查询，空即事实，不是 404）。
package cost

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

// ---- 查询与结果 ----

// BaselineListQuery 是 §2 列表的查询条件。
type BaselineListQuery struct {
	Keyword string // 模糊匹配 sku_code（大小写不敏感）
	// OnlySinglePoint 只返回 single_point=true 的行（只有 1 家有效报价供应商）。
	OnlySinglePoint bool
	Page            int
	Size            int
}

// BaselineListItem 是 §2 列表的一行（json tag 与契约字段名一一对应，供 fieldmask 精确命中）。
type BaselineListItem struct {
	SKUID               int64  `json:"sku_id"`
	SKUCode             string `json:"sku_code"`
	Version             int    `json:"version"`
	ValidFrom           string `json:"valid_from"` // RFC3339
	Currency            string `json:"currency"`
	PrimarySupplierID   int64  `json:"primary_supplier_id"`
	PrimarySupplierName string `json:"primary_supplier_name"`
	// UnitCost 代表组件完全成本（契约 §10-2：优先 input，否则字母序第一个）。
	UnitCost      string `json:"unit_cost"`       // StringFixed(8)
	UnitCostBasis string `json:"unit_cost_basis"` // 代表组件 component_type
	// SupplierCount 该 SKU 当前 EFFECTIVE 报价的去重供应商数。
	SupplierCount int  `json:"supplier_count"`
	SinglePoint   bool `json:"single_point"` // supplier_count == 1
	// FloorPrice = unit_cost / (1 − min_gross_margin)。读 sys_config 失败时置 null
	//（读路径降级，不打 500——6b-4 裁决）。
	FloorPrice *string `json:"floor_price"`
}

// BaselineListResult 是 §2 列表响应包 data 段（list/total/page/size，README 分页约定）。
type BaselineListResult struct {
	List  []BaselineListItem `json:"list"`
	Total int64              `json:"total"`
	Page  int                `json:"page"`
	Size  int                `json:"size"`
}

// BaselineHistoryItem 是 §3 历史时间线的一行。
type BaselineHistoryItem struct {
	Version             int             `json:"version"`
	ValidFrom           string          `json:"valid_from"`
	ValidTo             *string         `json:"valid_to"` // 当前版本为 null
	ChangeReason        string          `json:"change_reason"`
	PrimarySupplierID   int64           `json:"primary_supplier_id"`
	PrimarySupplierName string          `json:"primary_supplier_name"`
	UnitCost            string          `json:"unit_cost"` // 代表组件，StringFixed(8)
	UnitCostBasis       string          `json:"unit_cost_basis"`
	CalcSnapshot        json.RawMessage `json:"calc_snapshot"`
}

// ---- 读仓储接口（GORM 实现见 internal/repo/cost_baseline.go 读侧追加段） ----

// BaselineReadStore 是读路径的仓储接口（只读——不走 FOR UPDATE，不参与版本切换）。
type BaselineReadStore interface {
	// ListBaselines 查当前版本列表（已按 keyword/onlySinglePoint 过滤、按 sku_id 升序分页、
	// 已按 supplier_count 展开好——repo 与 DB 最近，在那里做 GROUP BY 与 HAVING 最省）。
	// 返回行逐个带齐：版本字段 + sku_code + primary_supplier_name + 代表组件 (basis, unit_cost) + supplier_count。
	// 实现必须固定 2 发查询（paged 行 + supplier 计数），禁止逐行 N+1。
	ListBaselines(ctx context.Context, q BaselineListQuery) ([]BaselineListItem, int64, error)
	// ResolveSKUID 把 handler 的 {sku} 路径段解析为 sku_id：
	// 纯数字 → 按 id；否则按 sku_code。两者都不存在 → (0, false, nil)。
	// sku_code 撞出多行 = 数据事故（返回错误），不静默取第一行。
	ResolveSKUID(ctx context.Context, sku string) (int64, bool, error)
	// ListBaselineHistory 查一个 SKU 的版本时间线：asOf=nil 全量（version DESC）；
	// asOf 非空 = 区间包含那一刻的那一条（valid_from<=asOf AND (valid_to IS NULL OR valid_to>asOf)，
	// 查不到返回空切片）。代表组件同样由 repo 展开。
	ListBaselineHistory(ctx context.Context, skuID int64, asOf *time.Time) ([]BaselineHistoryItem, error)
	// LoadMinGrossMargin 读 sys_config.min_gross_margin（floor 用）。
	// 读失败 → (zero, err) 由 service 置 floor_price=null，不打 500。
	LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error)

	// ---- 6d-4（§4 比价 / §5 议价机会）追加 ----

	// ListBaselineTrend 按天聚合取每日最新版本的 unit_cost（契约 §10-8 不插值）。
	// 日历日边界 = db.timezone（Asia/Shanghai）；返回 []TrendPoint{date, unit_cost, version}
	// 按 date 升序。days 已由 service 层规整（>0），repo 不兜底。
	ListBaselineTrend(ctx context.Context, skuID int64, days int) ([]TrendPoint, error)
	// ListSKUIDs 返回所有有成本基线的 sku_id（bargain 遍历域），升序，去重。
	ListSKUIDs(ctx context.Context) ([]int64, error)
	// LoadSysConfigDecimal 读 sys_config 数值配置（quote_anomaly_mkt 等）。
	// 与 supplier.ApproveStore.GetSysConfigDecimal 同款语义：缺失/非法 → 错误上抛。
	LoadSysConfigDecimal(ctx context.Context, key string) (decimal.Decimal, error)
	// ListPrimarySupplierNames 给一批 sku_id 取当前基线的 primary_supplier_name
	// （返回 supplier_id → legal_name；bargain 逐 SKU 不 N+1）。
	ListPrimarySupplierNames(ctx context.Context, skuIDs []int64) (map[int64]string, error)
	// ListPrimarySupplierIDs 给一批 sku_id 取当前基线的 (sku_id → primary_supplier_id)。
	// bargain 的"主供应商"必须来自这里（cost_baseline 当前行），**不是** CalcSKU.Primary——
	// 手动锁定的主供应商不是算法冠军，用 CalcSKU.Primary 会读错对象（6d-4 实现陷阱）。
	ListPrimarySupplierIDs(ctx context.Context, skuIDs []int64) (map[int64]int64, error)
}

// ---- ReadService ----

// ReadService 是成本基线的只读服务（无状态，可并发）。
type ReadService struct {
	store BaselineReadStore
}

// NewReadService 构造只读服务。
func NewReadService(store BaselineReadStore) *ReadService {
	return &ReadService{store: store}
}

// ListBaselines 实现 §2 列表。分页在 handler 已规整（page>=1, 1<=size<=100）。
// floor 用与重算侧同一个 Floor() 纯函数（裁决 1），读 sys_config 失败时该批 floor_price=null。
func (s *ReadService) ListBaselines(ctx context.Context, q BaselineListQuery) (*BaselineListResult, error) {
	items, total, err := s.store.ListBaselines(ctx, q)
	if err != nil {
		return nil, err
	}
	margin, mErr := s.store.LoadMinGrossMargin(ctx)
	for i := range items {
		if mErr != nil {
			items[i].FloorPrice = nil // 读路径降级：null 不 500
			continue
		}
		f, ferr := Floor(mustDecimal(items[i].UnitCost), margin)
		if ferr != nil {
			items[i].FloorPrice = nil // 除零等配置非法——同样降级
			continue
		}
		fs := f.StringFixed(8)
		items[i].FloorPrice = &fs
	}
	return &BaselineListResult{List: items, Total: total, Page: q.Page, Size: q.Size}, nil
}

// History 实现 §3 历史（含 asOf）。sku 段解析不到 → (nil, false, nil) 由 handler 回 404。
func (s *ReadService) History(ctx context.Context, sku string, asOf *time.Time) ([]BaselineHistoryItem, bool, error) {
	skuID, ok, err := s.store.ResolveSKUID(ctx, sku)
	if err != nil || !ok {
		return nil, ok, err
	}
	items, err := s.store.ListBaselineHistory(ctx, skuID, asOf)
	if err != nil {
		return nil, true, err
	}
	return items, true, nil
}

// ResolveSKUID 透传 store 的 {sku} 解析（6d-3 起也供 lock handler 复用——
// {sku} 的「纯数字按 id、否则按 sku_code、多行=数据事故」契约只有一份，挂在 store 上）。
func (s *ReadService) ResolveSKUID(ctx context.Context, sku string) (int64, bool, error) {
	return s.store.ResolveSKUID(ctx, sku)
}

// mustDecimal 解析 repo 已按 StringFixed(8) 规范化的金额串——repo 层字段，不可能非法；
// 出错是编程错误，按零值降级（不 panic——读路径别因为展示字段崩掉整个列表）。
func mustDecimal(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}
