package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

func TestMutateSerializesConcurrentAppends(t *testing.T) {
	t.Log("业务：多个变更同时发起时必须串行化，历史只追加，条目数量精确、不交错、不丢失")
	dir := t.TempDir()
	s := New(dir)

	const n = 30
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errCh <- s.Mutate(func(st *model.State) error {
				es := st.Envs["e"]
				if es == nil {
					es = &model.EnvState{Env: "e"}
					st.Envs["e"] = es
				}
				es.History = append(es.History, model.VersionRecord{
					Version: string(rune('a'+i%26)) + "x",
				})
				return nil
			})
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("unexpected mutate error: %v", err)
		}
	}

	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(st.Envs["e"].History); got != n {
		t.Fatalf("want %d history entries, got %d (interleaved/lost writes)", n, got)
	}

	data, err := readRaw(s)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonBalanced(data) {
		t.Errorf("state file is not a complete JSON document (torn write suspected)")
	}
	t.Log("通过：30 个并发追加全部落盘，条目数精确为 30，状态文件是完整 JSON")
}

func TestMutateRejectedChangeNotPersisted(t *testing.T) {
	t.Log("业务：fn 返回错误时必须放弃写盘，被拦下的变更绝不污染状态")
	dir := t.TempDir()
	s := New(dir)

	boom := errors.New("boom")
	if err := s.Mutate(func(st *model.State) error {
		st.Envs["e"] = &model.EnvState{Env: "e", CurrentVersion: "vX"}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}

	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := st.Envs["e"]; exists {
		t.Fatalf("rejected mutation must not be persisted, got %+v", st.Envs)
	}
	t.Log("通过：被拒绝的变更未写盘，状态保持为空")
}

func TestLoadMissingFileReturnsEmptyState(t *testing.T) {
	t.Log("业务：状态文件不存在时返回可用的空状态，而不是报错")
	s := New(t.TempDir())
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Envs == nil {
		t.Fatal("Envs map must be initialized")
	}
	if err := s.Mutate(func(st *model.State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	t.Log("通过：首次空状态可正常加载并提交")
}

// readRaw / jsonBalanced 用于断言落盘文件是完整 JSON（防半截写入）。
func readRaw(s *Store) ([]byte, error) {
	return readFile(s.statePath())
}

func jsonBalanced(b []byte) bool {
	depth := 0
	inStr := false
	var esc bool
	for _, c := range b {
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && !inStr
}
