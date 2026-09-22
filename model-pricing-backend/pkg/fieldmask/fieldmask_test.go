package fieldmask

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// quoteDTO 模拟报价/成本类返回 DTO，红线字段私有，嵌套类型丰富。
type quoteDTO struct {
	ID       int64        `json:"id"`
	SKU      string       `json:"sku"`
	Cost     *costDTO     `json:"cost,omitempty"`
	Margin   string       `json:"margin,omitempty"`
	Baseline *baselineDTO `json:"baseline,omitempty"`
	Labels   []string     `json:"labels"`
	Meta     MetaMap      `json:"meta"`
}

type costDTO struct {
	UnitCost    string   `json:"unit_cost"`
	ChannelRate string   `json:"channel_rate,omitempty"`
	LossRate    string   `json:"loss_rate,omitempty"`
	Extras      []string `json:"extras"`
}

type baselineDTO struct {
	Version int    `json:"version"`
	Formula string `json:"formula,omitempty"`
}

// MetaMap 测试 map[string]any 含红线键时整条删除。
type MetaMap map[string]interface{}

func mustJSON(t *testing.T, v any) map[string]interface{} {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func toJSONValue(t *testing.T, v any) interface{} {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var m interface{}
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func TestApply_NilMaskReturnsOriginal(t *testing.T) {
	v := &quoteDTO{ID: 1, SKU: "GPT-5", Margin: "20%"}
	out := Apply(v, nil)
	require.Same(t, v, out, "nil mask should return original value")
	out2 := Apply(v, []string{})
	require.Same(t, v, out2, "empty mask should return original value")
}

func TestApply_DeleteRedlineKeysFromTopLevelStruct(t *testing.T) {
	d := &quoteDTO{
		ID:     1,
		SKU:    "GPT-5",
		Cost:   &costDTO{UnitCost: "1.0", ChannelRate: "0.5%"},
		Margin: "20%",
		Baseline: &baselineDTO{
			Version: 1,
			Formula: "(1+r)",
		},
	}
	out := Apply(d, []string{"cost", "margin", "baseline"})
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(b), `"cost"`)
	require.NotContains(t, string(b), `"margin"`)
	require.NotContains(t, string(b), `"baseline"`)
	require.Contains(t, string(b), `"id":1`)
	require.Contains(t, string(b), `"sku":"GPT-5"`)
}

func TestApply_DeleteRedlineKeysFromNestedMapAndSlice(t *testing.T) {
	payload := map[string]interface{}{
		"cost":     "10",
		"baseline": map[string]string{"version": "1"},
		"items": []interface{}{
			map[string]interface{}{"name": "a", "margin": "high"},
			map[string]interface{}{"name": "b", "margin": "low"},
		},
		"nested": map[string]interface{}{
			"cost": "hidden",
			"inner": map[string]interface{}{
				"baseline": "secret",
			},
		},
	}
	out := Apply(payload, []string{"cost", "margin", "baseline"})
	m := mustJSON(t, out)

	require.NotContains(t, m, "cost")
	require.NotContains(t, m, "baseline")
	require.NotContains(t, m, "margin")

	items, ok := m["items"].([]interface{})
	require.True(t, ok)
	require.Len(t, items, 2)
	for i, it := range items {
		itm, ok := it.(map[string]interface{})
		require.Truef(t, ok, "item %d should be a map", i)
		require.NotContains(t, itm, "margin")
		require.Contains(t, itm, "name")
	}

	nested, ok := m["nested"].(map[string]interface{})
	require.True(t, ok)
	require.NotContains(t, nested, "cost")
	inner, ok := nested["inner"].(map[string]interface{})
	require.True(t, ok)
	require.NotContains(t, inner, "baseline")
}

func TestApply_StructWithoutOmitEmptyTagStillRemoved(t *testing.T) {
	// 红线：字段即使没标 omitempty，命中 mask 后也必须从 JSON 中彻底消失。
	type S struct {
		Name   string `json:"name"`
		Cost   string `json:"cost"`   // 故意不加 omitempty
		Margin string `json:"margin"` // 故意不加 omitempty
	}
	out := Apply(S{Name: "ok", Cost: "x", Margin: "y"}, []string{"cost", "margin"})
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(b), "cost")
	require.NotContains(t, string(b), "margin")
	require.Contains(t, string(b), `"name":"ok"`)
}

func TestApply_StructWithJsonOmitEmpty(t *testing.T) {
	type S struct {
		Name   string `json:"name"`
		Cost   string `json:"cost,omitempty"`
		Margin string `json:"margin"`
	}
	out := Apply(S{Name: "ok", Cost: "x", Margin: "y"}, []string{"cost", "margin"})
	m := mustJSON(t, out)
	require.Equal(t, "ok", m["name"])
	require.NotContains(t, m, "cost")
	require.NotContains(t, m, "margin")
}

func TestApply_SliceOfStructs(t *testing.T) {
	in := []quoteDTO{
		{ID: 1, SKU: "a", Cost: &costDTO{UnitCost: "1"}},
		{ID: 2, SKU: "b", Margin: "5"},
	}
	out := Apply(in, []string{"cost", "margin"})
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(b), "cost")
	require.NotContains(t, string(b), "margin")
	require.Contains(t, string(b), `"id":2`)
}

func TestApply_ArrayOfMaps(t *testing.T) {
	arr := [2]map[string]interface{}{
		{"cost": "x", "value": 1},
		{"baseline": "y", "value": 2},
	}
	out := Apply(arr, []string{"cost", "baseline"})
	v := toJSONValue(t, out)
	array, ok := v.([]interface{})
	require.True(t, ok)
	require.Len(t, array, 2)
}

func TestApply_NonStringKeyedMapNormalizedToString(t *testing.T) {
	in := map[int]string{42: "answer"}
	out := Apply(in, []string{"cost"})
	m := mustJSON(t, out)
	require.Equal(t, "answer", m["42"])
}

func TestApply_DeepSliceOfMapsRedlineRemoved(t *testing.T) {
	in := map[string]interface{}{
		"rows": []interface{}{
			map[string]interface{}{"cost": 1, "v": 1},
			map[string]interface{}{"cost": 2, "v": 2},
		},
	}
	out := Apply(in, []string{"cost"})
	m := mustJSON(t, out)
	rows, ok := m["rows"].([]interface{})
	require.True(t, ok)
	require.Len(t, rows, 2)
	for _, r := range rows {
		rm, ok := r.(map[string]interface{})
		require.True(t, ok)
		require.NotContains(t, rm, "cost")
	}
}

func TestApply_NilInputs(t *testing.T) {
	require.Nil(t, Apply(nil, []string{"cost"}))
	var s *quoteDTO
	require.Nil(t, Apply(s, []string{"cost"}))
	var m map[string]interface{}
	require.Nil(t, Apply(m, []string{"cost"}))
	var slice []string
	require.Nil(t, Apply(slice, []string{"cost"}), "nil slice should yield nil via marshal/unmarshal")
}

func TestApply_MapStringAnyTypePreserved(t *testing.T) {
	in := map[string]interface{}{
		"name": "x",
		"cost": "y",
	}
	out := Apply(in, []string{"cost"})
	m, ok := out.(map[string]interface{})
	require.True(t, ok)
	require.NotContains(t, m, "cost")
	require.Equal(t, "x", m["name"])
}

func TestApply_StructPointerTypePreserved(t *testing.T) {
	in := &quoteDTO{ID: 5, SKU: "x", Cost: &costDTO{UnitCost: "1"}}
	out := Apply(in, []string{"cost"})
	m := mustJSON(t, out)
	require.NotContains(t, m, "cost")
	require.Equal(t, float64(5), m["id"])
	require.Equal(t, "x", m["sku"])
}

func TestApply_UnsupportedTypesReturnUnchanged(t *testing.T) {
	// 不可 JSON 序列化的值（chan/func）保守返回 nil，不 panic。
	require.Nil(t, Apply(make(chan int), []string{"cost"}))
	// 基本类型序列化后无字段可删，等价于原值。
	require.Equal(t, float64(123), toJSONValue(t, Apply(123, []string{"cost"})))
	require.Equal(t, "text", toJSONValue(t, Apply("text", []string{"cost"})))
	require.Equal(t, true, toJSONValue(t, Apply(true, []string{"cost"})))
}

func TestApply_StructUsesJsonTagNames(t *testing.T) {
	type S struct {
		InternalCost string `json:"my_cost"`
	}
	out := Apply(S{InternalCost: "a"}, []string{"my_cost"})
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(b), "my_cost")
}

func TestApply_LargeInt64Preserved(t *testing.T) {
	// 超过 2^53 的 int64 主键在 json.Unmarshal 到 float64 时会失真；
	// 使用 UseNumber 后应原样往返。
	type S struct {
		ID   int64  `json:"id"`
		Cost string `json:"cost"`
	}
	const big = int64(9007199254740993) // 2^53 + 1
	out := Apply(S{ID: big, Cost: "x"}, []string{"cost"})
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(b), `"id":9007199254740993`)
	require.NotContains(t, string(b), "cost")
}
