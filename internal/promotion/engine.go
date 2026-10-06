package promotion

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// ErrNoChange 表示重复晋级，内容与当前版本一致，不产生新版本。
var ErrNoChange = errors.New("no change: rendered config identical to current version")

// ErrNoPrevious 表示没有可回滚的上一个版本。
var ErrNoPrevious = errors.New("no previous version to roll back to")

// Drift 描述期望配置与已应用配置的差异。
type Drift struct {
	Field    string
	Expected any
	Actual   any
}

func (d Drift) String() string {
	return fmt.Sprintf("%s: 期望 %v，实际 %v", d.Field, d.Expected, d.Actual)
}

// Engine 用按环境的互斥锁串行化晋级/回滚，避免并发请求交错写入导致状态错乱。
type Engine struct {
	store *Store
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewEngine(store *Store) *Engine {
	return &Engine{store: store, locks: map[string]*sync.Mutex{}}
}

func (e *Engine) envLock(env string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.locks[env]
	if !ok {
		l = &sync.Mutex{}
		e.locks[env] = l
	}
	return l
}

// Promote 把渲染后的配置应用到目标环境，生成新版本；
// 与当前版本一致时返回 ErrNoChange，不产生重复版本、不污染历史。
func (e *Engine) Promote(env string, cfg map[string]any, comment string) (*Version, error) {
	l := e.envLock(env)
	l.Lock()
	defer l.Unlock()

	cur, err := e.store.Current(env)
	if err != nil {
		return nil, err
	}
	seq := 1
	if cur != nil {
		if reflect.DeepEqual(normalize(cur.Config), normalize(cfg)) {
			return nil, ErrNoChange
		}
		seq = cur.Seq + 1
	}
	v := Version{Env: env, Seq: seq, Config: cfg, Comment: comment}
	if err := e.store.Append(v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Rollback 精确回到上一个已应用版本（Seq 次新的历史版本）。
func (e *Engine) Rollback(env string) (*Version, error) {
	l := e.envLock(env)
	l.Lock()
	defer l.Unlock()

	cur, err := e.store.Current(env)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, ErrNoPrevious
	}
	hist, err := e.store.History(env)
	if err != nil {
		return nil, err
	}
	var prev *Version
	for i := range hist {
		if hist[i].Seq < cur.Seq && (prev == nil || hist[i].Seq > prev.Seq) {
			v := hist[i]
			prev = &v
		}
	}
	if prev == nil {
		return nil, ErrNoPrevious
	}
	if err := e.store.SetCurrent(env, prev.Seq); err != nil {
		return nil, err
	}
	return prev, nil
}

// DetectDrift 比较期望配置与当前已应用版本，递归返回全部差异字段；
// 环境从未应用过任何版本时，整体视为漂移。
func (e *Engine) DetectDrift(env string, desired map[string]any) ([]Drift, error) {
	cur, err := e.store.Current(env)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return []Drift{{Field: "(root)", Expected: desired, Actual: nil}}, nil
	}
	var out []Drift
	diffInto("", desired, cur.Config, &out)
	return out, nil
}

// diffInto 递归比较期望与实际，把差异追加到 out。
func diffInto(path string, want, got map[string]any, out *[]Drift) {
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		field := k
		if path != "" {
			field = path + "." + k
		}
		w, wOK := want[k]
		g, gOK := got[k]
		wMap, wIsMap := w.(map[string]any)
		gMap, gIsMap := g.(map[string]any)
		if wOK && gOK && wIsMap && gIsMap {
			diffInto(field, wMap, gMap, out)
			continue
		}
		if !reflect.DeepEqual(normalize(w), normalize(g)) {
			*out = append(*out, Drift{Field: field, Expected: w, Actual: g})
		}
	}
}

// normalize 把数值统一为 float64，消除 JSON 反序列化与手工构造之间的类型差。
func normalize(v any) any {
	switch t := v.(type) {
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, sv := range t {
			out[k] = normalize(sv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, sv := range t {
			out[i] = normalize(sv)
		}
		return out
	}
	return v
}
