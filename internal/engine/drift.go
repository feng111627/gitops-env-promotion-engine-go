package engine

import (
	"sort"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

// DetectDrift 对比环境“最近一次已应用版本的快照”与“当前声明式配置的渲染结果”。
//
// 语义：
//   - 只比较已应用版本中记录过的字段：overlay/base 中全新增加的字段属于新版本，
//     不算漂移（需要通过新的 apply/promote 才进入已应用状态）；
//   - 已应用字段的值与当前渲染值不同、或字段在声明式配置中被删除/置空，
//     都逐字段报告；没有差异则 drifted=false；
//   - 当前渲染若存在合并/密钥/策略违规，则这些违规随报告一起返回，
//     漂移检测不会把违规静默吞掉。
func (e *Engine) DetectDrift(env string) (*model.DriftReport, model.Rejections, error) {
	st, err := e.store.Load()
	if err != nil {
		return nil, nil, err
	}
	es := st.Envs[env]
	cur := latestRecord(es)
	if cur == nil {
		return nil, nil, model.Rejections{{
			Code: model.ReasonNotFound, Env: env,
			Message: "env has no applied version; run apply first",
		}}
	}

	final, _, rej, err := e.Render(env)
	if err != nil {
		return nil, nil, err
	}

	report := &model.DriftReport{
		Env:            env,
		CurrentVersion: cur.Version,
		Drifted:        false,
	}

	paths := []string{}
	collectLeafPaths("", cur.Snapshot, &paths)
	sort.Strings(paths)
	for _, p := range paths {
		expected, _ := lookupAny(cur.Snapshot, p)
		actual, found := lookupAny(final, p)
		if !found || !deepEqual(expected, actual) {
			report.Fields = append(report.Fields, model.DriftField{
				Field:    p,
				Expected: expected,
				Actual:   actualOrNil(actual, found),
			})
		}
	}
	report.Drifted = len(report.Fields) > 0
	return report, rej, nil
}

func actualOrNil(v any, found bool) any {
	if !found {
		return nil
	}
	return v
}

func collectLeafPaths(prefix string, v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			collectLeafPaths(joinPath(prefix, k), val, out)
		}
	default:
		*out = append(*out, prefix)
	}
}

func lookupAny(values map[string]any, path string) (any, bool) {
	var cur any = values
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '.' {
			seg := path[start:i]
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			cur, ok = m[seg]
			if !ok {
				return nil, false
			}
			start = i + 1
		}
	}
	return cur, true
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// deepEqual 通过规范化比较任意 JSON 值（map 键顺序无关）。
func deepEqual(a, b any) bool {
	return canonicalize(a) == canonicalize(b)
}
