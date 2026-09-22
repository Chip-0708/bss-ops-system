// Package workbench 的 service.go：10a §2 待办聚合 + §3 指标卡。
//
// 设计裁决（全部与 stage10a 提示词对齐）：
//  1. **权限**：已认证即可，不挂模块权限点（裁决 10）；按角色自动裁剪返回内容。
//  2. **行级过滤**（裁决 1）：采购/定价/销售/财务/管理看自己的待办（assignee_id=operator_id）；
//     **PLATFORM_ADMIN 看全部**（不按 assignee 过滤）。
//  3. **去重**：按 (biz_type, biz_id, priority) 去重（契约 §1 已声明）。
//  4. **指标卡实时聚合，不缓存**（裁决 2）——数据量小（各 <30 行），缓存是 10c 的事。
//  5. **破 floor**（裁决 3）：对每个 SKU，用 cost.Floor(unit_cost, min_gross_margin) 算 floor，
//     与当前生效 price_book 的 unit_price（代表组件）比较——`unit_price < floor_price` 计入。
//     只算 price_book.status='EFFECTIVE'。
//  6. **成本/毛利剔除**（裁决 9）：DTO 不含 unit_cost/floor_price/margin——SQL 层就不 SELECT。
package workbench

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ============================================================
// DTO
// ============================================================

// TodoItem 待办单行（不含成本/毛利字段）。
type TodoItem struct {
	ID           int64     `json:"id"`
	BizType      string    `json:"biz_type"`
	BizID        int64     `json:"biz_id"`
	Title        string    `json:"title"`
	Priority     string    `json:"priority"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	Deeplink     string    `json:"deeplink"`
	AssigneeName string    `json:"assignee_name"`
}

// TodoListResult 待办分页结果。
type TodoListResult struct {
	List  []TodoItem `json:"list"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}

// TodoQuery 查询条件。
type TodoQuery struct {
	BizType string
	Status  string // OPEN / DONE；空=全部
	Page    int
	Size    int
}

// MetricCard 指标卡单行。
type MetricCard struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Value    string `json:"value"` // 数字字符串（金额用 StringFixed(2)，计数值常用整数字符串）
	Unit     string `json:"unit"`
	Trend    string `json:"trend"` // UP / DOWN / FLAT / NA（本批不实现趋势，统一 NA）
	Deeplink string `json:"deeplink"`
}

// MetricsResult 指标卡集合（按角色裁剪后）。
type MetricsResult struct {
	Cards []MetricCard `json:"cards"`
}

// ============================================================
// Store 接口
// ============================================================

// Store 工作台所需仓储。
type Store interface {
	// ListTodos 待办列表（含按 ownerScope 的行级过滤 + 分页 + JOIN internal_staff 填 assignee_name）。
	// ownerScope=true 时按 assignee_id=operatorID 过滤；false 时不过滤（PLATFORM_ADMIN）。
	// 返回列表已按 (biz_type, biz_id, priority) 去重（SQL 或服务层均可，服务层兜底）。
	ListTodos(ctx context.Context, q TodoQuery, ownerScope bool, operatorID int64) ([]TodoItem, int64, error)

	// —— 采购卡片 ——
	// CountPendingQuotes 待审批报价数（quote_sheet status=SUBMITTED_PENDING / APPROVED_PENDING 且相关 buyer）。
	CountPendingQuotes(ctx context.Context, operatorID int64, ownerScope bool) (int64, error)
	// CountEffectiveQuotesThisMonth 本月生效报价数（裁决 4）。
	CountEffectiveQuotesThisMonth(ctx context.Context, operatorID int64, ownerScope bool, now time.Time) (int64, error)
	// CountQuotesExpiringIn30Days 30 天内到期报价数（裁决 5）。
	CountQuotesExpiringIn30Days(ctx context.Context, operatorID int64, ownerScope bool, now time.Time) (int64, error)
	// CountSupplierDepsForBuyer 单点依赖数（该 buyer 拥有的供应商中，按 SKU 去重只有 1 个有效报价的 SKU 数）。
	CountSupplierDepsForBuyer(ctx context.Context, operatorID int64, ownerScope bool) (int64, error)

	// —— 定价运营卡片 ——
	// CountPriceBooksPending 待发布价目表（status='DRAFT' 或 'APPROVING'）。
	CountPriceBooksPending(ctx context.Context) (int64, error)
	// LoadEffectivePriceBookItemsWithFloor 取生效价目表的（sku_id, 代表组件 unit_price）；floor 在 service 层算。
	// 返回的每个元素：sku_id / unit_price_str / currency。
	LoadEffectivePriceBookItemsWithFloor(ctx context.Context) ([]PriceBookItemForFloor, error)
	// LoadCurrentCostBaseline 按 sku_id 取当前生效成本基线（含 components 快照 + loss_rate/channel_rate）。
	LoadCurrentCostBaseline(ctx context.Context, skuID int64) (*CostBaselineForFloor, error)
	// LoadMinGrossMargin 从 sys_config 读全局 min_gross_margin（与 cost.CostBaselineRepo 同口径）。
	LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error)
	// CountPendingUpconduction 成本上涨待决策数（裁决 6）。
	CountPendingUpconduction(ctx context.Context) (int64, error)

	// —— 销售卡片 ——
	// CountMyCustomers 我的客户数（customer_profile.owner_sales_operator_id = operatorID）。
	CountMyCustomers(ctx context.Context, operatorID int64) (int64, error)
	// CountPendingCustomerQuotes 待确认报价数（裁决 7）。
	CountPendingCustomerQuotes(ctx context.Context, operatorID int64) (int64, error)

	// —— 财务卡片 ——
	// CountUnpaidDeposit 待处理押金数（裁决 8）。
	CountUnpaidDeposit(ctx context.Context) (int64, error)
}

// PriceBookItemForFloor 破 floor 判定的入参（不含 cost/margin——已剔除）。
type PriceBookItemForFloor struct {
	SKUID     int64
	UnitPrice string // 售价（代表组件），服务层 parse 为 decimal
	Currency  string
}

// CostBaselineForFloor 破 floor 判定的成本侧入参（不返回给 API，仅 service 内部用）。
type CostBaselineForFloor struct {
	SKUID       int64
	Currency    string
	Components  []CostComponentSnapshot // 来自 calc_snapshot.components
	LossRate    string                  // decimal str
	ChannelRate string                  // decimal str
}

// CostComponentSnapshot calc_snapshot.components 的单行。
type CostComponentSnapshot struct {
	ComponentType string
	UnitCost      string // decimal str
}

// ============================================================
// Service
// ============================================================

// Service 工作台查询服务。
type Service struct {
	store Store
	now   func() time.Time
}

// NewService 构造；now=nil 用 time.Now。
func NewService(store Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}

// ListTodos 待办聚合。
//
// 裁剪规则：
//   - PLATFORM_ADMIN：看全部（ownerScope=false）
//   - 其他角色：看自己的待办（ownerScope=true, assignee_id=operatorID）
//
// 去重：按 (biz_type, biz_id, priority) 保留 id 最小的一条。
// deeplink 按 biz_type：
//   - QUOTE → /quotes/{biz_id}/approval
//   - QUOTE_EXPIRE → /supplier/quotes/{biz_id}
func (s *Service) ListTodos(ctx context.Context, operatorRoles []string, operatorID int64, q TodoQuery) (*TodoListResult, error) {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.Size <= 0 {
		q.Size = 20
	}
	if q.Size > 200 {
		q.Size = 200
	}
	ownerScope := !hasRole(operatorRoles, "PLATFORM_ADMIN")
	list, total, err := s.store.ListTodos(ctx, q, ownerScope, operatorID)
	if err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	// 去重 + deeplink（assignee_name 已在 SQL 层 JOIN internal_staff 取到）
	seen := make(map[string]struct{})
	out := make([]TodoItem, 0, len(list))
	for _, it := range list {
		key := fmt.Sprintf("%s|%d|%s", it.BizType, it.BizID, it.Priority)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		it.Deeplink = deeplinkFor(it.BizType, it.BizID)
		out = append(out, it)
	}
	return &TodoListResult{List: out, Total: int(total), Page: q.Page, Size: q.Size}, nil
}

// deeplinkFor 按 biz_type 生成跳转链接。
func deeplinkFor(bizType string, bizID int64) string {
	switch bizType {
	case "QUOTE":
		return fmt.Sprintf("/quotes/%d/approval", bizID)
	case "QUOTE_EXPIRE":
		return fmt.Sprintf("/supplier/quotes/%d", bizID)
	default:
		return fmt.Sprintf("/todos/%d", bizID)
	}
}

// hasRole 工具：检查 roles 是否包含某个 code。
func hasRole(roles []string, code string) bool {
	for _, r := range roles {
		if r == code {
			return true
		}
	}
	return false
}

// ListMetrics 指标卡（按角色裁剪）。
//
// 优先级规则（多角色合并）：返回**所有**已拥有角色的卡片并集（不重复 key）。
// 例如 smoke_admin 有 PLATFORM_ADMIN+MODEL_OPS+PRICING_OP → 返回 PRICING_OP 的 3 张卡
// （PLATFORM_ADMIN/MODEL_OPS 当前未配置卡片）。
func (s *Service) ListMetrics(ctx context.Context, operatorRoles []string, operatorID int64) (*MetricsResult, error) {
	ownerScope := !hasRole(operatorRoles, "PLATFORM_ADMIN")
	cards := make([]MetricCard, 0, 8)
	addedKeys := make(map[string]struct{})

	// 采购 4 卡
	if hasRole(operatorRoles, "PROCUREMENT") {
		pend, err := s.store.CountPendingQuotes(ctx, operatorID, ownerScope)
		if err != nil {
			return nil, fmt.Errorf("count pending quotes: %w", err)
		}
		effMonth, err := s.store.CountEffectiveQuotesThisMonth(ctx, operatorID, ownerScope, s.now())
		if err != nil {
			return nil, fmt.Errorf("count effective quotes month: %w", err)
		}
		expire30, err := s.store.CountQuotesExpiringIn30Days(ctx, operatorID, ownerScope, s.now())
		if err != nil {
			return nil, fmt.Errorf("count quotes expiring 30d: %w", err)
		}
		singleDep, err := s.store.CountSupplierDepsForBuyer(ctx, operatorID, ownerScope)
		if err != nil {
			return nil, fmt.Errorf("count single deps: %w", err)
		}
		for _, c := range []MetricCard{
			{Key: "proc_pending_quotes", Title: "待审批报价数", Value: int64Str(pend), Unit: "条", Trend: "NA", Deeplink: "/quotes?status=PENDING"},
			{Key: "proc_effective_this_month", Title: "本月生效报价数", Value: int64Str(effMonth), Unit: "条", Trend: "NA", Deeplink: "/quotes?status=EFFECTIVE"},
			{Key: "proc_expire_30d", Title: "30 天内到期数", Value: int64Str(expire30), Unit: "条", Trend: "NA", Deeplink: "/quotes?expire=30d"},
			{Key: "proc_single_dep", Title: "单点依赖数", Value: int64Str(singleDep), Unit: "个", Trend: "NA", Deeplink: "/suppliers/single-dep"},
		} {
			if _, dup := addedKeys[c.Key]; !dup {
				addedKeys[c.Key] = struct{}{}
				cards = append(cards, c)
			}
		}
	}

	// 定价运营 3 卡
	if hasRole(operatorRoles, "PRICING_OP") {
		pbPending, err := s.store.CountPriceBooksPending(ctx)
		if err != nil {
			return nil, fmt.Errorf("count price books pending: %w", err)
		}
		floorViol, err := s.countFloorViolations(ctx)
		if err != nil {
			return nil, fmt.Errorf("count floor violations: %w", err)
		}
		upPend, err := s.store.CountPendingUpconduction(ctx)
		if err != nil {
			return nil, fmt.Errorf("count pending upconduction: %w", err)
		}
		for _, c := range []MetricCard{
			{Key: "pricing_pending_books", Title: "待发布价目表", Value: int64Str(pbPending), Unit: "份", Trend: "NA", Deeplink: "/price-books?status=DRAFT"},
			{Key: "pricing_floor_violations", Title: "破 floor 项数", Value: int64Str(floorViol), Unit: "条", Trend: "NA", Deeplink: "/pricing/floor-violations"},
			{Key: "pricing_pending_upconduction", Title: "成本上涨待决策数", Value: int64Str(upPend), Unit: "条", Trend: "NA", Deeplink: "/pricing/upconduction?status=PENDING"},
		} {
			if _, dup := addedKeys[c.Key]; !dup {
				addedKeys[c.Key] = struct{}{}
				cards = append(cards, c)
			}
		}
	}

	// 销售 3 卡（本季度成交额为 0 占位，10a-② 遗留）
	if hasRole(operatorRoles, "SALES") {
		custCnt, err := s.store.CountMyCustomers(ctx, operatorID)
		if err != nil {
			return nil, fmt.Errorf("count my customers: %w", err)
		}
		quotePend, err := s.store.CountPendingCustomerQuotes(ctx, operatorID)
		if err != nil {
			return nil, fmt.Errorf("count pending customer quotes: %w", err)
		}
		for _, c := range []MetricCard{
			{Key: "sales_my_customers", Title: "我的客户数", Value: int64Str(custCnt), Unit: "个", Trend: "NA", Deeplink: "/customers"},
			{Key: "sales_pending_quotes", Title: "待确认报价数", Value: int64Str(quotePend), Unit: "条", Trend: "NA", Deeplink: "/customer-quotes?status=PENDING"},
			{Key: "sales_quarterly_deal", Title: "本季度成交额", Value: "0.00", Unit: "元", Trend: "NA", Deeplink: "/deals/quarterly"},
		} {
			if _, dup := addedKeys[c.Key]; !dup {
				addedKeys[c.Key] = struct{}{}
				cards = append(cards, c)
			}
		}
	}

	// 财务 2 卡（待锁定汇率月份为 0 占位，10a-⑤ 遗留）
	if hasRole(operatorRoles, "FINANCE") {
		deposit, err := s.store.CountUnpaidDeposit(ctx)
		if err != nil {
			return nil, fmt.Errorf("count unpaid deposit: %w", err)
		}
		for _, c := range []MetricCard{
			{Key: "fin_fx_pending_month", Title: "待锁定汇率月份", Value: "0", Unit: "个月", Trend: "NA", Deeplink: "/fx/lock"},
			{Key: "fin_deposit_unpaid", Title: "待处理押金/授信", Value: int64Str(deposit), Unit: "笔", Trend: "NA", Deeplink: "/customers?deposit=UNPAID"},
		} {
			if _, dup := addedKeys[c.Key]; !dup {
				addedKeys[c.Key] = struct{}{}
				cards = append(cards, c)
			}
		}
	}

	return &MetricsResult{Cards: cards}, nil
}

// countFloorViolations 破 floor 项数（裁决 3）。
//
// 对每个生效价目表条目：取代表组件 unit_price（售价），与该 SKU 当前成本基线算出的
// floor_price 比较（floor = unit_cost / (1 - min_gross_margin)）。unit_price < floor → 计入。
//
// 数据源不出 DTO：所有 unit_cost/floor_price 都在 service/repo 内部消化，绝不透出到 API。
func (s *Service) countFloorViolations(ctx context.Context) (int64, error) {
	items, err := s.store.LoadEffectivePriceBookItemsWithFloor(ctx)
	if err != nil {
		return 0, fmt.Errorf("load price book items: %w", err)
	}
	minMargin, err := s.store.LoadMinGrossMargin(ctx)
	if err != nil {
		return 0, fmt.Errorf("load min_gross_margin: %w", err)
	}
	denom := decimal.NewFromInt(1).Sub(minMargin)
	if denom.LessThanOrEqual(decimal.Zero) {
		return 0, fmt.Errorf("sys_config min_gross_margin=%s 必须 < 1", minMargin.String())
	}
	var cnt int64
	for _, it := range items {
		bl, err := s.store.LoadCurrentCostBaseline(ctx, it.SKUID)
		if err != nil || bl == nil {
			continue // 无成本基线 → 无法判 floor，跳过（不算破）
		}
		// 代表组件 unit_cost：input 优先，无 input 按 component_type 字母序兜底（与 8a 同口径）。
		unitCost, ok := representativeUnitCost(bl.Components)
		if !ok {
			continue
		}
		loss, err := decimal.NewFromString(bl.LossRate)
		if err != nil {
			continue
		}
		ch, err := decimal.NewFromString(bl.ChannelRate)
		if err != nil {
			continue
		}
		// 完全成本 = 组件报价 × (1+lossRate) × (1+channelRate)
		fullCost := unitCost.Mul(decimal.NewFromInt(1).Add(loss)).Mul(decimal.NewFromInt(1).Add(ch))
		// floor = fullCost / (1 - minGrossMargin)
		floor := fullCost.Div(denom)
		// 售价
		price, err := decimal.NewFromString(it.UnitPrice)
		if err != nil {
			continue
		}
		// 破 floor 判定（变异验证 #2 锚点：是 < 不是 <=）
		if price.LessThan(floor) {
			cnt++
		}
	}
	return cnt, nil
}

// representativeUnitCost 从组件快照取代表组件（input 优先，否则字母序最小）。
func representativeUnitCost(components []CostComponentSnapshot) (decimal.Decimal, bool) {
	if len(components) == 0 {
		return decimal.Zero, false
	}
	// 找 input
	for _, c := range components {
		if c.ComponentType == "input" {
			d, err := decimal.NewFromString(c.UnitCost)
			if err == nil {
				return d, true
			}
		}
	}
	// 兜底：字母序最小
	best := components[0]
	for _, c := range components[1:] {
		if c.ComponentType < best.ComponentType {
			best = c
		}
	}
	d, err := decimal.NewFromString(best.UnitCost)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}

// int64Str 计数 → 字符串。
func int64Str(v int64) string {
	return fmt.Sprintf("%d", v)
}
