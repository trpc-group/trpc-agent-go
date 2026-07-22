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
	"net/http"
	"net/http/httptest"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2aserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
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

func TestNewRequiresRunnerAndAgentCard(t *testing.T) {
	card := a2aserver.AgentCard{
		Name: "agent",
		URL:  "http://localhost:8080",
	}
	tests := []struct {
		name    string
		opts    []Option
		wantErr string
	}{
		{
			name:    "runner",
			wantErr: "runner (WithRunner) is required",
		},
		{
			name:    "agent card",
			opts:    []Option{WithRunner(&modeTestRunner{})},
			wantErr: "agent card (WithAgentCard) is required",
		},
		{
			name:    "runner before agent card",
			opts:    []Option{WithAgentCard(card)},
			wantErr: "runner (WithRunner) is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.opts...)
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("New error = %v, want %q", err, test.wantErr)
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
						Delta: model.Message{Content: "hello"},
					}},
				},
			},
			{
				Response: &model.Response{
					ID:        "response-id",
					IsPartial: true,
					Choices: []model.Choice{{
						Delta: model.Message{Content: " world"},
					}},
				},
			},
			{
				Response: &model.Response{
					ID:   "response-id",
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("hello world"),
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
		processor, err := buildProcessor("agent", &options{
			runner:       newRunner(),
			errorHandler: defaultErrorHandler,
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
		if text != "hello world" {
			t.Errorf("task artifact text = %q, want hello world", text)
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

	t.Run("explicit manager returns Task", func(t *testing.T) {
		processor, err := buildProcessor("agent", &options{
			runner:       newRunner(),
			errorHandler: defaultErrorHandler,
			taskManagerBuilder: func(taskmanager.MessageProcessor) (taskmanager.TaskManager, error) {
				return nil, nil
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

	t.Run("stateless preserves final-only response", func(t *testing.T) {
		processor, err := buildProcessor("agent", &options{
			runner: &modeTestRunner{events: []*event.Event{
				{
					Response: &model.Response{
						ID:   "response-id",
						Done: true,
						Choices: []model.Choice{{
							Message: model.NewAssistantMessage("final answer"),
						}},
					},
				},
				{
					Response: &model.Response{
						Object: model.ObjectTypeRunnerCompletion,
						Done:   true,
					},
				},
			}},
			errorHandler: defaultErrorHandler,
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
		if task == nil || len(task.Artifacts) != 1 ||
			len(task.Artifacts[0].Parts) != 1 {
			t.Fatalf("task = %#v, want one artifact part", task)
		}
		if got := task.Artifacts[0].Parts[0].TextContent(); got != "final answer" {
			t.Fatalf("artifact content = %q, want final answer", got)
		}
	})
}

func TestResponseRewriterRunsBeforeTaskAggregation(t *testing.T) {
	runner := &modeTestRunner{events: []*event.Event{
		{
			Response: &model.Response{
				ID:        "response",
				IsPartial: true,
				Choices: []model.Choice{{
					Delta: model.Message{Content: "secret"},
				}},
			},
		},
		{
			Response: &model.Response{
				Object: model.ObjectTypeRunnerCompletion,
				Done:   true,
			},
		},
	}}
	processor, err := buildProcessor("agent", &options{
		runner:       runner,
		errorHandler: defaultErrorHandler,
		responseRewriter: func(
			_ context.Context,
			result protocol.StreamEvent,
		) protocol.StreamEvent {
			if _, ok := result.(*protocol.TaskArtifactUpdateEvent); ok {
				return nil
			}
			return result
		},
	})
	if err != nil {
		t.Fatalf("buildProcessor failed: %v", err)
	}
	manager, err := stateless.NewTaskManager(processor)
	if err != nil {
		t.Fatalf("stateless.NewTaskManager failed: %v", err)
	}
	request := protocol.SendMessageParams{Message: protocol.NewMessage(
		protocol.MessageRoleUser,
		[]*protocol.Part{protocol.NewTextPart("hi")},
	)}
	response, err := manager.OnSendMessage(
		NewContextWithUserID(context.Background(), "user"),
		request,
	)
	if err != nil {
		t.Fatalf("OnSendMessage failed: %v", err)
	}
	task := response.GetTask()
	if task == nil {
		t.Fatal("response did not contain a Task")
	}
	if len(task.Artifacts) != 0 {
		t.Fatalf("dropped artifact reappeared in completed Task: %#v", task.Artifacts)
	}
}

func TestTaskManagerBuilderPropagatesError(t *testing.T) {
	wantErr := errors.New("task manager unavailable")
	_, err := New(
		WithRunner(&modeTestRunner{}),
		WithAgentCard(a2aserver.AgentCard{
			Name: "agent",
			URL:  "http://localhost:8080",
		}),
		WithTaskManagerBuilder(func(
			taskmanager.MessageProcessor,
		) (taskmanager.TaskManager, error) {
			return nil, wantErr
		}),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("New error = %v, want wrapped %v", err, wantErr)
	}
}

func TestServerRoutesPrimarySupportedInterface(t *testing.T) {
	server, err := New(
		WithRunner(&modeTestRunner{}),
		WithAgentCard(a2aserver.AgentCard{
			Name: "agent",
			URL:  "http://example.com/legacy",
			SupportedInterfaces: []a2aserver.AgentInterface{
				{
					URL:             "http://example.com/primary",
					ProtocolBinding: "JSONRPC",
					ProtocolVersion: "1.0",
				},
			},
		}),
	)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"http://example.com/primary/.well-known/agent-card.json",
		nil,
	)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("primary interface status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestDataPartUsesJSONRepresentation(t *testing.T) {
	converter := &defaultA2AMessageToAgentMessage{}
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]*protocol.Part{protocol.NewDataPart(map[string]any{
			"enabled": true,
			"count":   2,
		})},
	)
	converted, err := converter.ConvertToAgentMessage(context.Background(), message)
	if err != nil {
		t.Fatalf("ConvertToAgentMessage failed: %v", err)
	}
	if len(converted.ContentParts) != 1 || converted.ContentParts[0].Text == nil {
		t.Fatalf("content parts = %#v, want one text part", converted.ContentParts)
	}
	if got := *converted.ContentParts[0].Text; got != `{"count":2,"enabled":true}` {
		t.Fatalf("data part text = %q, want JSON object", got)
	}
}

func TestRunnerClosureBeforeCompletionFailsTask(t *testing.T) {
	processor, err := buildProcessor("agent", &options{
		runner:       &modeTestRunner{},
		errorHandler: defaultErrorHandler,
	})
	if err != nil {
		t.Fatalf("buildProcessor failed: %v", err)
	}
	manager, err := stateless.NewTaskManager(processor)
	if err != nil {
		t.Fatalf("stateless.NewTaskManager failed: %v", err)
	}
	response, err := manager.OnSendMessage(
		NewContextWithUserID(context.Background(), "user"),
		protocol.SendMessageParams{Message: protocol.NewMessage(
			protocol.MessageRoleUser,
			[]*protocol.Part{protocol.NewTextPart("hi")},
		)},
	)
	if err != nil {
		t.Fatalf("OnSendMessage failed: %v", err)
	}
	task := response.GetTask()
	if task == nil {
		t.Fatalf("response = %#v, want Task", response.Result)
	}
	if task.Status.State != protocol.TaskStateFailed {
		t.Fatalf("task state = %s, want failed", task.Status.State)
	}
}

func TestTerminalAgentErrorFailsTaskByDefault(t *testing.T) {
	processor, err := buildProcessor("agent", &options{
		runner: &modeTestRunner{events: []*event.Event{{
			Response: &model.Response{
				Object: model.ObjectTypeError,
				Done:   true,
				Error: &model.ResponseError{
					Type:    model.ErrorTypeFlowError,
					Message: "agent failed",
				},
			},
		}}},
		errorHandler: defaultErrorHandler,
	})
	if err != nil {
		t.Fatalf("buildProcessor failed: %v", err)
	}
	manager, err := stateless.NewTaskManager(processor)
	if err != nil {
		t.Fatalf("stateless.NewTaskManager failed: %v", err)
	}
	response, err := manager.OnSendMessage(
		NewContextWithUserID(context.Background(), "user"),
		protocol.SendMessageParams{Message: protocol.NewMessage(
			protocol.MessageRoleUser,
			[]*protocol.Part{protocol.NewTextPart("hi")},
		)},
	)
	if err != nil {
		t.Fatalf("OnSendMessage failed: %v", err)
	}
	task := response.GetTask()
	if task == nil || task.Status.State != protocol.TaskStateFailed {
		t.Fatalf("task = %#v, want failed Task", task)
	}
	if task.Status.Message == nil || len(task.Status.Message.Parts) != 1 ||
		task.Status.Message.Parts[0].TextContent() != "agent failed" {
		t.Fatalf("failure status message = %#v", task.Status.Message)
	}
	if got := task.Status.Message.Metadata[ia2a.MessageMetadataTaskStateKey]; got != string(protocol.TaskStateFailed) {
		t.Fatalf("task state metadata = %v, want failed", got)
	}
}

func TestNewAgentCardAdvertisesV1JSONRPCInterface(t *testing.T) {
	card, err := NewAgentCard(
		"agent",
		"description",
		"127.0.0.1:8888",
		true,
	)
	if err != nil {
		t.Fatalf("NewAgentCard failed: %v", err)
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("supported interface count = %d, want 1", len(card.SupportedInterfaces))
	}
	iface := card.SupportedInterfaces[0]
	if iface.URL != "http://127.0.0.1:8888" ||
		iface.ProtocolBinding != "JSONRPC" ||
		iface.ProtocolVersion != protocol.ProtocolVersionV1 {
		t.Fatalf("supported interface = %#v", iface)
	}
}
