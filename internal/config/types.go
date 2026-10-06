// Package config 实现基础配置与多环境覆盖层的分层合并与渲染。
package config

// Layer 是一层配置（基础层或某个环境的覆盖层），键为字段路径。
type Layer map[string]any

// RejectReason 区分渲染/合并被拒绝的原因。
type RejectReason string

const (
	ReasonTypeConflict    RejectReason = "type_conflict"    // 同名字段在不同层类型不一致
	ReasonMissingRequired RejectReason = "missing_required" // 必填字段缺失
	ReasonOutOfRange      RejectReason = "out_of_range"     // 数值越界
	ReasonInvalidLayer    RejectReason = "invalid_layer"    // 层文件本身无法解析
)

// Reject 描述一次被拒绝的合并/渲染。
type Reject struct {
	Field  string
	Reason RejectReason
	Detail string
}

func (r Reject) Error() string { return r.Field + ": " + string(r.Reason) + ": " + r.Detail }

// FieldSpec 描述字段的默认语义与约束，用于兼容旧配置（缺字段时按默认值渲染）。
type FieldSpec struct {
	Required bool
	Default  any
	Min      *float64
	Max      *float64
}

// Schema 是字段名到 FieldSpec 的映射。
type Schema map[string]FieldSpec
