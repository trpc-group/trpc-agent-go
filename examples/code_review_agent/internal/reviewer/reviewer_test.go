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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
	projectDir := t.TempDir()
	executor, err := getCodeexecutor(projectDir, "local")
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
	ctx := context.Background()
	engine := provider.Engine()
	workspace, err := engine.Manager().CreateWorkspace(
		ctx,
		"outside-example",
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Manager().Cleanup(ctx, workspace)
	rel, err := filepath.Rel(projectDir, workspace.Path)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsLocal(rel) {
		t.Fatalf("local workspace %q is inside project directory %q", workspace.Path, projectDir)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "local_workspace")); !os.IsNotExist(err) {
		t.Fatalf("project-local workspace exists: %v", err)
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

func TestGovernedCallbacksRedactGenericToolOutput(t *testing.T) {
	governance := newGovernedExecution(nil, redact.New(), newApprover(ApprovalConfig{}, true), "local")
	callbacks := governance.Callbacks()
	tests := []struct {
		name   string
		args   *tool.AfterToolArgs
		secret string
	}{
		{
			name: "result",
			args: &tool.AfterToolArgs{
				ToolName: "ordinary_tool",
				Result: map[string]any{
					"status": "completed",
					"output": "password=tool-output-secret",
				},
			},
			secret: "tool-output-secret",
		},
		{
			name: "error",
			args: &tool.AfterToolArgs{
				ToolName: "ordinary_tool",
				Error:    fmt.Errorf("command failed with token=tool-error-secret"),
			},
			secret: "tool-error-secret",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := callbacks.RunAfterTool(context.Background(), tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || result.CustomResult == nil {
				t.Fatal("credential-bearing tool output was not replaced")
			}
			if strings.Contains(fmt.Sprint(result.CustomResult), tt.secret) {
				t.Fatalf("custom result contains plaintext: %#v", result.CustomResult)
			}
		})
	}
}

func TestApproverDecisions(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		want        tool.PermissionAction
		wantPrompts int
	}{
		{name: "empty approves", input: "\n", want: tool.PermissionActionAllow, wantPrompts: 1},
		{name: "yes approves", input: "yes\n", want: tool.PermissionActionAllow, wantPrompts: 1},
		{name: "no denies", input: "n\n", want: tool.PermissionActionDeny, wantPrompts: 1},
		{name: "invalid retries", input: "maybe\ny\n", want: tool.PermissionActionAllow, wantPrompts: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			decision, err := newApprover(
				ApprovalConfig{Input: strings.NewReader(tt.input), Output: &output}, false,
			).decide(
				context.Background(),
				workspaceExecToolName,
				"skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
				"Collect repository check evidence.",
			)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tt.want {
				t.Fatalf("decision = %q, want %q", decision.Action, tt.want)
			}
			if got := strings.Count(output.String(), "Approve? [Y/n]"); got != tt.wantPrompts {
				t.Fatalf("approval prompts = %d, want %d", got, tt.wantPrompts)
			}
		})
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
		"skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
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
		"skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
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
		"skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"Collect repository check evidence again.",
	)
	if !errors.Is(err, errApprovalInputUnavailable) {
		t.Fatalf("second approval = %#v, error = %v, want unavailable terminal", decision, err)
	}
	if got := strings.Count(output.String(), "Approve? [Y/n]"); got != 1 {
		t.Fatalf("approval prompts = %d, want only the canceled decision prompt", got)
	}
}

func TestReviewPermissionPolicyAsksForConfiguredScriptReference(t *testing.T) {
	ctx, governance := openGovernedTask(t, "read-skill-script")
	decision, err := governance.PermissionPolicy().CheckToolPermission(
		ctx,
		&tool.PermissionRequest{
			ToolName:   workspaceExecToolName,
			ToolCallID: "read-script",
			Arguments:  []byte(`{"command":"cat skills/code-review/scripts/run-go-checks.sh"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAsk {
		t.Fatalf("configured script reference decision = %#v, want ask", decision)
	}
	if len(governance.started) != 0 {
		t.Fatalf("configured script reference started executions = %#v", governance.started)
	}
}

func TestReviewPermissionPolicyAllowsOrdinaryWorkspaceCommand(t *testing.T) {
	ctx, governance := openGovernedTask(t, "raw-baseline-bypass-task")
	decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:   workspaceExecToolName,
		ToolCallID: "raw-go-test",
		Arguments:  []byte(`{"command":"cd work/inputs/repo && go test ./... 2>&1","timeout_sec":120}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("decision = %#v, want allow", decision)
	}
	snapshot, err := governance.recorder.Snapshot(ctx, "raw-baseline-bypass-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PermissionDecisions) != 1 ||
		snapshot.PermissionDecisions[0].Decision != string(tool.PermissionActionAllow) {
		t.Fatalf("permission audit = %#v, want one allow", snapshot.PermissionDecisions)
	}
	if len(snapshot.SandboxRuns) != 0 || len(governance.started) != 1 {
		t.Fatalf("ordinary command execution state: runs=%d started=%d",
			len(snapshot.SandboxRuns), len(governance.started))
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

func TestReviewToolSchemasRequireRuntimeFields(t *testing.T) {
	governance := newGovernedExecution(nil, redact.New(), newApprover(ApprovalConfig{}, true), "local")
	tools := []tool.Tool{
		governance.PermissionTool(),
		newSubmitReviewResultsTool(nil),
	}
	if len(tools) != 2 {
		t.Fatalf("review tools = %d, want 2", len(tools))
	}
	tests := []struct {
		name     string
		required []string
	}{
		{
			name:     requestToolPermissionName,
			required: []string{"reason", "target_arguments", "target_tool"},
		},
		{
			name:     submitReviewResultsName,
			required: []string{"conclusion", "findings", "needs_human_review", "warnings"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := callableToolNamed(t, tools, tt.name).Declaration().InputSchema
			if schema == nil {
				t.Fatalf("%s has no input schema", tt.name)
			}
			got := append([]string(nil), schema.Required...)
			sort.Strings(got)
			if !reflect.DeepEqual(got, tt.required) {
				t.Fatalf("required fields = %v, want %v", got, tt.required)
			}
			if tt.name == requestToolPermissionName {
				targetArguments := schema.Properties["target_arguments"]
				if targetArguments == nil || targetArguments.Type != "object" ||
					targetArguments.AdditionalProperties != true {
					t.Fatalf("target_arguments schema = %#v, want an object", targetArguments)
				}
				outputSchema := callableToolNamed(t, tools, tt.name).Declaration().OutputSchema
				if outputSchema == nil {
					t.Fatal("request_tool_permission has no output schema")
				}
				outputArguments := outputSchema.Properties["target_arguments"]
				if outputArguments == nil || outputArguments.Type != "object" ||
					outputArguments.AdditionalProperties != true {
					t.Fatalf("output target_arguments schema = %#v", outputArguments)
				}
			}
		})
	}
}

func TestGovernedWorkspaceExecMasksTruncatesAndAudits(t *testing.T) {
	ctx, governance := openGovernedTask(t, "governed-output-task")
	governance.outputLimitBytes = 96
	governance.backend = "container"
	arguments := []byte(`{"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo","timeout_sec":120}`)
	identity, err := approvalIdentity(workspaceExecToolName, arguments)
	if err != nil {
		t.Fatal(err)
	}
	governance.grant(workspaceExecToolName, identity)
	decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "output-call", Arguments: arguments,
	})
	if err != nil || decision.Action != tool.PermissionActionAllow {
		t.Fatalf("permission = %#v, err = %v", decision, err)
	}

	secret := "sk-0123456789abcdef"
	result, err := governance.Callbacks().RunAfterTool(ctx, &tool.AfterToolArgs{
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

	snapshot, err := governance.recorder.Snapshot(ctx, "governed-output-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.SandboxRuns) != 1 {
		t.Fatalf("sandbox runs = %#v", snapshot.SandboxRuns)
	}
	run := snapshot.SandboxRuns[0]
	if run.Status != "succeeded" || len(run.OutputSummary) > 96 || !run.OutputTruncated || run.RedactionCount < 1 {
		t.Fatalf("sandbox audit = %#v", run)
	}
	if strings.Contains(run.OutputSummary, secret) {
		t.Fatal("sandbox audit contains plaintext secret")
	}
}

func TestGovernedWorkspaceExecConvertsTimeoutToModelEvidence(t *testing.T) {
	governance := newGovernedExecution(nil, redact.New(), newApprover(ApprovalConfig{}, true), "container")
	if err := governance.beginExecution("timeout-call"); err != nil {
		t.Fatal(err)
	}
	result, err := governance.Callbacks().RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName: workspaceExecToolName, ToolCallID: "timeout-call",
		Arguments: []byte(`{"command":"sleep 10","timeout_sec":1}`),
		Error:     context.DeadlineExceeded,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.CustomResult)
	if !strings.Contains(string(encoded), `"status":"timed_out"`) ||
		!strings.Contains(string(encoded), "deadline exceeded") {
		t.Fatalf("timeout result = %s", encoded)
	}
}

func TestGovernedWorkspaceExecDoesNotTreatMissingStartAsSuccess(t *testing.T) {
	ctx, governance := openGovernedTask(t, "missing-execution-start-task")
	governance.backend = "container"
	result, err := governance.Callbacks().RunAfterTool(
		ctx,
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
	snapshot, err := governance.recorder.Snapshot(ctx, "missing-execution-start-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.SandboxRuns) != 1 ||
		snapshot.SandboxRuns[0].Status != "failed" ||
		snapshot.SandboxRuns[0].ErrorType != "missing_execution_start" {
		t.Fatalf("sandbox audit = %#v", snapshot.SandboxRuns)
	}
}

func TestBuildMonitoringSummaryUsesDurableFactsOnly(t *testing.T) {
	started := time.Now().Add(-2 * time.Second)
	finished := started.Add(2 * time.Second)
	snapshot := store.ReviewSnapshot{
		Task: store.ReviewTaskRecord{StartedAt: started, FinishedAt: finished},
		PermissionDecisions: []store.PermissionDecisionRecord{
			{DecisionKind: decisionKindToolPermission, Decision: string(tool.PermissionActionAllow), ToolCallID: "a"},
			{DecisionKind: decisionKindToolPermission, Decision: string(tool.PermissionActionAsk), ToolCallID: "b"},
			{DecisionKind: decisionKindPermissionRequest, Decision: string(tool.PermissionActionAllow), ToolCallID: "c"},
			{DecisionKind: decisionKindToolPermission, Decision: string(tool.PermissionActionDeny), ToolCallID: "d"},
		},
		SandboxRuns: []store.SandboxRunRecord{
			{Status: "succeeded", Duration: time.Second},
			{Status: "failed", Duration: 500 * time.Millisecond, ErrorType: "nonzero_exit"},
		},
		Results: []store.ReviewResultRecord{
			{ResultKind: "finding", Severity: "high"},
			{ResultKind: "warning", Severity: "low"},
		},
	}
	summary := buildMonitoringSummary(snapshot, finished, "review_failure")
	if summary.ToolCallCount != 3 {
		t.Fatalf("tool call count = %d, want 3 tool_permission rows", summary.ToolCallCount)
	}
	if summary.PermissionInterceptionCount != 2 {
		t.Fatalf("interception count = %d, want ask+deny only", summary.PermissionInterceptionCount)
	}
	if summary.SandboxDurationMS != 1500 {
		t.Fatalf("sandbox duration = %d", summary.SandboxDurationMS)
	}
	if summary.FindingCount != 1 || summary.ResultKindDistribution["warning"] != 1 {
		t.Fatalf("result projection = %#v", summary)
	}
	if summary.ExceptionDistribution["nonzero_exit"] != 1 ||
		summary.ExceptionDistribution["review_failure"] != 1 {
		t.Fatalf("exception distribution = %#v", summary.ExceptionDistribution)
	}
}

func reviewInvocationContext(ctx context.Context, taskID string) context.Context {
	invocation := agent.NewInvocation(agent.WithInvocationRunOptions(agent.NewRunOptions(
		agent.WithRuntimeState(map[string]any{runtimeStateReviewTaskID: taskID}),
	)))
	return agent.NewInvocationContext(ctx, invocation)
}
