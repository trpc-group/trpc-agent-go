//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolctx "trpc.group/trpc-go/trpc-agent-go/tool/context"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestToolCallsRequireSerialExecutionForPensieveMutators(t *testing.T) {
	safe := function.NewFunctionTool(
		func(context.Context, struct{}) (string, error) { return "ok", nil },
		function.WithName("safe_tool"),
	)
	tools := map[string]tool.Tool{
		"safe_tool":      safe,
		"note":           toolctx.NewNoteTool(),
		"delete_context": toolctx.NewDeleteContextTool(),
		"list_context":   toolctx.NewListContextTool(),
	}
	calls := []model.ToolCall{
		{Function: model.FunctionDefinitionParam{Name: "safe_tool"}},
		{Function: model.FunctionDefinitionParam{Name: "list_context"}},
	}
	if toolCallsRequireSerialExecution(calls, tools) {
		t.Fatal("safe tools alone must stay parallel-eligible")
	}
	calls = append(calls, model.ToolCall{
		Function: model.FunctionDefinitionParam{Name: "note"},
	})
	if !toolCallsRequireSerialExecution(calls, tools) {
		t.Fatal("note must force the serial path")
	}
	if !toolCallsRequireSerialExecution([]model.ToolCall{{
		Function: model.FunctionDefinitionParam{Name: "delete_context"},
	}}, tools) {
		t.Fatal("delete_context must force the serial path")
	}
}
