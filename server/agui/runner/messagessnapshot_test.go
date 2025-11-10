//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestMessagesSnapshotRequiresAppName(t *testing.T) {
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		sessionService:    &testSessionService{},
	}

	ch, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

func TestMessagesSnapshotRequiresSessionService(t *testing.T) {
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
	}

	ch, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

func TestMessagesSnapshotHappyPath(t *testing.T) {
	events := []event.Event{
		newResponse(model.RoleUser, "hello", nil),
		newResponse(model.RoleSystem, "system", nil),
		newResponse(model.RoleAssistant, "reply", func(m *model.Message) {
			m.ToolName = "calc"
			m.ToolCalls = []model.ToolCall{{
				ID:   "tool-call-1",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "calc",
					Arguments: []byte("{}"),
				},
			}}
		}),
		newResponse(model.RoleTool, "42", func(m *model.Message) {
			m.ToolName = "calc"
			m.ToolID = "tool-call-1"
		}),
	}

	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		sessionService:    &testSessionService{events: events},
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.NoError(t, err)

	collected := collectAGUIEvents(t, stream)
	assert.Len(t, collected, 3)

	_, ok := collected[0].(*aguievents.RunStartedEvent)
	assert.True(t, ok)

	snapshot, ok := collected[1].(*aguievents.MessagesSnapshotEvent)
	assert.True(t, ok)
	assert.Len(t, snapshot.Messages, 4)
	assert.Equal(t, "hello", *snapshot.Messages[0].Content)
	assert.Equal(t, "system", *snapshot.Messages[1].Content)
	assert.Equal(t, "calc", snapshot.Messages[2].ToolCalls[0].Function.Name)
	assert.Equal(t, "tool-call-1", *snapshot.Messages[3].ToolCallID)

	_, ok = collected[2].(*aguievents.RunFinishedEvent)
	assert.True(t, ok)
}

func TestMessagesSnapshotUnknownRole(t *testing.T) {
	events := []event.Event{
		newResponse(model.RoleUser, "hello", nil),
		newResponse(model.Role("unknown"), "?", nil),
	}

	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		sessionService:    &testSessionService{events: events},
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.NoError(t, err)

	collected := collectAGUIEvents(t, stream)
	assert.Len(t, collected, 2)
	_, ok := collected[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
}

func collectAGUIEvents(t *testing.T, ch <-chan aguievents.Event) []aguievents.Event {
	t.Helper()
	var events []aguievents.Event
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

func TestMessagesSnapshotRunnerNil(t *testing.T) {
	r := &runner{
		runner: nil,
	}
	ch, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

func TestMessagesSnapshotInputNil(t *testing.T) {
	r := &runner{
		runner: noopBaseRunner{},
	}
	ch, err := r.MessagesSnapshot(context.Background(), nil)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

func TestMessagesSnapshotUserIDResolverError(t *testing.T) {
	userIDResolver := func(context.Context, *adapter.RunAgentInput) (string, error) { return "", errors.New("boom") }
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    userIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		sessionService:    &testSessionService{events: []event.Event{newResponse(model.RoleUser, "hello", nil)}},
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.NoError(t, err)
	collected := collectAGUIEvents(t, stream)
	assert.Len(t, collected, 2)
	_, ok := collected[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
}

func TestMessagesSnapshotEmptyEvents(t *testing.T) {
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		sessionService:    &testSessionService{},
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.NoError(t, err)
	collected := collectAGUIEvents(t, stream)
	assert.Len(t, collected, 3)
	snapshot, ok := collected[1].(*aguievents.MessagesSnapshotEvent)
	assert.True(t, ok)
	assert.Len(t, snapshot.Messages, 0)
}

func TestMessagesSnapshotGetSessionError(t *testing.T) {
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: NewOptions().RunAgentInputHook,
		appName:           "demo",
		sessionService:    &testSessionService{getErr: errors.New("get session error")},
	}

	stream, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.NoError(t, err)
	collected := collectAGUIEvents(t, stream)
	assert.Len(t, collected, 2)
	_, ok := collected[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
}

// TestMessagesSnapshotRunAgentInputHookError verifies MessagesSnapshot returns error when input hook fails.
func TestMessagesSnapshotRunAgentInputHookError(t *testing.T) {
	runAgentInputHook := func(context.Context, *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
		return nil, errors.New("hook failure")
	}
	r := &runner{
		runner:            noopBaseRunner{},
		userIDResolver:    NewOptions().UserIDResolver,
		runAgentInputHook: runAgentInputHook,
		appName:           "demo",
		sessionService:    &testSessionService{},
	}

	ch, err := r.MessagesSnapshot(
		context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"},
	)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

type noopBaseRunner struct{}

func (noopBaseRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message,
	_ ...agent.RunOption) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

// TestGetSessionEventsNilSession verifies nil session is handled gracefully.
func TestGetSessionEventsNilSession(t *testing.T) {
	r := &runner{
		sessionService: &testSessionService{returnNil: true},
	}
	events, err := r.getSessionEvents(context.Background(), session.Key{})
	assert.NoError(t, err)
	assert.Nil(t, events)
}

// TestConvertToMessagesSnapshotEventSkipsNilResponse ensures nil response events are ignored.
func TestConvertToMessagesSnapshotEventSkipsNilResponse(t *testing.T) {
	r := &runner{}
	snapshot, err := r.convertToMessagesSnapshotEvent(context.Background(), "user-id", []event.Event{{}})
	assert.NoError(t, err)
	assert.NotNil(t, snapshot)
	assert.Len(t, snapshot.Messages, 0)
}

func TestConvertToMessagesSnapshotEventIncludesUserIDName(t *testing.T) {
	r := &runner{}
	snapshot, err := r.convertToMessagesSnapshotEvent(context.Background(),
		"user-id", []event.Event{newResponse(model.RoleUser, "hello", nil)})
	assert.NoError(t, err)
	assert.NotNil(t, snapshot)
	assert.Len(t, snapshot.Messages, 1)
	assert.NotNil(t, snapshot.Messages[0].Name)
	assert.Equal(t, "user-id", *snapshot.Messages[0].Name)
}

// TestConvertToMessagesSnapshotEventBeforeCallbackMutates ensures before callbacks update messages.
func TestConvertToMessagesSnapshotEventBeforeCallbackMutates(t *testing.T) {
	callbacks := translator.NewCallbacks().
		RegisterBeforeTranslate(func(ctx context.Context, evt *event.Event) (*event.Event, error) {
			if evt.Response != nil && len(evt.Response.Choices) > 0 {
				evt.Response.Choices[0].Message.Content = "patched"
			}
			return evt, nil
		})
	r := &runner{
		translateCallbacks: callbacks,
	}
	snapshot, err := r.convertToMessagesSnapshotEvent(context.Background(),
		"user-id", []event.Event{newResponse(model.RoleUser, "hello", nil)})
	assert.NoError(t, err)
	assert.NotNil(t, snapshot)
	assert.Len(t, snapshot.Messages, 1)
	assert.Equal(t, "patched", *snapshot.Messages[0].Content)
}

func TestConvertToMessagesSnapshotEventDeduplicatesUserMessages(t *testing.T) {
	r := &runner{}
	sharedRequestID := "req-shared"
	events := []event.Event{
		newResponseWithRequestID(model.RoleUser, "hello", sharedRequestID, nil),
		newResponseWithRequestID(model.RoleUser, "hello again", sharedRequestID, nil),
		newResponseWithRequestID(model.RoleUser, "next", "req-next", nil),
	}
	snapshot, err := r.convertToMessagesSnapshotEvent(context.Background(), "user-id", events)
	assert.NoError(t, err)
	assert.NotNil(t, snapshot)
	assert.Len(t, snapshot.Messages, 2)
	assert.Equal(t, "hello", *snapshot.Messages[0].Content)
	assert.Equal(t, "next", *snapshot.Messages[1].Content)
}

func TestIgnoreEvent(t *testing.T) {
	tests := []struct {
		name string
		evt  *event.Event
		want bool
	}{
		{
			name: "nil event",
			evt:  nil,
			want: true,
		},
		{
			name: "nil response",
			evt:  &event.Event{Response: nil},
			want: true,
		},
		{
			name: "nil choices",
			evt:  &event.Event{Response: &model.Response{Choices: nil}},
			want: true,
		},
		{
			name: model.ObjectTypeChatCompletion,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypeChatCompletion,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}},
			}},
			want: false,
		},
		{
			name: model.ObjectTypeToolResponse,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypeToolResponse,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleTool, Content: "hello"}}},
			}},
			want: false,
		},
		{
			name: "",
			evt: &event.Event{Response: &model.Response{
				Object:  "",
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}},
			}},
			want: false,
		},
		{
			name: model.ObjectTypeError,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypeError,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypePreprocessingBasic,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypePreprocessingBasic,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypePreprocessingContent,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypePreprocessingContent,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypePreprocessingIdentity,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypePreprocessingIdentity,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypePreprocessingInstruction,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypePreprocessingInstruction,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypePreprocessingPlanning,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypePreprocessingPlanning,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypePostprocessingPlanning,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypePostprocessingPlanning,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypePostprocessingCodeExecution,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypePostprocessingCodeExecution,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypeTransfer,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypeTransfer,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypeRunnerCompletion,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypeRunnerCompletion,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypeStateUpdate,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypeStateUpdate,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
		{
			name: model.ObjectTypeChatCompletionChunk,
			evt: &event.Event{Response: &model.Response{
				Object:  model.ObjectTypeChatCompletionChunk,
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
			}},
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &runner{}
			got := r.ignoreEvent(test.evt)
			assert.Equal(t, test.want, got)
		})
	}
}

// TestConvertToMessagesSnapshotEventBeforeCallbackError ensures errors bubble up.
func TestConvertToMessagesSnapshotEventBeforeCallbackError(t *testing.T) {
	callbacks := translator.NewCallbacks().
		RegisterBeforeTranslate(func(ctx context.Context, evt *event.Event) (*event.Event, error) {
			return nil, errors.New("fail")
		})
	r := &runner{
		translateCallbacks: callbacks,
	}
	snapshot, err := r.convertToMessagesSnapshotEvent(context.Background(),
		"user-id", []event.Event{newResponse(model.RoleUser, "hello", nil)})
	assert.Nil(t, snapshot)
	assert.Error(t, err)
}

func newResponse(role model.Role, content string, mutate func(*model.Message)) event.Event {
	msg := model.Message{Role: role, Content: content}
	if mutate != nil {
		mutate(&msg)
	}
	resp := &model.Response{
		ID:      "id-" + string(role) + content,
		Choices: []model.Choice{{Message: msg}},
	}
	evt := event.NewResponseEvent("invocation", string(role), resp)
	evt.RequestID = uuid.NewString()
	return *evt
}

func newResponseWithRequestID(role model.Role, content, requestID string,
	mutate func(*model.Message)) event.Event {
	evt := newResponse(role, content, mutate)
	evt.RequestID = requestID
	return evt
}

type testSessionService struct {
	events    []event.Event
	getErr    error
	returnNil bool
}

func (s *testSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap,
	opts ...session.Option) (*session.Session, error) {
	return nil, nil
}

func (s *testSessionService) GetSession(ctx context.Context, key session.Key,
	opts ...session.Option) (*session.Session, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.returnNil {
		return nil, nil
	}
	sess := &session.Session{AppName: key.AppName, UserID: key.UserID, ID: key.SessionID}
	if len(s.events) > 0 {
		sess.Events = append([]event.Event(nil), s.events...)
	}
	return sess, nil
}

func (s *testSessionService) ListSessions(ctx context.Context, key session.UserKey,
	opts ...session.Option) ([]*session.Session, error) {
	return nil, nil
}

func (s *testSessionService) DeleteSession(ctx context.Context, key session.Key, opts ...session.Option) error {
	return nil
}

func (s *testSessionService) UpdateAppState(ctx context.Context, app string, state session.StateMap) error {
	return nil
}

func (s *testSessionService) DeleteAppState(ctx context.Context, app string, key string) error {
	return nil
}

func (s *testSessionService) ListAppStates(ctx context.Context, app string) (session.StateMap, error) {
	return nil, nil
}

func (s *testSessionService) UpdateUserState(ctx context.Context, key session.UserKey, state session.StateMap) error {
	return nil
}

func (s *testSessionService) ListUserStates(ctx context.Context, key session.UserKey) (session.StateMap, error) {
	return nil, nil
}

func (s *testSessionService) DeleteUserState(ctx context.Context, key session.UserKey, stateKey string) error {
	return nil
}

func (s *testSessionService) AppendEvent(ctx context.Context, sess *session.Session, evt *event.Event,
	opts ...session.Option) error {
	return nil
}

func (s *testSessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, summary string,
	force bool) error {
	return nil
}

func (s *testSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, summary string,
	force bool) error {
	return nil
}

func (s *testSessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session) (string, bool) {
	return "", false
}

func (s *testSessionService) Close() error { return nil }
