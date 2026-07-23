//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/toolresultround"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type perCallResultInput struct{}

type perCallResultOutput struct {
	Value string `json:"value"`
}

type perCallBarrierModel struct {
	delegate       *sequentialModel
	roundCompleted <-chan struct{}
}

func (m *perCallBarrierModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	if len(m.delegate.Requests()) > 0 {
		select {
		case <-m.roundCompleted:
		default:
			return nil, errors.New(
				"next model request started before the tool round completed",
			)
		}
	}
	return m.delegate.GenerateContent(ctx, req)
}

func (m *perCallBarrierModel) Info() model.Info {
	return m.delegate.Info()
}

func TestRunner_PerToolCallResultEventsEndToEnd(t *testing.T) {
	const (
		appName     = "per-tool-call-result-events"
		userID      = "user"
		sessionID   = "session"
		finalAnswer = "all tools completed"
	)

	slowStarted := make(chan struct{})
	slowCompleted := make(chan struct{})
	releaseSlow := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseSlow:
		default:
			close(releaseSlow)
		}
	})

	slowTool := function.NewFunctionTool(
		func(
			ctx context.Context,
			_ perCallResultInput,
		) (perCallResultOutput, error) {
			close(slowStarted)
			defer close(slowCompleted)
			select {
			case <-releaseSlow:
				return perCallResultOutput{Value: "slow"}, nil
			case <-ctx.Done():
				return perCallResultOutput{}, ctx.Err()
			}
		},
		function.WithName("slow"),
		function.WithDescription("Returns after the test releases it."),
	)
	fastTool := function.NewFunctionTool(
		func(
			ctx context.Context,
			_ perCallResultInput,
		) (perCallResultOutput, error) {
			select {
			case <-slowStarted:
				return perCallResultOutput{Value: "fast"}, nil
			case <-ctx.Done():
				return perCallResultOutput{}, ctx.Err()
			}
		},
		function.WithName("fast"),
		function.WithDescription("Returns after the slow tool starts."),
	)

	modelStub := &sequentialModel{
		name: "per-tool-call-result-model",
		responses: []*model.Response{
			{
				ID:   "tool-call-response",
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								ID:   "call-slow",
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name:      "slow",
									Arguments: []byte(`{}`),
								},
							},
							{
								ID:   "call-fast",
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name:      "fast",
									Arguments: []byte(`{}`),
								},
							},
						},
					},
				}},
			},
			{
				ID:   "final-response",
				Done: true,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage(finalAnswer),
				}},
			},
		},
	}
	ag := llmagent.New(
		"per-tool-call-result-agent",
		llmagent.WithModel(&perCallBarrierModel{
			delegate:       modelStub,
			roundCompleted: slowCompleted,
		}),
		llmagent.WithTools([]tool.Tool{slowTool, fastTool}),
		llmagent.WithEnableParallelTools(true),
	)
	sessionService := sessioninmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, sessionService.Close())
	})
	stripRoundMarker := &testPlugin{
		name: "strip-tool-result-round-marker",
		reg: func(registry *plugin.Registry) {
			registry.OnEvent(func(
				_ context.Context,
				_ *agent.Invocation,
				evt *event.Event,
			) (*event.Event, error) {
				if !toolresultround.HasMarker(evt) {
					return evt, nil
				}
				replacement := *evt
				replacement.Extensions = nil
				return &replacement, nil
			})
		},
	}
	r := NewRunner(
		appName,
		ag,
		WithSessionService(sessionService),
		WithPlugins(stripRoundMarker),
	)
	t.Cleanup(func() {
		require.NoError(t, r.Close())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	eventCh, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage("run both tools"),
		agent.WithToolResultEventPerToolCallEnabled(true),
	)
	require.NoError(t, err)

	var events []*event.Event
	for {
		select {
		case evt, ok := <-eventCh:
			require.True(t, ok, "event stream closed before the first tool result")
			events = append(events, evt)
			if evt != nil && evt.IsToolResultResponse() {
				require.Equal(t, []string{"call-fast"}, evt.GetToolResultIDs())
				require.Len(t, evt.Response.Choices, 1)
				goto firstResultReceived
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for the first tool result")
		}
	}

firstResultReceived:
	require.Len(t, modelStub.Requests(), 1)
	close(releaseSlow)
	events = append(events, collectRunnerEvents(eventCh)...)

	resultEvents := perCallToolResultEvents(events)
	require.Len(t, resultEvents, 2)
	require.Equal(t, []string{"call-fast"}, resultEvents[0].GetToolResultIDs())
	require.Equal(t, []string{"call-slow"}, resultEvents[1].GetToolResultIDs())
	require.Len(t, resultEvents[0].Response.Choices, 1)
	require.Len(t, resultEvents[1].Response.Choices, 1)
	require.True(t, hasAssistantContent(events, finalAnswer))

	sess, err := sessionService.GetSession(
		context.Background(),
		session.Key{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	persistedResults := perCallPersistedToolResultEvents(sess.GetEvents())
	require.Len(t, persistedResults, 2)
	require.Equal(t, []string{"call-fast"}, persistedResults[0].GetToolResultIDs())
	require.Equal(t, []string{"call-slow"}, persistedResults[1].GetToolResultIDs())
	require.True(t, toolresultround.HasMarker(&persistedResults[0]))
	require.True(t, toolresultround.IsIncomplete(&persistedResults[0]))
	require.True(t, toolresultround.HasMarker(&persistedResults[1]))
	require.False(t, toolresultround.IsIncomplete(&persistedResults[1]))

	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	require.Equal(
		t,
		[]string{"call-slow", "call-fast"},
		toolResultMessageIDs(requests[1].messages),
	)
}

func perCallToolResultEvents(events []*event.Event) []*event.Event {
	var results []*event.Event
	for _, evt := range events {
		if evt != nil && evt.IsToolResultResponse() {
			results = append(results, evt)
		}
	}
	return results
}

func perCallPersistedToolResultEvents(events []event.Event) []event.Event {
	var results []event.Event
	for i := range events {
		if events[i].IsToolResultResponse() {
			results = append(results, events[i])
		}
	}
	return results
}

func toolResultMessageIDs(messages []model.Message) []string {
	var ids []string
	for _, message := range messages {
		if message.Role == model.RoleTool {
			ids = append(ids, message.ToolID)
		}
	}
	return ids
}

func hasAssistantContent(events []*event.Event, content string) bool {
	for _, evt := range events {
		if evt == nil || evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Role == model.RoleAssistant &&
				choice.Message.Content == content {
				return true
			}
		}
	}
	return false
}
