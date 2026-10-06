package config

import "testing"

func f(v float64) *float64 { return &v }

func TestMerge_PriorityOrder(t *testing.T) {
	t.Log("业务: 分层合并 - 后面的环境覆盖层优先于基础层，嵌套 map 递归合并")
	base := Layer{"replicas": 1.0, "image": "app:1.0", "res": map[string]any{"cpu": "100m", "mem": "128Mi"}}
	dev := Layer{"replicas": 2.0, "res": map[string]any{"cpu": "200m"}}
	prod := Layer{"replicas": 4.0}

	merged, rejects := Merge(base, dev, prod)
	if len(rejects) != 0 {
		t.Fatalf("不应有拒绝: %v", rejects)
	}
	if merged["replicas"] != 4.0 {
		t.Errorf("replicas 应被最后一层覆盖为 4，实际 %v", merged["replicas"])
	}
	if merged["image"] != "app:1.0" {
		t.Errorf("未被覆盖的字段应保留基础层值，实际 %v", merged["image"])
	}
	res := merged["res"].(map[string]any)
	if res["cpu"] != "200m" || res["mem"] != "128Mi" {
		t.Errorf("嵌套 map 应递归合并，实际 %v", res)
	}
}

func TestMerge_TypeConflictRejected(t *testing.T) {
	t.Log("业务: 字段冲突 - 同名字段类型不一致时给出 type_conflict 拒绝且保留原值")
	base := Layer{"replicas": 1.0, "res": map[string]any{"cpu": "100m"}}
	bad := Layer{"replicas": "三", "res": "map-vs-string"}

	merged, rejects := Merge(base, bad)
	if len(rejects) != 2 {
		t.Fatalf("应有 2 条拒绝，实际 %d: %v", len(rejects), rejects)
	}
	for _, r := range rejects {
		if r.Reason != ReasonTypeConflict {
			t.Errorf("拒绝原因应为 type_conflict，实际 %s", r.Reason)
		}
	}
	if merged["replicas"] != 1.0 {
		t.Errorf("冲突字段不应被静默覆盖，实际 %v", merged["replicas"])
	}
	if _, ok := merged["res"].(map[string]any); !ok {
		t.Errorf("map 被 string 覆盖属于静默丢弃，实际 %v", merged["res"])
	}
}

func TestRender_LegacyConfigGetsDefaults(t *testing.T) {
	t.Log("业务: 兼容旧配置 - 旧配置缺少新增字段时按默认语义渲染，不直接失败")
	schema := Schema{
		"replicas": {Required: true, Min: f(1), Max: f(10)},
		"logLevel": {Default: "info"}, // 后来新增的字段
		"tracing":  {Default: false},  // 后来新增的字段
	}
	legacy := Layer{"replicas": 2.0} // 旧配置，没有 logLevel/tracing

	cfg, rejects := Render(schema, legacy)
	if len(rejects) != 0 {
		t.Fatalf("旧配置不应失败: %v", rejects)
	}
	if cfg["logLevel"] != "info" || cfg["tracing"] != false {
		t.Errorf("新增字段应按默认值渲染，实际 %v", cfg)
	}
}

func TestRender_MissingRequiredAndOutOfRange(t *testing.T) {
	t.Log("业务: 拒绝原因可区分 - 必填缺失为 missing_required，越界为 out_of_range")
	schema := Schema{
		"image":    {Required: true},
		"replicas": {Required: true, Min: f(1), Max: f(10)},
	}
	cfg, rejects := Render(schema, Layer{"replicas": 99.0})
	if cfg != nil && cfg["replicas"] != 99.0 {
		t.Errorf("越界值不应被静默改写，实际 %v", cfg["replicas"])
	}
	reasons := map[RejectReason]bool{}
	for _, r := range rejects {
		reasons[r.Reason] = true
	}
	if !reasons[ReasonMissingRequired] {
		t.Error("缺少 missing_required 拒绝")
	}
	if !reasons[ReasonOutOfRange] {
		t.Error("缺少 out_of_range 拒绝")
	}
	if len(rejects) != 2 {
		t.Errorf("应各有一条拒绝，实际 %v", rejects)
	}
}
