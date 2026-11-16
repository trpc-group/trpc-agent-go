//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

type deferredPermissionTestTool struct {
	requests []tool.PermissionRequest
	calls    int
}

func (*deferredPermissionTestTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "delete_file"}
}

func (t *deferredPermissionTestTool) CheckPermission(
	_ context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	t.requests = append(t.requests, *req)
	return tool.DenyPermission("write access is disabled"), nil
}

func (t *deferredPermissionTestTool) Call(context.Context, []byte) (any, error) {
	t.calls++
	return "executed", nil
}

type deferredPermissionTestToolSet struct {
	member tool.Tool
}

func (s *deferredPermissionTestToolSet) Tools(context.Context) []tool.Tool {
	return []tool.Tool{s.member}
}

func (*deferredPermissionTestToolSet) Name() string { return "files" }
func (*deferredPermissionTestToolSet) Close() error { return nil }

func TestDeferredToolPermissionIsEnforcedByToolsNode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		toolName  string
		inToolSet bool
	}{
		{name: "standalone", toolName: "delete_file"},
		{name: "named tool set", toolName: "files_delete_file", inToolSet: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &deferredPermissionTestTool{}
			var node NodeFunc
			if tc.inToolSet {
				set := deferred.ToolSet(&deferredPermissionTestToolSet{member: original}, nil)
				node = NewToolsNodeFunc(nil, WithToolSets([]tool.ToolSet{set}))
			} else {
				node = NewToolsNodeFunc(map[string]tool.Tool{tc.toolName: deferred.Tool(original, nil)})
			}

			const callID = "call-delete"
			const arguments = `{"path":"unsafe"}`
			state := State{StateKeyMessages: []model.Message{{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   callID,
					Function: model.FunctionDefinitionParam{
						Name:      tc.toolName,
						Arguments: []byte(arguments),
					},
				}},
			}}}
			result, err := node(context.Background(), state)
			require.NoError(t, err)
			require.Zero(t, original.calls, "denied tool must not execute")
			require.Len(t, original.requests, 1)
			req := original.requests[0]
			require.Equal(t, tc.toolName, req.ToolName)
			require.Equal(t, tc.toolName, req.Declaration.Name)
			require.Equal(t, callID, req.ToolCallID)
			require.JSONEq(t, arguments, string(req.Arguments))

			messages := result.(State)[StateKeyMessages].([]model.Message)
			require.Len(t, messages, 1)
			var denial tool.PermissionResult
			require.NoError(t, json.Unmarshal([]byte(messages[0].Content), &denial))
			require.Equal(t, tool.PermissionResult{
				Status: tool.PermissionResultStatusDenied,
				Tool:   tc.toolName,
				Reason: "write access is disabled",
			}, denial)
		})
	}
}
