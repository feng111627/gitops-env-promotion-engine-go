package policy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/secret"
)

// 策略规则类型（kind）：
//
//	required            字段必须存在且非 null
//	range               数值必须落在 [min, max]
//	enum                字符串必须取自枚举集合
//	pattern             字符串必须匹配正则
//	noPlaintextSecret   字段必须以 secret:// 引用外部密钥，禁止明文
//
// 每条规则可用 envs 限定生效环境；为空表示对所有环境生效。
// 校验会遍历全部规则与全部字段，一次性汇总所有违规，绝不碰到第一条就中断。
type Rule struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Field     string   `json:"field,omitempty"`
	Envs      []string `json:"envs,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Enum      []string `json:"enum,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	SecretRef bool     `json:"secretRef,omitempty"`
}

type Policy struct {
	Rules []Rule `json:"rules"`
}

// Validate 对（已解析密钥后的）最终值执行策略；
// noPlaintextSecret 同时检查渲染值中的引用形态。
// renderedValues 为密钥解析前的渲染值（用于明文密钥检测），
// finalValues 为密钥解析后的最终值（用于 required/range/enum/pattern）。
func Validate(p *Policy, env string, renderedValues, finalValues map[string]any) model.Rejections {
	rej := model.Rejections{}
	if p == nil {
		return rej
	}
	for _, r := range p.Rules {
		if !appliesToEnv(r.Envs, env) {
			continue
		}
		switch r.Kind {
		case "required":
			v, ok := lookup(finalValues, r.Field)
			if !ok || v == nil || (isString(v) && strings.TrimSpace(v.(string)) == "") {
				rej = append(rej, reject(model.ReasonMissingRequired, env, r))
			}
		case "range":
			v, ok := lookup(finalValues, r.Field)
			if !ok || v == nil {
				continue // 缺失交给 required
			}
			n, err := toNumber(v)
			if err != nil {
				rej = append(rej, rejectWith(model.ReasonTypeMismatch, env, r, err.Error()))
				break
			}
			if r.Min != nil && n < *r.Min || r.Max != nil && n > *r.Max {
				rej = append(rej, rejectWith(model.ReasonOutOfRange, env, r,
					fmt.Sprintf("value %v out of range [%s, %s]", v, minStr(r.Min), maxStr(r.Max))))
			}
		case "enum":
			v, ok := lookup(finalValues, r.Field)
			if !ok || v == nil {
				continue
			}
			s, isStr := v.(string)
			if !isStr {
				rej = append(rej, rejectWith(model.ReasonTypeMismatch, env, r, "enum requires a string"))
				break
			}
			if !contains(r.Enum, s) {
				rej = append(rej, rejectWith(model.ReasonEnumViolation, env, r,
					fmt.Sprintf("value %q not in %v", s, r.Enum)))
			}
		case "pattern":
			v, ok := lookup(finalValues, r.Field)
			if !ok || v == nil {
				continue
			}
			s, isStr := v.(string)
			if !isStr {
				rej = append(rej, rejectWith(model.ReasonTypeMismatch, env, r, "pattern requires a string"))
				break
			}
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				rej = append(rej, rejectWith(model.ReasonPatternViolation, env, r, "invalid rule pattern: "+err.Error()))
				break
			}
			if !re.MatchString(s) {
				rej = append(rej, rejectWith(model.ReasonPatternViolation, env, r,
					fmt.Sprintf("value %q does not match %q", s, r.Pattern)))
			}
		case "noPlaintextSecret":
			// 解析前的值必须是 secret:// 引用；任何非引用形态都视为明文密钥。
			v, ok := lookup(renderedValues, r.Field)
			if !ok || v == nil {
				rej = append(rej, reject(model.ReasonMissingRequired, env, r))
				break
			}
			s, isStr := v.(string)
			if !isStr || !strings.HasPrefix(s, secret.SecretMarker) {
				rej = append(rej, rejectWith(model.ReasonPlaintextSecret, env, r,
					fmt.Sprintf("secret field must use %s reference, got plaintext value", secret.SecretMarker)))
			}
		default:
			rej = append(rej, rejectWith(model.ReasonPatternViolation, env, r, "unknown policy kind: "+r.Kind))
		}
	}
	return rej
}

func appliesToEnv(envs []string, env string) bool {
	if len(envs) == 0 {
		return true
	}
	return contains(envs, env)
}

func reject(code model.RejectCode, env string, r Rule) model.RejectReason {
	return rejectWith(code, env, r, "")
}

func rejectWith(code model.RejectCode, env string, r Rule, msg string) model.RejectReason {
	return model.RejectReason{
		Code:    code,
		Env:     env,
		Field:   r.Field,
		Rule:    r.Name,
		Message: msg,
	}
}

func isString(v any) bool { _, ok := v.(string); return ok }

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func toNumber(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case int32:
		return float64(n), nil
	default:
		return 0, fmt.Errorf("%v is not a number", v)
	}
}

func minStr(m *float64) string {
	if m == nil {
		return "-inf"
	}
	return fmt.Sprintf("%v", *m)
}

func maxStr(m *float64) string {
	if m == nil {
		return "+inf"
	}
	return fmt.Sprintf("%v", *m)
}

func lookup(values map[string]any, path string) (any, bool) {
	var cur any = values
	for _, seg := range strings.Split(path, ".") {
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
