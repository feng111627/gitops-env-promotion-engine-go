// 命令行入口：本地渲染、晋级、回滚与漂移检测。
//
// 配置目录布局：
//
//	<dir>/base.json        基础配置层
//	<dir>/envs/<env>.json  环境覆盖层
//	<dir>/schema.json      字段默认语义与约束（可选）
//	<dir>/policy.json      声明式策略（可选）
//	<dir>/secrets.json     外部注入的密钥源（可选，模拟）
//
// 状态目录：<state>/<env>/current.json 与 history/<seq>.json。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/config"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/policy"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/promotion"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/secrets"
)

func main() {
	dir := flag.String("dir", "examples", "配置根目录")
	state := flag.String("state", "state", "状态根目录")
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: engine [-dir 配置目录] [-state 状态目录] <render|promote|rollback|drift> <env>")
		os.Exit(2)
	}
	cmd, env := args[0], args[1]

	var err error
	switch cmd {
	case "render":
		err = cmdRender(*dir, env)
	case "promote":
		err = cmdPromote(*dir, *state, env)
	case "rollback":
		err = cmdRollback(*state, env)
	case "drift":
		err = cmdDrift(*dir, *state, env)
	default:
		fmt.Fprintln(os.Stderr, "未知命令:", cmd)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

// pipeline 渲染 + 策略校验 + 密钥解析，任一环节失败都明确拒绝。
func pipeline(dir, env string) (map[string]any, error) {
	cfg, err := renderOnly(dir, env)
	if err != nil {
		return nil, err
	}
	pol, err := loadPolicy(dir)
	if err != nil {
		return nil, err
	}
	if vs := policy.Validate(env, cfg, pol); len(vs) > 0 {
		for _, v := range vs {
			fmt.Fprintln(os.Stderr, "策略违规:", v)
		}
		return nil, fmt.Errorf("环境 %s 存在 %d 条策略违规，已拒绝", env, len(vs))
	}
	src, err := loadSecrets(dir)
	if err != nil {
		return nil, err
	}
	resolved, missing := secrets.NewResolver(src).Resolve(cfg)
	if len(missing) > 0 {
		for _, m := range missing {
			fmt.Fprintln(os.Stderr, "密钥缺失:", m)
		}
		return nil, fmt.Errorf("环境 %s 存在 %d 个缺失密钥，已拒绝", env, len(missing))
	}
	return resolved, nil
}

func renderOnly(dir, env string) (map[string]any, error) {
	base, err := config.LoadLayer(filepath.Join(dir, "base.json"))
	if err != nil {
		return nil, err
	}
	overlay, err := config.LoadLayer(filepath.Join(dir, "envs", env+".json"))
	if err != nil {
		return nil, err
	}
	schema, err := loadSchema(dir)
	if err != nil {
		return nil, err
	}
	cfg, rejects := config.Render(schema, base, overlay)
	if len(rejects) > 0 {
		for _, r := range rejects {
			fmt.Fprintln(os.Stderr, "渲染拒绝:", r)
		}
		return nil, fmt.Errorf("环境 %s 渲染存在 %d 处拒绝", env, len(rejects))
	}
	return cfg, nil
}

func cmdRender(dir, env string) error {
	cfg, err := pipeline(dir, env)
	if err != nil {
		return err
	}
	return printJSON(cfg)
}

func cmdPromote(dir, stateDir, env string) error {
	cfg, err := pipeline(dir, env)
	if err != nil {
		return err
	}
	eng := promotion.NewEngine(promotion.NewStore(stateDir))
	v, err := eng.Promote(env, cfg, "promote "+env)
	if errors.Is(err, promotion.ErrNoChange) {
		fmt.Printf("环境 %s 配置与当前版本一致，不产生新版本\n", env)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("环境 %s 已晋级到版本 %d\n", v.Env, v.Seq)
	return nil
}

func cmdRollback(stateDir, env string) error {
	eng := promotion.NewEngine(promotion.NewStore(stateDir))
	v, err := eng.Rollback(env)
	if err != nil {
		return err
	}
	fmt.Printf("环境 %s 已回滚到版本 %d\n", v.Env, v.Seq)
	return nil
}

func cmdDrift(dir, stateDir, env string) error {
	cfg, err := pipeline(dir, env)
	if err != nil {
		return err
	}
	eng := promotion.NewEngine(promotion.NewStore(stateDir))
	drifts, err := eng.DetectDrift(env, cfg)
	if err != nil {
		return err
	}
	if len(drifts) == 0 {
		fmt.Printf("环境 %s 无漂移\n", env)
		return nil
	}
	fmt.Printf("环境 %s 存在 %d 处漂移:\n", env, len(drifts))
	for _, d := range drifts {
		fmt.Println(" ", d)
	}
	return nil
}

func loadSchema(dir string) (config.Schema, error) {
	var schema config.Schema
	if err := loadOptionalJSON(filepath.Join(dir, "schema.json"), &schema); err != nil {
		return nil, err
	}
	return schema, nil
}

func loadPolicy(dir string) (policy.Policy, error) {
	var p policy.Policy
	if err := loadOptionalJSON(filepath.Join(dir, "policy.json"), &p); err != nil {
		return p, err
	}
	return p, nil
}

func loadSecrets(dir string) (secrets.Source, error) {
	src := secrets.Source{}
	if err := loadOptionalJSON(filepath.Join(dir, "secrets.json"), &src); err != nil {
		return nil, err
	}
	return src, nil
}

func loadOptionalJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
