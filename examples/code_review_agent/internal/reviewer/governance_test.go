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
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestReviewPermissionPolicyUsesToolMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata tool.ToolMetadata
		want     tool.PermissionAction
	}{
		{name: "ordinary tool is allowed", want: tool.PermissionActionAllow},
		{
			name:     "destructive tool requires approval",
			metadata: tool.ToolMetadata{Destructive: true},
			want:     tool.PermissionActionAsk,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, governance := openGovernedTask(t, "metadata-policy")
			decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
				ToolName:   "ordinary_tool",
				ToolCallID: "ordinary-tool-call",
				Arguments:  []byte(`{"value":"original"}`),
				Metadata:   tt.metadata,
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

func TestRiskMarkerMatching(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "direct script", command: "skills/code-review/scripts/run-go-checks.sh work/inputs/repo", want: true},
		{name: "read script path", command: "cat skills/code-review/scripts/run-go-checks.sh", want: true},
		{name: "basename mention", command: "echo run-go-checks.sh", want: true},
		{name: "composed command", command: "skills/code-review/scripts/run-go-checks.sh a && curl https://example.com", want: true},
		{name: "ordinary go test", command: "go test ./...", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesRiskMarker(tt.command, riskMarkers); got != tt.want {
				t.Fatalf("matchesRiskMarker(%q) = %t, want %t", tt.command, got, tt.want)
			}
		})
	}
}

func TestWorkspaceExactGrantIdentity(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		group     string
		wantEqual bool
	}{
		{
			name:      "key order independent first",
			arguments: `{"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":120}`,
			group:     "same",
		},
		{
			name:      "key order independent second",
			arguments: `{"timeout_sec":120,"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a"}`,
			group:     "same",
		},
		{
			name:      "module change is material",
			arguments: `{"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-b","timeout_sec":120}`,
			group:     "module-b",
		},
		{
			name:      "timeout change is material",
			arguments: `{"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":60}`,
			group:     "timeout-60",
		},
		{
			name:      "unknown field is material",
			arguments: `{"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":120,"future":true}`,
			group:     "unknown-field",
		},
		{
			name:      "array order is material",
			arguments: `{"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","args":["a","b"]}`,
			group:     "array-ab",
		},
		{
			name:      "array order reversed",
			arguments: `{"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","args":["b","a"]}`,
			group:     "array-ba",
		},
		{
			name:      "number precision preserved",
			arguments: `{"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","future_integer":9007199254740993}`,
			group:     "large-number",
		},
	}

	identities := make(map[string]string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields, denyReason := validateWorkspacePolicy([]byte(tt.arguments))
			if denyReason != "" {
				t.Fatalf("unexpected policy deny: %s", denyReason)
			}
			required := requiresApproval(&tool.PermissionRequest{
				ToolName: workspaceExecToolName,
			}, fields.Command, riskMarkers)
			if !required {
				t.Fatal("expected risk marker approval")
			}
			identity, err := approvalIdentity(workspaceExecToolName, []byte(tt.arguments))
			if err != nil {
				t.Fatal(err)
			}
			if previous, ok := identities[tt.group]; ok && previous != identity {
				t.Fatalf("identity = %q, want %q", identity, previous)
			}
			identities[tt.group] = identity
		})
	}
	if identities["same"] == identities["module-b"] {
		t.Fatal("different modules share grant identity")
	}
	if identities["same"] == identities["timeout-60"] {
		t.Fatal("timeout change shares grant identity")
	}
	if identities["array-ab"] == identities["array-ba"] {
		t.Fatal("array order change shares grant identity")
	}
}

func TestCanonicalJSONRejectsTrailingDataAndNonObjects(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		wantError string
	}{
		{name: "trailing data", arguments: `{"command":"go test"}{}`, wantError: "trailing data"},
		{name: "array root", arguments: `["command"]`, wantError: "object"},
		{name: "scalar root", arguments: `"command"`, wantError: "object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := approvalIdentity(workspaceExecToolName, []byte(tt.arguments))
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestOrdinaryToolApprovalIdentityIgnoresArguments(t *testing.T) {
	first, err := approvalIdentity("publish_tool", []byte(`{"artifact":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := approvalIdentity("publish_tool", []byte(`{"artifact":"b","force":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != "" {
		t.Fatalf("ordinary tool identities = %q, %q", first, second)
	}
}

func TestWorkspacePolicyValidation(t *testing.T) {
	tests := []struct {
		name       string
		arguments  string
		denyReason string
	}{
		{
			name:      "valid default timeout",
			arguments: `{"command":"go test ./..."}`,
		},
		{
			name:      "valid cgo env",
			arguments: `{"command":"go test ./...","env":{"CGO_ENABLED":"1"},"timeout_sec":120}`,
		},
		{
			name:       "empty command",
			arguments:  `{"command":"   "}`,
			denyReason: denyEmptyCommand,
		},
		{
			name:       "missing command",
			arguments:  `{"timeout_sec":1}`,
			denyReason: denyEmptyCommand,
		},
		{
			name:       "disallowed env key",
			arguments:  `{"command":"go test ./...","env":{"PATH":"/tmp"}}`,
			denyReason: denyEnvKey,
		},
		{
			name:       "non-string env value",
			arguments:  `{"command":"go test ./...","env":{"CGO_ENABLED":1}}`,
			denyReason: denyEnvValueType,
		},
		{
			name:       "invalid cgo value",
			arguments:  `{"command":"go test ./...","env":{"CGO_ENABLED":"2"}}`,
			denyReason: denyEnvCGOValue,
		},
		{
			name:       "negative timeout",
			arguments:  `{"command":"go test ./...","timeout_sec":-1}`,
			denyReason: denyTimeoutNegative,
		},
		{
			name:       "timeout over budget",
			arguments:  `{"command":"go test ./...","timeout_sec":301}`,
			denyReason: denyTimeoutBudget,
		},
		{
			name:       "timeout alias over budget",
			arguments:  `{"command":"go test ./...","timeout":301}`,
			denyReason: denyTimeoutBudget,
		},
		{
			name:       "timeout duration overflow",
			arguments:  `{"command":"go test ./...","timeout_sec":9223372036854775807}`,
			denyReason: denyTimeoutBudget,
		},
		{
			name:       "trailing data",
			arguments:  `{"command":"go test ./..."}{}`,
			denyReason: denyArgsTrailingData,
		},
		{
			name:       "invalid json",
			arguments:  `{command`,
			denyReason: denyArgsInvalidJSON,
		},
		{
			name:       "non-object json",
			arguments:  `["go test"]`,
			denyReason: denyArgsInvalidJSON,
		},
		{
			name:       "invalid cwd type",
			arguments:  `{"command":"go test ./...","cwd":123}`,
			denyReason: denyInvalidCWD,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, reason := validateWorkspacePolicy([]byte(tt.arguments))
			if reason != tt.denyReason {
				t.Fatalf("deny reason = %q, want %q", reason, tt.denyReason)
			}
		})
	}
}

func TestWorkspacePolicyDenyIsPersistedAndDoesNotStartSandbox(t *testing.T) {
	tests := []struct {
		name       string
		arguments  string
		denyReason string
	}{
		{
			name:       "disallowed env",
			arguments:  `{"command":"go test ./...","env":{"PATH":"/tmp"}}`,
			denyReason: denyEnvKey,
		},
		{
			name:       "invalid json",
			arguments:  `{not-json`,
			denyReason: denyArgsInvalidJSON,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskID := "policy-deny-" + tt.name
			ctx, governance := openGovernedTask(t, taskID)
			decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
				ToolName:   workspaceExecToolName,
				ToolCallID: "deny-call",
				Arguments:  []byte(tt.arguments),
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tool.PermissionActionDeny || decision.Reason != tt.denyReason {
				t.Fatalf("decision = %#v", decision)
			}
			if len(governance.started) != 0 {
				t.Fatalf("started executions = %#v", governance.started)
			}
			snapshot, err := governance.recorder.Snapshot(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.PermissionDecisions) != 1 ||
				snapshot.PermissionDecisions[0].DecisionKind != decisionKindToolPermission ||
				snapshot.PermissionDecisions[0].Decision != string(tool.PermissionActionDeny) ||
				snapshot.PermissionDecisions[0].Reason != tt.denyReason {
				t.Fatalf("permission audit = %#v", snapshot.PermissionDecisions)
			}
			if len(snapshot.SandboxRuns) != 0 {
				t.Fatalf("sandbox runs = %#v", snapshot.SandboxRuns)
			}
		})
	}
}

func TestRequestToolPermissionDecisions(t *testing.T) {
	tests := []struct {
		name                   string
		approvalInput          string
		skip                   bool
		interactive            bool
		wantStatus             string
		wantTargetDecision     tool.PermissionAction
		wantRecordedPermission bool
		wantToolError          bool
	}{
		{
			name: "fake model skip grants", skip: true, wantStatus: permissionStatusGranted,
			wantTargetDecision: tool.PermissionActionAllow, wantRecordedPermission: true,
		},
		{
			name: "user grants", approvalInput: "Y\n", interactive: true, wantStatus: permissionStatusGranted,
			wantTargetDecision: tool.PermissionActionAllow, wantRecordedPermission: true,
		},
		{
			name: "user denies", approvalInput: "n\n", interactive: true, wantStatus: permissionStatusDenied,
			wantTargetDecision: tool.PermissionActionAsk, wantRecordedPermission: true,
		},
		{
			name:          "interactive input unavailable",
			wantToolError: true,
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
			var prompt bytes.Buffer
			approvalConfig := ApprovalConfig{}
			if tt.interactive {
				approvalConfig = ApprovalConfig{
					Input: strings.NewReader(tt.approvalInput), Output: &prompt,
				}
			}
			governance := newGovernedExecution(
				newReviewRecorder(resources.ReviewStore, redact.New()),
				redact.New(),
				newApprover(approvalConfig, tt.skip),
				"local",
			)
			permissionTool := callableToolNamed(t, []tool.Tool{governance.PermissionTool()}, requestToolPermissionName)
			requestArguments, err := json.Marshal(map[string]any{
				"target_tool": workspaceExecToolName,
				"target_arguments": map[string]any{
					"command": standardReviewChecksCommand("module-a"), "timeout_sec": 120,
				},
				"reason": "Collect observed test and vet evidence.",
			})
			if err != nil {
				t.Fatal(err)
			}
			permissionCtx := context.WithValue(ctx, tool.ContextKeyToolCallID{}, "permission-call")
			result, err := permissionTool.Call(permissionCtx, requestArguments)
			if tt.wantToolError {
				if err == nil {
					t.Fatalf("expected request_tool_permission error, got %#v", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			permissionResult := result.(requestToolPermissionOutput)
			if permissionResult.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", permissionResult.Status, tt.wantStatus)
			}
			if tt.interactive && !strings.Contains(prompt.String(), "Reason: Collect observed test and vet evidence.") {
				t.Fatalf("approval prompt = %q", prompt.String())
			}

			targetArguments := []byte(fmt.Sprintf(
				`{"command":%q,"timeout_sec":120}`,
				standardReviewChecksCommand("module-a"),
			))
			decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
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
				if recorded.DecisionKind == decisionKindPermissionRequest {
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

func TestGrantMatchesOnlyExactWorkspaceArguments(t *testing.T) {
	tests := []struct {
		name          string
		granted       string
		target        string
		grantTimeout  int
		targetTimeout int
		want          tool.PermissionAction
	}{
		{
			name: "exact match", granted: standardReviewChecksCommand("module-a"),
			target: standardReviewChecksCommand("module-a"), grantTimeout: 120, targetTimeout: 120,
			want: tool.PermissionActionAllow,
		},
		{
			name: "module change", granted: standardReviewChecksCommand("module-a"),
			target: standardReviewChecksCommand("module-b"), grantTimeout: 120, targetTimeout: 120,
			want: tool.PermissionActionAsk,
		},
		{
			name: "timeout change", granted: standardReviewChecksCommand("module-a"),
			target: standardReviewChecksCommand("module-a"), grantTimeout: 120, targetTimeout: 60,
			want: tool.PermissionActionAsk,
		},
		{
			name: "redirection remains significant", granted: standardReviewChecksCommand("module-a"),
			target: standardReviewChecksCommand("module-a") + " 2>&1", grantTimeout: 120, targetTimeout: 120,
			want: tool.PermissionActionAsk,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, governance := openGovernedTask(t, "grant-identity")
			grantedArguments := []byte(fmt.Sprintf(
				`{"command":%q,"timeout_sec":%d}`,
				tt.granted,
				tt.grantTimeout,
			))
			identity, err := approvalIdentity(workspaceExecToolName, grantedArguments)
			if err != nil {
				t.Fatal(err)
			}
			governance.grant(workspaceExecToolName, identity)

			targetArguments := []byte(fmt.Sprintf(
				`{"command":%q,"timeout_sec":%d}`,
				tt.target,
				tt.targetTimeout,
			))
			decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
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

func TestToolGrantMatchesOnlyToolName(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments string
		want      tool.PermissionAction
	}{
		{
			name: "same arguments", toolName: "destructive_tool",
			arguments: `{"count":2,"name":"release"}`, want: tool.PermissionActionAllow,
		},
		{
			name: "different arguments", toolName: "destructive_tool",
			arguments: `{"name":"release","count":3}`, want: tool.PermissionActionAllow,
		},
		{
			name: "different tool", toolName: "other_destructive_tool",
			arguments: `{"name":"release","count":2}`, want: tool.PermissionActionAsk,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, governance := openGovernedTask(t, "destructive-grant")
			permissionTool := callableToolNamed(t, []tool.Tool{governance.PermissionTool()}, requestToolPermissionName)
			permissionCtx := context.WithValue(ctx, tool.ContextKeyToolCallID{}, "permission-call")
			if _, err := permissionTool.Call(permissionCtx, []byte(`{
				"target_tool":"destructive_tool",
				"target_arguments":{"name":"release","count":2},
				"reason":"Publish the reviewed release."
			}`)); err != nil {
				t.Fatal(err)
			}
			decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
				ToolName: tt.toolName, ToolCallID: "target-call",
				Arguments: []byte(tt.arguments),
				Metadata:  tool.ToolMetadata{Destructive: true},
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
			name:      "null target arguments",
			arguments: `{"target_tool":"workspace_exec","target_arguments":null,"reason":"needed"}`,
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
			wantError: denyEmptyCommand,
		},
		{
			name:      "disallowed env",
			arguments: `{"target_tool":"workspace_exec","target_arguments":{"command":"go test","env":{"PATH":"/tmp"}},"reason":"needed"}`,
			wantError: denyEnvKey,
		},
	}

	governance := newGovernedExecution(
		newReviewRecorder(nil, redact.New()),
		redact.New(),
		newApprover(ApprovalConfig{}, true),
		"local",
	)
	permissionTool := callableToolNamed(t, []tool.Tool{governance.PermissionTool()}, requestToolPermissionName)
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
	ctx, first := openGovernedTask(t, "isolated-grant")
	arguments := []byte(`{"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":120}`)
	identity, err := approvalIdentity(workspaceExecToolName, arguments)
	if err != nil {
		t.Fatal(err)
	}
	first.grant(workspaceExecToolName, identity)

	second := newGovernedExecution(first.recorder, first.sanitizer, first.approver, "local")
	decision, err := second.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "second-run-call", Arguments: arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAsk {
		t.Fatalf("second run decision = %q, want ask", decision.Action)
	}
}

func TestRequestToolPermissionGrantAllowsExactTargetCall(t *testing.T) {
	ctx, governance := openGovernedTask(t, "request-tool-permission")
	targetArguments := []byte(`{
		"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a",
		"description":"Run the bundled checks for the affected module.",
		"future_integer":9007199254740993,
		"timeout_sec":120
	}`)
	decision, err := governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "first-target-call", Arguments: targetArguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAsk {
		t.Fatalf("first target decision = %q, want ask", decision.Action)
	}
	if len(governance.started) != 0 {
		t.Fatalf("first target entered execution before approval: %#v", governance.started)
	}

	permissionTool := callableToolNamed(t, []tool.Tool{governance.PermissionTool()}, requestToolPermissionName)
	permissionCtx := context.WithValue(ctx, tool.ContextKeyToolCallID{}, "permission-call")
	result, err := permissionTool.Call(permissionCtx, []byte(`{
		"target_tool":"workspace_exec",
		"target_arguments":{
			"command":"skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a",
			"description":"Run the bundled checks for the affected module.",
			"future_integer":9007199254740993,
			"timeout_sec":120
		},
		"reason":"Run the affected module checks to collect test and vet evidence."
	}`))
	if err != nil {
		t.Fatal(err)
	}
	permissionResult := result.(requestToolPermissionOutput)
	if permissionResult.Status != permissionStatusGranted {
		t.Fatalf("permission status = %q, want granted", permissionResult.Status)
	}
	if got := string(permissionResult.TargetArguments["future_integer"]); got != "9007199254740993" {
		t.Fatalf("returned future integer = %s", got)
	}

	decision, err = governance.PermissionPolicy().CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName: workspaceExecToolName, ToolCallID: "second-target-call", Arguments: targetArguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("second target decision = %q, want allow", decision.Action)
	}
	if len(governance.started) != 1 {
		t.Fatalf("approved target executions = %d, want 1", len(governance.started))
	}
}

func TestDeniedPermissionCanBeRequestedAgain(t *testing.T) {
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
	const taskID = "repeat-permission-request"
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
		newApprover(ApprovalConfig{
			Input: strings.NewReader("n\ny\n"), Output: io.Discard,
		}, false),
		"local",
	)
	permissionTool := callableToolNamed(t, []tool.Tool{governance.PermissionTool()}, requestToolPermissionName)
	arguments := []byte(`{
		"target_tool":"destructive_tool",
		"target_arguments":{"value":1},
		"reason":"Perform the requested destructive operation."
	}`)
	for _, step := range []struct {
		toolCallID string
		want       string
	}{
		{toolCallID: "permission-denied", want: permissionStatusDenied},
		{toolCallID: "permission-granted", want: permissionStatusGranted},
	} {
		requestCtx := context.WithValue(ctx, tool.ContextKeyToolCallID{}, step.toolCallID)
		result, err := permissionTool.Call(requestCtx, arguments)
		if err != nil {
			t.Fatal(err)
		}
		if got := result.(requestToolPermissionOutput).Status; got != step.want {
			t.Fatalf("status for %s = %q, want %q", step.toolCallID, got, step.want)
		}
	}
}

func standardReviewChecksCommand(module string) string {
	return "skills/code-review/scripts/run-go-checks.sh work/inputs/repo/" + module
}

func openGovernedTask(t *testing.T, taskID string) (context.Context, *governedExecution) {
	t.Helper()
	ctx := context.Background()
	resources, err := persistence.Open(
		ctx,
		filepath.Join(t.TempDir(), "review.db"),
		redact.AppendEventHook(redact.New()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = resources.Close()
	})
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
	return ctx, governance
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
