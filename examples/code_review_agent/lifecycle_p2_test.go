//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewLifecycleUsesCheckpointsAndTwoPhaseFinalize(t *testing.T) {
	store := newLifecycleTestStore()
	clock := steppedClock(time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), 10*time.Millisecond)
	code, stdout, stderr := runForTestWithHooks(t,
		[]string{"--fixture", "clean", "--dry-run"}, nil, nil,
		runtimeHooks{reviewStore: store, taskID: "task-lifecycle", now: clock},
	)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr)
	}
	if len(store.checkpoints) != 5 || len(store.saves) != 2 {
		t.Fatalf("checkpoints = %d, saves = %d", len(store.checkpoints), len(store.saves))
	}
	wantStages := []string{
		reviewStageInput,
		reviewStageAnalysis,
		reviewStageGovernance,
		reviewStageReportWrite,
		reviewStagePersistence,
	}
	for i, want := range wantStages {
		if store.checkpoints[i].Status != reviewStatusRunning || store.checkpoints[i].Stage != want {
			t.Fatalf("checkpoint %d = %+v, want running/%s", i, store.checkpoints[i], want)
		}
	}
	if store.saves[0].Status != reviewStatusRunning || store.saves[0].Stage != reviewStagePersistence {
		t.Fatalf("first save = %+v", store.saves[0])
	}
	completed := store.saves[1]
	if completed.Status != reviewStatusCompleted || completed.Stage != reviewStageCompleted ||
		completed.Failure != nil || !completed.FinishedAt.After(store.saves[0].FinishedAt) ||
		completed.Metrics.ToolCalls != 1 {
		t.Fatalf("completed report = %+v", completed)
	}
	var summary reviewSummary
	mustUnmarshalSummary(t, stdout, &summary)
	if summary.Stage != reviewStageCompleted || summary.Failure != nil {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestReviewLifecyclePersistsStageFailures(t *testing.T) {
	t.Run("input", func(t *testing.T) {
		store := newLifecycleTestStore()
		secret := "supersecret123"
		missing := filepath.Join(t.TempDir(), "audit-token="+secret, "missing.diff")
		code, _, stderr := runForTestWithHooks(t,
			[]string{"--diff-file", missing, "--dry-run"}, nil, nil,
			runtimeHooks{reviewStore: store, taskID: "task-input-failure"},
		)
		assertLifecycleFailure(t, code, stderr, store, "task-input-failure", reviewStageInput)
		report, _ := store.LoadReview(context.Background(), "task-input-failure")
		if strings.Contains(stderr, secret) || strings.Contains(report.Failure.Message, secret) {
			t.Fatalf("failure leaked secret: stderr=%s report=%+v", stderr, report.Failure)
		}
	})

	t.Run("governance", func(t *testing.T) {
		store := newLifecycleTestStore()
		code, _, stderr := runForTestWithHooks(t,
			[]string{"--fixture", "clean", "--dry-run"}, nil, nil,
			runtimeHooks{
				reviewStore: store,
				taskID:      "task-governance-failure",
				skillLoader: func() (codeReviewSkill, error) {
					return codeReviewSkill{}, errors.New("skill unavailable")
				},
			},
		)
		assertLifecycleFailure(t, code, stderr, store, "task-governance-failure", reviewStageGovernance)
	})

	t.Run("report write", func(t *testing.T) {
		store := newLifecycleTestStore()
		outputFile := filepath.Join(t.TempDir(), "output-file")
		if err := os.WriteFile(outputFile, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := runForTestWithHooks(t,
			[]string{"--fixture", "clean", "--dry-run", "--output-dir", outputFile}, nil, nil,
			runtimeHooks{reviewStore: store, taskID: "task-write-failure"},
		)
		assertLifecycleFailure(t, code, stderr, store, "task-write-failure", reviewStageReportWrite)
	})

	t.Run("first persistence", func(t *testing.T) {
		store := newLifecycleTestStore()
		store.failSaveAt = 1
		code, _, stderr := runForTestWithHooks(t,
			[]string{"--fixture", "clean", "--dry-run"}, nil, nil,
			runtimeHooks{reviewStore: store, taskID: "task-save-failure"},
		)
		assertLifecycleFailure(t, code, stderr, store, "task-save-failure", reviewStagePersistence)
	})

	t.Run("final persistence", func(t *testing.T) {
		store := newLifecycleTestStore()
		store.failSaveAt = 2
		code, _, stderr := runForTestWithHooks(t,
			[]string{"--fixture", "clean", "--dry-run"}, nil, nil,
			runtimeHooks{reviewStore: store, taskID: "task-final-save-failure"},
		)
		assertLifecycleFailure(t, code, stderr, store, "task-final-save-failure", reviewStagePersistence)
	})
}

func TestReviewLifecycleReportsStorageOpenAndCheckpointFailures(t *testing.T) {
	t.Run("storage open", func(t *testing.T) {
		code, _, stderr := runRawForTest(t,
			[]string{"--fixture", "clean", "--dry-run", "--db-path", ":memory:"}, nil, nil,
			runtimeHooks{taskID: "task-storage-open"},
		)
		if code != 1 || !strings.Contains(stderr, "task-storage-open") ||
			!strings.Contains(stderr, reviewStageStorageOpen) {
			t.Fatalf("exit code = %d, stderr = %s", code, stderr)
		}
	})

	t.Run("checkpoint", func(t *testing.T) {
		store := newLifecycleTestStore()
		store.failCheckpointAt = 1
		code, _, stderr := runForTestWithHooks(t,
			[]string{"--fixture", "clean", "--dry-run"}, nil, nil,
			runtimeHooks{reviewStore: store, taskID: "task-checkpoint-failure"},
		)
		if code != 1 || !strings.Contains(stderr, "task-checkpoint-failure") ||
			!strings.Contains(stderr, reviewStagePersistence) {
			t.Fatalf("exit code = %d, stderr = %s", code, stderr)
		}
	})
}

func TestShowTaskLoadsRunningAndFailedReports(t *testing.T) {
	store := newMemoryReviewStore()
	started := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	running := newRunningReviewReport("task-running", config{effectiveRuntime: runtimeFake}, started)
	if err := store.CheckpointReview(context.Background(), running); err != nil {
		t.Fatal(err)
	}
	failed := running
	failed.TaskID = "task-failed"
	markReviewFailed(&failed, reviewStageAnalysis, errors.New("analysis failed"), started.Add(time.Second))
	if err := store.CheckpointReview(context.Background(), failed); err != nil {
		t.Fatal(err)
	}
	for _, taskID := range []string{"task-running", "task-failed"} {
		code, stdout, stderr := runForTestWithHooks(t,
			[]string{"--show-task", taskID}, nil, nil,
			runtimeHooks{reviewStore: store},
		)
		if code != 0 {
			t.Fatalf("show %s exit code = %d; stderr: %s", taskID, code, stderr)
		}
		if !strings.Contains(stdout, `"stage"`) {
			t.Fatalf("show %s missing stage: %s", taskID, stdout)
		}
	}
}

func TestFailedReviewIsQueryableFromSQLite(t *testing.T) {
	requireSQLiteDriver(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "reviews.db")
	missing := filepath.Join(tempDir, "missing.diff")
	code, _, stderr := runRawForTest(t,
		[]string{
			"--diff-file", missing,
			"--dry-run",
			"--db-path", dbPath,
			"--output-dir", filepath.Join(tempDir, "output"),
		}, nil, nil, runtimeHooks{taskID: "task-failed-sqlite"},
	)
	if code != 1 || !strings.Contains(stderr, reviewStageInput) {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	code, stdout, stderr := runRawForTest(t,
		[]string{"--show-task", "task-failed-sqlite", "--db-path", dbPath},
		nil, nil, runtimeHooks{},
	)
	if code != 0 {
		t.Fatalf("show failed task exit code = %d; stderr: %s", code, stderr)
	}
	var report reviewReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("unmarshal failed task: %v\n%s", err, stdout)
	}
	if report.Status != reviewStatusFailed || report.Stage != reviewStageInput ||
		report.Failure == nil {
		t.Fatalf("failed sqlite report = %+v", report)
	}
}

func assertLifecycleFailure(
	t *testing.T,
	code int,
	stderr string,
	store *lifecycleTestStore,
	taskID string,
	stage string,
) {
	t.Helper()
	if code != 1 || !strings.Contains(stderr, taskID) || !strings.Contains(stderr, stage) {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	report, err := store.LoadReview(context.Background(), taskID)
	if err != nil {
		t.Fatalf("load failed review: %v", err)
	}
	if report.Status != reviewStatusFailed || report.Stage != stage || report.Failure == nil ||
		report.Failure.Stage != stage || report.Conclusion != reviewConclusionNeedsHumanReview {
		t.Fatalf("failed report = %+v", report)
	}
}

func steppedClock(start time.Time, step time.Duration) func() time.Time {
	current := start.Add(-step)
	return func() time.Time {
		current = current.Add(step)
		return current
	}
}

type lifecycleTestStore struct {
	reports          map[string]reviewReport
	checkpoints      []reviewReport
	saves            []reviewReport
	checkpointCalls  int
	saveCalls        int
	failCheckpointAt int
	failSaveAt       int
}

func newLifecycleTestStore() *lifecycleTestStore {
	return &lifecycleTestStore{reports: map[string]reviewReport{}}
}

func (s *lifecycleTestStore) CheckpointReview(_ context.Context, report reviewReport) error {
	s.checkpointCalls++
	if s.failCheckpointAt == s.checkpointCalls {
		return errors.New("checkpoint failed")
	}
	clone := cloneReviewReport(report)
	s.checkpoints = append(s.checkpoints, clone)
	s.reports[report.TaskID] = clone
	return nil
}

func (s *lifecycleTestStore) SaveReview(_ context.Context, report reviewReport) error {
	s.saveCalls++
	if s.failSaveAt == s.saveCalls {
		return errors.New("save failed")
	}
	clone := cloneReviewReport(report)
	s.saves = append(s.saves, clone)
	s.reports[report.TaskID] = clone
	return nil
}

func (s *lifecycleTestStore) LoadReview(_ context.Context, taskID string) (reviewReport, error) {
	report, ok := s.reports[taskID]
	if !ok {
		return reviewReport{}, errReviewTaskNotFound
	}
	return cloneReviewReport(report), nil
}

func (s *lifecycleTestStore) Close() error {
	return nil
}
