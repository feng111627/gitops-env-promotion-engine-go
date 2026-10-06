package model

import (
	"fmt"
	"strings"
	"time"
)

// RejectCode 是可区分的拒绝原因码。
type RejectCode string

const (
	ReasonTypeMismatch       RejectCode = "TYPE_MISMATCH"
	ReasonMissingRequired    RejectCode = "MISSING_REQUIRED"
	ReasonOutOfRange         RejectCode = "OUT_OF_RANGE"
	ReasonEnumViolation      RejectCode = "ENUM_VIOLATION"
	ReasonPatternViolation   RejectCode = "PATTERN_VIOLATION"
	ReasonSecretMissing      RejectCode = "SECRET_MISSING"
	ReasonPlaintextSecret    RejectCode = "PLAINTEXT_SECRET"
	ReasonDuplicatePromotion RejectCode = "DUPLICATE_PROMOTION"
	ReasonConcurrentChange   RejectCode = "CONCURRENT_CHANGE"
	ReasonNothingToRollback  RejectCode = "NOTHING_TO_ROLLBACK"
	ReasonNotFound           RejectCode = "NOT_FOUND"
)

// RejectReason 描述一条被拒绝的变更或一条校验违规。
type RejectReason struct {
	Code    RejectCode `json:"code"`
	Env     string     `json:"env,omitempty"`
	Field   string     `json:"field,omitempty"`
	Rule    string     `json:"rule,omitempty"`
	Message string     `json:"message"`
}

func (r RejectReason) Error() string {
	parts := make([]string, 0, 4)
	if r.Env != "" {
		parts = append(parts, "env="+r.Env)
	}
	if r.Field != "" {
		parts = append(parts, "field="+r.Field)
	}
	if r.Rule != "" {
		parts = append(parts, "rule="+r.Rule)
	}
	parts = append(parts, string(r.Code), r.Message)
	return strings.Join(parts, " ")
}

// Rejections 是一次性汇总的全部拒绝原因，而非碰到第一条就中断。
type Rejections []RejectReason

func (rs Rejections) Error() string {
	msgs := make([]string, len(rs))
	for i, r := range rs {
		msgs[i] = r.Error()
	}
	return fmt.Sprintf("%d rejection(s): %s", len(rs), strings.Join(msgs, "; "))
}

func (rs Rejections) Has() bool { return len(rs) > 0 }

// FieldOrigin 记录字段最终取值来自哪一层。
type FieldOrigin struct {
	Path  string `json:"path"`
	Layer string `json:"layer"` // base | overlay:<env> | default
}

// RenderedConfig 是某环境分层合并后、密钥解析前的渲染结果。
type RenderedConfig struct {
	Env     string                 `json:"env"`
	Values  map[string]any         `json:"values"`
	Origins map[string]FieldOrigin `json:"origins"`
}

// FieldSchema 声明字段类型与“旧配置缺字段”时使用的确定默认值。
type FieldSchema struct {
	Path    string `json:"path"`
	Type    string `json:"type"` // object | array | string | number | bool
	Default any    `json:"default,omitempty"`
}

// Schema 是新增字段的默认语义表。
type Schema struct {
	Fields []FieldSchema `json:"fields"`
}

// 版本记录动作。
const (
	ActionApply    = "apply"
	ActionPromote  = "promote"
	ActionRollback = "rollback"
)

// VersionRecord 是一次已应用变更的不可变版本记录。
type VersionRecord struct {
	Version       string         `json:"version"`
	Parent        string         `json:"parent,omitempty"`
	Action        string         `json:"action"`
	SourceEnv     string         `json:"sourceEnv,omitempty"`
	SourceVersion string         `json:"sourceVersion,omitempty"`
	RestoredFrom  string         `json:"restoredFrom,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	Checksum      string         `json:"checksum"`
	Snapshot      map[string]any `json:"snapshot"`
	Message       string         `json:"message,omitempty"`
}

// EnvState 是单个环境的已应用状态与版本历史。
type EnvState struct {
	Env            string          `json:"env"`
	CurrentVersion string          `json:"currentVersion"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	History        []VersionRecord `json:"history"`
}

// State 是全部环境的状态集合。
type State struct {
	Envs map[string]*EnvState `json:"envs"`
}

// DriftField 描述一个漂移字段。
type DriftField struct {
	Field    string `json:"field"`
	Expected any    `json:"expected"` // 已应用版本中的值
	Actual   any    `json:"actual"`   // 当前声明式配置渲染出的值
}

// DriftReport 是漂移检测结论。
type DriftReport struct {
	Env            string       `json:"env"`
	CurrentVersion string       `json:"currentVersion"`
	Drifted        bool         `json:"drifted"`
	Fields         []DriftField `json:"fields,omitempty"`
}
