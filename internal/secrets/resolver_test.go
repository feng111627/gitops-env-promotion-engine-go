package secrets

import "testing"

func TestResolve_InjectsExternalSecrets(t *testing.T) {
	t.Log("业务: 密钥注入 - secret:// 引用从外部源解析，支持嵌套字段")
	r := NewResolver(Source{"db/password": "s3cr3t"})
	cfg := map[string]any{
		"db":    map[string]any{"password": "secret://db/password"},
		"image": "app:1.0",
	}
	out, missing := r.Resolve(cfg)
	if len(missing) != 0 {
		t.Fatalf("不应有缺失: %v", missing)
	}
	db := out["db"].(map[string]any)
	if db["password"] != "s3cr3t" {
		t.Errorf("密钥应被解析注入，实际 %v", db["password"])
	}
	// 原配置不被修改
	orig := cfg["db"].(map[string]any)
	if orig["password"] != "secret://db/password" {
		t.Errorf("Resolve 不应修改原配置，实际 %v", orig["password"])
	}
}

func TestResolve_MissingReportedPerField(t *testing.T) {
	t.Log("业务: 密钥缺失 - 缺失密钥按字段逐个报错，不以空值或占位值蒙混")
	r := NewResolver(Source{}) // 空密钥源
	cfg := map[string]any{
		"db":  map[string]any{"password": "secret://db/password"},
		"api": map[string]any{"token": "secret://api/token"},
	}
	out, missing := r.Resolve(cfg)
	if len(missing) != 2 {
		t.Fatalf("应有 2 个缺失密钥，实际 %v", missing)
	}
	fields := map[string]bool{}
	for _, m := range missing {
		fields[m.Field] = true
	}
	if !fields["db.password"] || !fields["api.token"] {
		t.Errorf("缺失应按字段逐个报告，实际 %v", missing)
	}
	db := out["db"].(map[string]any)
	if db["password"] != "secret://db/password" {
		t.Errorf("缺失密钥不应被替换为空值/占位值，实际 %v", db["password"])
	}
}
