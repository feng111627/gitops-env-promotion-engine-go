package engine

import (
	"fmt"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

// Rollback 让环境精确回到“上一个已应用版本”。
//
// 语义：
//   - 当前版本必须存在其父版本；历史只有一条（v1，无父版本）时
//     返回 NOTHING_TO_ROLLBACK，绝不凭空构造一个“空配置”版本；
//   - 回滚本身作为一条不可变 rollback 版本记录追加到历史（历史只追加），
//     其快照内容与父版本快照逐字节语义一致（checksum 相同），
//     并通过 parent/restoredFrom 精确指向来源，保证可审计、可解释；
//   - 回滚后再次回滚会沿着 parent 链继续向前，不会在两个版本间来回弹跳。
func (e *Engine) Rollback(env, message string) (*model.VersionRecord, error) {
	var created *model.VersionRecord
	err := e.store.Mutate(func(st *model.State) error {
		es := st.Envs[env]
		cur := latestRecord(es)
		if cur == nil {
			return model.Rejections{{
				Code: model.ReasonNothingToRollback, Env: env,
				Message: "env has never been applied; nothing to rollback",
			}}
		}
		// “上一个已应用版本”沿有效版本链解析：
		//   - 普通记录（apply/promote）：上一版即其 parent；
		//   - 回滚记录：当前生效内容来自 restoredFrom，其上一版是
		//     restoredFrom 这条记录的 parent。
		// 这样连续回滚会沿历史单调向前，而不会在两个快照间来回弹跳。
		anchorVersion := cur.Parent
		if cur.Action == model.ActionRollback && cur.RestoredFrom != "" {
			restored, ok := findRecord(es, cur.RestoredFrom)
			if !ok {
				return model.Rejections{{
					Code: model.ReasonNothingToRollback, Env: env,
					Field:   cur.RestoredFrom,
					Message: fmt.Sprintf("restored-from version %s is missing from history", cur.RestoredFrom),
				}}
			}
			anchorVersion = restored.Parent
		}
		if anchorVersion == "" {
			return model.Rejections{{
				Code: model.ReasonNothingToRollback, Env: env,
				Field:   cur.Version,
				Message: fmt.Sprintf("current effective version %s is the initial version and has no previous applied version", cur.Version),
			}}
		}
		parent, ok := findRecord(es, anchorVersion)
		if !ok {
			return model.Rejections{{
				Code: model.ReasonNothingToRollback, Env: env,
				Field:   anchorVersion,
				Message: fmt.Sprintf("previous applied version %s is missing from history", anchorVersion),
			}}
		}

		rec := newRecord(cur.Version, model.ActionRollback, cloneMap(parent.Snapshot))
		rec.RestoredFrom = parent.Version
		rec.Message = message
		appendRecord(es, &rec)
		created = &rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func findRecord(es *model.EnvState, version string) (*model.VersionRecord, bool) {
	for i := range es.History {
		if es.History[i].Version == version {
			return &es.History[i], true
		}
	}
	return nil, false
}
