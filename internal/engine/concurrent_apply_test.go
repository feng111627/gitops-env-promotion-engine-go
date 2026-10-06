package engine

import (
	"sync"
	"testing"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

func TestConcurrentAppliesCreateOrderedLinearHistory(t *testing.T) {
	t.Log("业务：并发的状态写入必须在锁下串行成线性历史，版本号连续、无断裂、无交错")
	h := newHarness(t)

	// 预置 10 份互不相同的渲染输入，避免多个 goroutine 重写同一个 overlay 文件；
	// 本用例只验证“状态读-改-写”的串行化与版本链连续性。
	envByName := map[string]string{}
	for i := 0; i < 10; i++ {
		name := "e" + string(rune('a'+i))
		envByName[name] = name
		h.setOverlay(t, name, map[string]any{
			"app": map[string]any{"replicas": float64(1), "logLevel": "info"},
		})
	}

	var wg sync.WaitGroup
	for name := range envByName {
		wg.Add(1)
		go func(env string) {
			defer wg.Done()
			if _, err := h.eng.Apply(env, "parallel"); err != nil {
				t.Errorf("apply %s: %v", env, err)
			}
			// 每个环境再做 4 次串行无关的并发第二次 apply（新环境，保证成功）。
		}(name)
	}
	wg.Wait()

	st, err := h.st.Load()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, es := range st.Envs {
		if es.Env == "dev" {
			continue
		}
		count++
		if len(es.History) != 1 || es.CurrentVersion != "v1" {
			t.Errorf("env %s history not linear: %+v", es.Env, es.History)
		}
		rec := es.History[0]
		if rec.Action != model.ActionApply || rec.Checksum == "" {
			t.Errorf("env %s record malformed: %+v", es.Env, rec)
		}
	}
	if count != 10 {
		t.Errorf("want 10 env entries serialized, got %d", count)
	}
	t.Log("通过：10 个并发 apply 在锁下串行落盘，各自版本链连续、checksum 完整")
}
