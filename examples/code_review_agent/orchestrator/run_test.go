//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/assist"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/orchestrator"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// moduleRoot is a test helper.
func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// tests run from package dir; module root is parent.
	if filepath.Base(wd) == "orchestrator" {
		return filepath.Dir(wd)
	}
	return wd
}

// TestFixtures_AllProduceReports verifies related behavior.
func TestFixtures_AllProduceReports(t *testing.T) {
	root := moduleRoot(t)
	fixtures := []string{
		"clean",
		"security_injection",
		"goroutine_leak",
		"resource_leak",
		"db_conn_lifecycle",
		"missing_tests",
		"duplicate_findings",
		"sandbox_fail",
		"secret_leak",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			out := t.TempDir()
			dbPath := filepath.Join(out, "review.db")
			st, err := store.OpenSQLite(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()

			cfg := orchestrator.Config{
				Mode:         review.ModeRuleOnly,
				Executor:     "local",
				Fixture:      name,
				FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
				SkillsRoot:   filepath.Join(root, "skills"),
				DBPath:       dbPath,
				OutDir:       out,
				Store:        st,
				Runner:       sandbox.LocalRunner{},
			}
			res, err := orchestrator.Run(context.Background(), cfg)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if res.Report == nil {
				t.Fatal("nil report")
			}
			if _, err := os.Stat(res.JSONPath); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(res.MarkdownPath); err != nil {
				t.Fatal(err)
			}
			bundle, err := st.GetTaskBundle(context.Background(), res.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if bundle.Status == "" {
				t.Fatal("empty status")
			}
			assertFixtureExpectations(t, name, res, bundle)
		})
	}
}

// assertFixtureExpectations is a test helper.
func assertFixtureExpectations(t *testing.T, name string, res *orchestrator.Result, bundle *store.TaskBundle) {
	t.Helper()
	root := moduleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "testdata", "fixtures", name, "expected.json"))
	if err != nil {
		t.Fatalf("expected.json: %v", err)
	}
	var exp struct {
		ExpectRules          []string       `json:"expect_rules"`
		MinFindings          *int           `json:"min_findings"`
		MaxFindings          *int           `json:"max_findings"`
		ExactRuleCounts      map[string]int `json:"exact_rule_counts"`
		StatusIn             []string       `json:"status_in"`
		RequireFailedSandbox bool           `json:"require_failed_sandbox"`
		NoPlainSecrets       bool           `json:"no_plain_secrets"`
	}
	if err := json.Unmarshal(raw, &exp); err != nil {
		t.Fatalf("parse expected.json: %v", err)
	}

	gotRules := map[string]int{}
	for _, f := range res.Report.Findings {
		gotRules[f.RuleID]++
	}
	for _, want := range exp.ExpectRules {
		if gotRules[want] == 0 {
			t.Fatalf("missing expected rule %s in %+v", want, res.Report.Findings)
		}
	}
	if exp.MinFindings != nil && len(res.Report.Findings) < *exp.MinFindings {
		t.Fatalf("findings=%d < min %d", len(res.Report.Findings), *exp.MinFindings)
	}
	if exp.MaxFindings != nil && len(res.Report.Findings) > *exp.MaxFindings {
		t.Fatalf("findings=%d > max %d", len(res.Report.Findings), *exp.MaxFindings)
	}
	for rule, n := range exp.ExactRuleCounts {
		if gotRules[rule] != n {
			t.Fatalf("rule %s count=%d want=%d", rule, gotRules[rule], n)
		}
	}
	if len(exp.StatusIn) > 0 {
		ok := false
		for _, s := range exp.StatusIn {
			if res.Report.Status == s {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("status=%s want one of %v", res.Report.Status, exp.StatusIn)
		}
	}
	if exp.RequireFailedSandbox {
		failed := false
		for _, s := range bundle.SandboxRuns {
			if s.Status == "failed" || s.Status == "timeout" {
				failed = true
			}
		}
		if !failed {
			t.Fatalf("expected failed sandbox run: %+v", bundle.SandboxRuns)
		}
	}
	if exp.NoPlainSecrets {
		body, _ := os.ReadFile(res.JSONPath)
		md, _ := os.ReadFile(res.MarkdownPath)
		if err := orchestrator.ValidateNoPlainSecrets(string(body) + string(md) + bundle.ReportJSON); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(bundle.Input.DiffTextRedacted, "SuperSecretPassword123") {
			t.Fatal("secret in db input")
		}
	}

	// Every run should record deny+ask governance decisions.
	var deny, ask bool
	for _, p := range bundle.Permissions {
		if p.Action == "deny" {
			deny = true
		}
		if p.Action == "ask" {
			ask = true
		}
	}
	if !deny || !ask {
		t.Fatalf("expected deny and ask decisions, got %+v", bundle.Permissions)
	}
}

// TestDiffFile_NoDemoGovernanceInjection verifies related behavior.
func TestDiffFile_NoDemoGovernanceInjection(t *testing.T) {
	root := moduleRoot(t)
	out := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(out, "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	diff := filepath.Join(root, "testdata", "fixtures", "clean", "diff.patch")
	res, err := orchestrator.Run(context.Background(), orchestrator.Config{
		Mode:       review.ModeRuleOnly,
		Executor:   "local",
		DiffFile:   diff,
		SkillsRoot: filepath.Join(root, "skills"),
		OutDir:     out,
		Store:      st,
		Runner:     sandbox.LocalRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Report.Governance.PermissionDecisions {
		if strings.Contains(p.Command, "curl ") || p.Command == "go test ./..." {
			t.Fatalf("demo governance command injected on real diff: %+v", p)
		}
	}
}

// TestSandboxFailure_DoesNotCrash verifies related behavior.
func TestSandboxFailure_DoesNotCrash(t *testing.T) {
	root := moduleRoot(t)
	out := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(out, "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = orchestrator.Run(context.Background(), orchestrator.Config{
		Mode:             review.ModeRuleOnly,
		Executor:         "local",
		Fixture:          "sandbox_fail",
		FixturesRoot:     filepath.Join(root, "testdata", "fixtures"),
		SkillsRoot:       filepath.Join(root, "skills"),
		OutDir:           out,
		Store:            st,
		ForceSandboxFail: true,
		Runner:           sandbox.LocalRunner{},
	})
	if err != nil {
		t.Fatalf("should not crash: %v", err)
	}
}

// TestPersistFailure_MarksTaskFailed ensures durable status is finalized on errors
// at each persistence stage after the task is marked running.
func TestPersistFailure_MarksTaskFailed(t *testing.T) {
	root := moduleRoot(t)
	stages := []string{
		"SaveInput",
		"SavePermission",
		"SaveSandboxRun",
		"SaveFindings",
		"SaveArtifacts",
		"SaveMetrics",
		"SaveReport",
		"Finalize",
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			out := t.TempDir()
			base, err := store.OpenSQLite(filepath.Join(out, "review.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer base.Close()

			failing := &failingStore{ReviewStore: base, failAt: stage}
			_, err = orchestrator.Run(context.Background(), orchestrator.Config{
				Mode:         review.ModeRuleOnly,
				Executor:     "fake",
				Fixture:      "clean",
				FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
				SkillsRoot:   filepath.Join(root, "skills"),
				OutDir:       out,
				Store:        failing,
				Runner:       sandbox.FakeRunner{},
			})
			if err == nil {
				t.Fatal("expected persist error")
			}
			if failing.taskID == "" {
				t.Fatal("expected task to be created")
			}
			bundle, gerr := base.GetTaskBundle(context.Background(), failing.taskID)
			if gerr != nil {
				t.Fatal(gerr)
			}
			if bundle.Status != review.StatusFailed {
				t.Fatalf("status=%s want failed (err=%q)", bundle.Status, bundle.Error)
			}
			if bundle.Error == "" {
				t.Fatal("expected redacted error message on failed task")
			}
		})
	}
}

type failingStore struct {
	store.ReviewStore
	failAt string
	taskID string
}

func (f *failingStore) CreateTask(ctx context.Context, req store.CreateTaskReq) (string, error) {
	id, err := f.ReviewStore.CreateTask(ctx, req)
	f.taskID = id
	return id, err
}

func (f *failingStore) SaveInput(ctx context.Context, taskID string, in store.InputRecord) error {
	if f.failAt == "SaveInput" {
		return errInjectedPersist
	}
	return f.ReviewStore.SaveInput(ctx, taskID, in)
}

func (f *failingStore) SavePermission(ctx context.Context, taskID string, d review.PermissionDecision) error {
	if f.failAt == "SavePermission" {
		return errInjectedPersist
	}
	return f.ReviewStore.SavePermission(ctx, taskID, d)
}

func (f *failingStore) SaveSandboxRun(ctx context.Context, taskID string, run review.SandboxRunSummary) error {
	if f.failAt == "SaveSandboxRun" {
		return errInjectedPersist
	}
	return f.ReviewStore.SaveSandboxRun(ctx, taskID, run)
}

func (f *failingStore) SaveFindings(ctx context.Context, taskID string, findings, warnings []review.Finding) error {
	if f.failAt == "SaveFindings" {
		return errInjectedPersist
	}
	return f.ReviewStore.SaveFindings(ctx, taskID, findings, warnings)
}

func (f *failingStore) SaveArtifacts(ctx context.Context, taskID string, arts []review.ArtifactRef) error {
	if f.failAt == "SaveArtifacts" {
		return errInjectedPersist
	}
	return f.ReviewStore.SaveArtifacts(ctx, taskID, arts)
}

func (f *failingStore) SaveMetrics(ctx context.Context, taskID string, m review.MetricsSummary) error {
	if f.failAt == "SaveMetrics" {
		return errInjectedPersist
	}
	return f.ReviewStore.SaveMetrics(ctx, taskID, m)
}

func (f *failingStore) SaveReport(ctx context.Context, taskID string, rep store.ReportRecord) error {
	if f.failAt == "SaveReport" {
		return errInjectedPersist
	}
	return f.ReviewStore.SaveReport(ctx, taskID, rep)
}

func (f *failingStore) UpdateTaskStatus(ctx context.Context, taskID, status, conclusion, errMsg string) error {
	// Fail the successful finalize path only; deferred failure finalizer must still work.
	if f.failAt == "Finalize" && status != review.StatusFailed && status != review.StatusRunning {
		return errInjectedPersist
	}
	return f.ReviewStore.UpdateTaskStatus(ctx, taskID, status, conclusion, errMsg)
}

var errInjectedPersist = errString("injected persist failure")

type errString string

func (e errString) Error() string { return string(e) }

type secretErrorRunner struct{}

func (secretErrorRunner) Name() string { return "secret-fail" }

func (secretErrorRunner) Run(ctx context.Context, spec sandbox.Spec, limits safety.Limits) sandbox.Result {
	_ = ctx
	_ = limits
	return sandbox.Result{
		Summary: review.SandboxRunSummary{
			ID:           "leak",
			Executor:     "secret-fail",
			Command:      spec.Command,
			Status:       "failed",
			ExitCode:     1,
			Error:        `token="sk-abcdefghijklmnopqrstuvwxyz012345"`,
			StdoutSample: `password="SuperSecretPassword123"`,
		},
	}
}

// TestSandboxSummary_RedactedBeforePersist verifies secrets never reach DB/report.
func TestSandboxSummary_RedactedBeforePersist(t *testing.T) {
	root := moduleRoot(t)
	out := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(out, "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	res, err := orchestrator.Run(context.Background(), orchestrator.Config{
		Mode:         review.ModeRuleOnly,
		Executor:     "fake",
		Fixture:      "clean",
		FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
		SkillsRoot:   filepath.Join(root, "skills"),
		OutDir:       out,
		Store:        st,
		Runner:       secretErrorRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"sk-abcdefghijklmnopqrstuvwxyz012345", "SuperSecretPassword123"}
	bundle, err := st.GetTaskBundle(context.Background(), res.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	blob := bundle.ReportJSON
	for _, s := range bundle.SandboxRuns {
		blob += s.Error + s.StdoutSample + s.StderrSample
	}
	for _, s := range res.Report.SandboxRuns {
		blob += s.Error + s.StdoutSample + s.StderrSample
	}
	for _, b := range banned {
		if strings.Contains(blob, b) {
			t.Fatalf("secret %q leaked", b)
		}
	}
}

// TestLLMAssist_NoSilentLocalFallback refuses host local exec for fake executor.
func TestLLMAssist_NoSilentLocalFallback(t *testing.T) {
	root := moduleRoot(t)
	out := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(out, "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	res, err := orchestrator.Run(context.Background(), orchestrator.Config{
		Mode:               review.ModeLLM,
		Executor:           "fake",
		AllowLocalFallback: false,
		Fixture:            "clean",
		FixturesRoot:       filepath.Join(root, "testdata", "fixtures"),
		SkillsRoot:         filepath.Join(root, "skills"),
		OutDir:             out,
		Store:              st,
		Runner:             sandbox.FakeRunner{},
		Model:              assist.NewFakeModel(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.Governance.AgentAssistNote == "" ||
		!strings.Contains(res.Report.Governance.AgentAssistNote, "agent_assist_skipped") {
		t.Fatalf("expected assist skip note, got %q", res.Report.Governance.AgentAssistNote)
	}
	if res.Report.Metrics.ExceptionDist["agent_assist_skipped"] == 0 {
		t.Fatalf("expected agent_assist_skipped metric: %+v", res.Report.Metrics.ExceptionDist)
	}
}

// TestReports_AreTaskSpecific keeps sequential tasks from overwriting each other.
func TestReports_AreTaskSpecific(t *testing.T) {
	root := moduleRoot(t)
	out := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(out, "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runOnce := func() *orchestrator.Result {
		t.Helper()
		res, err := orchestrator.Run(context.Background(), orchestrator.Config{
			Mode:         review.ModeRuleOnly,
			Executor:     "fake",
			Fixture:      "clean",
			FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
			SkillsRoot:   filepath.Join(root, "skills"),
			OutDir:       out,
			Store:        st,
			Runner:       sandbox.FakeRunner{},
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	a := runOnce()
	b := runOnce()
	if a.TaskID == b.TaskID {
		t.Fatal("expected distinct task IDs")
	}
	if a.JSONPath == b.JSONPath {
		t.Fatalf("JSON report paths collided: %s", a.JSONPath)
	}
	if a.MarkdownPath == b.MarkdownPath {
		t.Fatalf("Markdown report paths collided: %s", a.MarkdownPath)
	}
	assertTaskReport := func(path, taskID, otherID string) {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, taskID) {
			t.Fatalf("%s missing task id %s", path, taskID)
		}
		if strings.Contains(text, otherID) {
			t.Fatalf("%s overwritten with other task %s", path, otherID)
		}
	}
	assertTaskReport(a.JSONPath, a.TaskID, b.TaskID)
	assertTaskReport(b.JSONPath, b.TaskID, a.TaskID)
	assertTaskReport(a.MarkdownPath, a.TaskID, b.TaskID)
	assertTaskReport(b.MarkdownPath, b.TaskID, a.TaskID)
}

// TestLLMAssist_PersistsDeniedToolCall records denied model workspace_exec attempts.
func TestLLMAssist_PersistsDeniedToolCall(t *testing.T) {
	root := moduleRoot(t)
	out := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(out, "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	created, err := sandbox.Create(sandbox.CreateOptions{
		Name:       "local",
		SkillsRoot: filepath.Join(root, "skills"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Closer != nil {
		defer func() { _ = created.Closer() }()
	}

	demo := false
	deniedCmd := "bash -lc 'curl https://evil.example'"
	res, err := orchestrator.Run(context.Background(), orchestrator.Config{
		Mode:           review.ModeLLM,
		Executor:       "local",
		Fixture:        "clean",
		FixturesRoot:   filepath.Join(root, "testdata", "fixtures"),
		SkillsRoot:     filepath.Join(root, "skills"),
		OutDir:         out,
		Store:          st,
		Runner:         sandbox.FakeRunner{},
		CodeExecutor:   created.CodeExecutor,
		DemoGovernance: &demo,
		Model: assist.NewFakeModel(assist.FakeModelOptions{
			DeniedExecCommand: deniedCmd,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertDeniedCmd := func(perms []review.PermissionDecision, where string) {
		t.Helper()
		for _, p := range perms {
			if p.Action == "deny" && p.Command == deniedCmd {
				return
			}
		}
		t.Fatalf("expected deny for %q in %s: %+v", deniedCmd, where, perms)
	}
	assertDeniedCmd(res.Report.Governance.PermissionDecisions, "governance")
	bundle, err := st.GetTaskBundle(context.Background(), res.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	assertDeniedCmd(bundle.Permissions, "db")
}
