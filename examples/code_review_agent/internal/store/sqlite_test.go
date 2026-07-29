//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestSQLiteStoreReconstructsCompleteReviewExactly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reviews.db")
	store := openTestStore(t, ctx, path)

	task := testTask("task-complete")
	require.NoError(t, store.CreateTask(ctx, task))
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, task.UpdatedAt.Add(time.Second)))

	runs := []review.SandboxRun{
		testSandboxRun(task.ID, "go test ./...", review.SandboxStatusCompleted, 1200*time.Millisecond, intPtr(0)),
		testSandboxRun(task.ID, "go vet ./...", review.SandboxStatusFailed, 800*time.Millisecond, intPtr(1)),
	}
	for _, run := range runs {
		require.NoError(t, store.RecordSandboxRun(ctx, run))
	}

	decisions := []review.GovernanceDecision{
		testDecision(task.ID, review.DecisionKindFilter, "go_test", review.DecisionActionAllow),
		testDecision(task.ID, review.DecisionKindPermission, "go_vet", review.DecisionActionAllow),
	}
	for _, decision := range decisions {
		require.NoError(t, store.RecordGovernanceDecision(ctx, decision))
	}

	completion := testCompletion(task.ID, task.UpdatedAt.Add(2*time.Second))
	require.NoError(t, store.CompleteTask(ctx, completion))

	got, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)

	wantTask := task
	wantTask.CreatedAt = task.CreatedAt.UTC()
	wantTask.Status = review.TaskStatusCompleted
	wantTask.Phase = review.PhaseCompleted
	wantTask.UpdatedAt = completion.UpdatedAt.UTC()
	want := review.StoredReview{
		Report: review.Report{
			SchemaVersion:       review.SchemaVersion,
			Task:                wantTask,
			Input:               completion.Input,
			SandboxRuns:         runs,
			GovernanceDecisions: decisions,
			Findings:            completion.Findings,
			Artifacts:           completion.Artifacts,
			Metrics:             completion.Metrics,
			Conclusion:          completion.Conclusion,
		},
		PublicationArtifacts: completion.PublicationArtifacts,
		Metadata:             completion.Report,
	}
	require.Equal(t, want, got)
	require.NoError(t, got.Report.Validate())
}

func TestSQLiteStoreConfiguresFileDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reviews.db")
	store := openTestStore(t, ctx, path)

	var foreignKeys int
	require.NoError(t, store.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys))
	require.Equal(t, 1, foreignKeys)

	var journalMode string
	require.NoError(t, store.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode))
	require.Equal(t, "wal", journalMode)

	rows, err := store.db.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'index' AND name IN (
			'idx_sandbox_runs_task_id',
			'idx_governance_decisions_task_id',
			'idx_findings_task_id',
			'idx_artifacts_task_id'
		)
		ORDER BY name`)
	require.NoError(t, err)
	defer rows.Close()
	var indexes []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		indexes = append(indexes, name)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{
		"idx_artifacts_task_id",
		"idx_findings_task_id",
		"idx_governance_decisions_task_id",
		"idx_sandbox_runs_task_id",
	}, indexes)
}

func TestSQLiteStoreRejectsSecretBearingTaskID(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reviews.db")
	store := openTestStore(t, ctx, path)
	const secret = "sk-test-secret-task-identifier-1234567890"
	task := testTask(secret)
	err := store.CreateTask(ctx, task)
	require.ErrorContains(t, err, "id")
	require.NotContains(t, err.Error(), secret)
	var count int
	require.NoError(t, store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM review_tasks").Scan(&count))
	require.Zero(t, count)
	assertNoPersistedTextContains(t, path, secret)
}

func TestSQLiteStoreRecordsGovernanceDecisionIdempotently(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))
	task := testTask("task-governance-idempotent")
	require.NoError(t, store.CreateTask(ctx, task))
	decision := testDecision(
		task.ID,
		review.DecisionKindPermission,
		"workspace_exec",
		review.DecisionActionAllow,
	)
	require.NoError(t, store.RecordGovernanceDecision(ctx, decision))
	require.NoError(t, store.RecordGovernanceDecision(ctx, decision))
	var count int
	require.NoError(t, store.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM governance_decisions WHERE task_id = ?",
		task.ID,
	).Scan(&count))
	require.Equal(t, 1, count)
}

func TestSQLiteStoreRejectsDuplicateTasksAndFindings(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))
	task := testTask("task-duplicate")
	require.NoError(t, store.CreateTask(ctx, task))
	require.Error(t, store.CreateTask(ctx, task))
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, task.UpdatedAt.Add(time.Second)))

	completion := testCompletion(task.ID, task.UpdatedAt.Add(2*time.Second))
	completion.Findings = append(completion.Findings, completion.Findings[0])
	completion.Metrics.FindingTotal++
	completion.Metrics.SeverityCounts[review.SeverityHigh]++
	err := store.CompleteTask(ctx, completion)
	require.ErrorContains(t, err, "UNIQUE constraint failed: findings.task_id, findings.fingerprint")

	got, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, review.TaskStatusRunning, got.Report.Task.Status)
	require.Equal(t, review.PhaseFinalize, got.Report.Task.Phase)
	require.Empty(t, got.Report.Findings)
	require.Empty(t, got.Report.Artifacts)
	require.Equal(t, review.ReviewInput{}, got.Report.Input)
	require.Equal(t, review.Metrics{}, got.Report.Metrics)
	require.Empty(t, got.Report.Conclusion)
}

func TestSQLiteStoreRollsBackFailedCompletionWithoutPersistingSecret(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reviews.db")
	store := openTestStore(t, ctx, path)
	task := testTask("task-rollback")
	require.NoError(t, store.CreateTask(ctx, task))
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, task.UpdatedAt.Add(time.Second)))

	const secret = "sk-test-rollback-secret-plaintext"
	completion := testCompletion(task.ID, task.UpdatedAt.Add(2*time.Second))
	completion.Findings[0].Evidence = secret
	duplicate := completion.Findings[0]
	duplicate.Title = "duplicate fingerprint"
	completion.Findings = append(completion.Findings, duplicate)
	completion.Metrics.FindingTotal++
	completion.Metrics.SeverityCounts[review.SeverityHigh]++
	err := store.CompleteTask(ctx, completion)
	require.ErrorContains(t, err, "UNIQUE constraint failed: findings.task_id, findings.fingerprint")
	got, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, review.TaskStatusRunning, got.Report.Task.Status)
	require.Empty(t, got.Report.Findings)

	require.NoError(t, store.Close())
	assertNoPersistedTextContains(t, path, secret)
}

func TestSQLiteStorePersistsOnlyRedactedValuesAndNoSecretSidecars(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reviews.db")
	store := openTestStore(t, ctx, path)
	task := testTask("task-redacted")
	require.NoError(t, store.CreateTask(ctx, task))
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, task.UpdatedAt.Add(time.Second)))

	const secret = "sk-test-success-secret-plaintext"
	completion := testCompletion(task.ID, task.UpdatedAt.Add(2*time.Second))
	completion.Findings[0].Evidence = "credential=" + secret
	require.NoError(t, store.CompleteTask(ctx, completion))

	got, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	require.Contains(t, got.Report.Findings[0].Evidence, "[REDACTED:credential]")
	require.NotContains(t, got.Report.Findings[0].Evidence, secret)
	require.FileExists(t, path+"-wal")
	assertNoPersistedTextContains(t, path, secret)
}

func TestSQLiteStoreRedactsEveryFreeTextPersistenceRoute(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reviews.db")
	store := openTestStore(t, ctx, path)
	const secret = "sk-test-0123456789abcdefghijklmnopqrstuv"

	failedTask := testTask("task-secret-failure")
	require.NoError(t, store.CreateTask(ctx, failedTask))
	require.NoError(t, store.FailTask(ctx, failedTask.ID, review.PhaseCreated,
		"terminal error "+secret, failedTask.UpdatedAt.Add(time.Second)))

	task := testTask("task-secret-success")
	require.NoError(t, store.CreateTask(ctx, task))
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, task.UpdatedAt.Add(time.Second)))
	run := testSandboxRun(task.ID, "go test ./...", review.SandboxStatusCompleted, time.Second, intPtr(0))
	run.Stdout = "stdout " + secret
	run.Stderr = "stderr " + secret
	require.NoError(t, store.RecordSandboxRun(ctx, run))
	decision := testDecision(task.ID, review.DecisionKindPermission, "go_test", review.DecisionActionAllow)
	decision.Reason = "reason " + secret
	decision.Rule = "rule " + secret
	require.NoError(t, store.RecordGovernanceDecision(ctx, decision))

	completion := testCompletion(task.ID, task.UpdatedAt.Add(2*time.Second))
	for index := range completion.Findings {
		finding := &completion.Findings[index]
		finding.Category = fmt.Sprintf("category-%d %s", index, secret)
		finding.Title = fmt.Sprintf("title-%d %s", index, secret)
		finding.Evidence = fmt.Sprintf("evidence-%d %s", index, secret)
		finding.Recommendation = fmt.Sprintf("recommendation-%d %s", index, secret)
	}
	completion.Metrics.ErrorTypeCounts = map[string]int{"error type " + secret: 1}
	completion.Conclusion = "conclusion warning " + secret
	require.NoError(t, store.CompleteTask(ctx, completion))

	failed, err := store.GetReview(ctx, failedTask.ID)
	require.NoError(t, err)
	stored, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	bytes, err := json.Marshal([]any{failed, stored})
	require.NoError(t, err)
	require.NotContains(t, string(bytes), secret)
	require.Contains(t, string(bytes), "[REDACTED:")
	require.FileExists(t, path+"-wal")
	assertNoPersistedTextContains(t, path, secret)
}

func TestSQLiteStoreRequiresRunningFinalizeForCompletion(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))
	task := testTask("task-not-finalizing")
	require.NoError(t, store.CreateTask(ctx, task))

	err := store.CompleteTask(ctx, testCompletion(task.ID, task.UpdatedAt.Add(time.Second)))
	require.ErrorContains(t, err, "running finalize")
	got, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, review.TaskStatusPending, got.Report.Task.Status)
	require.Equal(t, review.PhaseCreated, got.Report.Task.Phase)
	require.Equal(t, review.ReviewInput{}, got.Report.Input)
}

func TestSQLiteStoreBindsReportMetadataToCanonicalArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*review.Completion)
		wantError string
	}{
		{
			name: "references must be distinct",
			mutate: func(completion *review.Completion) {
				completion.Report.MarkdownArtifactReference = completion.Report.JSONArtifactReference
			},
			wantError: "distinct",
		},
		{
			name: "json artifact name",
			mutate: func(completion *review.Completion) {
				completion.PublicationArtifacts[0].Name = "report.json"
			},
			wantError: "review_report.json",
		},
		{
			name: "json artifact mime",
			mutate: func(completion *review.Completion) {
				completion.PublicationArtifacts[0].MIMEType = "text/plain"
			},
			wantError: "application/json",
		},
		{
			name: "markdown artifact name",
			mutate: func(completion *review.Completion) {
				completion.PublicationArtifacts[1].Name = "report.md"
			},
			wantError: "review_report.md",
		},
		{
			name: "markdown artifact mime",
			mutate: func(completion *review.Completion) {
				completion.PublicationArtifacts[1].MIMEType = "text/plain"
			},
			wantError: "text/markdown",
		},
		{
			name: "report digest",
			mutate: func(completion *review.Completion) {
				completion.Report.Digest = "sha256:different"
			},
			wantError: "json artifact digest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))
			task := testTask("task-metadata")
			require.NoError(t, store.CreateTask(ctx, task))
			require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize,
				task.UpdatedAt.Add(time.Second)))
			completion := testCompletion(task.ID, task.UpdatedAt.Add(2*time.Second))
			tt.mutate(&completion)
			require.ErrorContains(t, store.CompleteTask(ctx, completion), tt.wantError)
		})
	}
}

func TestSQLiteStoreEnforcesForeignKeys(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))

	_, err := store.db.ExecContext(ctx, `INSERT INTO sandbox_runs
		(task_id, schema_version, command, status, duration_ns, exit_code,
		 timed_out, stdout, stderr, truncated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "missing-task", review.SchemaVersion,
		"go test ./...", review.SandboxStatusCompleted, int64(time.Second), 0,
		false, "", "", false)
	require.ErrorContains(t, err, "FOREIGN KEY constraint failed")
}

func TestSQLiteStoreTransitionsAndFailsTask(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))
	task := testTask("task-failed")
	require.NoError(t, store.CreateTask(ctx, task))

	transitionedAt := task.UpdatedAt.Add(time.Second)
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseSandbox, transitionedAt))
	require.Error(t, store.TransitionPhase(ctx, task.ID, review.PhaseRules, transitionedAt.Add(time.Second)))

	failedAt := transitionedAt.Add(2 * time.Second)
	require.NoError(t, store.FailTask(ctx, task.ID, review.PhaseSandbox, "sandbox unavailable", failedAt))

	got, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, review.TaskStatusFailed, got.Report.Task.Status)
	require.Equal(t, review.PhaseSandbox, got.Report.Task.Phase)
	require.Equal(t, "sandbox unavailable", got.Report.Task.TerminalError)
	require.Equal(t, failedAt.UTC(), got.Report.Task.UpdatedAt)
	require.Error(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, failedAt.Add(time.Second)))
}

func TestSQLiteStoreCancelsTask(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))
	task := testTask("task-canceled")
	require.NoError(t, store.CreateTask(ctx, task))
	require.NoError(t, store.TransitionPhase(
		ctx, task.ID, review.PhaseInput, task.UpdatedAt.Add(time.Second)))
	require.NoError(t, store.CancelTask(
		ctx, task.ID, review.PhaseInput, "context canceled", task.UpdatedAt.Add(2*time.Second)))
	stored, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, review.TaskStatusCanceled, stored.Report.Task.Status)
	require.Equal(t, review.PhaseInput, stored.Report.Task.Phase)
	require.Equal(t, "context canceled", stored.Report.Task.TerminalError)
}

func TestSQLiteStoreRejectsInvalidLifecycleWrites(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))

	invalid := testTask("task-invalid-create")
	invalid.Status = review.TaskStatusRunning
	invalid.Phase = review.PhaseInput
	require.Error(t, store.CreateTask(ctx, invalid))

	task := testTask("task-lifecycle")
	require.NoError(t, store.CreateTask(ctx, task))
	firstUpdate := task.UpdatedAt.Add(2 * time.Second)
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseRules, firstUpdate))
	require.Error(t, store.TransitionPhase(ctx, task.ID, review.PhaseSandbox, firstUpdate.Add(-time.Second)))
	require.ErrorContains(t, store.CompleteTask(ctx, testCompletion(task.ID, firstUpdate.Add(time.Second))),
		"running finalize")
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, firstUpdate.Add(time.Second)))
	require.NoError(t, store.CompleteTask(ctx, testCompletion(task.ID, firstUpdate.Add(2*time.Second))))

	require.Error(t, store.RecordSandboxRun(ctx,
		testSandboxRun(task.ID, "go test ./...", review.SandboxStatusCompleted, time.Second, intPtr(0))))
	require.Error(t, store.RecordGovernanceDecision(ctx,
		testDecision(task.ID, review.DecisionKindPermission, "go_test", review.DecisionActionAllow)))
	got, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	require.Empty(t, got.Report.SandboxRuns)
	require.Empty(t, got.Report.GovernanceDecisions)
}

func TestSQLiteStoreSupportsConcurrentReads(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))
	task := testTask("task-concurrent")
	require.NoError(t, store.CreateTask(ctx, task))
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, task.UpdatedAt.Add(time.Second)))
	require.NoError(t, store.CompleteTask(ctx, testCompletion(task.ID, task.UpdatedAt.Add(2*time.Second))))

	const readers = 24
	start := make(chan struct{})
	errs := make(chan error, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := store.GetReview(ctx, task.ID)
			if err == nil && got.Report.Task.ID != task.ID {
				err = fmt.Errorf("got task %q", got.Report.Task.ID)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestSQLiteStoreGetReviewUsesConsistentSnapshot(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "reviews.db"))
	task := testTask("task-snapshot")
	require.NoError(t, store.CreateTask(ctx, task))
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, task.UpdatedAt.Add(time.Second)))

	taskRead := make(chan struct{})
	continueRead := make(chan struct{})
	store.afterReadTaskForTest = func() {
		close(taskRead)
		<-continueRead
	}
	type result struct {
		review review.StoredReview
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		got, err := store.GetReview(ctx, task.ID)
		resultCh <- result{review: got, err: err}
	}()

	<-taskRead
	require.NoError(t, store.CompleteTask(ctx, testCompletion(task.ID, task.UpdatedAt.Add(2*time.Second))))
	close(continueRead)
	first := <-resultCh
	require.NoError(t, first.err)
	require.Equal(t, review.TaskStatusRunning, first.review.Report.Task.Status)
	require.Equal(t, review.PhaseFinalize, first.review.Report.Task.Phase)
	require.Equal(t, review.ReviewInput{}, first.review.Report.Input)
	require.Empty(t, first.review.Report.Findings)

	store.afterReadTaskForTest = nil
	latest, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, review.TaskStatusCompleted, latest.Report.Task.Status)
	require.NotEmpty(t, latest.Report.Findings)
}

func TestSQLiteStoreCancelsCompletionBlockedOnDatabaseLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reviews.db")
	store := openTestStore(t, ctx, path)
	task := testTask("task-locked")
	require.NoError(t, store.CreateTask(ctx, task))
	require.NoError(t, store.TransitionPhase(ctx, task.ID, review.PhaseFinalize, task.UpdatedAt.Add(time.Second)))

	lockDB, err := sql.Open("sqlite3", sqliteTestDSN(path))
	require.NoError(t, err)
	defer lockDB.Close()
	conn, err := lockDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, func() error {
		_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		return err
	}())
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	}()

	blockedCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = store.CompleteTask(blockedCtx, testCompletion(task.ID, task.UpdatedAt.Add(2*time.Second)))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 2*time.Second)

	got, err := store.GetReview(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, review.TaskStatusRunning, got.Report.Task.Status)
	require.Equal(t, review.PhaseFinalize, got.Report.Task.Phase)
	require.Equal(t, review.ReviewInput{}, got.Report.Input)
	require.Empty(t, got.Report.Findings)
	require.Empty(t, got.Report.Artifacts)
}

func TestSQLiteStoreHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := openTestStore(t, context.Background(), filepath.Join(t.TempDir(), "reviews.db"))

	require.ErrorIs(t, store.CreateTask(ctx, testTask("task-canceled-context")), context.Canceled)
}

func openTestStore(t *testing.T, ctx context.Context, path string) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func testTask(id string) review.Task {
	createdAt := time.Date(2026, time.July, 29, 8, 30, 0, 123456789, time.FixedZone("UTC+8", 8*60*60))
	return review.Task{
		SchemaVersion: review.SchemaVersion,
		ID:            id,
		Status:        review.TaskStatusPending,
		Phase:         review.PhaseCreated,
		Mode:          review.ModeRuleOnly,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}
}

func testSandboxRun(taskID, command string, status review.SandboxStatus, duration time.Duration, exitCode *int) review.SandboxRun {
	return review.SandboxRun{
		SchemaVersion: review.SchemaVersion,
		TaskID:        taskID,
		Command:       command,
		Status:        status,
		Duration:      duration,
		ExitCode:      exitCode,
		Stdout:        "bounded stdout",
		Stderr:        "bounded stderr",
		Truncated:     true,
	}
}

func testDecision(taskID string, kind review.DecisionKind, tool string, action review.DecisionAction) review.GovernanceDecision {
	digest := sha256.Sum256([]byte(tool))
	return review.GovernanceDecision{
		SchemaVersion: review.SchemaVersion,
		TaskID:        taskID,
		DecisionID:    fmt.Sprintf("%s:%x:%s", kind, digest[:8], action),
		Kind:          kind,
		Tool:          tool,
		Action:        action,
		Reason:        "fixed operation is allowed",
		Rule:          "allow-fixed-operation",
	}
}

func testCompletion(taskID string, updatedAt time.Time) review.Completion {
	findings := []review.Finding{
		{
			SchemaVersion:  review.SchemaVersion,
			TaskID:         taskID,
			Severity:       review.SeverityHigh,
			Category:       "security",
			Layer:          review.ChangeLayerUnified,
			File:           "internal/server.go",
			Line:           42,
			EndLine:        44,
			SemanticAnchor: "authorization-before-use",
			Title:          "authorization occurs too late",
			Evidence:       "request data is used before authorization",
			Recommendation: "authorize before using request data",
			Confidence:     review.ConfidenceHigh,
			Source:         review.SourceRule,
			RuleID:         "security/authorization/v1",
			Disposition:    review.DispositionFinding,
		},
		{
			SchemaVersion:  review.SchemaVersion,
			TaskID:         taskID,
			Severity:       review.SeverityLow,
			Category:       "testing",
			Layer:          review.ChangeLayerUnified,
			File:           "internal/server_test.go",
			Line:           10,
			SemanticAnchor: "missing-empty-request-test",
			Title:          "boundary case is not covered",
			Evidence:       "no test covers an empty request",
			Recommendation: "add an empty request test",
			Confidence:     review.ConfidenceMedium,
			Source:         review.SourceModel,
			RuleID:         "testing/missing-boundary/v1",
			Disposition:    review.DispositionWarning,
		},
	}
	for index := range findings {
		findings[index].Fingerprint = findings[index].ExpectedFingerprint()
	}
	return review.Completion{
		TaskID:    taskID,
		UpdatedAt: updatedAt,
		Input: review.ReviewInput{
			SchemaVersion: review.SchemaVersion,
			TaskID:        taskID,
			Source:        review.InputSourceDiffFile,
			Digest:        "sha256:input",
			ChangedFiles:  []string{"internal/server.go", "internal/server_test.go"},
		},
		Findings: findings,
		Artifacts: []review.ArtifactRecord{
			{
				SchemaVersion: review.SchemaVersion,
				TaskID:        taskID,
				Name:          "go-test.log",
				Reference:     "artifact://go-test.log/1",
				Digest:        "sha256:evidence",
				MIMEType:      "text/plain",
				Size:          1024,
			},
		},
		PublicationArtifacts: []review.ArtifactRecord{
			{
				SchemaVersion: review.SchemaVersion,
				TaskID:        taskID,
				Name:          "review_report.json",
				Reference:     "artifact://review_report.json/3",
				Digest:        "sha256:canonical-report",
				MIMEType:      "application/json",
				Size:          4096,
			},
			{
				SchemaVersion: review.SchemaVersion,
				TaskID:        taskID,
				Name:          "review_report.md",
				Reference:     "artifact://review_report.md/2",
				Digest:        "sha256:markdown",
				MIMEType:      "text/markdown",
				Size:          2048,
			},
		},
		Metrics: review.Metrics{
			SchemaVersion:    review.SchemaVersion,
			TotalDuration:    3 * time.Second,
			SandboxDuration:  2 * time.Second,
			ToolInvocations:  2,
			PermissionBlocks: 0,
			FindingTotal:     2,
			SeverityCounts: map[review.Severity]int{
				review.SeverityHigh: 1,
				review.SeverityLow:  1,
			},
			WarningCount:     1,
			HumanReviewCount: 0,
			ErrorTypeCounts: map[string]int{
				"sandbox_exit": 1,
			},
		},
		Report: review.ReportMetadata{
			SchemaVersion:             review.SchemaVersion,
			TaskID:                    taskID,
			Digest:                    "sha256:canonical-report",
			JSONArtifactReference:     "artifact://review_report.json/3",
			MarkdownArtifactReference: "artifact://review_report.md/2",
		},
		Conclusion: "review completed with one actionable finding",
	}
}

func assertNoPersistedTextContains(t *testing.T, path, secret string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	require.NoError(t, err)
	defer db.Close()

	tables, err := db.Query(`
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	require.NoError(t, err)
	defer tables.Close()
	var names []string
	for tables.Next() {
		var name string
		require.NoError(t, tables.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, tables.Err())

	for _, table := range names {
		rows, err := db.Query(fmt.Sprintf("SELECT * FROM %q", table))
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for i := range values {
				destinations[i] = &values[i]
			}
			require.NoError(t, rows.Scan(destinations...))
			for _, value := range values {
				switch value := value.(type) {
				case string:
					require.NotContains(t, value, secret)
				case []byte:
					require.NotContains(t, string(value), secret)
				}
			}
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}

	for _, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		bytes, err := os.ReadFile(candidate)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		require.NotContains(t, string(bytes), secret, candidate)
	}
}

func sqliteTestDSN(path string) string {
	return "file:" + filepath.ToSlash(path) + "?_busy_timeout=5000&_foreign_keys=on"
}

func intPtr(value int) *int {
	return &value
}
