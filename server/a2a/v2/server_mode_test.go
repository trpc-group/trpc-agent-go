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
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/memory"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/stateless"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type modeTestRunner struct {
	events []*event.Event
}

func (r *modeTestRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event, len(r.events))
	for _, evt := range r.events {
		out <- evt
	}
	close(out)
	return out, nil
}

func (*modeTestRunner) Close() error { return nil }

func TestBuildProcessorKeepsEventTypeAcrossManagers(t *testing.T) {
	tests := []struct {
		name          string
		taskManager   TaskManagerBuilder
		streamingType StreamingEventType
	}{
		{
			name:          "default stateless manager",
			streamingType: StreamingEventTypeTaskArtifactUpdate,
		},
		{
			name: "explicit retaining manager",
			taskManager: func(taskmanager.MessageProcessor) taskmanager.TaskManager {
				return nil
			},
			streamingType: StreamingEventTypeTaskArtifactUpdate,
		},
		{
			name:          "message events with stateless manager",
			streamingType: StreamingEventTypeMessage,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := &options{
				runner:             &modeTestRunner{},
				errorHandler:       defaultErrorHandler,
				streamingEventType: test.streamingType,
				taskManagerBuilder: test.taskManager,
			}
			processor, err := buildProcessor(nil, nil, "agent", opts)
			if err != nil {
				t.Fatalf("buildProcessor failed: %v", err)
			}
			if processor.streamingEventType != test.streamingType {
				t.Errorf(
					"streamingEventType = %v, want %v",
					processor.streamingEventType,
					test.streamingType,
				)
			}
			converter, ok := processor.eventToA2AConverter.(*defaultEventToA2AMessage)
			if !ok {
				t.Fatalf("converter type = %T, want defaultEventToA2AMessage", processor.eventToA2AConverter)
			}
			if converter.streamingEventType != test.streamingType {
				t.Errorf(
					"converter streamingEventType = %v, want %v",
					converter.streamingEventType,
					test.streamingType,
				)
			}
		})
	}
}

func TestMessageProcessorManagerModes(t *testing.T) {
	newRunner := func() *modeTestRunner {
		return &modeTestRunner{events: []*event.Event{
			{
				Response: &model.Response{
					ID:        "response-id",
					IsPartial: true,
					Choices: []model.Choice{{
						Delta: model.Message{Content: "hel"},
					}},
				},
			},
			{
				Response: &model.Response{
					ID:        "response-id",
					IsPartial: true,
					Choices: []model.Choice{{
						Delta: model.Message{Content: "lo"},
					}},
				},
			},
			{
				Response: &model.Response{
					ID:   "response-id",
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("hello"),
					}},
				},
			},
			{
				Response: &model.Response{
					Object: model.ObjectTypeRunnerCompletion,
					Done:   true,
				},
				StateDelta: map[string][]byte{"state-key": []byte(`"value"`)},
			},
		}}
	}
	request := protocol.SendMessageParams{
		Message: protocol.NewMessage(
			protocol.MessageRoleUser,
			[]*protocol.Part{protocol.NewTextPart("hi")},
		),
	}
	ctx := NewContextWithUserID(context.Background(), "user")

	t.Run("stateless returns request-local Task", func(t *testing.T) {
		processor, err := buildProcessor(nil, nil, "agent", &options{
			runner:             newRunner(),
			errorHandler:       defaultErrorHandler,
			streamingEventType: StreamingEventTypeTaskArtifactUpdate,
		})
		if err != nil {
			t.Fatalf("buildProcessor failed: %v", err)
		}
		manager, err := stateless.NewTaskManager(processor)
		if err != nil {
			t.Fatalf("stateless.NewTaskManager failed: %v", err)
		}
		response, err := manager.OnSendMessage(ctx, request)
		if err != nil {
			t.Fatalf("OnSendMessage failed: %v", err)
		}
		task := response.GetTask()
		if task == nil {
			t.Fatalf("response = %#v, want Task", response.Result)
		}
		if task.Status.State != protocol.TaskStateCompleted {
			t.Errorf("task state = %s, want completed", task.Status.State)
		}
		var text string
		for _, artifact := range task.Artifacts {
			for _, part := range artifact.Parts {
				text += part.TextContent()
			}
		}
		if text != "hello" {
			t.Errorf("task artifact text = %q, want hello", text)
		}
		if len(task.Artifacts) != 1 {
			t.Fatalf("task artifact count = %d, want 1", len(task.Artifacts))
		}
		stateDelta := DecodeStateDeltaMetadata(task.Artifacts[0].Metadata[ia2a.MessageMetadataStateDeltaKey])
		if got := string(stateDelta["state-key"]); got != `"value"` {
			t.Errorf("state delta = %q, want %q", got, `"value"`)
		}
		if _, err := manager.OnGetTask(ctx, protocol.TaskQueryParams{ID: task.ID}); err == nil {
			t.Fatal("OnGetTask succeeded, want task-not-found")
		}
	})

	t.Run("stateless unary supports task-bound Messages", func(t *testing.T) {
		processor, err := buildProcessor(nil, nil, "agent", &options{
			runner:             newRunner(),
			errorHandler:       defaultErrorHandler,
			streamingEventType: StreamingEventTypeMessage,
		})
		if err != nil {
			t.Fatalf("buildProcessor failed: %v", err)
		}
		manager, err := stateless.NewTaskManager(processor)
		if err != nil {
			t.Fatalf("stateless.NewTaskManager failed: %v", err)
		}
		response, err := manager.OnSendMessage(ctx, request)
		if err != nil {
			t.Fatalf("OnSendMessage failed: %v", err)
		}
		task := response.GetTask()
		if task == nil || task.Status.Message == nil {
			t.Fatalf("response = %#v, want Task with final Message", response.Result)
		}
		var text string
		for _, part := range task.Status.Message.Parts {
			text += part.TextContent()
		}
		if text != "hello" {
			t.Errorf("task status message text = %q, want hello", text)
		}
		stateDelta := DecodeStateDeltaMetadata(task.Status.Message.Metadata[ia2a.MessageMetadataStateDeltaKey])
		if got := string(stateDelta["state-key"]); got != `"value"` {
			t.Errorf("state delta = %q, want %q", got, `"value"`)
		}
	})

	t.Run("stateless streaming supports task-bound Messages", func(t *testing.T) {
		processor, err := buildProcessor(nil, nil, "agent", &options{
			runner:             newRunner(),
			errorHandler:       defaultErrorHandler,
			streamingEventType: StreamingEventTypeMessage,
		})
		if err != nil {
			t.Fatalf("buildProcessor failed: %v", err)
		}
		manager, err := stateless.NewTaskManager(processor)
		if err != nil {
			t.Fatalf("stateless.NewTaskManager failed: %v", err)
		}
		stream, err := manager.OnSendMessageStream(ctx, request)
		if err != nil {
			t.Fatalf("OnSendMessageStream failed: %v", err)
		}
		var task *protocol.Task
		var texts []string
		var completed *protocol.TaskStatusUpdateEvent
		for response := range stream {
			if snapshot := response.GetTask(); snapshot != nil {
				if task != nil {
					t.Fatalf("received duplicate Task snapshot: %#v", snapshot)
				}
				task = snapshot
				continue
			}
			if status := response.GetStatusUpdate(); status != nil {
				if status.Status.State == protocol.TaskStateCompleted {
					completed = status
				}
				continue
			}
			message := response.GetMessage()
			if message == nil {
				t.Fatalf("unexpected stream response: %#v", response.Result)
			}
			if task == nil {
				t.Fatal("received Message before Task snapshot")
			}
			if message.TaskID == nil || *message.TaskID != task.ID {
				t.Errorf("message task ID = %v, want %q", message.TaskID, task.ID)
			}
			for _, part := range message.Parts {
				texts = append(texts, part.TextContent())
			}
		}
		if len(texts) != 2 || texts[0] != "hel" || texts[1] != "lo" {
			t.Fatalf("stream texts = %#v, want [hel lo]", texts)
		}
		if task == nil {
			t.Fatal("stream did not start with a Task snapshot")
		}
		if completed == nil || completed.Status.Message == nil {
			t.Fatalf("completed status = %#v, want final Message", completed)
		}
		var completedText string
		for _, part := range completed.Status.Message.Parts {
			completedText += part.TextContent()
		}
		if completedText != "hello" {
			t.Errorf("completed message text = %q, want hello", completedText)
		}
		stateDelta := DecodeStateDeltaMetadata(completed.Status.Message.Metadata[ia2a.MessageMetadataStateDeltaKey])
		if got := string(stateDelta["state-key"]); got != `"value"` {
			t.Errorf("final state delta = %q, want %q", got, `"value"`)
		}
		if _, err := manager.OnGetTask(ctx, protocol.TaskQueryParams{ID: task.ID}); err == nil {
			t.Fatal("OnGetTask succeeded, want task-not-found")
		}
	})

	t.Run("explicit manager returns Task", func(t *testing.T) {
		processor, err := buildProcessor(nil, nil, "agent", &options{
			runner:             newRunner(),
			errorHandler:       defaultErrorHandler,
			streamingEventType: StreamingEventTypeTaskArtifactUpdate,
			taskManagerBuilder: func(taskmanager.MessageProcessor) taskmanager.TaskManager {
				return nil
			},
		})
		if err != nil {
			t.Fatalf("buildProcessor failed: %v", err)
		}
		manager, err := memory.NewTaskManager(processor)
		if err != nil {
			t.Fatalf("memory.NewTaskManager failed: %v", err)
		}
		response, err := manager.OnSendMessage(ctx, request)
		if err != nil {
			t.Fatalf("OnSendMessage failed: %v", err)
		}
		task := response.GetTask()
		if task == nil {
			t.Fatalf("response = %#v, want Task", response.Result)
		}
		if task.Status.State != protocol.TaskStateCompleted {
			t.Errorf("task state = %s, want completed", task.Status.State)
		}
		stored, err := manager.OnGetTask(ctx, protocol.TaskQueryParams{ID: task.ID})
		if err != nil {
			t.Fatalf("OnGetTask failed: %v", err)
		}
		if stored.Status.State != protocol.TaskStateCompleted {
			t.Errorf("stored task state = %s, want completed", stored.Status.State)
		}
	})
}
