// Package secrets 从外部注入的密钥源解析配置中的 secret:// 引用。
package secrets

import "strings"

// Prefix 是密钥引用前缀，如 "secret://db/password"。
const Prefix = "secret://"

// Source 是外部密钥源（本地用 map 模拟，由调用方注入）。
type Source map[string]string

// Resolver 解析配置中的密钥引用。
type Resolver struct {
	src Source
}

func NewResolver(src Source) *Resolver { return &Resolver{src: src} }

// MissingError 按字段记录缺失的密钥。
type MissingError struct {
	Field string
	Name  string
}

func (e MissingError) Error() string {
	return "field " + e.Field + ": secret " + e.Name + " not found"
}

// Resolve 递归解析 cfg 中所有 "secret://name" 字符串引用，返回解析后的副本。
// 缺失的密钥按字段逐个收集为 MissingError，原引用保留不动，
// 绝不以空值或占位值蒙混。
func (r *Resolver) Resolve(cfg map[string]any) (map[string]any, []MissingError) {
	out := make(map[string]any, len(cfg))
	var missing []MissingError
	for k, v := range cfg {
		nv, m := r.resolveValue(k, v)
		out[k] = nv
		missing = append(missing, m...)
	}
	return out, missing
}

func (r *Resolver) resolveValue(field string, v any) (any, []MissingError) {
	switch t := v.(type) {
	case string:
		if name, ok := strings.CutPrefix(t, Prefix); ok {
			if val, found := r.src[name]; found {
				return val, nil
			}
			return v, []MissingError{{Field: field, Name: name}}
		}
		return v, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		var missing []MissingError
		for k, sv := range t {
			nv, m := r.resolveValue(field+"."+k, sv)
			out[k] = nv
			missing = append(missing, m...)
		}
		return out, missing
	case []any:
		out := make([]any, len(t))
		var missing []MissingError
		for i, sv := range t {
			nv, m := r.resolveValue(field, sv)
			out[i] = nv
			missing = append(missing, m...)
		}
		return out, missing
	}
	return v, nil
}
