package render

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileLoader 用本地 JSON 文件模拟基础配置与环境覆盖层：
//   - <root>/base.json
//   - <root>/overlays/<env>.json
type FileLoader struct {
	root string
}

func NewFileLoader(root string) *FileLoader {
	return &FileLoader{root: root}
}

func (l *FileLoader) LoadBase() (map[string]any, error) {
	return readJSONMap(filepath.Join(l.root, "base.json"))
}

func (l *FileLoader) LoadOverlay(env string) (map[string]any, error) {
	if env == "" {
		return nil, fmt.Errorf("env must not be empty")
	}
	return readJSONMap(filepath.Join(l.root, "overlays", env+".json"))
}

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// ReadJSONFile 是供引擎读取 schema/策略的通用工具。
func ReadJSONFile(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
