// Package price 的 sync_service.go：官方价采集批次 + 暂存区 + 差异比对（07-supplier-and-price-change.md §5/§6，阶段 7a）。
//
// 语义裁决（全部钉死在本文件与单测，改动前先读）：
//  1. sync_job 创建即完成：MVP 无自动采集过程，sync_job 只是「一批人工录入的容器」。
//     status 用 DDL 枚举 SUCCESS（000006 注释 RUNNING/SUCCESS/FAILED）——提示词裁决 2 原文
//     写 "DONE"，但红线 4 禁止自创状态串，按 DDL 枚举收敛为 SUCCESS（语义等同）。
//     P1 自动采集时再补 RUNNING/FAILED 状态机（遗留 7a-①）。
//  2. payload 结构 = {component_type: 十进制字符串} 的扁平 map（裁决 1，遗留 7a-②），
//     key 限定 quote/price 共用的 12 种 component_type（§0.3.1），值必须是合法非负
//     decimal 字符串——写入时校验并 trim 规整，绝不静默吞非法值。
//  3. 差异比对在**读侧实时计算**（staging_price 表没有 diff 列），口径（裁决 4）：
//     - UNMATCHED：sku_id 为 NULL（raw_sku_code 匹配不到），diff_detail=[]；
//     - NEW：该 SKU 无「is_current=true 且同币种」的 price_version，
//     diff_detail 列出 payload 全部组件（old_price=null、delta_pct=null）；
//     - CHANGED/UNCHANGED：只比对 payload 里出现且当前版本也有的 component_type
//     （payload 多出的组件跳过），数值比较用 decimal.Equal（"2.50"≡"2.50000000"）；
//     - delta_pct = (new−old)/old，StringFixed(6)；old=0 → null（不除零）。
//  4. sku_id 与 raw_sku_code 二选一（裁决 3/6）：sku_id 优先（给了就忽略 raw_sku_code，
//     落库 NULL）；只给 raw_sku_code 时按 model_sku.sku_code 精确匹配——匹配到回填
//     sku_id 且保留原始码，匹配不到 sku_id=NULL、match_status=UNMATCHED（只标记，
//     怎么处理待产品裁决——遗留 7a-③）。
//  5. processed 恒 false（裁决 5）：本批只做录入，7b confirm 时置 true。
//  6. 金额一律 shopspring/decimal + 字符串传输（红线 1），本文件禁止 float64 参与运算。
package price

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ---- 常量 ----

// sync_job.job_type 的合法枚举（000006 DDL 注释）。
const (
	JobTypeSyncModels    = "SYNC_MODELS"
	JobTypeSyncPrices    = "SYNC_PRICES"
	JobTypeSyncCommunity = "SYNC_COMMUNITY"
)

// sync_job.status 的合法枚举（000006 DDL 注释：RUNNING/SUCCESS/FAILED）。
// MVP 人工录入创建即 JobStatusSuccess（裁决 2，见包注释）。
const (
	JobStatusRunning = "RUNNING"
	JobStatusSuccess = "SUCCESS"
	JobStatusFailed  = "FAILED"
)

// diff_status 的四个值（契约 §6）。这是比对结果标记，不是 §2.6 实体状态机。
const (
	DiffNew       = "NEW"
	DiffChanged   = "CHANGED"
	DiffUnchanged = "UNCHANGED"
	DiffUnmatched = "UNMATCHED"
)

// staging_price.match_status（000006 DDL 注释：UNMATCHED/MATCHED/CONFLICT）。
// MVP 人工录入只会产生 MATCHED / UNMATCHED；CONFLICT 留给 P1 多源采集。
const (
	MatchMatched   = "MATCHED"
	MatchUnmatched = "UNMATCHED"
)

// maxStagingItems 单次录入行数上限（与供应商 CSV 导入的 500 行同款纪律）。
const maxStagingItems = 500

// componentTypes 是 payload key 的合法集合——与 supplier 包 §0.3.1 的 12 种
// component_type 完全一致（那份是私有的，这里独立一份；改任何一侧必须同步改另一侧）。
var componentTypes = map[string]bool{
	"input": true, "output": true, "cached_input": true,
	"cache_write_5m": true, "cache_write_1h": true, "reasoning": true,
	"embedding": true, "request": true,
	"image_input": true, "image_output": true,
	"audio_input": true, "audio_output": true,
}

// ---- 领域错误（handler 按 errors.Is 映射 HTTP 码） ----

var (
	// ErrSyncStoreNil 未接仓储时的防御错误（不允许空指针 panic）。
	ErrSyncStoreNil = errors.New("price.SyncService 未接 SyncStore")
	// ErrJobTypeInvalid job_type 不在 DDL 枚举内（绝不自创值——红线 4 同款纪律）。
	ErrJobTypeInvalid = errors.New("job_type 非法（仅 SYNC_MODELS/SYNC_PRICES/SYNC_COMMUNITY）")
	// ErrJobSourceEmpty source 必填。
	ErrJobSourceEmpty = errors.New("source 必填且不能为空白")
	// ErrJobSKUInvalid sku_ids 含非正整数。
	ErrJobSKUInvalid = errors.New("sku_ids 含非正整数")
	// ErrSyncJobNotFound 采集批次不存在（404）。
	ErrSyncJobNotFound = errors.New("采集批次不存在")
	// ErrStagingItemsEmpty items 必填非空（绝不静默吞输入）。
	ErrStagingItemsEmpty = errors.New("items 必填且不能为空")
	// ErrStagingTooMany 单次录入超过 500 行。
	ErrStagingTooMany = errors.New("单次最多录入 500 行，请拆分后重新提交")
	// ErrStagingItemNoSKU sku_id 与 raw_sku_code 都缺失。
	ErrStagingItemNoSKU = errors.New("sku_id 与 raw_sku_code 至少提供一个")
	// ErrStagingSKUNotFound 显式给的 sku_id 在 model_sku 不存在（提示词单测要求 400）。
	ErrStagingSKUNotFound = errors.New("sku_id 不存在")
	// ErrStagingCurrencyInvalid currency 必须是 3 位币种码（char(3) 列）。
	ErrStagingCurrencyInvalid = errors.New("currency 必须是 3 位币种码（如 USD/CNY）")
	// ErrStagingPayloadEmpty payload 必填非空（提示词单测要求 400）。
	ErrStagingPayloadEmpty = errors.New("payload 必填且不能为空")
	// ErrStagingComponentInvalid payload 含 12 种之外的 component_type。
	ErrStagingComponentInvalid = errors.New("payload 含未知 component_type")
	// ErrStagingPriceInvalid payload 值不是合法非负十进制字符串。
	ErrStagingPriceInvalid = errors.New("payload 值必须是合法的非负十进制字符串")
)

// ---- DTO（json tag 与契约字段一一对应） ----

// SyncJob 是采集批次（§5 响应 data）。时间一律 RFC3339 字符串（项目约定，
// 与 cost/supplier 各列表 DTO 同构——不用 time.Time 直出纳秒）。
type SyncJob struct {
	ID         int64   `json:"id"`
	JobType    string  `json:"job_type"`
	Source     string  `json:"source"`
	Status     string  `json:"status"`
	StartedAt  string  `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
	ErrorMsg   *string `json:"error_msg"`
	// ItemCount 是 sku_ids 的留痕（sync_job 表没有 payload 列，只能落计数——遗留 7a-①）。
	ItemCount *int `json:"item_count"`
}

// SyncJobListResult 是 §5 列表响应包 data 段（README 分页约定）。
type SyncJobListResult struct {
	List  []SyncJob `json:"list"`
	Total int64     `json:"total"`
	Page  int       `json:"page"`
	Size  int       `json:"size"`
}

// DiffDetail 是差异明细的一行（契约 §6）。
// OldPrice 为 *string：NEW 时 old_price=null（提示词草图写 string，但 null 语义要求指针——
// 这是按契约语义对草图的最小修正）。价格字符串均为原样透传（old=DB numeric(20,8) 原文，
// new=录入原文），不做重排版。
type DiffDetail struct {
	ComponentType string  `json:"component_type"`
	OldPrice      *string `json:"old_price"`
	NewPrice      string  `json:"new_price"`
	DeltaPct      *string `json:"delta_pct"`
}

// StagingItem 是 §6 列表的一行。
type StagingItem struct {
	ID          int64             `json:"id"`
	SyncJobID   int64             `json:"sync_job_id"`
	SKUID       *int64            `json:"sku_id"`
	RawSkuCode  *string           `json:"raw_sku_code"`
	Currency    string            `json:"currency"`
	Payload     map[string]string `json:"payload"`
	MatchStatus string            `json:"match_status"`
	DiffStatus  string            `json:"diff_status"`
	DiffDetail  []DiffDetail      `json:"diff_detail"`
	Processed   bool              `json:"processed"`
	CreatedAt   string            `json:"created_at"`
}

// StagingListResult 是 §6 列表响应包 data 段。
type StagingListResult struct {
	List  []StagingItem `json:"list"`
	Total int64         `json:"total"`
	Page  int           `json:"page"`
	Size  int           `json:"size"`
}

// ---- 输入 ----

// CreateJobInput 是 POST /price-sync/jobs 的请求体（§5）。
type CreateJobInput struct {
	JobType string  `json:"job_type"`
	Source  string  `json:"source"`
	SkuIDs  []int64 `json:"sku_ids"`
}

// StagingItemInput 是录入的一行。Payload 值收 any 是为了给「非字符串值」精确 400
// （直接 bind map[string]string 会把类型错误糊成一条泛化消息）。
type StagingItemInput struct {
	SKUID      *int64         `json:"sku_id"`
	RawSkuCode *string        `json:"raw_sku_code"`
	Currency   string         `json:"currency"`
	Payload    map[string]any `json:"payload"`
}

// CreateStagingInput 是 POST /staging-prices 的请求体（裁决 3）。
type CreateStagingInput struct {
	SyncJobID int64              `json:"sync_job_id"`
	Items     []StagingItemInput `json:"items"`
}

// StagingQuery 是 §6 列表查询条件。
type StagingQuery struct {
	SyncJobID *int64 // nil = 不过滤
	Page      int
	Size      int
}

// ---- 仓储窄接口（GORM 实现见 internal/repo/price_sync.go） ----

// CreateJobParams 是 CreateSyncJob 的落库参数（服务层已校验，repo 机械写入）。
type CreateJobParams struct {
	Now          time.Time
	RequestID    string
	OperatorID   int64
	OperatorRole string
	JobType      string
	Source       string
	ItemCount    int
}

// StagingRowInsert 是一行 staging_price 的落库参数（服务层已解析/校验完）。
type StagingRowInsert struct {
	SKUID       *int64
	RawSkuCode  *string
	Currency    string
	Payload     map[string]string // 已 trim 规整的十进制字符串
	MatchStatus string            // MATCHED / UNMATCHED
}

// CreateStagingParams 是批量录入的落库参数（单事务：逐行 INSERT + 审计）。
type CreateStagingParams struct {
	Now          time.Time
	RequestID    string
	OperatorID   int64
	OperatorRole string
	SyncJobID    int64
	Rows         []StagingRowInsert
}

// CreateStagingResult 是录入结果（§裁决 3 响应 data）。
type CreateStagingResult struct {
	CreatedCount int     `json:"created_count"`
	StagingIDs   []int64 `json:"staging_ids"`
	AuditLogID   int64   `json:"-"`
}

// StagingRow 是读侧的暂存行（repo → service）。
type StagingRow struct {
	ID          int64
	SyncJobID   int64
	SKUID       *int64
	RawSkuCode  *string
	Currency    string
	Payload     map[string]string
	MatchStatus string
	Processed   bool
	CreatedAt   time.Time
}

// CurrentPriceVersion 是某 SKU 的当前官方价版本（is_current=true；
// uk_price_current 部分唯一索引保证每 SKU 至多一行）。
type CurrentPriceVersion struct {
	SKUID      int64
	Currency   string
	VersionNo  int
	Components map[string]string // component_type → unit_price 原文（numeric(20,8) ::text）
}

// SyncStore 是采集/暂存的仓储窄接口。写方法必须经 txOf(ctx)
// （幂等中间件注入的事务优先——CLAUDE.md 坑位表同款纪律）。
type SyncStore interface {
	// CreateSyncJob 单事务：INSERT sync_job（创建即 SUCCESS）+ audit_log。
	CreateSyncJob(ctx context.Context, p CreateJobParams) (*SyncJob, error)
	// ListSyncJobs 分页列表（id DESC）。
	ListSyncJobs(ctx context.Context, page, size int) ([]SyncJob, int64, error)
	// SyncJobExists 批次存在性（404 门槛）。
	SyncJobExists(ctx context.Context, id int64) (bool, error)
	// SKUExists 校验 model_sku.id 存在。
	SKUExists(ctx context.Context, skuID int64) (bool, error)
	// FindSKUIDByCode 按 sku_code 精确匹配（uk_sku_code 唯一）。
	FindSKUIDByCode(ctx context.Context, code string) (int64, bool, error)
	// CreateStagingPrices 单事务：逐行 INSERT staging_price + audit_log。
	CreateStagingPrices(ctx context.Context, p CreateStagingParams) (*CreateStagingResult, error)
	// ListStagingPrices 分页列表（id ASC = 录入序），可按 sync_job_id 过滤。
	ListStagingPrices(ctx context.Context, q StagingQuery) ([]StagingRow, int64, error)
	// LoadCurrentPriceVersions 批量取当前官方价版本 + 组件（固定 2 发查询，禁止 N+1）。
	LoadCurrentPriceVersions(ctx context.Context, skuIDs []int64) ([]CurrentPriceVersion, error)
}

// ---- 服务 ----

// SyncService 是采集批次 + 暂存区的业务编排（无状态，可并发使用）。
type SyncService struct {
	store SyncStore
	now   func() time.Time // 可注入时钟（测试用），默认 time.Now().UTC
}

// NewSyncService 构造服务。now 为 nil 时用 time.Now().UTC。
func NewSyncService(store SyncStore, now func() time.Time) *SyncService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &SyncService{store: store, now: now}
}

// Operator 是写操作的审计素材（鉴权已在中间件完成，这里只落审计——
// 与 cost.PutOperator 同一纪律：OperatorRole 绝不参与权限判定）。
type Operator struct {
	OperatorID   int64
	OperatorRole string
}

// CreateJob 创建采集批次（§5）。MVP 创建即 SUCCESS、started_at=finished_at=now、
// error_msg=NULL（裁决 2）；sku_ids 只留 item_count 痕迹（表无 payload 列）。
func (s *SyncService) CreateJob(ctx context.Context, in CreateJobInput, op Operator, requestID string) (*SyncJob, error) {
	if s == nil || s.store == nil {
		return nil, ErrSyncStoreNil
	}
	jobType := strings.TrimSpace(in.JobType)
	switch jobType {
	case JobTypeSyncModels, JobTypeSyncPrices, JobTypeSyncCommunity:
	default:
		return nil, fmt.Errorf("%w：收到 %q", ErrJobTypeInvalid, in.JobType)
	}
	source := strings.TrimSpace(in.Source)
	if source == "" || len(source) > 32 {
		return nil, ErrJobSourceEmpty
	}
	for _, id := range in.SkuIDs {
		if id <= 0 {
			return nil, fmt.Errorf("%w：sku_id=%d", ErrJobSKUInvalid, id)
		}
	}
	return s.store.CreateSyncJob(ctx, CreateJobParams{
		Now:          s.now(),
		RequestID:    requestID,
		OperatorID:   op.OperatorID,
		OperatorRole: op.OperatorRole,
		JobType:      jobType,
		Source:       source,
		ItemCount:    len(in.SkuIDs),
	})
}

// ListJobs 采集批次列表（§5，id DESC 分页）。
func (s *SyncService) ListJobs(ctx context.Context, page, size int) (*SyncJobListResult, error) {
	if s == nil || s.store == nil {
		return nil, ErrSyncStoreNil
	}
	list, total, err := s.store.ListSyncJobs(ctx, page, size)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []SyncJob{}
	}
	return &SyncJobListResult{List: list, Total: total, Page: page, Size: size}, nil
}

// CreateStaging 手工录入采集结果（裁决 3）。校验全部在落库前完成，
// 任一行非法 → 整批 400 不落库（绝不部分成功——与 CSV 导入 preview/confirm 的两段式不同，
// 这里是 JSON 直录，原子性更直白）。
func (s *SyncService) CreateStaging(ctx context.Context, in CreateStagingInput, op Operator, requestID string) (*CreateStagingResult, error) {
	if s == nil || s.store == nil {
		return nil, ErrSyncStoreNil
	}
	if in.SyncJobID <= 0 {
		return nil, ErrSyncJobNotFound
	}
	ok, err := s.store.SyncJobExists(ctx, in.SyncJobID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w：sync_job_id=%d", ErrSyncJobNotFound, in.SyncJobID)
	}
	if len(in.Items) == 0 {
		return nil, ErrStagingItemsEmpty
	}
	if len(in.Items) > maxStagingItems {
		return nil, ErrStagingTooMany
	}

	rows := make([]StagingRowInsert, 0, len(in.Items))
	for i := range in.Items {
		row, err := s.normalizeItem(ctx, &in.Items[i])
		if err != nil {
			return nil, fmt.Errorf("items[%d]: %w", i, err)
		}
		rows = append(rows, *row)
	}
	return s.store.CreateStagingPrices(ctx, CreateStagingParams{
		Now:          s.now(),
		RequestID:    requestID,
		OperatorID:   op.OperatorID,
		OperatorRole: op.OperatorRole,
		SyncJobID:    in.SyncJobID,
		Rows:         rows,
	})
}

// normalizeItem 校验并规整一行录入（裁决 3/4/6 的落地处）。
func (s *SyncService) normalizeItem(ctx context.Context, it *StagingItemInput) (*StagingRowInsert, error) {
	row := &StagingRowInsert{}

	// SKU 解析：sku_id 优先；只给 raw_sku_code 时按 sku_code 精确匹配。
	if it.SKUID != nil {
		if *it.SKUID <= 0 {
			return nil, fmt.Errorf("%w：sku_id=%d", ErrStagingSKUNotFound, *it.SKUID)
		}
		exists, err := s.store.SKUExists(ctx, *it.SKUID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("%w：sku_id=%d", ErrStagingSKUNotFound, *it.SKUID)
		}
		skuID := *it.SKUID
		row.SKUID = &skuID
		row.MatchStatus = MatchMatched
		// raw_sku_code 被忽略（裁决 3：都给了以 sku_id 为准）——落库 NULL。
	} else {
		code := ""
		if it.RawSkuCode != nil {
			code = strings.TrimSpace(*it.RawSkuCode)
		}
		if code == "" {
			return nil, ErrStagingItemNoSKU
		}
		skuID, matched, err := s.store.FindSKUIDByCode(ctx, code)
		if err != nil {
			return nil, err
		}
		row.RawSkuCode = strPtr(code)
		if matched {
			row.SKUID = &skuID
			row.MatchStatus = MatchMatched
		} else {
			row.SKUID = nil // 匹配不到绝不硬填（变异验证 3 的挂接点）
			row.MatchStatus = MatchUnmatched
		}
	}

	// currency：3 位币种码（char(3) 列，trim + 大写规整）。
	currency := strings.ToUpper(strings.TrimSpace(it.Currency))
	if len(currency) != 3 {
		return nil, fmt.Errorf("%w：收到 %q", ErrStagingCurrencyInvalid, it.Currency)
	}
	row.Currency = currency

	// payload：非空 + key 限 12 组件 + 值为合法非负 decimal 字符串（trim 后落库）。
	if len(it.Payload) == 0 {
		return nil, ErrStagingPayloadEmpty
	}
	payload := make(map[string]string, len(it.Payload))
	for ct, v := range it.Payload {
		if !componentTypes[ct] {
			return nil, fmt.Errorf("%w：%s（合法集合为 12 种 component_type）", ErrStagingComponentInvalid, ct)
		}
		raw, isStr := v.(string)
		if !isStr {
			return nil, fmt.Errorf("%w：%s 的值必须是字符串（金额一律字符串传输——红线 1）", ErrStagingPriceInvalid, ct)
		}
		trimmed := strings.TrimSpace(raw)
		d, err := decimal.NewFromString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("%w：%s=%q", ErrStagingPriceInvalid, ct, raw)
		}
		if d.IsNegative() {
			return nil, fmt.Errorf("%w：%s=%q 为负数", ErrStagingPriceInvalid, ct, raw)
		}
		payload[ct] = trimmed
	}
	row.Payload = payload
	return row, nil
}

// ListStaging 暂存区列表（§6）：读侧实时算 diff（表无 diff 列），固定 3 发查询
// （count + paged 行 + 当前官方价批量），禁止逐行 N+1。
func (s *SyncService) ListStaging(ctx context.Context, q StagingQuery) (*StagingListResult, error) {
	if s == nil || s.store == nil {
		return nil, ErrSyncStoreNil
	}
	rows, total, err := s.store.ListStagingPrices(ctx, q)
	if err != nil {
		return nil, err
	}

	// 批量取当前官方价版本（去重 sku_id）。
	skuSet := make(map[int64]struct{}, len(rows))
	for i := range rows {
		if rows[i].SKUID != nil {
			skuSet[*rows[i].SKUID] = struct{}{}
		}
	}
	skuIDs := make([]int64, 0, len(skuSet))
	for id := range skuSet {
		skuIDs = append(skuIDs, id)
	}
	sort.Slice(skuIDs, func(i, j int) bool { return skuIDs[i] < skuIDs[j] })
	versions, err := s.store.LoadCurrentPriceVersions(ctx, skuIDs)
	if err != nil {
		return nil, err
	}
	verBySku := make(map[int64]CurrentPriceVersion, len(versions))
	for _, v := range versions {
		verBySku[v.SKUID] = v // uk_price_current 保证每 SKU 至多一行 current
	}

	list := make([]StagingItem, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		item := StagingItem{
			ID:          r.ID,
			SyncJobID:   r.SyncJobID,
			SKUID:       r.SKUID,
			RawSkuCode:  r.RawSkuCode,
			Currency:    r.Currency,
			Payload:     r.Payload,
			MatchStatus: r.MatchStatus,
			Processed:   r.Processed,
			CreatedAt:   r.CreatedAt.UTC().Format(time.RFC3339),
		}
		if r.SKUID == nil {
			// UNMATCHED：raw_sku_code 匹配不到 SKU（裁决 4），不做比对。
			item.DiffStatus = DiffUnmatched
			item.DiffDetail = []DiffDetail{}
			list = append(list, item)
			continue
		}
		var cur *CurrentPriceVersion
		if v, ok := verBySku[*r.SKUID]; ok && strings.EqualFold(v.Currency, r.Currency) {
			cur = &v // 同币种才算「有当前官方价」；币种不同按 NEW 处理（裁决 4）
		}
		status, detail, err := ComputeDiff(r.Payload, cur)
		if err != nil {
			return nil, fmt.Errorf("staging_price id=%d diff: %w", r.ID, err)
		}
		item.DiffStatus = status
		item.DiffDetail = detail
		list = append(list, item)
	}
	return &StagingListResult{List: list, Total: total, Page: q.Page, Size: q.Size}, nil
}

// ---- 差异比对（纯函数，可单测） ----

// ComputeDiff 按裁决 4 的口径比对 payload 与当前官方价版本。
// cur == nil → NEW（diff_detail 列出 payload 全部组件，old_price/delta_pct=null）；
// cur != nil → 只比对 payload 里出现且当前版本也有的组件，数值等价即 UNCHANGED。
// payload/组件原文解析失败 = 数据被绕过应用层写脏，报错不静默。
func ComputeDiff(payload map[string]string, cur *CurrentPriceVersion) (string, []DiffDetail, error) {
	cts := make([]string, 0, len(payload))
	for ct := range payload {
		cts = append(cts, ct)
	}
	sort.Strings(cts) // 输出顺序确定（map 遍历序随机，绝不带给前端）

	if cur == nil {
		details := make([]DiffDetail, 0, len(cts))
		for _, ct := range cts {
			details = append(details, DiffDetail{
				ComponentType: ct,
				OldPrice:      nil,
				NewPrice:      payload[ct],
				DeltaPct:      nil,
			})
		}
		return DiffNew, details, nil
	}

	details := make([]DiffDetail, 0, len(cts))
	for _, ct := range cts {
		oldRaw, ok := cur.Components[ct]
		if !ok {
			continue // payload 多出的组件不比对（裁决 4）
		}
		newDec, err := decimal.NewFromString(strings.TrimSpace(payload[ct]))
		if err != nil {
			return "", nil, fmt.Errorf("payload %s=%q 非法: %w", ct, payload[ct], err)
		}
		oldDec, err := decimal.NewFromString(strings.TrimSpace(oldRaw))
		if err != nil {
			return "", nil, fmt.Errorf("price_component %s=%q 非法: %w", ct, oldRaw, err)
		}
		if newDec.Equal(oldDec) {
			continue // 数值等价（"2.50"≡"2.50000000"）→ 不进 diff_detail
		}
		d := DiffDetail{
			ComponentType: ct,
			OldPrice:      strPtr(strings.TrimSpace(oldRaw)),
			NewPrice:      payload[ct],
		}
		if !oldDec.IsZero() {
			// delta_pct = (new−old)/old，6 位小数（E2E 锚点 "0.120000"）；old=0 → null 不除零。
			pct := newDec.Sub(oldDec).Div(oldDec).StringFixed(6)
			d.DeltaPct = &pct
		}
		details = append(details, d)
	}
	if len(details) > 0 {
		return DiffChanged, details, nil
	}
	return DiffUnchanged, details, nil
}

// strPtr 返回字符串指针（nil 语义与 NULL 列对齐）。
func strPtr(s string) *string { return &s }
