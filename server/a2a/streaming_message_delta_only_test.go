//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	a2aclient "trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestStreamingMessageTextDeltaOnlyHTTP(t *testing.T) {
	for _, tc := range []struct {
		name        string
		options     []Option
		wantText    string
		wantThought string
	}{
		{name: "default", wantText: "hellohello world world", wantThought: "thinkthink again again"},
		{name: "enabled", options: []Option{WithStreamingMessageTextDeltaOnly(true)}, wantText: "hello world", wantThought: "think again"},
		{name: "disabled", options: []Option{WithStreamingMessageTextDeltaOnly(true), WithStreamingMessageTextDeltaOnly(false)}, wantText: "hellohello world world", wantThought: "thinkthink again again"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &mockRunner{runFunc: func(context.Context, string, string, model.Message, ...agent.RunOption) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 4)
				for i, content := range []string{"hello", "hello", " world", " world"} {
					message := model.Message{Role: model.RoleAssistant, Content: content, ReasoningContent: "think"}
					if i >= 2 {
						message.ReasoningContent = " again"
					}
					choice := model.Choice{Message: message}
					object := model.ObjectTypeChatCompletion
					if i%2 == 0 {
						choice = model.Choice{Delta: message}
						object = model.ObjectTypeChatCompletionChunk
					}
					ch <- &event.Event{Response: &model.Response{
						ID: "response", Object: object, IsPartial: i%2 == 0, Done: i == 3,
						Choices: []model.Choice{choice},
					}}
				}
				close(ch)
				return ch, nil
			}}
			card, err := NewAgentCard("delta-only-test", "", "http://localhost", true)
			require.NoError(t, err)
			opts := []Option{
				WithRunner(backend), WithAgentCard(card),
				WithStreamingEventType(StreamingEventTypeMessage),
				WithEventToA2APartMapper(func(_ context.Context, evt *event.Event) ([]protocol.Part, error) {
					if !evt.Response.IsPartial && !evt.Response.Done {
						return []protocol.Part{protocol.NewDataPart(map[string]any{"payload": "keep"})}, nil
					}
					return nil, nil
				}),
			}
			srv, err := New(append(opts, tc.options...)...)
			require.NoError(t, err)
			httpServer := httptest.NewServer(srv.Handler())
			defer httpServer.Close()
			client, err := a2aclient.NewA2AClient(httpServer.URL)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := client.StreamMessage(ctx, protocol.SendMessageParams{
				Message: protocol.NewMessage(protocol.MessageRoleUser, []protocol.Part{protocol.NewTextPart("hello")}),
			})
			require.NoError(t, err)
			var text, thought strings.Builder
			var completions, dataParts int
			var states []protocol.TaskState
			for evt := range stream {
				switch result := evt.Result.(type) {
				case *protocol.Message:
					require.Equal(t, "response", result.Metadata["llm_response_id"])
					if result.Metadata["object_type"] == model.ObjectTypeChatCompletion {
						completions++
					}
					for _, part := range result.Parts {
						switch part := part.(type) {
						case *protocol.TextPart:
							if part.Metadata["thought"] == true {
								thought.WriteString(part.Text)
							} else {
								text.WriteString(part.Text)
							}
						case *protocol.DataPart:
							require.Equal(t, map[string]any{"payload": "keep"}, part.Data)
							dataParts++
						}
					}
				case *protocol.TaskStatusUpdateEvent:
					states = append(states, result.Status.State)
				}
			}
			require.NoError(t, ctx.Err())
			require.Equal(t, tc.wantText, text.String())
			require.Equal(t, tc.wantThought, thought.String())
			require.Equal(t, 2, completions, "completion metadata must remain on the wire")
			require.Equal(t, 1, dataParts)
			require.Equal(t, []protocol.TaskState{protocol.TaskStateSubmitted, protocol.TaskStateCompleted}, states)
		})
	}
}

func TestStreamingMessageTextDeltaOnlyConversionBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		enabled   bool
		eventType StreamingEventType
		partial   bool
		delta     string
		want      string
	}{
		{name: "complete-only default", eventType: StreamingEventTypeMessage, want: "complete"},
		{name: "complete-only delta mode", enabled: true, eventType: StreamingEventTypeMessage},
		{name: "non-partial delta retained", enabled: true, eventType: StreamingEventTypeMessage, delta: "delta", want: "delta"},
		{name: "partial delta retained", enabled: true, eventType: StreamingEventTypeMessage, partial: true, delta: "delta", want: "delta"},
		{name: "artifact snapshot unaffected", enabled: true, want: "complete"},
		{name: "artifact delta unaffected", enabled: true, partial: true, delta: "delta", want: "delta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			converter := &defaultEventToA2AMessage{
				streamingEventType: tc.eventType, streamingMessageTextDeltaOnly: tc.enabled,
			}
			evt := &event.Event{Response: &model.Response{
				ID: "response", Object: model.ObjectTypeChatCompletion, IsPartial: tc.partial,
				Choices: []model.Choice{{Message: model.NewAssistantMessage("complete"), Delta: model.Message{Content: tc.delta}}},
			}}
			result, err := converter.ConvertStreamingToA2AMessage(ctx, evt, EventToA2AStreamingOptions{})
			require.NoError(t, err)
			var parts []protocol.Part
			switch result := result.(type) {
			case *protocol.Message:
				parts = result.Parts
				require.Equal(t, "response", result.Metadata["llm_response_id"])
			case *protocol.TaskArtifactUpdateEvent:
				parts = result.Artifact.Parts
				require.NotNil(t, result.Append)
				require.Equal(t, tc.partial, *result.Append)
			default:
				t.Fatalf("unexpected streaming result %T", result)
			}
			if tc.want == "" {
				require.Empty(t, parts)
			} else {
				require.Equal(t, []protocol.Part{protocol.NewTextPart(tc.want)}, parts)
			}
			unary, err := converter.ConvertToA2AMessage(ctx, evt, EventToA2AUnaryOptions{})
			require.NoError(t, err)
			require.Equal(t, []protocol.Part{protocol.NewTextPart("complete")}, unary.(*protocol.Message).Parts)
		})
	}
}

func TestStreamingMessageTextDeltaOnlyOmitsIDOnlyEvent(t *testing.T) {
	converter := &defaultEventToA2AMessage{
		streamingEventType: StreamingEventTypeMessage, streamingMessageTextDeltaOnly: true,
	}
	evt := &event.Event{Response: &model.Response{
		ID:      "response",
		Choices: []model.Choice{{Message: model.NewAssistantMessage("complete")}},
	}}
	result, err := converter.ConvertStreamingToA2AMessage(context.Background(), evt, EventToA2AStreamingOptions{})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestStreamingMessageTextDeltaOnlyNonPartialReasoning(t *testing.T) {
	converter := &defaultEventToA2AMessage{
		streamingEventType: StreamingEventTypeMessage, streamingMessageTextDeltaOnly: true,
	}
	evt := &event.Event{Response: &model.Response{
		ID: "response", Object: model.ObjectTypeChatCompletion, IsPartial: false,
		Choices: []model.Choice{{
			Delta:   model.Message{ReasoningContent: "delta thought"},
			Message: model.Message{Content: "complete", ReasoningContent: "complete thought"},
		}},
	}}
	result, err := converter.ConvertStreamingToA2AMessage(context.Background(), evt, EventToA2AStreamingOptions{})
	require.NoError(t, err)
	msg, ok := result.(*protocol.Message)
	require.True(t, ok)
	require.Len(t, msg.Parts, 1)
	part, ok := msg.Parts[0].(protocol.TextPart)
	require.True(t, ok)
	require.Equal(t, "delta thought", part.Text)
	require.Equal(t, true, part.Metadata["thought"])
}

func TestStreamingMessageTextDeltaOnlyCustomConverter(t *testing.T) {
	backend := &mockRunner{runFunc: func(context.Context, string, string, model.Message, ...agent.RunOption) (<-chan *event.Event, error) {
		ch := make(chan *event.Event, 1)
		ch <- &event.Event{Response: &model.Response{
			ID: "custom-response", Object: model.ObjectTypeChatCompletion, Done: true,
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "complete reply"}}},
		}}
		close(ch)
		return ch, nil
	}}
	custom := &mockEventToA2AConverter{
		convertStreamingToA2AMessageFunc: func(_ context.Context, evt *event.Event, opts EventToA2AStreamingOptions) (protocol.StreamingMessageResult, error) {
			msg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
				protocol.NewTextPart(evt.Response.Choices[0].Message.Content),
			})
			msg.ContextID = &opts.CtxID
			msg.TaskID = &opts.TaskID
			msg.Metadata = map[string]any{
				"object_type": evt.Response.Object, "llm_response_id": evt.Response.ID,
			}
			return &msg, nil
		},
	}
	card, err := NewAgentCard("custom-converter-test", "", "http://localhost", true)
	require.NoError(t, err)
	srv, err := New(
		WithRunner(backend), WithAgentCard(card),
		WithStreamingEventType(StreamingEventTypeMessage),
		WithStreamingMessageTextDeltaOnly(true), WithEventToA2AConverter(custom),
	)
	require.NoError(t, err)
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	client, err := a2aclient.NewA2AClient(httpServer.URL)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.StreamMessage(ctx, protocol.SendMessageParams{
		Message: protocol.NewMessage(protocol.MessageRoleUser, []protocol.Part{protocol.NewTextPart("hello")}),
	})
	require.NoError(t, err)
	var messages int
	var states []protocol.TaskState
	for evt := range stream {
		switch result := evt.Result.(type) {
		case *protocol.Message:
			messages++
			require.Len(t, result.Parts, 1)
			part, ok := result.Parts[0].(*protocol.TextPart)
			require.True(t, ok)
			require.Equal(t, "complete reply", part.Text)
			require.Equal(t, model.ObjectTypeChatCompletion, result.Metadata["object_type"])
			require.Equal(t, "custom-response", result.Metadata["llm_response_id"])
		case *protocol.TaskStatusUpdateEvent:
			states = append(states, result.Status.State)
		}
	}
	require.NoError(t, ctx.Err())
	require.Equal(t, 1, messages)
	require.Contains(t, states, protocol.TaskStateCompleted)
}
