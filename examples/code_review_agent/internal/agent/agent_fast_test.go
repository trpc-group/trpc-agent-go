//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/execution"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/llm"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNormalizeExecutionPlan(t *testing.T) {
	trueValue := true
	falseValue := false
	tests := []struct {
		name    string
		req     Request
		want    executionPlan
		wantErr string
	}{
		{name: "canonical default", req: Request{Mode: ModeReview}, want: executionPlan{Mode: ModeReview}},
		{name: "canonical both", req: Request{Mode: ModeReview, SandboxEnabled: &trueValue, ModelEnabled: &trueValue}, want: executionPlan{Mode: ModeReview, SandboxRequested: true, ModelRequested: true}},
		{name: "legacy rule only", req: Request{Mode: ModeRuleOnly}, want: executionPlan{Mode: ModeReview}},
		{name: "legacy sandbox", req: Request{Mode: ModeSandbox}, want: executionPlan{Mode: ModeReview, SandboxRequested: true}},
		{name: "legacy sandbox explicit false", req: Request{Mode: ModeSandbox, SandboxEnabled: &falseValue}, want: executionPlan{Mode: ModeReview}},
		{name: "legacy model", req: Request{Mode: ModeFakeModel}, want: executionPlan{Mode: ModeReview, ModelRequested: true}},
		{name: "legacy model explicit false", req: Request{Mode: ModeFakeModel, ModelEnabled: &falseValue}, want: executionPlan{Mode: ModeReview}},
		{name: "dry run", req: Request{Mode: ModeDryRun}, want: executionPlan{Mode: ModeDryRun}},
		{name: "dry run sandbox conflict", req: Request{Mode: ModeDryRun, SandboxEnabled: &trueValue}, wantErr: "dry-run cannot enable sandbox or model review"},
		{name: "unknown", req: Request{Mode: "fast-and-loose"}, wantErr: `unsupported review mode "fast-and-loose"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeExecutionPlan(tc.req)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize execution plan: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("plan = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestFastParseSkillFindingsRedactsAndDedupes(t *testing.T) {
	secret := "sk-1234567890abcdef"
	item := `{"severity":"critical","category":"security","file":"config.go","line":7,"title":"secret","evidence":"` + secret + `","recommendation":"remove","confidence":"high","source":"skill_run","rule_id":"secret-leak","status":"finding"}`
	result, err := parseSkillFindings(`{"findings":[` + item + `,` + item + `],"warnings":[]}`)
	if err != nil {
		t.Fatalf("parse findings: %v", err)
	}
	if len(result.Findings) != 1 || strings.Contains(result.Findings[0].Evidence, secret) || !strings.Contains(result.Findings[0].Evidence, "[REDACTED]") {
		t.Fatalf("unexpected sanitized findings: %+v", result.Findings)
	}
}

func TestFastSandboxOutputIsRedactedBoundedUTF8(t *testing.T) {
	got := sandboxRunOutput("sk-1234567890abcdef界tail", 16)
	if !utf8.ValidString(got) || len(got) > 16 || strings.Contains(got, "sk-1234567890abcdef") {
		t.Fatalf("unsafe bounded output: %q", got)
	}
}

func TestFastArtifactLimitsRejectUnknownCountAndTotal(t *testing.T) {
	cfg := Config{MaxArtifactBytes: 32, MaxArtifactTotalBytes: 8, MaxArtifactCount: 1}
	for _, artifacts := range [][]artifactPayload{
		{{Name: "../report.json", Data: []byte("x")}},
		{{Name: "review_report.json", Data: []byte("x")}, {Name: "review_report.md", Data: []byte("x")}},
		{{Name: "review_report.json", Data: []byte("123456789")}},
	} {
		if err := enforceArtifactLimits(cfg, artifacts); err == nil {
			t.Fatalf("expected artifact boundary error for %+v", artifacts)
		}
	}
}

func TestFastFinalizeReviewResultIsIdempotentForSandboxFailures(t *testing.T) {
	ctx := reviewResultContext{
		TaskID:    "task-fast",
		StartedAt: time.Now(),
		Runs:      []storage.SandboxRunRecord{{Command: "go test ./...", Status: "failed"}},
	}
	result := finalizeReviewResult(review.Result{}, ctx)
	result = finalizeReviewResult(result, ctx)
	if got := result.Metrics.ExceptionCounts["sandbox_failed"]; got != 1 {
		t.Fatalf("sandbox failure count = %d, want 1", got)
	}
}

func TestFinalizeReviewResultRecordsRequestedAndEnteredCapabilities(t *testing.T) {
	ctx := reviewResultContext{
		StartedAt: time.Now(),
		Plan:      executionPlan{Mode: ModeReview, SandboxRequested: true, ModelRequested: true},
		Runs:      []storage.SandboxRunRecord{{Command: "go test ./...", ExecutionStarted: true, Status: "timed_out"}},
		Model:     llm.RunSummary{CallCount: 1},
	}
	result := finalizeReviewResult(review.Result{}, ctx)
	if result.Metrics.Mode != ModeReview || !result.Metrics.SandboxRequested || !result.Metrics.SandboxExecuted || !result.Metrics.ModelRequested || !result.Metrics.ModelExecuted {
		t.Fatalf("unexpected capability audit: %+v", result.Metrics)
	}
}

func TestSandboxWithoutRepositoryProducesAuditedHumanReview(t *testing.T) {
	result, decision, run := sandboxUnavailableAudit("task-no-repo")
	if decision.Action != "skipped" || run.Status != "skipped" || run.ExecutionStarted {
		t.Fatalf("unexpected sandbox skip audit: decision=%+v run=%+v", decision, run)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Status != "needs_human_review" || result.Warnings[0].RuleID != "sandbox-repo-required" {
		t.Fatalf("unexpected human review result: %+v", result)
	}
}

func TestFastPersistedReviewItemsDedupeAcrossBuckets(t *testing.T) {
	item := review.Finding{File: "service.go", Line: 9, Category: "resource", RuleID: "resource-leak", Status: "needs_human_review"}
	items := persistedReviewItems(review.Result{Warnings: []review.Finding{item}, HumanReviewItems: []review.Finding{item}})
	if len(items) != 1 {
		t.Fatalf("persisted items = %+v, want one deduped item", items)
	}
}

type fastCallableTool struct {
	name string
	call func(context.Context, []byte) (any, error)
}

func (t fastCallableTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func (t fastCallableTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if t.call == nil {
		return nil, nil
	}
	return t.call(ctx, jsonArgs)
}

type fastStore struct {
	saveTaskCalls int
	saveTaskErrAt int
	saveTaskErr   error
	saveReviewErr error
}

func (s *fastStore) SaveTask(context.Context, storage.Task) error {
	s.saveTaskCalls++
	if s.saveTaskErrAt > 0 && s.saveTaskCalls == s.saveTaskErrAt {
		return s.saveTaskErr
	}
	return nil
}

func (*fastStore) SaveFinding(context.Context, string, review.Finding) error {
	return nil
}

func (*fastStore) SaveDecision(context.Context, storage.DecisionRecord) error {
	return nil
}

func (*fastStore) SaveFilterDecision(context.Context, storage.FilterDecisionRecord) error {
	return nil
}

func (*fastStore) SaveSandboxRun(context.Context, storage.SandboxRunRecord) error {
	return nil
}

func (*fastStore) SaveArtifact(context.Context, storage.ArtifactRecord) error {
	return nil
}

func (*fastStore) SaveMetrics(context.Context, storage.MetricsRecord) error {
	return nil
}

func (*fastStore) SaveReport(context.Context, string, []byte, []byte) error {
	return nil
}

func (s *fastStore) SaveReview(context.Context, storage.ReviewRecord) error {
	return s.saveReviewErr
}

func (*fastStore) Close() error {
	return nil
}

func writeFastSkillRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	skillDir := filepath.Join(root, defaultSkillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# code-review\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return root
}

func writeFastDiffFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "input.diff")
	diff := "diff --git a/service.go b/service.go\n--- a/service.go\n+++ b/service.go\n@@ -1 +1 @@\n-old\n+new\n"
	if err := os.WriteFile(path, []byte(diff), 0o644); err != nil {
		t.Fatalf("write diff file: %v", err)
	}
	return path
}

func TestInputConfigMapsMaxInputBytes(t *testing.T) {
	t.Parallel()

	cfg := inputConfig(Config{
		FixturesRoot:  "fixtures",
		MaxInputBytes: 123,
	})
	if cfg.FixturesRoot != "fixtures" || cfg.MaxInputBytes != 123 {
		t.Fatalf("input config = %+v, want fixtures root and max bytes propagated", cfg)
	}
}

func TestNewDryRunDoesNotCreateContainerExecutor(t *testing.T) {
	t.Parallel()

	factoryCalls := 0
	ag, err := New(Config{
		SkillsRoot: writeFastSkillRoot(t),
		Runtime:    RuntimeContainer,
		OutputDir:  t.TempDir(),
		Timeout:    time.Second,
		ExecutorFactory: func(execution.Config) (codeexecutor.CodeExecutor, error) {
			factoryCalls++
			return execution.FakeExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls after New = %d, want 0", factoryCalls)
	}

	_, err = ag.Run(context.Background(), Request{
		DiffFile: writeFastDiffFile(t),
		Mode:     ModeDryRun,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls after dry-run = %d, want 0", factoryCalls)
	}
	if err := ag.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls after unused Close = %d, want 0", factoryCalls)
	}
}

func TestRunDirectJoinsTerminalSaveTaskFailureWithReviewError(t *testing.T) {
	t.Parallel()

	reviewErr := errors.New("save review boom")
	saveErr := errors.New("save task boom")
	ag := &Agent{
		cfg: normalizeConfig(Config{
			OutputDir: t.TempDir(),
			Timeout:   time.Second,
		}),
		loadTool: fastCallableTool{
			name: "skill_load",
			call: func(context.Context, []byte) (any, error) {
				return map[string]any{"loaded": true}, nil
			},
		},
		store: &fastStore{
			saveTaskErrAt: 2,
			saveTaskErr:   saveErr,
			saveReviewErr: reviewErr,
		},
	}

	_, err := ag.runDirect(context.Background(), Request{
		DiffFile: writeFastDiffFile(t),
		Mode:     ModeDryRun,
	})
	if !errors.Is(err, reviewErr) {
		t.Fatalf("error %v does not include review error %v", err, reviewErr)
	}
	if !errors.Is(err, saveErr) {
		t.Fatalf("error %v does not include terminal save error %v", err, saveErr)
	}
}

func TestRunDirectJoinsTerminalSaveTaskFailureWithCancelError(t *testing.T) {
	t.Parallel()

	saveErr := errors.New("save task boom")
	ag := &Agent{
		cfg: normalizeConfig(Config{
			OutputDir: t.TempDir(),
			Timeout:   time.Second,
		}),
		loadTool: fastCallableTool{
			name: "skill_load",
			call: func(ctx context.Context, _ []byte) (any, error) {
				return nil, ctx.Err()
			},
		},
		store: &fastStore{saveTaskErrAt: 2, saveTaskErr: saveErr},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ag.runDirect(ctx, Request{
		DiffFile: writeFastDiffFile(t),
		Mode:     ModeDryRun,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v does not include context cancellation", err)
	}
	if !errors.Is(err, saveErr) {
		t.Fatalf("error %v does not include terminal save error %v", err, saveErr)
	}
}
