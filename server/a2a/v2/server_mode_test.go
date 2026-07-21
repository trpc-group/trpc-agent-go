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

func TestBuildProcessorSelectsTaskMode(t *testing.T) {
	tests := []struct {
		name              string
		taskManager       TaskManagerBuilder
		wantTaskMode      bool
		wantStreamingType StreamingEventType
	}{
		{
			name:              "default stateless mode",
			wantStreamingType: StreamingEventTypeMessage,
		},
		{
			name: "explicit task manager",
			taskManager: func(taskmanager.MessageProcessor) taskmanager.TaskManager {
				return nil
			},
			wantTaskMode:      true,
			wantStreamingType: StreamingEventTypeTaskArtifactUpdate,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := &options{
				runner:             &modeTestRunner{},
				errorHandler:       defaultErrorHandler,
				streamingEventType: StreamingEventTypeTaskArtifactUpdate,
				taskManagerBuilder: test.taskManager,
			}
			processor, err := buildProcessor(nil, nil, "agent", opts)
			if err != nil {
				t.Fatalf("buildProcessor failed: %v", err)
			}
			if processor.taskMode != test.wantTaskMode {
				t.Errorf("taskMode = %v, want %v", processor.taskMode, test.wantTaskMode)
			}
			if processor.streamingEventType != test.wantStreamingType {
				t.Errorf(
					"streamingEventType = %v, want %v",
					processor.streamingEventType,
					test.wantStreamingType,
				)
			}
			converter, ok := processor.eventToA2AConverter.(*defaultEventToA2AMessage)
			if !ok {
				t.Fatalf("converter type = %T, want defaultEventToA2AMessage", processor.eventToA2AConverter)
			}
			if converter.streamingEventType != test.wantStreamingType {
				t.Errorf(
					"converter streamingEventType = %v, want %v",
					converter.streamingEventType,
					test.wantStreamingType,
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

	t.Run("stateless returns direct Message", func(t *testing.T) {
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
		message := response.GetMessage()
		if message == nil {
			t.Fatalf("response = %#v, want Message", response.Result)
		}
		if message.TaskID != nil {
			t.Errorf("message task ID = %q, want nil", *message.TaskID)
		}
		if got := message.Parts[0].TextContent(); got != "hello" {
			t.Errorf("message text = %q, want hello", got)
		}
		stateDelta := DecodeStateDeltaMetadata(
			message.Metadata[ia2a.MessageMetadataStateDeltaKey],
		)
		if got := string(stateDelta["state-key"]); got != `"value"` {
			t.Errorf("state delta = %q, want %q", got, `"value"`)
		}
	})

	t.Run("stateless streaming returns Message deltas", func(t *testing.T) {
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
		stream, err := manager.OnSendMessageStream(ctx, request)
		if err != nil {
			t.Fatalf("OnSendMessageStream failed: %v", err)
		}
		var messages []*protocol.Message
		var texts []string
		for response := range stream {
			message := response.GetMessage()
			if message == nil {
				t.Fatalf("stream response = %#v, want Message", response.Result)
			}
			if message.TaskID != nil {
				t.Errorf("message task ID = %q, want nil", *message.TaskID)
			}
			messages = append(messages, message)
			if len(message.Parts) > 0 {
				texts = append(texts, message.Parts[0].TextContent())
			}
		}
		if len(texts) != 2 || texts[0] != "hel" || texts[1] != "lo" {
			t.Fatalf("stream texts = %#v, want [hel lo]", texts)
		}
		if len(messages) != 3 {
			t.Fatalf("stream message count = %d, want 3", len(messages))
		}
		stateDelta := DecodeStateDeltaMetadata(
			messages[2].Metadata[ia2a.MessageMetadataStateDeltaKey],
		)
		if got := string(stateDelta["state-key"]); got != `"value"` {
			t.Errorf("final state delta = %q, want %q", got, `"value"`)
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
	})
}
