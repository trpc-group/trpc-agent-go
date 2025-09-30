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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRunner_EnqueueSummaryJob_Calls(t *testing.T) {
	t.Run("calls EnqueueSummaryJob for qualifying events", func(t *testing.T) {
		// Create mock session service
		mockSessionService := &mockSessionService{}

		// Create a mock agent that generates qualifying events
		mockAgent := &mockAgent{name: "test-agent"}

		// Create runner with mock session service
		runner := NewRunner("test-app", mockAgent, WithSessionService(mockSessionService))

		ctx := context.Background()
		userID := "test-user"
		sessionID := "test-session"

		// Run the runner with qualifying event
		_, err := RunWithMessages(ctx, runner, userID, sessionID, []model.Message{
			{Role: model.RoleUser, Content: "Hello"},
		})
		require.NoError(t, err)

		// Wait a bit for async processing
		time.Sleep(100 * time.Millisecond)

		// Verify EnqueueSummaryJob was called once; worker cascades full-session internally.
		require.Len(t, mockSessionService.enqueueSummaryJobCalls, 1, "Should call EnqueueSummaryJob once")

		// Check the only call
		onlyCall := mockSessionService.enqueueSummaryJobCalls[0]
		assert.Equal(t, "", onlyCall.filterKey, "Call should use default empty FilterKey from mock agent")
		assert.False(t, onlyCall.force, "Call should not force")
		assert.NotNil(t, onlyCall.sess, "Call should have session")
	})

	t.Run("does not call EnqueueSummaryJob for non-qualifying events", func(t *testing.T) {
		// Create mock session service
		mockSessionService := &mockSessionService{}

		// Create a mock agent that generates non-qualifying events
		nonQualifyingAgent := &nonQualifyingMockAgent{name: "non-qualifying-agent"}

		// Create runner with mock session service
		runner := NewRunner("test-app", nonQualifyingAgent, WithSessionService(mockSessionService))

		ctx := context.Background()
		userID := "test-user"
		sessionID := "test-session"

		// Run the runner with non-qualifying event
		_, err := RunWithMessages(ctx, runner, userID, sessionID, []model.Message{
			{Role: model.RoleUser, Content: "Hello"},
		})
		require.NoError(t, err)

		// Wait a bit for async processing
		time.Sleep(100 * time.Millisecond)

		// Verify EnqueueSummaryJob was not called
		assert.Len(t, mockSessionService.enqueueSummaryJobCalls, 0, "Should not call EnqueueSummaryJob for non-qualifying events")
	})

	t.Run("calls EnqueueSummaryJob for events with state delta", func(t *testing.T) {
		// Create mock session service
		mockSessionService := &mockSessionService{}

		// Create a mock agent that generates events with state delta
		stateDeltaAgent := &stateDeltaMockAgent{name: "state-delta-agent"}

		// Create runner with mock session service
		runner := NewRunner("test-app", stateDeltaAgent, WithSessionService(mockSessionService))

		ctx := context.Background()
		userID := "test-user"
		sessionID := "test-session"

		// Run the runner with state delta event
		_, err := RunWithMessages(ctx, runner, userID, sessionID, []model.Message{
			{Role: model.RoleUser, Content: "Hello"},
		})
		require.NoError(t, err)

		// Wait a bit for async processing
		time.Sleep(100 * time.Millisecond)

		// Verify EnqueueSummaryJob was called once; full-session will be cascaded by worker.
		require.Len(t, mockSessionService.enqueueSummaryJobCalls, 1, "Should call EnqueueSummaryJob once for state delta events")
		onlyCall := mockSessionService.enqueueSummaryJobCalls[0]
		assert.Equal(t, "test-filter", onlyCall.filterKey, "Call should use event's FilterKey")
		assert.False(t, onlyCall.force, "Call should not force")
	})

	t.Run("handles EnqueueSummaryJob errors gracefully", func(t *testing.T) {
		// Create mock session service that returns error for EnqueueSummaryJob
		errorSessionService := &errorMockSessionService{
			mockSessionService: &mockSessionService{},
		}

		// Create a mock agent
		mockAgent := &mockAgent{name: "test-agent"}

		// Create runner with error session service
		runner := NewRunner("test-app", mockAgent, WithSessionService(errorSessionService))

		ctx := context.Background()
		userID := "test-user"
		sessionID := "test-session"

		// Run the runner - should not fail even if EnqueueSummaryJob returns error
		_, err := RunWithMessages(ctx, runner, userID, sessionID, []model.Message{
			{Role: model.RoleUser, Content: "Hello"},
		})
		require.NoError(t, err, "Runner should not fail when EnqueueSummaryJob returns error")

		// Wait a bit for async processing
		time.Sleep(100 * time.Millisecond)

		// Verify EnqueueSummaryJob was still called once despite error
		assert.Len(t, errorSessionService.enqueueSummaryJobCalls, 1, "Should still call EnqueueSummaryJob once even when it returns error")
	})
}

// nonQualifyingMockAgent generates non-qualifying events (partial responses).
type nonQualifyingMockAgent struct {
	name string
}

func (m *nonQualifyingMockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing non-qualifying events",
	}
}

func (m *nonQualifyingMockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *nonQualifyingMockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *nonQualifyingMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 1)

	// Create a non-qualifying event (partial response)
	nonQualifyingEvent := &event.Event{
		Response: &model.Response{
			ID:        "test-response",
			Model:     "test-model",
			Done:      false, // Partial response
			IsPartial: true,  // This makes it non-qualifying
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "This is a partial response",
					},
				},
			},
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "test-event-id",
		Timestamp:    time.Now(),
		FilterKey:    "test-filter",
	}

	eventCh <- nonQualifyingEvent
	close(eventCh)

	return eventCh, nil
}

func (m *nonQualifyingMockAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

// stateDeltaMockAgent generates events with state delta.
type stateDeltaMockAgent struct {
	name string
}

func (m *stateDeltaMockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing state delta events",
	}
}

func (m *stateDeltaMockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *stateDeltaMockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *stateDeltaMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 1)

	// Create an event with state delta
	stateDeltaEvent := &event.Event{
		StateDelta: map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "test-event-id",
		Timestamp:    time.Now(),
		FilterKey:    "test-filter",
	}

	eventCh <- stateDeltaEvent
	close(eventCh)

	return eventCh, nil
}

func (m *stateDeltaMockAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

// mockSessionService implements the session.Service interface for testing EnqueueSummaryJob calls.
type mockSessionService struct {
	enqueueSummaryJobCalls []enqueueSummaryJobCall
	appendEventCalls       []appendEventCall
	createSessionCalls     []createSessionCall
	getSessionCalls        []getSessionCall
}

type enqueueSummaryJobCall struct {
	sess      *session.Session
	filterKey string
	force     bool
}

type appendEventCall struct {
	sess    *session.Session
	event   *event.Event
	options []session.Option
}

type createSessionCall struct {
	key     session.Key
	state   session.StateMap
	options []session.Option
}

type getSessionCall struct {
	key     session.Key
	options []session.Option
}

func (m *mockSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	m.createSessionCalls = append(m.createSessionCalls, createSessionCall{key, state, options})
	return &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		Events:    []event.Event{},
		Summaries: map[string]*session.Summary{},
	}, nil
}

func (m *mockSessionService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	m.getSessionCalls = append(m.getSessionCalls, getSessionCall{key, options})
	return &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		Events:    []event.Event{},
		Summaries: map[string]*session.Summary{},
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

func (m *mockSessionService) AppendEvent(ctx context.Context, session *session.Session, event *event.Event, options ...session.Option) error {
	m.appendEventCalls = append(m.appendEventCalls, appendEventCall{session, event, options})
	return nil
}

func (m *mockSessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}

func (m *mockSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	m.enqueueSummaryJobCalls = append(m.enqueueSummaryJobCalls, enqueueSummaryJobCall{sess, filterKey, force})
	return nil
}

func (m *mockSessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session) (string, bool) {
	return "", false
}

func (m *mockSessionService) Close() error {
	return nil
}

// errorMockSessionService wraps mockSessionService and returns errors for EnqueueSummaryJob.
type errorMockSessionService struct {
	*mockSessionService
}

func (m *errorMockSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	// Call parent to record the call
	m.mockSessionService.EnqueueSummaryJob(ctx, sess, filterKey, force)
	return assert.AnError // Return error
}
