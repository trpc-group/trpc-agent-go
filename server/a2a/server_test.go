//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Mock implementations for testing
type mockAgent struct {
	name        string
	description string
	tools       []tool.Tool
	subAgents   []agent.Agent
	runFunc     func(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error)
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockAgent) Tools() []tool.Tool {
	return m.tools
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, invocation)
	}
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Content: "mock response",
					},
				},
			},
		},
	}
	close(ch)
	return ch, nil
}

func (m *mockAgent) SubAgents() []agent.Agent {
	return m.subAgents
}

func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range m.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

type mockTool struct {
	name        string
	description string
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockTool) Execute(ctx context.Context, input string) (string, error) {
	return "mock tool result", nil
}

type mockSessionService struct{}

func (m *mockSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	return &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     state,
		Events:    []event.Event{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *mockSessionService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     session.StateMap{},
		Events:    []event.Event{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *mockSessionService) ListSessions(ctx context.Context, userKey session.UserKey, options ...session.Option) ([]*session.Session, error) {
	return []*session.Session{}, nil
}

func (m *mockSessionService) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	return nil
}

func (m *mockSessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return nil
}

func (m *mockSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	return session.StateMap{}, nil
}

func (m *mockSessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	return session.StateMap{}, nil
}

func (m *mockSessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return nil
}

func (m *mockSessionService) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) AppendEvent(ctx context.Context, session *session.Session, event *event.Event, options ...session.Option) error {
	return nil
}

func (m *mockSessionService) Close() error {
	return nil
}

// Implement new session.Service summary methods.
func (m *mockSessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}

func (m *mockSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}

func (m *mockSessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	return "", false
}

type mockA2AToAgentConverter struct{}

func (m *mockA2AToAgentConverter) ConvertToAgentMessage(ctx context.Context, message protocol.Message) (*model.Message, error) {
	return &model.Message{
		Role:    model.RoleUser,
		Content: "converted message",
	}, nil
}

type mockEventToA2AConverter struct {
	convertToA2AMessageFunc          func(ctx context.Context, event *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error)
	convertStreamingToA2AMessageFunc func(ctx context.Context, event *event.Event, options EventToA2AStreamingOptions) (protocol.StreamingMessageResult, error)
}

func (m *mockEventToA2AConverter) ConvertToA2AMessage(
	ctx context.Context,
	event *event.Event,
	options EventToA2AUnaryOptions,
) (protocol.UnaryMessageResult, error) {
	if m.convertToA2AMessageFunc != nil {
		return m.convertToA2AMessageFunc(ctx, event, options)
	}
	return &protocol.Message{
		Role:  protocol.MessageRoleAgent,
		Parts: []protocol.Part{&protocol.TextPart{Text: "converted event"}},
	}, nil
}

func (m *mockEventToA2AConverter) ConvertStreamingToA2AMessage(
	ctx context.Context,
	event *event.Event,
	options EventToA2AStreamingOptions,
) (protocol.StreamingMessageResult, error) {
	if m.convertStreamingToA2AMessageFunc != nil {
		return m.convertStreamingToA2AMessageFunc(ctx, event, options)
	}
	return &protocol.Message{
		Role:  protocol.MessageRoleAgent,
		Parts: []protocol.Part{&protocol.TextPart{Text: "streaming event"}},
	}, nil
}

type mockTaskManager struct {
	processMessageFunc func(ctx context.Context, message protocol.Message, options taskmanager.ProcessOptions, handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error)
}

func (m *mockTaskManager) ProcessMessage(ctx context.Context, message protocol.Message, options taskmanager.ProcessOptions, handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	if m.processMessageFunc != nil {
		return m.processMessageFunc(ctx, message, options, handler)
	}
	return &taskmanager.MessageProcessingResult{}, nil
}

// mockTaskHandler implements TaskHandler interface for testing
type mockTaskHandler struct {
	buildTaskFunc         func(specificTaskID *string, contextID *string) (string, error)
	updateTaskStateFunc   func(taskID *string, state protocol.TaskState, message *protocol.Message) error
	addArtifactFunc       func(taskID *string, artifact protocol.Artifact, isFinal bool, needMoreData bool) error
	subscribeTaskFunc     func(taskID *string) (taskmanager.TaskSubscriber, error)
	getTaskFunc           func(taskID *string) (taskmanager.CancellableTask, error)
	cleanTaskFunc         func(taskID *string) error
	getMessageHistoryFunc func() []protocol.Message
	getContextIDFunc      func() string
	getMetadataFunc       func() (map[string]any, error)
}

func (m *mockTaskHandler) BuildTask(specificTaskID *string, contextID *string) (string, error) {
	if m.buildTaskFunc != nil {
		return m.buildTaskFunc(specificTaskID, contextID)
	}
	return "test-task-id", nil
}

func (m *mockTaskHandler) UpdateTaskState(taskID *string, state protocol.TaskState, message *protocol.Message) error {
	if m.updateTaskStateFunc != nil {
		return m.updateTaskStateFunc(taskID, state, message)
	}
	return nil
}

func (m *mockTaskHandler) AddArtifact(taskID *string, artifact protocol.Artifact, isFinal bool, needMoreData bool) error {
	if m.addArtifactFunc != nil {
		return m.addArtifactFunc(taskID, artifact, isFinal, needMoreData)
	}
	return nil
}

func (m *mockTaskHandler) SubscribeTask(taskID *string) (taskmanager.TaskSubscriber, error) {
	if m.subscribeTaskFunc != nil {
		return m.subscribeTaskFunc(taskID)
	}
	return &mockTaskSubscriber{}, nil
}

func (m *mockTaskHandler) GetTask(taskID *string) (taskmanager.CancellableTask, error) {
	if m.getTaskFunc != nil {
		return m.getTaskFunc(taskID)
	}
	return nil, nil
}

func (m *mockTaskHandler) CleanTask(taskID *string) error {
	if m.cleanTaskFunc != nil {
		return m.cleanTaskFunc(taskID)
	}
	return nil
}

func (m *mockTaskHandler) GetMessageHistory() []protocol.Message {
	if m.getMessageHistoryFunc != nil {
		return m.getMessageHistoryFunc()
	}
	return []protocol.Message{}
}

func (m *mockTaskHandler) GetContextID() string {
	if m.getContextIDFunc != nil {
		return m.getContextIDFunc()
	}
	return "test-context-id"
}

func (m *mockTaskHandler) GetMetadata() (map[string]any, error) {
	if m.getMetadataFunc != nil {
		return m.getMetadataFunc()
	}
	return map[string]any{}, nil
}

// mockTaskSubscriber implements TaskSubscriber interface for testing
type mockTaskSubscriber struct {
	sendFunc    func(event protocol.StreamingMessageEvent) error
	channelFunc func() <-chan protocol.StreamingMessageEvent
	closedFunc  func() bool
	closeFunc   func()
	channel     chan protocol.StreamingMessageEvent
	closed      bool
}

func (m *mockTaskSubscriber) Send(event protocol.StreamingMessageEvent) error {
	if m.sendFunc != nil {
		return m.sendFunc(event)
	}
	if m.channel != nil {
		select {
		case m.channel <- event:
			return nil
		default:
			return nil
		}
	}
	return nil
}

func (m *mockTaskSubscriber) Channel() <-chan protocol.StreamingMessageEvent {
	if m.channelFunc != nil {
		return m.channelFunc()
	}
	if m.channel == nil {
		m.channel = make(chan protocol.StreamingMessageEvent, 10)
	}
	return m.channel
}

func (m *mockTaskSubscriber) Closed() bool {
	if m.closedFunc != nil {
		return m.closedFunc()
	}
	return m.closed
}

func (m *mockTaskSubscriber) Close() {
	if m.closeFunc != nil {
		m.closeFunc()
		return
	}
	m.closed = true
	if m.channel != nil {
		close(m.channel)
	}
}

// TestMessageProcessor_ProcessMessage tests the ProcessMessage method with table-driven approach
func TestMessageProcessor_ProcessMessage(t *testing.T) {
	ctxID := "ctx123"
	taskID := "task123"

	tests := []struct {
		name           string
		message        protocol.Message
		options        taskmanager.ProcessOptions
		setupHandler   func() *mockTaskHandler
		setupProcessor func() *messageProcessor
		expectError    bool
		errorContains  string
		validateResult func(*testing.T, *taskmanager.MessageProcessingResult)
	}{
		{
			name: "successful_message_processing",
			message: protocol.Message{
				Kind:      "message",
				MessageID: "msg123",
				ContextID: &ctxID,
				Role:      protocol.MessageRoleUser,
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Hello, world!"},
				},
			},
			options: taskmanager.ProcessOptions{
				Streaming: false,
			},
			setupHandler: func() *mockTaskHandler {
				return &mockTaskHandler{
					buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
						return "task-123", nil
					},
				}
			},
			setupProcessor: func() *messageProcessor {
				return createTestMessageProcessor()
			},
			expectError: false,
			validateResult: func(t *testing.T, result *taskmanager.MessageProcessingResult) {
				if result == nil {
					t.Error("Expected non-nil result")
					return
				}
				if result.Result == nil {
					t.Error("Expected non-nil result message")
					return
				}
				if result.StreamingEvents != nil {
					t.Error("Expected nil streaming events for non-streaming processing")
				}
				msg, ok := result.Result.(*protocol.Message)
				if !ok {
					t.Error("Expected protocol.Message type")
					return
				}
				if msg.Role != protocol.MessageRoleAgent {
					t.Errorf("Expected agent role, got: %v", msg.Role)
				}
				if len(msg.Parts) == 0 {
					t.Error("Expected non-empty message parts")
				}
			},
		},
		{
			name: "streaming_message_processing",
			message: protocol.Message{
				Kind:      "message",
				MessageID: "msg456",
				ContextID: &ctxID,
				TaskID:    &taskID,
				Role:      protocol.MessageRoleUser,
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Hello, streaming!"},
				},
			},
			options: taskmanager.ProcessOptions{
				Streaming: true,
			},
			setupHandler: func() *mockTaskHandler {
				return &mockTaskHandler{
					buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
						return "stream-task-123", nil
					},
					subscribeTaskFunc: func(taskID *string) (taskmanager.TaskSubscriber, error) {
						return &mockTaskSubscriber{}, nil
					},
				}
			},
			setupProcessor: func() *messageProcessor {
				return createTestMessageProcessor()
			},
			expectError: false,
			validateResult: func(t *testing.T, result *taskmanager.MessageProcessingResult) {
				if result == nil {
					t.Error("Expected non-nil result")
					return
				}
				if result.StreamingEvents == nil {
					t.Error("Expected non-nil streaming events for streaming processing")
					return
				}
				if result.Result != nil {
					t.Error("Expected nil result for streaming processing")
				}
				subscriber := result.StreamingEvents
				if subscriber == nil {
					t.Error("Expected non-nil subscriber")
					return
				}
				// Verify subscriber channel is available
				if subscriber.Channel() == nil {
					t.Error("Expected non-nil subscriber channel")
				}
			},
		},
		{
			name: "custom_error_handler_non_streaming",
			message: protocol.Message{
				Kind:      "message",
				MessageID: "msg789",
				ContextID: &ctxID,
				Role:      protocol.MessageRoleUser,
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Error test"},
				},
			},
			options: taskmanager.ProcessOptions{
				Streaming: false,
			},
			setupHandler: func() *mockTaskHandler {
				return &mockTaskHandler{
					buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
						return "", errors.New("build task failed")
					},
				}
			},
			setupProcessor: func() *messageProcessor {
				return createTestMessageProcessorWithCustomErrorHandler()
			},
			expectError: false,
			validateResult: func(t *testing.T, result *taskmanager.MessageProcessingResult) {
				if result == nil {
					t.Error("Expected non-nil result")
					return
				}
				if result.Result == nil {
					t.Error("Expected non-nil result message")
					return
				}
				// Verify custom error handler was used
				msg, ok := result.Result.(*protocol.Message)
				if !ok {
					t.Error("Expected protocol.Message type")
					return
				}
				if len(msg.Parts) == 0 {
					t.Error("Expected error message parts")
					return
				}
				textPart, ok := msg.Parts[0].(*protocol.TextPart)
				if !ok {
					t.Error("Expected text part")
					return
				}
				// The actual error will be "user is nil" since we don't set up auth context
				if textPart.Text != "Custom error: a2aserver: user is nil" {
					t.Errorf("Expected custom error message, got: %s", textPart.Text)
				}
			},
		},
		{
			name: "custom_error_handler_streaming",
			message: protocol.Message{
				Kind:      "message",
				MessageID: "msg890",
				ContextID: &ctxID,
				Role:      protocol.MessageRoleUser,
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Streaming error test"},
				},
			},
			options: taskmanager.ProcessOptions{
				Streaming: true,
			},
			setupHandler: func() *mockTaskHandler {
				return &mockTaskHandler{
					buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
						return "", errors.New("streaming build task failed")
					},
				}
			},
			setupProcessor: func() *messageProcessor {
				return createTestMessageProcessorWithCustomErrorHandler()
			},
			expectError: false,
			validateResult: func(t *testing.T, result *taskmanager.MessageProcessingResult) {
				if result == nil {
					t.Error("Expected non-nil result")
					return
				}
				if result.StreamingEvents == nil {
					t.Error("Expected non-nil streaming events")
					return
				}
				// Verify streaming subscriber was created for error handling
				subscriber := result.StreamingEvents
				// The subscriber should be available for error streaming
				if subscriber == nil {
					t.Error("Expected non-nil subscriber")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			handler := tt.setupHandler()
			processor := tt.setupProcessor()

			result, err := processor.ProcessMessage(ctx, tt.message, tt.options, handler)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				if tt.errorContains != "" && !containsString(err.Error(), tt.errorContains) {
					t.Errorf("Expected error to contain '%s', got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if tt.validateResult != nil {
					tt.validateResult(t, result)
				}
			}
		})
	}
}

// Helper functions for testing
func createTestMessageProcessor() *messageProcessor {
	return &messageProcessor{
		runner:              &mockRunner{},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &defaultEventToA2AMessage{},
		errorHandler:        defaultErrorHandler,
		debugLogging:        false,
	}
}

// createTestMessageProcessorWithCustomErrorHandler creates a message processor with custom error handler
func createTestMessageProcessorWithCustomErrorHandler() *messageProcessor {
	customErrorHandler := func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
		errorText := fmt.Sprintf("Custom error: %s", err.Error())
		errorMsg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
			&protocol.TextPart{Text: errorText},
		})
		return &errorMsg, nil
	}

	return &messageProcessor{
		runner:              &mockRunner{},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &defaultEventToA2AMessage{},
		errorHandler:        customErrorHandler,
		debugLogging:        false,
	}
}

// mockRunner for testing
type mockRunner struct {
	runFunc func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error)
}

func (m *mockRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, userID, sessionID, message, opts...)
	}
	// Return a channel with a simple completion event
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			ID:        "test-response-id",
			Object:    "chat.completion",
			Created:   time.Now().Unix(),
			Model:     "test-model",
			Choices:   []model.Choice{},
			Timestamp: time.Now(),
			Done:      true,
		},
		InvocationID: "test-invocation",
		Author:       "test-agent",
		ID:           "test-event-id",
		Timestamp:    time.Now(),
	}
	close(ch)
	return ch, nil
}

func (m *mockRunner) Close() error { return nil }

// errorA2AMessageConverter for testing conversion errors
type errorA2AMessageConverter struct{}

func (e *errorA2AMessageConverter) ConvertToAgentMessage(ctx context.Context, message protocol.Message) (*model.Message, error) {
	return nil, errors.New("conversion failed")
}

// containsString checks if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Additional test cases for better coverage
func TestMessageProcessor_HandleError(t *testing.T) {
	tests := []struct {
		name      string
		streaming bool
		error     error
		msg       *protocol.Message
	}{
		{
			name:      "non-streaming error",
			streaming: false,
			error:     errors.New("test error"),
			msg: &protocol.Message{
				MessageID: "test-msg",
				Role:      protocol.MessageRoleUser,
			},
		},
		{
			name:      "streaming error",
			streaming: true,
			error:     errors.New("streaming test error"),
			msg: &protocol.Message{
				MessageID: "test-msg-stream",
				Role:      protocol.MessageRoleUser,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := createTestMessageProcessor()
			ctx := context.Background()

			result, err := processor.handleError(ctx, tt.msg, tt.streaming, tt.error)

			if err != nil {
				t.Errorf("handleError() unexpected error: %v", err)
				return
			}

			if result == nil {
				t.Error("handleError() should return non-nil result")
				return
			}

			if tt.streaming {
				if result.StreamingEvents == nil {
					t.Error("handleError() should return streaming events for streaming error")
				}
				if result.Result != nil {
					t.Error("handleError() should not return result for streaming error")
				}
			} else {
				if result.Result == nil {
					t.Error("handleError() should return result for non-streaming error")
				}
				if result.StreamingEvents != nil {
					t.Error("handleError() should not return streaming events for non-streaming error")
				}
			}
		})
	}
}

func TestMessageProcessor_HandleError_ResponseRewriter(t *testing.T) {
	proc := &messageProcessor{
		responseRewriter: ResponseRewriterFuncs{
			Unary: func(result protocol.UnaryMessageResult) protocol.UnaryMessageResult {
				msg, ok := result.(*protocol.Message)
				if !ok {
					return result
				}
				delete(msg.Metadata, "debug_trace")
				return msg
			},
		},
		errorHandler: func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			result := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart("err")})
			result.Metadata = map[string]any{
				"debug_trace":   "drop-me",
				"business_code": "keep-me",
			}
			return &result, nil
		},
	}

	result, err := proc.handleError(context.Background(), &protocol.Message{MessageID: "err"}, false, errors.New("boom"))
	assert.NoError(t, err)
	msg, ok := result.Result.(*protocol.Message)
	if !assert.True(t, ok, "expected *protocol.Message, got %T", result.Result) {
		return
	}
	assert.Equal(t, "keep-me", msg.Metadata["business_code"])
	assert.NotContains(t, msg.Metadata, "debug_trace")
}

func TestMessageProcessor_HandleError_ResponseRewriter_DropUnaryResult(t *testing.T) {
	proc := &messageProcessor{
		responseRewriter: ResponseRewriterFuncs{
			Unary: func(result protocol.UnaryMessageResult) protocol.UnaryMessageResult {
				return nil
			},
		},
		errorHandler: func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			result := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart("err")})
			return &result, nil
		},
	}

	result, err := proc.handleError(context.Background(), &protocol.Message{MessageID: "err"}, false, errors.New("boom"))
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		assert.Nil(t, result.Result)
		assert.Nil(t, result.StreamingEvents)
	}
}

func TestMessageProcessor_HandleStreamingProcessingError_ResponseRewriter(t *testing.T) {
	proc := &messageProcessor{
		responseRewriter: ResponseRewriterFuncs{
			Streaming: func(result protocol.StreamingMessageResult) protocol.StreamingMessageResult {
				msg, ok := result.(*protocol.Message)
				if !ok {
					return result
				}
				delete(msg.Metadata, "debug_trace")
				return msg
			},
		},
		errorHandler: func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			result := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart("err")})
			result.Metadata = map[string]any{
				"debug_trace":   "drop-me",
				"business_code": "keep-me",
			}
			return &result, nil
		},
	}
	subscriber := &mockTaskSubscriber{channel: make(chan protocol.StreamingMessageEvent, 1)}

	err := proc.handleStreamingProcessingError(context.Background(), &protocol.Message{MessageID: "err-stream"}, subscriber, errors.New("boom"))
	assert.NoError(t, err)

	select {
	case event := <-subscriber.channel:
		msg, ok := event.Result.(*protocol.Message)
		if !assert.True(t, ok, "expected *protocol.Message, got %T", event.Result) {
			return
		}
		assert.Equal(t, "keep-me", msg.Metadata["business_code"])
		assert.NotContains(t, msg.Metadata, "debug_trace")
	default:
		t.Fatal("expected streaming error message event")
	}
}

func TestMessageProcessor_HandleStreamingProcessingError_ResponseRewriter_DropResult(
	t *testing.T,
) {
	proc := &messageProcessor{
		responseRewriter: ResponseRewriterFuncs{
			Streaming: func(
				result protocol.StreamingMessageResult,
			) protocol.StreamingMessageResult {
				return nil
			},
		},
		errorHandler: func(
			ctx context.Context,
			msg *protocol.Message,
			err error,
		) (*protocol.Message, error) {
			result := protocol.NewMessage(
				protocol.MessageRoleAgent,
				[]protocol.Part{protocol.NewTextPart("err")},
			)
			return &result, nil
		},
	}
	subscriber := &mockTaskSubscriber{
		channel: make(chan protocol.StreamingMessageEvent, 1),
	}

	err := proc.handleStreamingProcessingError(
		context.Background(),
		&protocol.Message{MessageID: "err-stream-drop"},
		subscriber,
		errors.New("boom"),
	)
	assert.NoError(t, err)

	select {
	case event := <-subscriber.channel:
		t.Fatalf("unexpected streaming error event: %#v", event)
	default:
	}
}

func TestMessageProcessor_HandleError_ResponseRewriter_DropStreamingResult(t *testing.T) {
	proc := &messageProcessor{
		responseRewriter: ResponseRewriterFuncs{
			Streaming: func(result protocol.StreamingMessageResult) protocol.StreamingMessageResult {
				return nil
			},
		},
		errorHandler: func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			result := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart("err")})
			return &result, nil
		},
	}

	result, err := proc.handleError(context.Background(), &protocol.Message{MessageID: "err"}, true, errors.New("boom"))
	assert.NoError(t, err)
	if !assert.NotNil(t, result) {
		return
	}
	if !assert.NotNil(t, result.StreamingEvents) {
		return
	}
	select {
	case event, ok := <-result.StreamingEvents.Channel():
		if ok {
			assert.Nil(t, event.Result)
			t.Fatal("expected dropped streaming result to emit no events")
		}
	default:
		t.Fatal("expected dropped streaming result subscriber to close immediately")
	}
}

func TestMessageProcessor_HandleError_HandlerFailure(t *testing.T) {
	proc := &messageProcessor{
		errorHandler: func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			return nil, fmt.Errorf("handler failure")
		},
	}
	msg := &protocol.Message{MessageID: "err"}
	_, err := proc.handleError(context.Background(), msg, false, errors.New("boom"))
	assert.Error(t, err)
}

func TestMessageProcessor_HandleStreamingProcessingError_HandlerFailure(t *testing.T) {
	proc := &messageProcessor{
		errorHandler: func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			return nil, fmt.Errorf("handler failure")
		},
	}
	msg := &protocol.Message{MessageID: "stream"}
	err := proc.handleStreamingProcessingError(context.Background(), msg, &mockTaskSubscriber{}, errors.New("boom"))
	assert.Error(t, err)
}

func TestMessageProcessor_HandleError_DebugLogging(t *testing.T) {
	proc := &messageProcessor{
		debugLogging: true,
		errorHandler: func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			res := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart("err")})
			return &res, nil
		},
	}
	msg := &protocol.Message{MessageID: "dbg"}
	res, err := proc.handleError(context.Background(), msg, false, errors.New("boom"))
	assert.NoError(t, err)
	assert.NotNil(t, res)
	assert.NotNil(t, res.Result)
	streamRes, err := proc.handleError(context.Background(), msg, true, errors.New("boom"))
	assert.NoError(t, err)
	assert.NotNil(t, streamRes)
	assert.NotNil(t, streamRes.StreamingEvents)
}

func TestIsFinalStreamingEventVariants(t *testing.T) {
	// nil and empty events are not final
	assert.False(t, isFinalStreamingEvent(nil))
	assert.False(t, isFinalStreamingEvent(&event.Event{}))

	// Regular done event is NOT final (we wait for runner.completion)
	base := &event.Event{Response: &model.Response{Done: false}}
	assert.False(t, isFinalStreamingEvent(base))

	// Tool calls are not final
	toolCall := &event.Event{Response: &model.Response{
		Done: true,
		Choices: []model.Choice{
			{Message: model.Message{ToolCalls: []model.ToolCall{{ID: "call"}}}},
		},
	}}
	assert.False(t, isFinalStreamingEvent(toolCall))

	// Tool role is not final
	toolRole := &event.Event{Response: &model.Response{
		Done: true,
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleTool}},
		},
	}}
	assert.False(t, isFinalStreamingEvent(toolRole))

	// Regular assistant response is NOT final (we wait for runner.completion)
	assistantResp := &event.Event{Response: &model.Response{
		Done: true,
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleAssistant}},
		},
	}}
	assert.False(t, isFinalStreamingEvent(assistantResp))

	// Only runner.completion is truly final
	runnerCompletion := &event.Event{Response: &model.Response{
		Done:   true,
		Object: model.ObjectTypeRunnerCompletion,
	}}
	assert.True(t, isFinalStreamingEvent(runnerCompletion))
}

func TestMessageProcessor_ProcessMessage_EdgeCases(t *testing.T) {
	ctxID := "edge-ctx"

	tests := []struct {
		name           string
		message        protocol.Message
		setupProcessor func() *messageProcessor
		expectError    bool
	}{
		{
			name: "conversion_error",
			message: protocol.Message{
				Kind:      "message",
				MessageID: "conv-error",
				ContextID: &ctxID,
				Role:      protocol.MessageRoleUser,
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "test"},
				},
			},
			setupProcessor: func() *messageProcessor {
				return &messageProcessor{
					runner:              &mockRunner{},
					a2aToAgentConverter: &errorA2AMessageConverter{},
					eventToA2AConverter: &defaultEventToA2AMessage{},
					errorHandler:        defaultErrorHandler,
					debugLogging:        false,
				}
			},
			expectError: false, // Should handle conversion error gracefully
		},
		{
			name: "runner_error",
			message: protocol.Message{
				Kind:      "message",
				MessageID: "runner-error",
				ContextID: &ctxID,
				Role:      protocol.MessageRoleUser,
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "test"},
				},
			},
			setupProcessor: func() *messageProcessor {
				return &messageProcessor{
					runner: &mockRunner{
						runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
							return nil, errors.New("runner failed")
						},
					},
					a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
					eventToA2AConverter: &defaultEventToA2AMessage{},
					errorHandler:        defaultErrorHandler,
					debugLogging:        false,
				}
			},
			expectError: false, // Should handle runner error gracefully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			handler := &mockTaskHandler{
				buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
					return "test-task", nil
				},
			}
			processor := tt.setupProcessor()
			options := taskmanager.ProcessOptions{Streaming: false}

			result, err := processor.ProcessMessage(ctx, tt.message, options, handler)

			if tt.expectError {
				if err == nil {
					t.Error("ProcessMessage() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("ProcessMessage() unexpected error: %v", err)
				}
				if result == nil {
					t.Error("ProcessMessage() should return non-nil result")
				}
			}
		})
	}
}

func TestMessageProcessor_ProcessMessage_ContextIDMissing(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: "user"})
	proc := createTestMessageProcessor()
	msg := protocol.Message{
		MessageID: "missing",
		Role:      protocol.MessageRoleUser,
	}
	result, err := proc.ProcessMessage(ctx, msg, taskmanager.ProcessOptions{}, &mockTaskHandler{})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Result)
}

func TestMessageProcessor_HandleStreamingProcessingError(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx-stream"
	msg := &protocol.Message{ContextID: &ctxID}
	processor := createTestMessageProcessor()
	processor.debugLogging = true

	t.Run("send_failure", func(t *testing.T) {
		sub := &mockTaskSubscriber{
			sendFunc: func(protocol.StreamingMessageEvent) error {
				return fmt.Errorf("send failure")
			},
		}
		err := processor.handleStreamingProcessingError(ctx, msg, sub, errors.New("run failed"))
		assert.Error(t, err)
	})

	t.Run("success_path", func(t *testing.T) {
		var received protocol.StreamingMessageEvent
		sub := &mockTaskSubscriber{
			sendFunc: func(evt protocol.StreamingMessageEvent) error {
				received = evt
				return nil
			},
		}
		err := processor.handleStreamingProcessingError(ctx, msg, sub, errors.New("upstream err"))
		assert.NoError(t, err)
		assert.NotNil(t, received.Result)
	})
}

func TestMessageProcessor_ProcessStreamingMessage_Errors(t *testing.T) {
	ctx := context.Background()
	ctxID := "stream-ctx"
	msg := &protocol.Message{ContextID: &ctxID}

	t.Run("nil_agent_message", func(t *testing.T) {
		processor := createTestMessageProcessor()
		handler := &mockTaskHandler{}

		result, err := processor.processStreamingMessage(ctx, "user", "session", msg, nil, handler, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.StreamingEvents)
	})

	t.Run("runner_error", func(t *testing.T) {
		var closed bool
		var cleanedTaskID string
		sub := &mockTaskSubscriber{
			closeFunc: func() { closed = true },
		}
		handler := &mockTaskHandler{
			buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
				return "task-id", nil
			},
			subscribeTaskFunc: func(taskID *string) (taskmanager.TaskSubscriber, error) {
				return sub, nil
			},
			cleanTaskFunc: func(taskID *string) error {
				cleanedTaskID = *taskID
				return nil
			},
		}
		processor := createTestMessageProcessor()
		processor.runner = &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				return nil, errors.New("runner failure")
			},
		}
		agentMsg := &model.Message{Role: model.RoleUser, Content: "input"}

		result, err := processor.processStreamingMessage(ctx, "user", "session", msg, agentMsg, handler, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.StreamingEvents)
		assert.True(t, closed)
		assert.Equal(t, "task-id", cleanedTaskID)
	})

	t.Run("build_task_error", func(t *testing.T) {
		processor := createTestMessageProcessor()
		handler := &mockTaskHandler{
			buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
				return "", fmt.Errorf("build failed")
			},
		}
		result, err := processor.processStreamingMessage(ctx, "user", "session", msg, &model.Message{}, handler, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.StreamingEvents)
	})

	t.Run("subscribe_error", func(t *testing.T) {
		processor := createTestMessageProcessor()
		var cleanedTaskID string
		handler := &mockTaskHandler{
			buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
				return "task", nil
			},
			subscribeTaskFunc: func(taskID *string) (taskmanager.TaskSubscriber, error) {
				return nil, fmt.Errorf("subscribe failed")
			},
			cleanTaskFunc: func(taskID *string) error {
				cleanedTaskID = *taskID
				return nil
			},
		}
		result, err := processor.processStreamingMessage(ctx, "user", "session", msg, &model.Message{}, handler, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.StreamingEvents)
		assert.Equal(t, "task", cleanedTaskID)
	})

	t.Run("success_path", func(t *testing.T) {
		processor := createTestMessageProcessor()
		processor.debugLogging = true
		eventCh := make(chan *event.Event, 1)
		eventCh <- &event.Event{
			Response: &model.Response{
				Choices: []model.Choice{
					{Delta: model.Message{Content: "chunk"}},
				},
			},
		}
		close(eventCh)
		processor.runner = &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				return eventCh, nil
			},
		}
		handler := &mockTaskHandler{
			buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
				return "task", nil
			},
			subscribeTaskFunc: func(taskID *string) (taskmanager.TaskSubscriber, error) {
				return &mockTaskSubscriber{}, nil
			},
			cleanTaskFunc: func(taskID *string) error { return nil },
		}
		result, err := processor.processStreamingMessage(ctx, "user", "session", msg, &model.Message{Content: "input"}, handler, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.StreamingEvents)
	})
}

func TestMessageProcessor_ProcessBatchStreamingEvents(t *testing.T) {
	ctx := context.Background()
	ctxID := "batch-ctx"
	msg := &protocol.Message{ContextID: &ctxID, MessageID: "msg"}
	taskID := "tid"

	t.Run("empty_batch", func(t *testing.T) {
		proc := createTestMessageProcessor()
		sub := &mockTaskSubscriber{}
		cont, err := proc.processBatchStreamingEvents(
			ctx,
			taskID,
			msg,
			[]*event.Event{},
			sub,
			nil,
			nil,
		)
		assert.NoError(t, err)
		assert.True(t, cont)
	})

	t.Run("nil_event_entries", func(t *testing.T) {
		proc := createTestMessageProcessor()
		sub := &mockTaskSubscriber{}
		batch := []*event.Event{{}, nil}
		cont, err := proc.processBatchStreamingEvents(
			ctx,
			taskID,
			msg,
			batch,
			sub,
			nil,
			nil,
		)
		assert.NoError(t, err)
		assert.True(t, cont)
	})

	t.Run("converter_error", func(t *testing.T) {
		proc := createTestMessageProcessor()
		proc.eventToA2AConverter = streamingErrorConverter{}
		sub := &mockTaskSubscriber{}
		evt := &event.Event{Response: &model.Response{}}
		_, err := proc.processBatchStreamingEvents(
			ctx,
			taskID,
			msg,
			[]*event.Event{evt},
			sub,
			nil,
			nil,
		)
		assert.Error(t, err)
	})

	t.Run("send_error", func(t *testing.T) {
		proc := createTestMessageProcessor()
		sendErrSub := &mockTaskSubscriber{
			sendFunc: func(protocol.StreamingMessageEvent) error {
				return fmt.Errorf("send error")
			},
		}
		evt := &event.Event{Response: &model.Response{
			Choices: []model.Choice{
				{Delta: model.Message{Content: "chunk"}},
			},
		}}
		_, err := proc.processBatchStreamingEvents(
			ctx,
			taskID,
			msg,
			[]*event.Event{evt},
			sendErrSub,
			nil,
			nil,
		)
		assert.Error(t, err)
	})

	t.Run("final_event_stops", func(t *testing.T) {
		proc := createTestMessageProcessor()
		sub := &mockTaskSubscriber{}
		// Only runner.completion is treated as final event
		final := &event.Event{
			Response: &model.Response{
				Object: model.ObjectTypeRunnerCompletion,
				Done:   true,
			},
			StateDelta: map[string][]byte{
				"last_response":              []byte(`"final"`),
				graph.StateKeyLastResponseID: []byte(`"resp-final"`),
			},
		}
		var finalMetadata map[string]any
		cont, err := proc.processBatchStreamingEvents(
			ctx,
			taskID,
			msg,
			[]*event.Event{final},
			sub,
			nil,
			&finalMetadata,
		)
		assert.NoError(t, err)
		assert.False(t, cont)
		assert.Equal(t, "resp-final", finalMetadata[ia2a.MessageMetadataResponseIDKey])
		rawStateDelta, ok := finalMetadata[ia2a.MessageMetadataStateDeltaKey]
		if assert.True(t, ok, "expected state_delta metadata") {
			decoded := ia2a.DecodeStateDeltaMetadata(rawStateDelta)
			assert.Equal(t, []byte(`"final"`), decoded["last_response"])
		}
	})
}

type streamingErrorConverter struct{}

func (streamingErrorConverter) ConvertToA2AMessage(ctx context.Context, event *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
	return nil, nil
}

func (streamingErrorConverter) ConvertStreamingToA2AMessage(ctx context.Context, event *event.Event, options EventToA2AStreamingOptions) (protocol.StreamingMessageResult, error) {
	return nil, errors.New("stream conversion failed")
}

func TestMessageProcessor_ProcessMessage_NilAgentMessage(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	processor := createTestMessageProcessor()

	result, err := processor.processMessage(ctx, "user", "session", msg, nil, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestMessageProcessor_ProcessMessage_Success(t *testing.T) {
	ctxID := "ctx"
	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: "user"})
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "success",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}
	processor := &messageProcessor{
		debugLogging: true,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "response"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{},
		errorHandler:        defaultErrorHandler,
	}
	handler := &mockTaskHandler{
		buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
			return "task", nil
		},
	}
	res, err := processor.ProcessMessage(ctx, msg, taskmanager.ProcessOptions{Streaming: false}, handler)
	assert.NoError(t, err)
	assert.NotNil(t, res)
	assert.NotNil(t, res.Result)
}

func TestProcessAgentStreamingEvents_SendFailure(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	events := make(chan *event.Event)
	close(events)

	var handlerCalled bool
	proc := createTestMessageProcessor()
	proc.errorHandler = func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
		handlerCalled = true
		res := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart("error")})
		return &res, nil
	}

	sendCount := 0
	sub := &mockTaskSubscriber{
		sendFunc: func(protocol.StreamingMessageEvent) error {
			sendCount++
			if sendCount == 1 {
				return fmt.Errorf("send fail")
			}
			return nil
		},
	}

	var cleaned bool
	handler := &mockTaskHandler{
		cleanTaskFunc: func(taskID *string) error {
			cleaned = true
			return nil
		},
	}

	proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, handler)
	assert.True(t, handlerCalled)
	assert.True(t, cleaned)
}

func TestProcessAgentStreamingEvents_FinalSendFailureAndCleanFailure(
	t *testing.T,
) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	events := make(chan *event.Event)
	close(events)

	proc := createTestMessageProcessor()

	sendCount := 0
	sub := &mockTaskSubscriber{
		sendFunc: func(protocol.StreamingMessageEvent) error {
			sendCount++
			if sendCount == 2 || sendCount == 3 {
				return fmt.Errorf("send fail")
			}
			return nil
		},
	}

	var cleaned bool
	handler := &mockTaskHandler{
		cleanTaskFunc: func(taskID *string) error {
			cleaned = true
			return fmt.Errorf("clean fail")
		},
	}

	assert.NotPanics(t, func() {
		proc.processAgentStreamingEvents(
			ctx,
			"task",
			"user1",
			"session1",
			msg,
			events,
			sub,
			handler,
		)
	})
	assert.True(t, cleaned)
}

func TestProcessAgentStreamingEvents_MessageTypeSkipsFinalArtifact(
	t *testing.T,
) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	events := make(chan *event.Event)
	close(events)

	proc := createTestMessageProcessor()
	proc.streamingEventType = StreamingEventTypeMessage

	var results []protocol.StreamingMessageResult
	sub := &mockTaskSubscriber{
		sendFunc: func(evt protocol.StreamingMessageEvent) error {
			if evt.Result != nil {
				results = append(results, evt.Result)
			}
			return nil
		},
	}

	proc.processAgentStreamingEvents(
		ctx,
		"task",
		"user1",
		"session1",
		msg,
		events,
		sub,
		&mockTaskHandler{},
	)

	if len(results) != 2 {
		t.Fatalf("expected 2 events, got %d", len(results))
	}

	if _, ok := results[0].(*protocol.TaskStatusUpdateEvent); !ok {
		t.Fatalf("expected TaskStatusUpdateEvent, got %T", results[0])
	}
	if _, ok := results[1].(*protocol.TaskStatusUpdateEvent); !ok {
		t.Fatalf("expected TaskStatusUpdateEvent, got %T", results[1])
	}

	for i, res := range results {
		if _, ok := res.(*protocol.TaskArtifactUpdateEvent); ok {
			t.Fatalf(
				"did not expect TaskArtifactUpdateEvent at %d",
				i,
			)
		}
	}
}

func TestMessageProcessor_ProcessMessage_Streaming_BuildTaskError(
	t *testing.T,
) {
	ctxID := "ctx"
	msg := protocol.Message{
		Kind:      "message",
		MessageID: "msg-build-task",
		ContextID: &ctxID,
		Role:      protocol.MessageRoleUser,
		Parts: []protocol.Part{
			protocol.NewTextPart("hi"),
		},
	}

	ctx := context.WithValue(
		context.Background(),
		auth.AuthUserKey,
		&auth.User{ID: ""},
	)

	proc := createTestMessageProcessor()
	proc.debugLogging = true

	handler := &mockTaskHandler{
		buildTaskFunc: func(
			specificTaskID *string,
			contextID *string,
		) (string, error) {
			return "", fmt.Errorf("build task failed")
		},
	}

	res, err := proc.ProcessMessage(
		ctx,
		msg,
		taskmanager.ProcessOptions{Streaming: true},
		handler,
	)
	assert.NoError(t, err)
	assert.NotNil(t, res)
}

func TestMessageProcessor_ProcessMessage_ConversionError_WithAuthUser(
	t *testing.T,
) {
	ctxID := "ctx"
	msg := protocol.Message{
		Kind:      "message",
		MessageID: "conv-err-auth",
		ContextID: &ctxID,
		Role:      protocol.MessageRoleUser,
		Parts: []protocol.Part{
			protocol.NewTextPart("hi"),
		},
	}
	ctx := context.WithValue(
		context.Background(),
		auth.AuthUserKey,
		&auth.User{ID: "user"},
	)

	proc := createTestMessageProcessor()
	proc.a2aToAgentConverter = &errorA2AMessageConverter{}
	handler := &mockTaskHandler{
		buildTaskFunc: func(
			specificTaskID *string,
			contextID *string,
		) (string, error) {
			return "task", nil
		},
	}

	res, err := proc.ProcessMessage(
		ctx,
		msg,
		taskmanager.ProcessOptions{Streaming: false},
		handler,
	)
	assert.NoError(t, err)
	assert.NotNil(t, res)
	assert.NotNil(t, res.Result)
}

func TestMessageProcessor_ProcessMessage_RunnerError_WithAuthUser(
	t *testing.T,
) {
	ctxID := "ctx"
	msg := protocol.Message{
		Kind:      "message",
		MessageID: "runner-err-auth",
		ContextID: &ctxID,
		Role:      protocol.MessageRoleUser,
		Parts: []protocol.Part{
			protocol.NewTextPart("hi"),
		},
	}
	ctx := context.WithValue(
		context.Background(),
		auth.AuthUserKey,
		&auth.User{ID: "user"},
	)

	proc := createTestMessageProcessor()
	proc.runner = &mockRunner{
		runFunc: func(
			ctx context.Context,
			userID string,
			sessionID string,
			message model.Message,
			opts ...agent.RunOption,
		) (<-chan *event.Event, error) {
			return nil, errors.New("runner failed")
		},
	}
	handler := &mockTaskHandler{
		buildTaskFunc: func(
			specificTaskID *string,
			contextID *string,
		) (string, error) {
			return "task", nil
		},
	}

	res, err := proc.ProcessMessage(
		ctx,
		msg,
		taskmanager.ProcessOptions{Streaming: false},
		handler,
	)
	assert.NoError(t, err)
	assert.NotNil(t, res)
	assert.NotNil(t, res.Result)
}

func TestProcessAgentStreamingEvents_ConverterError(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	events := make(chan *event.Event, 1)
	events <- &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Delta: model.Message{Content: "chunk"}},
			},
		},
	}
	close(events)

	var handlerCalled bool
	proc := createTestMessageProcessor()
	proc.eventToA2AConverter = streamingErrorConverter{}
	proc.errorHandler = func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
		handlerCalled = true
		res := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart("err")})
		return &res, nil
	}

	var results []protocol.StreamingMessageResult
	sub := &mockTaskSubscriber{
		sendFunc: func(evt protocol.StreamingMessageEvent) error {
			if evt.Result != nil {
				results = append(results, evt.Result)
			}
			return nil
		},
	}
	proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, &mockTaskHandler{})
	assert.True(t, handlerCalled)
	assert.Len(t, results, 2)
	_, isSubmitted := results[0].(*protocol.TaskStatusUpdateEvent)
	assert.True(t, isSubmitted)
	_, isMessage := results[1].(*protocol.Message)
	assert.True(t, isMessage)
}

func TestProcessAgentStreamingEvents_ContextCanceledSkipsCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	events := make(chan *event.Event)
	close(events)

	proc := createTestMessageProcessor()

	var results []protocol.StreamingMessageResult
	sub := &mockTaskSubscriber{
		sendFunc: func(evt protocol.StreamingMessageEvent) error {
			if evt.Result != nil {
				results = append(results, evt.Result)
			}
			return nil
		},
	}

	proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, &mockTaskHandler{})
	assert.Len(t, results, 1)
	status, ok := results[0].(*protocol.TaskStatusUpdateEvent)
	assert.True(t, ok)
	assert.Equal(t, protocol.TaskStateSubmitted, status.Status.State)
}

func TestProcessAgentStreamingEvents_Success(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	events := make(chan *event.Event, 1)
	events <- &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{Delta: model.Message{Content: "chunk"}}},
		},
	}
	close(events)

	sub := &mockTaskSubscriber{}
	handler := &mockTaskHandler{
		cleanTaskFunc: func(taskID *string) error { return nil },
	}

	processor := createTestMessageProcessor()
	processor.debugLogging = true
	ch := sub.Channel()
	processor.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, handler)

	count := 0
	for evt := range ch {
		if evt.Result != nil {
			count++
		}
	}
	assert.NotZero(t, count)
}

// TestMessageProcessor_ProcessMessage_EmptyUserID tests the userID generation when user.ID is empty
func TestMessageProcessor_ProcessMessage_EmptyUserID(t *testing.T) {
	ctxID := "test-context-123"

	tests := []struct {
		name           string
		userID         string
		expectedUserID string
		validateFunc   func(t *testing.T, capturedUserID string)
	}{
		{
			name:           "empty_user_id_generates_from_context",
			userID:         "", // Empty user ID
			expectedUserID: "A2A_USER_test-context-123",
			validateFunc: func(t *testing.T, capturedUserID string) {
				if capturedUserID != "A2A_USER_test-context-123" {
					t.Errorf("Expected generated userID 'A2A_USER_test-context-123', got '%s'", capturedUserID)
				}
			},
		},
		{
			name:           "non_empty_user_id_uses_original",
			userID:         "actual-user-456",
			expectedUserID: "actual-user-456",
			validateFunc: func(t *testing.T, capturedUserID string) {
				if capturedUserID != "actual-user-456" {
					t.Errorf("Expected original userID 'actual-user-456', got '%s'", capturedUserID)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedUserID string

			// Create a mock runner that captures the userID
			mockRunner := &mockRunner{
				runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
					capturedUserID = userID
					ch := make(chan *event.Event, 1)
					ch <- &event.Event{
						Response: &model.Response{
							Choices: []model.Choice{{Message: model.Message{Content: "response"}}},
						},
					}
					close(ch)
					return ch, nil
				},
			}

			processor := &messageProcessor{
				runner:              mockRunner,
				a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
				eventToA2AConverter: &defaultEventToA2AMessage{},
				errorHandler:        defaultErrorHandler,
				debugLogging:        true,
			}

			// Create context with user having the specified ID
			ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: tt.userID})

			msg := protocol.Message{
				ContextID: &ctxID,
				MessageID: "test-msg",
				Role:      protocol.MessageRoleUser,
				Parts:     []protocol.Part{protocol.NewTextPart("test message")},
			}

			handler := &mockTaskHandler{
				buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
					return "task-id", nil
				},
			}

			result, err := processor.ProcessMessage(ctx, msg, taskmanager.ProcessOptions{Streaming: false}, handler)

			if err != nil {
				t.Errorf("ProcessMessage() unexpected error: %v", err)
				return
			}

			if result == nil {
				t.Error("ProcessMessage() returned nil result")
				return
			}

			// Validate the captured userID
			if tt.validateFunc != nil {
				tt.validateFunc(t, capturedUserID)
			}
		})
	}
}

// TestMessageProcessor_ProcessStreamingMessage_EmptyUserID tests userID generation in streaming mode
func TestMessageProcessor_ProcessStreamingMessage_EmptyUserID(t *testing.T) {
	ctxID := "stream-context-789"

	var capturedUserID string

	mockRunner := &mockRunner{
		runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
			capturedUserID = userID
			ch := make(chan *event.Event, 1)
			ch <- &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{{Delta: model.Message{Content: "chunk"}}},
				},
			}
			close(ch)
			return ch, nil
		},
	}

	processor := &messageProcessor{
		runner:              mockRunner,
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &defaultEventToA2AMessage{},
		errorHandler:        defaultErrorHandler,
		debugLogging:        false,
	}

	// Create context with user having empty ID
	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: ""})

	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "stream-msg",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("streaming test")},
	}

	handler := &mockTaskHandler{
		buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
			return "stream-task-id", nil
		},
		subscribeTaskFunc: func(taskID *string) (taskmanager.TaskSubscriber, error) {
			return &mockTaskSubscriber{}, nil
		},
		cleanTaskFunc: func(taskID *string) error {
			return nil
		},
	}

	result, err := processor.ProcessMessage(ctx, msg, taskmanager.ProcessOptions{Streaming: true}, handler)

	if err != nil {
		t.Errorf("ProcessMessage() unexpected error: %v", err)
		return
	}

	if result == nil {
		t.Error("ProcessMessage() returned nil result")
		return
	}

	// Verify the userID was generated from context ID
	expectedUserID := "A2A_USER_stream-context-789"
	if capturedUserID != expectedUserID {
		t.Errorf("Expected generated userID '%s', got '%s'", expectedUserID, capturedUserID)
	}
}

func TestBuildA2AServer_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		options     *options
		expectError bool
		errorMsg    string
	}{
		{
			name: "task_manager_creation_error",
			options: &options{
				agent:           &mockAgent{name: "test", description: "test"},
				sessionService:  &mockSessionService{},
				host:            "localhost:8080",
				errorHandler:    defaultErrorHandler,
				enableStreaming: false,
				// No custom task manager builder, will use default which might fail
			},
			expectError: false, // Default task manager should work
		},
		{
			name: "custom_task_manager_builder_returns_nil",
			options: &options{
				agent:          &mockAgent{name: "test", description: "test"},
				sessionService: &mockSessionService{},
				host:           "localhost:8080",
				errorHandler:   defaultErrorHandler,
				taskManagerBuilder: func(processor taskmanager.MessageProcessor) taskmanager.TaskManager {
					return nil // This should cause an error
				},
			},
			expectError: true,
			errorMsg:    "NewA2AServer requires a non-nil taskManager",
		},
		{
			name: "custom_processor_builder",
			options: &options{
				agent:          &mockAgent{name: "test", description: "test"},
				sessionService: &mockSessionService{},
				host:           "localhost:8080",
				errorHandler:   defaultErrorHandler,
				processorBuilder: func(agent agent.Agent, sessionService session.Service) taskmanager.MessageProcessor {
					return &mockTaskManager{}
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := buildA2AServer(tt.options)

			if tt.expectError {
				if err == nil {
					t.Error("buildA2AServer() expected error but got none")
				}
				if tt.errorMsg != "" && !containsString(err.Error(), tt.errorMsg) {
					t.Errorf("buildA2AServer() error = %v, should contain %v", err, tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("buildA2AServer() unexpected error: %v", err)
				}
				if server == nil {
					t.Error("buildA2AServer() should return non-nil server")
				}
			}
		})
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		opts    []Option
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid configuration",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithHost("localhost:8080"),
			},
			wantErr: false,
		},
		{
			name: "with custom session service",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithHost("localhost:8080"),
				WithSessionService(&mockSessionService{}),
			},
			wantErr: false,
		},
		{
			name: "with custom converters",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithHost("localhost:8080"),
				WithA2AToAgentConverter(&mockA2AToAgentConverter{}),
				WithEventToA2AConverter(&mockEventToA2AConverter{}),
			},
			wantErr: false,
		},
		{
			name: "with process message hook",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithHost("localhost:8080"),
				WithProcessMessageHook(func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
					return next
				}),
			},
			wantErr: false,
		},
		{
			name: "missing agent",
			opts: []Option{
				WithHost("localhost:9090"),
			},
			wantErr: true,
			errMsg:  "either agent (WithAgent) or runner (WithRunner) is required",
		},
		{
			name: "missing host without agent card",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithHost(""),
			},
			wantErr: true,
			errMsg:  "host is required when agent card is not provided",
		},
		{
			name: "agent and runner cannot be used together",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithRunner(&mockRunner{}),
				WithAgentCard(a2a.AgentCard{
					Name:        "custom-agent",
					Description: "custom description",
					URL:         "http://custom.example.com",
				}),
			},
			wantErr: true,
			errMsg:  "WithAgent and WithRunner cannot be used together; use WithAgentCard with WithRunner",
		},
		{
			name: "with agent card but no host - should succeed",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithAgentCard(a2a.AgentCard{
					Name:        "custom-agent",
					Description: "custom description",
					URL:         "http://custom.example.com",
				}),
			},
			wantErr: false,
		},
		{
			name: "runner with agent card but no host - should succeed",
			opts: []Option{
				WithRunner(&mockRunner{}),
				WithAgentCard(a2a.AgentCard{
					Name:        "runner-agent",
					Description: "runner description",
					URL:         "http://runner.example.com",
				}),
			},
			wantErr: false,
		},
		{
			name:    "no options",
			opts:    []Option{},
			wantErr: true,
			errMsg:  "either agent (WithAgent) or runner (WithRunner) is required",
		},
		{
			name: "runner without agent card",
			opts: []Option{
				WithRunner(&mockRunner{}),
			},
			wantErr: true,
			errMsg:  "agent card (WithAgentCard) is required when using runner without agent",
		},
		{
			name: "buildAgentCard error - empty agent name",
			opts: []Option{
				WithAgent(&mockAgent{name: "", description: "test"}, true),
				WithHost("localhost:8080"),
			},
			wantErr: true,
		},
		{
			name: "explicit agent card with empty name",
			opts: []Option{
				WithAgent(&mockAgent{name: "test", description: "desc"}, true),
				WithAgentCard(a2a.AgentCard{
					Name:        "",
					Description: "desc",
					URL:         "http://localhost:8080",
				}),
			},
			wantErr: true,
			errMsg:  "agent card name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := New(tt.opts...)
			if tt.wantErr {
				if err == nil {
					t.Errorf("New() expected error but got none")
					return
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("New() error = %v, want %v", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("New() unexpected error = %v", err)
					return
				}
				if server == nil {
					t.Errorf("New() returned nil server")
				}
			}
		})
	}
}

func TestBuildAgentCard(t *testing.T) {
	tests := []struct {
		name     string
		options  *options
		expected a2a.AgentCard
	}{
		{
			name: "agent with no tools - host only",
			options: &options{
				agent: &mockAgent{
					name:        "test-agent",
					description: "test description",
					tools:       []tool.Tool{},
				},
				host:            "localhost:8080",
				enableStreaming: true,
			},
			expected: a2a.AgentCard{
				Name:        "test-agent",
				Description: "test description",
				URL:         "http://localhost:8080",
				Capabilities: a2a.AgentCapabilities{
					Streaming:  boolPtr(true),
					Extensions: defaultAgentCardExtensions(),
				},
				Skills: []a2a.AgentSkill{
					{
						Name:        "test-agent",
						Description: stringPtr("test description"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"default"},
					},
				},
				DefaultInputModes:  []string{"text"},
				DefaultOutputModes: []string{"text"},
			},
		},
		{
			name: "agent with http URL",
			options: &options{
				agent: &mockAgent{
					name:        "test-agent",
					description: "test description",
					tools:       []tool.Tool{},
				},
				host:            "http://example.com:8080",
				enableStreaming: true,
			},
			expected: a2a.AgentCard{
				Name:        "test-agent",
				Description: "test description",
				URL:         "http://example.com:8080",
				Capabilities: a2a.AgentCapabilities{
					Streaming:  boolPtr(true),
					Extensions: defaultAgentCardExtensions(),
				},
				Skills: []a2a.AgentSkill{
					{
						Name:        "test-agent",
						Description: stringPtr("test description"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"default"},
					},
				},
				DefaultInputModes:  []string{"text"},
				DefaultOutputModes: []string{"text"},
			},
		},
		{
			name: "agent with https URL",
			options: &options{
				agent: &mockAgent{
					name:        "test-agent",
					description: "test description",
					tools:       []tool.Tool{},
				},
				host:            "https://secure.example.com",
				enableStreaming: true,
			},
			expected: a2a.AgentCard{
				Name:        "test-agent",
				Description: "test description",
				URL:         "https://secure.example.com",
				Capabilities: a2a.AgentCapabilities{
					Streaming:  boolPtr(true),
					Extensions: defaultAgentCardExtensions(),
				},
				Skills: []a2a.AgentSkill{
					{
						Name:        "test-agent",
						Description: stringPtr("test description"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"default"},
					},
				},
				DefaultInputModes:  []string{"text"},
				DefaultOutputModes: []string{"text"},
			},
		},
		{
			name: "agent with custom URL",
			options: &options{
				agent: &mockAgent{
					name:        "custom-agent",
					description: "agent with custom scheme",
					tools:       []tool.Tool{},
				},
				host:            "custom://service.namespace",
				enableStreaming: true,
			},
			expected: a2a.AgentCard{
				Name:        "custom-agent",
				Description: "agent with custom scheme",
				URL:         "custom://service.namespace",
				Capabilities: a2a.AgentCapabilities{
					Streaming:  boolPtr(true),
					Extensions: defaultAgentCardExtensions(),
				},
				Skills: []a2a.AgentSkill{
					{
						Name:        "custom-agent",
						Description: stringPtr("agent with custom scheme"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"default"},
					},
				},
				DefaultInputModes:  []string{"text"},
				DefaultOutputModes: []string{"text"},
			},
		},
		{
			name: "agent with tools",
			options: &options{
				agent: &mockAgent{
					name:        "tool-agent",
					description: "agent with tools",
					tools: []tool.Tool{
						&mockTool{name: "calculator", description: "math tool"},
						&mockTool{name: "weather", description: "weather tool"},
					},
				},
				host:            "localhost:9090",
				enableStreaming: false,
			},
			expected: a2a.AgentCard{
				Name:        "tool-agent",
				Description: "agent with tools",
				URL:         "http://localhost:9090",
				Capabilities: a2a.AgentCapabilities{
					Streaming:  boolPtr(false),
					Extensions: defaultAgentCardExtensions(),
				},
				Skills: []a2a.AgentSkill{
					{
						Name:        "tool-agent",
						Description: stringPtr("agent with tools"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"default"},
					},
					{
						Name:        "calculator",
						Description: stringPtr("math tool"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"tool"},
					},
					{
						Name:        "weather",
						Description: stringPtr("weather tool"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"tool"},
					},
				},
				DefaultInputModes:  []string{"text"},
				DefaultOutputModes: []string{"text"},
			},
		},
		{
			name: "custom agent card",
			options: &options{
				agent: &mockAgent{
					name:        "custom-agent",
					description: "custom description",
				},
				host: "localhost:8080",
				agentCard: &a2a.AgentCard{
					Name:        "override-name",
					Description: "override description",
					URL:         "http://override.com",
				},
			},
			expected: a2a.AgentCard{
				Name:        "override-name",
				Description: "override description",
				URL:         "http://override.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildAgentCard(tt.options)
			if err != nil {
				t.Fatalf("buildAgentCard() returned error: %v", err)
			}
			if !compareAgentCards(result, tt.expected) {
				t.Errorf("buildAgentCard() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestBuildProcessor(t *testing.T) {
	tests := []struct {
		name           string
		agent          agent.Agent
		session        session.Service
		serverIdentity string
		options        *options
	}{
		{
			name:           "default converters",
			agent:          &mockAgent{name: "test-agent", description: "test description"},
			session:        inmemory.NewSessionService(),
			serverIdentity: "test-agent",
			options: &options{
				agent: &mockAgent{name: "test-agent", description: "test description"},
			},
		},
		{
			name:           "custom converters",
			agent:          &mockAgent{name: "test-agent", description: "test description"},
			session:        inmemory.NewSessionService(),
			serverIdentity: "test-agent",
			options: &options{
				agent:               &mockAgent{name: "test-agent", description: "test description"},
				a2aToAgentConverter: &mockA2AToAgentConverter{},
				eventToA2AConverter: &mockEventToA2AConverter{},
			},
		},
		{
			name:           "custom runner",
			session:        inmemory.NewSessionService(),
			serverIdentity: "runner-agent",
			options: &options{
				runner: &mockRunner{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor, err := buildProcessor(tt.agent, tt.session, tt.serverIdentity, tt.options)
			if err != nil {
				t.Fatalf("buildProcessor() returned error: %v", err)
			}
			if processor == nil {
				t.Errorf("buildProcessor() returned nil")
				return
			}
			if processor.runner == nil {
				t.Errorf("buildProcessor() runner is nil")
			}
			if processor.a2aToAgentConverter == nil {
				t.Errorf("buildProcessor() a2aToAgentConverter is nil")
			}
			if processor.eventToA2AConverter == nil {
				t.Errorf("buildProcessor() eventToA2AConverter is nil")
			}
			if processor.agentName != tt.serverIdentity {
				t.Errorf("buildProcessor() agentName = %q, want %q", processor.agentName, tt.serverIdentity)
			}
			if tt.options.runner != nil &&
				processor.runner != tt.options.runner {
				t.Errorf("buildProcessor() should reuse custom runner")
			}
		})
	}
}

func TestBuildProcessor_RunnerWithoutAgent(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "runner-only-agent",
		Description: "agent provided via runner only",
		URL:         "http://localhost:9090",
	}
	processor, err := buildProcessor(nil, inmemory.NewSessionService(), card.Name, &options{
		runner:    &mockRunner{},
		agentCard: card,
	})
	assert.NoError(t, err)
	assert.NotNil(t, processor)
	assert.Equal(t, "runner-only-agent", processor.agentName)
}

func TestBuildProcessor_DefaultEventConverterUsesEventPartMappers(t *testing.T) {
	mapper := func(ctx context.Context, event *event.Event) ([]protocol.Part, error) {
		return nil, nil
	}

	processor, err := buildProcessor(&mockAgent{name: "mapper-agent"}, inmemory.NewSessionService(), "mapper-agent", &options{
		eventPartMappers: []EventToA2APartMapper{mapper},
	})
	assert.NoError(t, err)
	assert.NotNil(t, processor)

	converter, ok := processor.eventToA2AConverter.(*defaultEventToA2AMessage)
	assert.True(t, ok)
	assert.Len(t, converter.eventPartMappers, 1)
	assert.NotNil(t, converter.eventPartMappers[0])
}

func TestBuildProcessor_CustomEventConverterIgnoresEventPartMappers(t *testing.T) {
	customConverter := &mockEventToA2AConverter{}
	mapper := func(ctx context.Context, event *event.Event) ([]protocol.Part, error) {
		return nil, nil
	}

	processor, err := buildProcessor(&mockAgent{name: "mapper-agent"}, inmemory.NewSessionService(), "mapper-agent", &options{
		eventToA2AConverter: customConverter,
		eventPartMappers:    []EventToA2APartMapper{mapper},
	})
	assert.NoError(t, err)
	assert.NotNil(t, processor)
	assert.Same(t, customConverter, processor.eventToA2AConverter)
}

func TestBuildProcessor_NoAgentNoRunner(t *testing.T) {
	_, err := buildProcessor(nil, inmemory.NewSessionService(), "card-only", &options{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent is required when runner is not provided")
}

func TestBuildAgentCard_ErrorWhenNoAgentAndNoCard(t *testing.T) {
	_, err := buildAgentCard(&options{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent is required when agent card is not provided")
}

func TestBuildProcessor_RequiresServerIdentity(t *testing.T) {
	_, err := buildProcessor(
		&mockAgent{name: "test-agent", description: "test description"},
		inmemory.NewSessionService(),
		"",
		&options{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent card name is required")
}

func TestMessageProcessor_ProcessMessage_RuntimeStateIncludesServerContext(t *testing.T) {
	ctxID := "runtime-session-1"
	var capturedState map[string]any

	proc := &messageProcessor{
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ro := agent.RunOptions{}
				for _, opt := range opts {
					opt(&ro)
				}
				capturedState = ro.RuntimeState

				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &defaultEventToA2AMessage{},
		errorHandler:        defaultErrorHandler,
		agentName:           "test-agent",
	}

	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: "actual-user"})
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "runtime-msg",
		Role:      protocol.MessageRoleUser,
		Metadata: map[string]any{
			"client_key": "client-value",
		},
		Parts: []protocol.Part{protocol.NewTextPart("hello")},
	}

	result, err := proc.ProcessMessage(ctx, msg, taskmanager.ProcessOptions{Streaming: false}, &mockTaskHandler{})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "client-value", capturedState["client_key"])
}

func TestMessageProcessor_ProcessMessage_AppendsRunOptions(t *testing.T) {
	ctxID := "runtime-session-2"
	var (
		capturedState     map[string]any
		capturedRequestID string
	)

	proc := &messageProcessor{
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ro := agent.RunOptions{}
				for _, opt := range opts {
					opt(&ro)
				}
				capturedState = ro.RuntimeState
				capturedRequestID = ro.RequestID

				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &defaultEventToA2AMessage{},
		errorHandler:        defaultErrorHandler,
		agentName:           "test-agent",
		runOptions:          []agent.RunOption{agent.WithRequestID("req-from-options")},
	}

	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: "actual-user"})
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "runtime-msg-builder",
		Role:      protocol.MessageRoleUser,
		Metadata:  map[string]any{"client_key": "client-value"},
		Parts:     []protocol.Part{protocol.NewTextPart("hello")},
	}

	result, err := proc.ProcessMessage(ctx, msg, taskmanager.ProcessOptions{Streaming: false}, &mockTaskHandler{})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "req-from-options", capturedRequestID)
	assert.Equal(t, "client-value", capturedState["client_key"])
}

func TestMessageProcessor_ProcessMessage_RuntimeStateMergesWithRunOptions(t *testing.T) {
	ctxID := "merge-session"
	var capturedState map[string]any

	proc := &messageProcessor{
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ro := agent.RunOptions{}
				for _, opt := range opts {
					opt(&ro)
				}
				capturedState = ro.RuntimeState

				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &defaultEventToA2AMessage{},
		errorHandler:        defaultErrorHandler,
		agentName:           "test-agent",
		// User also sets RuntimeState via WithRunOptions — should be merged, not overwritten
		runOptions: []agent.RunOption{
			agent.WithRuntimeState(map[string]any{
				"user_custom_key": "user-value",
				"client_key":      "will-be-overwritten-by-metadata",
			}),
		},
	}

	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: "actual-user"})
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "merge-msg",
		Role:      protocol.MessageRoleUser,
		Metadata: map[string]any{
			"client_key": "from-metadata",
		},
		Parts: []protocol.Part{protocol.NewTextPart("hello")},
	}

	result, err := proc.ProcessMessage(ctx, msg, taskmanager.ProcessOptions{Streaming: false}, &mockTaskHandler{})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// User's custom key should be preserved
	assert.Equal(t, "user-value", capturedState["user_custom_key"])
	// A2A metadata takes precedence on conflicting keys
	assert.Equal(t, "from-metadata", capturedState["client_key"])
}

func TestWithRunOptions(t *testing.T) {
	opts := &options{}

	WithRunOptions(agent.WithRequestID("req"))(opts)

	assert.Len(t, opts.runOptions, 1)
}

func TestMessageProcessor_ProcessMessage_SharedRuntimeStateNotMutated(t *testing.T) {
	ctxID := "shared-state-session"

	originalState := map[string]any{
		"shared_key": "original-value",
	}

	proc := &messageProcessor{
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ro := agent.RunOptions{}
				for _, opt := range opts {
					opt(&ro)
				}
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &defaultEventToA2AMessage{},
		errorHandler:        defaultErrorHandler,
		agentName:           "test-agent",
		runOptions: []agent.RunOption{
			agent.WithRuntimeState(originalState),
		},
	}

	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: "user"})
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "shared-msg",
		Role:      protocol.MessageRoleUser,
		Metadata:  map[string]any{"request_key": "request-value"},
		Parts:     []protocol.Part{protocol.NewTextPart("hello")},
	}

	_, err := proc.ProcessMessage(ctx, msg, taskmanager.ProcessOptions{Streaming: false}, &mockTaskHandler{})
	assert.NoError(t, err)

	// The original shared map must not be mutated by the merge logic.
	assert.Equal(t, map[string]any{"shared_key": "original-value"}, originalState)
}

func TestMessageProcessor_AddTaskMetadataUsesAppName(t *testing.T) {
	proc := &messageProcessor{
		adkCompatibility: true,
		agentName:        "agent-name",
	}
	evt := protocol.NewTaskStatusUpdateEvent(
		"task-id",
		"ctx-id",
		protocol.TaskStatus{State: protocol.TaskStateSubmitted},
		false,
	)

	proc.addTaskMetadata(&evt, "user-1", "session-1")

	assert.Equal(t, "agent-name", evt.Metadata[ia2a.GetADKMetadataKey("app_name")])
	assert.Equal(t, "user-1", evt.Metadata[ia2a.GetADKMetadataKey("user_id")])
	assert.Equal(t, "session-1", evt.Metadata[ia2a.GetADKMetadataKey("session_id")])
}

func TestBuildSkillsFromCardTools(t *testing.T) {
	tests := []struct {
		name      string
		tools     []tool.Tool
		agentName string
		agentDesc string
		expected  []a2a.AgentSkill
	}{
		{
			name:      "no tools",
			tools:     []tool.Tool{},
			agentName: "test-agent",
			agentDesc: "test description",
			expected: []a2a.AgentSkill{
				{
					Name:        "test-agent",
					Description: stringPtr("test description"),
					InputModes:  []string{"text"},
					OutputModes: []string{"text"},
					Tags:        []string{"default"},
				},
			},
		},
		{
			name: "with tools",
			tools: []tool.Tool{
				&mockTool{name: "calculator", description: "math tool"},
				&mockTool{name: "weather", description: "weather tool"},
			},
			agentName: "tool-agent",
			agentDesc: "agent with tools",
			expected: []a2a.AgentSkill{
				{
					Name:        "tool-agent",
					Description: stringPtr("agent with tools"),
					InputModes:  []string{"text"},
					OutputModes: []string{"text"},
					Tags:        []string{"default"},
				},
				{
					Name:        "calculator",
					Description: stringPtr("math tool"),
					InputModes:  []string{"text"},
					OutputModes: []string{"text"},
					Tags:        []string{"tool"},
				},
				{
					Name:        "weather",
					Description: stringPtr("weather tool"),
					InputModes:  []string{"text"},
					OutputModes: []string{"text"},
					Tags:        []string{"tool"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildSkillsFromCardTools(tt.tools, tt.agentName, tt.agentDesc)
			if !compareSkills(result, tt.expected) {
				t.Errorf("buildSkillsFromCardTools() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestProcessMessageHook(t *testing.T) {
	tests := []struct {
		name                string
		setupOptions        func() ([]Option, *bool, *bool)
		expectCustomBuilder bool
		expectHookCalled    bool
		wantErr             bool
	}{
		{
			name: "hook is applied during server creation",
			setupOptions: func() ([]Option, *bool, *bool) {
				mockHook := func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
					return &mockHookedProcessor{next: next}
				}
				return []Option{
					WithAgent(&mockAgent{name: "test-agent", description: "test"}, false),
					WithHost("localhost:8080"),
					WithProcessMessageHook(mockHook),
				}, nil, nil
			},
			expectCustomBuilder: false,
			expectHookCalled:    false,
			wantErr:             false,
		},
		{
			name: "hook with custom processor builder",
			setupOptions: func() ([]Option, *bool, *bool) {
				customBuilderCalled := false
				customBuilder := func(agent agent.Agent, sessionService session.Service) taskmanager.MessageProcessor {
					customBuilderCalled = true
					return &mockTaskManager{}
				}

				hookCalled := false
				customHook := func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
					hookCalled = true
					return &mockHookedProcessor{next: next}
				}

				return []Option{
					WithAgent(&mockAgent{name: "test-agent", description: "test"}, false),
					WithHost("localhost:8080"),
					WithProcessorBuilder(customBuilder),
					WithProcessMessageHook(customHook),
				}, &customBuilderCalled, &hookCalled
			},
			expectCustomBuilder: true,
			expectHookCalled:    true,
			wantErr:             false,
		},
		{
			name: "hook with default processor",
			setupOptions: func() ([]Option, *bool, *bool) {
				hookCalled := false
				customHook := func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
					hookCalled = true
					return &mockHookedProcessor{next: next}
				}

				return []Option{
					WithAgent(&mockAgent{name: "test-agent", description: "test"}, false),
					WithHost("localhost:8080"),
					WithProcessMessageHook(customHook),
				}, nil, &hookCalled
			},
			expectCustomBuilder: false,
			expectHookCalled:    true,
			wantErr:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, customBuilderPtr, hookCalledPtr := tt.setupOptions()

			server, err := New(opts...)
			if tt.wantErr {
				if err == nil {
					t.Errorf("New() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("New() failed: %v", err)
			}
			if server == nil {
				t.Fatal("New() returned nil server")
			}

			// Check custom builder was called if expected
			if tt.expectCustomBuilder && customBuilderPtr != nil {
				if !*customBuilderPtr {
					t.Error("Custom processor builder was not called")
				}
			}

			// Check hook was called if expected
			if tt.expectHookCalled && hookCalledPtr != nil {
				if !*hookCalledPtr {
					t.Error("Process message hook was not called")
				}
			}
		})
	}
}

// mockHookedProcessor is a mock processor for testing hooks
type mockHookedProcessor struct {
	next taskmanager.MessageProcessor
}

func (m *mockHookedProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	return m.next.ProcessMessage(ctx, message, options, handler)
}

// Helper functions
func boolPtr(b bool) *bool {
	return &b
}

func compareAgentCards(a, b a2a.AgentCard) bool {
	if a.Name != b.Name || a.Description != b.Description || a.URL != b.URL {
		return false
	}
	if a.Capabilities.Streaming != nil && b.Capabilities.Streaming != nil {
		if *a.Capabilities.Streaming != *b.Capabilities.Streaming {
			return false
		}
	} else if a.Capabilities.Streaming != b.Capabilities.Streaming {
		return false
	}
	if !compareExtensions(a.Capabilities.Extensions, b.Capabilities.Extensions) {
		return false
	}
	return compareSkills(a.Skills, b.Skills) &&
		compareStringSlices(a.DefaultInputModes, b.DefaultInputModes) &&
		compareStringSlices(a.DefaultOutputModes, b.DefaultOutputModes)
}

func defaultAgentCardExtensions() []a2a.AgentExtension {
	return []a2a.AgentExtension{
		{
			URI: ia2a.ExtensionTRPCA2AVersion,
			Params: map[string]any{
				"version": ia2a.InteractionVersion,
			},
		},
	}
}

func compareExtensions(a, b []a2a.AgentExtension) bool {
	if len(a) != len(b) {
		return false
	}
	for i, extA := range a {
		extB := b[i]
		if extA.URI != extB.URI {
			return false
		}
		if len(extA.Params) != len(extB.Params) {
			return false
		}
		for k, v := range extA.Params {
			if extB.Params[k] != v {
				return false
			}
		}
	}
	return true
}

func compareSkills(a, b []a2a.AgentSkill) bool {
	if len(a) != len(b) {
		return false
	}
	for i, skillA := range a {
		skillB := b[i]
		if skillA.Name != skillB.Name {
			return false
		}
		if skillA.Description != nil && skillB.Description != nil {
			if *skillA.Description != *skillB.Description {
				return false
			}
		} else if skillA.Description != skillB.Description {
			return false
		}
		if !compareStringSlices(skillA.InputModes, skillB.InputModes) ||
			!compareStringSlices(skillA.OutputModes, skillB.OutputModes) ||
			!compareStringSlices(skillA.Tags, skillB.Tags) {
			return false
		}
	}
	return true
}

func compareStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, strA := range a {
		if strA != b[i] {
			return false
		}
	}
	return true
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "already has http scheme",
			input:    "http://example.com",
			expected: "http://example.com",
		},
		{
			name:     "already has https scheme",
			input:    "https://example.com",
			expected: "https://example.com",
		},
		{
			name:     "custom scheme",
			input:    "custom://service.namespace",
			expected: "custom://service.namespace",
		},
		{
			name:     "custom scheme",
			input:    "grpc://example.com:9090",
			expected: "grpc://example.com:9090",
		},
		{
			name:     "host only - simple domain",
			input:    "example.com",
			expected: "http://example.com",
		},
		{
			name:     "host only - with port",
			input:    "localhost:8080",
			expected: "http://localhost:8080",
		},
		{
			name:     "host only - IP address",
			input:    "192.168.1.1",
			expected: "http://192.168.1.1",
		},
		{
			name:     "host only - IP with port",
			input:    "127.0.0.1:9999",
			expected: "http://127.0.0.1:9999",
		},
		{
			name:     "complete URL with path",
			input:    "http://example.com/api/v1",
			expected: "http://example.com/api/v1",
		},
		{
			name:     "https URL with path and query",
			input:    "https://example.com/api?key=value",
			expected: "https://example.com/api?key=value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ia2a.NormalizeURL(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractBasePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "http with path",
			input:    "http://example.com/api/v1",
			expected: "/api/v1",
		},
		{
			name:     "https with path",
			input:    "https://example.com/api/v1/agents",
			expected: "/api/v1/agents",
		},
		{
			name:     "http without path",
			input:    "http://example.com",
			expected: "",
		},
		{
			name:     "https without path",
			input:    "https://example.com:8080",
			expected: "",
		},
		{
			name:     "http with root path",
			input:    "http://example.com/",
			expected: "/",
		},
		{
			name:     "custom scheme with path - should return path",
			input:    "grpc://example.com:9090/service",
			expected: "/service",
		},
		{
			name:     "custom scheme without path - should return empty",
			input:    "custom://service.namespace",
			expected: "",
		},
		{
			name:     "http with path and query",
			input:    "http://example.com/api?key=value",
			expected: "/api",
		},
		{
			name:     "https with path and fragment",
			input:    "https://example.com/docs#section",
			expected: "/docs",
		},
		{
			name:     "invalid URL - no scheme",
			input:    "://invalid",
			expected: "",
		},
		{
			name:     "grpc with complex path",
			input:    "grpc://service:9090/api/v1/rpc",
			expected: "/api/v1/rpc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractBasePath(tt.input)
			if result != tt.expected {
				t.Errorf("extractBasePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestMessageProcessor_ProcessMessage_ContextCancellation tests context cancellation during event processing
func TestMessageProcessor_ProcessMessage_ContextCancellation(t *testing.T) {
	ctxID := "ctx"
	ctx, cancel := context.WithCancel(context.Background())
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "cancel-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		debugLogging: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 2)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "response1"}}},
					},
				}
				// Cancel context before sending second event
				cancel()
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "response2"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{},
		errorHandler:        defaultErrorHandler,
	}

	result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
	// handleError returns a result with error message, not an error
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Result)
}

// TestMessageProcessor_ProcessMessage_NilEvent tests handling of nil events
func TestMessageProcessor_ProcessMessage_NilEvent(t *testing.T) {
	ctxID := "ctx"
	ctx := context.Background()
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "nil-event-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		debugLogging: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 2)
				// Send nil event
				ch <- nil
				// Send valid event
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "response"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{},
		errorHandler:        defaultErrorHandler,
	}

	result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Result)
}

// TestMessageProcessor_ProcessMessage_NilResponse tests handling of events with nil response
func TestMessageProcessor_ProcessMessage_NilResponse(t *testing.T) {
	ctxID := "ctx"
	ctx := context.Background()
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "nil-response-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		debugLogging: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 2)
				// Send event with nil response
				ch <- &event.Event{Response: nil}
				// Send valid event
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "response"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{},
		errorHandler:        defaultErrorHandler,
	}

	result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Result)
}

func TestGraphResumeStateFromMetadata(t *testing.T) {
	t.Run("checkpoint only keeps checkpoint state without command", func(t *testing.T) {
		state := ia2a.GraphResumeStateFromMetadata(map[string]any{
			graph.CfgKeyCheckpointID: "ck-1",
		})
		if assert.NotNil(t, state) {
			assert.Equal(t, "ck-1", state[graph.CfgKeyCheckpointID])
			_, exists := state[graph.StateKeyCommand]
			assert.False(t, exists)
		}
	})

	t.Run("state_delta resume builds state and command", func(t *testing.T) {
		stateDelta := EncodeStateDeltaMetadata(map[string][]byte{
			graph.CfgKeyLineageID:    []byte(`"ln-sd"`),
			graph.CfgKeyCheckpointID: []byte(`"ck-sd"`),
			graph.CfgKeyCheckpointNS: []byte(`"ns-sd"`),
			"resume":                 []byte(`"approve"`),
			graph.CfgKeyResumeMap:    []byte(`{"approval":true}`),
		})
		state := ia2a.GraphResumeStateFromMetadata(map[string]any{
			ia2a.MessageMetadataStateDeltaKey: stateDelta,
		})
		if assert.NotNil(t, state) {
			assert.Equal(t, "ln-sd", state[graph.CfgKeyLineageID])
			assert.Equal(t, "ck-sd", state[graph.CfgKeyCheckpointID])
			assert.Equal(t, "ns-sd", state[graph.CfgKeyCheckpointNS])
			cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
			if assert.True(t, ok, "expected ResumeCommand in state") {
				assert.Equal(t, "approve", cmd.Resume)
				assert.Equal(t, true, cmd.ResumeMap["approval"])
			}
		}
	})

	t.Run("state_delta pregel_metadata fallback extracts checkpoint info", func(t *testing.T) {
		pregelJSON := []byte(`{"lineageId":"ln-p","checkpointId":"ck-p","checkpointNs":"ns-p","interruptKey":"approval"}`)
		stateDelta := EncodeStateDeltaMetadata(map[string][]byte{
			graph.MetadataKeyPregel: pregelJSON,
			"resume":                []byte(`"yes"`),
		})
		state := ia2a.GraphResumeStateFromMetadata(map[string]any{
			ia2a.MessageMetadataStateDeltaKey: stateDelta,
		})
		if assert.NotNil(t, state) {
			assert.Equal(t, "ln-p", state[graph.CfgKeyLineageID])
			assert.Equal(t, "ck-p", state[graph.CfgKeyCheckpointID])
			assert.Equal(t, "ns-p", state[graph.CfgKeyCheckpointNS])
			cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
			if assert.True(t, ok, "expected ResumeCommand in state") {
				assert.Equal(t, "yes", cmd.Resume)
			}
		}
	})

	t.Run("state_delta checkpoint merges serialized Command fallback", func(t *testing.T) {
		state := ia2a.GraphResumeStateFromMetadata(map[string]any{
			ia2a.MessageMetadataStateDeltaKey: EncodeStateDeltaMetadata(map[string][]byte{
				graph.CfgKeyLineageID:    []byte(`"ln-sd"`),
				graph.CfgKeyCheckpointID: []byte(`"ck-sd"`),
			}),
			graph.StateKeyCommand: map[string]any{
				"Resume":    "approve",
				"ResumeMap": map[string]any{"approval": true},
			},
		})
		if assert.NotNil(t, state) {
			assert.Equal(t, "ln-sd", state[graph.CfgKeyLineageID])
			assert.Equal(t, "ck-sd", state[graph.CfgKeyCheckpointID])
			cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
			if assert.True(t, ok, "expected ResumeCommand in state") {
				assert.Equal(t, "approve", cmd.Resume)
				assert.Equal(t, true, cmd.ResumeMap["approval"])
			}
		}
	})

	t.Run("flattened resume and resume_map remain backward compatible", func(t *testing.T) {
		state := ia2a.GraphResumeStateFromMetadata(map[string]any{
			graph.CfgKeyCheckpointID: "ck-1",
			"resume":                 "approve",
			graph.CfgKeyResumeMap:    map[string]any{"approval": true},
		})
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		if assert.True(t, ok, "expected ResumeCommand in state") {
			assert.Equal(t, "approve", cmd.Resume)
			assert.Equal(t, true, cmd.ResumeMap["approval"])
		}
	})

	t.Run("serialized Command struct via transferStateKey fallback", func(t *testing.T) {
		state := ia2a.GraphResumeStateFromMetadata(map[string]any{
			graph.CfgKeyCheckpointID: "ck-cmd",
			graph.CfgKeyLineageID:    "ln-cmd",
			graph.CfgKeyCheckpointNS: "ns-cmd",
			graph.StateKeyCommand: map[string]any{
				"Resume":    nil,
				"ResumeMap": map[string]any{"remote_ask_approval": true},
			},
		})
		if assert.NotNil(t, state) {
			assert.Equal(t, "ln-cmd", state[graph.CfgKeyLineageID])
			assert.Equal(t, "ck-cmd", state[graph.CfgKeyCheckpointID])
			assert.Equal(t, "ns-cmd", state[graph.CfgKeyCheckpointNS])
			cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
			if assert.True(t, ok, "expected ResumeCommand in state") {
				assert.Equal(t, true, cmd.ResumeMap["remote_ask_approval"])
			}
		}
	})

	t.Run("serialized Command struct with Resume value", func(t *testing.T) {
		state := ia2a.GraphResumeStateFromMetadata(map[string]any{
			graph.CfgKeyCheckpointID: "ck-2",
			graph.StateKeyCommand: map[string]any{
				"Resume":    "approved",
				"ResumeMap": nil,
			},
		})
		if assert.NotNil(t, state) {
			cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
			if assert.True(t, ok, "expected ResumeCommand in state") {
				assert.Equal(t, "approved", cmd.Resume)
			}
		}
	})
}

func TestMessageProcessor_ProcessMessage_GraphResumeMetadataBecomesCommand(t *testing.T) {
	ctxID := "ctx"
	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: "user-1"})
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "graph-resume-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("resume")},
		Metadata: map[string]any{
			ia2a.MessageMetadataStateDeltaKey: EncodeStateDeltaMetadata(map[string][]byte{
				graph.CfgKeyLineageID:    []byte(`"ln-1"`),
				graph.CfgKeyCheckpointID: []byte(`"ck-1"`),
				"resume":                 []byte(`"approve"`),
				graph.CfgKeyResumeMap:    []byte(`{"approval":true}`),
			}),
		},
	}

	processor := &messageProcessor{
		debugLogging:         false,
		a2aToAgentConverter:  &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter:  &mockEventToA2AConverter{},
		errorHandler:         defaultErrorHandler,
		structuredTaskErrors: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				var ro agent.RunOptions
				for _, opt := range opts {
					opt(&ro)
				}
				if assert.NotNil(t, ro.RuntimeState) {
					assert.Equal(t, "ln-1", ro.RuntimeState[graph.CfgKeyLineageID])
					assert.Equal(t, "ck-1", ro.RuntimeState[graph.CfgKeyCheckpointID])
					cmd, ok := ro.RuntimeState[graph.StateKeyCommand].(*graph.ResumeCommand)
					if assert.True(t, ok, "expected ResumeCommand in runtime state") {
						assert.Equal(t, "approve", cmd.Resume)
						assert.Equal(t, true, cmd.ResumeMap["approval"])
					}
				}
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
	}

	result, err := processor.ProcessMessage(
		ctx,
		msg,
		taskmanager.ProcessOptions{},
		&mockTaskHandler{},
	)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestMessageProcessor_ProcessMessage_StateDeltaResumeBecomesCommand(t *testing.T) {
	ctxID := "ctx"
	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: "user-1"})
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "state-delta-resume-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("resume")},
		Metadata: map[string]any{
			ia2a.MessageMetadataStateDeltaKey: EncodeStateDeltaMetadata(map[string][]byte{
				graph.CfgKeyLineageID:    []byte(`"ln-sd"`),
				graph.CfgKeyCheckpointID: []byte(`"ck-sd"`),
				"resume":                 []byte(`"approve"`),
				graph.CfgKeyResumeMap:    []byte(`{"approval":true}`),
			}),
		},
	}

	processor := &messageProcessor{
		debugLogging:         false,
		a2aToAgentConverter:  &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter:  &mockEventToA2AConverter{},
		errorHandler:         defaultErrorHandler,
		structuredTaskErrors: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				var ro agent.RunOptions
				for _, opt := range opts {
					opt(&ro)
				}
				if assert.NotNil(t, ro.RuntimeState) {
					assert.Equal(t, "ln-sd", ro.RuntimeState[graph.CfgKeyLineageID])
					assert.Equal(t, "ck-sd", ro.RuntimeState[graph.CfgKeyCheckpointID])
					cmd, ok := ro.RuntimeState[graph.StateKeyCommand].(*graph.ResumeCommand)
					if assert.True(t, ok, "expected ResumeCommand in runtime state") {
						assert.Equal(t, "approve", cmd.Resume)
						assert.Equal(t, true, cmd.ResumeMap["approval"])
					}
				}
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
	}

	result, err := processor.ProcessMessage(
		ctx,
		msg,
		taskmanager.ProcessOptions{},
		&mockTaskHandler{},
	)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestMessageProcessor_ProcessMessage_StateDeltaCheckpointAndSerializedCommandBecomeResumeCommand(t *testing.T) {
	ctxID := "ctx"
	ctx := context.WithValue(context.Background(), auth.AuthUserKey, &auth.User{ID: "user-1"})
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "state-delta-command-fallback-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("resume")},
		Metadata: map[string]any{
			ia2a.MessageMetadataStateDeltaKey: EncodeStateDeltaMetadata(map[string][]byte{
				graph.CfgKeyLineageID:    []byte(`"ln-sd"`),
				graph.CfgKeyCheckpointID: []byte(`"ck-sd"`),
			}),
			graph.StateKeyCommand: map[string]any{
				"Resume":    "approve",
				"ResumeMap": map[string]any{"approval": true},
			},
		},
	}

	processor := &messageProcessor{
		debugLogging:         false,
		a2aToAgentConverter:  &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter:  &mockEventToA2AConverter{},
		errorHandler:         defaultErrorHandler,
		structuredTaskErrors: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				var ro agent.RunOptions
				for _, opt := range opts {
					opt(&ro)
				}
				if assert.NotNil(t, ro.RuntimeState) {
					assert.Equal(t, "ln-sd", ro.RuntimeState[graph.CfgKeyLineageID])
					assert.Equal(t, "ck-sd", ro.RuntimeState[graph.CfgKeyCheckpointID])
					cmd, ok := ro.RuntimeState[graph.StateKeyCommand].(*graph.ResumeCommand)
					if assert.True(t, ok, "expected ResumeCommand in runtime state") {
						assert.Equal(t, "approve", cmd.Resume)
						assert.Equal(t, true, cmd.ResumeMap["approval"])
					}
				}
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "ok"}}}}}
				close(ch)
				return ch, nil
			},
		},
	}

	result, err := processor.ProcessMessage(
		ctx,
		msg,
		taskmanager.ProcessOptions{},
		&mockTaskHandler{},
	)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

// TestMessageProcessor_ProcessMessage_ConversionFailure tests handling of conversion errors
func TestMessageProcessor_ProcessMessage_ConversionFailure(t *testing.T) {
	ctxID := "ctx"
	ctx := context.Background()
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "conversion-fail-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		debugLogging: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "response"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{
			convertToA2AMessageFunc: func(ctx context.Context, event *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
				return nil, errors.New("conversion failed")
			},
		},
		errorHandler: defaultErrorHandler,
	}

	result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
	// handleError returns a result with error message, not an error
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Result)
}

// TestMessageProcessor_ProcessMessage_TaskTypeResult tests handling of Task type conversion results
func TestMessageProcessor_ProcessMessage_TaskTypeResult(t *testing.T) {
	ctxID := "ctx"
	ctx := context.Background()
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "task-type-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		debugLogging: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "response"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{
			convertToA2AMessageFunc: func(ctx context.Context, event *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
				// Return a Task type result
				return &protocol.Task{
					ID:        "task-123",
					ContextID: ctxID,
					Artifacts: []protocol.Artifact{
						{
							ArtifactID: "artifact-1",
							Parts: []protocol.Part{
								protocol.NewTextPart("artifact content"),
							},
						},
					},
				}, nil
			},
		},
		errorHandler: defaultErrorHandler,
	}

	result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Result)
	// Verify that parts from Task artifacts were collected
	resultMsg, ok := result.Result.(*protocol.Message)
	assert.True(t, ok)
	assert.Equal(t, 1, len(resultMsg.Parts))
}

// TestMessageProcessor_ProcessMessage_MultipleEvents tests handling of multiple events
func TestMessageProcessor_ProcessMessage_MultipleEvents(t *testing.T) {
	ctxID := "ctx"
	ctx := context.Background()
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "multi-event-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		debugLogging: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 3)
				// Send multiple events
				for i := 1; i <= 3; i++ {
					ch <- &event.Event{
						Response: &model.Response{
							Choices: []model.Choice{{Message: model.Message{Content: fmt.Sprintf("response%d", i)}}},
						},
					}
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{},
		errorHandler:        defaultErrorHandler,
	}

	result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Result)
	// When multiple events are returned, the result is a Task with history and artifacts
	resultTask, ok := result.Result.(*protocol.Task)
	assert.True(t, ok, "Expected *protocol.Task for multiple events, got %T", result.Result)
	// History should contain first 2 messages, artifacts should contain the last message
	assert.Equal(t, 2, len(resultTask.History))
	assert.Equal(t, 1, len(resultTask.Artifacts))
}

func TestBuildTaskErrorMetadata(t *testing.T) {
	t.Run("nil event returns nil", func(t *testing.T) {
		assert.Nil(t, buildTaskErrorMetadata(nil))
	})

	t.Run("missing response returns nil", func(t *testing.T) {
		assert.Nil(
			t,
			buildTaskErrorMetadata(&event.Event{}),
		)
	})

	t.Run("missing error returns nil", func(t *testing.T) {
		assert.Nil(t, buildTaskErrorMetadata(&event.Event{
			Response: &model.Response{},
		}))
	})

	t.Run("flow error keeps failed metadata", func(t *testing.T) {
		metadata := buildTaskErrorMetadata(&event.Event{
			Response: &model.Response{
				Error: &model.ResponseError{
					Type:    model.ErrorTypeFlowError,
					Message: "task failed",
				},
			},
		})

		if assert.NotNil(t, metadata) {
			assert.Equal(
				t,
				model.ObjectTypeError,
				metadata[ia2a.MessageMetadataObjectTypeKey],
			)
			assert.Equal(
				t,
				model.ErrorTypeFlowError,
				metadata[ia2a.MessageMetadataErrorTypeKey],
			)
			assert.Equal(
				t,
				"task failed",
				metadata[ia2a.MessageMetadataErrorMessageKey],
			)
			assert.Equal(
				t,
				string(protocol.TaskStateFailed),
				metadata[ia2a.MessageMetadataTaskStateKey],
			)
			_, ok := metadata[ia2a.MessageMetadataResponseIDKey]
			assert.False(t, ok)
		}
	})

	t.Run("stop agent error keeps canceled state and response id", func(t *testing.T) {
		const responseID = "resp-1"
		metadata := buildTaskErrorMetadata(&event.Event{
			Response: &model.Response{
				ID: responseID,
				Error: &model.ResponseError{
					Type:    agent.ErrorTypeStopAgentError,
					Message: "task canceled",
				},
			},
		})

		if assert.NotNil(t, metadata) {
			assert.Equal(
				t,
				string(protocol.TaskStateCanceled),
				metadata[ia2a.MessageMetadataTaskStateKey],
			)
			assert.Equal(
				t,
				responseID,
				metadata[ia2a.MessageMetadataResponseIDKey],
			)
		}
	})

}

func TestBuildTaskErrorMessage(t *testing.T) {
	const (
		taskID = "task-1"
		ctxID  = "ctx-1"
	)

	t.Run("nil event returns nil", func(t *testing.T) {
		assert.Nil(t, buildTaskErrorMessage(taskID, ctxID, nil, nil))
	})

	t.Run("missing response returns nil", func(t *testing.T) {
		assert.Nil(
			t,
			buildTaskErrorMessage(
				taskID,
				ctxID,
				&event.Event{},
				nil,
			),
		)
	})

	t.Run("missing error returns nil", func(t *testing.T) {
		assert.Nil(t, buildTaskErrorMessage(
			taskID,
			ctxID,
			&event.Event{
				Response: &model.Response{},
			},
			nil,
		))
	})

	t.Run("response id and text keep mirrored metadata", func(t *testing.T) {
		const responseID = "resp-2"
		agentEvent := &event.Event{
			Response: &model.Response{
				ID: responseID,
				Error: &model.ResponseError{
					Type:    model.ErrorTypeFlowError,
					Message: "task failed",
				},
			},
		}
		metadata := buildTaskErrorMetadata(agentEvent)
		msg := buildTaskErrorMessage(
			taskID,
			ctxID,
			agentEvent,
			metadata,
		)

		if assert.NotNil(t, msg) {
			assert.Equal(t, responseID, msg.MessageID)
			if assert.NotNil(t, msg.Metadata) {
				assert.Equal(
					t,
					metadata[ia2a.MessageMetadataErrorTypeKey],
					msg.Metadata[ia2a.MessageMetadataErrorTypeKey],
				)
				assert.Equal(
					t,
					metadata[ia2a.MessageMetadataTaskStateKey],
					msg.Metadata[ia2a.MessageMetadataTaskStateKey],
				)
			}
			if assert.Len(t, msg.Parts, 1) {
				var text string
				switch part := msg.Parts[0].(type) {
				case *protocol.TextPart:
					text = part.Text
				case protocol.TextPart:
					text = part.Text
				}
				if assert.NotEmpty(t, text) {
					assert.Equal(t, "task failed", text)
				}
			}

			metadata[ia2a.MessageMetadataErrorCodeKey] = "mutated"
			_, ok := msg.Metadata[ia2a.MessageMetadataErrorCodeKey]
			assert.False(t, ok)
		}
	})

	t.Run("empty error message keeps empty parts", func(t *testing.T) {
		agentEvent := &event.Event{
			Response: &model.Response{
				Error: &model.ResponseError{
					Type: model.ErrorTypeFlowError,
				},
			},
		}
		msg := buildTaskErrorMessage(
			taskID,
			ctxID,
			agentEvent,
			buildTaskErrorMetadata(agentEvent),
		)

		if assert.NotNil(t, msg) {
			assert.NotEmpty(t, msg.MessageID)
			assert.NotNil(t, msg.Metadata)
			assert.Len(t, msg.Parts, 0)
		}
	})
}

func TestMessageProcessor_ProcessMessage_StructuredTaskError(
	t *testing.T,
) {
	ctxID := "ctx"
	code := "A2A_500"
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "structured-error-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		structuredTaskErrors: true,
		runner: &mockRunner{
			runFunc: func(
				ctx context.Context,
				userID string,
				sessionID string,
				message model.Message,
				opts ...agent.RunOption,
			) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						ID:   "resp-1",
						Done: true,
						Error: &model.ResponseError{
							Type:    model.ErrorTypeFlowError,
							Message: "task failed",
							Code:    &code,
						},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{},
		errorHandler:        defaultErrorHandler,
	}

	result, err := processor.processMessage(
		context.Background(),
		"user",
		ctxID,
		&msg,
		&model.Message{Content: "input"},
		nil,
	)
	assert.NoError(t, err)
	if !assert.NotNil(t, result) {
		return
	}

	task, ok := result.Result.(*protocol.Task)
	if !assert.True(t, ok) {
		return
	}
	assert.Equal(t, protocol.TaskStateFailed, task.Status.State)
	assert.NotNil(t, task.Metadata)
	assert.Equal(t, code, task.Metadata[ia2a.MessageMetadataErrorCodeKey])
	assert.NotNil(t, task.Status.Message)
	assert.Equal(
		t,
		task.Metadata[ia2a.MessageMetadataErrorCodeKey],
		task.Status.Message.Metadata[ia2a.MessageMetadataErrorCodeKey],
	)
	task.Metadata["new_key"] = "task-only"
	_, ok = task.Status.Message.Metadata["new_key"]
	assert.False(t, ok)
	assert.Len(t, task.Status.Message.Parts, 1)
}

func TestMessageProcessor_ProcessMessage_StructuredTaskError_ResponseRewriterDrop(
	t *testing.T,
) {
	ctxID := "ctx"
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "structured-error-drop-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		structuredTaskErrors: true,
		responseRewriter: ResponseRewriterFuncs{
			Unary: func(
				result protocol.UnaryMessageResult,
			) protocol.UnaryMessageResult {
				return nil
			},
		},
		runner: &mockRunner{
			runFunc: func(
				ctx context.Context,
				userID string,
				sessionID string,
				message model.Message,
				opts ...agent.RunOption,
			) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Done: true,
						Error: &model.ResponseError{
							Type:    model.ErrorTypeFlowError,
							Message: "task failed",
						},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{},
		errorHandler:        defaultErrorHandler,
	}

	result, err := processor.processMessage(
		context.Background(),
		"user",
		ctxID,
		&msg,
		&model.Message{Content: "input"},
		nil,
	)
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		assert.Nil(t, result.Result)
		assert.Nil(t, result.StreamingEvents)
	}
}

func TestMessageProcessor_ProcessMessage_MultipleEvents_PreservesArtifactMetadata(
	t *testing.T,
) {
	ctxID := "ctx"
	ctx := context.Background()
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "multi-event-metadata-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		debugLogging: false,
		runner: &mockRunner{
			runFunc: func(
				ctx context.Context,
				userID string,
				sessionID string,
				message model.Message,
				opts ...agent.RunOption,
			) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 2)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{
							Message: model.Message{
								Content: "response1",
							},
						}},
					},
				}
				ch <- &event.Event{
					Response: &model.Response{
						ID:     "resp-final",
						Object: "graph.execution",
						Choices: []model.Choice{{
							Message: model.Message{},
						}},
					},
					StateDelta: map[string][]byte{
						"_node_metadata": []byte(
							`{"nodeId":"planner","phase":"start"}`,
						),
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &defaultEventToA2AMessage{},
		errorHandler:        defaultErrorHandler,
	}

	result, err := processor.processMessage(
		ctx,
		"user",
		"session",
		&msg,
		&model.Message{Content: "input"},
		nil,
	)
	assert.NoError(t, err)
	resultTask, ok := result.Result.(*protocol.Task)
	assert.True(
		t,
		ok,
		"Expected *protocol.Task for multiple events, got %T",
		result.Result,
	)
	if !assert.Len(t, resultTask.Artifacts, 1) {
		return
	}
	assert.Equal(
		t,
		"graph.execution",
		resultTask.Artifacts[0].Metadata[ia2a.MessageMetadataObjectTypeKey],
	)
	rawStateDelta, ok := resultTask.Artifacts[0].Metadata[ia2a.MessageMetadataStateDeltaKey]
	if assert.True(t, ok, "expected state_delta in artifact metadata") {
		decoded := ia2a.DecodeStateDeltaMetadata(rawStateDelta)
		assert.Equal(
			t,
			[]byte(`{"nodeId":"planner","phase":"start"}`),
			decoded["_node_metadata"],
		)
	}
}

func TestMessageProcessor_ProcessBatchStreamingEvents_StructuredTaskError(
	t *testing.T,
) {
	code := "A2A_500"
	processor := &messageProcessor{
		structuredTaskErrors: true,
	}
	ctxID := "ctx"
	msg := &protocol.Message{
		ContextID: &ctxID,
		MessageID: "streaming-error-test",
	}
	subscriber := &mockTaskSubscriber{
		channel: make(chan protocol.StreamingMessageEvent, 1),
	}
	terminalTaskError := false

	cont, err := processor.processBatchStreamingEvents(
		context.Background(),
		"task-1",
		msg,
		[]*event.Event{{
			Response: &model.Response{
				ID:   "resp-1",
				Done: true,
				Error: &model.ResponseError{
					Type:    model.ErrorTypeFlowError,
					Message: "task failed",
					Code:    &code,
				},
			},
		}},
		subscriber,
		&terminalTaskError,
		nil,
	)
	assert.NoError(t, err)
	assert.False(t, cont)
	assert.True(t, terminalTaskError)

	select {
	case streamEvent := <-subscriber.channel:
		status, ok := streamEvent.Result.(*protocol.TaskStatusUpdateEvent)
		if !assert.True(t, ok) {
			return
		}
		assert.Equal(t, protocol.TaskStateFailed, status.Status.State)
		assert.Equal(
			t,
			code,
			status.Metadata[ia2a.MessageMetadataErrorCodeKey],
		)
		if assert.NotNil(t, status.Status.Message) {
			messageMetadata := status.Status.Message.Metadata
			messageCode := messageMetadata[ia2a.MessageMetadataErrorCodeKey]
			assert.Equal(
				t,
				status.Metadata[ia2a.MessageMetadataErrorCodeKey],
				messageCode,
			)
			assert.Len(t, status.Status.Message.Parts, 1)
		}
	default:
		t.Fatal("expected task failure status event")
	}
}

func TestMessageProcessor_ProcessBatchStreamingEvents_DropStructuredTaskError(
	t *testing.T,
) {
	processor := &messageProcessor{
		structuredTaskErrors: true,
		responseRewriter: ResponseRewriterFuncs{
			Streaming: func(
				result protocol.StreamingMessageResult,
			) protocol.StreamingMessageResult {
				return nil
			},
		},
	}
	ctxID := "ctx"
	msg := &protocol.Message{
		ContextID: &ctxID,
		MessageID: "streaming-error-drop-test",
	}
	subscriber := &mockTaskSubscriber{
		channel: make(chan protocol.StreamingMessageEvent, 1),
	}
	terminalTaskError := false

	cont, err := processor.processBatchStreamingEvents(
		context.Background(),
		"task-1",
		msg,
		[]*event.Event{{
			Response: &model.Response{
				Done: true,
				Error: &model.ResponseError{
					Type:    model.ErrorTypeFlowError,
					Message: "task failed",
				},
			},
		}},
		subscriber,
		&terminalTaskError,
		nil,
	)
	assert.NoError(t, err)
	assert.False(t, cont)
	assert.True(t, terminalTaskError)

	select {
	case streamEvent := <-subscriber.channel:
		t.Fatalf("unexpected streaming result: %#v", streamEvent)
	default:
	}
}

func TestProcessAgentStreamingEvents_ResponseRewriterDropsCompletionEvents(
	t *testing.T,
) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	events := make(chan *event.Event)
	close(events)

	proc := createTestMessageProcessor()
	proc.responseRewriter = ResponseRewriterFuncs{
		Streaming: func(
			result protocol.StreamingMessageResult,
		) protocol.StreamingMessageResult {
			switch v := result.(type) {
			case *protocol.TaskArtifactUpdateEvent:
				return nil
			case *protocol.TaskStatusUpdateEvent:
				if v.Status.State == protocol.TaskStateCompleted {
					return nil
				}
			}
			return result
		},
	}

	var results []protocol.StreamingMessageResult
	sub := &mockTaskSubscriber{
		sendFunc: func(evt protocol.StreamingMessageEvent) error {
			if evt.Result != nil {
				results = append(results, evt.Result)
			}
			return nil
		},
	}

	proc.processAgentStreamingEvents(
		ctx,
		"task",
		"user1",
		"session1",
		msg,
		events,
		sub,
		&mockTaskHandler{},
	)

	if assert.Len(t, results, 1) {
		status, ok := results[0].(*protocol.TaskStatusUpdateEvent)
		if assert.True(t, ok) {
			assert.Equal(t, protocol.TaskStateSubmitted, status.Status.State)
		}
	}
}

func TestProcessAgentStreamingEvents_StopAgentError(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	code := "A2A_499"
	events := make(chan *event.Event, 1)
	events <- &event.Event{
		Response: &model.Response{
			ID:   "resp-1",
			Done: true,
			Error: &model.ResponseError{
				Type:    agent.ErrorTypeStopAgentError,
				Message: "task canceled",
				Code:    &code,
			},
		},
	}
	close(events)

	subscriber := &mockTaskSubscriber{
		channel: make(chan protocol.StreamingMessageEvent, 4),
	}
	processor := createTestMessageProcessor()
	processor.structuredTaskErrors = true

	processor.processAgentStreamingEvents(
		ctx,
		"task-1",
		"user",
		"session",
		msg,
		events,
		subscriber,
		&mockTaskHandler{},
	)

	var results []protocol.StreamingMessageResult
	for {
		select {
		case streamEvent, ok := <-subscriber.channel:
			if !ok {
				goto done
			}
			if streamEvent.Result != nil {
				results = append(results, streamEvent.Result)
			}
		default:
			goto done
		}
	}

done:
	if !assert.Len(t, results, 2) {
		return
	}

	submitted, ok := results[0].(*protocol.TaskStatusUpdateEvent)
	if !assert.True(t, ok) {
		return
	}
	assert.Equal(t, protocol.TaskStateSubmitted, submitted.Status.State)

	status, ok := results[1].(*protocol.TaskStatusUpdateEvent)
	if !assert.True(t, ok) {
		return
	}
	assert.Equal(t, protocol.TaskStateCanceled, status.Status.State)
	assert.Equal(
		t,
		code,
		status.Metadata[ia2a.MessageMetadataErrorCodeKey],
	)
	if assert.NotNil(t, status.Status.Message) {
		messageMetadata := status.Status.Message.Metadata
		messageCode := messageMetadata[ia2a.MessageMetadataErrorCodeKey]
		assert.Equal(
			t,
			status.Metadata[ia2a.MessageMetadataErrorCodeKey],
			messageCode,
		)
		assert.Len(t, status.Status.Message.Parts, 1)
	}
}

func TestMessageProcessor_ProcessBatchStreamingEvents_GraphNodeErrorNotTerminal(
	t *testing.T,
) {
	processor := &messageProcessor{
		structuredTaskErrors: true,
		eventToA2AConverter: &mockEventToA2AConverter{
			convertStreamingToA2AMessageFunc: func(
				ctx context.Context,
				event *event.Event,
				options EventToA2AStreamingOptions,
			) (protocol.StreamingMessageResult, error) {
				return nil, nil
			},
		},
	}
	ctxID := "ctx"
	msg := &protocol.Message{
		ContextID: &ctxID,
		MessageID: "graph-node-error-test",
	}
	subscriber := &mockTaskSubscriber{
		channel: make(chan protocol.StreamingMessageEvent, 1),
	}
	terminalTaskError := false

	cont, err := processor.processBatchStreamingEvents(
		context.Background(),
		"task-1",
		msg,
		[]*event.Event{{
			Response: &model.Response{
				Object: graph.ObjectTypeGraphNodeError,
				Error: &model.ResponseError{
					Type:    model.ErrorTypeFlowError,
					Message: "node failed",
				},
			},
		}},
		subscriber,
		&terminalTaskError,
		nil,
	)
	assert.NoError(t, err)
	assert.True(t, cont)
	assert.False(t, terminalTaskError)

	select {
	case streamEvent := <-subscriber.channel:
		t.Fatalf("unexpected streaming result: %#v", streamEvent)
	default:
	}
}

func TestProcessAgentStreamingEvents_HidesRunnerCompletion_TaskArtifact(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	events := make(chan *event.Event, 1)
	events <- &event.Event{
		Response: &model.Response{
			ID:     "runner-completion-test",
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		},
		StateDelta: map[string][]byte{
			"last_response":              []byte(`"final"`),
			graph.StateKeyLastResponseID: []byte(`"resp-final"`),
		},
	}
	close(events)

	subscriber := &mockTaskSubscriber{
		channel: make(chan protocol.StreamingMessageEvent, 4),
	}
	processor := createTestMessageProcessor()

	processor.processAgentStreamingEvents(
		ctx,
		"task-1",
		"user",
		"session",
		msg,
		events,
		subscriber,
		&mockTaskHandler{},
	)

	var results []protocol.StreamingMessageResult
	for {
		select {
		case streamEvent, ok := <-subscriber.channel:
			if !ok {
				goto doneArtifact
			}
			if streamEvent.Result != nil {
				results = append(results, streamEvent.Result)
			}
		default:
			goto doneArtifact
		}
	}

doneArtifact:
	if !assert.Len(t, results, 3) {
		return
	}

	_, ok := results[0].(*protocol.TaskStatusUpdateEvent)
	if !assert.True(t, ok, "expected submitted status event") {
		return
	}
	finalArtifact, ok := results[1].(*protocol.TaskArtifactUpdateEvent)
	if !assert.True(t, ok, "expected final artifact event") {
		return
	}
	completed, ok := results[2].(*protocol.TaskStatusUpdateEvent)
	if !assert.True(t, ok, "expected completed status event") {
		return
	}

	if assert.NotNil(t, finalArtifact.LastChunk, "expected final artifact marker") {
		assert.True(t, *finalArtifact.LastChunk)
	}
	assert.Empty(t, finalArtifact.Artifact.Parts)
	assert.Equal(t, "resp-final", finalArtifact.Metadata[ia2a.MessageMetadataResponseIDKey])
	assert.NotContains(t, finalArtifact.Metadata, ia2a.MessageMetadataObjectTypeKey)
	rawStateDelta, ok := finalArtifact.Metadata[ia2a.MessageMetadataStateDeltaKey]
	if assert.True(t, ok, "expected state_delta on final artifact") {
		decoded := ia2a.DecodeStateDeltaMetadata(rawStateDelta)
		assert.Equal(t, []byte(`"final"`), decoded["last_response"])
	}
	assert.Nil(t, completed.Metadata)
}

func TestProcessAgentStreamingEvents_HidesRunnerCompletion_MessageMode(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	events := make(chan *event.Event, 1)
	events <- &event.Event{
		Response: &model.Response{
			ID:     "runner-completion-test",
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		},
		StateDelta: map[string][]byte{
			"last_response":              []byte(`"final"`),
			graph.StateKeyLastResponseID: []byte(`"resp-final"`),
		},
	}
	close(events)

	subscriber := &mockTaskSubscriber{
		channel: make(chan protocol.StreamingMessageEvent, 4),
	}
	processor := createTestMessageProcessor()
	processor.streamingEventType = StreamingEventTypeMessage

	processor.processAgentStreamingEvents(
		ctx,
		"task-1",
		"user",
		"session",
		msg,
		events,
		subscriber,
		&mockTaskHandler{},
	)

	var results []protocol.StreamingMessageResult
	for {
		select {
		case streamEvent, ok := <-subscriber.channel:
			if !ok {
				goto doneMessage
			}
			if streamEvent.Result != nil {
				results = append(results, streamEvent.Result)
			}
		default:
			goto doneMessage
		}
	}

doneMessage:
	if !assert.Len(t, results, 2) {
		return
	}

	_, ok := results[0].(*protocol.TaskStatusUpdateEvent)
	if !assert.True(t, ok, "expected submitted status event") {
		return
	}
	completed, ok := results[1].(*protocol.TaskStatusUpdateEvent)
	if !assert.True(t, ok, "expected completed status event") {
		return
	}

	assert.Equal(t, protocol.TaskStateCompleted, completed.Status.State)
	assert.Equal(t, "resp-final", completed.Metadata[ia2a.MessageMetadataResponseIDKey])
	assert.NotContains(t, completed.Metadata, ia2a.MessageMetadataObjectTypeKey)
	rawStateDelta, ok := completed.Metadata[ia2a.MessageMetadataStateDeltaKey]
	if assert.True(t, ok, "expected state_delta on completed status") {
		decoded := ia2a.DecodeStateDeltaMetadata(rawStateDelta)
		assert.Equal(t, []byte(`"final"`), decoded["last_response"])
	}
}

func TestProcessAgentStreamingEvents_PropagatesRunnerCompletionError_TaskArtifact(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	code := "A2A_500"
	events := make(chan *event.Event, 1)
	events <- &event.Event{
		Response: &model.Response{
			ID:     "runner-completion-test",
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
			Error: &model.ResponseError{
				Type:    model.ErrorTypeFlowError,
				Message: "runner failed",
				Code:    &code,
			},
		},
		StateDelta: map[string][]byte{
			"last_response":              []byte(`"final"`),
			graph.StateKeyLastResponseID: []byte(`"resp-final"`),
		},
	}
	close(events)

	subscriber := &mockTaskSubscriber{
		channel: make(chan protocol.StreamingMessageEvent, 4),
	}
	processor := createTestMessageProcessor()

	processor.processAgentStreamingEvents(
		ctx,
		"task-1",
		"user",
		"session",
		msg,
		events,
		subscriber,
		&mockTaskHandler{},
	)

	var results []protocol.StreamingMessageResult
	for {
		select {
		case streamEvent, ok := <-subscriber.channel:
			if !ok {
				goto doneArtifactError
			}
			if streamEvent.Result != nil {
				results = append(results, streamEvent.Result)
			}
		default:
			goto doneArtifactError
		}
	}

doneArtifactError:
	if !assert.Len(t, results, 3) {
		return
	}

	finalArtifact, ok := results[1].(*protocol.TaskArtifactUpdateEvent)
	if !assert.True(t, ok, "expected final artifact event") {
		return
	}
	assert.Equal(t, model.ObjectTypeError, finalArtifact.Metadata[ia2a.MessageMetadataObjectTypeKey])
	assert.Equal(t, model.ErrorTypeFlowError, finalArtifact.Metadata[ia2a.MessageMetadataErrorTypeKey])
	assert.Equal(t, "runner failed", finalArtifact.Metadata[ia2a.MessageMetadataErrorMessageKey])
	assert.Equal(t, code, finalArtifact.Metadata[ia2a.MessageMetadataErrorCodeKey])
	assert.Equal(t, "resp-final", finalArtifact.Metadata[ia2a.MessageMetadataResponseIDKey])
	rawStateDelta, ok := finalArtifact.Metadata[ia2a.MessageMetadataStateDeltaKey]
	if assert.True(t, ok, "expected state_delta on final artifact") {
		decoded := ia2a.DecodeStateDeltaMetadata(rawStateDelta)
		assert.Equal(t, []byte(`"final"`), decoded["last_response"])
	}
}

func TestProcessAgentStreamingEvents_PropagatesRunnerCompletionError_MessageMode(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}
	code := "A2A_500"
	events := make(chan *event.Event, 1)
	events <- &event.Event{
		Response: &model.Response{
			ID:     "runner-completion-test",
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
			Error: &model.ResponseError{
				Type:    model.ErrorTypeFlowError,
				Message: "runner failed",
				Code:    &code,
			},
		},
		StateDelta: map[string][]byte{
			"last_response":              []byte(`"final"`),
			graph.StateKeyLastResponseID: []byte(`"resp-final"`),
		},
	}
	close(events)

	subscriber := &mockTaskSubscriber{
		channel: make(chan protocol.StreamingMessageEvent, 4),
	}
	processor := createTestMessageProcessor()
	processor.streamingEventType = StreamingEventTypeMessage

	processor.processAgentStreamingEvents(
		ctx,
		"task-1",
		"user",
		"session",
		msg,
		events,
		subscriber,
		&mockTaskHandler{},
	)

	var results []protocol.StreamingMessageResult
	for {
		select {
		case streamEvent, ok := <-subscriber.channel:
			if !ok {
				goto doneMessageError
			}
			if streamEvent.Result != nil {
				results = append(results, streamEvent.Result)
			}
		default:
			goto doneMessageError
		}
	}

doneMessageError:
	if !assert.Len(t, results, 2) {
		return
	}

	completed, ok := results[1].(*protocol.TaskStatusUpdateEvent)
	if !assert.True(t, ok, "expected completed status event") {
		return
	}

	assert.Equal(t, model.ObjectTypeError, completed.Metadata[ia2a.MessageMetadataObjectTypeKey])
	assert.Equal(t, model.ErrorTypeFlowError, completed.Metadata[ia2a.MessageMetadataErrorTypeKey])
	assert.Equal(t, "runner failed", completed.Metadata[ia2a.MessageMetadataErrorMessageKey])
	assert.Equal(t, code, completed.Metadata[ia2a.MessageMetadataErrorCodeKey])
	assert.Equal(t, "resp-final", completed.Metadata[ia2a.MessageMetadataResponseIDKey])
	rawStateDelta, ok := completed.Metadata[ia2a.MessageMetadataStateDeltaKey]
	if assert.True(t, ok, "expected state_delta on completed status") {
		decoded := ia2a.DecodeStateDeltaMetadata(rawStateDelta)
		assert.Equal(t, []byte(`"final"`), decoded["last_response"])
	}
}

func TestBuildFinalStreamingMetadata(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		assert.Nil(t, buildFinalStreamingMetadata(nil))
	})

	t.Run("empty event no metadata", func(t *testing.T) {
		evt := &event.Event{}
		assert.Nil(t, buildFinalStreamingMetadata(evt))
	})

	t.Run("only response id from state delta", func(t *testing.T) {
		evt := &event.Event{
			StateDelta: map[string][]byte{
				graph.StateKeyLastResponseID: []byte(`"chatcmpl-abc"`),
			},
		}
		meta := buildFinalStreamingMetadata(evt)
		assert.Equal(t, "chatcmpl-abc", meta[ia2a.MessageMetadataResponseIDKey])
		_, hasStateDelta := meta[ia2a.MessageMetadataStateDeltaKey]
		assert.True(t, hasStateDelta, "state_delta should be encoded")
	})

	t.Run("only error no state delta", func(t *testing.T) {
		evt := &event.Event{
			Response: &model.Response{
				Error: &model.ResponseError{
					Type:    model.ErrorTypeFlowError,
					Message: "boom",
				},
			},
		}
		meta := buildFinalStreamingMetadata(evt)
		assert.Equal(t, model.ObjectTypeError, meta[ia2a.MessageMetadataObjectTypeKey])
		assert.Equal(t, "boom", meta[ia2a.MessageMetadataErrorMessageKey])
	})

	t.Run("only state delta no response id key", func(t *testing.T) {
		evt := &event.Event{
			StateDelta: map[string][]byte{
				"custom_key": []byte(`"val"`),
			},
		}
		meta := buildFinalStreamingMetadata(evt)
		assert.Nil(t, meta[ia2a.MessageMetadataResponseIDKey])
		rawSD, ok := meta[ia2a.MessageMetadataStateDeltaKey]
		assert.True(t, ok)
		decoded := ia2a.DecodeStateDeltaMetadata(rawSD)
		assert.Equal(t, []byte(`"val"`), decoded["custom_key"])
	})

}

func TestFinalStreamingResponseID(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		assert.Empty(t, finalStreamingResponseID(nil))
	})

	t.Run("empty state delta", func(t *testing.T) {
		evt := &event.Event{}
		assert.Empty(t, finalStreamingResponseID(evt))
	})

	t.Run("missing key", func(t *testing.T) {
		evt := &event.Event{
			StateDelta: map[string][]byte{
				"other": []byte(`"x"`),
			},
		}
		assert.Empty(t, finalStreamingResponseID(evt))
	})

	t.Run("empty value", func(t *testing.T) {
		evt := &event.Event{
			StateDelta: map[string][]byte{
				graph.StateKeyLastResponseID: nil,
			},
		}
		assert.Empty(t, finalStreamingResponseID(evt))
	})

	t.Run("invalid json", func(t *testing.T) {
		evt := &event.Event{
			StateDelta: map[string][]byte{
				graph.StateKeyLastResponseID: []byte("{bad"),
			},
		}
		assert.Empty(t, finalStreamingResponseID(evt))
	})

	t.Run("valid json", func(t *testing.T) {
		evt := &event.Event{
			StateDelta: map[string][]byte{
				graph.StateKeyLastResponseID: []byte(`"chatcmpl-123"`),
			},
		}
		assert.Equal(t, "chatcmpl-123", finalStreamingResponseID(evt))
	})
}

func TestMergeRunnerCompletionStateDeltaIntoLastMessage(t *testing.T) {
	t.Run("empty messages", func(t *testing.T) {
		assert.False(t, mergeRunnerCompletionStateDeltaIntoLastMessage(
			nil, map[string][]byte{"k": []byte("v")},
		))
	})

	t.Run("empty state delta", func(t *testing.T) {
		msgs := []protocol.Message{{}}
		assert.False(t, mergeRunnerCompletionStateDeltaIntoLastMessage(msgs, nil))
	})

	t.Run("nil metadata on last message", func(t *testing.T) {
		msgs := []protocol.Message{{}}
		ok := mergeRunnerCompletionStateDeltaIntoLastMessage(
			msgs,
			map[string][]byte{"k": []byte(`"v"`)},
		)
		assert.True(t, ok)
		assert.NotNil(t, msgs[0].Metadata)
		rawSD := msgs[0].Metadata[ia2a.MessageMetadataStateDeltaKey]
		decoded := ia2a.DecodeStateDeltaMetadata(rawSD)
		assert.Equal(t, []byte(`"v"`), decoded["k"])
	})

	t.Run("merge with existing state delta", func(t *testing.T) {
		existing := EncodeStateDeltaMetadata(map[string][]byte{
			"old": []byte(`"old_val"`),
		})
		msgs := []protocol.Message{{
			Metadata: map[string]any{
				ia2a.MessageMetadataStateDeltaKey: existing,
			},
		}}
		ok := mergeRunnerCompletionStateDeltaIntoLastMessage(
			msgs,
			map[string][]byte{"new": []byte(`"new_val"`)},
		)
		assert.True(t, ok)
		rawSD := msgs[0].Metadata[ia2a.MessageMetadataStateDeltaKey]
		decoded := ia2a.DecodeStateDeltaMetadata(rawSD)
		assert.Equal(t, []byte(`"old_val"`), decoded["old"])
		assert.Equal(t, []byte(`"new_val"`), decoded["new"])
	})

	t.Run("nil value in state delta", func(t *testing.T) {
		msgs := []protocol.Message{{}}
		ok := mergeRunnerCompletionStateDeltaIntoLastMessage(
			msgs,
			map[string][]byte{"k": nil},
		)
		assert.True(t, ok)
	})

}

func TestCloneStateDeltaBytes(t *testing.T) {
	assert.Nil(t, cloneStateDeltaBytes(nil))

	original := []byte("hello")
	cloned := cloneStateDeltaBytes(original)
	assert.Equal(t, original, cloned)
	cloned[0] = 'H'
	assert.NotEqual(t, original, cloned, "clone must not share backing array")
}

func TestNormalizeResponseResults(t *testing.T) {
	t.Run("nil inputs", func(t *testing.T) {
		proc := &messageProcessor{}
		assert.Nil(t, proc.rewriteUnaryResult(nil))
		assert.Nil(t, proc.rewriteStreamingResult(nil))
		assert.Nil(t, normalizeProtocolMessage(nil))
		assert.Nil(t, normalizeTask(nil))
		assert.Nil(t, normalizeTaskArtifactUpdateEvent(nil))
		assert.Nil(t, normalizeTaskStatusUpdateEvent(nil))
		assert.Nil(t, normalizeArtifact(nil))
	})

	t.Run("empty message with only response id is dropped", func(t *testing.T) {
		msg := &protocol.Message{
			Metadata: map[string]any{
				ia2a.MessageMetadataResponseIDKey: "resp-1",
			},
		}
		assert.Nil(t, normalizeProtocolMessage(msg))
	})

	t.Run("task normalizes nested empty messages and artifacts", func(t *testing.T) {
		task := &protocol.Task{
			Metadata: map[string]any{},
			Status: protocol.TaskStatus{
				Message: &protocol.Message{
					Metadata: map[string]any{
						ia2a.MessageMetadataResponseIDKey: "resp-1",
					},
				},
			},
			History: []protocol.Message{
				{
					Metadata: map[string]any{
						ia2a.MessageMetadataResponseIDKey: "resp-history",
					},
				},
				{
					Parts: []protocol.Part{protocol.NewTextPart("keep")},
				},
			},
			Artifacts: []protocol.Artifact{
				{},
				{
					Metadata: map[string]any{"business": "keep"},
				},
			},
		}

		normalized := normalizeTask(task)
		assert.Nil(t, normalized.Metadata)
		assert.Nil(t, normalized.Status.Message)
		if assert.Len(t, normalized.History, 1) {
			assert.Equal(t, "keep", normalized.History[0].Parts[0].(protocol.TextPart).Text)
		}
		if assert.Len(t, normalized.Artifacts, 1) {
			assert.Equal(t, "keep", normalized.Artifacts[0].Metadata["business"])
		}
	})

	t.Run("artifact update keeps final or contentful metadata", func(t *testing.T) {
		lastChunk := true
		assert.NotNil(t, normalizeTaskArtifactUpdateEvent(
			&protocol.TaskArtifactUpdateEvent{LastChunk: &lastChunk},
		))
		assert.NotNil(t, normalizeTaskArtifactUpdateEvent(
			&protocol.TaskArtifactUpdateEvent{
				Metadata: map[string]any{"business": "keep"},
			},
		))
		assert.Nil(t, normalizeTaskArtifactUpdateEvent(
			&protocol.TaskArtifactUpdateEvent{
				Metadata: map[string]any{
					ia2a.MessageMetadataResponseIDKey: "resp-1",
				},
			},
		))
	})

	t.Run("streaming task falls through unchanged", func(t *testing.T) {
		task := &protocol.Task{ID: "task-1"}
		result := normalizeStreamingResult(task)
		assert.Same(t, task, result)
	})
}

// TestMessageProcessor_ProcessMessage_NoPartsCollected tests handling when no parts are collected
func TestMessageProcessor_ProcessMessage_NoPartsCollected(t *testing.T) {
	ctxID := "ctx"
	ctx := context.Background()
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "no-parts-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	processor := &messageProcessor{
		debugLogging: false,
		runner: &mockRunner{
			runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 1)
				// Send event that converts to nil result
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{Message: model.Message{Content: "response"}}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{
			convertToA2AMessageFunc: func(ctx context.Context, event *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
				// Return nil to simulate no parts collected
				return nil, nil
			},
		},
		errorHandler: defaultErrorHandler,
	}

	result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
	// handleError returns a result with error message, not an error
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Result)
}

func TestMessageProcessor_ProcessMessage_ResponseRewriterDropFinalResult(
	t *testing.T,
) {
	ctxID := "ctx"
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "drop-final-result-test",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}
	processor := &messageProcessor{
		responseRewriter: ResponseRewriterFuncs{
			Unary: func(
				result protocol.UnaryMessageResult,
			) protocol.UnaryMessageResult {
				return nil
			},
		},
		runner: &mockRunner{
			runFunc: func(
				ctx context.Context,
				userID string,
				sessionID string,
				message model.Message,
				opts ...agent.RunOption,
			) (<-chan *event.Event, error) {
				ch := make(chan *event.Event, 1)
				ch <- &event.Event{
					Response: &model.Response{
						Choices: []model.Choice{{
							Message: model.Message{Content: "answer"},
						}},
					},
				}
				close(ch)
				return ch, nil
			},
		},
		a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
		eventToA2AConverter: &mockEventToA2AConverter{},
		errorHandler:        defaultErrorHandler,
	}

	result, err := processor.processMessage(
		context.Background(),
		"user",
		"session",
		&msg,
		&model.Message{Content: "input"},
		nil,
	)
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		assert.Nil(t, result.Result)
		assert.Nil(t, result.StreamingEvents)
	}
}

// TestMessageProcessor_ProcessMessage_SkipsRunnerCompletion tests that runner.completion events
// are filtered out in the non-streaming processMessage path, so only real content events are
// converted to A2A messages.
func TestMessageProcessor_ProcessMessage_SkipsRunnerCompletion(t *testing.T) {
	ctxID := "ctx"
	ctx := context.Background()
	msg := protocol.Message{
		ContextID: &ctxID,
		MessageID: "skip-runner-completion",
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart("hi")},
	}

	t.Run("single_content_event_followed_by_runner_completion", func(t *testing.T) {
		var convertCallCount int
		processor := &messageProcessor{
			debugLogging: false,
			runner: &mockRunner{
				runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
					ch := make(chan *event.Event, 2)
					ch <- &event.Event{
						Response: &model.Response{
							Object:  "chat.completion",
							Done:    true,
							Choices: []model.Choice{{Message: model.Message{Content: "real answer"}}},
						},
					}
					ch <- &event.Event{
						Response: &model.Response{
							Object: model.ObjectTypeRunnerCompletion,
							Done:   true,
						},
					}
					close(ch)
					return ch, nil
				},
			},
			a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
			eventToA2AConverter: &mockEventToA2AConverter{
				convertToA2AMessageFunc: func(ctx context.Context, evt *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
					convertCallCount++
					return &protocol.Message{
						Role:  protocol.MessageRoleAgent,
						Parts: []protocol.Part{protocol.NewTextPart(evt.Response.Choices[0].Message.Content)},
					}, nil
				},
			},
			errorHandler: defaultErrorHandler,
		}

		result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.Result)

		// runner.completion should be skipped, so converter is called only once
		assert.Equal(t, 1, convertCallCount, "converter should be called once; runner.completion must be skipped")

		// Result should be a single Message (not a Task), because only one message was collected
		resultMsg, ok := result.Result.(*protocol.Message)
		assert.True(t, ok, "Expected *protocol.Message, got %T", result.Result)
		assert.Equal(t, protocol.MessageRoleAgent, resultMsg.Role)
	})

	t.Run("multiple_content_events_followed_by_runner_completion", func(t *testing.T) {
		var convertCallCount int
		processor := &messageProcessor{
			debugLogging: false,
			runner: &mockRunner{
				runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
					ch := make(chan *event.Event, 3)
					ch <- &event.Event{
						Response: &model.Response{
							Object:  "chat.completion",
							Choices: []model.Choice{{Message: model.Message{Content: "tool call"}}},
						},
					}
					ch <- &event.Event{
						Response: &model.Response{
							Object:  "chat.completion",
							Done:    true,
							Choices: []model.Choice{{Message: model.Message{Content: "final answer"}}},
						},
					}
					ch <- &event.Event{
						Response: &model.Response{
							Object: model.ObjectTypeRunnerCompletion,
							Done:   true,
						},
					}
					close(ch)
					return ch, nil
				},
			},
			a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
			eventToA2AConverter: &mockEventToA2AConverter{
				convertToA2AMessageFunc: func(ctx context.Context, evt *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
					convertCallCount++
					return &protocol.Message{
						Role:  protocol.MessageRoleAgent,
						Parts: []protocol.Part{protocol.NewTextPart(evt.Response.Choices[0].Message.Content)},
					}, nil
				},
			},
			errorHandler: defaultErrorHandler,
		}

		result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.Result)

		// runner.completion should be skipped, so converter is called only twice
		assert.Equal(t, 2, convertCallCount, "converter should be called twice; runner.completion must be skipped")

		// Multiple messages → result is a Task
		resultTask, ok := result.Result.(*protocol.Task)
		assert.True(t, ok, "Expected *protocol.Task for multiple events, got %T", result.Result)
		assert.Equal(t, 1, len(resultTask.History), "history should contain the first message")
		assert.Equal(t, 1, len(resultTask.Artifacts), "artifacts should contain the last content message")
	})

	t.Run("only_runner_completion_returns_no_response_error", func(t *testing.T) {
		processor := &messageProcessor{
			debugLogging: false,
			runner: &mockRunner{
				runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
					ch := make(chan *event.Event, 1)
					ch <- &event.Event{
						Response: &model.Response{
							Object: model.ObjectTypeRunnerCompletion,
							Done:   true,
						},
					}
					close(ch)
					return ch, nil
				},
			},
			a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
			eventToA2AConverter: &mockEventToA2AConverter{},
			errorHandler:        defaultErrorHandler,
		}

		result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
		// When all events are runner.completion, no messages are collected → error path
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.Result)
	})

	t.Run("runner_completion_with_echoed_choices_is_preserved", func(t *testing.T) {
		var convertCallCount int
		processor := &messageProcessor{
			debugLogging: false,
			runner: &mockRunner{
				runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
					ch := make(chan *event.Event, 1)
					ch <- &event.Event{
						Response: &model.Response{
							Object:  model.ObjectTypeRunnerCompletion,
							Done:    true,
							Choices: []model.Choice{{Message: model.Message{Content: "graph final answer"}}},
						},
					}
					close(ch)
					return ch, nil
				},
			},
			a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
			eventToA2AConverter: &mockEventToA2AConverter{
				convertToA2AMessageFunc: func(ctx context.Context, evt *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
					convertCallCount++
					return &protocol.Message{
						Role:  protocol.MessageRoleAgent,
						Parts: []protocol.Part{protocol.NewTextPart(evt.Response.Choices[0].Message.Content)},
					}, nil
				},
			},
			errorHandler: defaultErrorHandler,
		}

		result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.Result)

		assert.Equal(t, 1, convertCallCount, "runner.completion with choices must not be skipped")

		resultMsg, ok := result.Result.(*protocol.Message)
		if assert.True(t, ok, "Expected *protocol.Message, got %T", result.Result) {
			assert.NotEmpty(t, resultMsg.Parts)
			assert.Equal(t, "graph final answer", resultMsg.Parts[0].(protocol.TextPart).Text)
		}
	})

	t.Run("runner_completion_with_state_delta_only_is_preserved", func(t *testing.T) {
		var convertCallCount int
		processor := &messageProcessor{
			debugLogging: false,
			runner: &mockRunner{
				runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
					ch := make(chan *event.Event, 2)
					ch <- &event.Event{
						Response: &model.Response{
							Object:  "chat.completion",
							Done:    true,
							Choices: []model.Choice{{Message: model.Message{Content: "answer"}}},
						},
					}
					ch <- &event.Event{
						Response: &model.Response{
							Object: model.ObjectTypeRunnerCompletion,
							Done:   true,
						},
						StateDelta: map[string][]byte{
							"graph_state": []byte(`"completed"`),
						},
					}
					close(ch)
					return ch, nil
				},
			},
			a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
			eventToA2AConverter: &mockEventToA2AConverter{
				convertToA2AMessageFunc: func(ctx context.Context, evt *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
					convertCallCount++
					return &protocol.Message{
						Role:  protocol.MessageRoleAgent,
						Parts: []protocol.Part{protocol.NewTextPart(evt.Response.Choices[0].Message.Content)},
					}, nil
				},
			},
			errorHandler: defaultErrorHandler,
		}

		result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.Result)

		// State-delta-only runner completion should not become a separate
		// converted message. It should be merged into the latest content message.
		assert.Equal(t, 1, convertCallCount, "runner.completion state_delta should be merged into the latest message")

		resultMsg, ok := result.Result.(*protocol.Message)
		if assert.True(t, ok, "Expected *protocol.Message, got %T", result.Result) {
			assert.NotEmpty(t, resultMsg.Parts)
			assert.Equal(t, "answer", resultMsg.Parts[0].(protocol.TextPart).Text)

			rawStateDelta, exists := resultMsg.Metadata[ia2a.MessageMetadataStateDeltaKey]
			if assert.True(t, exists, "expected merged state_delta metadata on final message") {
				decoded := DecodeStateDeltaMetadata(rawStateDelta)
				assert.Equal(t, []byte(`"completed"`), decoded["graph_state"])
			}
		}
	})

	t.Run("runner_completion_with_blocked_state_delta_stays_hidden", func(t *testing.T) {
		var convertCallCount int
		processor := &messageProcessor{
			debugLogging: false,
			responseRewriter: ResponseRewriterFuncs{
				Unary: func(result protocol.UnaryMessageResult) protocol.UnaryMessageResult {
					msg, ok := result.(*protocol.Message)
					if !ok {
						return result
					}
					delete(msg.Metadata, ia2a.MessageMetadataStateDeltaKey)
					return msg
				},
			},
			runner: &mockRunner{
				runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
					ch := make(chan *event.Event, 2)
					ch <- &event.Event{
						Response: &model.Response{
							Object:  "chat.completion",
							Done:    true,
							Choices: []model.Choice{{Message: model.Message{Content: "answer"}}},
						},
					}
					ch <- &event.Event{
						Response: &model.Response{
							Object: model.ObjectTypeRunnerCompletion,
							Done:   true,
						},
						StateDelta: map[string][]byte{
							"graph_state": []byte(`"completed"`),
						},
					}
					close(ch)
					return ch, nil
				},
			},
			a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
			eventToA2AConverter: &mockEventToA2AConverter{
				convertToA2AMessageFunc: func(ctx context.Context, evt *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
					convertCallCount++
					return &protocol.Message{
						Role:  protocol.MessageRoleAgent,
						Parts: []protocol.Part{protocol.NewTextPart(evt.Response.Choices[0].Message.Content)},
					}, nil
				},
			},
			errorHandler: defaultErrorHandler,
		}

		result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.Result)

		assert.Equal(t, 1, convertCallCount, "rewritten state_delta should not re-expose runner.completion")

		resultMsg, ok := result.Result.(*protocol.Message)
		if assert.True(t, ok, "Expected *protocol.Message, got %T", result.Result) {
			assert.NotEmpty(t, resultMsg.Parts)
			assert.Equal(t, "answer", resultMsg.Parts[0].(protocol.TextPart).Text)
			assert.Nil(t, resultMsg.Metadata)
		}
	})

	t.Run("default_converter_response_rewriter_runs_once", func(t *testing.T) {
		rewriteCallCount := 0
		processor := &messageProcessor{
			debugLogging: false,
			responseRewriter: ResponseRewriterFuncs{
				Unary: func(result protocol.UnaryMessageResult) protocol.UnaryMessageResult {
					rewriteCallCount++
					msg, ok := result.(*protocol.Message)
					if !ok {
						return result
					}
					msg.Metadata["rewrite_call_count"] = rewriteCallCount
					return msg
				},
			},
			runner: &mockRunner{
				runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
					ch := make(chan *event.Event, 1)
					ch <- &event.Event{
						Response: &model.Response{
							ID:      "resp-filter-once",
							Object:  "graph.execution",
							Choices: []model.Choice{{Message: model.Message{}}},
						},
						StateDelta: map[string][]byte{
							"business_result": []byte(`"ok"`),
						},
					}
					close(ch)
					return ch, nil
				},
			},
			a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
			eventToA2AConverter: &defaultEventToA2AMessage{
				graphEventObjectAllowlist: []string{"graph.*"},
			},
			errorHandler: defaultErrorHandler,
		}

		result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
		assert.NoError(t, err)
		resultMsg, ok := result.Result.(*protocol.Message)
		if !assert.True(t, ok, "Expected *protocol.Message, got %T", result.Result) {
			return
		}
		assert.Equal(t, 1, rewriteCallCount)
		assert.Equal(t, 1, resultMsg.Metadata["rewrite_call_count"])
	})

	t.Run("custom_converter_output_uses_response_rewriter", func(t *testing.T) {
		processor := &messageProcessor{
			debugLogging: false,
			responseRewriter: ResponseRewriterFuncs{
				Unary: func(result protocol.UnaryMessageResult) protocol.UnaryMessageResult {
					msg, ok := result.(*protocol.Message)
					if !ok {
						return result
					}
					raw, ok := msg.Metadata[ia2a.MessageMetadataStateDeltaKey]
					if !ok {
						return msg
					}
					stateDelta := DecodeStateDeltaMetadata(raw)
					delete(stateDelta, "debug_trace")
					msg.Metadata[ia2a.MessageMetadataStateDeltaKey] = EncodeStateDeltaMetadata(stateDelta)
					return msg
				},
			},
			runner: &mockRunner{
				runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
					ch := make(chan *event.Event, 1)
					ch <- &event.Event{
						Response: &model.Response{
							Object:  "chat.completion",
							Done:    true,
							Choices: []model.Choice{{Message: model.Message{Content: "answer"}}},
						},
					}
					close(ch)
					return ch, nil
				},
			},
			a2aToAgentConverter: &defaultA2AMessageToAgentMessage{},
			eventToA2AConverter: &mockEventToA2AConverter{
				convertToA2AMessageFunc: func(ctx context.Context, evt *event.Event, options EventToA2AUnaryOptions) (protocol.UnaryMessageResult, error) {
					return &protocol.Message{
						Role:  protocol.MessageRoleAgent,
						Parts: []protocol.Part{protocol.NewTextPart("answer")},
						Metadata: map[string]any{
							ia2a.MessageMetadataStateDeltaKey: EncodeStateDeltaMetadata(map[string][]byte{
								"business_result": []byte(`"ok"`),
								"debug_trace":     []byte(`"drop-me"`),
							}),
						},
					}, nil
				},
			},
			errorHandler: defaultErrorHandler,
		}

		result, err := processor.processMessage(ctx, "user", "session", &msg, &model.Message{Content: "input"}, nil)
		assert.NoError(t, err)
		resultMsg, ok := result.Result.(*protocol.Message)
		if !assert.True(t, ok, "Expected *protocol.Message, got %T", result.Result) {
			return
		}
		rawStateDelta, ok := resultMsg.Metadata[ia2a.MessageMetadataStateDeltaKey]
		if !assert.True(t, ok, "expected state_delta metadata on result message") {
			return
		}
		decoded := DecodeStateDeltaMetadata(rawStateDelta)
		assert.Contains(t, decoded, "business_result")
		assert.NotContains(t, decoded, "debug_trace")
	})
}

// TestTraceContextMiddleware_Extract tests that trace context is extracted from HTTP headers
func TestTraceContextMiddleware_Extract(t *testing.T) {
	// Save the original propagator and restore it after the test
	originalPropagator := otel.GetTextMapPropagator()
	defer otel.SetTextMapPropagator(originalPropagator)

	// Set up the W3C Trace Context propagator
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Create a valid traceparent header
	traceparent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	// Create middleware
	middleware := &traceContextMiddleware{}

	// Track if the handler received a context with trace info
	var receivedCtx context.Context
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})

	// Wrap the handler
	wrapped := middleware.Wrap(handler)

	// Create request with traceparent header
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("traceparent", traceparent)
	rec := httptest.NewRecorder()

	// Execute
	wrapped.ServeHTTP(rec, req)

	// Verify the context contains trace info
	spanContext := trace.SpanContextFromContext(receivedCtx)
	assert.True(t, spanContext.IsValid(), "Expected valid span context")
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", spanContext.TraceID().String())
	assert.Equal(t, "00f067aa0ba902b7", spanContext.SpanID().String())
}

// TestTraceContextMiddleware_NoTraceparent tests that context is unchanged when no traceparent header
func TestTraceContextMiddleware_NoTraceparent(t *testing.T) {
	// Save the original propagator and restore it after the test
	originalPropagator := otel.GetTextMapPropagator()
	defer otel.SetTextMapPropagator(originalPropagator)

	// Set up the W3C Trace Context propagator
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Create middleware
	middleware := &traceContextMiddleware{}

	// Track the received context
	var receivedCtx context.Context
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})

	// Wrap the handler
	wrapped := middleware.Wrap(handler)

	// Create request WITHOUT traceparent header
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	// Execute
	wrapped.ServeHTTP(rec, req)

	// Verify the context does not contain valid trace info
	spanContext := trace.SpanContextFromContext(receivedCtx)
	assert.False(t, spanContext.IsValid(), "Expected invalid span context when no traceparent")
}

func TestBuildA2AServer_BuildProcessorErrorWrapping(t *testing.T) {
	opts := &options{
		sessionService: &mockSessionService{},
		errorHandler:   defaultErrorHandler,
		agentCard: &a2a.AgentCard{
			Name: "valid",
			URL:  "http://localhost:8080",
		},
	}
	_, err := buildA2AServer(opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to build processor:")
}

func TestProcessStreamingMessage_CleanupTask_OnSubscribeError(t *testing.T) {
	t.Run("cleanup succeeds", func(t *testing.T) {
		ctx := context.Background()
		ctxID := "ctx"
		msg := &protocol.Message{ContextID: &ctxID}

		var cleanedTaskID string
		handler := &mockTaskHandler{
			buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
				return "task-cleanup", nil
			},
			subscribeTaskFunc: func(taskID *string) (taskmanager.TaskSubscriber, error) {
				return nil, fmt.Errorf("subscribe failed")
			},
			cleanTaskFunc: func(taskID *string) error {
				cleanedTaskID = *taskID
				return nil
			},
		}

		proc := createTestMessageProcessor()
		result, err := proc.processStreamingMessage(ctx, "user", "session", msg, &model.Message{Content: "hi"}, handler, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "task-cleanup", cleanedTaskID)
	})

	t.Run("cleanup itself fails should not panic", func(t *testing.T) {
		ctx := context.Background()
		ctxID := "ctx"
		msg := &protocol.Message{ContextID: &ctxID}

		var cleanCalled bool
		handler := &mockTaskHandler{
			buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
				return "task-clean-fail", nil
			},
			subscribeTaskFunc: func(taskID *string) (taskmanager.TaskSubscriber, error) {
				return nil, fmt.Errorf("subscribe failed")
			},
			cleanTaskFunc: func(taskID *string) error {
				cleanCalled = true
				return fmt.Errorf("clean error")
			},
		}

		proc := createTestMessageProcessor()
		assert.NotPanics(t, func() {
			_, _ = proc.processStreamingMessage(ctx, "user", "session", msg, &model.Message{Content: "hi"}, handler, nil)
		})
		assert.True(t, cleanCalled)
	})
}

func TestProcessStreamingMessage_CleanupTask_OnRunnerError(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}

	var cleanedTaskID string
	var subscriberClosed bool
	sub := &mockTaskSubscriber{
		closeFunc: func() { subscriberClosed = true },
	}
	handler := &mockTaskHandler{
		buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
			return "task-runner-err", nil
		},
		subscribeTaskFunc: func(taskID *string) (taskmanager.TaskSubscriber, error) {
			return sub, nil
		},
		cleanTaskFunc: func(taskID *string) error {
			cleanedTaskID = *taskID
			return nil
		},
	}

	proc := createTestMessageProcessor()
	proc.runner = &mockRunner{
		runFunc: func(ctx context.Context, userID string, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
			return nil, errors.New("runner boom")
		},
	}

	result, err := proc.processStreamingMessage(ctx, "user", "session", msg, &model.Message{Content: "hi"}, handler, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "task-runner-err", cleanedTaskID)
	assert.True(t, subscriberClosed)
}

func TestAbortStreaming_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}

	events := make(chan *event.Event, 1)
	events <- &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{Delta: model.Message{Content: "chunk"}}},
		},
	}
	close(events)

	sendCount := 0
	sub := &mockTaskSubscriber{
		sendFunc: func(evt protocol.StreamingMessageEvent) error {
			sendCount++
			if sendCount == 1 {
				// After sending the submitted event, cancel context so tunnel returns Canceled
				cancel()
				return nil
			}
			// Subsequent sends fail with context.Canceled
			return context.Canceled
		},
	}

	proc := createTestMessageProcessor()
	assert.NotPanics(t, func() {
		proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, &mockTaskHandler{})
	})
}

func TestAbortStreaming_DeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}

	events := make(chan *event.Event)
	close(events)

	sendCount := 0
	sub := &mockTaskSubscriber{
		sendFunc: func(evt protocol.StreamingMessageEvent) error {
			sendCount++
			if sendCount == 1 {
				return nil // submitted event succeeds
			}
			return context.DeadlineExceeded
		},
	}

	proc := createTestMessageProcessor()
	assert.NotPanics(t, func() {
		proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, &mockTaskHandler{})
	})
}

func TestAbortStreaming_OtherError_HandleStreamingErrorFails(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}

	events := make(chan *event.Event)
	close(events)

	proc := createTestMessageProcessor()
	proc.errorHandler = func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
		return nil, fmt.Errorf("handler also failed")
	}

	sendCount := 0
	sub := &mockTaskSubscriber{
		sendFunc: func(evt protocol.StreamingMessageEvent) error {
			sendCount++
			if sendCount == 1 {
				return nil // submitted succeeds
			}
			return fmt.Errorf("send error")
		},
	}

	assert.NotPanics(t, func() {
		proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, &mockTaskHandler{})
	}, "handleStreamingProcessingError failure in abortStreaming should not panic")
}

func TestAbortStreaming_FinalArtifactSendFail_Aborts(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}

	events := make(chan *event.Event)
	close(events) // no agent events

	sendCount := 0
	sub := &mockTaskSubscriber{
		sendFunc: func(evt protocol.StreamingMessageEvent) error {
			sendCount++
			if sendCount == 1 {
				return nil // submitted succeeds
			}
			if sendCount == 2 {
				// final artifact send fails
				return fmt.Errorf("artifact send fail")
			}
			return nil
		},
	}

	var handlerCalled bool
	proc := createTestMessageProcessor()
	proc.errorHandler = func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
		handlerCalled = true
		res := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart("err")})
		return &res, nil
	}

	proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, &mockTaskHandler{})
	assert.True(t, handlerCalled, "error handler should fire for final artifact send failure")
	// Should abort before sending completed
	assert.Equal(t, 3, sendCount, "should be: submitted + artifact fail + error msg")
}

func TestAbortStreaming_CompletedSendFail_Aborts(t *testing.T) {
	ctx := context.Background()
	ctxID := "ctx"
	msg := &protocol.Message{ContextID: &ctxID}

	events := make(chan *event.Event)
	close(events)

	sendCount := 0
	sub := &mockTaskSubscriber{
		sendFunc: func(evt protocol.StreamingMessageEvent) error {
			sendCount++
			if sendCount == 3 {
				// completed send fails
				return fmt.Errorf("completed send fail")
			}
			return nil
		},
	}

	var handlerCalled bool
	proc := createTestMessageProcessor()
	proc.errorHandler = func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
		handlerCalled = true
		res := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart("err")})
		return &res, nil
	}

	proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, &mockTaskHandler{})
	assert.True(t, handlerCalled, "error handler should fire for completed send failure")
}

func TestBuildRuntimeState(t *testing.T) {
	t.Run("empty metadata", func(t *testing.T) {
		result := buildRuntimeState(map[string]any{})
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("copies all entries", func(t *testing.T) {
		metadata := map[string]any{
			"key1": "value1",
			"key2": 42,
			"key3": true,
		}
		result := buildRuntimeState(metadata)
		assert.Equal(t, metadata, result)
	})

	t.Run("shallow copy - modifications don't affect original", func(t *testing.T) {
		metadata := map[string]any{
			"key1": "value1",
		}
		result := buildRuntimeState(metadata)
		result["key2"] = "new"
		assert.NotContains(t, metadata, "key2", "original should not be affected")
	})

	t.Run("nil metadata produces empty map", func(t *testing.T) {
		result := buildRuntimeState(nil)
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})
}

func TestProcessAgentStreamingEvents_CleanupTaskInDefer(t *testing.T) {
	t.Run("cleanup called on normal completion", func(t *testing.T) {
		ctx := context.Background()
		ctxID := "ctx"
		msg := &protocol.Message{ContextID: &ctxID}

		events := make(chan *event.Event)
		close(events)

		var cleanedTaskID string
		handler := &mockTaskHandler{
			cleanTaskFunc: func(taskID *string) error {
				cleanedTaskID = *taskID
				return nil
			},
		}

		sub := &mockTaskSubscriber{
			sendFunc: func(evt protocol.StreamingMessageEvent) error { return nil },
		}

		proc := createTestMessageProcessor()
		proc.processAgentStreamingEvents(ctx, "my-task", "user1", "session1", msg, events, sub, handler)
		assert.Equal(t, "my-task", cleanedTaskID)
	})

	t.Run("cleanup error should not panic", func(t *testing.T) {
		ctx := context.Background()
		ctxID := "ctx"
		msg := &protocol.Message{ContextID: &ctxID}

		events := make(chan *event.Event)
		close(events)

		handler := &mockTaskHandler{
			cleanTaskFunc: func(taskID *string) error {
				return fmt.Errorf("defer clean error")
			},
		}

		sub := &mockTaskSubscriber{
			sendFunc: func(evt protocol.StreamingMessageEvent) error { return nil },
		}

		proc := createTestMessageProcessor()
		assert.NotPanics(t, func() {
			proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, sub, handler)
		})
	})
}
