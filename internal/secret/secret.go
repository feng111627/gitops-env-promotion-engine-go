package secret

import (
	"fmt"
	"strings"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

// SecretMarker 标识需要外部注入的密钥引用，例如 "secret://db/password"。
// 渲染后的配置中只允许出现该引用，真实值一律来自外部 Resolver，
// 不允许空值或占位值（如 "changeme"、"***"）蒙混。
const SecretMarker = "secret://"

// Resolver 从外部来源（环境变量、本地密钥文件等）解析密钥。
// found=false 表示来源中不存在该引用；value 为空同样视为缺失。
type Resolver interface {
	Resolve(ref string) (value string, found bool)
}

// MapResolver 是基于内存映射的解析器，便于本地模拟与测试。
// 键是不带 marker 的引用路径，例如 "db/password"。
type MapResolver map[string]string

func (m MapResolver) Resolve(ref string) (string, bool) {
	v, ok := m[ref]
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// Resolve 深拷贝并遍历渲染结果，把所有 secret:// 引用替换为注入值。
// 缺失密钥按字段逐个收集为 SECRET_MISSING，不会在第一条就中断。
// 未使用引用的普通字段原样保留（明文检查由 policy 的 noPlaintextSecret 负责）。
func Resolve(values map[string]any, resolver Resolver) (map[string]any, model.Rejections, error) {
	if resolver == nil {
		resolver = MapResolver{}
	}
	rej := model.Rejections{}
	out := resolveValue("", values, resolver, &rej)
	m, _ := out.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m, rej, nil
}

func resolveValue(path string, v any, resolver Resolver, rej *model.Rejections) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = resolveValue(join(path, k), val, resolver, rej)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = resolveValue(fmt.Sprintf("%s[%d]", path, i), val, resolver, rej)
		}
		return out
	case string:
		if !strings.HasPrefix(t, SecretMarker) {
			return t
		}
		ref := strings.TrimPrefix(t, SecretMarker)
		val, found := resolver.Resolve(ref)
		if !found {
			*rej = append(*rej, model.RejectReason{
				Code:    model.ReasonSecretMissing,
				Field:   path,
				Rule:    "secret-ref",
				Message: fmt.Sprintf("external secret %q is not provided; empty/placeholder value is forbidden", t),
			})
			return t // 保留引用占位，绝不写入假值
		}
		return val
	default:
		return v
	}
}

func join(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
