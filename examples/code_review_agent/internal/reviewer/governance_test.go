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

			const taskID = "metadata-policy"
			if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
				TaskID: taskID, AppName: codeReviewAgentName,
				UserID: "reviewer", InputKind: "fixture",
			}); err != nil {
				t.Fatal(err)
			}
			ctx = reviewInvocationContext(ctx, taskID)
			state := newReviewRunState()
			const toolCallID = "ordinary-tool-call"
			arguments := []byte(`{"value":"original"}`)

			decision, err := newReviewPermissionPolicy(
				newReviewRecorder(resources.ReviewStore, redact.New()), state,
			).CheckToolPermission(ctx, &tool.PermissionRequest{
				ToolName: "ordinary_tool", ToolCallID: toolCallID,
				Arguments: arguments, Metadata: tt.metadata,
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

func TestWorkspaceExecApprovalIdentity(t *testing.T) {
	tests := []struct {
		name             string
		arguments        string
		requiresApproval bool
		group            string
	}{
		{
			name:      "unrestricted command",
			arguments: `{"command":"go test ./..."}`,
		},
		{
			name:             "simple module a",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a","timeout_sec":120}`,
			requiresApproval: true,
			group:            "simple",
		},
		{
			name:             "simple module b and different tool arguments",
			arguments:        `{"timeout_sec":60,"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-b"}`,
			requiresApproval: true,
			group:            "simple",
		},
		{
			name:             "script reference uses the script grant",
			arguments:        `{"command":"cat skills/code-review/scripts/run-go-checks.sh"}`,
			requiresApproval: true,
			group:            "simple",
		},
		{
			name:             "quoted shell operator is argument data",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh 'work/inputs/repo/module;a'"}`,
			requiresApproval: true,
			group:            "simple",
		},
		{
			name:             "escaped shell operator is argument data",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module\\&a"}`,
			requiresApproval: true,
			group:            "simple",
		},
		{
			name:             "same complex call first object order",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a && curl https://example.com","timeout_sec":120}`,
			requiresApproval: true,
			group:            "complex-a",
		},
		{
			name:             "same complex call canonical object order",
			arguments:        `{"timeout_sec":120,"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a && curl https://example.com"}`,
			requiresApproval: true,
			group:            "complex-a",
		},
		{
			name:             "complex call with different module",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-b && curl https://example.com","timeout_sec":120}`,
			requiresApproval: true,
			group:            "complex-b",
		},
		{
			name:             "redirection is complex",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a 2>&1","timeout_sec":120}`,
			requiresApproval: true,
			group:            "redirected",
		},
		{
			name:             "or operation is complex",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a || echo failed","timeout_sec":120}`,
			requiresApproval: true,
			group:            "or-operation",
		},
		{
			name:             "pipeline is complex",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a | tee checks.log","timeout_sec":120}`,
			requiresApproval: true,
			group:            "pipeline",
		},
		{
			name:             "semicolon operation is complex",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/module-a; echo done","timeout_sec":120}`,
			requiresApproval: true,
			group:            "semicolon",
		},
		{
			name:             "command substitution is complex",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo/$(printf module-a)","timeout_sec":120}`,
			requiresApproval: true,
			group:            "substitution",
		},
		{
			name:             "command substitution in double quotes is complex",
			arguments:        `{"command":"sh skills/code-review/scripts/run-go-checks.sh \"work/inputs/repo/$(printf module-a)\"","timeout_sec":120}`,
			requiresApproval: true,
			group:            "quoted-substitution",
		},
	}

	identities := make(map[string]string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arguments := []byte(tt.arguments)
			required, err := requiresApproval(&tool.PermissionRequest{
				ToolName: workspaceExecToolName,
			}, arguments)
			if err != nil {
				t.Fatal(err)
			}
			if required != tt.requiresApproval {
				t.Fatalf("requiresApproval = %t, want %t", required, tt.requiresApproval)
			}
			if !required {
				return
			}
			identity, err := approvalIdentity(workspaceExecToolName, arguments)
			if err != nil {
				t.Fatal(err)
			}
			if previous, ok := identities[tt.group]; ok && previous != identity {
				t.Fatalf("identity = %q, want %q", identity, previous)
			}
			identities[tt.group] = identity
		})
	}
	if identities["simple"] == identities["complex-a"] {
		t.Fatal("complex call reused the simple script grant")
	}
	if identities["simple"] != reviewChecksCommand {
		t.Fatalf("simple identity = %q, want configured command %q", identities["simple"], reviewChecksCommand)
	}
	if identities["complex-a"] == identities["complex-b"] {
		t.Fatal("different complex calls reused the same grant")
	}
	for _, group := range []string{
		"redirected", "or-operation", "pipeline", "semicolon", "substitution",
		"quoted-substitution",
	} {
		if identities[group] == identities["simple"] {
			t.Fatalf("%s call reused the simple script grant", group)
		}
	}
}

func TestHasComplexShellStructure(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "simple command", command: "go test ./..."},
		{name: "quoted operator", command: `printf '%s' 'a;b'`},
		{name: "escaped operator", command: `printf a\&b`},
		{name: "trailing backslash", command: `printf value\`},
		{name: "single quoted substitution", command: `printf '$(date)'`},
		{name: "and operation", command: "go test ./... && echo done", want: true},
		{name: "newline", command: "go test ./...\necho done", want: true},
		{name: "command substitution", command: "printf $(date)", want: true},
		{name: "double quoted substitution", command: `printf "$(date)"`, want: true},
		{name: "double quoted backticks", command: "printf \"`date`\"", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasComplexShellStructure(tt.command); got != tt.want {
				t.Fatalf("hasComplexShellStructure(%q) = %t, want %t",
					tt.command, got, tt.want)
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
	if first != second {
		t.Fatalf("ordinary tool identities differ: %q != %q", first, second)
	}
}

func TestGovernedCommandsAreConfiguredByName(t *testing.T) {
	commands := governedCommands{"curl"}
	tests := []struct {
		command string
		want    bool
	}{
		{command: "curl https://example.com", want: true},
		{command: "printf ready && curl https://example.com", want: true},
		{command: "echo curl", want: true},
		{command: "go test ./...", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			_, got := commands.match(tt.command)
			if got != tt.want {
				t.Fatalf("match = %t, want %t", got, tt.want)
			}
		})
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
			state := newReviewRunState()
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

func TestRequestToolPermissionDoesNotAssessTargetRisk(t *testing.T) {
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

	const taskID = "permission-tool-role"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName,
		UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	ctx = reviewInvocationContext(ctx, taskID)
	permissionTool := callableToolNamed(t, newReviewToolSet(
		newReviewRecorder(resources.ReviewStore, redact.New()),
		newReviewRunState(),
		newApprover(ApprovalConfig{}, true),
	).Tools(ctx), requestToolPermissionName)
	ctx = context.WithValue(ctx, tool.ContextKeyToolCallID{}, "permission-call")
	result, err := permissionTool.Call(ctx, []byte(`{
				"target_tool":"ordinary_tool",
				"target_arguments":{},
		"reason":"Apply the requested change."
	}`))
	if err != nil {
		t.Fatal(err)
	}
	got := result.(requestToolPermissionOutput)
	if got.Status != permissionStatusGranted {
		t.Fatalf("status = %q, want %q", got.Status, permissionStatusGranted)
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
	state := newReviewRunState()
	permissionTool := callableToolNamed(t, newReviewToolSet(
		newReviewRecorder(resources.ReviewStore, redact.New()),
		state,
		newApprover(ApprovalConfig{
			Input: strings.NewReader("n\ny\n"), Output: io.Discard,
		}, false),
	).Tools(ctx), requestToolPermissionName)
	arguments := []byte(`{
		"target_tool":"destructive_tool",
		"target_arguments":{"value":1},
		"reason":"Perform the requested destructive operation."
	}`)
	steps := []struct {
		toolCallID string
		want       string
	}{
		{toolCallID: "permission-denied", want: permissionStatusDenied},
		{toolCallID: "permission-granted", want: permissionStatusGranted},
	}
	for _, step := range steps {
		requestCtx := context.WithValue(
			ctx, tool.ContextKeyToolCallID{}, step.toolCallID,
		)
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
			name: "simple call ignores other workspace arguments", granted: standardReviewChecksCommand("module-a"),
			target: standardReviewChecksCommand("module-b"), grantTimeout: 120, targetTimeout: 60,
			want: tool.PermissionActionAllow,
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
		{
			name:         "nonstandard grant includes all workspace arguments",
			granted:      standardReviewChecksCommand("module-a") + " && curl https://example.com",
			target:       standardReviewChecksCommand("module-a") + " && curl https://example.com",
			grantTimeout: 120, targetTimeout: 60, want: tool.PermissionActionAsk,
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
			state := newReviewRunState()
			grantedArguments := []byte(fmt.Sprintf(
				`{"command":%q,"timeout_sec":%d}`,
				tt.granted,
				tt.grantTimeout,
			))
			identity, err := approvalIdentity(
				workspaceExecToolName,
				grantedArguments,
			)
			if err != nil {
				t.Fatalf("grant identity = %q, err = %v", identity, err)
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

func TestToolGrantMatchesOnlyToolName(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments string
		want      tool.PermissionAction
	}{
		{
			name:      "same arguments",
			toolName:  "destructive_tool",
			arguments: `{"count":2,"name":"release"}`,
			want:      tool.PermissionActionAllow,
		},
		{
			name:      "different arguments",
			toolName:  "destructive_tool",
			arguments: `{"name":"release","count":3}`,
			want:      tool.PermissionActionAllow,
		},
		{
			name:      "different tool",
			toolName:  "other_destructive_tool",
			arguments: `{"name":"release","count":2}`,
			want:      tool.PermissionActionAsk,
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
			const taskID = "destructive-grant"
			if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
				TaskID: taskID, AppName: codeReviewAgentName,
				UserID: "reviewer", InputKind: "fixture",
			}); err != nil {
				t.Fatal(err)
			}
			ctx = reviewInvocationContext(ctx, taskID)
			state := newReviewRunState()
			recorder := newReviewRecorder(resources.ReviewStore, redact.New())
			permissionTool := callableToolNamed(t, newReviewToolSet(
				recorder, state, newApprover(ApprovalConfig{}, true),
			).Tools(ctx), requestToolPermissionName)
			permissionCtx := context.WithValue(
				ctx, tool.ContextKeyToolCallID{}, "permission-call",
			)
			if _, err := permissionTool.Call(permissionCtx, []byte(`{
				"target_tool":"destructive_tool",
				"target_arguments":{"name":"release","count":2},
				"reason":"Publish the reviewed release."
			}`)); err != nil {
				t.Fatal(err)
			}

			const toolCallID = "target-call"
			arguments := []byte(tt.arguments)
			decision, err := newReviewPermissionPolicy(recorder, state).
				CheckToolPermission(ctx, &tool.PermissionRequest{
					ToolName: tt.toolName, ToolCallID: toolCallID,
					Arguments: arguments,
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
			wantError: "decode workspace_exec approval arguments",
		},
	}

	permissionTool := callableToolNamed(t, newReviewToolSet(
		newReviewRecorder(nil, redact.New()),
		newReviewRunState(),
		newApprover(ApprovalConfig{}, true),
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
	identity, err := approvalIdentity(workspaceExecToolName, arguments)
	if err != nil {
		t.Fatalf("identity = %q, err = %v", identity, err)
	}
	firstRun := newReviewRunState()
	firstRun.grant(workspaceExecToolName, identity)

	secondRun := newReviewRunState()
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
			state := newReviewRunState()
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
			if len(state.pendingExecutions) != 0 {
				t.Fatalf("first target remained pending after ask: %#v", state.pendingExecutions)
			}

			toolSet := newReviewToolSet(
				recorder,
				state,
				newApprover(ApprovalConfig{}, true),
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
			if len(state.pendingExecutions) != 0 {
				t.Fatalf("approved target remained pending: %#v", state.pendingExecutions)
			}
		})
	}
}

func prepareWorkspaceExecCallWithConfig(
	t *testing.T,
	state *reviewRunState,
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
