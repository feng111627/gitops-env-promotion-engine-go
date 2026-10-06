package engine

import (
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/policy"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/render"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/secret"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/store"
)

// Engine 编排分层渲染、外部密钥注入、声明式策略校验，
// 以及环境之间的晋级、回滚与漂移检测。
type Engine struct {
	loader   render.Loader
	schema   *model.Schema
	policy   *policy.Policy
	resolver secret.Resolver
	store    *store.Store
}

func New(loader render.Loader, schema *model.Schema, pol *policy.Policy, resolver secret.Resolver, st *store.Store) *Engine {
	return &Engine{loader: loader, schema: schema, policy: pol, resolver: resolver, store: st}
}

// Render 渲染单个环境的最终配置：
//  1. 分层合并（default < base < overlay）；
//  2. 外部密钥注入解析；
//  3. 声明式策略校验。
//
// 三个阶段的全部拒绝原因一次性汇总返回，可按 env/field/rule/code 区分，
// 任何一阶段有违规时，调用方都不得把结果写入已应用状态。
func (e *Engine) Render(env string) (map[string]any, *model.RenderedConfig, model.Rejections, error) {
	rendered, rej := render.Merge(env, e.loader, e.schema)

	final, secRej, err := secret.Resolve(rendered.Values, e.resolver)
	if err != nil {
		return nil, rendered, rej, err
	}
	rej = append(rej, secRej...)

	polRej := policy.Validate(e.policy, env, rendered.Values, final)
	rej = append(rej, polRej...)

	return final, rendered, rej, nil
}
