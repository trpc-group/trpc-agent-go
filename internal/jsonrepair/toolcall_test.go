//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonrepair

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// withNoopErrorfContext temporarily disables error logs during the callback.
func withNoopErrorfContext(t *testing.T, fn func()) {
	t.Helper()
	prev := log.ErrorfContext
	log.ErrorfContext = func(_ context.Context, _ string, _ ...any) {}
	t.Cleanup(func() { log.ErrorfContext = prev })
	fn()
}

// TestIsToolCallArgumentsJSONRepairEnabled_ReturnsExpected verifies the helper handles nil and boolean pointers.
func TestIsToolCallArgumentsJSONRepairEnabled_ReturnsExpected(t *testing.T) {
	require.False(t, IsToolCallArgumentsJSONRepairEnabled(nil))

	enabled := true
	require.True(t, IsToolCallArgumentsJSONRepairEnabled(&agent.Invocation{
		RunOptions: agent.RunOptions{ToolCallArgumentsJSONRepairEnabled: &enabled},
	}))

	disabled := false
	require.False(t, IsToolCallArgumentsJSONRepairEnabled(&agent.Invocation{
		RunOptions: agent.RunOptions{ToolCallArgumentsJSONRepairEnabled: &disabled},
	}))
	require.False(t, IsToolCallArgumentsJSONRepairEnabled(&agent.Invocation{}))
}

// TestRepairToolCallArguments_ReturnsOriginalForValidJSON verifies that valid JSON arguments are returned unchanged.
func TestRepairToolCallArguments_ReturnsOriginalForValidJSON(t *testing.T) {
	ctx := context.Background()
	arguments := []byte("{\"a\":1}")

	repaired := RepairToolCallArguments(ctx, "test_tool", arguments)
	require.Equal(t, arguments, repaired)
}

// TestRepairToolCallArguments_RepairsInvalidJSON verifies that invalid JSON arguments are repaired when possible.
func TestRepairToolCallArguments_RepairsInvalidJSON(t *testing.T) {
	ctx := context.Background()
	arguments := []byte("{a:2}")

	repaired := RepairToolCallArguments(ctx, "test_tool", arguments)
	require.Equal(t, "{\"a\":2}", string(repaired))
}

// TestRepairToolCallArguments_ReturnsOriginalOnRepairError verifies that arguments are returned unchanged when repair fails.
func TestRepairToolCallArguments_ReturnsOriginalOnRepairError(t *testing.T) {
	withNoopErrorfContext(t, func() {
		ctx := context.Background()
		arguments := []byte("callback {}")

		repaired := RepairToolCallArguments(ctx, "test_tool", arguments)
		require.Equal(t, arguments, repaired)
	})
}

// TestRepairToolCallArguments_ReturnsOriginalWhenRepairedInvalidJSON verifies invalid repair output does not replace arguments.
func TestRepairToolCallArguments_ReturnsOriginalWhenRepairedInvalidJSON(t *testing.T) {
	prev := log.ErrorfContext
	var calls int
	var gotFormat string
	var gotArgs []any
	log.ErrorfContext = func(_ context.Context, format string, args ...any) {
		calls++
		gotFormat = format
		gotArgs = args
	}
	t.Cleanup(func() { log.ErrorfContext = prev })

	ctx := context.Background()
	arguments := []byte("callback(")

	repaired := RepairToolCallArguments(ctx, "test_tool", arguments)
	require.Equal(t, arguments, repaired)
	require.Equal(t, 1, calls)
	require.Equal(t, "Tool call arguments JSON repair produced invalid JSON for %s", gotFormat)
	require.Len(t, gotArgs, 1)
	require.Equal(t, "test_tool", gotArgs[0])
}

// TestRepairToolCallArgumentsInPlace_RepairsArguments verifies that tool call arguments are repaired in place.
func TestRepairToolCallArgumentsInPlace_RepairsArguments(t *testing.T) {
	ctx := context.Background()
	toolCall := &model.ToolCall{
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "test_tool",
			Arguments: []byte("{a:2}"),
		},
	}

	RepairToolCallArgumentsInPlace(ctx, toolCall)
	require.Equal(t, "{\"a\":2}", string(toolCall.Function.Arguments))
}

// TestRepairToolCallsArgumentsInPlace_RepairsSlice verifies that a slice of tool calls is repaired in place.
func TestRepairToolCallsArgumentsInPlace_RepairsSlice(t *testing.T) {
	ctx := context.Background()
	toolCalls := []model.ToolCall{
		{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      "tool_1",
				Arguments: []byte("{a:2}"),
			},
		},
		{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      "tool_2",
				Arguments: []byte("{\"b\":3}"),
			},
		},
	}

	RepairToolCallsArgumentsInPlace(ctx, toolCalls)
	require.Equal(t, "{\"a\":2}", string(toolCalls[0].Function.Arguments))
	require.Equal(t, "{\"b\":3}", string(toolCalls[1].Function.Arguments))
}

// TestRepairResponseToolCallArgumentsInPlace_SkipsPartialResponse verifies that partial responses are not modified.
func TestRepairResponseToolCallArgumentsInPlace_SkipsPartialResponse(t *testing.T) {
	ctx := context.Background()
	response := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					ToolCalls: []model.ToolCall{
						{
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "tool_1",
								Arguments: []byte("{a:2}"),
							},
						},
					},
				},
			},
		},
	}

	RepairResponseToolCallArgumentsInPlace(ctx, response)
	require.Equal(t, "{a:2}", string(response.Choices[0].Message.ToolCalls[0].Function.Arguments))
}

// TestRepairResponseToolCallArgumentsInPlace_RepairsAllChoices verifies that tool calls are repaired for message and delta.
func TestRepairResponseToolCallArgumentsInPlace_RepairsAllChoices(t *testing.T) {
	ctx := context.Background()
	response := &model.Response{
		IsPartial: false,
		Choices: []model.Choice{
			{
				Message: model.Message{
					ToolCalls: []model.ToolCall{
						{
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "tool_1",
								Arguments: []byte("{a:2}"),
							},
						},
					},
				},
				Delta: model.Message{
					ToolCalls: []model.ToolCall{
						{
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "tool_2",
								Arguments: []byte("{c:4}"),
							},
						},
					},
				},
			},
		},
	}

	RepairResponseToolCallArgumentsInPlace(ctx, response)
	require.Equal(t, "{\"a\":2}", string(response.Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.Equal(t, "{\"c\":4}", string(response.Choices[0].Delta.ToolCalls[0].Function.Arguments))
}

// TestChooseToolCallArguments_ReturnsOriginalForInvalidOutput verifies invalid repair output does not replace arguments.
func TestChooseToolCallArguments_ReturnsOriginalForInvalidOutput(t *testing.T) {
	original := []byte("{a:2}")

	chosen, usedRepair := chooseToolCallArguments(original, []byte("not json"))
	require.Equal(t, original, chosen)
	require.False(t, usedRepair)
}

// TestRepairToolCallArgumentsInPlace_NoPanicOnNil verifies nil tool calls are ignored.
func TestRepairToolCallArgumentsInPlace_NoPanicOnNil(t *testing.T) {
	require.NotPanics(t, func() {
		RepairToolCallArgumentsInPlace(context.Background(), nil)
	})
}
