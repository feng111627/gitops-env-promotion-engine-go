package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/policy"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/render"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/secret"
	"github.com/feng111627/gitops-env-promotion-engine-go/internal/store"
)

// harness 准备一套基于本地临时目录的真实引擎（真实文件 + 真实文件锁）。
type harness struct {
	dir    string
	eng    *Engine
	st     *store.Store
	loader *render.FileLoader
}

func baseJSON() map[string]any {
	return map[string]any{
		"app": map[string]any{
			"name":     "checkout",
			"replicas": float64(1),
			"logLevel": "info",
		},
		"db": map[string]any{
			"host":     "db.internal",
			"port":     float64(5432),
			"password": "secret://db/password",
		},
	}
}

func schemaDoc() *model.Schema {
	return &model.Schema{Fields: []model.FieldSchema{
		{Path: "app.name", Type: "string", Default: "unnamed"},
		{Path: "app.replicas", Type: "number", Default: float64(1)},
		{Path: "http.retries", Type: "number", Default: float64(3)},
	}}
}

func policyDoc() *policy.Policy {
	minR, maxR := float64(1), float64(10)
	return &policy.Policy{Rules: []policy.Rule{
		{Name: "name-req", Kind: "required", Field: "app.name"},
		{Name: "replicas-range", Kind: "range", Field: "app.replicas", Min: &minR, Max: &maxR},
		{Name: "level-enum", Kind: "enum", Field: "app.logLevel", Enum: []string{"debug", "info", "warn"}},
		{Name: "pwd-secret", Kind: "noPlaintextSecret", Field: "db.password"},
	}}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "configs")
	if err := os.MkdirAll(filepath.Join(cfgDir, "overlays"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(cfgDir, "base.json"), baseJSON())

	for _, env := range []string{"dev", "staging", "prod"} {
		writeJSON(t, filepath.Join(cfgDir, "overlays", env+".json"), map[string]any{})
	}

	loader := render.NewFileLoader(cfgDir)
	st := store.New(filepath.Join(dir, "state"))
	eng := New(loader, schemaDoc(), policyDoc(),
		secret.MapResolver{"db/password": "resolved-pw"}, st)
	return &harness{dir: dir, eng: eng, st: st, loader: loader}
}

func (h *harness) setOverlay(t *testing.T, env string, m map[string]any) {
	t.Helper()
	writeJSON(t, filepath.Join(h.dir, "configs", "overlays", env+".json"), m)
}

func (h *harness) stateEnv(t *testing.T, env string) *model.EnvState {
	st, err := h.st.Load()
	if err != nil {
		t.Fatal(err)
	}
	return st.Envs[env]
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func rejectionCodes(err error) []model.RejectCode {
	rs, ok := err.(model.Rejections)
	if !ok {
		return nil
	}
	codes := make([]model.RejectCode, len(rs))
	for i, r := range rs {
		codes[i] = r.Code
	}
	return codes
}

func hasCode(codes []model.RejectCode, want model.RejectCode) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}
