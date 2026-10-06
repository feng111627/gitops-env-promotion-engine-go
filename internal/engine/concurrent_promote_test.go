package engine

import (
	"sync"
	"testing"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

func TestConcurrentPromotions(t *testing.T) {
	t.Log("业务：多个相同晋级请求同时发起，只有一个写入成功，其余明确拒绝；历史恰好一条、状态不交错")
	h := newHarness(t)
	if _, err := h.eng.Apply("dev", "v1"); err != nil {
		t.Fatal(err)
	}

	const n = 25
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, duplicated, other := 0, 0, 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.eng.Promote("dev", "staging", "concurrent")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case hasCode(rejectionCodes(err), model.ReasonDuplicatePromotion):
				duplicated++
			default:
				other++
				t.Errorf("unexpected rejection: %v", err)
			}
		}()
	}
	wg.Wait()

	if ok != 1 {
		t.Errorf("exactly one promotion must succeed, got %d", ok)
	}
	if duplicated != n-1 {
		t.Errorf("remaining %d must be rejected as duplicate, got %d", n-1, duplicated)
	}
	if other != 0 {
		t.Errorf("there must be no unclassified result, got %d", other)
	}

	es := h.stateEnv(t, "staging")
	if len(es.History) != 1 || es.CurrentVersion != "v1" {
		t.Fatalf("history must contain exactly one v1, got %+v", es.History)
	}
	dev := h.stateEnv(t, "dev")
	if es.History[0].Checksum != dev.History[0].Checksum {
		t.Errorf("concurrent writes corrupted the snapshot checksum")
	}
	t.Logf("通过：%d 个并发晋级中 1 成功、%d 个 DUPLICATE_PROMOTION，历史只有一条且 checksum 一致", ok, duplicated)
}
