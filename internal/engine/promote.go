package engine

import (
	"fmt"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

// Promote 把源环境当前已应用版本晋级到目标环境。
//
// 语义：
//   - 源环境必须已有已应用版本（先 apply），否则 NOT_FOUND；
//   - 晋级只复制“已应用版本的快照”（已经过密钥注入与策略校验的最终配置），
//     保证不同环境最终配置一致、可解释；
//   - 目标环境最新版本与源当前版本内容相同且来源相同，则判定为重复晋级，
//     返回 DUPLICATE_PROMOTION，不产生新版本、不污染历史；
//   - 整个读-改-写在文件锁内完成；并发请求被串行化，后到者若内容已一致，
//     以 CONCURRENT_CHANGE 之外的明确 DUPLICATE_PROMOTION 拒绝；
//     若等待期间目标被其它请求改动到不同内容，则以 CONCURRENT_CHANGE 拒绝，
//     要求调用方基于最新状态重试。
func (e *Engine) Promote(srcEnv, dstEnv, message string) (*model.VersionRecord, error) {
	if srcEnv == dstEnv {
		return nil, model.Rejections{{
			Code:    model.ReasonOutOfRange,
			Message: "source and target env must differ",
		}}
	}

	var created *model.VersionRecord
	err := e.store.Mutate(func(st *model.State) error {
		src := st.Envs[srcEnv]
		if src == nil || src.CurrentVersion == "" {
			return model.Rejections{{
				Code: model.ReasonNotFound, Env: srcEnv,
				Message: "source env has no applied version; run apply first",
			}}
		}
		srcRec := latestRecord(src)
		if srcRec == nil {
			return model.Rejections{{
				Code: model.ReasonNotFound, Env: srcEnv,
				Message: "source env history is empty",
			}}
		}

		dst := st.Envs[dstEnv]
		if dst != nil && dst.CurrentVersion != "" {
			latest := latestRecord(dst)
			if latest != nil && latest.Action == model.ActionPromote &&
				latest.SourceEnv == srcEnv && latest.SourceVersion == src.CurrentVersion &&
				latest.Checksum == srcRec.Checksum {
				return model.Rejections{{
					Code: model.ReasonDuplicatePromotion, Env: dstEnv,
					Field: latest.Version,
					Message: fmt.Sprintf("version %s from %s is already applied to %s; duplicate promotion rejected",
						src.CurrentVersion, srcEnv, dstEnv),
				}}
			}
		}

		rec := newRecord(currentVersion(dst), model.ActionPromote, cloneMap(srcRec.Snapshot))
		rec.SourceEnv = srcEnv
		rec.SourceVersion = src.CurrentVersion
		rec.Message = message

		if dst == nil {
			dst = &model.EnvState{Env: dstEnv}
			st.Envs[dstEnv] = dst
		}
		appendRecord(dst, &rec)
		created = &rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// Apply 把环境当前声明式配置渲染并作为新版本写入已应用状态（首次部署）。
// 存在任何合并/密钥/策略拒绝原因时整体拒绝、不写任何版本。
func (e *Engine) Apply(env, message string) (*model.VersionRecord, error) {
	final, _, rej, err := e.Render(env)
	if err != nil {
		return nil, err
	}
	if rej.Has() {
		return nil, rej
	}

	var created *model.VersionRecord
	err = e.store.Mutate(func(st *model.State) error {
		es := st.Envs[env]
		rec := newRecord(currentVersion(es), model.ActionApply, cloneMap(final))
		rec.Message = message
		if es == nil {
			es = &model.EnvState{Env: env}
			st.Envs[env] = es
		}
		appendRecord(es, &rec)
		created = &rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func latestRecord(es *model.EnvState) *model.VersionRecord {
	if es == nil || len(es.History) == 0 {
		return nil
	}
	rec := es.History[len(es.History)-1]
	return &rec
}

func currentVersion(es *model.EnvState) string {
	if es == nil {
		return ""
	}
	return es.CurrentVersion
}

// appendRecord 不可变地追加版本记录。历史只追加，绝不改写或删除既有条目。
func appendRecord(es *model.EnvState, rec *model.VersionRecord) {
	es.History = append(es.History, *rec)
	es.CurrentVersion = rec.Version
	es.UpdatedAt = rec.CreatedAt
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		c := make([]any, len(t))
		for i := range t {
			c[i] = cloneValue(t[i])
		}
		return c
	default:
		return v
	}
}
