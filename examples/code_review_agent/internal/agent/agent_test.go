//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/execution"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/llm"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage/sqlite"
	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
	telemetrytrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const testReviewTimeout = 10 * time.Second

// TestAgentRunUsesFrameworkSkillPermissionExecutorAndStore 固定最小审查链路。
func TestAgentRunUsesFrameworkSkillPermissionExecutorAndStore(t *testing.T) {

	root := repoRoot(t)
	dbPath := filepath.Join(t.TempDir(), "review.db")
	outDir := t.TempDir()

	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		SQLitePath: dbPath,
		OutputDir:  outDir,
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "secret.diff"),
		Mode:     ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.TaskID == "" {
		t.Fatalf("TaskID is empty")
	}
	if len(result.Findings) == 0 {
		t.Fatalf("expected at least one finding from skill script")
	}
	if result.Metrics.ToolCallCount == 0 {
		t.Fatalf("expected framework tool calls to be counted")
	}

	jsonReport, err := os.ReadFile(filepath.Join(outDir, "review_report.json"))
	if err != nil {
		t.Fatalf("read json report: %v", err)
	}
	if strings.Contains(string(jsonReport), "sk-1234567890abcdef") {
		t.Fatalf("json report leaked raw secret: %s", jsonReport)
	}
	for _, want := range []string{
		"\"governance_summary\"",
		"\"sandbox_summary\"",
		"\"artifacts\"",
		"\"human_review_items\"",
		"\"conclusion\"",
	} {
		if !strings.Contains(string(jsonReport), want) {
			t.Fatalf("expected json report to include %s, got %s", want, jsonReport)
		}
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()

	if _, err := store.TaskByID(context.Background(), result.TaskID); err != nil {
		t.Fatalf("load task: %v", err)
	}
	findings, err := store.FindingsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load findings: %v", err)
	}
	if len(findings) == 0 {
		t.Fatalf("expected persisted findings")
	}
	if strings.Contains(findings[0].Evidence, "sk-1234567890abcdef") {
		t.Fatalf("sqlite finding leaked raw secret: %+v", findings[0])
	}

	decisions, err := store.DecisionsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load permission decisions: %v", err)
	}
	if len(decisions) == 0 || decisions[0].Action != "allow" {
		t.Fatalf("expected allow permission decision, got %+v", decisions)
	}

	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	if len(runs) == 0 || runs[0].TimeoutMS == 0 || runs[0].OutputLimitBytes == 0 {
		t.Fatalf("expected bounded sandbox run record, got %+v", runs)
	}
	if runs[0].EnvWhitelist != sandboxEnvWhitelist {
		t.Fatalf("expected env whitelist %q, got %+v", sandboxEnvWhitelist, runs[0])
	}
	if runs[0].FinishedAt.IsZero() || runs[0].ArtifactCount != 4 {
		t.Fatalf("expected sandbox audit completion fields, got %+v", runs[0])
	}
	filterDecisions, err := store.FilterDecisionsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load filter decisions: %v", err)
	}
	if len(filterDecisions) == 0 || filterDecisions[0].Action != "redact" {
		t.Fatalf("expected redaction filter decision, got %+v", filterDecisions)
	}
	artifacts, err := store.ArtifactsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load artifacts: %v", err)
	}
	if len(artifacts) < 2 || artifacts[0].Name == "" {
		t.Fatalf("expected report artifacts, got %+v", artifacts)
	}
	for _, artifact := range artifacts {
		if artifact.Size == 0 {
			t.Fatalf("expected artifact size to be persisted, got %+v", artifacts)
		}
	}
}

// TestLocalFallbackExecutorsUseIsolatedWorkDirs 固定并发评测时本地执行目录隔离。
func TestLocalFallbackExecutorsUseIsolatedWorkDirs(t *testing.T) {

	first, err := execution.NewExecutor(execution.Config{Runtime: RuntimeLocalFallback, Timeout: testReviewTimeout})
	if err != nil {
		t.Fatalf("execution.NewExecutor first returned error: %v", err)
	}
	second, err := execution.NewExecutor(execution.Config{Runtime: RuntimeLocalFallback, Timeout: testReviewTimeout})
	if err != nil {
		t.Fatalf("execution.NewExecutor second returned error: %v", err)
	}
	firstLocal, ok := first.(*localexec.CodeExecutor)
	if !ok {
		t.Fatalf("expected local executor, got %T", first)
	}
	secondLocal, ok := second.(*localexec.CodeExecutor)
	if !ok {
		t.Fatalf("expected local executor, got %T", second)
	}
	if firstLocal.WorkDir == "" || secondLocal.WorkDir == "" {
		t.Fatalf("expected non-empty work dirs, got %q and %q", firstLocal.WorkDir, secondLocal.WorkDir)
	}
	if firstLocal.WorkDir == secondLocal.WorkDir {
		t.Fatalf("local fallback executors should not share work dir %q", firstLocal.WorkDir)
	}
}

// TestAgentRunDoesNotPersistRawSecretsInSQLite 固定明文密钥不落库。
func TestAgentRunDoesNotPersistRawSecretsInSQLite(t *testing.T) {

	root := repoRoot(t)
	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot:   filepath.Join(root, "skills"),
		FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
		Runtime:      RuntimeLocalFallback,
		SQLitePath:   dbPath,
		OutputDir:    t.TempDir(),
		Timeout:      testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	if _, err := ag.Run(context.Background(), Request{
		Fixture: "secret.diff",
		Mode:    ModeRuleOnly,
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	defer db.Close()

	leaks, err := scanSQLiteForRawSecrets(context.Background(), db, []string{
		"sk-1234567890abcdef",
	})
	if err != nil {
		t.Fatalf("scan sqlite: %v", err)
	}
	if len(leaks) > 0 {
		t.Fatalf("sqlite persisted raw secrets: %s", strings.Join(leaks, ", "))
	}
}

// TestAgentRunRedactsCommonSecretShapesInReportsAndSQLite 固定常见密钥不会进入报告和数据库。
func TestAgentRunRedactsCommonSecretShapesInReportsAndSQLite(t *testing.T) {

	root := repoRoot(t)
	outDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	diffPath := filepath.Join(t.TempDir(), "secrets.diff")
	rawSecrets := []string{
		"sk-1234567890abcdef",
		"llm-live-1234567890abcdef",
		"sk-proj-1234567890abcdef",
		"abc.def.ghi",
		"plain-password",
		"ghp_1234567890abcdef1234567890abcdef1234",
		"github_pat_1234567890abcdef1234567890abcdef",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature",
		"-----BEGIN PRIVATE KEY-----",
		"db-password-123",
	}
	diff := `diff --git a/leak.go b/leak.go
--- /dev/null
+++ b/leak.go
@@ -0,0 +1,14 @@
+package redactiondemo
+
+const apiKey = "sk-1234567890abcdef"
+const llmkey = "llm-live-1234567890abcdef"
+const openaiKey = "sk-proj-1234567890abcdef"
+const bearerToken = "Bearer abc.def.ghi"
+const password = "plain-password"
+const githubToken = "ghp_1234567890abcdef1234567890abcdef1234"
+const client_secret = "github_pat_1234567890abcdef1234567890abcdef"
+const jwtToken = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature"
+const privateKey = "-----BEGIN PRIVATE KEY-----MIIEvQIBADANBgkqhkiG9w0BAQEFAASC-----END PRIVATE KEY-----"
+const secretDSN = "postgres://reviewer:db-password-123@db.example.com/app?sslmode=require"
+const tokenPlaceholder = "dummy"
+const retryTokenTimeoutSeconds = 30
`
	if err := os.WriteFile(diffPath, []byte(diff), 0o644); err != nil {
		t.Fatalf("write diff: %v", err)
	}

	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		SQLitePath: dbPath,
		OutputDir:  outDir,
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: diffPath,
		Mode:     ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Metrics.RedactionCount == 0 {
		t.Fatalf("expected redaction count, got %+v", result.Metrics)
	}
	if len(result.Findings) == 0 || !strings.Contains(result.Findings[0].Evidence, "[REDACTED]") {
		t.Fatalf("expected readable redacted evidence, got %+v", result.Findings)
	}

	for _, name := range []string{"review_report.json", "review_report.md", "review_report.zh.md", "review_diagnostics.json"} {
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, raw := range rawSecrets {
			if strings.Contains(string(data), raw) {
				t.Fatalf("%s leaked raw secret %q: %s", name, raw, data)
			}
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	defer db.Close()
	leaks, err := scanSQLiteForRawSecrets(context.Background(), db, rawSecrets)
	if err != nil {
		t.Fatalf("scan sqlite: %v", err)
	}
	if len(leaks) > 0 {
		t.Fatalf("sqlite persisted raw secrets: %s", strings.Join(leaks, ", "))
	}
}

// TestParseSkillFindingsDedupesAndRedactsSecretFindings 固定脚本输出进入 Agent 的安全边界。
func TestParseSkillFindingsDedupesAndRedactsSecretFindings(t *testing.T) {

	stdout := `{"findings":[` +
		`{"severity":"critical","category":"security","file":"config.go","line":7,"title":"Potential secret appears in added code","evidence":"const llmkey = \"llm-live-1234567890abcdef\"","recommendation":"Replace the literal with a secret manager or environment lookup.","confidence":"high","source":"skill_run","rule_id":"secret-leak","status":"finding"},` +
		`{"severity":"critical","category":"security","file":"config.go","line":7,"title":"Potential secret appears in added code","evidence":"const llmkey = \"llm-live-1234567890abcdef\"","recommendation":"Replace the literal with a secret manager or environment lookup.","confidence":"high","source":"skill_run","rule_id":"secret-leak","status":"finding"}` +
		`],"warnings":[]}`

	result, err := parseSkillFindings(stdout)
	if err != nil {
		t.Fatalf("parseSkillFindings returned error: %v", err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("expected duplicate secret finding to be deduped, got %+v", result.Findings)
	}
	if strings.Contains(result.Findings[0].Evidence, "llm-live-1234567890abcdef") {
		t.Fatalf("expected Agent safety boundary to redact evidence, got %+v", result.Findings[0])
	}
}

// TestAgentRunPersistsWarningsForReplay 固定 warning 可回放。
func TestAgentRunPersistsWarningsForReplay(t *testing.T) {

	root := repoRoot(t)
	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot:   filepath.Join(root, "skills"),
		FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
		Runtime:      RuntimeLocalFallback,
		SQLitePath:   dbPath,
		OutputDir:    t.TempDir(),
		Timeout:      testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		Fixture: "test-missing.diff",
		Mode:    ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected fixture to produce warning")
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()

	items, err := store.FindingsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load review items: %v", err)
	}
	for _, item := range items {
		if item.RuleID == "missing-test-hint" && item.Status == "warning" {
			return
		}
	}
	t.Fatalf("expected warning to be persisted for replay, got %+v", items)
}

// TestAgentRunDoesNotExecuteNonAllowPermission 固定非 allow 不执行。
func TestAgentRunDoesNotExecuteNonAllowPermission(t *testing.T) {

	cases := []struct {
		name     string
		decision tool.PermissionDecision
	}{
		{name: "deny", decision: tool.DenyPermission("blocked by test policy")},
		{name: "ask", decision: tool.AskPermission("requires approval in test policy")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := repoRoot(t)
			dbPath := filepath.Join(t.TempDir(), "review.db")
			ag, err := New(Config{
				SkillsRoot:   filepath.Join(root, "skills"),
				FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
				Runtime:      RuntimeLocalFallback,
				SQLitePath:   dbPath,
				OutputDir:    t.TempDir(),
				Timeout:      testReviewTimeout,
			})
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			defer ag.Close()
			ag.policy = tool.PermissionPolicyFunc(func(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
				_ = ctx
				_ = req
				return tc.decision, nil
			})

			result, err := ag.Run(context.Background(), Request{
				Fixture: "secret.diff",
				Mode:    ModeRuleOnly,
			})
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			for _, finding := range result.Findings {
				if finding.RuleID == "secret-leak" {
					t.Fatalf("skill_run appears to have executed after %s decision: %+v", tc.decision.Action, result.Findings)
				}
			}
			if len(result.HumanReviewItems) == 0 {
				t.Fatalf("expected non-allow decision to create a human review item")
			}
			if len(result.GovernanceSummary.PermissionDecisions) == 0 ||
				result.GovernanceSummary.PermissionDecisions[0].Action != string(tc.decision.Action) {
				t.Fatalf("expected governance summary action %q, got %+v", tc.decision.Action, result.GovernanceSummary)
			}

			store, err := sqlite.Open(dbPath)
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			defer store.Close()
			decisions, err := store.DecisionsByTaskID(context.Background(), result.TaskID)
			if err != nil {
				t.Fatalf("load decisions: %v", err)
			}
			if len(decisions) != 1 || decisions[0].Action != string(tc.decision.Action) {
				t.Fatalf("expected persisted %s decision, got %+v", tc.decision.Action, decisions)
			}
			runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
			if err != nil {
				t.Fatalf("load sandbox runs: %v", err)
			}
			if len(runs) != 1 || runs[0].Status != string(tc.decision.Action) || runs[0].ExitCode != 0 {
				t.Fatalf("expected non-executed %s sandbox record, got %+v", tc.decision.Action, runs)
			}
		})
	}
}

// TestAgentRunCountsAllPermissionBlocks 固定所有非 allow 决策都会计数。
func TestAgentRunCountsAllPermissionBlocks(t *testing.T) {

	root := repoRoot(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/blocked\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package blocked\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		SQLitePath: dbPath,
		OutputDir:  t.TempDir(),
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()
	ag.policy = tool.PermissionPolicyFunc(func(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
		_ = ctx
		if req.ToolName == "workspace_exec" {
			return tool.DenyPermission("go checks blocked by test policy"), nil
		}
		return tool.AllowPermission(), nil
	})

	result, err := ag.Run(context.Background(), Request{
		RepoPath: repo,
		Mode:     ModeSandbox,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Metrics.PermissionBlocks != 2 || result.GovernanceSummary.PermissionBlocks != 2 {
		t.Fatalf("expected two blocked Go check decisions, got metrics=%+v governance=%+v", result.Metrics, result.GovernanceSummary)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	metrics, err := store.MetricsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load metrics: %v", err)
	}
	if metrics.PermissionBlockCount != 2 {
		t.Fatalf("expected persisted permission block count 2, got %+v", metrics)
	}
	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	for _, run := range runs {
		if strings.HasPrefix(run.Command, "go ") && run.Status != "deny" {
			t.Fatalf("expected denied Go check run to skip executor, got %+v", run)
		}
	}
}

// TestAgentRunAcceptsFixtureInput 固定 fixture 输入路径。
func TestAgentRunAcceptsFixtureInput(t *testing.T) {

	root := repoRoot(t)
	outDir := t.TempDir()
	ag, err := New(Config{
		SkillsRoot:   filepath.Join(root, "skills"),
		FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
		Runtime:      RuntimeLocalFallback,
		OutputDir:    outDir,
		Timeout:      testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		Fixture: "secret.diff",
		Mode:    ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Findings) == 0 || result.Findings[0].RuleID != "secret-leak" {
		t.Fatalf("expected fixture secret finding, got %+v", result.Findings)
	}
	if _, err := os.Stat(filepath.Join(outDir, "review_report.json")); err != nil {
		t.Fatalf("expected json report: %v", err)
	}
}

// TestReadInputFromRepoReturnsRepoPath 固定仓库输入仍按 repo path 读取。
func TestReadInputFromRepoReturnsRepoPath(t *testing.T) {

	root := repoRoot(t)
	diff, ref, err := readInput(Config{}, Request{
		RepoPath: root,
	})
	if err != nil {
		t.Fatalf("readInput returned error: %v", err)
	}
	if ref != root {
		t.Fatalf("expected repo path ref %q, got %q", root, ref)
	}
	if diff == nil {
		t.Fatalf("expected repo diff bytes")
	}
}

// TestReadInputFromRepoReadsWorkingTreeDiff 固定仓库输入按工作区 diff 读取。
func TestReadInputFromRepoReadsWorkingTreeDiff(t *testing.T) {

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package demo\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	diff, ref, err := readInput(Config{}, Request{RepoPath: repo})
	if err != nil {
		t.Fatalf("readInput returned error: %v", err)
	}
	if ref != repo {
		t.Fatalf("expected repo path ref %q, got %q", repo, ref)
	}
	if len(diff) == 0 {
		t.Fatalf("expected repo diff content")
	}
}

// TestInputMetadataFromRepoPathDiff 固定 repo-path 输入的 Go 元数据。
func TestInputMetadataFromRepoPathDiff(t *testing.T) {

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repometa\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "handler.go"), []byte("package handler\n\nfunc Serve() {}\n"), 0o644); err != nil {
		t.Fatalf("write handler.go: %v", err)
	}
	diff, _, err := readInput(Config{}, Request{RepoPath: repo})
	if err != nil {
		t.Fatalf("readInput returned error: %v", err)
	}

	meta := inputMetadata(diff, repo)
	if meta.ModulePath != "example.com/repometa" {
		t.Fatalf("module path = %q, want example.com/repometa", meta.ModulePath)
	}
	if !stringSliceContains(meta.ChangedGoFiles, "handler.go") {
		t.Fatalf("expected handler.go in metadata, got %+v", meta)
	}
	if !stringSliceContains(meta.PackageNames, "handler") {
		t.Fatalf("expected package handler in metadata, got %+v", meta)
	}
	if meta.HasTests || len(meta.TouchedTestFiles) != 0 {
		t.Fatalf("expected no touched tests, got %+v", meta)
	}
}

// TestReadInputFromFileListBuildsDiff 固定文件路径列表输入。
func TestReadInputFromFileListBuildsDiff(t *testing.T) {

	repo := t.TempDir()
	src := filepath.Join(repo, "foo.go")
	if err := os.WriteFile(src, []byte("package demo\n\nfunc Bad() { panic(\"boom\") }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	listPath := filepath.Join(repo, "files.txt")
	if err := os.WriteFile(listPath, []byte("# changed files\nfoo.go\n\n"), 0o644); err != nil {
		t.Fatalf("write file list: %v", err)
	}

	diff, ref, err := readInput(Config{}, Request{FileList: listPath, RepoPath: repo})
	if err != nil {
		t.Fatalf("readInput returned error: %v", err)
	}
	if ref != listPath {
		t.Fatalf("expected file list ref %q, got %q", listPath, ref)
	}
	for _, want := range []string{"diff --git a/foo.go b/foo.go", "+++ b/foo.go", "+func Bad() { panic(\"boom\") }"} {
		if !strings.Contains(string(diff), want) {
			t.Fatalf("expected generated diff to include %q, got %s", want, diff)
		}
	}
}

// TestReadInputFromFileListRejectsRepoEscape 固定路径列表不能跳出 repo。
func TestReadInputFromFileListRejectsRepoEscape(t *testing.T) {

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("make repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.go"), []byte("package secret\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	listPath := filepath.Join(repo, "files.txt")
	if err := os.WriteFile(listPath, []byte("../secret.go\n"), 0o644); err != nil {
		t.Fatalf("write file list: %v", err)
	}

	if _, _, err := readInput(Config{}, Request{FileList: listPath, RepoPath: repo}); err == nil {
		t.Fatalf("expected repo escape to be rejected")
	}
}

// TestAgentRunAcceptsFileListInput 固定路径列表进入完整审查链路。
func TestAgentRunAcceptsFileListInput(t *testing.T) {

	root := repoRoot(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package demo\n\nfunc Bad() { panic(\"boom\") }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	listPath := filepath.Join(repo, "files.txt")
	if err := os.WriteFile(listPath, []byte("foo.go\n"), 0o644); err != nil {
		t.Fatalf("write file list: %v", err)
	}
	outDir := t.TempDir()
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		OutputDir:  outDir,
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		FileList: listPath,
		RepoPath: repo,
		Mode:     ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Findings) == 0 || result.Findings[0].RuleID != "panic-direct" {
		t.Fatalf("expected panic finding from file list input, got %+v", result.Findings)
	}
	if _, err := os.Stat(filepath.Join(outDir, "review_report.json")); err != nil {
		t.Fatalf("expected json report: %v", err)
	}
}

// TestRequestInputKindRecognizesFileList 固定路径列表 telemetry 类型。
func TestRequestInputKindRecognizesFileList(t *testing.T) {

	if got := requestInputKind(Request{FileList: "files.txt"}); got != "file_list" {
		t.Fatalf("input kind = %q, want file_list", got)
	}
}

// TestReportArtifactsRemainStable 固定报告和诊断产物语义不变。
func TestReportArtifactsRemainStable(t *testing.T) {

	arts := reportArtifacts()
	if len(arts) != 4 {
		t.Fatalf("expected 4 artifacts, got %+v", arts)
	}
	if arts[0].Name != "review_report.json" || arts[1].Name != "review_report.md" || arts[2].Name != "review_report.zh.md" || arts[3].Name != "review_diagnostics.json" {
		t.Fatalf("unexpected artifacts: %+v", arts)
	}
}

// TestEnforceArtifactLimitsBlocksOversizedReports 固定产物大小边界。
func TestEnforceArtifactLimitsBlocksOversizedReports(t *testing.T) {

	err := enforceArtifactLimits(Config{MaxArtifactBytes: 4}, []artifactPayload{{
		Name: "review_report.json",
		Data: []byte("12345"),
	}})
	if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("expected artifact size limit error, got %v", err)
	}
}

func TestEnforceArtifactLimitsRejectsUnknownNamesCountAndTotalBytes(t *testing.T) {

	base := Config{MaxArtifactBytes: 8, MaxArtifactTotalBytes: 8, MaxArtifactCount: 1}
	for _, tc := range []struct {
		name      string
		artifacts []artifactPayload
		contains  string
	}{
		{name: "unknown name", artifacts: []artifactPayload{{Name: "../secret.txt", Data: []byte("x")}}, contains: "not allowed"},
		{name: "count", artifacts: []artifactPayload{{Name: "review_report.json", Data: []byte("x")}, {Name: "review_report.md", Data: []byte("x")}}, contains: "count limit"},
		{name: "total", artifacts: []artifactPayload{{Name: "review_report.json", Data: []byte("123456789")}}, contains: "total size limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := enforceArtifactLimits(base, tc.artifacts)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("enforceArtifactLimits error = %v, want %q", err, tc.contains)
			}
		})
	}
}

// TestAgentRunRejectsOversizedArtifacts 固定超大产物不落盘。
func TestAgentRunRejectsOversizedArtifacts(t *testing.T) {

	root := repoRoot(t)
	outDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot:       filepath.Join(root, "skills"),
		Runtime:          RuntimeLocalFallback,
		SQLitePath:       dbPath,
		OutputDir:        outDir,
		Timeout:          testReviewTimeout,
		MaxArtifactBytes: 1,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	_, err = ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "secret.diff"),
		Mode:     ModeRuleOnly,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("expected artifact size limit error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "review_report.json")); !os.IsNotExist(statErr) {
		t.Fatalf("oversized report should not be written, stat err=%v", statErr)
	}
	store, openErr := sqlite.Open(dbPath)
	if openErr != nil {
		t.Fatalf("open sqlite: %v", openErr)
	}
	defer store.Close()
	var tasks []sqlite.Task
	db, openDBErr := sql.Open("sqlite", dbPath)
	if openDBErr != nil {
		t.Fatalf("open sqlite directly: %v", openDBErr)
	}
	defer db.Close()
	rows, queryErr := db.QueryContext(context.Background(), `SELECT task_id FROM review_tasks`)
	if queryErr != nil {
		t.Fatalf("query tasks: %v", queryErr)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			t.Fatalf("scan task id: %v", scanErr)
		}
		task, loadErr := store.TaskByID(context.Background(), id)
		if loadErr != nil {
			t.Fatalf("load task: %v", loadErr)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate task rows: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != "failed" || tasks[0].FinishedAt.IsZero() {
		t.Fatalf("expected failed task after artifact error, got %+v", tasks)
	}
}

// TestConclusionStatuses 固定最终结论规则。
func TestConclusionStatuses(t *testing.T) {

	cases := []struct {
		name   string
		result review.Result
		want   string
	}{
		{
			name: "blocking finding",
			result: review.Result{Findings: []review.Finding{{
				Severity: "high",
			}}},
			want: "fail",
		},
		{
			name: "human review",
			result: review.Result{HumanReviewItems: []review.Finding{{
				Severity: "low",
			}}},
			want: "needs_human_review",
		},
		{
			name: "sandbox exception",
			result: review.Result{Metrics: review.Metrics{
				ExceptionCounts: map[string]int{"sandbox_failed": 1},
			}},
			want: "needs_human_review",
		},
		{
			name:   "pass",
			result: review.Result{},
			want:   "pass",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := conclusion(tc.result)
			if got.Status != tc.want {
				t.Fatalf("conclusion status = %q, want %q", got.Status, tc.want)
			}
		})
	}
}

// TestArtifactServiceReportsCanBeSavedAsArtifacts 固定报告和诊断可进入官方 artifact service。
func TestArtifactServiceReportsCanBeSavedAsArtifacts(t *testing.T) {

	root := repoRoot(t)
	svc := inmemory.NewService()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	outDir := t.TempDir()
	ag, err := New(Config{
		SkillsRoot:      filepath.Join(root, "skills"),
		Runtime:         RuntimeLocalFallback,
		SQLitePath:      dbPath,
		OutputDir:       outDir,
		Timeout:         testReviewTimeout,
		ArtifactService: svc,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "secret.diff"),
		Mode:     ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	keys, err := svc.ListArtifactKeys(context.Background(), artifact.SessionInfo{
		AppName:   "cr-agent",
		UserID:    "local",
		SessionID: result.TaskID,
	})
	if err != nil {
		t.Fatalf("list artifact keys: %v", err)
	}
	if len(keys) != 4 {
		t.Fatalf("expected 4 artifacts to be saved in official artifact service, got %+v", keys)
	}
	if _, err := os.Stat(filepath.Join(outDir, "review_report.zh.md")); err != nil {
		t.Fatalf("expected Chinese Markdown report artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "review_diagnostics.json")); err != nil {
		t.Fatalf("expected diagnostics artifact: %v", err)
	}
	diagnostics, err := os.ReadFile(filepath.Join(outDir, "review_diagnostics.json"))
	if err != nil {
		t.Fatalf("read diagnostics artifact: %v", err)
	}
	for _, want := range []string{`"conclusion"`, result.Conclusion.Status, result.Conclusion.Reason} {
		if !strings.Contains(string(diagnostics), want) {
			t.Fatalf("expected diagnostics artifact to include %q, got %s", want, diagnostics)
		}
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	recs, err := store.ArtifactsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load artifact records: %v", err)
	}
	if len(recs) != 4 {
		t.Fatalf("expected persisted artifact records, got %+v", recs)
	}
	for _, rec := range recs {
		if rec.Size == 0 {
			t.Fatalf("expected artifact size, got %+v", recs)
		}
	}
}

// TestAgentDefaultArtifactService 保存默认官方产物边界。
func TestAgentDefaultArtifactService(t *testing.T) {

	root := repoRoot(t)
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		OutputDir:  t.TempDir(),
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()
	if ag.artifactService == nil {
		t.Fatal("expected default artifact service")
	}

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "secret.diff"),
		Mode:     ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	art, err := ag.artifactService.LoadArtifact(context.Background(), artifactSessionInfo(result.TaskID), "review_report.json", nil)
	if err != nil {
		t.Fatalf("load default artifact: %v", err)
	}
	if art == nil || art.MimeType != "application/json" || !strings.Contains(string(art.Data), `"task_id"`) {
		t.Fatalf("expected saved JSON report artifact, got %+v", art)
	}
}

// TestAgentRunRecordsTelemetryAttributes 固定官方 telemetry span 摘要。
func TestAgentRunRecordsTelemetryAttributes(t *testing.T) {
	recorder := useAgentTelemetrySpanRecorder(t)

	root := repoRoot(t)
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		OutputDir:  t.TempDir(),
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "secret.diff"),
		Mode:     ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	span := findAgentReviewSpan(t, recorder)
	attrs := agentSpanAttributes(span.Attributes())
	for _, key := range []string{
		"cr_agent.task_id",
		"cr_agent.runtime",
		"cr_agent.mode",
		"cr_agent.sandbox_requested",
		"cr_agent.sandbox_executed",
		"cr_agent.model_requested",
		"cr_agent.model_executed",
		"cr_agent.input_type",
		"cr_agent.finding_count",
		"cr_agent.artifact_count",
		"cr_agent.permission_block_count",
		"cr_agent.tool_call_count",
		"cr_agent.model_call_count",
		"cr_agent.model_finding_count",
		"cr_agent.model_exception_count",
		"cr_agent.sandbox_run_count",
		"cr_agent.total_duration_ms",
		"cr_agent.sandbox_duration_ms",
		"cr_agent.model_duration_ms",
		"cr_agent.severity_counts",
		"cr_agent.exception_counts",
		"cr_agent.conclusion_status",
		"cr_agent.conclusion_reason",
	} {
		if _, ok := attrs[key]; !ok {
			t.Fatalf("expected telemetry attribute %q, attrs=%+v", key, attrs)
		}
	}
	if attrs["cr_agent.task_id"].AsString() != result.TaskID {
		t.Fatalf("task id attribute mismatch: got %q want %q", attrs["cr_agent.task_id"].AsString(), result.TaskID)
	}
	if attrs["cr_agent.runtime"].AsString() != RuntimeLocalFallback {
		t.Fatalf("runtime attribute mismatch: %+v", attrs["cr_agent.runtime"])
	}
	if attrs["cr_agent.mode"].AsString() != ModeReview {
		t.Fatalf("mode attribute mismatch: %+v", attrs["cr_agent.mode"])
	}
	if attrs["cr_agent.sandbox_requested"].AsBool() || attrs["cr_agent.sandbox_executed"].AsBool() || attrs["cr_agent.model_requested"].AsBool() || attrs["cr_agent.model_executed"].AsBool() {
		t.Fatalf("legacy rule-only must normalize to disabled capabilities: %+v", attrs)
	}
	if attrs["cr_agent.input_type"].AsString() != "diff_file" {
		t.Fatalf("input type attribute mismatch: %+v", attrs["cr_agent.input_type"])
	}
	if attrs["cr_agent.finding_count"].AsInt64() != int64(len(result.Findings)) {
		t.Fatalf("finding count attribute mismatch: %+v", attrs["cr_agent.finding_count"])
	}
	if attrs["cr_agent.artifact_count"].AsInt64() != 4 {
		t.Fatalf("expected 4 artifact telemetry records, got %+v", attrs["cr_agent.artifact_count"])
	}
	if attrs["cr_agent.permission_block_count"].AsInt64() != int64(result.Metrics.PermissionBlocks) {
		t.Fatalf("permission block count attribute mismatch: %+v", attrs["cr_agent.permission_block_count"])
	}
	if attrs["cr_agent.tool_call_count"].AsInt64() != int64(result.Metrics.ToolCallCount) {
		t.Fatalf("tool call count attribute mismatch: %+v", attrs["cr_agent.tool_call_count"])
	}
	if attrs["cr_agent.model_call_count"].AsInt64() != int64(result.Metrics.ModelCallCount) {
		t.Fatalf("model call count attribute mismatch: %+v", attrs["cr_agent.model_call_count"])
	}
	if attrs["cr_agent.model_finding_count"].AsInt64() != int64(result.Metrics.ModelFindingCount) {
		t.Fatalf("model finding count attribute mismatch: %+v", attrs["cr_agent.model_finding_count"])
	}
	if attrs["cr_agent.model_exception_count"].AsInt64() != int64(result.Metrics.ModelExceptionCount) {
		t.Fatalf("model exception count attribute mismatch: %+v", attrs["cr_agent.model_exception_count"])
	}
	if attrs["cr_agent.sandbox_run_count"].AsInt64() != int64(len(result.SandboxSummary.Runs)) {
		t.Fatalf("sandbox run count attribute mismatch: %+v", attrs["cr_agent.sandbox_run_count"])
	}
	if attrs["cr_agent.total_duration_ms"].AsInt64() != result.Metrics.TotalDurationMS {
		t.Fatalf("total duration attribute mismatch: %+v", attrs["cr_agent.total_duration_ms"])
	}
	if attrs["cr_agent.sandbox_duration_ms"].AsInt64() != result.Metrics.SandboxDurationMS {
		t.Fatalf("sandbox duration attribute mismatch: %+v", attrs["cr_agent.sandbox_duration_ms"])
	}
	if attrs["cr_agent.model_duration_ms"].AsInt64() != result.Metrics.ModelDurationMS {
		t.Fatalf("model duration attribute mismatch: %+v", attrs["cr_agent.model_duration_ms"])
	}
	if !strings.Contains(attrs["cr_agent.severity_counts"].AsString(), `"critical":1`) {
		t.Fatalf("severity distribution attribute mismatch: %+v", attrs["cr_agent.severity_counts"])
	}
	if attrs["cr_agent.exception_counts"].AsString() == "" {
		t.Fatalf("expected exception distribution attribute, got %+v", attrs["cr_agent.exception_counts"])
	}
	if attrs["cr_agent.exception_count"].AsInt64() != int64(exceptionCount(result.Metrics.ExceptionCounts)) {
		t.Fatalf("exception count attribute mismatch: %+v", attrs["cr_agent.exception_count"])
	}
	if attrs["cr_agent.conclusion_status"].AsString() != result.Conclusion.Status {
		t.Fatalf("conclusion status attribute mismatch: got %q want %q", attrs["cr_agent.conclusion_status"].AsString(), result.Conclusion.Status)
	}
	if attrs["cr_agent.conclusion_reason"].AsString() != result.Conclusion.Reason {
		t.Fatalf("conclusion reason attribute mismatch: got %q want %q", attrs["cr_agent.conclusion_reason"].AsString(), result.Conclusion.Reason)
	}
}

// TestAgentRunWritesGoInputMetadataToDiagnostics 固定 Go 输入元数据进入诊断产物。
func TestAgentRunWritesGoInputMetadataToDiagnostics(t *testing.T) {

	root := repoRoot(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/metademo\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "service.go"), []byte("package service\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("write service.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "service_test.go"), []byte("package service\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write service_test.go: %v", err)
	}
	listPath := filepath.Join(repo, "files.txt")
	if err := os.WriteFile(listPath, []byte("service.go\nservice_test.go\n"), 0o644); err != nil {
		t.Fatalf("write file list: %v", err)
	}
	outDir := t.TempDir()
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		OutputDir:  outDir,
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		FileList: listPath,
		RepoPath: repo,
		Mode:     ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.InputMetadata.ModulePath != "example.com/metademo" {
		t.Fatalf("expected module metadata, got %+v", result.InputMetadata)
	}
	if !result.InputMetadata.HasTests || len(result.InputMetadata.TouchedTestFiles) != 1 {
		t.Fatalf("expected touched test metadata, got %+v", result.InputMetadata)
	}
	if !stringSliceContains(result.InputMetadata.ChangedGoFiles, "service.go") ||
		!stringSliceContains(result.InputMetadata.ChangedGoFiles, "service_test.go") {
		t.Fatalf("expected changed Go files, got %+v", result.InputMetadata)
	}
	if !stringSliceContains(result.InputMetadata.PackageNames, "service") {
		t.Fatalf("expected package name metadata, got %+v", result.InputMetadata)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "review_diagnostics.json"))
	if err != nil {
		t.Fatalf("read diagnostics: %v", err)
	}
	for _, want := range []string{`"input_metadata"`, `"module_path": "example.com/metademo"`, `"service_test.go"`, `"has_tests": true`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected diagnostics to include %q, got %s", want, data)
		}
	}
}

// TestAgentRunRecordsSandboxFailureWithoutCrashing 固定失败不崩溃。
func TestAgentRunRecordsSandboxFailureWithoutCrashing(t *testing.T) {

	root := repoRoot(t)
	dbPath := filepath.Join(t.TempDir(), "review.db")
	outDir := t.TempDir()
	ag, err := New(Config{
		SkillsRoot:   filepath.Join(root, "skills"),
		FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
		Runtime:      RuntimeLocalFallback,
		SQLitePath:   dbPath,
		OutputDir:    outDir,
		Timeout:      testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		Fixture: "sandbox-fail.diff",
		Mode:    ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run should not fail when sandbox command fails: %v", err)
	}
	if got := result.Metrics.ExceptionCounts["sandbox_failed"]; got != 1 {
		t.Fatalf("expected sandbox_failed exception count, got %+v", result.Metrics.ExceptionCounts)
	}
	if _, err := os.Stat(filepath.Join(outDir, "review_report.json")); err != nil {
		t.Fatalf("expected json report: %v", err)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()

	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].ExitCode == 0 {
		t.Fatalf("expected failed sandbox run with nonzero exit, got %+v", runs)
	}
	if runs[0].TimeoutMS != ag.cfg.Timeout.Milliseconds() || runs[0].OutputLimitBytes != ag.cfg.OutputLimitBytes {
		t.Fatalf("expected failed sandbox run to record timeout/output limit, got %+v", runs[0])
	}
	if runs[0].EnvWhitelist != sandboxEnvWhitelist {
		t.Fatalf("expected failed sandbox run env whitelist %q, got %+v", sandboxEnvWhitelist, runs[0])
	}
	if runs[0].StdoutDigest == "" {
		t.Fatalf("expected failed sandbox run stdout digest, got %+v", runs[0])
	}
	metrics, err := store.MetricsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load metrics: %v", err)
	}
	if !strings.Contains(metrics.ExceptionCountsJSON, "sandbox_failed") {
		t.Fatalf("expected persisted sandbox_failed metric, got %s", metrics.ExceptionCountsJSON)
	}
}

// TestAgentRunRecordsSandboxTimeoutWithoutCrashing 固定超时可审计。
func TestAgentRunRecordsSandboxTimeoutWithoutCrashing(t *testing.T) {

	root := repoRoot(t)
	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot:   filepath.Join(root, "skills"),
		FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
		Runtime:      RuntimeLocalFallback,
		SQLitePath:   dbPath,
		OutputDir:    t.TempDir(),
		Timeout:      1 * time.Second,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		Fixture: "sandbox-timeout.diff",
		Mode:    ModeRuleOnly,
	})
	if err != nil {
		t.Fatalf("Run should not fail when sandbox times out: %v", err)
	}
	if got := result.Metrics.ExceptionCounts["sandbox_failed"]; got != 1 {
		t.Fatalf("expected sandbox_failed exception count, got %+v", result.Metrics.ExceptionCounts)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "timed_out" {
		t.Fatalf("expected timed_out sandbox run, got %+v", runs)
	}
	if runs[0].TimeoutMS != ag.cfg.Timeout.Milliseconds() || runs[0].OutputLimitBytes != ag.cfg.OutputLimitBytes {
		t.Fatalf("expected timed_out sandbox run to record timeout/output limit, got %+v", runs[0])
	}
	if runs[0].EnvWhitelist != sandboxEnvWhitelist {
		t.Fatalf("expected timed_out sandbox run env whitelist %q, got %+v", sandboxEnvWhitelist, runs[0])
	}
	if runs[0].StdoutDigest == "" {
		t.Fatalf("expected timed_out sandbox run stdout digest, got %+v", runs[0])
	}
}

// TestAgentRunDryRunRecordsSkippedSandbox 固定 dry-run 审计记录。
func TestAgentRunDryRunRecordsSkippedSandbox(t *testing.T) {

	root := repoRoot(t)
	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		SQLitePath: dbPath,
		OutputDir:  t.TempDir(),
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "secret.diff"),
		Mode:     ModeDryRun,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Metrics.ToolCallCount != 1 {
		t.Fatalf("dry-run should only load skill, got tool calls %d", result.Metrics.ToolCallCount)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "skipped" {
		t.Fatalf("expected skipped sandbox run, got %+v", runs)
	}
	if runs[0].EnvWhitelist != sandboxEnvWhitelist {
		t.Fatalf("expected dry-run env whitelist %q, got %+v", sandboxEnvWhitelist, runs[0])
	}
	decisions, err := store.DecisionsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load decisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Action != "dry_run" {
		t.Fatalf("expected dry_run permission decision, got %+v", decisions)
	}
}

// TestAgentRunFakeModelUsesProviderBoundary 固定 fake-model 经过模型审查边界。
func TestAgentRunFakeModelUsesProviderBoundary(t *testing.T) {

	root := repoRoot(t)
	outDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	diffPath := filepath.Join(t.TempDir(), "fake-model.diff")
	diff := `diff --git a/model.go b/model.go
--- /dev/null
+++ b/model.go
@@ -0,0 +1,5 @@
+package modeldemo
+
+func RiskyModelPath() {
+	_ = "CR_AGENT_FAKE_MODEL_HIGH"
+}
`
	if err := os.WriteFile(diffPath, []byte(diff), 0o644); err != nil {
		t.Fatalf("write diff: %v", err)
	}
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		SQLitePath: dbPath,
		OutputDir:  outDir,
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: diffPath,
		Mode:     ModeFakeModel,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !hasFindingSource(result.Findings, "fake_model") {
		t.Fatalf("expected fake-model provider finding, got %+v", result.Findings)
	}
	if result.Metrics.ModelCallCount != 1 || result.Metrics.ModelFindingCount == 0 || result.Metrics.ModelDurationMS == 0 {
		t.Fatalf("expected model metrics, got %+v", result.Metrics)
	}
	if result.Metrics.ModelProvider != "fake" || result.Metrics.ModelName != "fake_model" || result.Metrics.ModelBackend != "trpc-agent-go/model.Model" {
		t.Fatalf("expected non-sensitive model audit fields, got %+v", result.Metrics)
	}

	diagnostics, err := os.ReadFile(filepath.Join(outDir, "review_diagnostics.json"))
	if err != nil {
		t.Fatalf("read diagnostics: %v", err)
	}
	reportJSON, err := os.ReadFile(filepath.Join(outDir, "review_report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	for _, artifact := range [][]byte{diagnostics, reportJSON} {
		for _, want := range []string{
			`"model_call_count": 1`,
			`"model_finding_count": 1`,
			`"model_provider": "fake"`,
			`"model_name": "fake_model"`,
			`"model_backend": "trpc-agent-go/model.Model"`,
		} {
			if !strings.Contains(string(artifact), want) {
				t.Fatalf("expected artifact to include %q, got %s", want, artifact)
			}
		}
	}
	for _, forbidden := range []string{"api_key", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "base_url"} {
		if strings.Contains(string(reportJSON), forbidden) || strings.Contains(string(diagnostics), forbidden) {
			t.Fatalf("model audit fields must not leak secret configuration key %q", forbidden)
		}
	}
	for _, want := range []string{`"model_call_count": 1`, `"model_finding_count": 1`} {
		if !strings.Contains(string(diagnostics), want) {
			t.Fatalf("expected diagnostics to include %q, got %s", want, diagnostics)
		}
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	metrics, err := store.MetricsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load metrics: %v", err)
	}
	if metrics.ModelCallCount != 1 || metrics.ModelFindingCount != 1 {
		t.Fatalf("expected persisted model metrics, got %+v", metrics)
	}
	if metrics.ModelProvider != "fake" || metrics.ModelName != "fake_model" || metrics.ModelBackend != "trpc-agent-go/model.Model" {
		t.Fatalf("expected persisted model audit fields, got %+v", metrics)
	}
	items, err := store.FindingsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load findings: %v", err)
	}
	if !hasFindingSource(items, "fake_model") {
		t.Fatalf("expected sqlite model finding source, got %+v", items)
	}
}

// TestReviewProviderModelAdapterImplementsOfficialModel 固定模型 provider 可走官方 model.Model 接口。
func TestReviewProviderModelAdapterImplementsOfficialModel(t *testing.T) {

	rawSecret := "sk-officialmodel-1234567890"
	var seenInput llm.Input
	provider := llm.ProviderFunc(func(ctx context.Context, input llm.Input) (llm.Output, error) {
		_ = ctx
		seenInput = input
		if strings.Contains(input.DiffSummary, rawSecret) {
			t.Fatalf("official model adapter leaked raw input secret: %s", input.DiffSummary)
		}
		return llm.Output{Findings: []review.Finding{{
			Severity:       "medium",
			Category:       "logic",
			File:           "main.go",
			Line:           7,
			Title:          "Official adapter model signal",
			Evidence:       "adapter evidence " + rawSecret,
			Recommendation: "Inspect the official model adapter path.",
			Confidence:     "high",
			Source:         "model",
			RuleID:         "official-model-adapter",
		}}}, nil
	})
	var official agentmodel.Model = llm.ProviderModelAdapter{
		Name:     "cr-agent-test-model",
		Provider: provider,
	}

	ch, err := official.GenerateContent(context.Background(), llm.InputRequest(llm.Input{
		DiffSummary: "+ secret = \"" + rawSecret + "\"",
		InputMetadata: review.InputMetadata{
			ChangedGoFiles: []string{"main.go"},
		},
	}))
	if err != nil {
		t.Fatalf("GenerateContent returned error: %v", err)
	}
	var responses []*agentmodel.Response
	for rsp := range ch {
		responses = append(responses, rsp)
	}
	if len(responses) != 1 {
		t.Fatalf("expected one official model response, got %+v", responses)
	}
	if responses[0].Error != nil {
		t.Fatalf("unexpected official model response error: %+v", responses[0].Error)
	}
	if responses[0].Model != "cr-agent-test-model" || responses[0].Object != agentmodel.ObjectTypeChatCompletion {
		t.Fatalf("unexpected official model metadata: %+v", responses[0])
	}
	var output llm.Output
	if err := json.Unmarshal([]byte(responses[0].Choices[0].Message.Content), &output); err != nil {
		t.Fatalf("decode official model response content: %v", err)
	}
	if !hasRuleID(output.Findings, "official-model-adapter") {
		t.Fatalf("expected adapter finding in official response, got %+v", output.Findings)
	}
	for _, finding := range output.Findings {
		if strings.Contains(finding.Evidence, rawSecret) {
			t.Fatalf("official model adapter leaked raw output secret: %+v", finding)
		}
	}
	if seenInput.DiffSummary == "" || !strings.Contains(seenInput.DiffSummary, "[REDACTED]") {
		t.Fatalf("expected provider to receive redacted model input, got %+v", seenInput)
	}
	if official.Info().Name != "cr-agent-test-model" {
		t.Fatalf("unexpected official model info: %+v", official.Info())
	}
}

// TestAgentRunEmitsOfficialEvents 固定 CLI 编排阶段通过官方 event.Event 暴露。
func TestAgentRunEmitsOfficialEvents(t *testing.T) {

	root := repoRoot(t)
	var events []*agentevent.Event
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		OutputDir:  t.TempDir(),
		Timeout:    testReviewTimeout,
		EventSink: func(ctx context.Context, ev *agentevent.Event) {
			_ = ctx
			if ev != nil {
				events = append(events, ev.Clone())
			}
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "safe.diff"),
		Mode:     ModeFakeModel,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.TaskID == "" {
		t.Fatalf("expected task id")
	}
	objects := eventObjects(events)
	for _, want := range []string{
		reviewEventInputLoaded,
		reviewEventSkillRun,
		reviewEventSandboxRun,
		reviewEventModelReview,
		reviewEventReportWritten,
		reviewEventTaskFinished,
	} {
		if !containsString(objects, want) {
			t.Fatalf("expected event %q in %+v", want, objects)
		}
	}
	for _, ev := range events {
		if ev.InvocationID != result.TaskID {
			t.Fatalf("expected event invocation id %q, got %+v", result.TaskID, ev)
		}
		if ev.Author != "cr-agent" {
			t.Fatalf("expected cr-agent author, got %+v", ev)
		}
	}
}

// TestAgentRunWithEventsUsesOfficialRunnerRoute 固定一次 review 可以通过官方 Runner/Event 路线消费事件流。
func TestAgentRunWithEventsUsesOfficialRunnerRoute(t *testing.T) {

	root := repoRoot(t)
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		OutputDir:  t.TempDir(),
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	events, err := ag.RunWithEvents(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "safe.diff"),
		Mode:     ModeFakeModel,
	})
	if err != nil {
		t.Fatalf("RunWithEvents returned error: %v", err)
	}
	var got []*agentevent.Event
	for ev := range events {
		if ev == nil {
			t.Fatalf("official runner emitted nil event")
		}
		got = append(got, ev)
	}
	objects := eventObjects(got)
	for _, want := range []string{
		reviewEventInputLoaded,
		reviewEventSkillRun,
		reviewEventSandboxRun,
		reviewEventModelReview,
		reviewEventReportWritten,
		reviewEventTaskFinished,
	} {
		if !containsString(objects, want) {
			t.Fatalf("expected official runner event %q in %+v", want, objects)
		}
	}
	for _, ev := range got {
		if ev.Author != "cr-agent" || ev.InvocationID == "" || ev.RequestID == "" {
			t.Fatalf("expected official event metadata from runner route, got %+v", ev)
		}
	}
}

// TestAgentRunE2BRuntimeRecordsUnsupportedAudit 固定 E2B 入口是显式 unsupported，而不是静默 fallback。
func TestAgentRunE2BRuntimeRecordsUnsupportedAudit(t *testing.T) {

	root := repoRoot(t)
	dbPath := filepath.Join(t.TempDir(), "review.db")
	outDir := t.TempDir()
	ag, err := New(Config{
		SkillsRoot:   filepath.Join(root, "skills"),
		FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
		Runtime:      RuntimeE2B,
		SQLitePath:   dbPath,
		OutputDir:    outDir,
		Timeout:      testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		Fixture: "safe.diff",
		Mode:    ModeSandbox,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.SandboxSummary.Runs) == 0 {
		t.Fatalf("expected unsupported sandbox run audit, got %+v", result.SandboxSummary)
	}
	if run := result.SandboxSummary.Runs[0]; run.Runtime != RuntimeE2B || run.Status != "unsupported" {
		t.Fatalf("expected e2b unsupported run, got %+v", run)
	}
	if len(result.HumanReviewItems) == 0 || !hasRuleID(result.HumanReviewItems, "e2b-runtime-unsupported") {
		t.Fatalf("expected e2b human review item, got %+v", result.HumanReviewItems)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Runtime != RuntimeE2B || runs[0].Status != "unsupported" {
		t.Fatalf("expected persisted e2b unsupported run, got %+v", runs)
	}
	diagnostics, err := os.ReadFile(filepath.Join(outDir, "review_diagnostics.json"))
	if err != nil {
		t.Fatalf("read diagnostics: %v", err)
	}
	if !strings.Contains(string(diagnostics), `"runtime": "e2b"`) || !strings.Contains(string(diagnostics), `"status": "unsupported"`) {
		t.Fatalf("expected diagnostics to record e2b unsupported runtime: %s", diagnostics)
	}
}

// TestE2BExecutorIsExplicitUnsupportedAdapter 固定当前 adapter 边界：
// e2b 不能静默回退到 local/container execution。
func TestE2BExecutorIsExplicitUnsupportedAdapter(t *testing.T) {

	exec, err := execution.NewExecutor(execution.Config{Runtime: RuntimeE2B})
	if err != nil {
		t.Fatalf("execution.NewExecutor returned error: %v", err)
	}
	if _, ok := exec.(execution.UnsupportedExecutor); !ok {
		t.Fatalf("expected e2b to use unsupported adapter until real workspace staging exists, got %T", exec)
	}
	_, err = exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{
			Code:     "echo should-not-run",
			Language: "bash",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), `runtime "e2b" is not supported`) {
		t.Fatalf("expected explicit unsupported e2b execution error, got %v", err)
	}
}

// TestAgentRunCarriesBaseHeadRefsToArtifactsAndSQLite 固定 base/head 作为审计上下文贯穿报告和落库。
func TestAgentRunCarriesBaseHeadRefsToArtifactsAndSQLite(t *testing.T) {

	root := repoRoot(t)
	dbPath := filepath.Join(t.TempDir(), "review.db")
	outDir := t.TempDir()
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		SQLitePath: dbPath,
		OutputDir:  outDir,
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "safe.diff"),
		Mode:     ModeRuleOnly,
		BaseRef:  "main",
		HeadRef:  "feature/review-agent",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.InputMetadata.BaseRef != "main" || result.InputMetadata.HeadRef != "feature/review-agent" {
		t.Fatalf("expected base/head metadata, got %+v", result.InputMetadata)
	}
	for _, name := range []string{"review_report.json", "review_diagnostics.json"} {
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(data), `"base_ref": "main"`) || !strings.Contains(string(data), `"head_ref": "feature/review-agent"`) {
			t.Fatalf("expected %s to include base/head refs: %s", name, data)
		}
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	report, err := store.ReportByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if !strings.Contains(string(report.JSON), `"base_ref": "main"`) || !strings.Contains(string(report.JSON), `"head_ref": "feature/review-agent"`) {
		t.Fatalf("expected persisted report to include base/head refs: %s", report.JSON)
	}
}

// TestModelProviderMergesFindingsByConfidenceAndDedupe 固定模型增量合并规则。
func TestModelProviderMergesFindingsByConfidenceAndDedupe(t *testing.T) {

	root := repoRoot(t)
	provider := llm.ProviderFunc(func(ctx context.Context, input llm.Input) (llm.Output, error) {
		_ = ctx
		return llm.Output{Findings: []review.Finding{
			{
				Severity:       "high",
				Category:       "error_handling",
				File:           "panic.go",
				Line:           2,
				Title:          "Duplicate model panic finding",
				Evidence:       "panic duplicate",
				Recommendation: "Use errors.",
				Confidence:     "high",
				Source:         "model",
				RuleID:         "panic-direct",
			},
			{
				Severity:       "medium",
				Category:       "logic",
				File:           "main.go",
				Line:           5,
				Title:          "High confidence semantic risk",
				Evidence:       "model_review_high",
				Recommendation: "Add the missing branch.",
				Confidence:     "high",
				Source:         "model",
				RuleID:         "model-semantic-risk",
			},
			{
				Severity:       "low",
				Category:       "maintainability",
				File:           "main.go",
				Line:           6,
				Title:          "Low confidence model hint",
				Evidence:       "model_review_low",
				Recommendation: "Ask a reviewer to confirm.",
				Confidence:     "low",
				Source:         "model",
				RuleID:         "model-low-confidence",
			},
		}}, nil
	})
	ag, err := New(Config{
		SkillsRoot:    filepath.Join(root, "skills"),
		Runtime:       RuntimeLocalFallback,
		OutputDir:     t.TempDir(),
		Timeout:       testReviewTimeout,
		ModelProvider: provider,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "panic.diff"),
		Mode:     ModeFakeModel,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := countRuleID(result.Findings, "panic-direct"); got != 1 {
		t.Fatalf("expected duplicate rule/model finding to dedupe to one finding, got %d in %+v", got, result.Findings)
	}
	if !hasRuleID(result.Findings, "model-semantic-risk") {
		t.Fatalf("expected high confidence model finding in findings, got %+v", result.Findings)
	}
	if hasRuleID(result.Findings, "model-low-confidence") {
		t.Fatalf("low confidence model finding must not enter findings, got %+v", result.Findings)
	}
	if !hasRuleID(result.Warnings, "model-low-confidence") || !hasRuleID(result.HumanReviewItems, "model-low-confidence") {
		t.Fatalf("expected low confidence model finding in warnings and human review, warnings=%+v human=%+v", result.Warnings, result.HumanReviewItems)
	}
}

func TestLowConfidenceModelFindingPersistsAsHumanReviewEvidence(t *testing.T) {

	root := repoRoot(t)
	outDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	provider := llm.ProviderFunc(func(ctx context.Context, input llm.Input) (llm.Output, error) {
		_ = ctx
		_ = input
		return llm.Output{Findings: []review.Finding{{
			Severity:       "low",
			Category:       "logic",
			File:           "main.go",
			Line:           9,
			Title:          "Needs reviewer confirmation",
			Evidence:       "ambiguous model signal",
			Recommendation: "Ask a human reviewer to confirm the behavior.",
			Confidence:     "low",
			Source:         "model",
			RuleID:         "model-human-review-low",
		}}}, nil
	})
	ag, err := New(Config{
		SkillsRoot:    filepath.Join(root, "skills"),
		Runtime:       RuntimeLocalFallback,
		OutputDir:     outDir,
		SQLitePath:    dbPath,
		Timeout:       testReviewTimeout,
		ModelProvider: provider,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "safe.diff"),
		Mode:     ModeFakeModel,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !hasRuleID(result.Warnings, "model-human-review-low") ||
		!hasRuleID(result.HumanReviewItems, "model-human-review-low") {
		t.Fatalf("expected low confidence model finding in warnings/human review, warnings=%+v human=%+v", result.Warnings, result.HumanReviewItems)
	}

	reportJSON, err := os.ReadFile(filepath.Join(outDir, "review_report.json"))
	if err != nil {
		t.Fatalf("read report JSON: %v", err)
	}
	if !strings.Contains(string(reportJSON), `"human_review_items"`) ||
		!strings.Contains(string(reportJSON), "model-human-review-low") {
		t.Fatalf("expected report human_review_items to include low confidence model finding: %s", reportJSON)
	}
	diagnostics, err := os.ReadFile(filepath.Join(outDir, "review_diagnostics.json"))
	if err != nil {
		t.Fatalf("read diagnostics: %v", err)
	}
	if !strings.Contains(string(diagnostics), `"status": "needs_human_review"`) {
		t.Fatalf("expected diagnostics conclusion to require human review: %s", diagnostics)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	items, err := store.FindingsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sqlite findings: %v", err)
	}
	if !hasRuleID(items, "model-human-review-low") {
		t.Fatalf("expected sqlite to persist low confidence model finding, got %+v", items)
	}
}

// TestModelProviderRedactsInputOutputReportsAndSQLite 固定模型输入输出都经过脱敏边界。
func TestModelProviderRedactsInputOutputReportsAndSQLite(t *testing.T) {

	root := repoRoot(t)
	outDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	diffPath := filepath.Join(t.TempDir(), "model-secret.diff")
	rawSecret := "sk-modelsecret-1234567890"
	diff := `diff --git a/secret.go b/secret.go
--- /dev/null
+++ b/secret.go
@@ -0,0 +1,5 @@
+package secretdemo
+
+func Configure() {
+	_ = "` + rawSecret + `"
+}
`
	if err := os.WriteFile(diffPath, []byte(diff), 0o644); err != nil {
		t.Fatalf("write diff: %v", err)
	}
	provider := llm.ProviderFunc(func(ctx context.Context, input llm.Input) (llm.Output, error) {
		_ = ctx
		if strings.Contains(input.DiffSummary, rawSecret) {
			t.Fatalf("model input leaked raw secret: %s", input.DiffSummary)
		}
		return llm.Output{Findings: []review.Finding{{
			Severity:       "medium",
			Category:       "security",
			File:           "secret.go",
			Line:           4,
			Title:          "Model evidence contains secret",
			Evidence:       "model saw " + rawSecret,
			Recommendation: "Remove the secret.",
			Confidence:     "high",
			Source:         "model",
			RuleID:         "model-secret-risk",
		}}}, nil
	})
	ag, err := New(Config{
		SkillsRoot:    filepath.Join(root, "skills"),
		Runtime:       RuntimeLocalFallback,
		SQLitePath:    dbPath,
		OutputDir:     outDir,
		Timeout:       testReviewTimeout,
		ModelProvider: provider,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: diffPath,
		Mode:     ModeFakeModel,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !hasRuleID(result.Findings, "model-secret-risk") {
		t.Fatalf("expected model secret risk finding, got %+v", result.Findings)
	}
	for _, finding := range result.Findings {
		if strings.Contains(finding.Evidence, rawSecret) {
			t.Fatalf("result finding leaked raw model output secret: %+v", finding)
		}
	}
	for _, name := range []string{"review_report.json", "review_report.md", "review_report.zh.md", "review_diagnostics.json"} {
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(data), rawSecret) {
			t.Fatalf("%s leaked raw model secret: %s", name, data)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	defer db.Close()
	leaks, err := scanSQLiteForRawSecrets(context.Background(), db, []string{rawSecret})
	if err != nil {
		t.Fatalf("scan sqlite: %v", err)
	}
	if len(leaks) > 0 {
		t.Fatalf("sqlite persisted raw model secret: %s", strings.Join(leaks, ", "))
	}
}

// TestModelProviderFailureDoesNotAbortReview 固定模型失败降级为人工复核和指标异常。
func TestModelProviderFailureDoesNotAbortReview(t *testing.T) {

	root := repoRoot(t)
	provider := llm.ProviderFunc(func(ctx context.Context, input llm.Input) (llm.Output, error) {
		_ = ctx
		_ = input
		return llm.Output{}, errors.New("provider failed with token=sk-modelboom-1234567890")
	})
	ag, err := New(Config{
		SkillsRoot:    filepath.Join(root, "skills"),
		Runtime:       RuntimeLocalFallback,
		OutputDir:     t.TempDir(),
		Timeout:       testReviewTimeout,
		ModelProvider: provider,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "safe.diff"),
		Mode:     ModeFakeModel,
	})
	if err != nil {
		t.Fatalf("model provider failure should not abort review: %v", err)
	}
	if result.Metrics.ModelExceptionCount != 1 || result.Metrics.ExceptionCounts["model_provider"] != 1 {
		t.Fatalf("expected model exception metrics, got %+v", result.Metrics)
	}
	if result.Conclusion.Status != "needs_human_review" {
		t.Fatalf("expected model failure to require human review, got %+v", result.Conclusion)
	}
	if !hasRuleID(result.HumanReviewItems, "model-provider-failed") {
		t.Fatalf("expected model failure human review item, got %+v", result.HumanReviewItems)
	}
	for _, item := range result.HumanReviewItems {
		if strings.Contains(item.Evidence, "sk-modelboom-1234567890") {
			t.Fatalf("model failure leaked raw secret: %+v", item)
		}
	}
}

// TestHTTPModelProviderCallsServerAndMergesFindings 固定显式开启的 HTTP provider 链路。
func TestHTTPModelProviderCallsServerAndMergesFindings(t *testing.T) {
	recorder := useAgentTelemetrySpanRecorder(t)
	root := repoRoot(t)
	outDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	apiKey := "sk-http-provider-1234567890"
	t.Setenv("CR_AGENT_TEST_MODEL_KEY", apiKey)

	var seenCalls int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seenCalls++
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		if r.URL.String() != "https://model.test/review" {
			t.Fatalf("unexpected provider URL: %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Fatalf("expected bearer authorization header, got %q", got)
		}
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read provider request body: %v", err)
		}
		if !strings.Contains(string(rawBody), `"diff_summary"`) || strings.Contains(string(rawBody), `"DiffSummary"`) {
			t.Fatalf("provider request should use snake_case llm.Input JSON keys: %s", rawBody)
		}
		var req llm.HTTPReviewRequest
		if err := json.Unmarshal(rawBody, &req); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		if req.Model != "review-test-model" {
			t.Fatalf("expected model name in request, got %+v", req)
		}
		body := string(rawBody)
		for _, raw := range []string{apiKey, "sk-modelsecret-1234567890"} {
			if strings.Contains(body, raw) {
				t.Fatalf("provider request leaked raw secret %q: %s", raw, body)
			}
		}
		if !strings.Contains(req.Input.DiffSummary, "[REDACTED]") {
			t.Fatalf("expected redacted diff summary, got %s", req.Input.DiffSummary)
		}
		return jsonHTTPResponse(t, http.StatusOK, llm.Output{Findings: []review.Finding{
			{
				Severity:       "high",
				Category:       "error_handling",
				File:           "panic.go",
				Line:           2,
				Title:          "Duplicate HTTP model panic finding",
				Evidence:       "duplicate from http provider",
				Recommendation: "Use errors.",
				Confidence:     "high",
				Source:         "model",
				RuleID:         "panic-direct",
			},
			{
				Severity:       "medium",
				Category:       "logic",
				File:           "secret.go",
				Line:           4,
				Title:          "HTTP model semantic risk",
				Evidence:       "provider saw sk-modelsecret-1234567890",
				Recommendation: "Add validation for the semantic branch.",
				Confidence:     "high",
				Source:         "model",
				RuleID:         "http-model-semantic-risk",
			},
			{
				Severity:       "low",
				Category:       "maintainability",
				File:           "secret.go",
				Line:           5,
				Title:          "HTTP model low confidence hint",
				Evidence:       "low confidence signal",
				Recommendation: "Ask a reviewer to confirm.",
				Confidence:     "low",
				Source:         "model",
				RuleID:         "http-model-low-confidence",
			},
		}}), nil
	})

	diffPath := filepath.Join(t.TempDir(), "http-model.diff")
	diff := `diff --git a/panic.go b/panic.go
--- a/panic.go
+++ b/panic.go
@@ -1,2 +1,5 @@
 package foo
+
+func Crash() { panic("boom") }
+const apiKey = "sk-modelsecret-1234567890"
`
	if err := os.WriteFile(diffPath, []byte(diff), 0o644); err != nil {
		t.Fatalf("write diff: %v", err)
	}
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		SQLitePath: dbPath,
		OutputDir:  outDir,
		Timeout:    testReviewTimeout,
		ModelHTTP: llm.HTTPConfig{
			Enabled:   true,
			Endpoint:  "https://model.test/review",
			APIKeyEnv: "CR_AGENT_TEST_MODEL_KEY",
			Model:     "review-test-model",
			Timeout:   2 * time.Second,
			Client:    &http.Client{Transport: transport},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: diffPath,
		Mode:     ModeFakeModel,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if seenCalls != 1 {
		t.Fatalf("expected one HTTP provider call, got %d", seenCalls)
	}
	if got := countRuleID(result.Findings, "panic-direct"); got != 1 {
		t.Fatalf("expected HTTP duplicate to dedupe with rule finding, got %d in %+v", got, result.Findings)
	}
	if !hasRuleID(result.Findings, "http-model-semantic-risk") {
		t.Fatalf("expected high confidence HTTP model finding in findings, got %+v", result.Findings)
	}
	if !hasRuleID(result.Warnings, "http-model-low-confidence") || !hasRuleID(result.HumanReviewItems, "http-model-low-confidence") {
		t.Fatalf("expected low confidence HTTP model finding in warnings/human review, warnings=%+v human=%+v", result.Warnings, result.HumanReviewItems)
	}
	if result.Metrics.ModelCallCount != 1 || result.Metrics.ModelFindingCount != 2 {
		t.Fatalf("expected HTTP model metrics, got %+v", result.Metrics)
	}
	assertNoSecretInResult(t, result, apiKey, "sk-modelsecret-1234567890")
	assertNoRawSecretsInSpanAttributes(t, findAgentReviewSpan(t, recorder), apiKey, "sk-modelsecret-1234567890")
	for _, name := range []string{"review_report.json", "review_report.md", "review_report.zh.md", "review_diagnostics.json"} {
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		assertNoRawSecrets(t, name, string(data), apiKey, "sk-modelsecret-1234567890")
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	defer db.Close()
	leaks, err := scanSQLiteForRawSecrets(context.Background(), db, []string{apiKey, "sk-modelsecret-1234567890"})
	if err != nil {
		t.Fatalf("scan sqlite: %v", err)
	}
	if len(leaks) > 0 {
		t.Fatalf("sqlite persisted raw HTTP model secrets: %s", strings.Join(leaks, ", "))
	}
}

// TestHTTPModelProviderFailureDoesNotAbortReview 固定 HTTP provider 失败降级。
func TestHTTPModelProviderFailureDoesNotAbortReview(t *testing.T) {

	root := repoRoot(t)
	cases := []struct {
		name      string
		secret    string
		configure func() llm.HTTPConfig
	}{
		{
			name:   "transport-error",
			secret: "sk-providertransport-1234567890",
			configure: func() llm.HTTPConfig {
				return llm.HTTPConfig{
					Enabled:  true,
					Endpoint: "https://model.test/review",
					Model:    "review-test-model",
					Timeout:  2 * time.Second,
					Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						_ = r
						return nil, errors.New("transport failed token=sk-providertransport-1234567890")
					})},
				}
			},
		},
		{
			name:   "non-2xx",
			secret: "sk-providerboom-1234567890",
			configure: func() llm.HTTPConfig {
				return llm.HTTPConfig{
					Enabled:  true,
					Endpoint: "https://model.test/review",
					Model:    "review-test-model",
					Timeout:  2 * time.Second,
					Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						_ = r
						return textHTTPResponse(http.StatusBadGateway, "provider failed token=sk-providerboom-1234567890"), nil
					})},
				}
			},
		},
		{
			name:   "invalid-json",
			secret: "sk-providerjson-1234567890",
			configure: func() llm.HTTPConfig {
				return llm.HTTPConfig{
					Enabled:  true,
					Endpoint: "https://model.test/review",
					Model:    "review-test-model",
					Timeout:  2 * time.Second,
					Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						_ = r
						return textHTTPResponse(http.StatusOK, `{"findings":[ token="sk-providerjson-1234567890"`), nil
					})},
				}
			},
		},
		{
			name:   "deadline",
			secret: "sk-providerdeadline-1234567890",
			configure: func() llm.HTTPConfig {
				return llm.HTTPConfig{
					Enabled:  true,
					Endpoint: "https://model.test/review",
					Model:    "review-test-model",
					Timeout:  2 * time.Second,
					Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						_ = r
						return nil, fmt.Errorf("deadline token=sk-providerdeadline-1234567890: %w", context.DeadlineExceeded)
					})},
				}
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ag, err := New(Config{
				SkillsRoot: filepath.Join(root, "skills"),
				Runtime:    RuntimeLocalFallback,
				OutputDir:  t.TempDir(),
				Timeout:    testReviewTimeout,
				ModelHTTP:  tc.configure(),
			})
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			defer ag.Close()

			result, err := ag.Run(context.Background(), Request{
				DiffFile: filepath.Join(root, "testdata", "fixtures", "safe.diff"),
				Mode:     ModeFakeModel,
			})
			if err != nil {
				t.Fatalf("HTTP provider failure should not abort review: %v", err)
			}
			if result.Metrics.ModelExceptionCount != 1 || result.Metrics.ExceptionCounts["model_provider"] != 1 {
				t.Fatalf("expected HTTP model exception metrics, got %+v", result.Metrics)
			}
			if result.Conclusion.Status != "needs_human_review" || !hasRuleID(result.HumanReviewItems, "model-provider-failed") {
				t.Fatalf("expected HTTP model failure human review, conclusion=%+v human=%+v", result.Conclusion, result.HumanReviewItems)
			}
			assertNoSecretInResult(t, result, tc.secret)
		})
	}
}

// TestRuleOnlyAndDryRunSkipModelProvider 固定兼容模式不调用模型边界。
func TestRuleOnlyAndDryRunSkipModelProvider(t *testing.T) {

	root := repoRoot(t)
	provider := &countingModelProvider{}
	for _, mode := range []string{ModeRuleOnly, ModeDryRun} {
		ag, err := New(Config{
			SkillsRoot:    filepath.Join(root, "skills"),
			Runtime:       RuntimeLocalFallback,
			OutputDir:     t.TempDir(),
			Timeout:       testReviewTimeout,
			ModelProvider: provider,
		})
		if err != nil {
			t.Fatalf("New returned error: %v", err)
		}
		result, err := ag.Run(context.Background(), Request{
			DiffFile: filepath.Join(root, "testdata", "fixtures", "secret.diff"),
			Mode:     mode,
		})
		_ = ag.Close()
		if err != nil {
			t.Fatalf("Run(%s) returned error: %v", mode, err)
		}
		if result.Metrics.ModelCallCount != 0 || result.Metrics.ModelFindingCount != 0 {
			t.Fatalf("%s should not record model metrics, got %+v", mode, result.Metrics)
		}
	}
	if provider.calls != 0 {
		t.Fatalf("rule-only and dry-run must not call model provider, calls=%d", provider.calls)
	}
}

// TestAgentRunSandboxModeExecutesGoChecks 固定 sandbox Go 检查。
func TestAgentRunSandboxModeExecutesGoChecks(t *testing.T) {

	root := repoRoot(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/demo\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package demo\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo_test.go"), []byte("package demo\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(\"bad\") } }\n"), 0o644); err != nil {
		t.Fatalf("write foo_test.go: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		SQLitePath: dbPath,
		OutputDir:  t.TempDir(),
		Timeout:    10 * time.Second,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		RepoPath: repo,
		Mode:     ModeSandbox,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	decisions, err := store.DecisionsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load decisions: %v", err)
	}
	assertDecisionForCommand(t, decisions, "go test ./...")
	assertDecisionForCommand(t, decisions, "go vet ./...")
	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	assertRunForCommand(t, runs, "go test ./...")
	assertRunForCommand(t, runs, "go vet ./...")
}

func TestReviewCanCombineSandboxAndModel(t *testing.T) {
	root := repoRoot(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/combined\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package combined\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	var modelInput llm.Input
	provider := llm.ProviderFunc(func(_ context.Context, input llm.Input) (llm.Output, error) {
		modelInput = input
		return llm.Output{}, nil
	})
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"), Runtime: RuntimeLocalFallback,
		OutputDir: t.TempDir(), Timeout: 10 * time.Second, ModelProvider: provider,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()
	enabled := true
	result, err := ag.Run(context.Background(), Request{
		RepoPath: repo, Mode: ModeReview, SandboxEnabled: &enabled, ModelEnabled: &enabled,
	})
	if err != nil {
		t.Fatalf("combined review: %v", err)
	}
	if !result.Metrics.SandboxRequested || !result.Metrics.SandboxExecuted || !result.Metrics.ModelRequested || !result.Metrics.ModelExecuted {
		t.Fatalf("combined capability audit: %+v", result.Metrics)
	}
	if len(modelInput.SandboxSummary.Runs) < 3 {
		t.Fatalf("model must receive Skill and Go-check summaries: %+v", modelInput.SandboxSummary)
	}
}

// TestRunGoSandboxCommandPrefersWorkspaceExec 固定 workspaceexec 成功时不触发 codeexec 兜底。
func TestRunGoSandboxCommandPrefersWorkspaceExec(t *testing.T) {

	root := repoRoot(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/workspaceprimary\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package workspaceprimary\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo_test.go"), []byte("package workspaceprimary\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(\"bad\") } }\n"), 0o644); err != nil {
		t.Fatalf("write foo_test.go: %v", err)
	}

	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		OutputDir:  t.TempDir(),
		Timeout:    10 * time.Second,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()
	fallback := &recordingTool{
		name: "execute_code",
		call: func(ctx context.Context, jsonArgs []byte) (any, error) {
			_ = ctx
			t.Fatalf("codeexec fallback should not be called after workspaceexec success: %s", jsonArgs)
			return nil, nil
		},
	}
	ag.checkTool = fallback

	decisions, run := ag.runGoSandboxCommand(context.Background(), "task-workspace-primary", repo, "go test ./...")
	if len(decisions) != 1 || decisions[0].Action != "allow" {
		t.Fatalf("expected one allow decision, got %+v", decisions)
	}
	if run.Status != "ok" {
		t.Fatalf("expected workspaceexec go test to succeed, got %+v", run)
	}
	if fallback.calls != 0 {
		t.Fatalf("codeexec fallback should not be called after workspaceexec success, calls=%d", fallback.calls)
	}
}

// TestRunGoSandboxCommandFallsBackToCodeExec 固定 workspaceexec 不可用时保留 codeexec 兜底。
func TestRunGoSandboxCommandFallsBackToCodeExec(t *testing.T) {

	root := repoRoot(t)
	repo := t.TempDir()
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		OutputDir:  t.TempDir(),
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()
	ag.exec = nil
	fallback := &recordingTool{
		name: "execute_code",
		call: func(ctx context.Context, jsonArgs []byte) (any, error) {
			_ = ctx
			if !strings.Contains(string(jsonArgs), "go vet ./...") {
				t.Fatalf("expected fallback args to include go vet command, got %s", jsonArgs)
			}
			return map[string]any{"output": "fallback ok"}, nil
		},
	}
	ag.checkTool = fallback

	decisions, run := ag.runGoSandboxCommand(context.Background(), "task-workspace-fallback", repo, "go vet ./...")
	if len(decisions) != 2 {
		t.Fatalf("expected workspace and codeexec decisions, got %+v", decisions)
	}
	for _, decision := range decisions {
		if decision.Action != "allow" {
			t.Fatalf("expected allow decision, got %+v", decision)
		}
	}
	if fallback.calls != 1 {
		t.Fatalf("expected exactly one codeexec fallback call, got %d", fallback.calls)
	}
	if run.Status != "ok" || run.StdoutDigest == "" {
		t.Fatalf("expected successful fallback sandbox run with digest, got %+v", run)
	}
}

func TestRunGoSandboxCommandDoesNotCallFallbackWhenSecondDecisionAsks(t *testing.T) {

	root := repoRoot(t)
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		OutputDir:  t.TempDir(),
		Timeout:    testReviewTimeout,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()
	ag.exec = nil
	fallback := &recordingTool{name: "execute_code", call: func(context.Context, []byte) (any, error) {
		t.Fatal("fallback must not execute after ask")
		return nil, nil
	}}
	ag.checkTool = fallback
	checks := 0
	ag.policy = tool.PermissionPolicyFunc(func(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
		checks++
		if req.ToolName == "workspace_exec" {
			return tool.AllowPermission(), nil
		}
		return tool.AskPermission("human approval required"), nil
	})

	decisions, run := ag.runGoSandboxCommand(context.Background(), "task-fallback-ask", t.TempDir(), "go vet ./...")
	if checks != 2 || len(decisions) != 2 {
		t.Fatalf("permission checks=%d decisions=%+v, want two", checks, decisions)
	}
	if decisions[1].Action != "ask" || run.Status != "ask" || fallback.calls != 0 {
		t.Fatalf("fallback ask boundary violated: decisions=%+v run=%+v calls=%d", decisions, run, fallback.calls)
	}
}

func TestSandboxRunOutputKeepsValidUTF8AtByteLimit(t *testing.T) {

	got := sandboxRunOutput("ab界cd", 4)
	if !utf8.ValidString(got) {
		t.Fatalf("sandbox output is invalid UTF-8: %q", got)
	}
	if len(got) > 4 {
		t.Fatalf("sandbox output exceeded byte limit: %d > 4", len(got))
	}
}

func TestFinalizeReviewResultDoesNotDoubleCountSandboxFailures(t *testing.T) {

	ctx := reviewResultContext{
		TaskID:    "task-finalize",
		StartedAt: time.Now(),
		Runs:      []storage.SandboxRunRecord{{Command: "go test ./...", Status: "failed"}},
	}
	result := finalizeReviewResult(review.Result{}, ctx)
	result = finalizeReviewResult(result, ctx)
	if got := result.Metrics.ExceptionCounts["sandbox_failed"]; got != 1 {
		t.Fatalf("sandbox_failed count = %d, want 1", got)
	}
}

// TestAgentRunSandboxModeRecordsGoCheckFailure 固定 Go 检查失败可审计。
func TestAgentRunSandboxModeRecordsGoCheckFailure(t *testing.T) {

	root := repoRoot(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/faildemo\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package faildemo\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo_test.go"), []byte("package faildemo\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 4 { t.Fatal(\"bad\") } }\n"), 0o644); err != nil {
		t.Fatalf("write foo_test.go: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Runtime:    RuntimeLocalFallback,
		SQLitePath: dbPath,
		OutputDir:  t.TempDir(),
		Timeout:    10 * time.Second,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		RepoPath: repo,
		Mode:     ModeSandbox,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := result.Metrics.ExceptionCounts["sandbox_failed"]; got == 0 {
		t.Fatalf("expected sandbox_failed metric, got %+v", result.Metrics.ExceptionCounts)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	for _, run := range runs {
		if run.Command == "go test ./..." {
			if run.Status != "failed" || run.ExitCode == 0 {
				t.Fatalf("expected failed go test run with exit code, got %+v", run)
			}
			if run.OutputLimitBytes != ag.cfg.OutputLimitBytes || run.EnvWhitelist != sandboxEnvWhitelist {
				t.Fatalf("expected failed go test run to record safety bounds, got %+v", run)
			}
			if run.StdoutDigest == "" || run.Output == "" {
				t.Fatalf("expected failed go test run to keep bounded output and digest, got %+v", run)
			}
			if strings.Contains(run.Output, "Error executing code block") && len(run.Output) > ag.cfg.OutputLimitBytes {
				t.Fatalf("failed go test output exceeded configured limit: %d > %d", len(run.Output), ag.cfg.OutputLimitBytes)
			}
			return
		}
	}
	t.Fatalf("go test sandbox run not found: %+v", runs)
}

// TestAgentRunSandboxModeOptionallyExecutesStaticcheck 固定 staticcheck 显式开启。
func TestAgentRunSandboxModeOptionallyExecutesStaticcheck(t *testing.T) {

	root := repoRoot(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/staticdemo\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package staticdemo\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot:        filepath.Join(root, "skills"),
		Runtime:           RuntimeLocalFallback,
		SQLitePath:        dbPath,
		OutputDir:         t.TempDir(),
		Timeout:           10 * time.Second,
		EnableStaticcheck: true,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		RepoPath: repo,
		Mode:     ModeSandbox,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	decisions, err := store.DecisionsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load decisions: %v", err)
	}
	assertDecisionForCommand(t, decisions, "staticcheck ./...")
	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	assertAnyRunForCommand(t, runs, "staticcheck ./...")
}

// TestAgentRunContainerRuntimeExecutesGoChecks 验证真实容器链路。
func TestAgentRunContainerRuntimeExecutesGoChecks(t *testing.T) {
	if os.Getenv("CR_AGENT_RUN_CONTAINER_TESTS") != "1" {
		t.Skip("set CR_AGENT_RUN_CONTAINER_TESTS=1 to run Docker container integration test")
	}

	root := repoRoot(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/containerdemo\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package containerdemo\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foo_test.go"), []byte("package containerdemo\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(\"bad\") } }\n"), 0o644); err != nil {
		t.Fatalf("write foo_test.go: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "review.db")
	ag, err := New(Config{
		SkillsRoot:            filepath.Join(root, "skills"),
		Runtime:               RuntimeContainer,
		SQLitePath:            dbPath,
		OutputDir:             t.TempDir(),
		Timeout:               60 * time.Second,
		ContainerRepoHostPath: repo,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer ag.Close()

	result, err := ag.Run(context.Background(), Request{
		DiffFile: filepath.Join(root, "testdata", "fixtures", "secret.diff"),
		RepoPath: repo,
		Mode:     ModeSandbox,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Findings) == 0 || result.Findings[0].RuleID != "secret-leak" {
		t.Fatalf("container Skill must produce the fixture finding, got %+v warnings=%+v", result.Findings, result.Warnings)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	runs, err := store.SandboxRunsByTaskID(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("load sandbox runs: %v", err)
	}
	assertRunForCommand(t, runs, "go test ./...")
	assertRunForCommand(t, runs, "go vet ./...")
	for _, run := range runs {
		if strings.Contains(run.Command, "go ") && run.Runtime != RuntimeContainer {
			t.Fatalf("go check should run in container runtime, got %+v", run)
		}
	}
}

func TestSandboxRepoPathForRuntime(t *testing.T) {

	hostRepo := filepath.Join(t.TempDir(), "repo")
	localPath := execution.SandboxRepoPathForRuntime(RuntimeLocalFallback, hostRepo)
	if localPath != hostRepo {
		t.Fatalf("local fallback path = %q, want %q", localPath, hostRepo)
	}
	containerPath := execution.SandboxRepoPathForRuntime(RuntimeContainer, hostRepo)
	if containerPath != containerRepoMountPath {
		t.Fatalf("container path = %q, want %q", containerPath, containerRepoMountPath)
	}
}

func TestGoSandboxCodeUsesRuntimeRepoPath(t *testing.T) {

	hostRepo := filepath.Join(t.TempDir(), "repo")
	code := execution.SandboxCode(RuntimeContainer, hostRepo, "go test ./...")
	if !strings.Contains(code, "cd "+execution.ShellQuote(containerRepoMountPath)) {
		t.Fatalf("container command should cd into mount path, got %q", code)
	}
	if !strings.Contains(code, "GOCACHE="+execution.ShellQuote(goSandboxCacheDir)) {
		t.Fatalf("container command should set sandbox Go cache, got %q", code)
	}
	if strings.Contains(code, hostRepo) {
		t.Fatalf("container command leaked host repo path %q: %q", hostRepo, code)
	}
}

func TestGoSandboxEnvIncludesContainerGoPath(t *testing.T) {

	env := execution.SandboxEnv(RuntimeContainer)
	if env["GOCACHE"] != goSandboxCacheDir {
		t.Fatalf("GOCACHE = %q, want %q", env["GOCACHE"], goSandboxCacheDir)
	}
	if !strings.Contains(env["PATH"], "/usr/local/go/bin") {
		t.Fatalf("PATH should include Go toolchain path, got %q", env["PATH"])
	}
	if !strings.Contains(sandboxEnvWhitelist, "PATH") {
		t.Fatalf("sandbox whitelist should disclose PATH boundary, got %q", sandboxEnvWhitelist)
	}
}

func TestGoSandboxExecCommandUsesContainerGoBinary(t *testing.T) {

	containerCommand := execution.SandboxExecCommand(RuntimeContainer, "go test ./...")
	if containerCommand != goSandboxBinary+" test ./..." {
		t.Fatalf("container go command = %q, want absolute go binary", containerCommand)
	}
	localCommand := execution.SandboxExecCommand(RuntimeLocalFallback, "go test ./...")
	if localCommand != "go test ./..." {
		t.Fatalf("local fallback command = %q, want original command", localCommand)
	}
	staticcheckCommand := execution.SandboxExecCommand(RuntimeContainer, "staticcheck ./...")
	if staticcheckCommand != "staticcheck ./..." {
		t.Fatalf("staticcheck command = %q, want original command", staticcheckCommand)
	}
}

// repoRoot 查找仓库根目录。
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatalf("repo root not found from %s", dir)
		}
		dir = next
	}
}

// useAgentTelemetrySpanRecorder 捕获官方 telemetry trace。
func useAgentTelemetrySpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	originalProvider := telemetrytrace.TracerProvider
	originalTracer := telemetrytrace.Tracer
	telemetrytrace.TracerProvider = provider
	telemetrytrace.Tracer = provider.Tracer("cr-agent-test")
	t.Cleanup(func() {
		telemetrytrace.TracerProvider = originalProvider
		telemetrytrace.Tracer = originalTracer
		_ = provider.Shutdown(context.Background())
	})
	return recorder
}

func findAgentReviewSpan(t *testing.T, recorder *tracetest.SpanRecorder) tracesdk.ReadOnlySpan {
	t.Helper()

	for _, span := range recorder.Ended() {
		if span.Name() == "cr-agent.review" {
			return span
		}
	}
	t.Fatalf("cr-agent.review span not found; got %d spans", len(recorder.Ended()))
	return nil
}

func eventObjects(events []*agentevent.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if ev != nil && ev.Response != nil {
			out = append(out, ev.Object)
		}
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func agentSpanAttributes(attrs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}

func assertNoRawSecretsInSpanAttributes(t *testing.T, span tracesdk.ReadOnlySpan, secrets ...string) {
	t.Helper()

	for _, attr := range span.Attributes() {
		value := attr.Value.Emit()
		for _, secret := range secrets {
			if strings.Contains(value, secret) {
				t.Fatalf("telemetry attribute %s leaked raw secret %q: %s", attr.Key, secret, value)
			}
		}
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type recordingTool struct {
	name  string
	calls int
	call  func(context.Context, []byte) (any, error)
}

func (t *recordingTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func (t *recordingTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	t.calls++
	return t.call(ctx, jsonArgs)
}

type countingModelProvider struct {
	calls int
}

func (p *countingModelProvider) Review(ctx context.Context, input llm.Input) (llm.Output, error) {
	_ = ctx
	_ = input
	p.calls++
	return llm.Output{}, nil
}

func hasFindingSource(findings []review.Finding, source string) bool {
	for _, finding := range findings {
		if finding.Source == source {
			return true
		}
	}
	return false
}

func hasRuleID(findings []review.Finding, ruleID string) bool {
	for _, finding := range findings {
		if finding.RuleID == ruleID {
			return true
		}
	}
	return false
}

func countRuleID(findings []review.Finding, ruleID string) int {
	count := 0
	for _, finding := range findings {
		if finding.RuleID == ruleID {
			count++
		}
	}
	return count
}

func mustJSONText(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(data)
}

func assertNoSecretInResult(t *testing.T, result review.Result, secrets ...string) {
	t.Helper()
	data := string(review.MustJSON(result))
	for _, secret := range secrets {
		if strings.Contains(data, secret) {
			t.Fatalf("review result leaked raw secret %q: %s", secret, data)
		}
	}
}

func assertNoRawSecrets(t *testing.T, label string, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaked raw secret %q: %s", label, secret, text)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonHTTPResponse(t *testing.T, status int, v any) *http.Response {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}
}

func textHTTPResponse(status int, text string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(text)),
	}
}

// assertDecisionForCommand 检查 allow 决策。
func assertDecisionForCommand(t *testing.T, decisions []sqlite.DecisionRecord, command string) {
	t.Helper()
	for _, decision := range decisions {
		if decision.Command == command && decision.Action == "allow" {
			return
		}
	}
	t.Fatalf("expected allow decision for %q, got %+v", command, decisions)
}

// assertRunForCommand 检查成功沙箱记录。
func assertRunForCommand(t *testing.T, runs []sqlite.SandboxRunRecord, command string) {
	t.Helper()
	for _, run := range runs {
		if run.Command == command && run.Status == "ok" && run.DurationMS >= 0 {
			if run.EnvWhitelist != sandboxEnvWhitelist {
				t.Fatalf("expected sandbox env whitelist %q, got %+v", sandboxEnvWhitelist, run)
			}
			return
		}
	}
	t.Fatalf("expected ok sandbox run for %q, got %+v", command, runs)
}

// assertAnyRunForCommand 检查沙箱记录存在。
func assertAnyRunForCommand(t *testing.T, runs []sqlite.SandboxRunRecord, command string) {
	t.Helper()
	for _, run := range runs {
		if run.Command == command && run.Status != "" {
			return
		}
	}
	t.Fatalf("expected sandbox run for %q, got %+v", command, runs)
}

// scanSQLiteForRawSecrets 扫描明文密钥。
func scanSQLiteForRawSecrets(ctx context.Context, db *sql.DB, secrets []string) ([]string, error) {
	tables, err := sqliteTableNames(ctx, db)
	if err != nil {
		return nil, err
	}
	var leaks []string
	for _, table := range tables {
		columns, err := sqliteTextColumns(ctx, db, table)
		if err != nil {
			return nil, err
		}
		for _, column := range columns {
			values, err := sqliteColumnValues(ctx, db, table, column)
			if err != nil {
				return nil, err
			}
			for _, value := range values {
				for _, secret := range secrets {
					if strings.Contains(value, secret) {
						leaks = append(leaks, table+"."+column+" contains "+secret)
					}
				}
			}
		}
	}
	return leaks, nil
}

// sqliteTableNames 返回用户表名。
func sqliteTableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT name FROM sqlite_schema
WHERE type='table' AND name NOT LIKE 'sqlite_%'
ORDER BY name
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

// sqliteTextColumns 返回文本列。
func sqliteTextColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		upperType := strings.ToUpper(columnType)
		if strings.Contains(upperType, "TEXT") || strings.Contains(upperType, "BLOB") {
			columns = append(columns, name)
		}
	}
	return columns, rows.Err()
}

// sqliteColumnValues 读取列值。
func sqliteColumnValues(ctx context.Context, db *sql.DB, table string, column string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+column+" FROM "+table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if value.Valid {
			values = append(values, value.String)
		}
	}
	return values, rows.Err()
}
