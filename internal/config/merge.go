package config

import (
	"fmt"
	"reflect"
)

// Merge 按固定顺序合并多层配置：索引越小优先级越低，后面的层覆盖前面的层。
// 同名嵌套 map 递归合并；类型冲突（如一层是 string、另一层是 map）不静默覆盖，
// 记录 Reject 并保留原值，由调用方决定是否拒绝整体渲染。
func Merge(layers ...Layer) (Layer, []Reject) {
	out := Layer{}
	var rejects []Reject
	for i, l := range layers {
		r := mergeInto(out, l, fmt.Sprintf("layer[%d]", i), "")
		rejects = append(rejects, r...)
	}
	return out, rejects
}

// mergeInto 把 src 深合并进 dst（就地修改 dst），path 为当前字段路径。
func mergeInto(dst, src Layer, layerName, path string) []Reject {
	var rejects []Reject
	for k, sv := range src {
		field := k
		if path != "" {
			field = path + "." + k
		}
		dv, exists := dst[k]
		if !exists || dv == nil || sv == nil {
			dst[k] = sv
			continue
		}
		dMap, dIsMap := dv.(map[string]any)
		sMap, sIsMap := sv.(map[string]any)
		switch {
		case dIsMap && sIsMap:
			rejects = append(rejects, mergeInto(Layer(dMap), Layer(sMap), layerName, field)...)
		case dIsMap != sIsMap || reflect.TypeOf(dv) != reflect.TypeOf(sv):
			rejects = append(rejects, Reject{
				Field:  field,
				Reason: ReasonTypeConflict,
				Detail: fmt.Sprintf("%s 提供 %T，与已有值 %T 类型不一致，已保留原值", layerName, sv, dv),
			})
		default:
			dst[k] = sv
		}
	}
	return rejects
}

// Render 先按 Merge 合并各层，再按 schema 补默认值并校验必填与取值范围。
// 旧配置缺少后来新增的字段时，按 schema 中的 Default 渲染，不会直接失败。
func Render(schema Schema, layers ...Layer) (Layer, []Reject) {
	merged, rejects := Merge(layers...)
	for field, spec := range schema {
		v, exists := merged[field]
		if !exists || v == nil {
			if spec.Default != nil {
				merged[field] = spec.Default
				continue
			}
			if spec.Required {
				rejects = append(rejects, Reject{
					Field:  field,
					Reason: ReasonMissingRequired,
					Detail: "必填字段缺失且无默认值",
				})
			}
			continue
		}
		if spec.Min != nil || spec.Max != nil {
			n, ok := toFloat(v)
			if !ok {
				rejects = append(rejects, Reject{
					Field:  field,
					Reason: ReasonTypeConflict,
					Detail: fmt.Sprintf("期望数值类型，实际为 %T", v),
				})
				continue
			}
			if spec.Min != nil && n < *spec.Min {
				rejects = append(rejects, Reject{
					Field:  field,
					Reason: ReasonOutOfRange,
					Detail: fmt.Sprintf("值 %v 小于下限 %v", n, *spec.Min),
				})
			}
			if spec.Max != nil && n > *spec.Max {
				rejects = append(rejects, Reject{
					Field:  field,
					Reason: ReasonOutOfRange,
					Detail: fmt.Sprintf("值 %v 大于上限 %v", n, *spec.Max),
				})
			}
		}
	}
	return merged, rejects
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
