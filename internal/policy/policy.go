// Package policy 用声明式策略校验渲染结果，一次性汇总全部违规。
package policy

import (
	"fmt"
	"strings"
)

// Rule 是单条声明式规则。
type Rule struct {
	Field             string
	Required          bool
	Min               *float64
	Max               *float64
	NoPlaintextSecret bool // 该字段必须以 secret:// 引用形式出现，禁止明文
}

// Policy 是一组规则。
type Policy struct {
	Rules []Rule
}

// Violation 是一条违规记录。
type Violation struct {
	Env    string
	Field  string
	Rule   string
	Detail string
}

func (v Violation) Error() string {
	return fmt.Sprintf("[%s] %s 违反 %s: %s", v.Env, v.Field, v.Rule, v.Detail)
}

// Validate 校验渲染结果，遍历全部规则并汇总所有违规，不因首条违规中断。
func Validate(env string, cfg map[string]any, p Policy) []Violation {
	var out []Violation
	for _, r := range p.Rules {
		v, exists := cfg[r.Field]
		if !exists || v == nil {
			if r.Required {
				out = append(out, Violation{Env: env, Field: r.Field, Rule: "required", Detail: "必填字段缺失"})
			}
			continue
		}
		if r.Min != nil || r.Max != nil {
			n, ok := toFloat(v)
			if !ok {
				out = append(out, Violation{Env: env, Field: r.Field, Rule: "range",
					Detail: fmt.Sprintf("期望数值类型，实际为 %T", v)})
			} else {
				if r.Min != nil && n < *r.Min {
					out = append(out, Violation{Env: env, Field: r.Field, Rule: "range",
						Detail: fmt.Sprintf("值 %v 小于下限 %v", n, *r.Min)})
				}
				if r.Max != nil && n > *r.Max {
					out = append(out, Violation{Env: env, Field: r.Field, Rule: "range",
						Detail: fmt.Sprintf("值 %v 大于上限 %v", n, *r.Max)})
				}
			}
		}
		if r.NoPlaintextSecret {
			s, ok := v.(string)
			if !ok || !strings.HasPrefix(s, "secret://") {
				out = append(out, Violation{Env: env, Field: r.Field, Rule: "no_plaintext_secret",
					Detail: "敏感字段必须以 secret:// 引用形式提供，禁止明文"})
			}
		}
	}
	return out
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
