//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/domain"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/governance"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRunFakeFullFlowProducesReportsAndAudit(t *testing.T) {
	dir := t.TempDir()
	diff := filepath.Join(dir, "fixture.diff")
	if err := os.WriteFile(diff, []byte("diff --git a/app.go b/app.go\n--- a/app.go\n+++ b/app.go\n@@ -1,1 +1,3 @@\n package app\n+const token = \"fixture-secret-value-github-token\"\n+func run(user string) { exec.Command(\"sh\", \"-c\", user) }\ndiff --git a/app_test.go b/app_test.go\n--- a/app_test.go\n+++ b/app_test.go\n@@ -1,1 +1,2 @@\n package app\n+func TestRun(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	db := filepath.Join(dir, "audit.db")
	result, err := Run(Config{DiffFile: diff, Runtime: RuntimeFake, Mode: ModeRuleOnly, OutDir: out, DBPath: db})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Status != domain.StatusCompleted {
		t.Fatalf("status = %s", result.Status)
	}
	for _, name := range []string{"review_report.json", "review_report.md"} {
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if bytes.Contains(data, []byte("fixture-secret-value")) {
			t.Fatalf("%s leaked secret", name)
		}
	}
	manifestPath := filepath.Join(out, "acceptance_manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read acceptance manifest: %v", err)
	}
	var manifest struct {
		TaskID    string            `json:"task_id"`
		Status    domain.Status     `json:"status"`
		Metrics   map[string]int    `json:"metrics"`
		Artifacts []reportArtifact  `json:"artifacts"`
		Checks    map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse acceptance manifest: %v\n%s", err, manifestBytes)
	}
	if manifest.TaskID != result.TaskID || manifest.Status != result.Status || len(manifest.Artifacts) != 3 || manifest.Checks["sandbox"] == "" {
		t.Fatalf("unexpected acceptance manifest: %#v", manifest)
	}
	st, err := storage.OpenSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var artifactRows int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM artifacts WHERE task_id=? AND path=?", result.TaskID, "acceptance_manifest.json").Scan(&artifactRows); err != nil {
		t.Fatal(err)
	}
	if artifactRows != 1 {
		t.Fatalf("acceptance manifest artifact rows = %d", artifactRows)
	}
}

type reportArtifact struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	ContentType string `json:"content_type"`
	Durable     bool   `json:"durable"`
}

func TestRunAllPublicFixtures(t *testing.T) {
	fixtures := map[string]domain.Status{
		"clean":             domain.StatusCompleted,
		"security":          domain.StatusCompleted,
		"goroutine_context": domain.StatusCompleted,
		"resource":          domain.StatusCompleted,
		"database":          domain.StatusCompleted,
		"test_missing":      domain.StatusNeedsHumanReview,
		"duplicate":         domain.StatusCompleted,
		"sandbox_failure":   domain.StatusCompleted,
		"secret_redaction":  domain.StatusCompleted,
	}
	for name, wantStatus := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			result, err := Run(Config{Fixture: name, Runtime: RuntimeFake, Mode: ModeRuleOnly, OutDir: dir, DBPath: filepath.Join(dir, "audit.db")})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if result.Status != wantStatus {
				t.Fatalf("status = %s, want %s", result.Status, wantStatus)
			}
			for _, name := range []string{"review_report.json", "review_report.md"} {
				data, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatalf("read %s: %v", name, err)
				}
				if bytes.Contains(data, []byte("fixture-secret-value")) {
					t.Fatalf("%s leaked secret", name)
				}
			}
		})
	}
}

func TestLocalRuntimeDoesNotFallbackToFake(t *testing.T) {
	_, err := Run(Config{Fixture: "clean", Runtime: RuntimeLocal, AllowLocal: true, OutDir: t.TempDir()})
	if err == nil {
		t.Fatalf("local runtime fell back to fake")
	}
}

func TestEarlyInputFailurePersistsReloadableFailedReport(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "audit.db")
	_, err := Run(Config{DiffFile: filepath.Join(dir, "missing.diff"), Runtime: RuntimeFake, OutDir: dir, DBPath: db, TaskID: "early-failure"})
	if err == nil {
		t.Fatalf("missing diff file succeeded")
	}
	st, openErr := storage.OpenSQLite(db)
	if openErr != nil {
		t.Fatalf("open audit db: %v", openErr)
	}
	defer st.Close()
	reloaded, getErr := st.GetReview("early-failure")
	if getErr != nil {
		t.Fatalf("reload early failure: %v", getErr)
	}
	if reloaded.Status != domain.StatusFailed || reloaded.Metrics["input_error"] != 1 || len(reloaded.ParserWarnings) == 0 {
		t.Fatalf("unexpected early failure report: %#v", reloaded)
	}
}

func TestPermissionDenyAndAskPersistWithoutCreatingRuntime(t *testing.T) {
	for _, action := range []tool.PermissionAction{tool.PermissionActionDeny, tool.PermissionActionAsk} {
		t.Run(string(action), func(t *testing.T) {
			dir := t.TempDir()
			repo := newChangedRepo(t)
			db := filepath.Join(dir, "audit.db")
			created := 0
			result, err := runWithRuntimeFactory(Config{
				RepoPath: repo, Runtime: RuntimeFake, Mode: ModeRuleOnly,
				OutDir: dir, DBPath: db, TaskID: "permission-" + string(action),
				PermissionAction: action, PermissionReason: "policy test",
			}, func(Config) (sandbox.Runtime, error) {
				created++
				return sandbox.NewFakeRuntime(), nil
			})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if created != 0 {
				t.Fatalf("runtime create count = %d, want 0", created)
			}
			if result.Status != domain.StatusNeedsHumanReview || len(result.SandboxRuns) != 0 {
				t.Fatalf("unexpected result: %#v", result)
			}
			if len(result.Governance) != 3 {
				t.Fatalf("governance decisions = %d, want 3", len(result.Governance))
			}
			st, err := storage.OpenSQLite(db)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			reloaded, err := st.GetReview(result.TaskID)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if len(reloaded.Governance) != 3 || len(reloaded.SandboxRuns) != 0 {
				t.Fatalf("persisted audit incomplete: %#v", reloaded)
			}
			var decisions, runs int
			if err := st.DB().QueryRow("SELECT COUNT(*) FROM governance_decisions WHERE task_id=?", result.TaskID).Scan(&decisions); err != nil {
				t.Fatal(err)
			}
			if err := st.DB().QueryRow("SELECT COUNT(*) FROM sandbox_runs WHERE task_id=?", result.TaskID).Scan(&runs); err != nil {
				t.Fatal(err)
			}
			if decisions != 3 || runs != 0 {
				t.Fatalf("database counts: decisions=%d runs=%d", decisions, runs)
			}
		})
	}
}

func TestStagingFailureNeverRunsAndStillClosesRuntime(t *testing.T) {
	dir := t.TempDir()
	repo := newChangedRepo(t)
	rt := &failingStageRuntime{}
	result, err := runWithRuntimeFactory(Config{
		RepoPath: repo, Runtime: RuntimeFake, Mode: ModeRuleOnly,
		OutDir: dir, DBPath: filepath.Join(dir, "audit.db"), TaskID: "stage-failure",
	}, func(Config) (sandbox.Runtime, error) { return rt, nil })
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Status != domain.StatusNeedsHumanReview {
		t.Fatalf("status = %s", result.Status)
	}
	if rt.stage != 1 || rt.run != 0 || rt.close != 1 {
		t.Fatalf("runtime counts: stage=%d run=%d close=%d", rt.stage, rt.run, rt.close)
	}
}

func TestSandboxAndGovernanceSecretsAreRedactedAcrossDurableSinks(t *testing.T) {
	dir := t.TempDir()
	repo := newChangedRepo(t)
	db := filepath.Join(dir, "audit.db")
	canaries := [][]byte{
		[]byte("fixture-secret-value-sandbox-stdout"),
		[]byte("fixture-secret-value-sandbox-stderr"),
		[]byte("fixture-secret-value-governance"),
	}
	result, err := runWithRuntimeFactory(Config{
		RepoPath: repo, Runtime: RuntimeFake, Mode: ModeRuleOnly,
		OutDir: dir, DBPath: db, TaskID: "redact-sandbox-governance",
		PermissionReason: "token = \"fixture-secret-value-governance\"",
	}, func(Config) (sandbox.Runtime, error) {
		rt := sandbox.NewFakeRuntime()
		rt.Enqueue(sandbox.Result{CommandID: governance.CommandGoTest, Stdout: "token = \"fixture-secret-value-sandbox-stdout\"", ExitCode: 0})
		rt.Enqueue(sandbox.Result{CommandID: governance.CommandGoVet, Stderr: "password = \"fixture-secret-value-sandbox-stderr\"", ExitCode: 0})
		rt.Enqueue(sandbox.Result{CommandID: governance.CommandStaticcheck, Stderr: "dependency_unavailable: staticcheck\n", ExitCode: 3})
		return rt, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Status != domain.StatusNeedsHumanReview || result.Metrics["dependency_unavailable"] != 1 {
		t.Fatalf("dependency unavailable not reflected: %#v", result)
	}
	if bytes.Contains([]byte(strings.Join(result.Governance, "\n")), []byte("fixture-secret-value")) {
		t.Fatalf("returned governance leaked secret: %#v", result.Governance)
	}
	for _, run := range result.SandboxRuns {
		if bytes.Contains([]byte(run.Stdout+run.Stderr), []byte("fixture-secret-value")) {
			t.Fatalf("returned sandbox run leaked secret: %#v", run)
		}
	}
	for _, sink := range []string{filepath.Join(dir, "review_report.json"), filepath.Join(dir, "review_report.md"), db, db + "-wal", db + "-shm"} {
		raw, readErr := os.ReadFile(sink)
		if readErr != nil {
			if os.IsNotExist(readErr) && strings.HasSuffix(sink, "-wal") || os.IsNotExist(readErr) && strings.HasSuffix(sink, "-shm") {
				continue
			}
			t.Fatalf("read sink %s: %v", sink, readErr)
		}
		for _, canary := range canaries {
			if bytes.Contains(raw, canary) {
				t.Fatalf("%s leaked canary %q", sink, canary)
			}
		}
	}
}

func TestAgentLoadsBundledSkillThenCallsPermissionBoundWorkspaceTool(t *testing.T) {
	skillPath, err := bundledSkillPath()
	if err != nil {
		t.Fatal(err)
	}
	audit, err := runAgentIntegration(context.Background(), skillPath, "approved-plan", tool.PermissionActionAllow, nil)
	if err != nil {
		t.Fatalf("agent integration: %v", err)
	}
	if !audit.SkillLoaded || !audit.BundledContentSeen || !audit.WorkspaceCalled {
		t.Fatalf("incomplete agent/skill/workspace path: %#v", audit)
	}
	want := []string{"skill_load", agentWorkspaceToolName}
	if len(audit.PermissionCalls) != len(want) {
		t.Fatalf("permission calls = %v, want %v", audit.PermissionCalls, want)
	}
	for i := range want {
		if audit.PermissionCalls[i] != want[i] {
			t.Fatalf("permission calls = %v, want %v", audit.PermissionCalls, want)
		}
	}
}

func TestAgentFrameworkPermissionDenyAndAskSkipWorkspaceTool(t *testing.T) {
	skillPath, err := bundledSkillPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []tool.PermissionAction{tool.PermissionActionDeny, tool.PermissionActionAsk} {
		t.Run(string(action), func(t *testing.T) {
			recorded := 0
			audit, err := runAgentIntegration(context.Background(), skillPath, "approved-plan", action, func(_, _ string) error {
				recorded++
				return nil
			})
			if err != nil {
				t.Fatalf("agent integration: %v", err)
			}
			if audit.SkillLoaded || audit.WorkspaceCalled || recorded != 2 || len(audit.PermissionCalls) != 2 {
				t.Fatalf("unexpected denied agent audit: %#v recorded=%d", audit, recorded)
			}
		})
	}
}

func TestRunAgentModeCannotUseTargetRepositorySkill(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, repo, "go.mod", "module example.com/target\n\ngo 1.24\n")
	writeFile(t, repo, "app.go", "package target\n")
	writeFile(t, repo, "skills/code-review/SKILL.md", "MALICIOUS TARGET SKILL\n")
	writeFile(t, repo, "skills/code-review/scripts/run_checks.sh", "#!/bin/sh\necho MALICIOUS\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "initial")
	writeFile(t, repo, "app.go", "package target\n\nconst changed = true\n")

	diffBytes, err := gitDiff(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, cleanup, err := buildSandboxSnapshot(Config{RepoPath: repo}, diffBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if strings.HasPrefix(snap.SkillPath, repo+string(os.PathSeparator)) {
		t.Fatalf("target repository became skill root: %s", snap.SkillPath)
	}

	out := t.TempDir()
	result, err := Run(Config{
		RepoPath: repo, Runtime: RuntimeFake, Mode: ModeAgent,
		OutDir: out, DBPath: filepath.Join(out, "audit.db"), TaskID: "agent-target-skill",
	})
	if err != nil {
		t.Fatalf("agent mode run: %v", err)
	}
	if result.Status != domain.StatusNeedsHumanReview {
		t.Fatalf("status = %s, want needs_human_review for missing tests", result.Status)
	}
	if len(result.Governance) != 5 {
		t.Fatalf("governance decisions = %d, want 5", len(result.Governance))
	}
	raw, err := os.ReadFile(filepath.Join(out, "review_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("MALICIOUS")) {
		t.Fatalf("target skill content reached report")
	}
}

func TestRunRepoPathUsesTrackedGitDiffAndAudit(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, repo, ".gitignore", "*.log\n.cache/\n")
	writeFile(t, repo, "go.mod", "module example.com/repo\n")
	writeFile(t, repo, "app.go", "package repo\n")
	runGit(t, repo, "add", ".gitignore", "go.mod", "app.go")
	runGit(t, repo, "commit", "-m", "initial")
	writeFile(t, repo, "app.go", "package repo\n\nfunc run(user string) { exec.Command(\"sh\", \"-c\", user) }\n")
	writeFile(t, repo, "ignored.log", "fixture-secret-value-github-token")
	writeFile(t, repo, "untracked.go", "package repo\nconst token = \"fixture-secret-value-github-token\"\n")

	dir := t.TempDir()
	result, err := Run(Config{RepoPath: repo, Runtime: RuntimeFake, Mode: ModeRuleOnly, OutDir: dir, DBPath: filepath.Join(dir, "audit.db"), TaskID: "repo-task"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.TaskID != "repo-task" || len(result.Findings) == 0 {
		t.Fatalf("unexpected repo result: %#v", result)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "review_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("fixture-secret-value")) {
		t.Fatalf("repo mode report included ignored/untracked canary")
	}
}

func TestGitDiffIncludesStagedAndUnstagedChangesWithLiteralFileSelection(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, repo, "both[1].go", "package repo\n")
	writeFile(t, repo, "other.go", "package repo\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "initial")
	writeFile(t, repo, "both[1].go", "package repo\nconst staged = 1\n")
	runGit(t, repo, "add", "--", "both[1].go")
	writeFile(t, repo, "both[1].go", "package repo\nconst staged = 1\nconst unstaged = 2\n")
	writeFile(t, repo, "other.go", "package repo\nconst excluded = 3\n")

	got, err := gitDiff(repo, []string{"both[1].go"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("staged = 1")) || !bytes.Contains(got, []byte("unstaged = 2")) {
		t.Fatalf("combined diff missing staged or unstaged change:\n%s", got)
	}
	if bytes.Contains(got, []byte("excluded = 3")) {
		t.Fatalf("literal file selection included another file:\n%s", got)
	}
}

func TestValidateConfigFileSelectionAndExclusiveInputs(t *testing.T) {
	if err := validateConfig(Config{RepoPath: ".", DiffFile: "x.diff"}); err == nil {
		t.Fatalf("multiple input modes accepted")
	}
	if err := validateConfig(Config{DiffFile: "x.diff", Files: []string{"a.go"}}); err == nil {
		t.Fatalf("file selection accepted without repo path")
	}
	if err := validateConfig(Config{RepoPath: ".", Files: []string{"../escape.go"}}); err == nil {
		t.Fatalf("unsafe selected path accepted")
	}
}

func TestDiffOnlyInputSkipsRepositorySandboxCommands(t *testing.T) {
	dir := t.TempDir()
	diff := filepath.Join(dir, "fixture.diff")
	if err := os.WriteFile(diff, []byte("diff --git a/app.go b/app.go\n--- a/app.go\n+++ b/app.go\n@@ -1 +1,2 @@\n package app\n+func Added() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := 0
	result, err := runWithRuntimeFactory(Config{
		DiffFile: diff, Runtime: RuntimeFake, Mode: ModeRuleOnly,
		OutDir: dir, DBPath: filepath.Join(dir, "audit.db"), TaskID: "diff-only",
	}, func(Config) (sandbox.Runtime, error) {
		created++
		return sandbox.NewFakeRuntime(), nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if created != 0 || len(result.SandboxRuns) != 0 {
		t.Fatalf("diff-only input used repository sandbox: created=%d runs=%d", created, len(result.SandboxRuns))
	}
	if result.Metrics["sandbox_skipped_no_repository"] != 1 {
		t.Fatalf("missing sandbox skip metric: %#v", result.Metrics)
	}
}

func TestBuildSandboxSnapshotRejectsRepositoryChangeAfterDiff(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, repo, "go.mod", "module example.com/repo\n")
	writeFile(t, repo, "app.go", "package repo\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "initial")
	writeFile(t, repo, "app.go", "package repo\nconst before = 1\n")
	diffBytes, err := gitDiff(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "app.go", "package repo\nconst after = 2\n")
	_, cleanup, err := buildSandboxSnapshot(Config{RepoPath: repo}, diffBytes)
	if cleanup != nil {
		defer cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "repository changed after diff collection") {
		t.Fatalf("snapshot accepted changed repository: %v", err)
	}
}

func TestPlannedCommandsUseNestedModulePackageCWD(t *testing.T) {
	snap := sandbox.Snapshot{Path: "/snapshot", Digest: "snapshot", SkillDigest: "skill", ScriptDigest: "script"}
	parsed := input.Diff{Files: []input.FileDiff{{NewPath: "cmd/app/main.go"}, {NewPath: "internal/lib/lib.go"}}}
	plans := plannedCommands(snap, RuntimeFake, parsed, []string{"go.mod", "cmd/app/go.mod"})
	got := map[string]map[string][]string{}
	for _, planned := range plans {
		if got[planned.Plan.Cwd] == nil {
			got[planned.Plan.Cwd] = map[string][]string{}
		}
		got[planned.Plan.Cwd][planned.Plan.CommandID] = planned.Plan.Args
	}
	assertPlanArgs(t, got, "work/repo", governance.CommandGoTest, []string{"test", "./internal/lib"})
	assertPlanArgs(t, got, "work/repo/cmd/app", governance.CommandGoTest, []string{"test", "."})
	assertPlanArgs(t, got, "work/repo", governance.CommandGoVet, []string{"vet", "./internal/lib"})
	assertPlanArgs(t, got, "work/repo/cmd/app", governance.CommandStaticcheck, []string{"staticcheck", "."})
}

func assertPlanArgs(t *testing.T, got map[string]map[string][]string, cwd, commandID string, want []string) {
	t.Helper()
	args := got[cwd][commandID]
	if len(args) != len(want) {
		t.Fatalf("%s %s args = %v, want %v", cwd, commandID, args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("%s %s args = %v, want %v", cwd, commandID, args, want)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_EXTERNAL_DIFF=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newChangedRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, repo, "go.mod", "module example.com/repo\n")
	writeFile(t, repo, "app.go", "package repo\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "initial")
	writeFile(t, repo, "app.go", "package repo\nconst changed = true\n")
	return repo
}

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

type failingStageRuntime struct {
	stage int
	run   int
	close int
}

func (r *failingStageRuntime) Stage(context.Context, sandbox.Snapshot) error {
	r.stage++
	return fmt.Errorf("injected stage failure")
}

func (r *failingStageRuntime) Run(context.Context, sandbox.Command) (sandbox.Result, error) {
	r.run++
	return sandbox.Result{}, nil
}

func (r *failingStageRuntime) Cleanup(context.Context) error { return nil }

func (r *failingStageRuntime) Close() error {
	r.close++
	return nil
}
