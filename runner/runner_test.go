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
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
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

func TestRunner_SessionIntegration(t *testing.T) {
	// Create an in-memory session service.
	sessionService := sessioninmemory.NewSessionService()

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
	assert.Len(t, sess.Events, 2)

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
	sessionService := sessioninmemory.NewSessionService()

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
	sessionService := sessioninmemory.NewSessionService()

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	emptyMessage := model.NewUserMessage("") // Empty message

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

	// Should have no events, user message was empty and not added to session, and session service filtered event start with user.
	assert.Len(t, sess.Events, 0)
}

func TestRunner_SkipAppendingSeedUserMessage(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	mockAgent := &mockAgent{name: "test-agent"}
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "seed-user"
	sessionID := "seed-session"
	seedHistory := []model.Message{
		model.NewSystemMessage("sys"),
		model.NewAssistantMessage("prev reply"),
		model.NewUserMessage("hello"),
	}

	message := model.NewUserMessage("hello")

	eventCh, err := runner.Run(ctx, userID, sessionID, message, agent.WithMessages(seedHistory))
	require.NoError(t, err)

	for range eventCh {
		// drain channel
	}

	sess, err := sessionService.GetSession(ctx, session.Key{AppName: "test-app", UserID: userID, SessionID: sessionID})
	require.NoError(t, err)
	require.NotNil(t, sess)
	// Expect: due to EnsureEventStartWithUser filtering, only the first user
	// event from seed is kept, plus agent response and runner completion = 3
	require.Len(t, sess.Events, 2)
	// Ensure we did not append a duplicate user message beyond the seed.
	userCount := 0
	for _, e := range sess.Events {
		if e.Author == authorUser {
			userCount++
		}
	}
	require.Equal(t, 1, userCount)
}

func TestRunner_AppendsDifferentUserAfterSeed(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	mockAgent := &mockAgent{name: "test-agent"}
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "seed-user2"
	sessionID := "seed-session2"
	seedHistory := []model.Message{
		model.NewSystemMessage("sys"),
		model.NewAssistantMessage("prev reply"),
		model.NewUserMessage("hello"),
	}

	// Different latest user, should be appended in addition to seeded user.
	message := model.NewUserMessage("hello too")

	eventCh, err := runner.Run(ctx, userID, sessionID, message, agent.WithMessages(seedHistory))
	require.NoError(t, err)

	for range eventCh {
		// drain channel
	}

	sess, err := sessionService.GetSession(ctx, session.Key{AppName: "test-app", UserID: userID, SessionID: sessionID})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Expect: seeded first user retained + appended user + agent response + runner completion = 4
	require.Len(t, sess.Events, 3)

	// Verify the first two events are users with expected contents.
	if !(len(sess.Events) >= 2) {
		t.Fatalf("expected at least two events")
	}
	// Event 0: seeded user
	if sess.Events[0].Author != authorUser {
		t.Fatalf("expected first event author user, got %s", sess.Events[0].Author)
	}
	if got := sess.Events[0].Response.Choices[0].Message.Content; got != "hello" {
		t.Fatalf("expected seeded user content 'hello', got %q", got)
	}
	// Event 1: appended user
	if sess.Events[1].Author != authorUser {
		t.Fatalf("expected second event author user, got %s", sess.Events[1].Author)
	}
	if got := sess.Events[1].Response.Choices[0].Message.Content; got != "hello too" {
		t.Fatalf("expected appended user content 'hello too', got %q", got)
	}
}

// TestRunner_InvocationInjection verifies that runner correctly injects invocation into context.
func TestRunner_InvocationInjection(t *testing.T) {
	// Create an in-memory session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a simple mock agent that verifies invocation is in context.
	mockAgent := &invocationVerificationAgent{name: "test-agent"}

	// Create runner.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	message := model.NewUserMessage("Test invocation injection")

	// Run the agent.
	eventCh, err := runner.Run(ctx, userID, sessionID, message)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Collect all events.
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Verify we received the success response indicating invocation was found in context.
	require.Len(t, events, 2)

	// First event should be from the mock agent.
	agentEvent := events[0]
	assert.Equal(t, "test-agent", agentEvent.Author)
	assert.Equal(t, "invocation-verification-success", agentEvent.Response.ID)
	assert.True(t, agentEvent.Response.Done)

	// Verify the response content indicates success.
	assert.Contains(t, agentEvent.Response.Choices[0].Message.Content, "Invocation found in context with ID:")
}

// invocationVerificationAgent is a simple mock agent that verifies invocation is present in context.
type invocationVerificationAgent struct {
	name string
}

func (m *invocationVerificationAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing invocation injection",
	}
}

func (m *invocationVerificationAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *invocationVerificationAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *invocationVerificationAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 1)

	// Verify that invocation is present in context.
	ctxInvocation, ok := agent.InvocationFromContext(ctx)
	if !ok || ctxInvocation == nil {
		// Create error event if invocation is not in context.
		errorEvent := &event.Event{
			Response: &model.Response{
				ID:    "invocation-verification-error",
				Model: "test-model",
				Done:  true,
				Error: &model.ResponseError{
					Type:    "invocation_verification_error",
					Message: "Invocation not found in context",
				},
			},
			InvocationID: invocation.InvocationID,
			Author:       m.name,
			ID:           "error-event-id",
			Timestamp:    time.Now(),
		}
		eventCh <- errorEvent
		close(eventCh)
		return eventCh, nil
	}

	// Create success response event.
	responseEvent := &event.Event{
		Response: &model.Response{
			ID:    "invocation-verification-success",
			Model: "test-model",
			Done:  true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Invocation found in context with ID: " + ctxInvocation.InvocationID,
					},
				},
			},
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "success-event-id",
		Timestamp:    time.Now(),
	}

	eventCh <- responseEvent
	close(eventCh)

	return eventCh, nil
}

func (m *invocationVerificationAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

func TestWithMemoryService(t *testing.T) {
	t.Run("sets memory service in options", func(t *testing.T) {
		memoryService := memoryinmemory.NewMemoryService()
		opts := &Options{}

		option := WithMemoryService(memoryService)
		option(opts)

		assert.Equal(t, memoryService, opts.memoryService, "Memory service should be set in options")
	})

	t.Run("sets nil memory service", func(t *testing.T) {
		opts := &Options{}

		option := WithMemoryService(nil)
		option(opts)

		assert.Nil(t, opts.memoryService, "Memory service should be nil")
	})
}

func TestWithArtifactService(t *testing.T) {
	t.Run("sets artifact service in options", func(t *testing.T) {
		artifactService := artifactinmemory.NewService()
		opts := &Options{}

		option := WithArtifactService(artifactService)
		option(opts)

		assert.Equal(t, artifactService, opts.artifactService, "Artifact service should be set in options")
	})

	t.Run("sets nil artifact service", func(t *testing.T) {
		opts := &Options{}

		option := WithArtifactService(nil)
		option(opts)

		assert.Nil(t, opts.artifactService, "Artifact service should be nil")
	})
}
