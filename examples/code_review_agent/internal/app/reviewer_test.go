//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/governance"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	storemodel "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

func TestReviewerFakeFullRoundTrip(t *testing.T) {
	root := enterExampleRoot(t)
	database := openAppStore(t)
	outputDir := t.TempDir()
	reviewer := testReviewer(database, root, outputDir, DefaultCheckerFactory)
	result, err := reviewer.Run(context.Background(), fixtureConfig(t, "fake"))
	if err != nil {
		t.Fatalf("Reviewer.Run() error = %v", err)
	}
	if result.Review.Task.Status != storemodel.StatusCompleted || len(result.Review.Findings) == 0 ||
		len(result.Review.Runs) != 2 || len(result.Review.Decisions) != 4 || len(result.Review.Artifacts) != 2 {
		t.Fatalf("review = %#v", result.Review)
	}
	if result.Review.Report.JSON == "" || result.Review.Report.Markdown == "" {
		t.Fatal("canonical reports are empty")
	}
	if result.Review.Task.FinishedAt == nil || result.Review.Metrics.TotalDurationMS !=
		result.Review.Task.FinishedAt.Sub(result.Review.Task.StartedAt).Milliseconds() {
		t.Fatalf("duration and task timestamps differ: task=%#v metrics=%#v",
			result.Review.Task, result.Review.Metrics)
	}
	for _, path := range []string{result.Written.JSONPath, result.Written.MarkdownPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("report %q: %v", path, err)
		}
	}
	if strings.Contains(result.Review.Report.JSON, "diff --git") {
		t.Fatal("raw diff was persisted")
	}
	removeWritten(t, result)
}

func TestRunFakeModelLoadsSkillThenRunsReview(t *testing.T) {
	root := enterExampleRoot(t)
	database := openAppStore(t)
	outputDir := t.TempDir()
	config := fixtureConfig(t, "fake")
	config.FakeModel = true
	result, err := RunFakeModel(context.Background(), config,
		testReviewer(database, root, outputDir, DefaultCheckerFactory))
	if err != nil {
		t.Fatalf("RunFakeModel() error = %v", err)
	}
	if result.TaskID == "" || result.Review.Task.Status != storemodel.StatusCompleted {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Review.Decisions) != 4 || len(result.Review.Runs) != 2 {
		t.Fatalf("review = %#v", result.Review)
	}
	removeWritten(t, result)
}

func TestSandboxFailuresCompleteWithWarningsAndAreClassified(t *testing.T) {
	root := enterExampleRoot(t)
	tests := []struct {
		name    string
		checker Checker
		want    map[string]string
	}{
		{"returned errors", failingChecker{}, map[string]string{
			"go-test": "*errors.errorString", "go-vet": "*errors.errorString"}},
		{"dependency cache", dependencyCacheChecker{}, map[string]string{
			"go-test": "dependency_cache", "go-vet": "dependency_cache"}},
		{"status only", statusOnlyChecker{}, map[string]string{
			"go-test": "sandbox_timeout", "go-vet": "sandbox_failed"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openAppStore(t)
			factory := func(governance.Authorizer, CheckerConfig) (Checker, error) {
				return test.checker, nil
			}
			result, err := testReviewer(database, root, t.TempDir(), factory).
				Run(context.Background(), fixtureConfig(t, "fake"))
			if err != nil || result.Review.Task.Status != storemodel.StatusCompletedWithWarnings ||
				len(result.Review.Runs) != len(test.want) {
				t.Fatalf("review = %#v, error = %v", result.Review, err)
			}
			for _, run := range result.Review.Runs {
				if run.Status != "failed" && run.Status != "timeout" || run.ErrorType != test.want[run.CheckID] ||
					result.Review.Metrics.ErrorTypeCounts[run.ErrorType] == 0 {
					t.Fatalf("run = %#v, metrics = %#v", run, result.Review.Metrics)
				}
			}
			removeWritten(t, result)
		})
	}
}

func TestReviewerFinalizeFailureCompensatesAndFailsTask(t *testing.T) {
	root := enterExampleRoot(t)
	database := openAppStore(t)
	store := finalizeFailStore{Store: database}
	outputDir := t.TempDir()
	result, err := testReviewer(store, root, outputDir, DefaultCheckerFactory).
		Run(context.Background(), fixtureConfig(t, "fake"))
	if err == nil {
		t.Fatal("Reviewer.Run() error = nil")
	}
	review, queryErr := database.GetReview(context.Background(), result.TaskID)
	if queryErr != nil || review.Task.Status != storemodel.StatusFailed || review.Task.FinishedAt == nil ||
		review.Metrics.TotalDurationMS != review.Task.FinishedAt.Sub(review.Task.StartedAt).Milliseconds() ||
		review.Metrics.ErrorTypeCounts["terminal_delivery_failure"] != 1 {
		t.Fatalf("failed review = %#v, error = %v", review, queryErr)
	}
	for _, name := range []string{"review_report.json", "review_report.md"} {
		if _, statErr := os.Stat(filepath.Join(outputDir, name)); !os.IsNotExist(statErr) {
			t.Fatalf("compensated report %q remains: %v", name, statErr)
		}
	}
}

func TestCancellationLeavesQueryableFailure(t *testing.T) {
	root := enterExampleRoot(t)
	tests := []struct {
		name, wantError string
		wrap            func(storemodel.Store) storemodel.Store
		wantRuns        int
	}{
		{"persisted run", "", func(store storemodel.Store) storemodel.Store { return store }, 1},
		{"run persistence failure", "save sandbox run", func(store storemodel.Store) storemodel.Store { return saveRunFailStore{Store: store} }, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openAppStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			factory := func(governance.Authorizer, CheckerConfig) (Checker, error) {
				return cancelingChecker{cancel: cancel}, nil
			}
			result, err := testReviewer(test.wrap(database), root, t.TempDir(), factory).Run(ctx, fixtureConfig(t, "fake"))
			if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Reviewer.Run() error = %v", err)
			}
			review, queryErr := database.GetReview(context.Background(), result.TaskID)
			if queryErr != nil || review.Task.Status != storemodel.StatusFailed || len(review.Runs) != test.wantRuns {
				t.Fatalf("failed review = %#v, error = %v", review, queryErr)
			}
			if test.wantRuns == 1 && (review.Runs[0].ErrorType != "*errors.errorString" || review.Metrics.ErrorTypeCounts["*errors.errorString"] == 0) {
				t.Fatalf("run metrics = %#v, %#v", review.Runs, review.Metrics)
			}
		})
	}
}

func TestDefaultCheckerFactoryRejectsImplicitLocal(t *testing.T) {
	if _, err := DefaultCheckerFactory(governance.Authorizer{}, CheckerConfig{Runtime: "local"}); err == nil {
		t.Fatal("DefaultCheckerFactory(local without approval) error = nil")
	}
}

type failingChecker struct{}

func (failingChecker) Check(_ context.Context, checkID, _ string, _ time.Duration) (sandbox.Run, error) {
	return sandbox.Run{CheckID: checkID, Runtime: "fake", Status: "failed", ExitCode: -1,
		Duration: time.Millisecond}, errors.New("injected sandbox failure")
}

type dependencyCacheChecker struct{}

func (dependencyCacheChecker) Check(_ context.Context, checkID, _ string, _ time.Duration) (sandbox.Run, error) {
	return sandbox.Run{CheckID: checkID, Runtime: "container", Status: "failed", ExitCode: -1,
		Duration: time.Millisecond}, fmt.Errorf("%w: cache miss", sandbox.ErrDependencyCache)
}

type statusOnlyChecker struct{}

func (statusOnlyChecker) Check(_ context.Context, checkID, _ string, _ time.Duration) (sandbox.Run, error) {
	if checkID == "go-test" {
		return sandbox.Run{CheckID: checkID, Runtime: "fake", Status: "timeout", ExitCode: -1, TimedOut: true, Duration: time.Millisecond}, nil
	}
	return sandbox.Run{CheckID: checkID, Runtime: "fake", Status: "failed", ExitCode: 1, Duration: time.Millisecond}, nil
}

type cancelingChecker struct{ cancel context.CancelFunc }

func (c cancelingChecker) Check(_ context.Context, checkID, _ string, _ time.Duration) (sandbox.Run, error) {
	c.cancel()
	return sandbox.Run{CheckID: checkID, Runtime: "fake", Status: "failed", ExitCode: -1},
		context.Canceled
}

type finalizeFailStore struct{ storemodel.Store }

type saveRunFailStore struct{ storemodel.Store }

func (saveRunFailStore) SaveRun(context.Context, string, storemodel.SandboxRun) error {
	return errors.New("injected save run failure")
}

func (finalizeFailStore) Finalize(context.Context, storemodel.FinalizeRequest) error {
	return errors.New("injected finalize failure")
}

func testReviewer(store storemodel.Store, root, outputDir string, factory CheckerFactory) *Reviewer {
	return &Reviewer{Store: store, OutputDir: outputDir, BuildContext: root, CheckerFactory: factory}
}

func removeWritten(t *testing.T, result Result) {
	t.Helper()
	if err := result.Written.Remove(); err != nil {
		t.Fatalf("Written.Remove() error = %v", err)
	}
}

func fixtureConfig(t *testing.T, runtimeName string) input.Config {
	t.Helper()
	config, err := input.ParseConfig([]string{"--fixture", "composite", "--runtime", runtimeName,
		"--skills-root", "skills", "--output-dir", t.TempDir()})
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	return config
}

func TestTrackerMetrics(t *testing.T) {
	started := time.Unix(100, 0)
	tracker := Start(started)
	tracker.RecordToolCall()
	tracker.RecordDecision("filter", "deny")
	tracker.RecordDecision("permission", "allow")
	tracker.RecordDecision("permission", "deny")
	tracker.RecordRun(12*time.Millisecond, "sandbox_timeout")
	findings := []reviewmodel.Finding{{Severity: "high"}, {Severity: "high"}, {Severity: "medium"}}
	metrics := tracker.Finish(started.Add(20*time.Millisecond), findings, errors.New("failed"))
	second := tracker.Finish(started.Add(time.Second), nil, nil)
	if metrics.ToolCalls != 1 || metrics.PermissionBlocks != 1 || metrics.SandboxDurationMS != 12 ||
		metrics.TotalDurationMS != 20 || metrics.FindingCount != 3 ||
		metrics.SeverityCounts["high"] != 2 || second.FindingCount != 3 {
		t.Fatalf("metrics = %#v, second = %#v", metrics, second)
	}
}

func TestSnapshotFreezesPersistedMetrics(t *testing.T) {
	started := time.Unix(200, 0)
	tracker := Start(started)
	findings := []reviewmodel.Finding{{Severity: "high"}, {Severity: "high"}}
	snapshot := tracker.Snapshot(started.Add(2*time.Millisecond), findings)
	finished := tracker.Finish(started.Add(time.Second), nil, errors.New("delivery failed"))
	if snapshot.TotalDurationMS != 2 || finished.TotalDurationMS != snapshot.TotalDurationMS || finished.FindingCount != 2 ||
		finished.SeverityCounts["high"] != 2 || finished.SeverityCounts["medium"] != 0 ||
		len(finished.ErrorTypeCounts) != 0 {
		t.Fatalf("snapshot = %#v, finished = %#v", snapshot, finished)
	}
}

func TestLoadBundledManifest(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "skills")
	manifest, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(manifest.Rules) != len(requiredRules) {
		t.Fatalf("got %d rules", len(manifest.Rules))
	}
}

func TestLoadRequiresRunnerSource(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "code-review")
	for _, path := range []string{"docs", "rules", "scripts/checkrunner"} {
		if err := os.MkdirAll(filepath.Join(dir, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: code-review\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("Load() error = nil")
	}
}

func TestValidateRuleRejectsInvalidFields(t *testing.T) {
	valid := Rule{ID: "GO-TEST-001", Category: "test", Severity: "medium", Confidence: 0.5,
		Modes: []string{"ast"}, Implementation: "tests", Enabled: true}
	tests := []struct {
		name   string
		mutate func(*Rule)
	}{
		{"empty", func(rule *Rule) { rule.ID = "" }},
		{"confidence", func(rule *Rule) { rule.Confidence = 2 }},
		{"severity", func(rule *Rule) { rule.Severity = "urgent" }},
		{"no modes", func(rule *Rule) { rule.Modes = nil }},
		{"bad mode", func(rule *Rule) { rule.Modes = []string{"shell"} }},
		{"implementation", func(rule *Rule) { rule.Implementation = "shell" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := valid
			test.mutate(&rule)
			if err := validateRule(rule); err == nil {
				t.Fatal("validateRule() error = nil")
			}
		})
	}
}

func TestValidateRulesRequiredAndDuplicate(t *testing.T) {
	rules := completeRules()
	if err := validateRules(rules); err != nil {
		t.Fatalf("validateRules() error = %v", err)
	}
	if err := validateRules(append(rules, rules[0])); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := validateRules(rules[1:]); err == nil {
		t.Fatal("missing required rule accepted")
	}
	rules[0].Enabled = false
	if err := validateRules(rules); err != nil {
		t.Fatalf("disabled rule rejected: %v", err)
	}
	for index := range rules {
		rules[index].Enabled = index < 3
	}
	if err := validateRules(rules); err == nil {
		t.Fatal("insufficient enabled categories accepted")
	}
}

func completeRules() []Rule {
	rules := make([]Rule, 0, len(requiredRules))
	for id := range requiredRules {
		rules = append(rules, Rule{ID: id, Category: requiredImplementations[id], Severity: "medium", Confidence: 0.5,
			Modes: []string{"ast"}, Implementation: requiredImplementations[id], Enabled: true})
	}
	return rules
}

func openAppStore(t *testing.T) *storemodel.SQLiteStore {
	t.Helper()
	database, err := storemodel.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})
	return database
}

func enterExampleRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	root, err := filepath.Abs(filepath.Join(current, "..", ".."))
	if err != nil {
		t.Fatalf("resolve example root: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir(example root) error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(current); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	return root
}
