package policy

import "testing"

func f(v float64) *float64 { return &v }

func TestValidate_AggregatesAllViolations(t *testing.T) {
	t.Log("业务: 策略违规 - 必填/范围/明文密钥全部违规按环境、字段、规则一次性汇总，不遇错即停")
	p := Policy{Rules: []Rule{
		{Field: "image", Required: true},
		{Field: "replicas", Required: true, Min: f(1), Max: f(10)},
		{Field: "dbPassword", NoPlaintextSecret: true},
	}}
	cfg := map[string]any{
		"replicas":   99.0,          // 越界
		"dbPassword": "plain-text!", // 明文密钥
		// image 缺失
	}
	vs := Validate("prod", cfg, p)
	if len(vs) != 3 {
		t.Fatalf("应汇总 3 条违规，实际 %d: %v", len(vs), vs)
	}
	byRule := map[string]bool{}
	for _, v := range vs {
		if v.Env != "prod" {
			t.Errorf("违规应带环境名，实际 %q", v.Env)
		}
		byRule[v.Rule] = true
	}
	for _, want := range []string{"required", "range", "no_plaintext_secret"} {
		if !byRule[want] {
			t.Errorf("缺少规则 %s 的违规记录", want)
		}
	}
}

func TestValidate_Pass(t *testing.T) {
	t.Log("业务: 策略违规 - 合规配置不产生任何违规")
	p := Policy{Rules: []Rule{
		{Field: "replicas", Required: true, Min: f(1), Max: f(10)},
		{Field: "dbPassword", NoPlaintextSecret: true},
	}}
	cfg := map[string]any{"replicas": 3.0, "dbPassword": "secret://db/password"}
	if vs := Validate("dev", cfg, p); len(vs) != 0 {
		t.Fatalf("合规配置不应有违规: %v", vs)
	}
}
