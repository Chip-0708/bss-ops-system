package supplier

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeImportStore 为单测的内存导入仓储。
type fakeImportStore struct {
	templateSKUs   []TemplateSKU
	componentTypes []string
}

func (f *fakeImportStore) ListTemplateSKUs(_ context.Context, _ int64, _ TemplateQuery) ([]TemplateSKU, error) {
	return f.templateSKUs, nil
}

func (f *fakeImportStore) ListComponentTypes(_ context.Context) ([]string, error) {
	return f.componentTypes, nil
}

// baseImportService 返回带 fake 仓储的导入服务（复用 5a-2 的 fakeQuoteStore 做官方价/SKU 校验）。
func baseImportService(qs *fakeQuoteStore, is *fakeImportStore) *ImportService {
	return NewImportService(NewQuoteService(qs), is)
}

// ---- 模板渲染 ----

func TestRenderTemplateCSV_DynamicColumns(t *testing.T) {
	data := &TemplateData{
		ComponentTypes: []string{"cache_write_5m", "cached_input", "input", "output"},
		Rows: []TemplateSKU{
			{ID: 40, SkuCode: "gpt-5-2026-04-11", ModelName: "GPT-5 gpt-5-2026-04-11", NativeCurrency: "USD",
				OfficialPrice: map[string]string{"input": "2.5", "output": "10", "cached_input": "1.25"}},
		},
	}
	b, err := RenderTemplateCSV(data)
	require.NoError(t, err)
	// UTF-8 BOM
	require.Equal(t, []byte{0xEF, 0xBB, 0xBF}, b[:3])
	header := strings.Split(string(b), "\n")[0]
	// 动态列：official_/multiplier_/price_ 按组件类型排序生成
	require.Contains(t, header, "official_cache_write_5m")
	require.Contains(t, header, "official_cached_input")
	require.Contains(t, header, "official_input")
	require.Contains(t, header, "official_output")
	require.Contains(t, header, "multiplier_cache_write_5m")
	require.Contains(t, header, "price_cache_write_5m")
	// valid_from/valid_to 不在 CSV 里（5c 裁决 2），last_valid_to 保留为只读参考
	require.NotContains(t, header, ",valid_from,")
	require.Contains(t, header, "last_valid_to")
}

// TestTemplate_AllTwelveComponentColumns 模板必须包含全部 12 个 component_type 的
// multiplier_/price_ 列（5c 修复：无官方价 SKU 的组件不在官方价并集里，
// 若只按并集出列，这类 SKU 无法通过 CSV 导入）。
func TestTemplate_AllTwelveComponentColumns(t *testing.T) {
	svc := baseImportService(baseStore(), &fakeImportStore{
		componentTypes: []string{"input", "output"}, // 官方价并集只有 2 个
	})
	b, err := svc.Template(context.Background(), 10, TemplateQuery{Scope: "active"})
	require.NoError(t, err)
	header := strings.Split(string(b), "\n")[0]
	for _, ct := range []string{
		"input", "output", "cached_input", "cache_write_5m", "cache_write_1h", "reasoning",
		"embedding", "request", "image_input", "image_output", "audio_input", "audio_output",
	} {
		require.Contains(t, header, "multiplier_"+ct, "缺 multiplier_%s 列", ct)
		require.Contains(t, header, "price_"+ct, "缺 price_%s 列", ct)
	}
	// 有官方价的排前面：input/output 应在 embedding 之前
	require.Less(t, strings.Index(header, "multiplier_input"), strings.Index(header, "multiplier_embedding"))
}

// TestImportPreview_NoOfficialPriceSkuImportable 无官方价 SKU（如 embedding 新模型）
// 能通过 CSV 导入（5c 修复：模板含全部 12 列，price_embedding 列存在）。
func TestImportPreview_NoOfficialPriceSkuImportable(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,price_embedding
14,qwen3-embedding-2026-03,CNY,0.5
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.OKCount)
	require.Len(t, res.PreviewItems, 1)
	require.Equal(t, int64(14), res.PreviewItems[0].SKUID)
	require.Equal(t, "embedding", res.PreviewItems[0].Components[0].ComponentType)
	require.Equal(t, "0.5", res.PreviewItems[0].Components[0].UnitPrice)
	require.Nil(t, res.PreviewItems[0].Components[0].Multiplier)
}

// TestImportPreview_SkuCodeBackfilled preview 的 rows[].sku_code 以库内为准回填
// （CSV 里的值可能被用户改错/留空；前端展示错误行时应看到模型名而不是一串数字）。
func TestImportPreview_SkuCodeBackfilled(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,fx_tier,multiplier_input
12,用户填错的名字,USD,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, "gpt-5-2026-04-11", res.Rows[0].SkuCode, "sku_code 以库内为准回填")
}

// ---- CSV 解析（纯函数） ----

func TestParseImportCSV_BOM(t *testing.T) {
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte("sku_id,sku_code,currency\n40,gpt-5,USD\n")...)
	rows, err := parseImportCSV(withBOM)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(2), int64(rows[0].Line))
	require.Equal(t, "40", rows[0].Fields["sku_id"])

	// 不带 BOM 也能解析
	rows2, err := parseImportCSV([]byte("sku_id,sku_code,currency\n40,gpt-5,USD\n"))
	require.NoError(t, err)
	require.Len(t, rows2, 1)
}

func TestParseImportCSV_MissingRequiredColumn(t *testing.T) {
	_, err := parseImportCSV([]byte("sku_id,sku_code\n40,gpt-5\n"))
	require.ErrorIs(t, err, ErrImportHeaderInvalid)
}

func TestParseImportCSV_Malformed(t *testing.T) {
	_, err := parseImportCSV([]byte("sku_id,sku_code,currency\n40,gpt-5,\"unclosed\n"))
	require.ErrorIs(t, err, ErrImportCSVMalformed)
}

// ---- 预览校验（四种填法） ----

// importCSVFixture 构造一份合法 CSV（1 个 USD SKU + 1 个无官方价 CNY SKU）。
// SKU id 与 baseStore() 对齐：12=gpt-5（USD，有官方价）、14=qwen3-embedding（CNY，无官方价）。
func importCSVFixture() []byte {
	return []byte(`sku_id,sku_code,currency,official_input,official_output,fx_tier,multiplier_input,price_input,multiplier_output,price_output,price_embedding,rpm,tpm
12,gpt-5-2026-04-11,USD,2.5,10,6.8,0.8,,0.75,,,3000,2000000
14,qwen3-embedding-2026-03,CNY,,,,,,,,0.5,,
`)
}

func TestImportPreview_OK(t *testing.T) {
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, importCSVFixture())
	require.NoError(t, err)
	require.Equal(t, 2, res.Total)
	require.Equal(t, 2, res.OKCount)
	require.Equal(t, 0, res.ErrorCount)
	require.Len(t, res.PreviewItems, 2)
	// 第 1 行：input/output 两值都传且自洽 → OK
	require.Equal(t, int64(12), res.PreviewItems[0].SKUID)
	require.Equal(t, "6.8", *res.PreviewItems[0].FxTier)
	// 第 2 行：无官方价 SKU 只填 price_embedding → 绝对价
	require.Equal(t, int64(14), res.PreviewItems[1].SKUID)
	require.Nil(t, res.PreviewItems[1].FxTier)
	require.Equal(t, "0.5", res.PreviewItems[1].Components[0].UnitPrice)
	require.Nil(t, res.PreviewItems[1].Components[0].Multiplier)
	// 约束列
	require.Equal(t, int64(3000), res.PreviewItems[0].Constraints["rpm"])
}

func TestImportValidate_OnlyMultiplierAutoPrice(t *testing.T) {
	// 只填倍率 → 服务端按当前官方价算出 price（导入主用法）
	csvData := []byte(`sku_id,sku_code,currency,fx_tier,multiplier_input,multiplier_output
12,gpt-5-2026-04-11,USD,6.8,0.8,0.75
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.OKCount)
	require.Len(t, res.PreviewItems, 1)
	// price = official × multiplier：input 2.5×0.8=2，output 10×0.75=7.5
	comps := res.PreviewItems[0].Components
	require.Len(t, comps, 2)
	require.Equal(t, "input", comps[0].ComponentType)
	require.Equal(t, "0.8", *comps[0].Multiplier)
	require.Equal(t, "2", comps[0].UnitPrice)
	require.Equal(t, "output", comps[1].ComponentType)
	require.Equal(t, "7.5", comps[1].UnitPrice)
}

func TestImportValidate_AbsolutePriceNoWarn(t *testing.T) {
	// 只填价格 + official 列填了与库内一致的值 → 无 WARN（LI-006 后行为：
	// 一致时不 WARN；不一致时才 WARN；解析失败 ERROR——本用例是「一致」路径）。
	csvData := []byte(`sku_id,sku_code,currency,official_input,fx_tier,price_input
12,gpt-5-2026-04-11,USD,2.5,6.8,2.2
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.OKCount)
	require.Equal(t, 0, res.WarnCount, "official 与库内一致 → 不触发 WARN")
	require.Nil(t, res.PreviewItems[0].Components[0].Multiplier)
	require.Equal(t, "2.2", res.PreviewItems[0].Components[0].UnitPrice)
}

// TestImportValidate_AbsolutePriceOfficialChangedWarn LI-006：绝对价模式下官方价
// 列被改成与库内不一致的数字 → WARN（提示"本行按绝对价提交"）。
func TestImportValidate_AbsolutePriceOfficialChangedWarn(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,official_input,fx_tier,price_input
12,gpt-5-2026-04-11,USD,9.99,6.8,2.2
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.WarnCount, "绝对价模式 + 官方价列被改动 → WARN")
	require.Contains(t, res.Rows[0].Messages[0], "官方价基准已变动")
	require.Contains(t, res.Rows[0].Messages[0], "本行按绝对价提交")
	// 绝对价模式：multiplier 仍然为 nil（WARN 不改变入库语义）
	require.Nil(t, res.PreviewItems[0].Components[0].Multiplier)
	require.Equal(t, "2.2", res.PreviewItems[0].Components[0].UnitPrice)
}

func TestImportValidate_WarnRecalculated(t *testing.T) {
	// 倍率模式 + 行的 official 列与当前官方价不一致 → WARN 并按最新官方价重算
	csvData := []byte(`sku_id,sku_code,currency,official_input,fx_tier,multiplier_input
12,gpt-5-2026-04-11,USD,9.99,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.WarnCount)
	require.Contains(t, res.Rows[0].Messages[0], "官方价基准已变动")
	// 按最新官方价重算：2.5 × 0.8 = 2
	require.Equal(t, "2", res.PreviewItems[0].Components[0].UnitPrice)
}

func TestImportValidate_ErrorRowRejected(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,fx_tier,multiplier_input
999,not-exist,USD,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.ErrorCount)
	require.Equal(t, "ERROR", res.Rows[0].Level)
	require.Contains(t, res.Rows[0].Messages[0], "SKU 不存在或不可报价")
	require.Empty(t, res.PreviewItems, "ERROR 行不得入 preview_items")
}

func TestImportValidate_PriceInconsistentError(t *testing.T) {
	// 两值都传但不自洽 → ERROR
	csvData := []byte(`sku_id,sku_code,currency,fx_tier,multiplier_input,price_input
12,gpt-5-2026-04-11,USD,6.8,0.8,9.99
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.ErrorCount)
	require.Contains(t, res.Rows[0].Messages[0], "不自洽")
}

func TestImportValidate_UnknownComponentColumnError(t *testing.T) {
	// multiplier_/price_ 前缀 + 未知 component_type → ERROR（5c 裁决 3）
	csvData := []byte(`sku_id,sku_code,currency,fx_tier,multiplier_magic
12,gpt-5-2026-04-11,USD,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.ErrorCount)
	require.Contains(t, res.Rows[0].Messages[0], "未知组件列")
}

func TestImportValidate_UnrelatedColumnIgnored(t *testing.T) {
	// 无关列（用户自加的备注列）→ 忽略，不报错（5c 裁决 3）
	csvData := []byte(`sku_id,sku_code,currency,fx_tier,multiplier_input,memo
12,gpt-5-2026-04-11,USD,6.8,0.8,我自己的备注
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.OKCount)
}

func TestImportValidate_MultiplierWithoutOfficialPriceError(t *testing.T) {
	// 无官方价 SKU 只填倍率 → ERROR（倍率模式不可用）
	csvData := []byte(`sku_id,sku_code,currency,multiplier_embedding
14,qwen3-embedding-2026-03,CNY,0.5
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.ErrorCount)
	require.Contains(t, res.Rows[0].Messages[0], "绝对价")
}

func TestImportValidate_RowLimitExceeded(t *testing.T) {
	// 501 数据行 → ErrImportTooManyRows
	var sb strings.Builder
	sb.WriteString("sku_id,sku_code,currency\n")
	for i := 1; i <= 501; i++ {
		sb.WriteString("40,gpt-5,USD\n")
	}
	svc := baseImportService(baseStore(), &fakeImportStore{})
	_, err := svc.Preview(context.Background(), testSupplier, []byte(sb.String()))
	require.ErrorIs(t, err, ErrImportTooManyRows)
}

func TestImportValidate_FileTooLarge(t *testing.T) {
	svc := baseImportService(baseStore(), &fakeImportStore{})
	_, err := svc.Preview(context.Background(), testSupplier, make([]byte, maxImportFileBytes+1))
	require.ErrorIs(t, err, ErrImportFileTooLarge)
}

func TestImportValidate_USDRequiresFxTier(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,multiplier_input
12,gpt-5-2026-04-11,USD,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.ErrorCount)
	require.Contains(t, res.Rows[0].Messages[0], "汇率档位")
}

func TestImportValidate_NoComponentFilledError(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,fx_tier
12,gpt-5-2026-04-11,USD,6.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.ErrorCount)
	require.Contains(t, res.Rows[0].Messages[0], "未填任何组件价格")
}

// ---- confirm ----

func TestImportConfirm_SourceIsImport(t *testing.T) {
	store := baseStore()
	qsvc := NewQuoteService(store)
	svc := NewImportService(qsvc, &fakeImportStore{})
	in := SubmitQuoteInput{
		ValidFrom: time.Now().UTC().Add(24 * time.Hour),
		ValidTo:   time.Now().UTC().Add(90 * 24 * time.Hour),
		Items: []SubmitQuoteItem{
			{SKUID: 12, FxTier: strP("6.8"), Components: []SubmitQuoteComponent{
				{ComponentType: "input", Multiplier: strP("0.8"), UnitPrice: "2.0"},
			}},
		},
	}
	_, err := svc.Confirm(context.Background(), testSupplier, in, 77, "req-imp-1")
	require.NoError(t, err)
	require.Equal(t, "IMPORT", store.createParams.Source, "confirm 落库 source 必须是 IMPORT")
}

func TestImportConfirm_ReusesSubmitValidation(t *testing.T) {
	// confirm 同样走九步校验：非终态互斥 → 409
	store := baseStore()
	store.nonTerminal = true
	svc := NewImportService(NewQuoteService(store), &fakeImportStore{})
	in := SubmitQuoteInput{
		ValidFrom: time.Now().UTC().Add(24 * time.Hour),
		ValidTo:   time.Now().UTC().Add(90 * 24 * time.Hour),
		Items: []SubmitQuoteItem{
			{SKUID: 12, FxTier: strP("6.8"), Components: []SubmitQuoteComponent{
				{ComponentType: "input", Multiplier: strP("0.8"), UnitPrice: "2.0"},
			}},
		},
	}
	_, err := svc.Confirm(context.Background(), testSupplier, in, 77, "req-imp-2")
	require.ErrorIs(t, err, ErrQuoteConflict)
}

// ---- LI-005 + LI-006（CSV 导入校验补强） ----

// TestImportValidate_DisplayColumnsConsistent 展示列与库内一致 → 不告警（回归基线）。
func TestImportValidate_DisplayColumnsConsistent(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,model_name,currency,fx_tier,multiplier_input
12,gpt-5-2026-04-11,OpenAI gpt-5-2026-04-11,USD,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.OKCount, "三个展示列全部一致 → 不告警")
	require.Equal(t, 0, res.WarnCount)
}

// TestImportValidate_SkuCodeMismatchWarn LI-005：sku_code 与库内不一致 → WARN。
func TestImportValidate_SkuCodeMismatchWarn(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,fx_tier,multiplier_input
12,typo-wrong-sku-code,USD,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.WarnCount, "sku_code 不一致 → WARN")
	require.Contains(t, res.Rows[0].Messages[0], "sku_code 与库内不一致")
	require.Contains(t, res.Rows[0].Messages[0], "typo-wrong-sku-code")
	require.Contains(t, res.Rows[0].Messages[0], "gpt-5-2026-04-11")
}

// TestImportValidate_ModelNameMismatchWarn LI-005：model_name 与库内不一致 → WARN。
func TestImportValidate_ModelNameMismatchWarn(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,model_name,currency,fx_tier,multiplier_input
12,gpt-5-2026-04-11,Wrong Vendor gpt-5-2026-04-11,USD,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.WarnCount, "model_name 不一致 → WARN")
	require.Contains(t, res.Rows[0].Messages[0], "model_name 与库内不一致")
}

// TestImportValidate_CurrencyMismatchWarn LI-005：currency 与库内不一致 → WARN。
func TestImportValidate_CurrencyMismatchWarn(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,fx_tier,multiplier_input
12,gpt-5-2026-04-11,CNY,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.WarnCount, "currency 不一致 → WARN")
	require.Contains(t, res.Rows[0].Messages[0], "currency 与库内不一致")
}

// TestImportValidate_SkuCodeEmptyNoWarn LI-005：sku_code 为空 → 不告警（用户没填不是错）。
func TestImportValidate_SkuCodeEmptyNoWarn(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,fx_tier,multiplier_input
12,,USD,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.OKCount, "sku_code 空 → 不告警")
	require.Equal(t, 0, res.WarnCount)
}

// TestImportValidate_MultiplierOfficialInvalidError LI-006：倍率模式官方价
// 列填非数字 → ERROR（绝不静默忽略）。
func TestImportValidate_MultiplierOfficialInvalidError(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,official_input,fx_tier,multiplier_input
12,gpt-5-2026-04-11,USD,not-a-number,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.ErrorCount, "官方价解析失败必须 ERROR")
	require.Contains(t, res.Rows[0].Messages[0], "官方价格式非法")
	require.Contains(t, res.Rows[0].Messages[0], "not-a-number")
}

// TestImportValidate_MultiplierOfficialMismatchWarn LI-006：倍率模式官方价
// 列与库内不一致 → WARN（已存在 TestImportValidate_WarnRecalculated，这里用
// 两值都传的分支覆盖另一个 WARN 路径）。
func TestImportValidate_MultiplierOfficialMismatchWarn(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,official_input,fx_tier,multiplier_input,price_input
12,gpt-5-2026-04-11,USD,2.25,6.8,0.8,2
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.WarnCount, "倍率模式 + 官方价不一致 → WARN")
	require.Contains(t, res.Rows[0].Messages[0], "官方价基准已变动")
	require.Contains(t, res.Rows[0].Messages[0], "2.25")
	require.Contains(t, res.Rows[0].Messages[0], "2.5")
}

// TestImportValidate_BothValueOfficialInvalidError LI-006：两值都传（倍率+价格）
// 且 official 列填非数字 → ERROR（绝不静默忽略）。
func TestImportValidate_BothValueOfficialInvalidError(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,official_input,fx_tier,multiplier_input,price_input
12,gpt-5-2026-04-11,USD,not-a-number,6.8,0.8,2
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.ErrorCount, "两值都传 + 官方价解析失败必须 ERROR")
	require.Contains(t, res.Rows[0].Messages[0], "官方价格式非法")
	require.Contains(t, res.Rows[0].Messages[0], "not-a-number")
}

// TestImportValidate_MultiplierOfficialMatchNoWarn LI-006：倍率模式官方价
// 列与库内一致 → 不告警。
func TestImportValidate_MultiplierOfficialMatchNoWarn(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,official_input,fx_tier,multiplier_input
12,gpt-5-2026-04-11,USD,2.5,6.8,0.8
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.OKCount, "官方价一致 → 不告警")
	require.Equal(t, 0, res.WarnCount)
}

// TestImportValidate_AbsoluteOfficialInvalidError LI-006：绝对价模式官方价
// 列填非数字 → ERROR（绝不静默忽略）。
func TestImportValidate_AbsoluteOfficialInvalidError(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,official_input,fx_tier,price_input
12,gpt-5-2026-04-11,USD,not-a-number,6.8,2.2
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.ErrorCount, "绝对价模式官方价解析失败必须 ERROR")
	require.Contains(t, res.Rows[0].Messages[0], "官方价格式非法")
	require.Contains(t, res.Rows[0].Messages[0], "not-a-number")
}

// TestImportValidate_AbsoluteOfficialEmptyNoWarn LI-006：绝对价模式官方价列为空
// → 不告警（无输入可比）。
func TestImportValidate_AbsoluteOfficialEmptyNoWarn(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,official_input,fx_tier,price_input
12,gpt-5-2026-04-11,USD,,6.8,2.2
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.OKCount, "官方价列为空 → 不告警")
	require.Equal(t, 0, res.WarnCount)
}

// TestImportValidate_AbsoluteOfficialNoBaselineNoWarn LI-006：绝对价模式且该 SKU
// 无官方价（hasOfficial=false）时，official_* 列填了合法数字也不告警（无基准可比）。
func TestImportValidate_AbsoluteOfficialNoBaselineNoWarn(t *testing.T) {
	csvData := []byte(`sku_id,sku_code,currency,official_embedding,price_embedding
14,qwen3-embedding-2026-03,CNY,9.99,0.5
`)
	svc := baseImportService(baseStore(), &fakeImportStore{})
	res, err := svc.Preview(context.Background(), testSupplier, csvData)
	require.NoError(t, err)
	require.Equal(t, 1, res.OKCount, "无官方价 SKU：official 列填值也不告警")
	require.Equal(t, 0, res.WarnCount)
}
