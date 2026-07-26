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
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestWorkspaceTimeoutBudgetAndObservedTimeoutEvidence covers the example's
// non-overridable timeout budget and the AfterTool path that turns a framework
// deadline into durable sandbox evidence without a GNU timeout wrapper.
func TestWorkspaceTimeoutBudgetAndObservedTimeoutEvidence(t *testing.T) {
	t.Run("over-budget timeout is denied without sandbox", func(t *testing.T) {
		ctx, governance := openGovernedTask(t, "timeout-budget-deny")
		decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
			ToolName:   workspaceExecToolName,
			ToolCallID: "budget-call",
			Arguments:  []byte(`{"command":"sleep 1","timeout_sec":301}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if decision.Action != tool.PermissionActionDeny || decision.Reason != denyTimeoutBudget {
			t.Fatalf("decision = %#v", decision)
		}
		snapshot, err := governance.recorder.Snapshot(ctx, "timeout-budget-deny")
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.SandboxRuns) != 0 {
			t.Fatalf("sandbox runs = %#v", snapshot.SandboxRuns)
		}
	})

	t.Run("local runtime timeout becomes sandbox evidence", func(t *testing.T) {
		ctx := context.Background()
		resources, err := persistence.Open(
			ctx,
			filepath.Join(t.TempDir(), "review.db"),
			redact.AppendEventHook(redact.New()),
		)
		if err != nil {
			t.Fatal(err)
		}
		defer resources.Close()
		const taskID = "timeout-observed"
		if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
			TaskID: taskID, AppName: codeReviewAgentName,
			UserID: "reviewer", InputKind: "fixture",
		}); err != nil {
			t.Fatal(err)
		}
		ctx = reviewInvocationContext(ctx, taskID)
		governance := newGovernedExecution(
			newReviewRecorder(resources.ReviewStore, redact.New()),
			redact.New(),
			newApprover(ApprovalConfig{}, true),
			"local",
		)

		arguments := []byte(`{"command":"sleep 5","timeout_sec":1}`)
		decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
			ToolName: workspaceExecToolName, ToolCallID: "timeout-call", Arguments: arguments,
		})
		if err != nil {
			t.Fatal(err)
		}
		if decision.Action != tool.PermissionActionAllow {
			t.Fatalf("decision = %#v, want allow for non-risk timeout probe", decision)
		}

		// Exercise the real local CodeExecutor timeout path. This mirrors
		// framework timeout delivery into AfterTool audit evidence.
		executor, err := getCodeexecutor(t.TempDir(), "local")
		if err != nil {
			t.Fatal(err)
		}
		defer closeCodeExecutor(executor)
		provider, ok := executor.(codeexecutor.EngineProvider)
		if !ok {
			t.Fatalf("local executor %T does not expose an engine", executor)
		}
		engine := provider.Engine()
		ws, err := engine.Manager().CreateWorkspace(ctx, "timeout-probe", codeexecutor.WorkspacePolicy{})
		if err != nil {
			t.Fatal(err)
		}
		defer engine.Manager().Cleanup(context.Background(), ws)

		start := time.Now()
		result, runErr := engine.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "sleep",
			Args:    []string{"5"},
			Timeout: time.Second,
		})
		elapsed := time.Since(start)
		if elapsed > 8*time.Second {
			t.Fatalf("timeout probe took %s, expected framework budget to return quickly", elapsed)
		}
		if runErr != nil {
			t.Logf("RunProgram error = %v", runErr)
		}
		if !result.TimedOut && result.ExitCode == 0 && runErr == nil {
			t.Fatalf("expected timeout, result=%#v err=%v", result, runErr)
		}

		exitCode := result.ExitCode
		var toolErr error
		if result.TimedOut {
			toolErr = context.DeadlineExceeded
		} else if runErr != nil {
			toolErr = runErr
		}
		after, err := governance.Callbacks().RunAfterTool(ctx, &tool.AfterToolArgs{
			ToolName:   workspaceExecToolName,
			ToolCallID: "timeout-call",
			Arguments:  arguments,
			Result: map[string]any{
				"status":    "exited",
				"output":    result.Stdout + result.Stderr,
				"exit_code": exitCode,
			},
			Error: toolErr,
		})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(after.CustomResult)
		if strings.Contains(string(arguments), "/usr/bin/timeout") ||
			strings.Contains(string(encoded), "/usr/bin/timeout") {
			t.Fatal("GNU timeout wrapper appeared in timeout probe")
		}

		snapshot, err := resources.ReviewStore.LoadTaskSnapshot(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.SandboxRuns) != 1 {
			t.Fatalf("sandbox runs = %#v", snapshot.SandboxRuns)
		}
		run := snapshot.SandboxRuns[0]
		if run.CommandPreview != "sleep 5" {
			t.Fatalf("command preview = %q", run.CommandPreview)
		}
		if strings.Contains(run.CommandPreview, "/usr/bin/timeout") {
			t.Fatal("sandbox audit stored a GNU timeout wrapper")
		}
		if run.Status == "succeeded" {
			t.Fatalf("sandbox status = %q, want timed_out or failed", run.Status)
		}
		if !run.TimedOut && run.Status != "timed_out" && run.ErrorType != "timeout" {
			t.Fatalf("sandbox run = %#v, want timeout evidence", run)
		}
	})
}
