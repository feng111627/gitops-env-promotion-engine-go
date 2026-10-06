package render

import (
	"fmt"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

// 分层与合并规则（固定优先级，自低到高）：
//
//  1. default ：schema 中声明的新增字段默认值。旧配置缺少后来新增的字段时，
//     按该层的确定默认值渲染，绝不因缺字段而失败。
//  2. base    ：基础配置，所有环境共享。
//  3. overlay ：环境覆盖层 configs/overlays/<env>.json，同名字段精确覆盖基础配置。
//
// 合并语义：
//   - 对象（map）按键深度合并；
//   - 数组整体替换，不做下标级合并（固定且可解释）；
//   - 标量同类型时 overlay 覆盖 base；
//   - 两层同名字段类型不一致时拒绝该覆盖（保留 base 值并记 TYPE_MISMATCH），
//     不会被静默覆盖或丢弃；
//   - 显式 null 视为“未设置”，忽略该层取值（低优先级层生效）。
type Loader interface {
	LoadBase() (map[string]any, error)
	LoadOverlay(env string) (map[string]any, error)
}

// Merge 按 default < base < overlay 的固定顺序分层合并。
// 返回渲染结果（含每个叶子字段的来源层）以及一次性汇总的拒绝原因。
func Merge(env string, loader Loader, schema *model.Schema) (*model.RenderedConfig, model.Rejections) {
	out := &model.RenderedConfig{
		Env:     env,
		Values:  map[string]any{},
		Origins: map[string]model.FieldOrigin{},
	}

	base, err := loader.LoadBase()
	if err != nil {
		return out, model.Rejections{{Code: model.ReasonNotFound, Message: fmt.Sprintf("load base: %v", err)}}
	}
	overlay, err := loader.LoadOverlay(env)
	if err != nil {
		return out, model.Rejections{{Code: model.ReasonNotFound, Env: env, Message: fmt.Sprintf("load overlay: %v", err)}}
	}

	rejections := model.Rejections{}
	merged := deepMerge("", base, overlay, out.Origins, &rejections)
	values, ok := merged.(map[string]any)
	if !ok && merged != nil {
		// 根节点理论上始终是对象。
		rejections = append(rejections, model.RejectReason{
			Code: model.ReasonTypeMismatch, Env: env, Field: "$",
			Message: "root config must be an object",
		})
		values = map[string]any{}
	}
	if values == nil {
		values = map[string]any{}
	}

	if schema != nil {
		applyDefaultsAndTypes(env, values, out.Origins, schema, &rejections)
	}

	out.Values = values
	return out, rejections
}

// deepMerge 深度合并 base 与 overlay，并记录每个叶子字段的来源层。
func deepMerge(path string, base, overlay any, origins map[string]model.FieldOrigin, rej *model.Rejections) any {
	// 显式 null 视为未设置。
	if overlay == nil {
		if base != nil {
			markLeaves(path, base, "base", origins)
		}
		return base
	}
	if base == nil {
		markLeaves(path, overlay, "overlay", origins)
		return overlay
	}

	bm, bIsMap := base.(map[string]any)
	om, oIsMap := overlay.(map[string]any)
	if bIsMap && oIsMap {
		keys := map[string]struct{}{}
		for k := range bm {
			keys[k] = struct{}{}
		}
		for k := range om {
			keys[k] = struct{}{}
		}
		result := make(map[string]any, len(keys))
		for k := range keys {
			result[k] = deepMerge(joinPath(path, k), bm[k], om[k], origins, rej)
		}
		return result
	}

	bt, ot := jsonType(base), jsonType(overlay)
	if bt != ot {
		*rej = append(*rej, model.RejectReason{
			Code:    model.ReasonTypeMismatch,
			Field:   path,
			Message: fmt.Sprintf("base expects %s but overlay provides %s; overlay rejected, base value kept", bt, ot),
		})
		markLeaves(path, base, "base", origins)
		return base
	}

	// 同类型：数组整体替换；标量 overlay 覆盖。
	markLeaves(path, overlay, "overlay", origins)
	return overlay
}

// markLeaves 递归记录值中所有叶子字段的来源层（数组/标量为叶子）。
func markLeaves(path string, v any, layer string, origins map[string]model.FieldOrigin) {
	if path == "" {
		return
	}
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			markLeaves(joinPath(path, k), val, layer, origins)
		}
		return
	}
	origins[path] = model.FieldOrigin{Path: path, Layer: layer}
}

// applyDefaultsAndTypes 按 schema 补齐新增字段默认值，并对已存在字段做类型校验。
func applyDefaultsAndTypes(env string, values map[string]any, origins map[string]model.FieldOrigin, schema *model.Schema, rej *model.Rejections) {
	for _, f := range schema.Fields {
		v, found := lookupPath(values, f.Path)
		if !found || v == nil {
			dv := cloneValue(f.Default)
			if err := setPath(values, f.Path, dv); err != nil {
				*rej = append(*rej, model.RejectReason{
					Code: model.ReasonTypeMismatch, Env: env, Field: f.Path,
					Message: fmt.Sprintf("cannot apply default: %v", err),
				})
				continue
			}
			origins[f.Path] = model.FieldOrigin{Path: f.Path, Layer: "default"}
			continue
		}
		if got := jsonType(v); got != f.Type {
			*rej = append(*rej, model.RejectReason{
				Code: model.ReasonTypeMismatch, Env: env, Field: f.Path,
				Message: fmt.Sprintf("schema expects %s but got %s", f.Type, got),
			})
		}
	}
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// jsonType 返回可解释的 JSON 类型名。
func jsonType(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "bool"
	case float32, float64, int, int8, int16, int32, int64, uint, uintptr:
		return "number"
	default:
		return "unknown"
	}
}

func lookupPath(values map[string]any, path string) (any, bool) {
	var cur any = values
	for _, seg := range splitPath(path) {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(values map[string]any, path string, v any) error {
	segs := splitPath(path)
	cur := values
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg]
		if !ok || next == nil {
			m := map[string]any{}
			cur[seg] = m
			cur = m
			continue
		}
		m, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("segment %q is not an object", seg)
		}
		cur = m
	}
	cur[segs[len(segs)-1]] = v
	return nil
}

func splitPath(path string) []string {
	var out []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			out = append(out, path[start:i])
			start = i + 1
		}
	}
	return append(out, path[start:])
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		c := make(map[string]any, len(t))
		for k, val := range t {
			c[k] = cloneValue(val)
		}
		return c
	case []any:
		c := make([]any, len(t))
		for i, val := range t {
			c[i] = cloneValue(val)
		}
		return c
	default:
		return v
	}
}
