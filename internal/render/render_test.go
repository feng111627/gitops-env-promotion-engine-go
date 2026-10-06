package render

import (
	"encoding/json"
	"testing"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

// memLoader 用内存数据模拟 base/overlay，便于单测。
type memLoader struct {
	base    string
	overlay string
}

func (m memLoader) LoadBase() (map[string]any, error) {
	var v map[string]any
	if err := json.Unmarshal([]byte(m.base), &v); err != nil {
		return nil, err
	}
	return v, nil
}
func (m memLoader) LoadOverlay(string) (map[string]any, error) {
	var v map[string]any
	if err := json.Unmarshal([]byte(m.overlay), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func j(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	return v
}

func TestMergePriority(t *testing.T) {
	t.Log("业务：分层合并优先级 default < base < overlay，同名字段 overlay 精确覆盖")
	l := memLoader{
		base:    `{"a":1,"b":2,"nested":{"x":"base","y":"keep"},"list":[1,2]}`,
		overlay: `{"b":20,"nested":{"x":"overlay"}}`,
	}
	out, rej := Merge("dev", l, nil)
	if rej.Has() {
		t.Fatalf("expected no rejection, got: %v", rej)
	}
	if out.Values["a"].(float64) != 1 {
		t.Errorf("base-only field a lost: %v", out.Values["a"])
	}
	if out.Values["b"].(float64) != 20 {
		t.Errorf("overlay should override b, got %v", out.Values["b"])
	}
	nested := out.Values["nested"].(map[string]any)
	if nested["x"] != "overlay" || nested["y"] != "keep" {
		t.Errorf("deep merge wrong: %v", nested)
	}
	if got := out.Values["list"].([]any); len(got) != 2 {
		t.Errorf("array should be replaced as whole, got %v", got)
	}
	if o := out.Origins["b"].Layer; o != "overlay" {
		t.Errorf("origin of b = %q, want overlay", o)
	}
	if o := out.Origins["nested.y"].Layer; o != "base" {
		t.Errorf("origin of nested.y = %q, want base", o)
	}
	t.Log("通过：base-only 保留、overlay 覆盖、对象深合并、数组整体替换、来源层可解释")
}

func TestMergeTypeConflict(t *testing.T) {
	t.Log("业务：字段类型不一致时 overlay 不得静默覆盖 base，必须给出 TYPE_MISMATCH")
	l := memLoader{
		base:    `{"port":5432,"nested":{"k":1}}`,
		overlay: `{"port":"five-four-three-two","nested":123}`,
	}
	out, rej := Merge("prod", l, nil)
	if len(rej) != 2 {
		t.Fatalf("want 2 TYPE_MISMATCH, got %d: %v", len(rej), rej)
	}
	for _, r := range rej {
		if r.Code != model.ReasonTypeMismatch {
			t.Errorf("want TYPE_MISMATCH, got %s", r.Code)
		}
	}
	if out.Values["port"].(float64) != 5432 {
		t.Errorf("base number must be kept on conflict, got %v", out.Values["port"])
	}
	if nested, ok := out.Values["nested"].(map[string]any); !ok || nested["k"].(float64) != 1 {
		t.Errorf("base object must be kept on conflict, got %v", out.Values["nested"])
	}
	t.Log("通过：两处类型冲突都被拒绝，base 值保留，拒绝原因可区分到字段")
}

func TestMergeNullMeansUnset(t *testing.T) {
	t.Log("业务：显式 null 视为未设置，由低优先级层生效，而不是把字段清空")
	l := memLoader{
		base:    `{"a":1,"b":2}`,
		overlay: `{"a":null}`,
	}
	out, rej := Merge("dev", l, nil)
	if rej.Has() {
		t.Fatalf("unexpected rejection: %v", rej)
	}
	if out.Values["a"].(float64) != 1 {
		t.Errorf("null overlay must not erase base value, got %v", out.Values["a"])
	}
	t.Log("通过：overlay 的 null 被忽略，base 值正常生效")
}

func TestMergeSchemaDefaultsForOldConfig(t *testing.T) {
	t.Log("业务：旧配置缺少后来新增字段时，按 schema 确定默认值渲染，不失败")
	l := memLoader{
		base:    `{"app":{"name":"old-app"}}`,
		overlay: `{}`,
	}
	schema := &model.Schema{Fields: []model.FieldSchema{
		{Path: "app.name", Type: "string", Default: "unnamed"},
		{Path: "app.replicas", Type: "number", Default: float64(1)},
		{Path: "app.tracing.enabled", Type: "bool", Default: false},
		{Path: "http.retries", Type: "number", Default: float64(3)},
	}}
	out, rej := Merge("staging", l, schema)
	if rej.Has() {
		t.Fatalf("old config must not fail on missing new fields: %v", rej)
	}
	if out.Values["app"].(map[string]any)["name"] != "old-app" {
		t.Errorf("existing field must not be overwritten by default")
	}
	app := out.Values["app"].(map[string]any)
	if app["replicas"].(float64) != 1 {
		t.Errorf("default replicas wrong: %v", app["replicas"])
	}
	if tracing, _ := app["tracing"].(map[string]any); tracing["enabled"].(bool) != false {
		t.Errorf("nested default wrong: %v", app["tracing"])
	}
	http := out.Values["http"].(map[string]any)
	if http["retries"].(float64) != 3 {
		t.Errorf("sibling-object default wrong: %v", http)
	}
	if o := out.Origins["http.retries"].Layer; o != "default" {
		t.Errorf("defaulted field origin = %q, want default", o)
	}
	t.Log("通过：新增标量、嵌套对象、兄弟对象默认值均按 schema 补齐，来源标记为 default")
}

func TestMergeSchemaTypeCheck(t *testing.T) {
	t.Log("业务：schema 声明的类型与实际值不符也要明确拒绝，不静默丢弃")
	l := memLoader{
		base:    `{"app":{"replicas":"many"}}`,
		overlay: `{}`,
	}
	schema := &model.Schema{Fields: []model.FieldSchema{
		{Path: "app.replicas", Type: "number", Default: float64(1)},
	}}
	_, rej := Merge("dev", l, schema)
	if len(rej) != 1 || rej[0].Code != model.ReasonTypeMismatch || rej[0].Field != "app.replicas" {
		t.Fatalf("want one TYPE_MISMATCH on app.replicas, got: %v", rej)
	}
	t.Log("通过：字符串冒充数字被 TYPE_MISMATCH 拒绝")
}
