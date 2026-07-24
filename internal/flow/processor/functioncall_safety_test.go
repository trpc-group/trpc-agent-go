// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package processor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestToolSafetyPermissionBoundary(t *testing.T) {
	tests := []struct {
		name       string
		arguments  string
		pipeline   safety.Decision
		wantCalls  int
		wantStatus string
	}{
		{"allow executes", `{"command":"go test ./tool/safety"}`, safety.DecisionNeedsHumanReview, 1, ""},
		{"deny intercepts", `{"command":"rm -rf /"}`, safety.DecisionNeedsHumanReview, 0, tool.PermissionResultStatusDenied},
		{"ask intercepts", `{"command":"echo hello | cat"}`, safety.DecisionAsk, 0, tool.PermissionResultStatusApprovalRequired},
		{"review intercepts", `{"command":"go install example.com/tool"}`, safety.DecisionNeedsHumanReview, 0, tool.PermissionResultStatusApprovalRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policyConfig := safety.DefaultPolicy()
			policyConfig.PipelineAction = tt.pipeline
			guard, err := safety.NewGuard(policyConfig)
			require.NoError(t, err)
			calls := 0
			tl := &mockCallableTool{
				declaration: &tool.Declaration{Name: "workspace_exec"},
				callFn: func(context.Context, []byte) (any, error) {
					calls++
					return map[string]any{"ok": true}, nil
				},
			}
			invocation := &agent.Invocation{RunOptions: agent.NewRunOptions(
				agent.WithToolPermissionPolicy(safety.NewPermissionPolicy(guard)),
			)}

			_, result, _, _, _, err := NewFunctionCallResponseProcessor(false, nil).executeToolWithCallbacks(
				context.Background(), invocation,
				model.ToolCall{ID: "call-safety", Function: model.FunctionDefinitionParam{
					Name: "workspace_exec", Arguments: []byte(tt.arguments),
				}},
				tl, nil,
			)

			require.NoError(t, err)
			require.Equal(t, tt.wantCalls, calls)
			if tt.wantStatus == "" {
				require.JSONEq(t, `{"ok":true}`, string(mustJSON(result)))
				return
			}
			permissionResult, ok := result.(tool.PermissionResult)
			require.True(t, ok)
			require.Equal(t, tt.wantStatus, permissionResult.Status)
		})
	}
}

func TestToolSafetyPermissionBoundaryNamedToolUsesCanonicalRequest(t *testing.T) {
	guard, err := safety.NewGuard(safety.DefaultPolicy())
	require.NoError(t, err)
	calls := 0
	original := &mockCallableTool{
		declaration: &tool.Declaration{Name: "exec_command"},
		callFn: func(context.Context, []byte) (any, error) {
			calls++
			return map[string]any{"ok": true}, nil
		},
	}
	named := itool.NewNamedToolSet(&mockToolSet{tools: []tool.Tool{original}}).Tools(context.Background())[0]
	invocation := &agent.Invocation{RunOptions: agent.NewRunOptions(
		agent.WithToolPermissionPolicy(safety.NewPermissionPolicy(guard)),
	)}

	_, result, _, _, _, err := NewFunctionCallResponseProcessor(false, nil).executeToolWithCallbacks(
		context.Background(), invocation,
		model.ToolCall{ID: "call-named-safety", Function: model.FunctionDefinitionParam{
			Name:      named.Declaration().Name,
			Arguments: []byte(`{"command":"rm -rf /"}`),
		}},
		named, nil,
	)

	require.NoError(t, err)
	require.Zero(t, calls)
	permissionResult, ok := result.(tool.PermissionResult)
	require.True(t, ok)
	require.Equal(t, tool.PermissionResultStatusDenied, permissionResult.Status)
}
