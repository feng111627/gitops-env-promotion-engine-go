package secret

import (
	"encoding/json"
	"testing"
)

func TestResolveInjectsExternalSecrets(t *testing.T) {
	t.Log("业务：secret:// 引用必须由外部注入解析，普通字段原样保留")
	values := map[string]any{
		"db": map[string]any{
			"host":     "db.internal",
			"password": "secret://db/password",
		},
		"nested": []any{"secret://a/key", "plain"},
	}
	resolver := MapResolver{"db/password": "s3cr3t", "a/key": "kval"}
	out, rej, err := Resolve(values, resolver)
	if err != nil || rej.Has() {
		t.Fatalf("unexpected failure: %v %v", err, rej)
	}
	db := out["db"].(map[string]any)
	if db["password"] != "s3cr3t" {
		t.Errorf("secret not injected, got %v", db["password"])
	}
	if db["host"] != "db.internal" {
		t.Errorf("plain field altered: %v", db["host"])
	}
	if out["nested"].([]any)[0] != "kval" {
		t.Errorf("array secret not injected: %v", out["nested"])
	}
	raw, _ := json.Marshal(values)
	if string(raw) == "" {
		t.Fail()
	}
	if values["db"].(map[string]any)["password"] != "secret://db/password" {
		t.Errorf("Resolve must not mutate the input rendered map")
	}
	t.Log("通过：对象与数组中的引用均被注入，入参未被改写，普通字段保持不变")
}

func TestResolveMissingSecretsReportedPerField(t *testing.T) {
	t.Log("业务：缺失密钥必须逐字段报 SECRET_MISSING，绝不用空值或占位值蒙混")
	values := map[string]any{
		"db":  map[string]any{"password": "secret://db/password"},
		"tls": map[string]any{"key": "secret://tls/private"},
		"ok":  "secret://present/one",
	}
	resolver := MapResolver{"present/one": "x"}
	out, rej, err := Resolve(values, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if len(rej) != 2 {
		t.Fatalf("want 2 missing-secret reasons, got %d: %v", len(rej), rej)
	}
	fields := map[string]bool{}
	for _, r := range rej {
		if r.Code != "SECRET_MISSING" {
			t.Errorf("want SECRET_MISSING, got %s", r.Code)
		}
		if r.Field == "" {
			t.Errorf("missing-secret reason must identify the field")
		}
		fields[r.Field] = true
	}
	if !fields["db.password"] || !fields["tls.key"] {
		t.Errorf("missing-secret fields wrong: %v", fields)
	}
	// 缺失的密钥位保留引用，绝不写入空字符串/占位值。
	if out["db"].(map[string]any)["password"] != "secret://db/password" {
		t.Errorf("missing secret must keep the reference, got %v", out["db"])
	}
	if out["ok"] != "x" {
		t.Errorf("present secret should be injected")
	}
	t.Log("通过：两个缺失密钥分别带字段名报出，缺失位保留引用，已提供的密钥正常注入")
}

func TestResolveEmptySecretValueTreatedAsMissing(t *testing.T) {
	t.Log("业务：来源中密钥值为空字符串同样视为缺失，不允许空值蒙混")
	resolver := MapResolver{"db/password": ""}
	_, rej, err := Resolve(map[string]any{"db": map[string]any{"password": "secret://db/password"}}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if len(rej) != 1 || rej[0].Code != "SECRET_MISSING" {
		t.Fatalf("empty secret value must be SECRET_MISSING, got: %v", rej)
	}
	t.Log("通过：空字符串密钥值被当作缺失拒绝")
}
