package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/engine"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/policy"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/render"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/secret"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/store"
)

const usage = `gitopsctl - 本地环境配置渲染与晋级工具

用法:
  gitopsctl --configs=configs render <env>
  gitopsctl --configs=configs apply <env> [--message=...]
  gitopsctl --configs=configs promote <src-env> <dst-env> [--message=...]
  gitopsctl --configs=configs rollback <env> [--message=...]
  gitopsctl --configs=configs drift <env>

说明:
  --configs  配置目录（默认 ./configs），包含 base.json、overlays/<env>.json、
             schema.json、policy.json
  --secrets  外部密钥文件（JSON map，例如 {"db/password":"..."}），
             也可用 GITOPS_SECRETS_FILE 环境变量指定
`

func main() {
	args, configsDir, secretsFile := parseFlags(os.Args[1:])
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if secretsFile == "" {
		secretsFile = os.Getenv("GITOPS_SECRETS_FILE")
	}

	schema := &model.Schema{}
	if err := render.ReadJSONFile(filepath.Join(configsDir, "schema.json"), schema); err != nil && !os.IsNotExist(err) {
		fatal("load schema: %v", err)
	}
	pol := &policy.Policy{}
	if err := render.ReadJSONFile(filepath.Join(configsDir, "policy.json"), pol); err != nil && !os.IsNotExist(err) {
		fatal("load policy: %v", err)
	}

	resolver, err := loadSecrets(secretsFile)
	if err != nil {
		fatal("%v", err)
	}

	eng := engine.New(
		render.NewFileLoader(configsDir),
		schema, pol, resolver,
		store.New(filepath.Join(configsDir, "state")),
	)

	cmd, rest := args[0], args[1:]
	var exitErr error
	switch cmd {
	case "render":
		requireN(cmd, rest, 1)
		exitErr = runRender(eng, rest[0])
	case "apply":
		requireN(cmd, rest, 1)
		exitErr = runApply(eng, rest[0], flagMessage(rest[1:]))
	case "promote":
		requireN(cmd, rest, 2)
		exitErr = runPromote(eng, rest[0], rest[1], flagMessage(rest[2:]))
	case "rollback":
		requireN(cmd, rest, 1)
		exitErr = runRollback(eng, rest[0], flagMessage(rest[1:]))
	case "drift":
		requireN(cmd, rest, 1)
		exitErr = runDrift(eng, rest[0])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if exitErr != nil {
		printRejections(exitErr)
		os.Exit(1)
	}
}

func runRender(eng *engine.Engine, env string) error {
	final, rendered, rej, err := eng.Render(env)
	if err != nil {
		return err
	}
	fmt.Printf("# env=%s 分层来源（default < base < overlay）\n", env)
	paths := make([]string, 0, len(rendered.Origins))
	for p := range rendered.Origins {
		paths = append(paths, p)
	}
	sortStrings(paths)
	for _, p := range paths {
		o := rendered.Origins[p]
		fmt.Printf("  %-40s <= %s\n", p, layerLabel(o.Layer, env))
	}
	fmt.Println("# 最终配置")
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(final)
	printRejectionsAsWarnings(rej)
	if rej.Has() {
		return rej
	}
	return nil
}

func runApply(eng *engine.Engine, env, msg string) error {
	rec, err := eng.Apply(env, msg)
	if err != nil {
		return err
	}
	fmt.Printf("applied %s to %s (checksum=%s)\n", rec.Version, env, short(rec.Checksum))
	return nil
}

func runPromote(eng *engine.Engine, src, dst, msg string) error {
	rec, err := eng.Promote(src, dst, msg)
	if err != nil {
		return err
	}
	fmt.Printf("promoted %s of %s -> %s as %s (checksum=%s)\n",
		rec.SourceVersion, rec.SourceEnv, dst, rec.Version, short(rec.Checksum))
	return nil
}

func runRollback(eng *engine.Engine, env, msg string) error {
	rec, err := eng.Rollback(env, msg)
	if err != nil {
		return err
	}
	fmt.Printf("rollback %s -> %s in %s (restored snapshot of %s, checksum=%s)\n",
		rec.Parent, rec.Version, env, rec.RestoredFrom, short(rec.Checksum))
	return nil
}

func runDrift(eng *engine.Engine, env string) error {
	report, rej, err := eng.DetectDrift(env)
	if err != nil {
		return err
	}
	if !report.Drifted {
		fmt.Printf("env=%s at %s: NO DRIFT\n", env, report.CurrentVersion)
	} else {
		fmt.Printf("env=%s at %s: DRIFT DETECTED (%d field(s))\n", env, report.CurrentVersion, len(report.Fields))
		for _, f := range report.Fields {
			fmt.Printf("  - %s: expected=%v actual=%v\n", f.Field, f.Expected, f.Actual)
		}
	}
	printRejectionsAsWarnings(rej)
	if rej.Has() {
		return rej
	}
	return nil
}

func loadSecrets(path string) (secret.Resolver, error) {
	if path == "" {
		return secret.MapResolver{}, nil
	}
	m := map[string]string{}
	if err := render.ReadJSONFile(path, &m); err != nil {
		return nil, fmt.Errorf("load secrets: %w", err)
	}
	return secret.MapResolver(m), nil
}

func layerLabel(layer, env string) string {
	if layer == "overlay" {
		return "overlay:" + env
	}
	return layer
}

func printRejections(err error) {
	if rs, ok := err.(model.Rejections); ok {
		fmt.Fprintf(os.Stderr, "REJECTED (%d reason(s)):\n", len(rs))
		for _, r := range rs {
			fmt.Fprintf(os.Stderr, "  - [%s] env=%s field=%s rule=%s %s\n",
				r.Code, nz(r.Env, "-"), nz(r.Field, "-"), nz(r.Rule, "-"), r.Message)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
}

func printRejectionsAsWarnings(rs model.Rejections) {
	for _, r := range rs {
		fmt.Fprintf(os.Stderr, "  ! [%s] env=%s field=%s rule=%s %s\n",
			r.Code, nz(r.Env, "-"), nz(r.Field, "-"), nz(r.Rule, "-"), r.Message)
	}
}

func nz(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func requireN(cmd string, rest []string, n int) {
	if len(rest) < n {
		fatal("%s requires %d argument(s)", cmd, n)
	}
}

func flagMessage(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "--message=") {
			return strings.TrimPrefix(a, "--message=")
		}
	}
	return ""
}

func parseFlags(argv []string) (args []string, configsDir, secretsFile string) {
	configsDir = "configs"
	for _, a := range argv {
		switch {
		case strings.HasPrefix(a, "--configs="):
			configsDir = strings.TrimPrefix(a, "--configs=")
		case strings.HasPrefix(a, "--secrets="):
			secretsFile = strings.TrimPrefix(a, "--secrets=")
		default:
			args = append(args, a)
		}
	}
	return args, configsDir, secretsFile
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(2)
}
