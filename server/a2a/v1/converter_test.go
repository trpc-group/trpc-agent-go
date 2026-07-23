//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestDefaultA2AMessageToAgentMessageConvertsAllPartKinds(t *testing.T) {
	image := protocol.NewRawPart([]byte("image"), "image/png")
	audio := protocol.NewRawPart([]byte("audio"), "audio/wav")
	file := protocol.NewRawPart([]byte("file"), "application/pdf")
	file.Filename = "report.pdf"
	imageURL := protocol.NewURLPart("https://example.com/image.png", "image/png")
	dataString := protocol.NewDataPart("plain")
	dataObject := protocol.NewDataPart(map[string]any{"count": 2})

	converted, err := (&defaultA2AMessageToAgentMessage{}).ConvertToAgentMessage(
		context.Background(),
		protocol.NewMessage(protocol.MessageRoleUser, []*protocol.Part{
			nil,
			protocol.NewTextPart("hello "),
			protocol.NewTextPart("world"),
			image,
			audio,
			file,
			imageURL,
			dataString,
			dataObject,
		}),
	)
	if err != nil {
		t.Fatalf("ConvertToAgentMessage failed: %v", err)
	}
	if converted.Content != "hello world" {
		t.Fatalf("content = %q, want hello world", converted.Content)
	}
	if len(converted.ContentParts) != 6 {
		t.Fatalf("content part count = %d, want 6", len(converted.ContentParts))
	}
	if converted.ContentParts[0].Type != model.ContentTypeImage ||
		converted.ContentParts[1].Type != model.ContentTypeAudio ||
		converted.ContentParts[2].Type != model.ContentTypeFile ||
		converted.ContentParts[3].Type != model.ContentTypeImage {
		t.Fatalf("file content types = %#v", converted.ContentParts[:4])
	}
	if got := *converted.ContentParts[4].Text; got != "plain" {
		t.Fatalf("string data part = %q, want plain", got)
	}
	if got := *converted.ContentParts[5].Text; got != `{"count":2}` {
		t.Fatalf("object data part = %q, want JSON", got)
	}
}

func TestDataPartTextRejectsUnsupportedValue(t *testing.T) {
	if _, err := dataPartText(func() {}); err == nil {
		t.Fatal("dataPartText succeeded for an unsupported value")
	}
}

func TestDefaultEventConverterMetadataAndFiltering(t *testing.T) {
	converter := &defaultEventToA2AMessage{adkCompatibility: true}
	part := protocol.NewDataPart(map[string]any{"value": true})
	converter.setPartTypeMetadata(part, "custom")
	if part.Metadata[ia2a.DataPartMetadataTypeKey] != "custom" ||
		part.Metadata[ia2a.GetADKMetadataKey(ia2a.DataPartMetadataTypeKey)] != "custom" {
		t.Fatalf("data part metadata = %#v", part.Metadata)
	}
	thought := protocol.NewTextPart("reasoning")
	converter.setThoughtMetadata(thought)
	if thought.Metadata[ia2a.TextPartMetadataThoughtKey] != true ||
		thought.Metadata[ia2a.GetADKMetadataKey(ia2a.TextPartMetadataThoughtKey)] != true {
		t.Fatalf("thought metadata = %#v", thought.Metadata)
	}

	if metadata := converter.buildMessageMetadata(nil); metadata != nil {
		t.Fatalf("nil event metadata = %#v, want nil", metadata)
	}
	evt := event.New("invocation", "agent",
		event.WithTag("tag"),
		event.WithStateDelta(map[string][]byte{"key": []byte(`"value"`)}),
		event.WithResponse(&model.Response{
			ID:     "response",
			Object: model.ObjectTypeChatCompletion,
		}),
	)
	metadata := converter.buildMessageMetadata(evt)
	for _, key := range []string{
		ia2a.MessageMetadataObjectTypeKey,
		ia2a.MessageMetadataTagKey,
		ia2a.MessageMetadataResponseIDKey,
		ia2a.MessageMetadataStateDeltaKey,
	} {
		if _, ok := metadata[key]; !ok {
			t.Fatalf("metadata %q missing from %#v", key, metadata)
		}
	}
	if !hasStructuredMetadata(metadata) || !hasContentfulMetadata(metadata) {
		t.Fatalf("metadata unexpectedly empty: %#v", metadata)
	}
	if hasContentfulMetadata(map[string]any{
		ia2a.MessageMetadataResponseIDKey: "response",
	}) {
		t.Fatal("response ID alone should not be contentful")
	}

	tests := []struct {
		name    string
		object  string
		allowed []string
		want    bool
	}{
		{name: "exact", object: "graph.node.start", allowed: []string{"graph.node.start"}, want: true},
		{name: "wildcard", object: "graph.node.start", allowed: []string{"*"}, want: true},
		{name: "prefix", object: "graph.node.start", allowed: []string{"graph.node.*"}, want: true},
		{name: "suffix", object: "graph.node.start", allowed: []string{"*.start"}, want: true},
		{name: "miss", object: "graph.node.start", allowed: []string{"graph.node.end"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := matchesAllowedGraphObjectType(test.object, test.allowed); got != test.want {
				t.Fatalf("matchesAllowedGraphObjectType() = %v, want %v", got, test.want)
			}
		})
	}

	if !converter.shouldEmitEvent(nil) ||
		!converter.shouldEmitEvent(&event.Event{}) {
		t.Fatal("events without response should be emitted")
	}
	if !converter.shouldEmitEvent(event.New("inv", "agent", event.WithResponse(&model.Response{
		Object: model.ObjectTypeChatCompletion,
	}))) {
		t.Fatal("non-graph event should be emitted")
	}
	if converter.shouldEmitEvent(event.New("inv", "agent", event.WithResponse(&model.Response{
		Object: "graph.node.start",
	}))) {
		t.Fatal("graph.node.start should be filtered by default")
	}
	if !converter.shouldEmitEvent(event.New("inv", "agent", event.WithResponse(&model.Response{
		Object: graph.ObjectTypeGraphExecution,
	}))) {
		t.Fatal("graph.execution should be emitted by default")
	}
}

func TestDefaultEventConverterStreamingBranches(t *testing.T) {
	options := EventToA2AStreamingOptions{CtxID: "context", TaskID: "task"}
	converter := &defaultEventToA2AMessage{}

	t.Run("missing response", func(t *testing.T) {
		result, err := converter.ConvertStreamingToA2AMessage(
			context.Background(),
			&event.Event{},
			options,
		)
		if err != nil || result != nil {
			t.Fatalf("result = %#v, err = %v; want nil", result, err)
		}
	})

	t.Run("terminal error", func(t *testing.T) {
		result, err := converter.ConvertStreamingToA2AMessage(
			context.Background(),
			event.New("inv", "agent", event.WithResponse(&model.Response{
				Done:   true,
				Object: model.ObjectTypeError,
				Error:  &model.ResponseError{Message: "failed"},
			})),
			options,
		)
		if err == nil || result != nil {
			t.Fatalf("result = %#v, err = %v; want terminal error", result, err)
		}
	})

	t.Run("metadata only", func(t *testing.T) {
		result, err := converter.ConvertStreamingToA2AMessage(
			context.Background(),
			event.New("inv", "agent",
				event.WithTag("state"),
				event.WithResponse(&model.Response{ID: "response"}),
			),
			options,
		)
		if err != nil {
			t.Fatalf("ConvertStreamingToA2AMessage failed: %v", err)
		}
		update, ok := result.(*protocol.TaskArtifactUpdateEvent)
		if !ok || update.TaskID != "task" || update.ContextID != "context" ||
			update.Metadata[ia2a.MessageMetadataTagKey] != "state" {
			t.Fatalf("metadata-only result = %#v", result)
		}
	})

	t.Run("response ID only is skipped", func(t *testing.T) {
		result, err := converter.ConvertStreamingToA2AMessage(
			context.Background(),
			event.New("inv", "agent", event.WithResponse(&model.Response{ID: "response"})),
			options,
		)
		if err != nil || result != nil {
			t.Fatalf("result = %#v, err = %v; want nil", result, err)
		}
	})

	t.Run("mapper parts and errors", func(t *testing.T) {
		mapped := &defaultEventToA2AMessage{eventPartMappers: []EventToA2APartMapper{
			nil,
			func(context.Context, *event.Event) ([]*protocol.Part, error) {
				return nil, nil
			},
			func(context.Context, *event.Event) ([]*protocol.Part, error) {
				return []*protocol.Part{protocol.NewTextPart("mapped")}, nil
			},
		}}
		result, err := mapped.ConvertStreamingToA2AMessage(
			context.Background(),
			event.New("inv", "agent", event.WithResponse(&model.Response{ID: "response"})),
			options,
		)
		if err != nil {
			t.Fatalf("mapper conversion failed: %v", err)
		}
		update := result.(*protocol.TaskArtifactUpdateEvent)
		if len(update.Artifact.Parts) != 1 ||
			update.Artifact.Parts[0].TextContent() != "mapped" {
			t.Fatalf("mapped update = %#v", update)
		}

		wantErr := errors.New("mapper failed")
		failing := &defaultEventToA2AMessage{eventPartMappers: []EventToA2APartMapper{
			func(context.Context, *event.Event) ([]*protocol.Part, error) {
				return nil, wantErr
			},
		}}
		if _, err := failing.ConvertStreamingToA2AMessage(
			context.Background(),
			event.New("inv", "agent", event.WithResponse(&model.Response{
				Choices: []model.Choice{{Delta: model.Message{Content: "text"}}},
			})),
			options,
		); !errors.Is(err, wantErr) {
			t.Fatalf("mapper error = %v, want %v", err, wantErr)
		}
	})

	t.Run("text and reasoning", func(t *testing.T) {
		result, err := converter.ConvertStreamingToA2AMessage(
			context.Background(),
			event.New("inv", "agent", event.WithResponse(&model.Response{
				ID:        "response",
				IsPartial: true,
				Choices: []model.Choice{{Delta: model.Message{
					Content:          "answer",
					ReasoningContent: "thinking",
				}}},
			})),
			options,
		)
		if err != nil {
			t.Fatalf("text conversion failed: %v", err)
		}
		update := result.(*protocol.TaskArtifactUpdateEvent)
		if len(update.Artifact.Parts) != 2 ||
			update.Artifact.Parts[0].TextContent() != "thinking" ||
			update.Artifact.Parts[1].TextContent() != "answer" {
			t.Fatalf("text update = %#v", update)
		}
	})

	t.Run("final message", func(t *testing.T) {
		result, err := converter.ConvertStreamingToA2AMessage(
			context.Background(),
			event.New("inv", "agent", event.WithResponse(&model.Response{
				ID: "response",
				Choices: []model.Choice{{Message: model.Message{
					Content: "final",
				}}},
			})),
			options,
		)
		if err != nil {
			t.Fatalf("final conversion failed: %v", err)
		}
		update := result.(*protocol.TaskArtifactUpdateEvent)
		if len(update.Artifact.Parts) != 1 ||
			update.Artifact.Parts[0].TextContent() != "final" {
			t.Fatalf("final update = %#v", update)
		}
	})
}

func TestDefaultEventConverterToolAndCodeEvents(t *testing.T) {
	converter := &defaultEventToA2AMessage{adkCompatibility: true}
	options := EventToA2AStreamingOptions{CtxID: "context", TaskID: "task"}

	toolEvent := event.New("inv", "agent", event.WithResponse(&model.Response{
		ID: "tool-response",
		Choices: []model.Choice{
			{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call",
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      "lookup",
						Arguments: []byte(`{"city":"Shenzhen"}`),
					},
				}},
			}},
			{Message: model.Message{
				Role:     model.RoleTool,
				ToolID:   "call",
				ToolName: "lookup",
				Content:  `{"temperature":30}`,
			}},
		},
	}))
	result, err := converter.convertToolCallToA2AMessage(toolEvent)
	if err != nil {
		t.Fatalf("convertToolCallToA2AMessage failed: %v", err)
	}
	message := result.(*protocol.Message)
	if len(message.Parts) != 2 {
		t.Fatalf("tool part count = %d, want 2", len(message.Parts))
	}
	for _, part := range message.Parts {
		if part.Metadata[ia2a.DataPartMetadataTypeKey] == nil ||
			part.Metadata[ia2a.GetADKMetadataKey(ia2a.DataPartMetadataTypeKey)] == nil {
			t.Fatalf("tool part metadata = %#v", part.Metadata)
		}
	}
	streamed, err := converter.ConvertStreamingToA2AMessage(
		context.Background(),
		toolEvent,
		options,
	)
	if err != nil {
		t.Fatalf("stream tool conversion failed: %v", err)
	}
	if got := len(streamed.(*protocol.TaskArtifactUpdateEvent).Artifact.Parts); got != 2 {
		t.Fatalf("stream tool part count = %d, want 2", got)
	}

	for _, test := range []struct {
		name string
		tag  string
		want string
	}{
		{name: "code", tag: event.CodeExecutionTag, want: ia2a.DataPartMetadataTypeExecutableCode},
		{name: "result", tag: event.CodeExecutionResultTag, want: ia2a.DataPartMetadataTypeCodeExecutionResult},
	} {
		t.Run(test.name, func(t *testing.T) {
			codeEvent := event.New("inv", "agent",
				event.WithTag(test.tag),
				event.WithResponse(&model.Response{
					ID:     "code-response",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{{Message: model.Message{
						Content: "print(1)",
					}}},
				}),
			)
			unary, err := converter.convertCodeExecutionToA2AMessage(codeEvent)
			if err != nil {
				t.Fatalf("unary code conversion failed: %v", err)
			}
			part := unary.(*protocol.Message).Parts[0]
			if got := part.Metadata[ia2a.DataPartMetadataTypeKey]; got != test.want {
				t.Fatalf("code part type = %v, want %s", got, test.want)
			}
			streamed, err := converter.ConvertStreamingToA2AMessage(
				context.Background(),
				codeEvent,
				options,
			)
			if err != nil || streamed == nil {
				t.Fatalf("stream code result = %#v, err = %v", streamed, err)
			}
		})
	}
}

func TestDefaultEventConverterInternalEmptyBranches(t *testing.T) {
	converter := &defaultEventToA2AMessage{}
	empty := event.New("inv", "agent", event.WithResponse(&model.Response{}))
	if result, err := converter.convertToolCallToA2AMessage(empty); err != nil || result != nil {
		t.Fatalf("empty tool result = %#v, err = %v", result, err)
	}
	if result, err := converter.convertCodeExecutionToA2AMessage(empty); err != nil || result != nil {
		t.Fatalf("empty code result = %#v, err = %v", result, err)
	}
	choice := event.New("inv", "agent", event.WithResponse(&model.Response{
		Choices: []model.Choice{{Message: model.Message{}}},
	}))
	if result, err := converter.convertToolCallToA2AMessage(choice); err != nil || result != nil {
		t.Fatalf("contentless tool result = %#v, err = %v", result, err)
	}
	if result, err := converter.convertCodeExecutionToA2AMessage(choice); err != nil || result != nil {
		t.Fatalf("contentless code result = %#v, err = %v", result, err)
	}
	if isToolCallEvent(nil) || isToolCallEvent(&event.Event{}) ||
		isCodeExecutionEvent(nil) || isCodeExecutionEvent(&event.Event{}) {
		t.Fatal("nil events were classified as tool/code events")
	}
}

func TestConvertFilePartResolution(t *testing.T) {
	tests := []struct {
		name     string
		part     *protocol.Part
		wantType model.ContentType
	}{
		{
			name: "metadata image wins",
			part: func() *protocol.Part {
				part := protocol.NewRawPart([]byte("image"), "application/octet-stream")
				part.Metadata = map[string]any{
					ia2a.FilePartMetadataContentTypeKey: ia2a.FilePartMetadataContentTypeImage,
				}
				return part
			}(),
			wantType: model.ContentTypeImage,
		},
		{name: "mime audio", part: protocol.NewRawPart([]byte("audio"), "audio/wav"), wantType: model.ContentTypeAudio},
		{name: "short image format", part: protocol.NewRawPart([]byte("image"), "png"), wantType: model.ContentTypeImage},
		{name: "short audio format", part: protocol.NewRawPart([]byte("audio"), "mp3"), wantType: model.ContentTypeAudio},
		{
			name: "legacy filename",
			part: func() *protocol.Part {
				part := protocol.NewRawPart([]byte("audio"), "")
				part.Filename = ia2a.FilePartMetadataContentTypeAudio
				return part
			}(),
			wantType: model.ContentTypeAudio,
		},
		{name: "generic raw", part: protocol.NewRawPart([]byte("file"), "application/pdf"), wantType: model.ContentTypeFile},
		{name: "image URL", part: protocol.NewURLPart("https://example.com/image", "image/png"), wantType: model.ContentTypeImage},
		{name: "audio URL uses file", part: protocol.NewURLPart("https://example.com/audio", "audio/wav"), wantType: model.ContentTypeFile},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parts := convertFilePart(test.part)
			if len(parts) != 1 || parts[0].Type != test.wantType {
				t.Fatalf("converted parts = %#v, want %s", parts, test.wantType)
			}
		})
	}

	if got := inferContentTypeFromMimeType(" "); got != "" {
		t.Fatalf("empty mime inference = %q", got)
	}
	if got := inferContentTypeFromMimeType("application/pdf"); got != "" {
		t.Fatalf("generic mime inference = %q", got)
	}
	unsupported := &protocol.Part{Content: protocol.Text("text")}
	if got := convertFilePart(unsupported); got != nil {
		t.Fatalf("unsupported file conversion = %#v, want nil", got)
	}
}
