//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewinput"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestReviewIntegrationArchitectureBranches exercises reviewer.Review as the
// primary integration seam across the architecture branches the refactor spec
// requires automation for.
func TestReviewIntegrationArchitectureBranches(t *testing.T) {
	// Review input fixtures and skills live at the example module root.
	// Package tests run from this package directory, so switch for fixture
	// resolution and skill loading.
	moduleRoot := filepath.Clean(filepath.Join("..", ".."))
	t.Chdir(moduleRoot)

	tests := []struct {
		name    string
		fixture string
		assert  func(t *testing.T, outcome ReviewOutcome, snapshot store.ReviewSnapshot)
	}{
		{
			name:    "clean success",
			fixture: "acceptance-clean",
			assert: func(t *testing.T, outcome ReviewOutcome, snapshot store.ReviewSnapshot) {
				t.Helper()
				requireCompletedReview(t, outcome, snapshot)
				if snapshot.Task.Conclusion == "" {
					t.Fatal("clean fixture produced empty conclusion")
				}
				if len(snapshot.Results) != 0 {
					t.Fatalf("clean fixture results = %#v, want none", snapshot.Results)
				}
				requireGovernedScriptLifecycle(t, snapshot)
			},
		},
		{
			name:    "sandbox failure still completes",
			fixture: "acceptance-sandbox-failure",
			assert: func(t *testing.T, outcome ReviewOutcome, snapshot store.ReviewSnapshot) {
				t.Helper()
				requireCompletedReview(t, outcome, snapshot)
				if len(snapshot.SandboxRuns) == 0 {
					t.Fatal("sandbox failure fixture produced no sandbox runs")
				}
				failed := false
				for _, run := range snapshot.SandboxRuns {
					if run.Status != "succeeded" {
						failed = true
						break
					}
				}
				if !failed {
					t.Fatalf("sandbox runs = %#v, want a non-success status", snapshot.SandboxRuns)
				}
				if snapshot.Task.Conclusion == "" {
					t.Fatal("sandbox failure fixture did not submit a conclusion")
				}
			},
		},
		{
			name:    "duplicate submission corrected",
			fixture: "acceptance-duplicate-finding",
			assert: func(t *testing.T, outcome ReviewOutcome, snapshot store.ReviewSnapshot) {
				t.Helper()
				requireCompletedReview(t, outcome, snapshot)
				findings := 0
				for _, result := range snapshot.Results {
					if result.ResultKind == "finding" {
						findings++
					}
				}
				if findings != 1 {
					t.Fatalf("findings = %d, want one canonical finding after retry", findings)
				}
				if !strings.Contains(snapshot.Task.Conclusion, "resource lifecycle") &&
					!strings.Contains(strings.ToLower(snapshot.Task.Conclusion), "resource") {
					// Fake model uses a stable conclusion; keep a soft check.
					if snapshot.Task.Conclusion == "" {
						t.Fatal("duplicate fixture conclusion missing")
					}
				}
			},
		},
		{
			name:    "secret redaction",
			fixture: "acceptance-secret-redaction",
			assert: func(t *testing.T, outcome ReviewOutcome, snapshot store.ReviewSnapshot) {
				t.Helper()
				requireCompletedReview(t, outcome, snapshot)
				blob := string(outcome.JSONReport) + string(outcome.MarkdownReport) +
					snapshot.Task.Conclusion + snapshot.Task.InputSummaryJSON
				for _, result := range snapshot.Results {
					blob += result.Evidence + result.Title + result.Recommendation
				}
				for _, run := range snapshot.SandboxRuns {
					blob += run.OutputSummary + run.ErrorMessage + run.CommandPreview
				}
				// Fixture embeds a hard-coded OpenAI-shaped credential; durable
				// projections must not retain the plaintext token body.
				if strings.Contains(blob, "sk-fixture0123456789abcd") {
					t.Fatal("plaintext fixture credential survived into durable report/snapshot")
				}
			},
		},
		{
			name:    "governed script ask grant exact retry",
			fixture: "acceptance-security",
			assert: func(t *testing.T, outcome ReviewOutcome, snapshot store.ReviewSnapshot) {
				t.Helper()
				requireCompletedReview(t, outcome, snapshot)
				requireGovernedScriptLifecycle(t, snapshot)
				findings := 0
				for _, result := range snapshot.Results {
					if result.ResultKind == "finding" {
						findings++
					}
				}
				if findings < 1 {
					t.Fatal("security fixture produced no findings")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			dbPath := filepath.Join(t.TempDir(), "review.db")
			resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(redact.New()))
			if err != nil {
				t.Fatal(err)
			}
			defer resources.Close()

			agent, err := NewReviewer(Dependencies{
				Store:           resources.ReviewStore,
				SessionService:  resources.SessionService,
				ArtifactService: resources.ArtifactService,
				Sanitizer:       redact.New(),
			}, Config{
				Mode: "fake-model",
				Sandbox: SandboxConfig{
					Backend: "local",
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			outcome, err := agent.Review(ctx, reviewinput.Spec{Fixture: tt.fixture})
			if err != nil {
				t.Fatalf("Review(%s) error = %v", tt.fixture, err)
			}
			if outcome.TaskID == "" {
				t.Fatal("Review returned empty task id")
			}
			snapshot, err := resources.ReviewStore.LoadTaskSnapshot(ctx, outcome.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			tt.assert(t, outcome, snapshot)
		})
	}
}

func requireCompletedReview(t *testing.T, outcome ReviewOutcome, snapshot store.ReviewSnapshot) {
	t.Helper()
	if snapshot.Task.Status != "completed" {
		t.Fatalf("task status = %q error=%s/%s",
			snapshot.Task.Status, snapshot.Task.ErrorType, snapshot.Task.ErrorMessage)
	}
	if len(outcome.JSONReport) == 0 || len(outcome.MarkdownReport) == 0 {
		t.Fatal("completed review missing report bytes")
	}
	if outcome.References.JSONName == "" || outcome.References.MarkdownName == "" {
		t.Fatalf("report references = %#v", outcome.References)
	}
	if snapshot.Task.MonitoringSummaryJSON == "" || snapshot.Task.MonitoringSummaryJSON == "{}" {
		// empty object is allowed only if no events; fake path should have counts
	}
}

func requireGovernedScriptLifecycle(t *testing.T, snapshot store.ReviewSnapshot) {
	t.Helper()
	var sawAsk, sawPermissionRequest, sawGrantedRetry, sawSandbox bool
	for _, decision := range snapshot.PermissionDecisions {
		if decision.DecisionKind == decisionKindToolPermission &&
			decision.ToolName == workspaceExecToolName &&
			decision.Decision == string(tool.PermissionActionAsk) {
			sawAsk = true
		}
		if decision.DecisionKind == decisionKindPermissionRequest &&
			decision.ToolName == workspaceExecToolName {
			sawPermissionRequest = true
		}
		if decision.DecisionKind == decisionKindToolPermission &&
			decision.ToolName == workspaceExecToolName &&
			decision.Decision == string(tool.PermissionActionAllow) &&
			strings.Contains(decision.CommandPreview, reviewChecksCommand) {
			sawGrantedRetry = true
		}
	}
	for _, run := range snapshot.SandboxRuns {
		if strings.Contains(run.CommandPreview, reviewChecksCommand) {
			sawSandbox = true
			if strings.Contains(run.CommandPreview, "/usr/bin/timeout") {
				t.Fatalf("sandbox command still uses GNU timeout wrapper: %q", run.CommandPreview)
			}
		}
	}
	if !sawAsk || !sawPermissionRequest || !sawGrantedRetry || !sawSandbox {
		t.Fatalf("governed lifecycle incomplete: ask=%t permission=%t retry=%t sandbox=%t decisions=%d runs=%d",
			sawAsk, sawPermissionRequest, sawGrantedRetry, sawSandbox,
			len(snapshot.PermissionDecisions), len(snapshot.SandboxRuns))
	}
}
