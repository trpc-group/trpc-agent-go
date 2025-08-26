//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent implements the agent.Agent interface for testing.
type mockAgent struct {
	name string
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing",
	}
}

// SubAgents implements the agent.Agent interface for testing.
func (m *mockAgent) SubAgents() []agent.Agent {
	return nil
}

// FindSubAgent implements the agent.Agent interface for testing.
func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 1)

	// Create a mock response event.
	responseEvent := &event.Event{
		Response: &model.Response{
			ID:    "test-response",
			Model: "test-model",
			Done:  true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Hello! I received your message: " + invocation.Message.Content,
					},
				},
			},
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "test-event-id",
		Timestamp:    time.Now(),
	}

	eventCh <- responseEvent
	close(eventCh)

	return eventCh, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

// spySessionService wraps a real session.Service and records whether
// CreateSessionSummary was called.
type spySessionService struct {
	base   session.Service
	called bool
}

func (s *spySessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	return s.base.CreateSession(ctx, key, state, options...)
}
func (s *spySessionService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return s.base.GetSession(ctx, key, options...)
}
func (s *spySessionService) ListSessions(ctx context.Context, userKey session.UserKey, options ...session.Option) ([]*session.Session, error) {
	return s.base.ListSessions(ctx, userKey, options...)
}
func (s *spySessionService) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	return s.base.DeleteSession(ctx, key, options...)
}
func (s *spySessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return s.base.UpdateAppState(ctx, appName, state)
}
func (s *spySessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return s.base.DeleteAppState(ctx, appName, key)
}
func (s *spySessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	return s.base.ListAppStates(ctx, appName)
}
func (s *spySessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return s.base.UpdateUserState(ctx, userKey, state)
}
func (s *spySessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	return s.base.ListUserStates(ctx, userKey)
}
func (s *spySessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return s.base.DeleteUserState(ctx, userKey, key)
}
func (s *spySessionService) AppendEvent(ctx context.Context, session *session.Session, ev *event.Event, options ...session.Option) error {
	return s.base.AppendEvent(ctx, session, ev, options...)
}
func (s *spySessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, force bool) error {
	s.called = true
	return s.base.CreateSessionSummary(ctx, sess, force)
}
func (s *spySessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session) (string, bool) {
	return s.base.GetSessionSummaryText(ctx, sess)
}
func (s *spySessionService) Close() error { return s.base.Close() }

func TestRunner_TriggersCreateSessionSummary(t *testing.T) {
	// Use inmemory service with no summarizer manager configured.
	// The CreateSessionSummary will early-return nil, which is fine for this test.
	base := inmemory.NewSessionService()
	spy := &spySessionService{base: base}

	mockAgent := &mockAgent{name: "test-agent"}
	r := NewRunner("test-app", mockAgent, WithSessionService(spy))

	ctx := context.Background()
	msg := model.NewUserMessage("hello")
	ch, err := r.Run(ctx, "u", "s", msg)
	require.NoError(t, err)

	// Drain events to let the runner finish the turn and trigger summarization.
	for range ch {
	}

	// Wait briefly for the async summarization trigger.
	deadline := time.Now().Add(200 * time.Millisecond)
	for !spy.called && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	assert.True(t, spy.called, "CreateSessionSummary should be called asynchronously.")
}

func TestRunner_SessionIntegration(t *testing.T) {
	// Create an in-memory session service.
	sessionService := inmemory.NewSessionService()

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner with session service.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	message := model.NewUserMessage("Hello, world!")

	// Run the agent.
	eventCh, err := runner.Run(ctx, userID, sessionID, message)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Collect all events.
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Verify we received the mock response.
	require.Len(t, events, 2)
	assert.Equal(t, "test-agent", events[0].Author)
	assert.Contains(t, events[0].Response.Choices[0].Message.Content, "Hello, world!")

	// Verify session was created and contains events.
	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := sessionService.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Verify session contains both user message and agent response.
	// Should have: user message + agent response + runner done = 3 events.
	assert.Len(t, sess.Events, 3)

	// Verify user event.
	userEvent := sess.Events[0]
	assert.Equal(t, authorUser, userEvent.Author)
	assert.Equal(t, "Hello, world!", userEvent.Response.Choices[0].Message.Content)

	// Verify agent event.
	agentEvent := sess.Events[1]
	assert.Equal(t, "test-agent", agentEvent.Author)
	assert.Contains(t, agentEvent.Response.Choices[0].Message.Content, "Hello, world!")
}

func TestRunner_SessionCreateIfMissing(t *testing.T) {
	// Create an in-memory session service.
	sessionService := inmemory.NewSessionService()

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "new-user"
	sessionID := "new-session"
	message := model.NewUserMessage("First message")

	// Run the agent (should create new session).
	eventCh, err := runner.Run(ctx, userID, sessionID, message)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Consume events.
	for range eventCh {
		// Just consume all events.
	}

	// Verify session was created.
	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := sessionService.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, sessionID, sess.ID)
	assert.Equal(t, userID, sess.UserID)
	assert.Equal(t, "test-app", sess.AppName)
}

func TestRunner_EmptyMessageHandling(t *testing.T) {
	// Create an in-memory session service.
	sessionService := inmemory.NewSessionService()

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	emptyMessage := model.Message{} // Empty message

	// Run the agent with empty message.
	eventCh, err := runner.Run(ctx, userID, sessionID, emptyMessage)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Consume events.
	for range eventCh {
		// Just consume all events.
	}

	// Verify session was created but only contains agent response (no user message).
	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := sessionService.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Should only have agent response, no user message since it was empty.
	assert.Len(t, sess.Events, 2)
	assert.Equal(t, "test-agent", sess.Events[0].Author)
}
