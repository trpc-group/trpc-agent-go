//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAgent_ReviewDryRun_CleanDiff(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "01_clean.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "completed", result.Task.Status)
	// Clean diff should have no critical/high findings.
	for _, f := range result.Findings {
		require.NotEqual(t, SeverityCritical, f.Severity,
			"clean diff should not have critical findings")
	}
}

func TestAgentGeneratedIDsUseFullUUIDs(t *testing.T) {
	storage := newTestStorage(t)
	agent := NewReviewAgent(storage)
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: filepath.Join("..", "fixtures", "02_security.diff"),
		DryRun:   true,
	})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(result.TaskID, "review-"))
	_, err = uuid.Parse(strings.TrimPrefix(result.TaskID, "review-"))
	require.NoError(t, err, "task IDs must retain the full UUID entropy")
	for _, finding := range result.Findings {
		_, err := uuid.Parse(finding.ID)
		require.NoError(t, err, "finding IDs must retain the full UUID entropy")
	}
}

func TestAgent_ReviewDryRun_SecurityDiff(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "02_security.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Findings)

	// Should detect security issues.
	hasSecurity := false
	for _, f := range result.Findings {
		if f.Category == "security" {
			hasSecurity = true
			break
		}
	}
	require.True(t, hasSecurity, "expected security findings")

	// Should have generated reports.
	require.NotEmpty(t, result.ReportJSON)
	require.NotEmpty(t, result.ReportMD)
}

func TestAgent_ReviewDryRun_AllFixtures(t *testing.T) {
	fixtures := []string{
		"01_clean.diff",
		"02_security.diff",
		"03_goroutine_leak.diff",
		"04_resource_unclosed.diff",
		"05_db_lifecycle.diff",
		"06_test_missing.diff",
		"07_duplicate.diff",
		"08_sensitive_info.diff",
	}

	for _, fx := range fixtures {
		t.Run(fx, func(t *testing.T) {
			s := newTestStorage(t)
			agent := NewReviewAgent(s)

			diffPath := filepath.Join("..", "fixtures", fx)
			result, err := agent.Review(context.Background(), ReviewInput{
				DiffFile: diffPath,
				DryRun:   true,
			})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "completed", result.Task.Status)
			require.NotEmpty(t, result.ReportJSON)
			require.NotEmpty(t, result.ReportMD)
		})
	}
}

func TestAgent_Review_PersistsToDB(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)
	ctx := context.Background()

	diffPath := filepath.Join("..", "fixtures", "02_security.diff")
	result, err := agent.Review(ctx, ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)

	// Verify task was persisted.
	task, err := s.GetTask(ctx, result.TaskID)
	require.NoError(t, err)
	require.Equal(t, "completed", task.Status)

	// Verify findings were persisted.
	findings, err := s.GetFindingsByTask(ctx, result.TaskID)
	require.NoError(t, err)
	require.NotEmpty(t, findings)
}

func TestAgent_Review_MonitoringMetrics(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "03_goroutine_leak.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, result.Monitoring)
	require.Greater(t, result.Monitoring.TotalDurationMs, int64(0))
	require.Greater(t, result.Monitoring.ToolCallCount, 0)
}

func TestAgent_Review_SensitiveInfoRedacted(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "08_sensitive_info.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)

	// Verify no raw secrets in findings evidence.
	for _, f := range result.Findings {
		require.NotContains(t, f.Evidence, "sk-proj-abcdef1234567890abcdef",
			"API key should be redacted in evidence")
		require.NotContains(t, f.Evidence, "MyP@ssw0rd!2024",
			"password should be redacted in evidence")
	}

	// Verify no raw secrets in report.
	require.NotContains(t, result.ReportJSON, "sk-proj-abcdef1234567890abcdef")
	require.NotContains(t, result.ReportMD, "sk-proj-abcdef1234567890abcdef")
}

func TestAgent_Review_DedupWorks(t *testing.T) {
	s := newTestStorage(t)
	agent := NewReviewAgent(s)

	diffPath := filepath.Join("..", "fixtures", "07_duplicate.diff")
	result, err := agent.Review(context.Background(), ReviewInput{
		DiffFile: diffPath,
		DryRun:   true,
	})
	require.NoError(t, err)

	// Verify no duplicate (file, line, category) in findings.
	seen := map[string]bool{}
	for _, f := range result.Findings {
		key := f.File + ":" + itoa(f.Line) + ":" + f.Category
		require.False(t, seen[key],
			"duplicate finding found: %s", key)
		seen[key] = true
	}
}

type cancelAfterSaveStorage struct {
	Storage
	cancel context.CancelFunc
	done   bool
}

func (s *cancelAfterSaveStorage) SaveTask(ctx context.Context, task *ReviewTask) error {
	if err := s.Storage.SaveTask(ctx, task); err != nil {
		return err
	}
	if !s.done {
		s.done = true
		s.cancel()
	}
	return nil
}

func TestAgentCancellationPersistsTerminalStatus(t *testing.T) {
	storage := newTestStorage(t)
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := &cancelAfterSaveStorage{Storage: storage, cancel: cancel}
	agent := NewReviewAgent(wrapped)

	_, err := agent.Review(ctx, ReviewInput{
		TaskID:   "cancel-after-save",
		DiffFile: filepath.Join("..", "fixtures", "01_clean.diff"),
		DryRun:   true,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled), "unexpected error: %v", err)
	task, getErr := storage.GetTask(context.Background(), "cancel-after-save")
	require.NoError(t, getErr)
	require.Equal(t, "failed", task.Status)
}

func TestAgentRejectsDuplicateTaskIDWithoutReplacingGraph(t *testing.T) {
	storage := newTestStorage(t)
	agent := NewReviewAgent(storage)
	input := ReviewInput{
		TaskID: "stable-task-id", DiffFile: filepath.Join("..", "fixtures", "02_security.diff"), DryRun: true,
	}
	first, err := agent.Review(context.Background(), input)
	require.NoError(t, err)
	firstFindings, err := storage.GetFindingsByTask(context.Background(), first.TaskID)
	require.NoError(t, err)
	require.NotEmpty(t, firstFindings)

	input.DiffFile = filepath.Join("..", "fixtures", "01_clean.diff")
	_, err = agent.Review(context.Background(), input)
	require.ErrorContains(t, err, "save task")
	after, err := storage.GetFindingsByTask(context.Background(), first.TaskID)
	require.NoError(t, err)
	require.Equal(t, firstFindings, after)
}

type recordingScopedSandbox struct {
	scopes       [][]string
	reviewScopes []ReviewScope
}

func (s *recordingScopedSandbox) ExecuteReview(
	_ context.Context,
	taskID string,
	command string,
	decision Decision,
	reason string,
	scope ReviewScope,
) *SandboxRun {
	s.scopes = append(s.scopes, append([]string(nil), scope.FilePaths...))
	s.reviewScopes = append(s.reviewScopes, ReviewScope{
		FilePaths:  append([]string(nil), scope.FilePaths...),
		HeadCommit: scope.HeadCommit,
		DiffSHA256: scope.DiffSHA256,
	})
	return successfulSandboxRun(taskID, command, decision, reason)
}

func successfulSandboxRun(
	taskID string,
	command string,
	decision Decision,
	reason string,
) *SandboxRun {
	return &SandboxRun{
		ID:                 uuid.NewString(),
		TaskID:             taskID,
		Command:            command,
		PermissionDecision: decision,
		PermissionReason:   reason,
		Status:             SandboxStatusSuccess,
	}
}

func TestAgentPassesSelectedPathsToScopedSandbox(t *testing.T) {
	repo := initGitRepository(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "go.mod"),
		[]byte("module example.com/review\n\ngo 1.21\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "selected.go"), []byte("package review\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "outside.go"), []byte("package review\n"), 0o600))
	gitAdd(t, repo, "go.mod", "selected.go", "outside.go")
	gitCommit(t, repo)
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "selected.go"),
		[]byte("package review\n\nconst Selected = true\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "outside.go"),
		[]byte("package review\n\nconst Outside = true\n"),
		0o600,
	))

	sandbox := &recordingScopedSandbox{}
	agent := NewReviewAgentWithSandbox(newTestStorage(t), sandbox)
	_, err := agent.Review(context.Background(), ReviewInput{
		RepoPath:  repo,
		FilePaths: []string{"selected.go"},
	})
	require.NoError(t, err)
	require.Equal(t, [][]string{{"selected.go"}, {"selected.go"}}, sandbox.scopes)
}

type advanceHEADAfterDiffStorage struct {
	Storage
	advance func() error
}

func (s *advanceHEADAfterDiffStorage) UpdateTaskDiff(
	ctx context.Context,
	taskID string,
	inputType string,
	inputPath string,
	diffSummary string,
) error {
	if err := s.Storage.UpdateTaskDiff(
		ctx,
		taskID,
		inputType,
		inputPath,
		diffSummary,
	); err != nil {
		return err
	}
	if s.advance == nil {
		return nil
	}
	advance := s.advance
	s.advance = nil
	return advance()
}

func TestAgentPinsHEADBeforeCapturingWorkspaceDiff(t *testing.T) {
	repo := initGitRepository(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "go.mod"),
		[]byte("module example.com/review\n\ngo 1.21\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "selected.go"),
		[]byte("package review\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "outside.txt"),
		[]byte("baseline\n"),
		0o600,
	))
	gitAdd(t, repo, "go.mod", "selected.go", "outside.txt")
	gitCommit(t, repo)
	capturedCommit, err := resolveGitCommit(context.Background(), repo, "HEAD")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "selected.go"),
		[]byte("package review\n\nconst Selected = true\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "outside.txt"),
		[]byte("advanced\n"),
		0o600,
	))

	baseStorage := newTestStorage(t)
	storage := &advanceHEADAfterDiffStorage{
		Storage: baseStorage,
		advance: func() error {
			gitAdd(t, repo, "outside.txt")
			gitCommit(t, repo)
			return nil
		},
	}
	sandbox := &recordingScopedSandbox{}
	agent := NewReviewAgentWithSandbox(storage, sandbox)
	_, err = agent.Review(context.Background(), ReviewInput{
		RepoPath:  repo,
		FilePaths: []string{"selected.go"},
	})
	require.NoError(t, err)
	require.Len(t, sandbox.reviewScopes, 2)
	advancedCommit, err := resolveGitCommit(context.Background(), repo, "HEAD")
	require.NoError(t, err)
	require.NotEqual(t, capturedCommit, advancedCommit)
	for _, scope := range sandbox.reviewScopes {
		require.Equal(t, capturedCommit, scope.HeadCommit)
		require.NotEmpty(t, scope.DiffSHA256)
	}
}

func TestDetermineSandboxCommandsChecksEveryChangedModule(t *testing.T) {
	repo := initGitRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/root\n\ngo 1.21\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "root.go"), []byte("package root\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "nested"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "nested", "go.mod"), []byte("module example.com/nested\n\ngo 1.21\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "nested", "nested.go"), []byte("package nested\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "nested", "nested_test.go"), []byte("package nested\nimport \"testing\"\nfunc TestFailure(t *testing.T) { t.Fatal(\"nested module checked\") }\n"), 0o600))
	gitAdd(t, repo, "go.mod", "root.go", "nested/go.mod", "nested/nested.go", "nested/nested_test.go")
	gitCommit(t, repo)

	agent := NewReviewAgent(newTestStorage(t))
	commands, _, err := agent.determineSandboxCommands(
		context.Background(),
		[]DiffFile{{Path: "root.go"}, {Path: "nested/go.mod"}},
		repo,
	)
	require.NoError(t, err)
	require.Equal(t, []string{
		"go vet ./...", "go test ./... -count=1 -timeout=30s",
		"go -C ./nested vet ./...", "go -C ./nested test ./... -count=1 -timeout=30s",
	}, commands)

	sandbox := NewSandbox(SandboxConfig{WorkDir: repo, Timeout: 30 * time.Second})
	run := sandbox.Execute(context.Background(), "nested-module", commands[3], DecisionAllow, "test")
	require.Equal(t, SandboxStatusFailed, run.Status, "nested failing test was not executed: %+v", run)
}

func TestDetermineSandboxCommandsIgnoresOutOfScopeModuleChanges(t *testing.T) {
	t.Run("deleted nested module", func(t *testing.T) {
		repo := initGitRepository(t)
		require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/root\n\ngo 1.21\n"), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(repo, "nested"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(repo, "nested", "go.mod"), []byte("module example.com/nested\n\ngo 1.21\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(repo, "nested", "nested.go"), []byte("package nested\n"), 0o600))
		gitAdd(t, repo, "go.mod", "nested/go.mod", "nested/nested.go")
		gitCommit(t, repo)
		require.NoError(t, os.Remove(filepath.Join(repo, "nested", "go.mod")))

		agent := NewReviewAgent(newTestStorage(t))
		commands, _, err := agent.determineSandboxCommands(
			context.Background(),
			[]DiffFile{{Path: "nested/nested.go"}},
			repo,
		)
		require.NoError(t, err)
		require.Equal(t, []string{
			"go -C ./nested vet ./...",
			"go -C ./nested test ./... -count=1 -timeout=30s",
		}, commands)
	})

	t.Run("untracked nested module", func(t *testing.T) {
		repo := initGitRepository(t)
		require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/root\n\ngo 1.21\n"), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(repo, "nested"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(repo, "nested", "nested.go"), []byte("package root\n"), 0o600))
		gitAdd(t, repo, "go.mod", "nested/nested.go")
		gitCommit(t, repo)
		require.NoError(t, os.WriteFile(filepath.Join(repo, "nested", "go.mod"), []byte("module example.com/unselected\n\ngo 1.21\n"), 0o600))

		agent := NewReviewAgent(newTestStorage(t))
		commands, _, err := agent.determineSandboxCommands(
			context.Background(),
			[]DiffFile{{Path: "nested/nested.go"}},
			repo,
		)
		require.NoError(t, err)
		require.Equal(t, []string{
			"go vet ./...",
			"go test ./... -count=1 -timeout=30s",
		}, commands)
	})
}

func TestDetermineSandboxCommandsChecksBothSidesOfCrossModuleRenames(t *testing.T) {
	t.Run("Go source rename", func(t *testing.T) {
		repo := initGitRepository(t)
		for _, module := range []string{"old", "new"} {
			require.NoError(t, os.MkdirAll(filepath.Join(repo, module), 0o700))
			require.NoError(t, os.WriteFile(
				filepath.Join(repo, module, "go.mod"),
				[]byte("module example.com/"+module+"\n\ngo 1.21\n"),
				0o600,
			))
		}
		gitAdd(t, repo, "old/go.mod", "new/go.mod")
		gitCommit(t, repo)

		agent := NewReviewAgent(newTestStorage(t))
		commands, _, err := agent.determineSandboxCommands(
			context.Background(),
			[]DiffFile{{
				Path: "new/moved.go", OldPath: "old/moved.go", IsRename: true,
			}},
			repo,
		)
		require.NoError(t, err)
		require.Equal(t, []string{
			"go -C ./new vet ./...",
			"go -C ./new test ./... -count=1 -timeout=30s",
			"go -C ./old vet ./...",
			"go -C ./old test ./... -count=1 -timeout=30s",
		}, commands)
	})

	t.Run("go.mod rename", func(t *testing.T) {
		repo := initGitRepository(t)
		require.NoError(t, os.WriteFile(
			filepath.Join(repo, "go.mod"),
			[]byte("module example.com/root\n\ngo 1.21\n"),
			0o600,
		))
		require.NoError(t, os.MkdirAll(filepath.Join(repo, "old"), 0o700))
		require.NoError(t, os.WriteFile(
			filepath.Join(repo, "old", "go.mod"),
			[]byte("module example.com/old\n\ngo 1.21\n"),
			0o600,
		))
		gitAdd(t, repo, "go.mod", "old/go.mod")
		gitCommit(t, repo)

		agent := NewReviewAgent(newTestStorage(t))
		commands, _, err := agent.determineSandboxCommands(
			context.Background(),
			[]DiffFile{{
				Path: "new/go.mod", OldPath: "old/go.mod", IsRename: true,
			}},
			repo,
		)
		require.NoError(t, err)
		require.Equal(t, []string{
			"go vet ./...",
			"go test ./... -count=1 -timeout=30s",
			"go -C ./new vet ./...",
			"go -C ./new test ./... -count=1 -timeout=30s",
		}, commands)
	})
}
