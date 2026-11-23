//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func historyEvidence(t *testing.T, status string, native bool) (*agent.Invocation, []event.Event) {
	t.Helper()
	call := model.ToolCall{ID: "call", Type: "function", Function: model.FunctionDefinitionParam{Name: "write", Arguments: []byte(`{}`)}}
	message := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{call}}
	if native {
		message.ProviderData = &model.ProviderData{Protocol: "test", Model: "test", Payload: json.RawMessage(`{"opaque":"preserve"}`)}
	}
	source := event.NewResponseEvent("old", "agent", &model.Response{Choices: []model.Choice{{Message: message}}})
	source.RequestID = "request"
	sess := session.NewSession("app", "u", "s")
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))
	record := tooljournal.Record{Version: 1, ID: "round", SourceEventID: source.ID, Response: source.Response, Calls: []tooljournal.Call{{Tool: call, Status: status}}}
	data, err := json.Marshal(record)
	require.NoError(t, err)
	sess.SetState(tooljournal.Prefix+record.ID, data)
	return inv, []event.Event{*source}
}

func TestExecutionHistory_UnknownAndNotStarted(t *testing.T) {
	for _, status := range []string{"not_started", "dispatched", "unknown"} {
		t.Run(status, func(t *testing.T) {
			inv, events := historyEvidence(t, status, false)
			before, err := json.Marshal(events)
			require.NoError(t, err)
			projected, err := completeTrackedToolRounds(events, inv)
			require.NoError(t, err)
			require.Len(t, projected, 2)
			content := projected[1].Choices[0].Message.Content
			require.Contains(t, content, "[framework]")
			if status == "not_started" {
				require.Contains(t, content, "not dispatched")
			} else {
				require.Contains(t, content, "Side effects may have occurred")
			}
			after, err := json.Marshal(events)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after))
			// A filtered-out source must not be resurrected from retained session state.
			projected, err = completeTrackedToolRounds(nil, inv)
			require.NoError(t, err)
			require.Empty(t, projected)
		})
	}
}

func TestExecutionHistory_NativeRequiresExplicitRecovery(t *testing.T) {
	inv, events := historyEvidence(t, "unknown", true)
	_, err := completeTrackedToolRounds(events, inv)
	require.ErrorContains(t, err, "incomplete native tool history")
	require.JSONEq(t, `{"opaque":"preserve"}`, string(events[0].Choices[0].Message.ProviderData.Payload))
	result := event.NewResponseEvent(events[0].InvocationID, "agent", &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleTool, ToolID: "call", Content: "actual"}}}})
	result.RequestID = events[0].RequestID
	projected, err := completeTrackedToolRounds(append(events, *result), inv)
	require.NoError(t, err)
	require.Len(t, projected, 2)
}
