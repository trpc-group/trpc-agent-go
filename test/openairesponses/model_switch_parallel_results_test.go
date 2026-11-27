//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestModelSwitchWithReorderedToolResults(t *testing.T) {
	const firstCall = `{"type":"function_call","id":"fc_first","call_id":"call_first","status":"completed","name":"lookup","arguments":"{\"key\":\"first\"}"}`
	const secondCall = `{"type":"function_call","id":"fc_second","call_id":"call_second","status":"completed","name":"lookup","arguments":"{\"key\":\"second\"}"}`
	var firstRuns, secondRuns atomic.Int32
	f := newSwitchFixtureWithTools(t, nil, func(f *switchFixture) []tool.Tool {
		lookup := function.NewFunctionTool(func(_ context.Context, input struct {
			Key string `json:"key"`
		}) (string, error) {
			f.toolRuns.Add(1)
			switch input.Key {
			case "first":
				firstRuns.Add(1)
			case "second":
				secondRuns.Add(1)
			default:
				return "", fmt.Errorf("unexpected key %q", input.Key)
			}
			return "result-for-" + input.Key, nil
		}, function.WithName("lookup"), function.WithDescription("Look up a key."))
		return []tool.Tool{lookup}
	},
		switchResponsesReply(switchReasoningItem+","+firstCall+","+secondCall),
		switchTextReply("msg_source", "source answer"),
		switchChatReply(`{"role":"assistant","content":"chat answer"}`, "stop"),
		switchTextReply("msg_back", "back answer"),
	)

	const sourceSession = "two-tools-original"
	const reorderedSession = "two-tools-reordered"
	require.Empty(t, f.run(t, sourceSession, switchSource, model.NewUserMessage("look up both")))
	require.EqualValues(t, 1, firstRuns.Load())
	require.EqualValues(t, 1, secondRuns.Load())

	ctx := context.Background()
	key := session.Key{AppName: "switch", UserID: "user", SessionID: sourceSession}
	saved, err := f.sessions.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, saved)
	key.SessionID = reorderedSession
	reordered, err := f.sessions.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// Runner normally merges tool results in call order. Store a copy with the
	// merged result choices reversed so that the next run loads a genuine
	// persisted out-of-order history. The content processor may reorder the
	// choices again, and the checks below therefore match every result by ID.
	var reversed bool
	for _, original := range saved.Events {
		cloned := original.Clone()
		if cloned.IsToolResultResponse() && len(cloned.Choices) == 2 {
			require.Equal(t, "call_first", cloned.Choices[0].Message.ToolID)
			require.Equal(t, "call_second", cloned.Choices[1].Message.ToolID)
			cloned.Choices[0], cloned.Choices[1] = cloned.Choices[1], cloned.Choices[0]
			reversed = true
		}
		require.NoError(t, f.sessions.AppendEvent(ctx, reordered, cloned))
	}
	require.True(t, reversed, "both completed tool results must belong to one event")
	persisted, err := f.sessions.GetSession(ctx, key)
	require.NoError(t, err)
	var persistedOrder []string
	persistedResults := make(map[string]string)
	for _, evt := range persisted.Events {
		if evt.IsToolResultResponse() {
			for _, choice := range evt.Choices {
				persistedOrder = append(persistedOrder, choice.Message.ToolID)
				persistedResults[choice.Message.ToolID] = choice.Message.Content
			}
		}
	}
	require.Equal(t, []string{"call_second", "call_first"}, persistedOrder)
	require.Contains(t, persistedResults["call_first"], "result-for-first")
	require.Contains(t, persistedResults["call_second"], "result-for-second")

	require.Empty(t, f.run(t, reorderedSession, switchChat, model.NewUserMessage("continue in chat")))
	require.Empty(t, f.run(t, reorderedSession, switchSource, model.NewUserMessage("return to responses")))
	require.EqualValues(t, 1, firstRuns.Load(), "the first completed call must not run again")
	require.EqualValues(t, 1, secondRuns.Load(), "the second completed call must not run again")
	require.EqualValues(t, 2, f.toolRuns.Load())

	requests := f.snapshot()
	require.Len(t, requests, 4)
	require.Equal(t, "/chat/completions", requests[2].Path)
	require.Equal(t, switchChat, requests[2].Model)
	var chatMessages []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"tool_call_id"`
		ToolCalls  []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	require.NoError(t, json.Unmarshal(requests[2].Messages, &chatMessages))
	chatCalls := make(map[string]string)
	chatResults := make(map[string]string)
	for _, msg := range chatMessages {
		for _, call := range msg.ToolCalls {
			require.Equal(t, "lookup", call.Function.Name)
			require.NotContains(t, chatCalls, call.ID)
			chatCalls[call.ID] = call.Function.Arguments
		}
		if msg.Role == "tool" {
			require.NotContains(t, chatResults, msg.ToolCallID)
			chatResults[msg.ToolCallID] = string(msg.Content)
		}
	}
	require.Len(t, chatCalls, 2)
	require.Len(t, chatResults, 2)
	require.JSONEq(t, `{"key":"first"}`, chatCalls["call_first"])
	require.JSONEq(t, `{"key":"second"}`, chatCalls["call_second"])
	require.Contains(t, chatResults["call_first"], "result-for-first")
	require.Contains(t, chatResults["call_second"], "result-for-second")
	require.NotContains(t, string(requests[2].Messages), "opaque-switch-reasoning")

	require.Equal(t, "/responses", requests[3].Path)
	require.Equal(t, switchSource, requests[3].Model)
	var input []json.RawMessage
	require.NoError(t, json.Unmarshal(requests[3].Input, &input))
	type responseItem struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
	}
	responseCalls := make(map[string]string)
	responseResults := make(map[string]string)
	var reasoningCount int
	for _, raw := range input {
		var item responseItem
		require.NoError(t, json.Unmarshal(raw, &item))
		switch item.Type {
		case "reasoning":
			if item.ID == "rs_switch" {
				reasoningCount++
				require.JSONEq(t, switchReasoningItem, string(raw))
			}
		case "function_call":
			require.Equal(t, "lookup", item.Name)
			require.NotContains(t, responseCalls, item.CallID)
			responseCalls[item.CallID] = item.Arguments
			switch item.CallID {
			case "call_first":
				require.JSONEq(t, firstCall, string(raw))
			case "call_second":
				require.JSONEq(t, secondCall, string(raw))
			}
		case "function_call_output":
			require.NotContains(t, responseResults, item.CallID)
			responseResults[item.CallID] = string(item.Output)
		}
	}
	require.Len(t, responseCalls, 2)
	require.Len(t, responseResults, 2)
	require.JSONEq(t, `{"key":"first"}`, responseCalls["call_first"])
	require.JSONEq(t, `{"key":"second"}`, responseCalls["call_second"])
	require.Contains(t, responseResults["call_first"], "result-for-first")
	require.Contains(t, responseResults["call_second"], "result-for-second")
	require.Equal(t, 1, reasoningCount)
	require.Contains(t, string(requests[3].Input), "opaque-switch-reasoning")
}
