//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2aagent

import (
	"bytes"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/model"
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

func TestConvertStreamingFinalOnlyArtifactPreservesContent(t *testing.T) {
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
	assertStreamingEventContentAndMetadata(t, events, "already streamed")
}

func TestConvertStreamingCompletedStatusPreservesMessage(t *testing.T) {
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
	assertStreamingEventContentAndMetadata(t, events, "already streamed")
}

func assertStreamingEventContentAndMetadata(
	t *testing.T,
	events []*event.Event,
	wantContent string,
) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	evt := events[0]
	if evt.Response == nil || len(evt.Response.Choices) != 1 {
		t.Fatalf("response = %#v, want one choice", evt.Response)
	}
	if got := evt.Response.Choices[0].Delta.Content; got != wantContent {
		t.Fatalf("delta content = %q, want %q", got, wantContent)
	}
	if evt.Response.ID != "response" {
		t.Fatalf("response ID = %q, want response", evt.Response.ID)
	}
	if got := string(evt.StateDelta["state-key"]); got != `"value"` {
		t.Fatalf("state delta = %q, want %q", got, `"value"`)
	}
}

func TestConvertResponseFileParts(t *testing.T) {
	image := protocol.NewRawPart([]byte("image"), "image/png")
	audio := protocol.NewRawPart([]byte("audio"), "audio/wav")
	file := protocol.NewRawPart([]byte("file"), "application/pdf")
	file.Filename = "report.pdf"
	imageURL := protocol.NewURLPart("https://example.com/image.png", "image/png")
	fileURL := protocol.NewFilePart(
		"https://example.com/report.pdf",
		"report.pdf",
		"application/pdf",
	)
	message := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]*protocol.Part{image, audio, file, imageURL, fileURL},
	)

	converter := &defaultA2AEventConverter{}
	events, err := converter.ConvertToEvents(
		*protocol.NewSendMessageResponseMessage(&message),
		"remote",
		&agent.Invocation{InvocationID: "invocation"},
	)
	if err != nil {
		t.Fatalf("ConvertToEvents failed: %v", err)
	}
	if len(events) != 1 || events[0].Response == nil ||
		len(events[0].Response.Choices) != 1 {
		t.Fatalf("events = %#v, want one response choice", events)
	}
	parts := events[0].Response.Choices[0].Message.ContentParts
	if len(parts) != 5 {
		t.Fatalf("content part count = %d, want 5", len(parts))
	}
	if parts[0].Type != model.ContentTypeImage || parts[0].Image == nil ||
		!bytes.Equal(parts[0].Image.Data, []byte("image")) {
		t.Fatalf("image part = %#v", parts[0])
	}
	if parts[1].Type != model.ContentTypeAudio || parts[1].Audio == nil ||
		!bytes.Equal(parts[1].Audio.Data, []byte("audio")) {
		t.Fatalf("audio part = %#v", parts[1])
	}
	if parts[2].Type != model.ContentTypeFile || parts[2].File == nil ||
		parts[2].File.Name != "report.pdf" ||
		!bytes.Equal(parts[2].File.Data, []byte("file")) {
		t.Fatalf("raw file part = %#v", parts[2])
	}
	if parts[3].Type != model.ContentTypeImage || parts[3].Image == nil ||
		parts[3].Image.URL != "https://example.com/image.png" {
		t.Fatalf("image URL part = %#v", parts[3])
	}
	if parts[4].Type != model.ContentTypeFile || parts[4].File == nil ||
		parts[4].File.URL != "https://example.com/report.pdf" {
		t.Fatalf("file URL part = %#v", parts[4])
	}

	streamEvents, err := converter.ConvertStreamingToEvents(
		protocol.NewStreamResponseMessage(&message),
		"remote",
		&agent.Invocation{InvocationID: "invocation"},
	)
	if err != nil {
		t.Fatalf("ConvertStreamingToEvents failed: %v", err)
	}
	if len(streamEvents) != 1 || streamEvents[0].Response == nil ||
		len(streamEvents[0].Response.Choices) != 1 {
		t.Fatalf("stream events = %#v, want one response choice", streamEvents)
	}
	if got := len(streamEvents[0].Response.Choices[0].Delta.ContentParts); got != 5 {
		t.Fatalf("stream content part count = %d, want 5", got)
	}
}

func TestConvertPackedToolRoundPreservesFinalAssistantMessage(t *testing.T) {
	toolCall := protocol.NewDataPart(map[string]any{
		ia2a.ToolCallFieldID:   "call-1",
		ia2a.ToolCallFieldName: "lookup",
		ia2a.ToolCallFieldArgs: map[string]any{"city": "Shenzhen"},
	})
	toolCall.Metadata = map[string]any{
		ia2a.DataPartMetadataTypeKey: ia2a.DataPartMetadataTypeFunctionCall,
	}
	toolResponse := protocol.NewDataPart(map[string]any{
		ia2a.ToolCallFieldID:       "call-1",
		ia2a.ToolCallFieldName:     "lookup",
		ia2a.ToolCallFieldResponse: map[string]any{"temperature": 30},
	})
	toolResponse.Metadata = map[string]any{
		ia2a.DataPartMetadataTypeKey: ia2a.DataPartMetadataTypeFunctionResp,
	}
	message := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]*protocol.Part{
			toolCall,
			toolResponse,
			protocol.NewTextPart("It is 30 degrees."),
		},
	)

	converter := &defaultA2AEventConverter{}
	events, err := converter.ConvertToEvents(
		*protocol.NewSendMessageResponseMessage(&message),
		"remote",
		&agent.Invocation{InvocationID: "invocation"},
	)
	if err != nil {
		t.Fatalf("ConvertToEvents failed: %v", err)
	}
	choices := events[0].Response.Choices
	if len(choices) != 3 {
		t.Fatalf("choice count = %d, want tool call, tool response, final message", len(choices))
	}
	if len(choices[0].Message.ToolCalls) != 1 ||
		choices[0].Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("tool call choice = %#v", choices[0])
	}
	if choices[1].Message.Role != model.RoleTool ||
		choices[1].Message.Content != `{"temperature":30}` {
		t.Fatalf("tool response choice = %#v", choices[1])
	}
	if choices[2].Message.Role != model.RoleAssistant ||
		choices[2].Message.Content != "It is 30 degrees." {
		t.Fatalf("final assistant choice = %#v", choices[2])
	}
}
