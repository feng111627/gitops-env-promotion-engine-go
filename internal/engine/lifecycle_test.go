package engine

import (
	"testing"

	"github.com/feng111627/gitops-env-promotion-engine-go/internal/model"
)

func TestApplyRendersDefaultsAndResolvedSecrets(t *testing.T) {
	t.Log("业务：首次 apply 渲染默认值并注入外部密钥，版本快照即最终配置")
	h := newHarness(t)
	rec, err := h.eng.Apply("dev", "initial")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rec.Version != "v1" || rec.Action != model.ActionApply {
		t.Fatalf("unexpected record: %+v", rec)
	}
	snap := rec.Snapshot
	if snap["db"].(map[string]any)["password"] != "resolved-pw" {
		t.Errorf("snapshot must contain resolved secret, got %v", snap["db"])
	}
	if snap["http"].(map[string]any)["retries"].(float64) != 3 {
		t.Errorf("schema default missing from snapshot: %v", snap["http"])
	}
	t.Log("通过：v1 快照含 schema 默认值与已注入密钥，不残留 secret:// 引用")
}

func TestApplyRejectsWhenValidationFails(t *testing.T) {
	t.Log("业务：渲染/策略/密钥存在任何违规时整体拒绝，不产生任何版本")
	h := newHarness(t)
	h.setOverlay(t, "dev", map[string]any{
		"app": map[string]any{"replicas": float64(99), "logLevel": "trace"},
		"db":  map[string]any{"password": "plain-text-pw"},
	})
	_, err := h.eng.Apply("dev", "bad")
	codes := rejectionCodes(err)
	for _, want := range []model.RejectCode{
		model.ReasonOutOfRange, model.ReasonEnumViolation, model.ReasonPlaintextSecret,
	} {
		if !hasCode(codes, want) {
			t.Errorf("want rejection %s in %v", want, codes)
		}
	}
	if es := h.stateEnv(t, "dev"); es != nil {
		t.Fatalf("rejected apply must not create any version, got %+v", es)
	}
	t.Log("通过：越界+枚举+明文密钥三类违规一次性拒绝，状态中无任何版本")
}

func TestPromotionConsistencyAndDuplicateRejection(t *testing.T) {
	t.Log("业务：晋级复制已应用快照保证环境间一致；重复晋级拒绝且不污染历史")
	h := newHarness(t)
	if _, err := h.eng.Apply("dev", ""); err != nil {
		t.Fatal(err)
	}
	rec, err := h.eng.Promote("dev", "staging", "")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if rec.Action != model.ActionPromote || rec.SourceEnv != "dev" || rec.SourceVersion != "v1" {
		t.Fatalf("promotion record wrong: %+v", rec)
	}

	// 即便 staging 自己的 overlay 不同，晋级得到的最终配置必须与 dev 已应用版本一致。
	devEs := h.stateEnv(t, "dev")
	stEs := h.stateEnv(t, "staging")
	devSum := devEs.History[0].Checksum
	stSum := stEs.History[0].Checksum
	if devSum != stSum {
		t.Errorf("promoted snapshot must match source: %s != %s", devSum, stSum)
	}

	_, err = h.eng.Promote("dev", "staging", "")
	if err == nil || !hasCode(rejectionCodes(err), model.ReasonDuplicatePromotion) {
		t.Fatalf("duplicate promotion must be rejected with DUPLICATE_PROMOTION, got %v", err)
	}
	if len(stEs.History) != 1 || stEs.CurrentVersion != "v1" {
		t.Fatalf("rejected duplicate must not add history: %+v", stEs)
	}
	t.Log("通过：晋级快照与源一致，重复晋级被明确拒绝且历史保持一条")
}

func TestPromotionAfterSourceChangeCreatesNewVersion(t *testing.T) {
	t.Log("业务：源环境产生新版本后再晋级，目标应得到新版本；相同来源重复晋级仍拒绝")
	h := newHarness(t)
	if _, err := h.eng.Apply("dev", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.eng.Promote("dev", "staging", ""); err != nil {
		t.Fatal(err)
	}
	h.setOverlay(t, "dev", map[string]any{
		"app": map[string]any{"replicas": float64(4)},
	})
	if _, err := h.eng.Apply("dev", "v2"); err != nil {
		t.Fatal(err)
	}
	rec2, err := h.eng.Promote("dev", "staging", "v2")
	if err != nil {
		t.Fatalf("promote v2: %v", err)
	}
	if rec2.Version != "v2" || rec2.SourceVersion != "v2" {
		t.Fatalf("want target v2 sourced from dev v2, got %+v", rec2)
	}
	if _, err := h.eng.Promote("dev", "staging", ""); !hasCode(rejectionCodes(err), model.ReasonDuplicatePromotion) {
		t.Fatalf("repeated promotion of same source version must be duplicate: %v", err)
	}
	if got := len(h.stateEnv(t, "staging").History); got != 2 {
		t.Fatalf("history must have exactly 2 entries, got %d", got)
	}
	t.Log("通过：源新版本可再次晋级，相同来源重复晋级仍被拒，历史精确为 2 条")
}

func TestPromoteFromEnvWithoutAppliedVersion(t *testing.T) {
	t.Log("业务：源环境从未 apply 时晋级必须明确拒绝（NOT_FOUND）")
	h := newHarness(t)
	_, err := h.eng.Promote("ghost", "staging", "")
	if !hasCode(rejectionCodes(err), model.ReasonNotFound) {
		t.Fatalf("want NOT_FOUND, got %v", err)
	}
	t.Log("通过：空源环境晋级被 NOT_FOUND 拒绝")
}

func TestRollbackRestoresPreviousAppliedVersion(t *testing.T) {
	t.Log("业务：回滚精确回到上一个已应用版本，checksum 一致，并沿历史单调向前")
	h := newHarness(t)
	if _, err := h.eng.Apply("dev", "v1"); err != nil {
		t.Fatal(err)
	}
	h.setOverlay(t, "dev", map[string]any{"app": map[string]any{"replicas": float64(4)}})
	if _, err := h.eng.Apply("dev", "v2"); err != nil {
		t.Fatal(err)
	}
	rb, err := h.eng.Rollback("dev", "back")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	es := h.stateEnv(t, "dev")
	v1 := es.History[0]
	if rb.RestoredFrom != "v1" || rb.Parent != "v2" {
		t.Fatalf("rollback pointer wrong: %+v", rb)
	}
	if rb.Checksum != v1.Checksum {
		t.Errorf("rollback snapshot must equal v1 snapshot: %s != %s", rb.Checksum, v1.Checksum)
	}
	if es.CurrentVersion != "v3" {
		t.Errorf("current pointer wrong: %s", es.CurrentVersion)
	}

	// 已回到最初版本内容，再次回滚必须拒绝，而不是弹回 v2。
	_, err = h.eng.Rollback("dev", "")
	if !hasCode(rejectionCodes(err), model.ReasonNothingToRollback) {
		t.Fatalf("rollback at initial content must be refused, got %v", err)
	}
	if got := len(es.History); got != 3 {
		t.Fatalf("refused rollback must not append history, got %d", got)
	}
	t.Log("通过：v3 回滚到 v1 内容且 checksum 相同，再回滚被拒绝，历史只追加共 3 条")
}

func TestRollbackNothingToRollback(t *testing.T) {
	t.Log("业务：从未应用、或只有初始版本时回滚必须给出 NOTHING_TO_ROLLBACK")
	h := newHarness(t)
	if _, err := h.eng.Rollback("dev", ""); !hasCode(rejectionCodes(err), model.ReasonNothingToRollback) {
		t.Fatalf("never-applied env: %v", err)
	}
	if _, err := h.eng.Apply("dev", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.eng.Rollback("dev", ""); !hasCode(rejectionCodes(err), model.ReasonNothingToRollback) {
		t.Fatalf("initial version rollback: %v", err)
	}
	t.Log("通过：无历史与仅初始版本两种情况都被正确拒绝")
}

func TestDriftDetectedThenCleared(t *testing.T) {
	t.Log("业务：声明式配置与已应用版本不一致时逐字段报告漂移；一致时无漂移")
	h := newHarness(t)
	if _, err := h.eng.Apply("dev", ""); err != nil {
		t.Fatal(err)
	}
	report, rej, err := h.eng.DetectDrift("dev")
	if err != nil || rej.Has() {
		t.Fatalf("fresh apply should be clean: %v %v", err, rej)
	}
	if report.Drifted {
		t.Fatalf("fresh apply must report no drift: %+v", report)
	}

	h.setOverlay(t, "dev", map[string]any{
		"app": map[string]any{"replicas": float64(7)},
	})
	report, _, err = h.eng.DetectDrift("dev")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Drifted || len(report.Fields) != 1 {
		t.Fatalf("want exactly 1 drifted field, got %+v", report.Fields)
	}
	f := report.Fields[0]
	if f.Field != "app.replicas" || f.Expected.(float64) != 1 || f.Actual.(float64) != 7 {
		t.Errorf("drift detail wrong: %+v", f)
	}

	if _, err := h.eng.Apply("dev", "catch-up"); err != nil {
		t.Fatal(err)
	}
	report, _, _ = h.eng.DetectDrift("dev")
	if report.Drifted {
		t.Fatalf("after re-apply, drift must be cleared: %+v", report.Fields)
	}
	t.Log("通过：修改后精确报出 app.replicas 期望值/实际值，重新应用后漂移清除")
}
