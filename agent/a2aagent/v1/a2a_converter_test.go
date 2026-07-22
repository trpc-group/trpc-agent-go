//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2aagent

import (
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
)

func TestConvertToEventsUsesCompletedStatusMessage(t *testing.T) {
	stateDelta := map[string][]byte{"state-key": []byte(`"value"`)}
	message := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]*protocol.Part{protocol.NewTextPart("hello")},
	)
	message.Metadata = map[string]any{
		ia2a.MessageMetadataStateDeltaKey: ia2a.EncodeStateDeltaMetadata(stateDelta),
	}
	task := protocol.NewTask("task", "context")
	task.Status = protocol.TaskStatus{
		State:   protocol.TaskStateCompleted,
		Message: &message,
	}
	task.Metadata = map[string]any{
		ia2a.MessageMetadataResponseIDKey: "response",
	}

	converter := &defaultA2AEventConverter{}
	events, err := converter.ConvertToEvents(
		*protocol.NewSendMessageResponseTask(task),
		"remote",
		&agent.Invocation{InvocationID: "invocation"},
	)
	if err != nil {
		t.Fatalf("ConvertToEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	evt := events[0]
	if evt.Response == nil || len(evt.Response.Choices) != 1 {
		t.Fatalf("response = %#v, want one choice", evt.Response)
	}
	if got := evt.Response.Choices[0].Message.Content; got != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}
	if evt.Response.ID != "response" {
		t.Fatalf("response ID = %q, want response", evt.Response.ID)
	}
	if got := string(evt.StateDelta["state-key"]); got != `"value"` {
		t.Fatalf("state delta = %q, want %q", got, `"value"`)
	}
	if !evt.Done || evt.IsPartial {
		t.Fatalf("event finality = (done=%v partial=%v), want final", evt.Done, evt.IsPartial)
	}
}

func TestConvertStreamingFinalArtifactPreservesMetadataOnly(t *testing.T) {
	lastChunk := true
	update := &protocol.TaskArtifactUpdateEvent{
		TaskID:    "task",
		ContextID: "context",
		Artifact: protocol.Artifact{
			ArtifactID: "artifact",
			Parts:      []*protocol.Part{protocol.NewTextPart("already streamed")},
			Metadata: map[string]any{
				ia2a.MessageMetadataStateDeltaKey: ia2a.EncodeStateDeltaMetadata(
					map[string][]byte{"state-key": []byte(`"value"`)},
				),
			},
		},
		Metadata: map[string]any{
			ia2a.MessageMetadataResponseIDKey: "response",
		},
		LastChunk: &lastChunk,
	}

	converter := &defaultA2AEventConverter{}
	events, err := converter.ConvertStreamingToEvents(
		protocol.NewStreamResponseArtifactUpdate(update),
		"remote",
		&agent.Invocation{InvocationID: "invocation"},
	)
	if err != nil {
		t.Fatalf("ConvertStreamingToEvents failed: %v", err)
	}
	assertMetadataOnlyStreamingEvent(t, events)
}

func TestConvertStreamingCompletedStatusPreservesMetadataOnly(t *testing.T) {
	message := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]*protocol.Part{protocol.NewTextPart("already streamed")},
	)
	message.Metadata = map[string]any{
		ia2a.MessageMetadataStateDeltaKey: ia2a.EncodeStateDeltaMetadata(
			map[string][]byte{"state-key": []byte(`"value"`)},
		),
	}
	update := protocol.NewTaskStatusUpdateEvent(
		"task",
		"context",
		protocol.TaskStatus{
			State:   protocol.TaskStateCompleted,
			Message: &message,
		},
		true,
	)
	update.Metadata = map[string]any{
		ia2a.MessageMetadataResponseIDKey: "response",
	}

	converter := &defaultA2AEventConverter{}
	events, err := converter.ConvertStreamingToEvents(
		protocol.NewStreamResponseStatusUpdate(&update),
		"remote",
		&agent.Invocation{InvocationID: "invocation"},
	)
	if err != nil {
		t.Fatalf("ConvertStreamingToEvents failed: %v", err)
	}
	assertMetadataOnlyStreamingEvent(t, events)
}

func assertMetadataOnlyStreamingEvent(t *testing.T, events []*event.Event) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	evt := events[0]
	if evt.Response == nil || len(evt.Response.Choices) != 1 {
		t.Fatalf("response = %#v, want one choice", evt.Response)
	}
	if got := evt.Response.Choices[0].Delta.Content; got != "" {
		t.Fatalf("delta content = %q, want empty metadata-only event", got)
	}
	if evt.Response.ID != "response" {
		t.Fatalf("response ID = %q, want response", evt.Response.ID)
	}
	if got := string(evt.StateDelta["state-key"]); got != `"value"` {
		t.Fatalf("state delta = %q, want %q", got, `"value"`)
	}
}
