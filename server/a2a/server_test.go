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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
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
		handler := &mockTaskHandler{
			buildTaskFunc: func(specificTaskID *string, contextID *string) (string, error) {
				return "task", nil
			},
			subscribeTaskFunc: func(taskID *string) (taskmanager.TaskSubscriber, error) {
				return nil, fmt.Errorf("subscribe failed")
			},
		}
		result, err := processor.processStreamingMessage(ctx, "user", "session", msg, &model.Message{}, handler, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.StreamingEvents)
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
		cont, err := proc.processBatchStreamingEvents(ctx, taskID, msg, []*event.Event{}, sub)
		assert.NoError(t, err)
		assert.True(t, cont)
	})

	t.Run("nil_event_entries", func(t *testing.T) {
		proc := createTestMessageProcessor()
		sub := &mockTaskSubscriber{}
		batch := []*event.Event{{}, nil}
		cont, err := proc.processBatchStreamingEvents(ctx, taskID, msg, batch, sub)
		assert.NoError(t, err)
		assert.True(t, cont)
	})

	t.Run("converter_error", func(t *testing.T) {
		proc := createTestMessageProcessor()
		proc.eventToA2AConverter = streamingErrorConverter{}
		sub := &mockTaskSubscriber{}
		evt := &event.Event{Response: &model.Response{}}
		_, err := proc.processBatchStreamingEvents(ctx, taskID, msg, []*event.Event{evt}, sub)
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
		_, err := proc.processBatchStreamingEvents(ctx, taskID, msg, []*event.Event{evt}, sendErrSub)
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
		}
		cont, err := proc.processBatchStreamingEvents(ctx, taskID, msg, []*event.Event{final}, sub)
		assert.NoError(t, err)
		assert.False(t, cont)
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

	proc.processAgentStreamingEvents(ctx, "task", "user1", "session1", msg, events, &mockTaskSubscriber{}, &mockTaskHandler{})
	assert.True(t, handlerCalled)
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
			errMsg:  "agent is required",
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
			name:    "no options",
			opts:    []Option{},
			wantErr: true,
			errMsg:  "agent is required",
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
					Streaming: boolPtr(true),
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
					Streaming: boolPtr(true),
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
					Streaming: boolPtr(true),
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
					Streaming: boolPtr(true),
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
					Streaming: boolPtr(false),
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
			result := buildAgentCard(tt.options)
			if !compareAgentCards(result, tt.expected) {
				t.Errorf("buildAgentCard() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestBuildProcessor(t *testing.T) {
	tests := []struct {
		name    string
		agent   agent.Agent
		session session.Service
		options *options
	}{
		{
			name:    "default converters",
			agent:   &mockAgent{name: "test-agent", description: "test description"},
			session: inmemory.NewSessionService(),
			options: &options{},
		},
		{
			name:    "custom converters",
			agent:   &mockAgent{name: "test-agent", description: "test description"},
			session: inmemory.NewSessionService(),
			options: &options{
				a2aToAgentConverter: &mockA2AToAgentConverter{},
				eventToA2AConverter: &mockEventToA2AConverter{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := buildProcessor(tt.agent, tt.session, tt.options)
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
		})
	}
}

func TestBuildSkillsFromTools(t *testing.T) {
	tests := []struct {
		name      string
		agent     agent.Agent
		agentName string
		agentDesc string
		expected  []a2a.AgentSkill
	}{
		{
			name:      "no tools",
			agent:     &mockAgent{tools: []tool.Tool{}},
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
			agent: &mockAgent{
				tools: []tool.Tool{
					&mockTool{name: "calculator", description: "math tool"},
					&mockTool{name: "weather", description: "weather tool"},
				},
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
			result := buildSkillsFromTools(tt.agent, tt.agentName, tt.agentDesc)
			if !compareSkills(result, tt.expected) {
				t.Errorf("buildSkillsFromTools() = %+v, want %+v", result, tt.expected)
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
	return compareSkills(a.Skills, b.Skills) &&
		compareStringSlices(a.DefaultInputModes, b.DefaultInputModes) &&
		compareStringSlices(a.DefaultOutputModes, b.DefaultOutputModes)
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
