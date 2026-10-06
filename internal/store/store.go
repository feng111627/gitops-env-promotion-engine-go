package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

// Store 用本地 JSON 文件（<root>/state.json）保存所有环境的已应用状态，
// 并用 <root>/state.lock 上的 flock(LOCK_EX) 把并发的读-改-写串行化，
// 保证多个晋级请求同时发起时不会交错写入、不会产生半截文件。
// 落盘采用“临时文件 + rename”原子替换。
type Store struct {
	root string
	mu   sync.Mutex // 同进程内 goroutine 的互斥（flock 负责跨进程）
}

func New(root string) *Store {
	return &Store{root: root}
}

func (s *Store) statePath() string { return filepath.Join(s.root, "state.json") }
func (s *Store) lockPath() string  { return filepath.Join(s.root, "state.lock") }

// Load 读取当前状态；state.json 不存在时返回空状态。
func (s *Store) Load() (*model.State, error) {
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return emptyState(), nil
		}
		return nil, err
	}
	var st model.State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse state.json: %w", err)
	}
	if st.Envs == nil {
		st.Envs = map[string]*model.EnvState{}
	}
	return &st, nil
}

// FileLock 是一把排他文件锁。
type FileLock struct{ f *os.File }

func (s *Store) Lock() (*FileLock, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &FileLock{f: f}, nil
}

func (l *FileLock) Unlock() error {
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		l.f.Close()
		return err
	}
	return l.f.Close()
}

// Mutate 在排他锁保护下执行一次读-改-写。
// fn 返回 error 时不写盘；返回 nil 时原子提交新状态。
func (s *Store) Mutate(fn func(*model.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lock, err := s.Lock()
	if err != nil {
		return fmt.Errorf("acquire state lock: %w", err)
	}
	defer lock.Unlock()

	st, err := s.Load()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err // 被拦下的变更：不写盘
	}
	return s.writeAtomic(st)
}

func (s *Store) writeAtomic(st *model.State) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil { // 状态文件可能含已注入的密钥值
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.statePath())
}

func emptyState() *model.State {
	return &model.State{Envs: map[string]*model.EnvState{}}
}
