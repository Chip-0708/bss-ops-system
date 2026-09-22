// Package supplier 的 import.go：批量导入（05-quotes.md §6，按 5c 裁决口径）。
// CSV 解析为纯函数（[]byte 进，行结构出），domain 层零 http/multipart 依赖（可单测）。
// 金额一律 shopspring/decimal。
package supplier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ---- 领域错误 ----

var (
	// ErrImportFileTooLarge 文件超过 2MB。
	ErrImportFileTooLarge = errors.New("文件大小超过 2MB 限制")
	// ErrImportTooManyRows 数据行超过 500。
	ErrImportTooManyRows = errors.New("单次最多导入 500 行，请拆分后重新上传")
	// ErrImportFormatNotSupported ?format=xlsx 本阶段预留但不实现。
	ErrImportFormatNotSupported = errors.New("本阶段仅支持 CSV（UTF-8 带 BOM），xlsx 暂未开放")
	// ErrImportHeaderInvalid 表头缺失必需列。
	ErrImportHeaderInvalid = errors.New("CSV 表头缺少必需列（sku_id / sku_code / currency）")
	// ErrImportCSVMalformed CSV 解析失败（列数不齐 / 引号未闭合等）。
	ErrImportCSVMalformed = errors.New("CSV 格式非法")
)

// ---- 常量 ----

const (
	maxImportFileBytes = 2 << 20 // 2 MB
	maxImportRows      = 500     // 数据行上限（不含表头）
)

// importFixedColumns 是固定列（锁定/只读 + 可填的 fx_tier 与约束列）。
// 用于表头校验时识别「已知但不可填的锁定列」与「完全无关的自定义列」（后者忽略，5c 裁决 3）。
var importFixedColumns = map[string]bool{
	"sku_id": true, "sku_code": true, "model_name": true, "currency": true,
	"last_valid_to": true, "fx_tier": true,
	"rpm": true, "tpm": true, "concurrency": true,
	"daily_quota": true, "actual_context": true, "compatibility": true,
}

// isKnownFixedColumn 判断是否固定列（供表头校验与未来扩展使用）。
func isKnownFixedColumn(col string) bool { return importFixedColumns[col] }

// ---- 模板（§6.1） ----

// TemplateQuery 是模板下载的查询条件。
type TemplateQuery struct {
	Scope    string // history / vendor / family / active（默认）
	VendorID *int64
	FamilyID *int64
}

// TemplateSKU 是模板的一行（一个可报价 SKU）。
type TemplateSKU struct {
	ID             int64
	SkuCode        string
	ModelName      string
	NativeCurrency string
	OfficialPrice  map[string]string // component_type → 官方价字符串（只读列）
	LastValidTo    *time.Time        // 该 SKU 上次报价的到期日（只读参考）
}

// TemplateData 是模板渲染数据（行 + 动态组件列）。
type TemplateData struct {
	ComponentTypes []string      // 动态组件列（排序后，列顺序稳定）
	Rows           []TemplateSKU // 模板行
}

// RenderTemplateCSV 渲染 CSV（UTF-8 带 BOM，Excel 兼容）。
// 列序：固定锁定列 → official_<type> ×N（动态）→ fx_tier → multiplier_<type>/price_<type> ×N（动态）
// → 约束 6 列 → last_valid_to。valid_from/valid_to 不在 CSV 里（由 confirm 请求体传入，5c 裁决 2）。
func RenderTemplateCSV(data *TemplateData) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM（encoding/csv 不写，需手动）

	w := csv.NewWriter(&buf)
	header := make([]string, 0, 4+len(data.ComponentTypes)+1+2*len(data.ComponentTypes)+7)
	header = append(header, "sku_id", "sku_code", "model_name", "currency")
	for _, ct := range data.ComponentTypes {
		header = append(header, "official_"+ct)
	}
	header = append(header, "fx_tier")
	for _, ct := range data.ComponentTypes {
		header = append(header, "multiplier_"+ct, "price_"+ct)
	}
	header = append(header, "rpm", "tpm", "concurrency", "daily_quota", "actual_context", "compatibility", "last_valid_to")
	if err := w.Write(header); err != nil {
		return nil, err
	}

	for _, row := range data.Rows {
		rec := []string{
			strconv.FormatInt(row.ID, 10),
			row.SkuCode,
			row.ModelName,
			row.NativeCurrency,
		}
		for _, ct := range data.ComponentTypes {
			rec = append(rec, row.OfficialPrice[ct]) // 无该组件官方价时为空串
		}
		rec = append(rec, "") // fx_tier 留空由用户填
		for range data.ComponentTypes {
			rec = append(rec, "", "") // multiplier / price 留空
		}
		lastValidTo := ""
		if row.LastValidTo != nil {
			lastValidTo = row.LastValidTo.Format(time.RFC3339)
		}
		rec = append(rec, "", "", "", "", "", "", lastValidTo) // 约束 6 列留空 + last_valid_to
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- 解析（纯函数，[]byte 进出行结构，零 http 依赖） ----

// importRow 是一行解析结果（表头名为键）。
type importRow struct {
	Line   int               // 行号（从 2 开始，1=表头）
	Fields map[string]string // 列名 → 原始字符串值（已 TrimSpace）
}

// parseImportCSV 解析 CSV：strip BOM → 表头 → 逐行（列数不齐/引号未闭合 → ErrImportCSVMalformed）。
// 返回行列表；行数超限由调用方（service）判定。
func parseImportCSV(data []byte) ([]importRow, error) {
	// 手动 strip UTF-8 BOM（encoding/csv 不去 BOM，否则第一列表头会带 BOM 前缀）
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	r := csv.NewReader(bytes.NewReader(data))
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w：读取表头失败", ErrImportCSVMalformed)
	}
	cols := make([]string, len(header))
	for i, h := range header {
		cols[i] = strings.TrimSpace(h)
	}
	// 必需列校验
	headerSet := make(map[string]bool, len(cols))
	for _, c := range cols {
		headerSet[c] = true
	}
	for _, req := range []string{"sku_id", "sku_code", "currency"} {
		if !headerSet[req] {
			return nil, fmt.Errorf("%w：缺 %s", ErrImportHeaderInvalid, req)
		}
	}

	var rows []importRow
	line := 1 // 表头已读，数据行从 2 开始
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("%w：第 %d 行解析失败", ErrImportCSVMalformed, line)
		}
		fields := make(map[string]string, len(cols))
		for i, c := range cols {
			if i < len(rec) {
				fields[c] = strings.TrimSpace(rec[i])
			} else {
				fields[c] = ""
			}
		}
		// 跳过全空行（所有字段为空）
		empty := true
		for _, v := range fields {
			if v != "" {
				empty = false
				break
			}
		}
		if !empty {
			rows = append(rows, importRow{Line: line, Fields: fields})
		}
	}
	return rows, nil
}

// unknownComponentColumn 识别 multiplier_/price_/official_ 前缀的未知组件列（5c 裁决 3）。
// 命中前缀但 component_type 不在固定 12 个集合 → 返回该列名（ERROR）；
// 固定列（isKnownFixedColumn）与完全无关的自定义列 → 空串（忽略）。
func unknownComponentColumn(col string) string {
	for _, prefix := range []string{"multiplier_", "price_", "official_"} {
		if strings.HasPrefix(col, prefix) {
			ct := strings.TrimPrefix(col, prefix)
			if !componentTypes[ct] {
				return col
			}
			return ""
		}
	}
	_ = isKnownFixedColumn(col) // 固定列显式豁免，保持判定路径单一
	return ""
}

// ---- 预览校验（§6.2，四种填法） ----

// ImportPreviewResult 是预览响应（§6.2 data）。
type ImportPreviewResult struct {
	Token        string              `json:"token"`
	Total        int                 `json:"total"`
	OKCount      int                 `json:"ok_count"`
	WarnCount    int                 `json:"warn_count"`
	ErrorCount   int                 `json:"error_count"`
	Rows         []ImportRowResult   `json:"rows"`
	PreviewItems []ImportPreviewItem `json:"preview_items"`
}

// ImportRowResult 是一行的校验结果。
type ImportRowResult struct {
	Line     int      `json:"line"`
	SkuID    int64    `json:"sku_id"`
	SkuCode  string   `json:"sku_code"`
	Level    string   `json:"level"` // OK / WARN / ERROR
	Messages []string `json:"messages"`
}

// ImportPreviewItem 是可供 confirm 回传的规范明细行（只含 OK+WARN）。
type ImportPreviewItem struct {
	SKUID       int64               `json:"sku_id"`
	Currency    string              `json:"currency"`
	FxTier      *string             `json:"fx_tier"`
	Constraints map[string]any      `json:"constraints"`
	Components  []ImportPreviewComp `json:"components"`
}

// ImportPreviewComp 是预览明细的一个组件。
type ImportPreviewComp struct {
	ComponentType string  `json:"component_type"`
	Multiplier    *string `json:"multiplier"`
	UnitPrice     string  `json:"unit_price"`
}

// ImportStore 是导入链路的仓储接口（GORM 实现见 internal/repo/supplier_import.go）。
type ImportStore interface {
	// ListTemplateSKUs 模板 SKU（scope=active/vendor/family/history）。
	ListTemplateSKUs(ctx context.Context, supplierID int64, q TemplateQuery) ([]TemplateSKU, error)
	// ListComponentTypes 扫描可报价 SKU 的官方价组件并集（排序后，列顺序稳定）。
	ListComponentTypes(ctx context.Context) ([]string, error)
}

// ImportService 是批量导入的领域服务。
type ImportService struct {
	quoteSvc *QuoteService
	store    ImportStore
	now      func() time.Time
}

// NewImportService 构造导入服务。
func NewImportService(quoteSvc *QuoteService, store ImportStore) *ImportService {
	return &ImportService{quoteSvc: quoteSvc, store: store, now: func() time.Time { return time.Now().UTC() }}
}

// fullComponentColumnOrder 返回全部 12 个 component_type 的列顺序（5c 修复）：
// 有官方价的排前面（常用列在前），其余按名称升序补齐。
// 背景：动态列若只取「官方价组件并集」，无官方价 SKU 的组件类型不在并集里，
// 模板就没有对应列——用户无法批量导入这类 SKU（如 embedding 新模型），
// 混进一行就只能手工补，批量导入的意义被削掉。故固定输出全部 12 列。
func fullComponentColumnOrder(withOfficial []string) []string {
	has := make(map[string]bool, len(withOfficial))
	for _, ct := range withOfficial {
		has[ct] = true
	}
	all := make([]string, 0, len(componentTypes))
	for ct := range componentTypes {
		all = append(all, ct)
	}
	sort.Slice(all, func(i, j int) bool {
		if has[all[i]] != has[all[j]] {
			return has[all[i]] // 有官方价的在前
		}
		return all[i] < all[j] // 名称升序，列顺序稳定
	})
	return all
}

// Template 模板下载（§6.1）：动态组件列 + 行数据。
func (s *ImportService) Template(ctx context.Context, supplierID int64, q TemplateQuery) ([]byte, error) {
	if q.Scope == "" {
		q.Scope = "active"
	}
	rows, err := s.store.ListTemplateSKUs(ctx, supplierID, q)
	if err != nil {
		return nil, err
	}
	withOfficial, err := s.store.ListComponentTypes(ctx)
	if err != nil {
		return nil, err
	}
	// 全部 12 个组件类型都出列（无官方价 SKU 也要能导入），有官方价的排前面
	cts := fullComponentColumnOrder(withOfficial)
	return RenderTemplateCSV(&TemplateData{ComponentTypes: cts, Rows: rows})
}

// Preview 上传校验预览（§6.2）：不落库，逐行校验，四种填法。
func (s *ImportService) Preview(ctx context.Context, sup *Supplier, file []byte) (*ImportPreviewResult, error) {
	if len(file) > maxImportFileBytes {
		return nil, ErrImportFileTooLarge
	}
	rows, err := parseImportCSV(file)
	if err != nil {
		return nil, err
	}
	if len(rows) > maxImportRows {
		return nil, ErrImportTooManyRows
	}

	// 收集 sku_id 并批量取可报价 SKU 与官方价
	skuIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		if id, perr := strconv.ParseInt(row.Fields["sku_id"], 10, 64); perr == nil && id > 0 {
			skuIDs = append(skuIDs, id)
		}
	}
	skus, err := s.quoteSvc.store.FindQuotableSKUs(ctx, skuIDs)
	if err != nil {
		return nil, err
	}
	officials, err := s.quoteSvc.store.FindOfficialComponents(ctx, skuIDs)
	if err != nil {
		return nil, err
	}

	token := sha256.Sum256(file)
	res := &ImportPreviewResult{
		Token:        hex.EncodeToString(token[:])[:16],
		Total:        len(rows),
		Rows:         []ImportRowResult{},
		PreviewItems: []ImportPreviewItem{},
	}

	for _, row := range rows {
		rr := ImportRowResult{Line: row.Line, Level: "OK", Messages: []string{}}
		skuID, _ := strconv.ParseInt(row.Fields["sku_id"], 10, 64)
		rr.SkuID = skuID
		// sku_code 以库内为准回填（CSV 里的值可能被用户改错/留空；
		// 前端展示错误行时用户看到的是模型名而不是一串数字）
		if sku, ok := skus[skuID]; ok {
			rr.SkuCode = sku.SkuCode
		} else {
			rr.SkuCode = row.Fields["sku_code"]
		}

		item, msgs, rowErr := s.validateImportRow(row, skus, officials)
		if rowErr != nil {
			rr.Level = "ERROR"
			rr.Messages = append(rr.Messages, rowErr.Error())
		} else {
			rr.Messages = append(rr.Messages, msgs...)
			if len(msgs) > 0 {
				rr.Level = "WARN"
			}
			if item != nil {
				res.PreviewItems = append(res.PreviewItems, *item)
			}
		}

		switch rr.Level {
		case "OK":
			res.OKCount++
		case "WARN":
			res.WarnCount++
		default:
			res.ErrorCount++
		}
		res.Rows = append(res.Rows, rr)
	}
	return res, nil
}

// validateImportRow 校验一行，返回规范明细（供 preview_items）、WARN 消息、ERROR。
// 四种填法（契约 §6.2）：
//
//	两值都传 → 自洽复核（1e-4），不自洽 → ERROR
//	只传 multiplier_<type> → 按当前官方价算出 price（导入主用法）
//	只传 price_<type> → 绝对价模式，multiplier=nil；无官方价 SKU 只能这样填
//	都不传 → 该组件跳过
//
// WARN 只在倍率模式下判定（官方价基准已变动 → 按最新官方价重算并标注）。
func (s *ImportService) validateImportRow(row importRow, skus map[int64]QuotableSKU, officials map[int64][]OfficialComponent) (*ImportPreviewItem, []string, error) {
	// 未知组件列校验（5c 裁决 3：前缀命中但类型未知 → ERROR；无关列忽略）
	for col := range row.Fields {
		if bad := unknownComponentColumn(col); bad != "" {
			return nil, nil, fmt.Errorf("未知组件列：%s", bad)
		}
	}

	skuID, err := strconv.ParseInt(row.Fields["sku_id"], 10, 64)
	if err != nil || skuID <= 0 {
		return nil, nil, fmt.Errorf("sku_id 非法：%q", row.Fields["sku_id"])
	}
	sku, ok := skus[skuID]
	if !ok {
		return nil, nil, fmt.Errorf("SKU 不存在或不可报价")
	}

	// warns 必须先于展示列校验声明——LI-005 的三段校验在 fx_tier 之前追加，
	// 顺序对测试可见性有要求（用户改 sku_code 应该比 fx_tier 提醒更早看到）。
	warns := []string{}

	// 展示列与数据库一致性校验（LI-005）：CSV 里的值与库内不一致时 WARN，不静默忽略。
	// sku_code / model_name / currency 都是只读参考列，CSV 里的值不参与入库，但用户改了应该被告知。
	// 空值不告警（用户没填不是错）；WARN 不阻塞导入。
	if csvSkuCode := strings.TrimSpace(row.Fields["sku_code"]); csvSkuCode != "" && csvSkuCode != sku.SkuCode {
		warns = append(warns, fmt.Sprintf("sku_code 与库内不一致（CSV %q → 库内 %q）", csvSkuCode, sku.SkuCode))
	}
	if csvModelName := strings.TrimSpace(row.Fields["model_name"]); csvModelName != "" && csvModelName != sku.ModelName {
		warns = append(warns, fmt.Sprintf("model_name 与库内不一致（CSV %q → 库内 %q）", csvModelName, sku.ModelName))
	}
	if csvCurrency := strings.TrimSpace(row.Fields["currency"]); csvCurrency != "" && csvCurrency != sku.NativeCurrency {
		warns = append(warns, fmt.Sprintf("currency 与库内不一致（CSV %q → 库内 %q）", csvCurrency, sku.NativeCurrency))
	}

	item := &ImportPreviewItem{SKUID: skuID, Currency: sku.NativeCurrency}

	// 汇率档位（USD 必填，CNY 传了忽略置 null）
	if sku.NativeCurrency == "USD" {
		raw := row.Fields["fx_tier"]
		if raw == "" {
			return nil, nil, fmt.Errorf("USD 模型必须选择汇率档位")
		}
		canonical, ok := normalizeFxTier(raw)
		if !ok {
			return nil, nil, fmt.Errorf("汇率档位不在可选范围内：%q", raw)
		}
		item.FxTier = &canonical
	}

	// 官方价基准（按组件类型索引）
	officialByType := make(map[string]decimal.Decimal)
	for _, oc := range officials[skuID] {
		officialByType[oc.ComponentType] = oc.UnitPrice
	}

	// 逐组件解析（按 componentTypes 固定顺序遍历，保证 preview_items 列序稳定）
	compTypes := make([]string, 0, len(componentTypes))
	for ct := range componentTypes {
		compTypes = append(compTypes, ct)
	}
	sort.Strings(compTypes)

	for _, ct := range compTypes {
		multRaw := row.Fields["multiplier_"+ct]
		priceRaw := row.Fields["price_"+ct]
		officialRaw := row.Fields["official_"+ct]

		if multRaw == "" && priceRaw == "" {
			continue // 该组件不参与
		}

		official, hasOfficial := officialByType[ct]
		pc := ImportPreviewComp{ComponentType: ct}

		switch {
		case multRaw != "" && priceRaw != "":
			// 两值都传：自洽复核
			mult, merr := decimal.NewFromString(multRaw)
			price, perr := decimal.NewFromString(priceRaw)
			if merr != nil || perr != nil {
				return nil, nil, fmt.Errorf("%s：倍率或价格格式非法", ct)
			}
			if !hasOfficial {
				return nil, nil, fmt.Errorf("%s：%w", ct, ErrQuoteMultiplierForbidden)
			}
			if price.Sub(official.Mul(mult)).Abs().Cmp(priceTolerance) > 0 {
				return nil, nil, fmt.Errorf("%s：倍率与折算价不自洽（官方价 %s × %s = %s，实报 %s）",
					ct, official.String(), mult.String(), official.Mul(mult).String(), price.String())
			}
			mStr := mult.String()
			pc.Multiplier = &mStr
			pc.UnitPrice = price.String()
			// 官方价基准已变动 → WARN（只在倍率模式判定）
			// LI-006：解析失败必须 ERROR，绝不静默忽略（前端联调实测改 "not-a-number" 被吞）。
			if officialRaw != "" {
				rowOfficial, rerr := decimal.NewFromString(officialRaw)
				if rerr != nil {
					return nil, nil, fmt.Errorf("%s：官方价格式非法：%q", ct, officialRaw)
				}
				if !rowOfficial.Equal(official) {
					warns = append(warns, fmt.Sprintf("%s：官方价基准已变动（%s → %s），已按最新官方价复核", ct, rowOfficial.String(), official.String()))
				}
			}

		case multRaw != "":
			// 只传倍率：服务端按当前官方价算出 price（导入主用法）
			mult, merr := decimal.NewFromString(multRaw)
			if merr != nil {
				return nil, nil, fmt.Errorf("%s：倍率格式非法：%q", ct, multRaw)
			}
			if !hasOfficial {
				return nil, nil, fmt.Errorf("%s：%w", ct, ErrQuoteMultiplierForbidden)
			}
			price := official.Mul(mult)
			mStr := mult.String()
			pc.Multiplier = &mStr
			pc.UnitPrice = price.String()
			// 官方价基准已变动 → WARN 并重算（重算口径：price = 当前官方价 × multiplier）
			// LI-006：解析失败必须 ERROR，绝不静默忽略。
			if officialRaw != "" {
				rowOfficial, rerr := decimal.NewFromString(officialRaw)
				if rerr != nil {
					return nil, nil, fmt.Errorf("%s：官方价格式非法：%q", ct, officialRaw)
				}
				if !rowOfficial.Equal(official) {
					warns = append(warns, fmt.Sprintf("%s：官方价基准已变动（%s → %s），已按最新官方价重算", ct, rowOfficial.String(), official.String()))
				}
			}

		default:
			// 只传价格：绝对价模式（multiplier=nil；无官方价 SKU 只能这样填）
			price, perr := decimal.NewFromString(priceRaw)
			if perr != nil {
				return nil, nil, fmt.Errorf("%s：价格格式非法：%q", ct, priceRaw)
			}
			pc.UnitPrice = price.String()
			// 绝对价模式：官方价列被改动时 WARN（不影响入库，但用户应知道系统没信任它）。
			// LI-006：解析失败必须 ERROR；hasOfficial=false 时不比对（无基准可比），
			// 但填了非数字仍 ERROR。
			if officialRaw != "" {
				rowOfficial, rerr := decimal.NewFromString(officialRaw)
				if rerr != nil {
					return nil, nil, fmt.Errorf("%s：官方价格式非法：%q", ct, officialRaw)
				}
				if hasOfficial && !rowOfficial.Equal(official) {
					warns = append(warns, fmt.Sprintf("%s：官方价基准已变动（%s → %s），本行按绝对价提交", ct, rowOfficial.String(), official.String()))
				}
			}
		}
		item.Components = append(item.Components, pc)
	}

	if len(item.Components) == 0 {
		return nil, nil, fmt.Errorf("该行未填任何组件价格（multiplier_<type> 或 price_<type> 至少填一个）")
	}

	// 约束列（可选，未知 key 已在表头校验拦掉；这里做类型松散校验）
	item.Constraints = parseImportConstraints(row)

	return item, warns, nil
}

// parseImportConstraints 解析约束 6 列（空值不参与）。
func parseImportConstraints(row importRow) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"rpm", "tpm", "concurrency", "daily_quota", "actual_context"} {
		if v := row.Fields[key]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				out[key] = n
			}
		}
	}
	if v := row.Fields["compatibility"]; v != "" {
		out["compatibility"] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Confirm 确认入库（§6.3）：复用九步校验（不信任前端），仅 source='IMPORT'。
// 不做「必须是 preview_items 的 sku 子集」校验（契约 §6.3 已删：技术上无法闭环，
// 九步校验已拒绝任何非法行；「confirm 的 items 应来自 preview_items」属前端纪律）。
func (s *ImportService) Confirm(ctx context.Context, sup *Supplier, in SubmitQuoteInput, operatorID int64, requestID string) (*SubmitQuoteResult, error) {
	return s.quoteSvc.SubmitImportQuote(ctx, sup, in, operatorID, requestID)
}
