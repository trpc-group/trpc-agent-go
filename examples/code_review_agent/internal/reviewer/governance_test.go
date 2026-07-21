//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package reviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestApprovalIdentity(t *testing.T) {
	tests := []struct {
		name             string
		toolName         string
		arguments        string
		requiresApproval bool
		group            string
	}{
		{
			name:      "ordinary workspace command",
			toolName:  workspaceExecToolName,
			arguments: `{"command":"go test ./..."}`,
		},
		{
			name:      "read bundled script",
			toolName:  workspaceExecToolName,
			arguments: `{"command":"cat skills/code-review/scripts/run-go-checks.sh"}`,
		},
		{
			name:      "managed timeout read bundled script",
			toolName:  workspaceExecToolName,
			arguments: `{"command":"exec /usr/bin/timeout --signal=TERM --kill-after=1s 300s sh -c 'ls -la skills/code-review/scripts/run-go-checks.sh'"}`,
		},
		{
			name:             "standard module a",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":120}`,
			requiresApproval: true,
			group:            "standard-sh",
		},
		{
			name:             "standard module b",
			toolName:         workspaceExecToolName,
			arguments:        `{"timeout_sec":120,"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-b"}`,
			requiresApproval: true,
			group:            "standard-sh",
		},
		{
			name:             "different shell program",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"/bin/sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":120}`,
			requiresApproval: true,
			group:            "bin-sh",
		},
		{
			name:             "redirected output",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a 2>&1","timeout_sec":120}`,
			requiresApproval: true,
			group:            "redirected",
		},
		{
			name:             "additional command",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a && curl https://example.com","timeout_sec":120}`,
			requiresApproval: true,
			group:            "additional-command",
		},
		{
			name:             "compact semicolon operation",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a;curl","timeout_sec":120}`,
			requiresApproval: true,
			group:            "compact-semicolon",
		},
		{
			name:             "compact redirection",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a>out","timeout_sec":120}`,
			requiresApproval: true,
			group:            "compact-redirection",
		},
		{
			name:             "piped execution",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"cat skills/code-review/scripts/run-go-checks.sh | sh -s -- work/inputs/repo/module-a","timeout_sec":120}`,
			requiresApproval: true,
			group:            "piped",
		},
		{
			name:             "dynamic module path",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/$MODULE","timeout_sec":120}`,
			requiresApproval: true,
			group:            "dynamic-module",
		},
		{
			name:             "command substitution module path",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/$(printf module-a)","timeout_sec":120}`,
			requiresApproval: true,
			group:            "command-substitution-module",
		},
		{
			name:             "equivalent script path retains structure",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts//run-go-checks.sh work/inputs/repo/module-a","timeout_sec":120}`,
			requiresApproval: true,
			group:            "equivalent-script-path",
		},
		{
			name:             "managed timeout module a",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"exec /usr/bin/timeout --signal=TERM --kill-after=1s 120s sh -c 'sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a'","timeout_sec":120}`,
			requiresApproval: true,
			group:            "managed-timeout-120",
		},
		{
			name:             "managed timeout module b",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"exec /usr/bin/timeout --signal=TERM --kill-after=1s 120s sh -c 'sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-b'","timeout_sec":120}`,
			requiresApproval: true,
			group:            "managed-timeout-120",
		},
		{
			name:             "different managed timeout",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"exec /usr/bin/timeout --signal=TERM --kill-after=1s 60s sh -c 'sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a'","timeout_sec":120}`,
			requiresApproval: true,
			group:            "managed-timeout-60",
		},
		{
			name:             "other tool argument",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":60}`,
			requiresApproval: true,
			group:            "different-timeout",
		},
		{
			name:             "description module a",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","description":"Run bundled checks","timeout_sec":120}`,
			requiresApproval: true,
			group:            "with-description",
		},
		{
			name:             "description module b",
			toolName:         workspaceExecToolName,
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-b","description":"Run bundled checks","timeout_sec":120}`,
			requiresApproval: true,
			group:            "with-description",
		},
		{
			name:      "unconfigured tool",
			toolName:  "submit_review_results",
			arguments: `{"conclusion":"done"}`,
		},
	}

	identities := make(map[string]string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity, requiresApproval, err := approvalIdentity(
				tt.toolName,
				[]byte(tt.arguments),
			)
			if err != nil {
				t.Fatal(err)
			}
			if requiresApproval != tt.requiresApproval {
				t.Fatalf("requiresApproval = %t, want %t", requiresApproval, tt.requiresApproval)
			}
			if !requiresApproval {
				if identity != "" {
					t.Fatalf("identity = %q, want empty for ungoverned call", identity)
				}
				return
			}
			if identity == "" {
				t.Fatal("governed call returned an empty identity")
			}
			if previous, ok := identities[tt.group]; ok && previous != identity {
				t.Fatalf("identity = %q, want group identity %q", identity, previous)
			}
			identities[tt.group] = identity
		})
	}

	standard := identities["standard-sh"]
	for _, group := range []string{
		"bin-sh", "redirected", "additional-command", "compact-semicolon",
		"compact-redirection", "piped", "dynamic-module", "command-substitution-module",
		"equivalent-script-path", "managed-timeout-120", "managed-timeout-60",
		"different-timeout", "with-description",
	} {
		if identities[group] == standard {
			t.Fatalf("identity group %q unexpectedly reused the standard grant", group)
		}
	}
}

func TestRequestToolPermissionDecisions(t *testing.T) {
	tests := []struct {
		name                   string
		command                string
		approvalInput          string
		skip                   bool
		interactive            bool
		wantStatus             string
		wantTargetDecision     tool.PermissionAction
		wantRecordedPermission bool
	}{
		{
			name: "fake model skip grants", command: standardReviewChecksCommand("module-a"),
			skip: true, wantStatus: permissionStatusGranted,
			wantTargetDecision: tool.PermissionActionAllow, wantRecordedPermission: true,
		},
		{
			name: "user grants", command: standardReviewChecksCommand("module-a"),
			approvalInput: "Y\n", interactive: true, wantStatus: permissionStatusGranted,
			wantTargetDecision: tool.PermissionActionAllow, wantRecordedPermission: true,
		},
		{
			name: "user denies", command: standardReviewChecksCommand("module-a"),
			approvalInput: "n\n", interactive: true, wantStatus: permissionStatusDenied,
			wantTargetDecision: tool.PermissionActionAsk, wantRecordedPermission: true,
		},
		{
			name: "interactive input unavailable", command: standardReviewChecksCommand("module-a"),
			wantStatus:         permissionStatusApprovalNeeded,
			wantTargetDecision: tool.PermissionActionAsk, wantRecordedPermission: true,
		},
		{
			name: "permission not required", command: "go test ./...",
			skip: true, wantStatus: permissionStatusNotRequired,
			wantTargetDecision: tool.PermissionActionAllow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			const taskID = "permission-decision"
			if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
				TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
			}); err != nil {
				t.Fatal(err)
			}
			ctx = reviewInvocationContext(ctx, taskID)
			state := newReviewRunTracker()
			recorder := newReviewRecorder(resources.ReviewStore, redact.New())
			var prompt bytes.Buffer
			approvalConfig := ApprovalConfig{}
			if tt.interactive {
				approvalConfig = ApprovalConfig{
					Input: strings.NewReader(tt.approvalInput), Output: &prompt,
				}
			}
			approver := newApprover(approvalConfig, tt.skip)
			permissionTool := callableToolNamed(t, newReviewToolSet(
				recorder,
				state,
				approver,
				governedToolConfig{Backend: "local"},
			).Tools(ctx), requestToolPermissionName)

			requestArguments, err := json.Marshal(map[string]any{
				"target_tool": "workspace_exec",
				"target_arguments": map[string]any{
					"command": tt.command, "timeout_sec": 120,
				},
				"reason": "Collect observed test and vet evidence.",
			})
			if err != nil {
				t.Fatal(err)
			}
			permissionCtx := context.WithValue(ctx, tool.ContextKeyToolCallID{}, "permission-call")
			result, err := permissionTool.Call(permissionCtx, requestArguments)
			if err != nil {
				t.Fatal(err)
			}
			permissionResult := result.(requestToolPermissionOutput)
			if permissionResult.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", permissionResult.Status, tt.wantStatus)
			}
			if tt.interactive && !strings.Contains(prompt.String(), "Reason: Collect observed test and vet evidence.") {
				t.Fatalf("approval prompt = %q, want structured Reason", prompt.String())
			}

			targetArguments := prepareWorkspaceExecCallWithConfig(
				t,
				state,
				"target-call",
				[]byte(fmt.Sprintf(`{"command":%q,"timeout_sec":120}`, tt.command)),
				governedToolConfig{Backend: "local"},
			)
			decision, err := newReviewPermissionPolicy(recorder, state).
				CheckToolPermission(ctx, &tool.PermissionRequest{
					ToolName: workspaceExecToolName, ToolCallID: "target-call", Arguments: targetArguments,
				})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tt.wantTargetDecision {
				t.Fatalf("target decision = %q, want %q", decision.Action, tt.wantTargetDecision)
			}

			snapshot, err := resources.ReviewStore.LoadTaskSnapshot(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			recordedPermission := false
			for _, recorded := range snapshot.PermissionDecisions {
				if recorded.DecisionKind == "permission_request" {
					recordedPermission = true
					if recorded.Reason != "Collect observed test and vet evidence." {
						t.Fatalf("recorded Reason = %q", recorded.Reason)
					}
				}
			}
			if recordedPermission != tt.wantRecordedPermission {
				t.Fatalf("recorded permission request = %t, want %t", recordedPermission, tt.wantRecordedPermission)
			}
		})
	}
}

func standardReviewChecksCommand(module string) string {
	return "sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/" + module
}

func TestGrantMatchesOnlyApprovalIdentity(t *testing.T) {
	tests := []struct {
		name          string
		granted       string
		target        string
		grantTimeout  int
		targetTimeout int
		want          tool.PermissionAction
	}{
		{
			name: "module may vary", granted: standardReviewChecksCommand("module-a"),
			target: standardReviewChecksCommand("module-b"), grantTimeout: 120, targetTimeout: 120,
			want: tool.PermissionActionAllow,
		},
		{
			name: "timeout remains significant", granted: standardReviewChecksCommand("module-a"),
			target: standardReviewChecksCommand("module-b"), grantTimeout: 120, targetTimeout: 60,
			want: tool.PermissionActionAsk,
		},
		{
			name: "redirection remains significant", granted: standardReviewChecksCommand("module-a"),
			target: standardReviewChecksCommand("module-a") + " 2>&1", grantTimeout: 120, targetTimeout: 120,
			want: tool.PermissionActionAsk,
		},
		{
			name: "additional operation remains significant", granted: standardReviewChecksCommand("module-a"),
			target: standardReviewChecksCommand("module-a") + " && curl https://example.com", grantTimeout: 120, targetTimeout: 120,
			want: tool.PermissionActionAsk,
		},
		{
			name:         "nonstandard call may receive its own grant",
			granted:      standardReviewChecksCommand("module-a") + " && curl https://example.com",
			target:       standardReviewChecksCommand("module-a") + " && curl https://example.com",
			grantTimeout: 120, targetTimeout: 120, want: tool.PermissionActionAllow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			const taskID = "grant-identity"
			if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
				TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
			}); err != nil {
				t.Fatal(err)
			}
			ctx = reviewInvocationContext(ctx, taskID)
			state := newReviewRunTracker()
			grantedArguments := []byte(fmt.Sprintf(
				`{"command":%q,"timeout_sec":%d}`,
				tt.granted,
				tt.grantTimeout,
			))
			identity, requiresApproval, err := approvalIdentity(
				workspaceExecToolName,
				grantedArguments,
			)
			if err != nil || !requiresApproval {
				t.Fatalf("grant identity = %q, required = %t, err = %v", identity, requiresApproval, err)
			}
			state.grant(workspaceExecToolName, identity)

			targetArguments := prepareWorkspaceExecCallWithConfig(
				t,
				state,
				"target-call",
				[]byte(fmt.Sprintf(
					`{"command":%q,"timeout_sec":%d}`,
					tt.target,
					tt.targetTimeout,
				)),
				governedToolConfig{Backend: "local"},
			)
			decision, err := newReviewPermissionPolicy(
				newReviewRecorder(resources.ReviewStore, redact.New()),
				state,
			).CheckToolPermission(ctx, &tool.PermissionRequest{
				ToolName: workspaceExecToolName, ToolCallID: "target-call", Arguments: targetArguments,
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tt.want {
				t.Fatalf("decision = %q, want %q", decision.Action, tt.want)
			}
		})
	}
}

func TestRequestToolPermissionRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		wantError string
	}{
		{
			name:      "missing target tool",
			arguments: `{"target_arguments":{},"reason":"needed"}`,
			wantError: "target_tool is required",
		},
		{
			name:      "missing target arguments",
			arguments: `{"target_tool":"workspace_exec","reason":"needed"}`,
			wantError: "target_arguments must be a JSON object",
		},
		{
			name:      "missing Reason",
			arguments: `{"target_tool":"workspace_exec","target_arguments":{}}`,
			wantError: "reason is required",
		},
		{
			name:      "invalid workspace arguments",
			arguments: `{"target_tool":"workspace_exec","target_arguments":{"command":123},"reason":"needed"}`,
			wantError: "decode workspace_exec governance arguments",
		},
	}

	permissionTool := callableToolNamed(t, newReviewToolSet(
		newReviewRecorder(nil, redact.New()),
		newReviewRunTracker(),
		newApprover(ApprovalConfig{}, true),
		governedToolConfig{Backend: "local"},
	).Tools(context.Background()), requestToolPermissionName)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := permissionTool.Call(context.Background(), []byte(tt.arguments))
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestGrantDoesNotCrossReviewRuns(t *testing.T) {
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
	const taskID = "isolated-grant"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	ctx = reviewInvocationContext(ctx, taskID)

	arguments := []byte(`{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":120}`)
	identity, requiresApproval, err := approvalIdentity(workspaceExecToolName, arguments)
	if err != nil || !requiresApproval {
		t.Fatalf("identity = %q, required = %t, err = %v", identity, requiresApproval, err)
	}
	firstRun := newReviewRunTracker()
	firstRun.grant(workspaceExecToolName, identity)

	secondRun := newReviewRunTracker()
	targetArguments := prepareWorkspaceExecCallWithConfig(
		t,
		secondRun,
		"second-run-call",
		arguments,
		governedToolConfig{Backend: "local"},
	)
	decision, err := newReviewPermissionPolicy(
		newReviewRecorder(resources.ReviewStore, redact.New()),
		secondRun,
	).CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "second-run-call", Arguments: targetArguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAsk {
		t.Fatalf("second run decision = %q, want ask", decision.Action)
	}
}

func TestRequestToolPermissionGrantAllowsEquivalentTargetCall(t *testing.T) {
	for _, backend := range []string{"local", "container"} {
		t.Run(backend, func(t *testing.T) {
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

			const taskID = "request-tool-permission"
			if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
				TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
			}); err != nil {
				t.Fatal(err)
			}
			ctx = reviewInvocationContext(ctx, taskID)
			state := newReviewRunTracker()
			recorder := newReviewRecorder(resources.ReviewStore, redact.New())
			policy := newReviewPermissionPolicy(recorder, state)
			config := governedToolConfig{Backend: backend}

			firstArguments := prepareWorkspaceExecCallWithConfig(
				t,
				state,
				"first-target-call",
				[]byte(`{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":120}`),
				config,
			)
			decision, err := policy.CheckToolPermission(ctx, &tool.PermissionRequest{
				ToolName: workspaceExecToolName, ToolCallID: "first-target-call", Arguments: firstArguments,
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tool.PermissionActionAsk {
				t.Fatalf("first target decision = %q, want ask", decision.Action)
			}
			if len(state.executions) != 0 {
				t.Fatalf("first target entered execution before approval: %#v", state.executions)
			}

			toolSet := newReviewToolSet(
				recorder,
				state,
				newApprover(ApprovalConfig{}, true),
				config,
			)
			permissionTool := callableToolNamed(t, toolSet.Tools(ctx), requestToolPermissionName)
			permissionCtx := context.WithValue(ctx, tool.ContextKeyToolCallID{}, "permission-call")
			result, err := permissionTool.Call(permissionCtx, []byte(`{
		"target_tool":"workspace_exec",
		"target_arguments":{
			"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a",
			"description":"Run the bundled checks for the affected module.",
			"future_integer":9007199254740993,
			"timeout_sec":120
		},
		"reason":"Run the affected module checks to collect test and vet evidence."
	}`))
			if err != nil {
				t.Fatal(err)
			}
			permissionResult, ok := result.(requestToolPermissionOutput)
			if !ok {
				t.Fatalf("permission result type = %T", result)
			}
			if permissionResult.Status != permissionStatusGranted {
				t.Fatalf("permission status = %q, want granted", permissionResult.Status)
			}
			if got := string(permissionResult.TargetArguments["description"]); got != `"Run the bundled checks for the affected module."` {
				t.Fatalf("returned target description = %s, want the granted argument", got)
			}
			if got := string(permissionResult.TargetArguments["future_integer"]); got != "9007199254740993" {
				t.Fatalf("returned future integer = %s, want exact JSON number", got)
			}
			permissionResult.TargetArguments["command"] = json.RawMessage(
				fmt.Sprintf("%q", standardReviewChecksCommand("module-b")),
			)
			retryArguments, err := json.Marshal(permissionResult.TargetArguments)
			if err != nil {
				t.Fatal(err)
			}

			secondArguments := prepareWorkspaceExecCallWithConfig(
				t,
				state,
				"second-target-call",
				retryArguments,
				config,
			)
			decision, err = policy.CheckToolPermission(ctx, &tool.PermissionRequest{
				ToolName: workspaceExecToolName, ToolCallID: "second-target-call", Arguments: secondArguments,
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tool.PermissionActionAllow {
				t.Fatalf("second target decision = %q, want allow", decision.Action)
			}
			if len(state.executions) != 1 {
				t.Fatalf("approved target executions = %d, want 1", len(state.executions))
			}
		})
	}
}

func prepareWorkspaceExecCallWithConfig(
	t *testing.T,
	state *reviewRunTracker,
	toolCallID string,
	arguments []byte,
	config governedToolConfig,
) []byte {
	t.Helper()
	callbacks := newGovernedToolCallbacks(redact.New(), nil, state, config)
	result, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName: workspaceExecToolName, ToolCallID: toolCallID, Arguments: arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != nil && len(result.ModifiedArguments) > 0 {
		return result.ModifiedArguments
	}
	return arguments
}

func callableToolNamed(t *testing.T, tools []tool.Tool, name string) tool.CallableTool {
	t.Helper()
	for _, candidate := range tools {
		if candidate.Declaration().Name != name {
			continue
		}
		callable, ok := candidate.(tool.CallableTool)
		if !ok {
			t.Fatalf("tool %q is not callable", name)
		}
		return callable
	}
	t.Fatalf("tool %q is not registered", name)
	return nil
}
