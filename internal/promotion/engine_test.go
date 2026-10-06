package promotion

import (
	"errors"
	"sync"
	"testing"
)

func newEngine(t *testing.T) *Engine {
	t.Helper()
	return NewEngine(NewStore(t.TempDir()))
}

func TestPromote_Rollback_Drift(t *testing.T) {
	t.Log("业务: 晋级/回滚/漂移 - 版本递增记录，回滚精确回到上一版本，漂移逐字段可解释")
	eng := newEngine(t)

	v1, err := eng.Promote("prod", map[string]any{"replicas": 1.0}, "v1")
	if err != nil || v1.Seq != 1 {
		t.Fatalf("首次晋级应为版本 1: v=%+v err=%v", v1, err)
	}
	v2, err := eng.Promote("prod", map[string]any{"replicas": 3.0}, "v2")
	if err != nil || v2.Seq != 2 {
		t.Fatalf("第二次晋级应为版本 2: v=%+v err=%v", v2, err)
	}

	drifts, err := eng.DetectDrift("prod", map[string]any{"replicas": 5.0})
	if err != nil || len(drifts) != 1 || drifts[0].Field != "replicas" {
		t.Fatalf("应检测到 replicas 一处漂移: %+v err=%v", drifts, err)
	}

	rb, err := eng.Rollback("prod")
	if err != nil || rb.Seq != 1 {
		t.Fatalf("回滚应精确回到版本 1: %+v err=%v", rb, err)
	}
	cur, _ := eng.store.Current("prod")
	if cur.Config["replicas"] != 1.0 {
		t.Errorf("回滚后当前配置应为上一版本内容，实际 %v", cur.Config)
	}
	// 回滚后漂移应对照版本 1 计算
	drifts, _ = eng.DetectDrift("prod", map[string]any{"replicas": 1.0})
	if len(drifts) != 0 {
		t.Errorf("回滚后与版本 1 一致，不应有漂移: %+v", drifts)
	}

	if _, err := eng.Rollback("prod"); !errors.Is(err, ErrNoPrevious) {
		t.Errorf("没有更早版本时应返回 ErrNoPrevious，实际 %v", err)
	}
}

func TestPromote_IdempotentNoDuplicateVersion(t *testing.T) {
	t.Log("业务: 重复晋级 - 内容一致时不产生重复版本、不污染历史")
	eng := newEngine(t)
	cfg := map[string]any{"replicas": 2.0}

	if _, err := eng.Promote("dev", cfg, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Promote("dev", cfg, "dup"); !errors.Is(err, ErrNoChange) {
		t.Fatalf("重复晋级应返回 ErrNoChange，实际 %v", err)
	}
	hist, _ := eng.store.History("dev")
	if len(hist) != 1 {
		t.Errorf("历史应只有 1 个版本，实际 %d", len(hist))
	}
}

func TestPromote_ConcurrentSerialized(t *testing.T) {
	t.Log("业务: 并发晋级 - 多请求串行写入，版本号连续无交错，重复内容被明确拒绝")
	eng := newEngine(t)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := map[string]any{"replicas": float64(i + 1)}
			_, errs[i] = eng.Promote("prod", cfg, "concurrent")
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil && !errors.Is(err, ErrNoChange) {
			t.Fatalf("并发晋级出现意外错误: %v", err)
		}
	}
	hist, err := eng.store.History("prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) == 0 {
		t.Fatal("并发后应至少有一个版本")
	}
	for i, v := range hist {
		if v.Seq != i+1 {
			t.Fatalf("版本号应连续从 1 递增，实际第 %d 个为 Seq=%d（状态错乱）", i, v.Seq)
		}
	}
	cur, _ := eng.store.Current("prod")
	if cur.Seq != hist[len(hist)-1].Seq {
		t.Errorf("当前版本应指向最后一条历史，实际 current=%d last=%d", cur.Seq, hist[len(hist)-1].Seq)
	}
}

func TestDrift_NeverApplied(t *testing.T) {
	t.Log("业务: 漂移检测 - 环境从未应用过任何版本时整体视为漂移")
	eng := newEngine(t)
	drifts, err := eng.DetectDrift("staging", map[string]any{"a": 1.0})
	if err != nil || len(drifts) != 1 {
		t.Fatalf("未应用环境应报整体漂移: %+v err=%v", drifts, err)
	}
}
