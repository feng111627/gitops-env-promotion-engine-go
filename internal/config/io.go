package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadLayer 从 JSON 文件加载一层配置；文件必须是一个 JSON 对象。
func LoadLayer(path string) (Layer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置层 %s 失败: %w", path, err)
	}
	var l Layer
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("解析配置层 %s 失败: %w", path, err)
	}
	if l == nil {
		return Layer{}, nil
	}
	return l, nil
}
