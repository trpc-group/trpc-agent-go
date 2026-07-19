// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package reviewer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewinput"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	_ "modernc.org/sqlite"
)

func TestNewReviewModelRequiresFixtureForFakeModel(t *testing.T) {
	r := &reviewer{config: Config{Mode: "fake-model"}}
	_, err := r.newReviewModel("")
	if err == nil || !strings.Contains(err.Error(), "fixture") {
		t.Fatalf("newReviewModel error = %v, want fixture requirement", err)
	}
}

func TestLocalCodeExecutorPreservesInteractiveRunner(t *testing.T) {
	executor, err := getCodeexecutor(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	defer closeCodeExecutor(executor)
	provider, ok := executor.(codeexecutor.EngineProvider)
	if !ok {
		t.Fatalf("local executor %T does not expose an engine", executor)
	}
	if _, ok := provider.Engine().Runner().(codeexecutor.InteractiveProgramRunner); !ok {
		t.Fatalf("local runner %T lost interactive execution support", provider.Engine().Runner())
	}
}

func TestOwnedReviewRunnerClosesRunnerAndCodeExecutorOnce(t *testing.T) {
	frameworkRunner := &closeTrackingRunner{}
	executor := &closeTrackingExecutor{}
	owned := &ownedReviewRunner{
		Runner:   frameworkRunner,
		executor: executor,
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if frameworkRunner.closeCalls != 1 || executor.closeCalls != 1 {
		t.Fatalf(
			"close calls = runner:%d executor:%d, want one each",
			frameworkRunner.closeCalls,
			executor.closeCalls,
		)
	}
}

type closeTrackingRunner struct {
	closeCalls int
}

func (r *closeTrackingRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	events := make(chan *event.Event)
	close(events)
	return events, nil
}

func (r *closeTrackingRunner) Close() error {
	r.closeCalls++
	return nil
}

type closeTrackingExecutor struct {
	closeCalls int
}

func (e *closeTrackingExecutor) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (e *closeTrackingExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{}
}

func (e *closeTrackingExecutor) Close() error {
	e.closeCalls++
	return nil
}

func TestReviewRecordsInputPreparationFailureOnTask(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	sanitizer := redact.New()
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(sanitizer))
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := NewReviewer(Dependencies{
		Store:           resources.ReviewStore,
		SessionService:  resources.SessionService,
		ArtifactService: resources.ArtifactService,
		Sanitizer:       sanitizer,
	}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, reviewErr := reviewer.Review(ctx, reviewinput.Spec{
		DiffFile: filepath.Join(t.TempDir(), "password=reviewer-secret-value"),
	})
	if reviewErr == nil || !strings.Contains(reviewErr.Error(), "read diff file") {
		t.Fatalf("Review error = %v, want missing diff error", reviewErr)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, inputKind, errorMessage string
	if err := db.QueryRow(`
SELECT task_status, input_kind, error_message
FROM review_tasks ORDER BY created_at DESC LIMIT 1`).Scan(&status, &inputKind, &errorMessage); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || inputKind != reviewinput.InputKindDiffFile || !strings.Contains(errorMessage, "read diff file") {
		t.Fatalf("task = status:%s kind:%s error:%s", status, inputKind, errorMessage)
	}
	if strings.Contains(errorMessage, "reviewer-secret-value") {
		t.Fatalf("stored task error contains plaintext: %s", errorMessage)
	}
}

func TestValidateReviewCompletionDoesNotRequireSkillScriptCall(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(redact.New()))
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()

	const taskID = "structured-result-without-script"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := resources.ReviewStore.SubmitReviewResults(
		ctx,
		taskID,
		nil,
		"No actionable findings.",
	); err != nil {
		t.Fatal(err)
	}

	reviewAgent := &reviewer{
		recorder: newReviewRecorder(resources.ReviewStore, redact.New()),
	}
	if err := reviewAgent.validateReviewCompletion(ctx, taskID); err != nil {
		t.Fatalf("structured conclusion was rejected without a Skill script call: %v", err)
	}
}

func TestRedactingToolCallbackReplacesCredentialBearingResult(t *testing.T) {
	callbacks := newRedactingToolCallbacks(redact.New())
	result, err := callbacks.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName: "workspace_exec",
		Result: map[string]any{
			"status": "completed",
			"output": "password=tool-output-secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.CustomResult == nil {
		t.Fatal("credential-bearing result was not replaced")
	}
	if strings.Contains(toString(result.CustomResult), "tool-output-secret") {
		t.Fatalf("custom result contains plaintext: %#v", result.CustomResult)
	}
}

func TestRedactingToolCallbackReplacesCredentialBearingError(t *testing.T) {
	callbacks := newRedactingToolCallbacks(redact.New())
	result, err := callbacks.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName: "workspace_exec",
		Error:    fmt.Errorf("command failed with token=tool-error-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.CustomResult == nil {
		t.Fatal("credential-bearing error was not replaced")
	}
	if strings.Contains(toString(result.CustomResult), "tool-error-secret") {
		t.Fatalf("custom error result contains plaintext: %#v", result.CustomResult)
	}
}

func TestReviewModelCallbacksCaptureToolPermissionExplanation(t *testing.T) {
	tracker := newReviewRunTracker()
	callbacks := newReviewModelCallbacks(tracker)
	const (
		toolCallID  = "checks-call"
		explanation = "Run repository checks to collect evidence for the review."
	)
	if _, err := callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := callbacks.RunAfterModel(context.Background(), &model.AfterModelArgs{
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: explanation,
				ToolCalls: []model.ToolCall{{
					ID: toolCallID,
				}},
			},
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := tracker.permissionExplanation(toolCallID); got != explanation {
		t.Fatalf("permission explanation = %q, want %q", got, explanation)
	}
}

func TestReviewModelCallbacksDoNotReuseExplanationAcrossToolCalls(t *testing.T) {
	tracker := newReviewRunTracker()
	callbacks := newReviewModelCallbacks(tracker)
	const explanation = "Run go test and go vet for both affected modules to establish their baseline results."
	firstCommand := "sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/first"
	secondCommand := "sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/second"

	if _, err := callbacks.RunAfterModel(context.Background(), &model.AfterModelArgs{
		Response: modelToolCallResponse("first-check", firstCommand, explanation),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := callbacks.RunAfterModel(context.Background(), &model.AfterModelArgs{
		Response: modelToolCallResponse("second-check", secondCommand, ""),
	}); err != nil {
		t.Fatal(err)
	}
	if got := tracker.permissionExplanation("second-check"); got != "" {
		t.Fatalf("second baseline explanation = %q, want no explanation from another tool call", got)
	}
}

func modelToolCallResponse(toolCallID, command, content string) *model.Response {
	return &model.Response{Choices: []model.Choice{{
		Message: model.Message{
			Role:    model.RoleAssistant,
			Content: content,
			ToolCalls: []model.ToolCall{{
				ID: toolCallID,
				Function: model.FunctionDefinitionParam{
					Name: workspaceExecToolName,
					Arguments: []byte(fmt.Sprintf(
						`{"command":%q}`,
						command,
					)),
				},
			}},
		},
	}}}
}

func TestReviewPermissionPolicyRecordsAskBeforeDefaultApproval(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(redact.New()))
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	const taskID = "permission-task"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}

	var prompt bytes.Buffer
	tracker := newReviewRunTracker()
	const explanation = "Run go test and go vet to verify whether the affected module passes both baseline checks."
	recordPermissionExplanation(t, tracker, "checks-call", explanation)
	arguments := prepareWorkspaceExecCall(
		t,
		tracker,
		"checks-call",
		[]byte(`{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo","timeout_sec":120}`),
	)
	policy := newReviewPermissionPolicy(
		newReviewRecorder(resources.ReviewStore, redact.New()),
		tracker,
		newApprover(ApprovalConfig{Input: strings.NewReader("\n"), Output: &prompt}, false),
	)
	ctx = reviewInvocationContext(ctx, taskID)
	decision, err := policy.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "checks-call",
		Arguments: arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("decision = %q, want allow", decision.Action)
	}
	if !strings.Contains(prompt.String(), explanation) ||
		!strings.Contains(prompt.String(), "Tool: workspace_exec") ||
		!strings.Contains(prompt.String(), "[Y/n]") ||
		strings.Contains(prompt.String(), "Purpose: run the bundled") {
		t.Fatalf("approval prompt = %q", prompt.String())
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT decision FROM permission_decisions WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var decisions []string
	for rows.Next() {
		var decision string
		if err := rows.Scan(&decision); err != nil {
			t.Fatal(err)
		}
		decisions = append(decisions, decision)
	}
	if got, want := strings.Join(decisions, ","), "ask,allow"; got != want {
		t.Fatalf("permission decisions = %q, want %q", got, want)
	}
	if tracker.toolCalls != 1 || tracker.permissionInterceptions != 1 {
		t.Fatalf("tracker = tools:%d interceptions:%d", tracker.toolCalls, tracker.permissionInterceptions)
	}
}

func TestCommandApproverRejectsN(t *testing.T) {
	var output bytes.Buffer
	decision, err := newApprover(
		ApprovalConfig{Input: strings.NewReader("n\n"), Output: &output}, false,
	).decide(
		context.Background(),
		workspaceExecToolName,
		"sh scripts/run-go-checks.sh work/inputs/repo",
		"Collect repository check evidence.",
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("decision = %q, want deny", decision.Action)
	}
}

func TestCommandApproverStopsWaitingWhenContextIsCanceled(t *testing.T) {
	input, inputWriter := io.Pipe()
	defer input.Close()
	defer inputWriter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := newApprover(
		ApprovalConfig{Input: input, Output: io.Discard},
		false,
	).decide(
		ctx,
		workspaceExecToolName,
		"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"Collect repository check evidence.",
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("approval cancellation error = %v, want deadline exceeded", err)
	}
}

func TestCommandApproverDoesNotReuseLateResponseAfterCancellation(t *testing.T) {
	input, inputWriter := io.Pipe()
	defer input.Close()
	defer inputWriter.Close()
	var output bytes.Buffer
	approver := newApprover(
		ApprovalConfig{Input: input, Output: &output},
		false,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := approver.decide(
		ctx,
		workspaceExecToolName,
		"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"Collect repository check evidence.",
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first approval error = %v, want deadline exceeded", err)
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(inputWriter, "Y\n")
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("late approval response was not consumed by its original read")
	}

	decision, err := approver.decide(
		context.Background(),
		workspaceExecToolName,
		"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"Collect repository check evidence again.",
	)
	if !errors.Is(err, errApprovalInputUnavailable) {
		t.Fatalf("second approval = %#v, error = %v, want unavailable terminal", decision, err)
	}
	if got := strings.Count(output.String(), "Approve? [Y/n]"); got != 1 {
		t.Fatalf("approval prompts = %d, want only the canceled decision prompt", got)
	}
}

func TestReviewPermissionPolicyDoesNotInventMissingExplanation(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(redact.New()))
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	const taskID = "missing-explanation-task"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}

	tracker := newReviewRunTracker()
	arguments := prepareWorkspaceExecCall(
		t,
		tracker,
		"unexplained-call",
		[]byte(`{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo","timeout_sec":120}`),
	)
	policy := newReviewPermissionPolicy(
		newReviewRecorder(resources.ReviewStore, redact.New()),
		tracker,
		newApprover(ApprovalConfig{}, true),
	)
	decision, err := policy.CheckToolPermission(reviewInvocationContext(ctx, taskID), &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "unexplained-call",
		Arguments: arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAsk || !strings.Contains(decision.Reason, "must explain") {
		t.Fatalf("decision = %#v, want ask for missing Agent explanation", decision)
	}
	snapshot, err := resources.ReviewStore.LoadTaskSnapshot(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PermissionDecisions) != 1 || snapshot.PermissionDecisions[0].Decision != "ask" {
		t.Fatalf("permission audit = %#v, want one ask", snapshot.PermissionDecisions)
	}
	if len(tracker.executions) != 0 {
		t.Fatalf("unexplained tool call was marked for execution: %#v", tracker.executions)
	}
}

func TestReviewChecksModuleRecognizesDocumentedInvocation(t *testing.T) {
	for _, command := range []string{
		"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"/bin/sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/nested",
		"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo 2>&1",
	} {
		if _, ok := reviewChecksModule(command); !ok {
			t.Errorf("reviewChecksModule(%q) rejected a documented invocation", command)
		}
	}
	for _, command := range []string{
		"cd work/inputs/repo && sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"sh work/inputs/repo/skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"sh skills/code-review/scripts/run-go-checks.sh ../repo",
	} {
		if _, ok := reviewChecksModule(command); ok {
			t.Errorf("reviewChecksModule(%q) accepted a rewritten invocation", command)
		}
	}
}

func TestCommandAttemptsReviewChecksExecutionIgnoresReadOnlyInspection(t *testing.T) {
	for _, command := range []string{
		"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"/bin/bash skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"cd work/inputs/repo && sh skills/code-review/scripts/run-go-checks.sh .",
		"skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"cat skills/code-review/scripts/run-go-checks.sh | sh -s -- work/inputs/repo",
		"cat skills/code-review/scripts/run-go-checks.sh|sh -s -- work/inputs/repo",
		". skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"source skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"env skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"timeout 120 skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"sh ./skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
	} {
		if !commandAttemptsReviewChecksExecution(command) {
			t.Errorf("commandAttemptsReviewChecksExecution(%q) = false, want true", command)
		}
	}
	for _, command := range []string{
		"ls skills/code-review/scripts/run-go-checks.sh",
		"cat skills/code-review/scripts/run-go-checks.sh",
		"cat ./skills/code-review/scripts/run-go-checks.sh",
		"sed -n '1,120p' skills/code-review/scripts/run-go-checks.sh",
		"rg run-go-checks.sh skills/code-review/SKILL.md",
		"echo sh skills/code-review/scripts/run-go-checks.sh",
		"sh work/inputs/repo/scripts/run-go-checks.sh work/inputs/repo",
	} {
		if commandAttemptsReviewChecksExecution(command) {
			t.Errorf("commandAttemptsReviewChecksExecution(%q) = true, want false", command)
		}
	}
}

func TestReviewPermissionPolicyAllowsReadOnlySkillScriptInspection(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(redact.New()))
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	const taskID = "read-skill-script"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}

	tracker := newReviewRunTracker()
	arguments := prepareWorkspaceExecCall(
		t,
		tracker,
		"read-script",
		[]byte(`{"command":"cat skills/code-review/scripts/run-go-checks.sh"}`),
	)
	policy := newReviewPermissionPolicy(
		newReviewRecorder(resources.ReviewStore, redact.New()),
		tracker,
		newApprover(ApprovalConfig{}, true),
	)
	decision, err := policy.CheckToolPermission(
		reviewInvocationContext(ctx, taskID),
		&tool.PermissionRequest{
			ToolName: workspaceExecToolName, ToolCallID: "read-script", Arguments: arguments,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("read-only Skill script inspection decision = %#v, want allow", decision)
	}
	if tracker.permissionInterceptions != 0 || len(tracker.executions) != 1 {
		t.Fatalf("read-only inspection state = interceptions:%d executions:%d",
			tracker.permissionInterceptions, len(tracker.executions))
	}
}

func TestReviewPermissionPolicyDoesNotForceChecksThroughSkillScript(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(redact.New()))
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	const taskID = "raw-baseline-bypass-task"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}

	tracker := newReviewRunTracker()
	arguments := prepareWorkspaceExecCall(
		t,
		tracker,
		"raw-go-test",
		[]byte(`{"command":"cd work/inputs/repo && go test ./... 2>&1","timeout_sec":120}`),
	)
	policy := newReviewPermissionPolicy(
		newReviewRecorder(resources.ReviewStore, redact.New()),
		tracker,
		newApprover(ApprovalConfig{}, true),
	)
	decision, err := policy.CheckToolPermission(reviewInvocationContext(ctx, taskID), &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "raw-go-test",
		Arguments: arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("decision = %#v, want ordinary workspace execution to remain allowed", decision)
	}
	snapshot, err := resources.ReviewStore.LoadTaskSnapshot(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PermissionDecisions) != 1 ||
		snapshot.PermissionDecisions[0].Decision != "allow" {
		t.Fatalf("permission audit = %#v, want one allow", snapshot.PermissionDecisions)
	}
	if len(snapshot.SandboxRuns) != 0 || len(tracker.executions) != 1 {
		t.Fatalf("ordinary command execution state: runs=%d pending=%d",
			len(snapshot.SandboxRuns), len(tracker.executions))
	}
	if tracker.permissionInterceptions != 0 {
		t.Fatalf("ordinary command was counted as a permission interception: %d",
			tracker.permissionInterceptions)
	}
}

func TestValidateReviewResultsEnforcesFindingConfidenceAndLocalPath(t *testing.T) {
	valid := store.ReviewResultRecord{
		ResultKind: "finding", Severity: "high", Category: "correctness",
		File: "internal/reviewer/reviewer.go", Line: 1, Title: "title",
		Evidence: "evidence", Recommendation: "fix the changed behavior",
		Confidence: 0.80, Source: "agent", RuleID: "GO-COR-001",
	}
	if err := validateReviewResults([]store.ReviewResultRecord{valid}); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	lowConfidence := valid
	lowConfidence.Confidence = 0.79
	if err := validateReviewResults([]store.ReviewResultRecord{lowConfidence}); err == nil {
		t.Fatal("low-confidence finding was accepted")
	}
	missingRecommendation := valid
	missingRecommendation.Recommendation = ""
	if err := validateReviewResults([]store.ReviewResultRecord{missingRecommendation}); err == nil {
		t.Fatal("result without a recommendation was accepted")
	}
	testFinding := valid
	testFinding.Category = "tests"
	testFinding.RuleID = "GO-TEST-001"
	if err := validateReviewResults([]store.ReviewResultRecord{testFinding}); err == nil {
		t.Fatal("test-coverage advisory was accepted as a finding")
	}
	escaping := valid
	escaping.File = "../outside.go"
	if err := validateReviewResults([]store.ReviewResultRecord{escaping}); err == nil {
		t.Fatal("repository-escaping result path was accepted")
	}
}

func TestSubmitReviewResultsSchemaRequiresRuntimeFields(t *testing.T) {
	tools := newReviewToolSet(nil).Tools(context.Background())
	if len(tools) != 1 {
		t.Fatalf("review tools = %d, want 1", len(tools))
	}
	schema := tools[0].Declaration().InputSchema
	if schema == nil {
		t.Fatal("submit_review_results has no input schema")
	}
	got := append([]string(nil), schema.Required...)
	sort.Strings(got)
	want := []string{"conclusion", "findings", "needs_human_review", "warnings"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required submit fields = %v, want %v", got, want)
	}
}

func TestReviewPermissionPolicyRecordsAskThenDenyWithoutExecution(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(redact.New()))
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	const taskID = "denied-permission-task"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	tracker := newReviewRunTracker()
	recordPermissionExplanation(
		t,
		tracker,
		"denied-checks",
		"Run go test and go vet to verify whether the affected module passes both baseline checks.",
	)
	arguments := prepareWorkspaceExecCall(
		t,
		tracker,
		"denied-checks",
		[]byte(`{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo","timeout_sec":120}`),
	)
	policy := newReviewPermissionPolicy(
		newReviewRecorder(resources.ReviewStore, redact.New()),
		tracker,
		newApprover(ApprovalConfig{Input: strings.NewReader("n\n"), Output: &bytes.Buffer{}}, false),
	)
	decision, err := policy.CheckToolPermission(reviewInvocationContext(ctx, taskID), &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "denied-checks",
		Arguments: arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("decision = %q, want deny", decision.Action)
	}
	snapshot, err := resources.ReviewStore.LoadTaskSnapshot(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PermissionDecisions) != 2 ||
		snapshot.PermissionDecisions[0].Decision != "ask" ||
		snapshot.PermissionDecisions[1].Decision != "deny" {
		t.Fatalf("permission audit = %#v", snapshot.PermissionDecisions)
	}
	if len(snapshot.SandboxRuns) != 0 || len(tracker.executions) != 0 {
		t.Fatalf("denied command was marked for execution: runs=%d pending=%d",
			len(snapshot.SandboxRuns), len(tracker.executions))
	}
}

func TestReviewPermissionPolicyRejectsBackgroundBaselineCheck(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(redact.New()))
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	const taskID = "background-baseline-task"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	tracker := newReviewRunTracker()
	recordPermissionExplanation(
		t,
		tracker,
		"background-checks",
		"Run the module checks to establish their terminal results.",
	)
	arguments := prepareWorkspaceExecCall(
		t,
		tracker,
		"background-checks",
		[]byte(`{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo","background":true}`),
	)
	policy := newReviewPermissionPolicy(
		newReviewRecorder(resources.ReviewStore, redact.New()),
		tracker,
		newApprover(ApprovalConfig{}, true),
	)
	decision, err := policy.CheckToolPermission(
		reviewInvocationContext(ctx, taskID),
		&tool.PermissionRequest{
			ToolName:   workspaceExecToolName,
			ToolCallID: "background-checks",
			Arguments:  arguments,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny ||
		!strings.Contains(decision.Reason, "foreground") {
		t.Fatalf("decision = %#v, want foreground-only denial", decision)
	}
	if len(tracker.executions) != 0 {
		t.Fatalf("background check entered execution: %#v", tracker.executions)
	}
}

func TestGovernedWorkspaceExecMasksTruncatesAndAudits(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	sanitizer := redact.New()
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(sanitizer))
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	const taskID = "governed-output-task"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	ctx = reviewInvocationContext(ctx, taskID)
	tracker := newReviewRunTracker()
	recordPermissionExplanation(
		t,
		tracker,
		"output-call",
		"Run go test and go vet to verify their results and capture the evidence within the output limit.",
	)
	recorder := newReviewRecorder(resources.ReviewStore, sanitizer)
	arguments := []byte(`{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo 2>&1","timeout_sec":120}`)
	callbacks := newGovernedToolCallbacks(sanitizer, recorder, tracker, governedToolConfig{
		Backend: "container", OutputLimitBytes: 96, ArtifactMaxBytes: 1024,
	})
	beforeResult, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName: workspaceExecToolName, ToolCallID: "output-call", Arguments: arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if beforeResult != nil && len(beforeResult.ModifiedArguments) > 0 {
		arguments = beforeResult.ModifiedArguments
	}
	policy := newReviewPermissionPolicy(recorder, tracker, newApprover(ApprovalConfig{}, true))
	decision, err := policy.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "output-call", Arguments: arguments,
	})
	if err != nil || decision.Action != tool.PermissionActionAllow {
		t.Fatalf("permission = %#v, err = %v", decision, err)
	}

	secret := "sk-0123456789abcdef"
	result, err := callbacks.RunAfterTool(ctx, &tool.AfterToolArgs{
		ToolName: workspaceExecToolName, ToolCallID: "output-call", Arguments: arguments,
		Result: map[string]any{"status": "exited", "output": secret + strings.Repeat("世", 100), "exit_code": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.CustomResult)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 512 || strings.Contains(string(encoded), secret) || !strings.Contains(string(encoded), "output_truncated") {
		t.Fatalf("bounded result = %s", encoded)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, summary string
	var limit, truncated, redactions int
	if err := db.QueryRow(`SELECT sandbox_status, stdout_summary, output_limit_bytes,
stdout_truncated, redaction_count FROM sandbox_runs WHERE task_id = ?`, taskID).
		Scan(&status, &summary, &limit, &truncated, &redactions); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || len(summary) > 96 || limit != 96 || truncated != 1 || redactions < 1 {
		t.Fatalf("sandbox audit = status:%s bytes:%d limit:%d truncated:%d redactions:%d",
			status, len(summary), limit, truncated, redactions)
	}
	if strings.Contains(summary, secret) {
		t.Fatal("sandbox audit contains plaintext secret")
	}
}

func TestGovernedWorkspaceExecConvertsTimeoutToModelEvidence(t *testing.T) {
	tracker := newReviewRunTracker()
	input := workspaceExecInput{Command: "sleep 10", Timeout: 1}
	if err := tracker.recordToolInput("timeout-call", input, input); err != nil {
		t.Fatal(err)
	}
	if err := tracker.beginExecution("timeout-call"); err != nil {
		t.Fatal(err)
	}
	callbacks := newGovernedToolCallbacks(redact.New(), nil, tracker, governedToolConfig{Backend: "container"})
	result, err := callbacks.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName: workspaceExecToolName, ToolCallID: "timeout-call",
		Arguments: []byte(`{"command":"sleep 10","timeout_sec":1}`),
		Error:     context.DeadlineExceeded,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.CustomResult)
	if !strings.Contains(string(encoded), `"status":"timed_out"`) || !strings.Contains(string(encoded), "deadline exceeded") {
		t.Fatalf("timeout result = %s", encoded)
	}
	if tracker.sandboxDuration < 0 || tracker.exceptions["timeout"] != 1 {
		t.Fatalf("tracker timeout evidence = %#v", tracker)
	}
}

func TestGovernedWorkspaceExecDoesNotTreatMissingStartAsSuccess(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	sanitizer := redact.New()
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(sanitizer))
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	const taskID = "missing-execution-start-task"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}

	tracker := newReviewRunTracker()
	callbacks := newGovernedToolCallbacks(
		sanitizer,
		newReviewRecorder(resources.ReviewStore, sanitizer),
		tracker,
		governedToolConfig{Backend: "container"},
	)
	result, err := callbacks.RunAfterTool(
		reviewInvocationContext(ctx, taskID),
		&tool.AfterToolArgs{
			ToolName:   workspaceExecToolName,
			ToolCallID: "unapproved-call",
			Arguments:  []byte(`{"command":"printf should-not-look-successful"}`),
			Result:     map[string]any{"status": "exited", "output": "output", "exit_code": 0},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.CustomResult)
	if !strings.Contains(string(encoded), `"status":"error"`) ||
		!strings.Contains(string(encoded), "without an approved execution start") {
		t.Fatalf("model result = %s", encoded)
	}
	snapshot, err := resources.ReviewStore.LoadTaskSnapshot(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.SandboxRuns) != 1 ||
		snapshot.SandboxRuns[0].Status != "failed" ||
		snapshot.SandboxRuns[0].ErrorType != "missing_execution_start" {
		t.Fatalf("sandbox audit = %#v", snapshot.SandboxRuns)
	}
}

func TestGovernedWorkspaceExecFiltersEnvironmentOverrides(t *testing.T) {
	tracker := newReviewRunTracker()
	callbacks := newGovernedToolCallbacks(
		redact.New(),
		nil,
		tracker,
		governedToolConfig{Backend: "local"},
	)
	result, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:   workspaceExecToolName,
		ToolCallID: "environment-call",
		Arguments:  []byte(`{"command":"go test ./...","env":{"CGO_ENABLED":"0","PATH":"/tmp/bin","TOKEN":"plaintext"},"stdin":"input","yield-time_ms":25,"future_option":{"enabled":true}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result.ModifiedArguments) == 0 {
		t.Fatal("environment governance did not replace workspace_exec arguments")
	}
	var input workspaceExecInput
	if err := json.Unmarshal(result.ModifiedArguments, &input); err != nil {
		t.Fatal(err)
	}
	if got, want := input.Env, map[string]string{"CGO_ENABLED": "0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("governed environment = %#v, want %#v", got, want)
	}
	if tracker.exceptions["environment_override_filtered"] != 1 {
		t.Fatalf("exception distribution = %#v", tracker.exceptions)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result.ModifiedArguments, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"stdin", "yield-time_ms", "future_option"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("governance removed workspace_exec field %q: %s", field, result.ModifiedArguments)
		}
	}
}

func toString(value any) string {
	return fmt.Sprint(value)
}

func reviewInvocationContext(ctx context.Context, taskID string) context.Context {
	invocation := agent.NewInvocation(agent.WithInvocationRunOptions(agent.NewRunOptions(
		agent.WithRuntimeState(map[string]any{runtimeStateReviewTaskID: taskID}),
	)))
	return agent.NewInvocationContext(ctx, invocation)
}

func recordPermissionExplanation(
	t *testing.T,
	tracker *reviewRunTracker,
	toolCallID string,
	explanation string,
) {
	t.Helper()
	callbacks := newReviewModelCallbacks(tracker)
	if _, err := callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := callbacks.RunAfterModel(context.Background(), &model.AfterModelArgs{
		Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
			Role:    model.RoleAssistant,
			Content: explanation,
			ToolCalls: []model.ToolCall{{
				ID: toolCallID,
			}},
		}}}},
	}); err != nil {
		t.Fatal(err)
	}
}

func prepareWorkspaceExecCall(
	t *testing.T,
	tracker *reviewRunTracker,
	toolCallID string,
	arguments []byte,
) []byte {
	t.Helper()
	callbacks := newGovernedToolCallbacks(
		redact.New(),
		nil,
		tracker,
		governedToolConfig{Backend: "local"},
	)
	result, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:   workspaceExecToolName,
		ToolCallID: toolCallID,
		Arguments:  arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != nil && len(result.ModifiedArguments) > 0 {
		return result.ModifiedArguments
	}
	return arguments
}
