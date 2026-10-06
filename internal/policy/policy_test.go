package policy

import "testing"

func f64(v float64) *float64 { return &v }

func samplePolicy() *Policy {
	return &Policy{Rules: []Rule{
		{Name: "name", Kind: "required", Field: "app.name"},
		{Name: "replicas", Kind: "range", Field: "app.replicas", Min: f64(1), Max: f64(10)},
		{Name: "level", Kind: "enum", Field: "app.logLevel", Enum: []string{"info", "warn"}},
		{Name: "host", Kind: "pattern", Field: "db.host", Pattern: `^db\..*\.internal$`},
		{Name: "pwd", Kind: "noPlaintextSecret", Field: "db.password"},
	}}
}

func TestValidateAggregatesAllViolations(t *testing.T) {
	t.Log("业务：多条规则同时违规时必须一次性全部汇总，不能碰到第一条就中断")
	rendered := map[string]any{
		"app": map[string]any{"name": "", "replicas": float64(50), "logLevel": "trace"},
		"db":  map[string]any{"host": "external.evil", "password": "hunter2"},
	}
	final := map[string]any{
		"app": map[string]any{"name": "", "replicas": float64(50), "logLevel": "trace"},
		"db":  map[string]any{"host": "external.evil", "password": "hunter2"},
	}
	rej := Validate(samplePolicy(), "prod", rendered, final)
	if len(rej) != 5 {
		t.Fatalf("want 5 aggregated violations, got %d: %v", len(rej), rej)
	}
	want := map[string]string{
		"name":     "MISSING_REQUIRED",
		"replicas": "OUT_OF_RANGE",
		"level":    "ENUM_VIOLATION",
		"host":     "PATTERN_VIOLATION",
		"pwd":      "PLAINTEXT_SECRET",
	}
	got := map[string]string{}
	for _, r := range rej {
		got[r.Rule] = string(r.Code)
		if r.Env != "prod" {
			t.Errorf("rejection must carry env, got %q", r.Env)
		}
	}
	for rule, code := range want {
		if got[rule] != code {
			t.Errorf("rule %s: want %s, got %s", rule, code, got[rule])
		}
	}
	t.Log("通过：required/range/enum/pattern/明文密钥 5 类违规一次汇总，均带环境与规则名")
}

func TestValidatePassesWithSecretReference(t *testing.T) {
	t.Log("业务：密钥字段以 secret:// 引用即满足禁止明文策略；解析后的最终值参与其它规则")
	rendered := map[string]any{
		"app": map[string]any{"name": "checkout", "replicas": float64(2), "logLevel": "info"},
		"db":  map[string]any{"host": "db.prod.internal", "password": "secret://db/password"},
	}
	final := map[string]any{
		"app": map[string]any{"name": "checkout", "replicas": float64(2), "logLevel": "info"},
		"db":  map[string]any{"host": "db.prod.internal", "password": "injected-secret"},
	}
	rej := Validate(samplePolicy(), "prod", rendered, final)
	if rej.Has() {
		t.Fatalf("valid config must pass, got: %v", rej)
	}
	t.Log("通过：引用形态的密钥字段通过 noPlaintextSecret，最终配置无任何违规")
}

func TestValidateEnvScopedRules(t *testing.T) {
	t.Log("业务：规则可用 envs 限定生效环境，其它环境不校验该规则")
	pol := &Policy{Rules: []Rule{
		{Name: "prod-min", Kind: "range", Field: "app.replicas", Min: f64(3), Max: f64(10), Envs: []string{"prod"}},
	}}
	values := map[string]any{"app": map[string]any{"replicas": float64(1)}}
	if rej := Validate(pol, "dev", values, values); rej.Has() {
		t.Errorf("prod-only rule must not apply to dev: %v", rej)
	}
	rej := Validate(pol, "prod", values, values)
	if len(rej) != 1 || rej[0].Code != "OUT_OF_RANGE" {
		t.Fatalf("prod env should violate replicas range: %v", rej)
	}
	t.Log("通过：prod 专属下限规则只在 prod 生效，dev 放行")
}

func TestValidateTypeAndRangeBoundary(t *testing.T) {
	t.Log("业务：取值越界（含边界外）拒绝，类型不符单独报 TYPE_MISMATCH")
	pol := &Policy{Rules: []Rule{
		{Name: "r", Kind: "range", Field: "v", Min: f64(0), Max: f64(1)},
	}}
	if rej := Validate(pol, "e", map[string]any{"v": float64(0)}, map[string]any{"v": float64(0)}); rej.Has() {
		t.Errorf("lower boundary should pass: %v", rej)
	}
	if rej := Validate(pol, "e", map[string]any{"v": float64(1)}, map[string]any{"v": float64(1)}); rej.Has() {
		t.Errorf("upper boundary should pass: %v", rej)
	}
	rej := Validate(pol, "e", map[string]any{"v": "many"}, map[string]any{"v": "many"})
	if len(rej) != 1 || rej[0].Code != "TYPE_MISMATCH" {
		t.Fatalf("non-number should be TYPE_MISMATCH, got: %v", rej)
	}
	t.Log("通过：边界值放行，越界与类型错误分别给出 OUT_OF_RANGE / TYPE_MISMATCH")
}
