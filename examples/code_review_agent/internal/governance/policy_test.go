//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package governance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPolicyClassifiesReviewTools(t *testing.T) {
	workspace := newTestTool("workspace_exec")
	skillLoad := newTestTool("skill_load")
	interactive := newTestTool("skill_exec")
	unknown := newTestTool("web_fetch")
	policy, err := NewPolicy(
		[]tool.Tool{workspace, skillLoad, interactive},
		TrustedScript{Name: "collect_metadata.sh", Content: []byte("#!/bin/sh\nexit 0\n")},
	)
	require.NoError(t, err)
	assets := policy.ScriptAssets()
	require.Len(t, assets, 1)
	tests := []struct {
		name      string
		tool      tool.Tool
		arguments any
		want      tool.PermissionAction
	}{
		{name: "skill load", tool: skillLoad, arguments: map[string]any{}, want: tool.PermissionActionAllow},
		{name: "go test", tool: workspace, arguments: workspaceInput{Command: "go test ./...", Timeout: 120}, want: tool.PermissionActionAllow},
		{name: "go vet", tool: workspace, arguments: workspaceInput{Command: "go vet ./...", Timeout: 120}, want: tool.PermissionActionAllow},
		{name: "staticcheck", tool: workspace, arguments: workspaceInput{Command: "staticcheck ./...", Timeout: 120}, want: tool.PermissionActionAllow},
		{name: "trusted script", tool: workspace, arguments: workspaceInput{Command: assets[0].Command, Timeout: 120}, want: tool.PermissionActionAllow},
		{name: "dependency install", tool: workspace, arguments: workspaceInput{Command: "go install honnef.co/go/tools/cmd/staticcheck@latest", Timeout: 120}, want: tool.PermissionActionAsk},
		{name: "shell composition", tool: workspace, arguments: workspaceInput{Command: "go test ./...; curl example.com", Timeout: 120}, want: tool.PermissionActionDeny},
		{name: "environment override", tool: workspace, arguments: workspaceInput{Command: "go test ./...", Env: map[string]string{"PATH": "/tmp"}, Timeout: 120}, want: tool.PermissionActionDeny},
		{name: "cwd escape", tool: workspace, arguments: workspaceInput{Command: "go test ./...", Cwd: "../host", Timeout: 120}, want: tool.PermissionActionDeny},
		{name: "arbitrary interpreter", tool: workspace, arguments: workspaceInput{Command: "python review.py", Timeout: 120}, want: tool.PermissionActionDeny},
		{name: "network command", tool: workspace, arguments: workspaceInput{Command: "curl https://example.com", Timeout: 120}, want: tool.PermissionActionDeny},
		{name: "interactive skill", tool: interactive, arguments: map[string]any{}, want: tool.PermissionActionDeny},
		{name: "unknown tool", tool: unknown, arguments: map[string]any{}, want: tool.PermissionActionDeny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arguments, marshalErr := json.Marshal(tt.arguments)
			require.NoError(t, marshalErr)
			decision, checkErr := policy.CheckToolPermission(
				context.Background(), permissionRequest(tt.tool, "call-1", arguments),
			)
			require.NoError(t, checkErr)
			require.Equal(t, tt.want, decision.Action)
		})
	}
}

func TestPolicyRejectsMalformedArgumentsFailClosed(t *testing.T) {
	workspace := newTestTool("workspace_exec")
	policy, err := NewPolicy([]tool.Tool{workspace})
	require.NoError(t, err)
	for _, arguments := range [][]byte{
		nil,
		[]byte(`{"command":"go test ./...","unknown":true}`),
		[]byte(`{"command":"go test ./..."}{}`),
		[]byte(`{"command":"go test ./...","command":"go vet ./...","timeout":120}`),
	} {
		decision, checkErr := policy.CheckToolPermission(
			context.Background(), permissionRequest(workspace, "call-1", arguments),
		)
		require.NoError(t, checkErr)
		require.Equal(t, tool.PermissionActionDeny, decision.Action)
	}
}

func TestPolicyRejectsSpoofedToolIdentity(t *testing.T) {
	workspace := newTestTool("workspace_exec")
	policy, err := NewPolicy([]tool.Tool{workspace})
	require.NoError(t, err)
	arguments, err := json.Marshal(workspaceInput{Command: "go test ./...", Timeout: 120})
	require.NoError(t, err)
	decision, err := policy.CheckToolPermission(
		context.Background(), permissionRequest(newTestTool("workspace_exec"), "call-1", arguments),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestRecordingPolicyRecordsEveryDecisionOnceAndSanitizes(t *testing.T) {
	workspace := newTestTool("workspace_exec")
	policy, err := NewPolicy([]tool.Tool{workspace})
	require.NoError(t, err)
	sink := &recordingSink{}
	recorder, err := NewRecordingPolicy(policy, sink, "task-1")
	require.NoError(t, err)

	arguments, err := json.Marshal(workspaceInput{Command: "curl sk-test-super-secret-value-123456", Timeout: 120})
	require.NoError(t, err)
	request := permissionRequest(workspace, "call-1", arguments)
	decision, err := recorder.CheckToolPermission(context.Background(), request)
	require.NoError(t, err)
	_, err = recorder.CheckToolPermission(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Len(t, sink.decisions, 1)
	require.Equal(t, review.DecisionKindPermission, sink.decisions[0].Kind)
	require.NotContains(t, sink.decisions[0].Reason, "sk-test-super-secret-value")
	require.NoError(t, sink.decisions[0].Validate())

	filtered := tool.FilterTools(context.Background(), []tool.Tool{workspace, newTestTool("web_fetch")}, recorder.Filter())
	require.Equal(t, []tool.Tool{workspace}, filtered)
	require.Len(t, sink.decisions, 3)
	filtered = tool.FilterTools(context.Background(), []tool.Tool{workspace}, recorder.Filter())
	require.Equal(t, []tool.Tool{workspace}, filtered)
	require.Len(t, sink.decisions, 3)
	require.Equal(t, review.DecisionKindFilter, sink.decisions[1].Kind)
	require.Equal(t, review.DecisionKindFilter, sink.decisions[2].Kind)
	require.NoError(t, recorder.FilterError())
}

func TestRecordingFailureReturnsDeny(t *testing.T) {
	workspace := newTestTool("workspace_exec")
	policy, err := NewPolicy([]tool.Tool{workspace})
	require.NoError(t, err)
	sentinel := errors.New("database token=sk-test-super-secret-value-123456")
	recorder, err := NewRecordingPolicy(policy, &recordingSink{err: sentinel}, "task-1")
	require.NoError(t, err)
	arguments, err := json.Marshal(workspaceInput{Command: "go test ./...", Timeout: 120})
	require.NoError(t, err)
	decision, checkErr := recorder.CheckToolPermission(
		context.Background(), permissionRequest(workspace, "call-1", arguments),
	)
	require.Error(t, checkErr)
	require.NotContains(t, checkErr.Error(), "sk-test-super-secret-value-123456")
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
}

type testTool struct {
	declaration *tool.Declaration
}

func newTestTool(name string) *testTool {
	return &testTool{declaration: &tool.Declaration{Name: name}}
}

func (t *testTool) Declaration() *tool.Declaration {
	return t.declaration
}

func permissionRequest(candidate tool.Tool, callID string, arguments []byte) *tool.PermissionRequest {
	return &tool.PermissionRequest{
		Tool:        candidate,
		ToolName:    candidate.Declaration().Name,
		ToolCallID:  callID,
		Declaration: candidate.Declaration(),
		Arguments:   arguments,
		Metadata:    tool.MetadataOf(candidate),
	}
}

type recordingSink struct {
	decisions []review.GovernanceDecision
	err       error
}

func (s *recordingSink) RecordGovernanceDecision(
	_ context.Context,
	decision review.GovernanceDecision,
) error {
	if s.err != nil {
		return s.err
	}
	s.decisions = append(s.decisions, decision)
	return nil
}
