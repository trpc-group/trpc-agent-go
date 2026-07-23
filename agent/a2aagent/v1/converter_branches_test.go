//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2aagent

import (
	"errors"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestDefaultInvocationConverterContentPartsAndMetadata(t *testing.T) {
	text := "text part"
	invocation := &agent.Invocation{
		InvocationID: "invocation",
		Message: model.Message{
			Content: "content",
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &text},
				{Type: model.ContentTypeText},
				{Type: model.ContentTypeImage, Image: &model.Image{
					Data: []byte("image"), Format: "image/png",
				}},
				{Type: model.ContentTypeImage, Image: &model.Image{
					URL: "https://example.com/image", Format: "image/png",
				}},
				{Type: model.ContentTypeImage},
				{Type: model.ContentTypeAudio, Audio: &model.Audio{
					Data: []byte("audio"), Format: "audio/wav",
				}},
				{Type: model.ContentTypeAudio},
				{Type: model.ContentTypeFile, File: &model.File{
					Name: "report.pdf", Data: []byte("file"), MimeType: "application/pdf",
				}},
				{Type: model.ContentTypeFile, File: &model.File{
					FileID: "https://example.com/file", MimeType: "application/pdf",
				}},
				{Type: model.ContentTypeFile},
				{Type: model.ContentType("unknown")},
			},
		},
		Session: &session.Session{ID: "context", UserID: "user"},
	}
	message, err := (&defaultEventA2AConverter{}).ConvertToA2AMessage("remote", invocation)
	if err != nil {
		t.Fatalf("ConvertToA2AMessage failed: %v", err)
	}
	if len(message.Parts) != 7 {
		t.Fatalf("part count = %d, want 7", len(message.Parts))
	}
	if message.ContextID == nil || *message.ContextID != "context" ||
		message.Metadata["invocation_id"] != "invocation" ||
		message.Metadata["user_id"] != "user" ||
		message.Metadata[ia2a.MessageMetadataInteractionSpecVersionKey] == nil {
		t.Fatalf("message metadata = %#v, context = %#v", message.Metadata, message.ContextID)
	}
	if message.Parts[2].Metadata[ia2a.FilePartMetadataContentTypeKey] !=
		ia2a.FilePartMetadataContentTypeImage ||
		message.Parts[4].Metadata[ia2a.FilePartMetadataContentTypeKey] !=
			ia2a.FilePartMetadataContentTypeAudio {
		t.Fatalf("file part metadata = %#v / %#v", message.Parts[2].Metadata, message.Parts[4].Metadata)
	}

	empty, err := (&defaultEventA2AConverter{}).ConvertToA2AMessage(
		"remote",
		&agent.Invocation{},
	)
	if err != nil || len(empty.Parts) != 1 || empty.Parts[0].TextContent() != "" {
		t.Fatalf("empty conversion = %#v, err = %v", empty, err)
	}
}

func TestDefaultResponseConverterUnaryVariants(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{InvocationID: "invocation"}

	empty, err := converter.ConvertToEvents(
		protocol.SendMessageResponse{},
		"remote",
		invocation,
	)
	if err != nil || len(empty) != 1 ||
		empty[0].Response.Choices[0].Message.Content != "" {
		t.Fatalf("empty response events = %#v, err = %v", empty, err)
	}

	history := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]*protocol.Part{protocol.NewTextPart("history")},
	)
	status := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]*protocol.Part{protocol.NewTextPart("status output")},
	)
	task := protocol.NewTask("task", "context")
	task.History = []protocol.Message{history}
	task.Status = protocol.TaskStatus{
		State:   protocol.TaskStateCompleted,
		Message: &status,
	}
	events, err := converter.ConvertToEvents(
		*protocol.NewSendMessageResponseTask(task),
		"remote",
		invocation,
	)
	if err != nil || len(events) != 2 ||
		events[0].Response.Choices[0].Message.Content != "history" ||
		events[1].Response.Choices[0].Message.Content != "status output" ||
		!events[1].Done {
		t.Fatalf("task response events = %#v, err = %v", events, err)
	}

	failed := protocol.NewTask("failed", "context")
	failed.Status = protocol.TaskStatus{State: protocol.TaskStateRejected}
	events, err = converter.ConvertToEvents(
		*protocol.NewSendMessageResponseTask(failed),
		"remote",
		invocation,
	)
	if err != nil || len(events) != 1 || events[0].Response.Error == nil ||
		events[0].Response.Error.Message != "remote task rejected" {
		t.Fatalf("failed task events = %#v, err = %v", events, err)
	}

	withArtifact := protocol.NewTask("artifact", "context")
	withArtifact.Status = protocol.TaskStatus{
		State: protocol.TaskStateCompleted,
		Message: func() *protocol.Message {
			message := protocol.NewMessage(
				protocol.MessageRoleAgent,
				[]*protocol.Part{protocol.NewTextPart("status ignored")},
			)
			return &message
		}(),
	}
	withArtifact.Artifacts = []protocol.Artifact{{
		ArtifactID: "artifact-id",
		Parts:      []*protocol.Part{protocol.NewTextPart("artifact output")},
	}}
	events, err = converter.ConvertToEvents(
		*protocol.NewSendMessageResponseTask(withArtifact),
		"remote",
		invocation,
	)
	if err != nil || len(events) != 1 ||
		events[0].Response.Choices[0].Message.Content != "artifact output" {
		t.Fatalf("artifact task events = %#v, err = %v", events, err)
	}
}

func TestDefaultResponseConverterStreamingVariants(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{InvocationID: "invocation"}

	empty, err := converter.ConvertStreamingToEvents(
		protocol.StreamResponse{},
		"remote",
		invocation,
	)
	if err != nil || len(empty) != 1 {
		t.Fatalf("empty stream events = %#v, err = %v", empty, err)
	}

	task := protocol.NewTask("task", "context")
	task.Status.State = protocol.TaskStateCompleted
	task.Artifacts = []protocol.Artifact{{
		ArtifactID: "artifact",
		Parts:      []*protocol.Part{protocol.NewTextPart("task output")},
	}}
	events, err := converter.ConvertStreamingToEvents(
		protocol.NewStreamResponseTask(task),
		"remote",
		invocation,
	)
	if err != nil || len(events) != 1 ||
		events[0].Response.Choices[0].Delta.Content != "task output" {
		t.Fatalf("stream task events = %#v, err = %v", events, err)
	}

	submitted := protocol.NewTaskStatusUpdateEvent(
		"task",
		"context",
		protocol.TaskStatus{State: protocol.TaskStateSubmitted},
		false,
	)
	events, err = converter.ConvertStreamingToEvents(
		protocol.NewStreamResponseStatusUpdate(&submitted),
		"remote",
		invocation,
	)
	if err != nil || events != nil {
		t.Fatalf("lifecycle status events = %#v, err = %v", events, err)
	}

	emptyArtifact := &protocol.TaskArtifactUpdateEvent{
		TaskID: "task", ContextID: "context",
		Artifact: protocol.Artifact{ArtifactID: "artifact"},
	}
	events, err = converter.ConvertStreamingToEvents(
		protocol.NewStreamResponseArtifactUpdate(emptyArtifact),
		"remote",
		invocation,
	)
	if err != nil || events != nil {
		t.Fatalf("empty artifact events = %#v, err = %v", events, err)
	}

}

func TestParseA2AMessagePartsBuiltInsAndMappers(t *testing.T) {
	thought := protocol.NewTextPart("reasoning")
	thought.Metadata = map[string]any{ia2a.TextPartMetadataThoughtKey: true}
	adkThought := protocol.NewTextPart(" adk")
	adkThought.Metadata = map[string]any{
		ia2a.GetADKMetadataKey(ia2a.TextPartMetadataThoughtKey): true,
	}
	call := protocol.NewDataPart(map[string]any{
		ia2a.ToolCallFieldID:   "call",
		ia2a.ToolCallFieldType: "function",
		ia2a.ToolCallFieldName: "lookup",
		ia2a.ToolCallFieldArgs: map[string]any{"city": "Shenzhen"},
	})
	call.Metadata = map[string]any{
		ia2a.DataPartMetadataTypeKey: ia2a.DataPartMetadataTypeFunctionCall,
	}
	response := protocol.NewDataPart(map[string]any{
		ia2a.ToolCallFieldID:       "call",
		ia2a.ToolCallFieldName:     "lookup",
		ia2a.ToolCallFieldResponse: "sunny",
	})
	response.Metadata = map[string]any{
		ia2a.DataPartMetadataTypeKey: ia2a.DataPartMetadataTypeFunctionResp,
	}
	code := protocol.NewDataPart(map[string]any{
		ia2a.CodeExecutionFieldContent: "print(1)",
	})
	code.Metadata = map[string]any{
		ia2a.DataPartMetadataTypeKey: ia2a.DataPartMetadataTypeExecutableCode,
	}
	codeResult := protocol.NewDataPart(map[string]any{
		ia2a.CodeExecutionFieldContent: "1",
	})
	codeResult.Metadata = map[string]any{
		ia2a.DataPartMetadataTypeKey: ia2a.DataPartMetadataTypeCodeExecutionResult,
	}
	custom := protocol.NewDataPart(map[string]any{"custom": true})
	custom.Metadata = map[string]any{ia2a.DataPartMetadataTypeKey: "custom"}
	mapperCalls := 0
	message := protocol.NewMessage(protocol.MessageRoleAgent, []*protocol.Part{
		nil,
		protocol.NewTextPart("before "),
		thought,
		adkThought,
		call,
		response,
		protocol.NewTextPart("after"),
		code,
		codeResult,
		custom,
	})
	message.Metadata = map[string]any{
		ia2a.MessageMetadataObjectTypeKey: model.ObjectTypeChatCompletion,
		ia2a.MessageMetadataTagKey:        "tag",
		ia2a.MessageMetadataResponseIDKey: "response",
		ia2a.MessageMetadataStateDeltaKey: ia2a.EncodeStateDeltaMetadata(
			map[string][]byte{"key": []byte(`"value"`)},
		),
	}
	result := parseA2AMessagePartsWithMappers(&message, []A2ADataPartMapper{
		nil,
		func(*protocol.Part, *A2ADataPartMappingResult) (bool, error) {
			mapperCalls++
			return false, errors.New("skip")
		},
		func(*protocol.Part, *A2ADataPartMappingResult) (bool, error) {
			mapperCalls++
			return false, nil
		},
		func(_ *protocol.Part, mapped *A2ADataPartMappingResult) (bool, error) {
			mapperCalls++
			mapped.SetTextContent(mapped.GetTextContent() + "mapped")
			mapped.SetReasoningContent(mapped.GetReasoningContent() + " mapped reasoning")
			mapped.AppendToolCall(model.ToolCall{ID: "mapped-call"})
			mapped.AppendToolResponse(A2ADataPartToolResponse{
				ID: "mapped", Name: "mapped", Content: "mapped",
			})
			mapped.SetCodeExecution("mapped code")
			mapped.SetCodeExecutionResult("mapped result")
			if err := mapped.SetEventExtension("custom", true); err != nil {
				return false, err
			}
			return true, nil
		},
	})
	if mapperCalls != 3 || result.textContent != "before mapped" ||
		result.finalTextContent != "after" ||
		result.reasoningContent != "reasoning adk mapped reasoning" ||
		len(result.toolCalls) != 2 || len(result.toolResponses) != 2 ||
		result.codeExecution != "mapped code" ||
		result.codeExecutionResult != "mapped result" ||
		result.responseID != "response" || result.tag != "tag" ||
		string(result.stateDelta["key"]) != `"value"` ||
		len(result.extensions) != 1 {
		t.Fatalf("parse result = %#v, mapper calls = %d", result, mapperCalls)
	}
	if got := parseA2AMessageParts(&message); got == nil {
		t.Fatal("parseA2AMessageParts returned nil")
	}
}

func TestDataPartAndTextHelperBranches(t *testing.T) {
	if text, thought := processTextPart(nil); text != "" || thought {
		t.Fatalf("nil text part = (%q, %v)", text, thought)
	}
	if text, thought := processTextPart(protocol.NewDataPart("data")); text != "" || thought {
		t.Fatalf("data as text = (%q, %v)", text, thought)
	}

	result := &parseResult{}
	processDataPart(nil, result)
	processDataPart(protocol.NewTextPart("text"), result)
	processDataPart(protocol.NewDataPart("not a map"), result)
	unknown := protocol.NewDataPart(map[string]any{"value": true})
	processDataPart(unknown, result)
	unknown.Metadata = map[string]any{ia2a.DataPartMetadataTypeKey: "unknown"}
	processDataPart(unknown, result)

	invalidCall := protocol.NewDataPart("invalid")
	if got := processFunctionCall(invalidCall); got != nil {
		t.Fatalf("invalid function call = %#v", got)
	}
	missingName := protocol.NewDataPart(map[string]any{
		ia2a.ToolCallFieldArgs: 1,
	})
	if got := processFunctionCall(missingName); got != nil {
		t.Fatalf("nameless function call = %#v", got)
	}
	stringArgs := protocol.NewDataPart(map[string]any{
		ia2a.ToolCallFieldName: "lookup",
		ia2a.ToolCallFieldArgs: `{"city":"Shenzhen"}`,
	})
	if got := processFunctionCall(stringArgs); got == nil ||
		string(got.Function.Arguments) != `{"city":"Shenzhen"}` {
		t.Fatalf("string-argument function call = %#v", got)
	}

	content, id, name := processFunctionResponse(protocol.NewDataPart("invalid"))
	if content != "" || id != "" || name != "" {
		t.Fatalf("invalid function response = (%q, %q, %q)", content, id, name)
	}
	response := protocol.NewDataPart(map[string]any{
		ia2a.ToolCallFieldResponse: map[string]any{"ok": true},
	})
	content, _, _ = processFunctionResponse(response)
	if content != `{"ok":true}` {
		t.Fatalf("structured function response = %q", content)
	}

	data := map[string]any{"fallback": "value"}
	if got := extractStringField(data, "primary", "fallback"); got != "value" {
		t.Fatalf("fallback string = %q", got)
	}
	if got := extractStringField(data, "missing", "also-missing"); got != "" {
		t.Fatalf("missing string = %q", got)
	}
	if got := processExecutableCode(protocol.NewDataPart("invalid")); got != "" {
		t.Fatalf("invalid executable code = %q", got)
	}
	if got := processCodeExecutionResult(protocol.NewDataPart("invalid")); got != "" {
		t.Fatalf("invalid code result = %q", got)
	}
}

func TestResponseBuildersErrorsObjectsAndCompletion(t *testing.T) {
	now := time.Now()
	responseErr := &model.ResponseError{Type: model.ErrorTypeFlowError, Message: "failed"}
	failed := &parseResult{taskState: protocol.TaskStateFailed, responseError: responseErr}
	if got := buildStreamingResponse("response", failed, protocol.MessageRoleAgent); !got.Done ||
		got.Object != model.ObjectTypeError || got.Error != responseErr {
		t.Fatalf("stream failure response = %#v", got)
	}
	if got := buildNonStreamingResponse("response", failed, protocol.MessageRoleAgent); !got.Done ||
		got.Object != model.ObjectTypeError || got.Error != responseErr {
		t.Fatalf("unary failure response = %#v", got)
	}

	recoverable := &parseResult{
		textContent:   "retry",
		responseError: responseErr,
		taskState:     protocol.TaskStateInputRequired,
		objectType:    model.ObjectTypeError,
	}
	if got := buildStreamingResponse("response", recoverable, protocol.MessageRoleUser); got.Done ||
		got.Object != model.ObjectTypeChatCompletion ||
		got.Choices[0].Message.Content != "retry" {
		t.Fatalf("recoverable stream response = %#v", got)
	}
	if got := buildRecoverableErrorResponse(
		"response",
		recoverable,
		protocol.MessageRoleUser,
		now,
	); got.Choices[0].Message.Role != model.RoleUser {
		t.Fatalf("recoverable response = %#v", got)
	}

	for _, test := range []struct {
		state protocol.TaskState
		want  string
	}{
		{state: protocol.TaskStateCanceled, want: "remote task canceled"},
		{state: protocol.TaskStateRejected, want: "remote task rejected"},
		{state: protocol.TaskStateFailed, want: "remote task failed"},
	} {
		if got := taskFailureMessage(test.state); got != test.want {
			t.Fatalf("failure message for %s = %q, want %q", test.state, got, test.want)
		}
		if !isTaskFailureState(test.state) {
			t.Fatalf("%s was not classified as failure", test.state)
		}
	}
	if isTaskFailureState(protocol.TaskStateCompleted) {
		t.Fatal("completed task classified as failure")
	}

	if got := extractObjectType(&parseResult{
		codeExecution: "print(1)",
	}); got != model.ObjectTypePostprocessingCodeExecution {
		t.Fatalf("code object type = %q", got)
	}
	if got := extractObjectType(&parseResult{
		toolResponses: []toolResponseData{{content: "result"}},
	}); got != model.ObjectTypeToolResponse {
		t.Fatalf("tool response object type = %q", got)
	}
	if got := extractObjectType(&parseResult{
		toolCalls: []model.ToolCall{{ID: "call"}},
	}); got != model.ObjectTypeChatCompletion {
		t.Fatalf("tool call object type = %q", got)
	}

	evt := event.New("inv", "agent", event.WithResponse(&model.Response{}))
	markGraphCompletionEvent(evt, &parseResult{objectType: graph.ObjectTypeGraphExecution})
	if !evt.Response.Done || evt.Response.IsPartial {
		t.Fatalf("graph completion event = %#v", evt)
	}
	markGraphCompletionEvent(nil, nil)

	if terminalStreamingResponseError(nil) != nil ||
		taskResponseError(nil) != nil ||
		streamingResponseContent(nil) != "" ||
		nonStreamingResponseContent(nil) != "" {
		t.Fatal("nil response helpers returned values")
	}
}

func TestTerminalStructuredErrorMarking(t *testing.T) {
	newErrorEvent := func() *event.Event {
		return event.New("inv", "agent", event.WithResponse(&model.Response{
			Error:     &model.ResponseError{Message: "failed"},
			IsPartial: true,
		}))
	}
	failed := protocol.NewTaskStatusUpdateEvent(
		"task",
		"context",
		protocol.TaskStatus{State: protocol.TaskStateFailed},
		true,
	)
	evt := newErrorEvent()
	markTerminalStructuredErrorEvent(evt, &failed)
	if !evt.Response.Done || evt.Response.Object != model.ObjectTypeError {
		t.Fatalf("failed status event = %#v", evt)
	}

	inputRequired := protocol.NewTaskStatusUpdateEvent(
		"task",
		"context",
		protocol.TaskStatus{State: protocol.TaskStateInputRequired},
		true,
	)
	evt = newErrorEvent()
	markTerminalStructuredErrorEvent(evt, &inputRequired)
	if evt.Response.Done {
		t.Fatalf("recoverable status event marked done: %#v", evt)
	}

	lastChunk := true
	evt = newErrorEvent()
	markTerminalStructuredErrorEvent(evt, &protocol.TaskArtifactUpdateEvent{
		LastChunk: &lastChunk,
	})
	if !evt.Response.Done {
		t.Fatalf("final artifact event not marked done: %#v", evt)
	}
	markTerminalStructuredErrorEvent(nil, nil)
}

func TestTaskMessageConversionAndMetadataHelpers(t *testing.T) {
	task := protocol.NewTask("task", "context")
	task.Status.State = protocol.TaskStateCompleted
	task.Metadata = map[string]any{"task": "metadata"}
	task.Artifacts = []protocol.Artifact{
		{ArtifactID: "first", Parts: []*protocol.Part{protocol.NewTextPart("one")}},
		{ArtifactID: "second", Parts: []*protocol.Part{protocol.NewTextPart("two")}},
	}
	message := convertTaskToMessage(task)
	if message.MessageID != "second" || len(message.Parts) != 2 ||
		message.Metadata[ia2a.MessageMetadataTaskStateKey] !=
			string(protocol.TaskStateCompleted) {
		t.Fatalf("task message = %#v", message)
	}

	statusMessage := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]*protocol.Part{protocol.NewTextPart("status")},
	)
	statusMessage.Metadata = map[string]any{"message": "metadata"}
	status := protocol.NewTaskStatusUpdateEvent(
		"task",
		"context",
		protocol.TaskStatus{
			State:   protocol.TaskStateInputRequired,
			Message: &statusMessage,
		},
		true,
	)
	status.Metadata = map[string]any{"event": "metadata"}
	convertedStatus := convertTaskStatusToMessage(&status)
	if convertedStatus == &statusMessage ||
		convertedStatus.Metadata["message"] != "metadata" ||
		convertedStatus.Metadata["event"] != "metadata" {
		t.Fatalf("converted status = %#v", convertedStatus)
	}

	artifact := &protocol.TaskArtifactUpdateEvent{
		TaskID: "task", ContextID: "context",
		Artifact: protocol.Artifact{
			ArtifactID: "artifact",
			Parts:      []*protocol.Part{protocol.NewTextPart("output")},
			Metadata:   map[string]any{"artifact": "metadata"},
		},
		Metadata: map[string]any{"event": "metadata"},
	}
	convertedArtifact := convertTaskArtifactToMessage(artifact)
	if convertedArtifact.Metadata["artifact"] != "metadata" ||
		convertedArtifact.Metadata["event"] != "metadata" {
		t.Fatalf("converted artifact = %#v", convertedArtifact)
	}

	if taskStateFromMetadata(nil) != "" ||
		taskStateFromMetadata(map[string]any{
			ia2a.MessageMetadataTaskStateKey: 1,
		}) != "" {
		t.Fatal("invalid task state metadata was accepted")
	}
	if mergeTaskMetadata("", nil) != nil {
		t.Fatal("empty merged metadata was non-nil")
	}
	merged := mergeTaskMetadata(
		protocol.TaskStateCompleted,
		map[string]any{"key": "old"},
		map[string]any{"key": "new"},
	)
	if merged["key"] != "new" {
		t.Fatalf("metadata precedence = %#v", merged)
	}
}
