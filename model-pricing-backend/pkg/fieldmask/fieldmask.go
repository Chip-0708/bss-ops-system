// Package fieldmask 按 json tag 递归剔除敏感字段。
//
// 实现方式（确定性物理删除）：输入对象先序列化为 JSON，再解析为通用 map，
// 随后递归删除 fieldMask 中命名的全部键。只要某个键命中了 mask，
// 无论原字段是否标记 omitempty，输出 JSON 中该键都**不存在**。
//
// 使用：data（任意可 JSON 序列化对象）→ map[string]interface{}（由 response 层直接输出）。
// 返回值统一为 map[string]interface{}；输入为 nil 时返回 nil。
package fieldmask

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Apply 返回删除了 mask 中全部键的通用 map（或 nil）。
func Apply(data interface{}, fieldMask []string) interface{} {
	if len(fieldMask) == 0 || data == nil {
		return data
	}

	b, err := json.Marshal(data)
	if err != nil {
		// 序列化失败（如 chan/func 等不可 JSON 类型）：保守起见返回 nil。
		return nil
	}

	// 使用 UseNumber 解析，避免大整数（如 int64 主键 > 2^53）被转成 float64 而失真。
	var root interface{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil
	}

	mask := make(map[string]struct{}, len(fieldMask))
	for _, k := range fieldMask {
		k = strings.TrimSpace(k)
		if k != "" {
			mask[k] = struct{}{}
		}
	}
	if len(mask) == 0 {
		return root
	}
	return prune(root, mask)
}

// prune 递归遍历 JSON 值，删除所有 map 中命中的键。
func prune(v interface{}, mask map[string]struct{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			if _, drop := mask[k]; drop {
				continue // 物理删除：键完全不进入结果
			}
			out[k] = prune(val, mask)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = prune(val, mask)
		}
		return out
	default:
		return v
	}
}
