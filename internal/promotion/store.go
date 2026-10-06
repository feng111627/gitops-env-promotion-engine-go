// Package promotion 实现配置在环境间的晋级、回滚与漂移检测，按版本记录状态。
package promotion

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Version 记录一次已应用的版本。
type Version struct {
	Env     string         `json:"env"`
	Seq     int            `json:"seq"`
	Config  map[string]any `json:"config"`
	Comment string         `json:"comment"`
}

// Store 用本地文件持久化各环境的已应用状态与历史。
// 目录布局：<dir>/<env>/current.json 与 <dir>/<env>/history/<seq>.json。
type Store struct {
	dir string
}

func NewStore(dir string) *Store { return &Store{dir: dir} }

func (s *Store) envDir(env string) string  { return filepath.Join(s.dir, env) }
func (s *Store) histDir(env string) string { return filepath.Join(s.envDir(env), "history") }
func (s *Store) currentPath(env string) string {
	return filepath.Join(s.envDir(env), "current.json")
}

// Current 返回环境当前已应用版本；从未应用过则返回 (nil, nil)。
func (s *Store) Current(env string) (*Version, error) {
	data, err := os.ReadFile(s.currentPath(env))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v Version
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("解析 %s 当前版本失败: %w", env, err)
	}
	return &v, nil
}

// History 返回环境的全部历史版本（按 Seq 升序）。
func (s *Store) History(env string) ([]Version, error) {
	entries, err := os.ReadDir(s.histDir(env))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Version
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.histDir(env), e.Name()))
		if err != nil {
			return nil, err
		}
		var v Version
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("解析历史版本 %s/%s 失败: %w", env, e.Name(), err)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// Append 追加一个新版本并设为当前版本。历史文件先写、当前指针后写，
// 保证 current 永远指向已落盘的历史版本。
func (s *Store) Append(v Version) error {
	if err := os.MkdirAll(s.histDir(v.Env), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%06d.json", v.Seq)
	if err := os.WriteFile(filepath.Join(s.histDir(v.Env), name), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(s.currentPath(v.Env), data, 0o644)
}

// SetCurrent 把当前指针切到指定历史版本（用于回滚），版本必须已存在。
func (s *Store) SetCurrent(env string, seq int) error {
	hist, err := s.History(env)
	if err != nil {
		return err
	}
	for _, v := range hist {
		if v.Seq == seq {
			data, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(s.currentPath(env), data, 0o644)
		}
	}
	return fmt.Errorf("环境 %s 不存在版本 %s", env, strconv.Itoa(seq))
}
