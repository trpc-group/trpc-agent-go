//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package orchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

func TestRunCompletesPipelineAndDegradesModelFailure(t *testing.T) {
	ctx := context.Background()
	reviewStore, err := store.NewSQLiteStore(ctx, filepath.Join(t.TempDir(), "reviews.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reviewStore.Close()) })
	loaded := testLoaded(t)
	candidate := testCandidate()
	exitCode := 1
	outputDirectory := t.TempDir()
	clock := newTestClock()
	orchestrator, err := New(Config{
		TaskID: "task-1",
		Mode:   review.ModeFakeModel,
		Store:  reviewStore,
		Load:   func(context.Context) (input.Loaded, error) { return loaded, nil },
		Rules:  fakeRules{candidates: []findings.Candidate{candidate}},
		Sandbox: fakeSandbox{result: sandbox.Result{
			Runs: []review.SandboxRun{{
				SchemaVersion: review.SchemaVersion,
				TaskID:        "task-1",
				Command:       "go test ./...",
				Status:        review.SandboxStatusFailed,
				Duration:      time.Second,
				ExitCode:      &exitCode,
			}},
			Candidates: []findings.Candidate{candidate},
		}},
		Assist: func(context.Context, string, input.Loaded) ([]findings.Candidate, error) {
			return nil, errors.New("model token=sk-test-super-secret-value-123456 failed")
		},
		Artifacts: inmemory.NewService(),
		ArtifactSession: artifact.SessionInfo{
			AppName: "code-review", UserID: "local", SessionID: "task-1",
		},
		OutputDirectory: outputDirectory,
		Now:             clock.Now,
	})
	require.NoError(t, err)

	result, err := orchestrator.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, review.TaskStatusCompleted, result.Stored.Report.Task.Status)
	require.Len(t, result.Stored.Report.Findings, 1)
	require.Len(t, result.Stored.Report.SandboxRuns, 1)
	require.Len(t, result.Stored.PublicationArtifacts, 2)
	require.Equal(t, 1, result.Stored.Report.Metrics.ErrorTypeCounts["model_error"])
	require.Equal(t, 1, result.Stored.Report.Metrics.ErrorTypeCounts["sandbox_failed"])
	require.FileExists(t, filepath.Join(outputDirectory, report.JSONName))
	require.FileExists(t, filepath.Join(outputDirectory, report.MarkdownName))
	require.NotContains(t, string(result.Document.JSON), "sk-test-super-secret-value-123456")
}

func TestRunPersistsFailureAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name       string
		loadErr    error
		wantStatus review.TaskStatus
	}{
		{name: "failure", loadErr: errors.New("invalid patch"), wantStatus: review.TaskStatusFailed},
		{name: "cancellation", loadErr: context.Canceled, wantStatus: review.TaskStatusCanceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			reviewStore, err := store.NewSQLiteStore(ctx, filepath.Join(t.TempDir(), "reviews.db"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reviewStore.Close()) })
			clock := newTestClock()
			orchestrator, err := New(Config{
				TaskID: "task-1", Mode: review.ModeRuleOnly, Store: reviewStore,
				Load:  func(context.Context) (input.Loaded, error) { return input.Loaded{}, test.loadErr },
				Rules: fakeRules{}, Sandbox: fakeSandbox{},
				Artifacts: inmemory.NewService(),
				ArtifactSession: artifact.SessionInfo{
					AppName: "code-review", UserID: "local", SessionID: "task-1",
				},
				OutputDirectory: t.TempDir(), Now: clock.Now,
			})
			require.NoError(t, err)
			_, err = orchestrator.Run(ctx)
			require.Error(t, err)
			stored, getErr := reviewStore.GetReview(ctx, "task-1")
			require.NoError(t, getErr)
			require.Equal(t, test.wantStatus, stored.Report.Task.Status)
		})
	}
}

func TestRunFailsWhenArtifactPublicationFails(t *testing.T) {
	ctx := context.Background()
	reviewStore, err := store.NewSQLiteStore(ctx, filepath.Join(t.TempDir(), "reviews.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reviewStore.Close()) })
	baseArtifacts := inmemory.NewService()
	clock := newTestClock()
	orchestrator, err := New(Config{
		TaskID: "task-1", Mode: review.ModeRuleOnly, Store: reviewStore,
		Load:      func(context.Context) (input.Loaded, error) { return testLoaded(t), nil },
		Rules:     fakeRules{candidates: []findings.Candidate{testCandidate()}},
		Sandbox:   fakeSandbox{},
		Artifacts: &failingArtifacts{Service: baseArtifacts},
		ArtifactSession: artifact.SessionInfo{
			AppName: "code-review", UserID: "local", SessionID: "task-1",
		},
		OutputDirectory: t.TempDir(), Now: clock.Now,
	})
	require.NoError(t, err)
	_, err = orchestrator.Run(ctx)
	require.Error(t, err)
	stored, getErr := reviewStore.GetReview(ctx, "task-1")
	require.NoError(t, getErr)
	require.Equal(t, review.TaskStatusFailed, stored.Report.Task.Status)
}

type fakeRules struct {
	candidates []findings.Candidate
	err        error
}

func (f fakeRules) Review(input.Diff, rules.Snapshots) ([]findings.Candidate, error) {
	return append([]findings.Candidate(nil), f.candidates...), f.err
}

type fakeSandbox struct {
	result sandbox.Result
	err    error
}

func (f fakeSandbox) Run(context.Context, sandbox.Request) (sandbox.Result, error) {
	return f.result, f.err
}

type failingArtifacts struct{ artifact.Service }

func (f *failingArtifacts) SaveArtifact(
	context.Context, artifact.SessionInfo, string, *artifact.Artifact,
) (int, error) {
	return 0, errors.New("artifact storage unavailable")
}

type testClock struct{ next time.Time }

func newTestClock() *testClock {
	return &testClock{next: time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	result := c.next
	c.next = c.next.Add(time.Second)
	return result
}

func testLoaded(t *testing.T) input.Loaded {
	t.Helper()
	raw := []byte("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n" +
		"@@ -1 +1,2 @@\n package x\n+var X = 1\n")
	diff, err := input.Parse(strings.NewReader(string(raw)))
	require.NoError(t, err)
	return input.Loaded{
		Source: review.InputSourceDiffFile,
		Digest: "0123456789abcdef",
		Raw:    raw,
		Diff:   diff,
		Snapshots: []input.Snapshot{{
			Layer: review.ChangeLayerUnified, Path: "x.go",
			Content: []byte("package x\nvar X = 1\n"),
		}},
	}
}

func testCandidate() findings.Candidate {
	return findings.Candidate{
		SchemaVersion: review.SchemaVersion,
		Severity:      review.SeverityHigh, Category: "security",
		Layer: review.ChangeLayerUnified, File: "x.go", Line: 2,
		SemanticAnchor: "unsafe-change", Title: "unsafe change",
		Evidence: "changed line is unsafe", Recommendation: "make the change safe",
		Confidence: review.ConfidenceHigh, Source: review.SourceRule,
		RuleID: "security/unsafe-change/v1", Disposition: review.DispositionFinding,
	}
}
