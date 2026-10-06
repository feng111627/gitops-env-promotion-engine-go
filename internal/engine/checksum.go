package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"fmt"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
	"sort"
	"strconv"
	"strings"
	"time"
)

// checksum 对配置快照做确定性哈希：递归排序对象键后做规范化序列化，
// 使“逻辑相同的配置”在任何环境、任何时间都得到相同 checksum。
func checksum(v any) string {
	canonical := canonicalize(v)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func canonicalize(v any) string {
	var b strings.Builder
	writeCanonical(&b, v)
	return b.String()
}

func writeCanonical(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",")
			}
			writeJSONString(b, k)
			b.WriteString(":")
			writeCanonical(b, t[k])
		}
		b.WriteString("}")
	case []any:
		b.WriteString("[")
		for i, val := range t {
			if i > 0 {
				b.WriteString(",")
			}
			writeCanonical(b, val)
		}
		b.WriteString("]")
	case string:
		writeJSONString(b, t)
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		b.WriteString(strconv.FormatFloat(t, 'g', -1, 64))
	case float32:
		b.WriteString(strconv.FormatFloat(float64(t), 'g', -1, 32))
	case int:
		b.WriteString(strconv.Itoa(t))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case json.Number:
		b.WriteString(t.String())
	default:
		// 兜底，正常路径不应走到。
		raw, _ := json.Marshal(v)
		b.Write(raw)
	}
}

func writeJSONString(b *strings.Builder, s string) {
	raw, _ := json.Marshal(s)
	b.Write(raw)
}

// nextVersion 在父版本号基础上递增（v1 -> v2），保持版本历史可读。
func nextVersion(parent string) string {
	if parent == "" {
		return "v1"
	}
	var n int
	if _, err := fmt.Sscanf(parent, "v%d", &n); err != nil || n <= 0 {
		return "v1"
	}
	return fmt.Sprintf("v%d", n+1)
}

// newRecord 构造一条不可变版本记录。
func newRecord(parent, action string, snapshot map[string]any) model.VersionRecord {
	return model.VersionRecord{
		Version:   nextVersion(parent),
		Parent:    parent,
		Action:    action,
		CreatedAt: time.Now().UTC(),
		Checksum:  checksum(snapshot),
		Snapshot:  snapshot,
	}
}
