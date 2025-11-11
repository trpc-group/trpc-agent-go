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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
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

// TestRunner_GraphCompletionPropagation tests that graph completion events
// are properly captured and propagated to the runner completion event.
func TestRunner_GraphCompletionPropagation(t *testing.T) {
	// Create a mock agent that emits a graph completion event.
	graphAgent := &graphCompletionMockAgent{name: "graph-agent"}

	// Create runner with in-memory session service.
	sessionService := sessioninmemory.NewSessionService()
	runner := NewRunner("test-app", graphAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	message := model.NewUserMessage("Execute graph")

	// Run the agent.
	eventCh, err := runner.Run(ctx, userID, sessionID, message)
	require.NoError(t, err, "Run should not return an error")

	// Collect all events.
	var events []*event.Event
	for ev := range eventCh {
		events = append(events, ev)
	}

	// Verify we received events.
	require.NotEmpty(t, events, "Should receive events")

	// Find the runner completion event (should be the last one).
	var runnerCompletionEvent *event.Event
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Object == model.ObjectTypeRunnerCompletion {
			runnerCompletionEvent = events[i]
			break
		}
	}

	require.NotNil(t, runnerCompletionEvent, "Should have runner completion event")

	// Verify that the state delta was propagated.
	assert.NotNil(t, runnerCompletionEvent.StateDelta, "State delta should be propagated")
	assert.Equal(t, "final_value", string(runnerCompletionEvent.StateDelta["final_key"]),
		"State delta should contain the final key-value pair")

	// Verify that the final choices were propagated.
	assert.NotEmpty(t, runnerCompletionEvent.Response.Choices,
		"Final choices should be propagated")
	assert.Equal(t, "Graph execution completed",
		runnerCompletionEvent.Response.Choices[0].Message.Content,
		"Final message content should match")
}

// graphCompletionMockAgent emits a graph completion event with state delta
// and choices.
type graphCompletionMockAgent struct {
	name string
}

func (m *graphCompletionMockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent that emits graph completion events",
	}
}

func (m *graphCompletionMockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *graphCompletionMockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *graphCompletionMockAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 2)

	// Emit a graph completion event with state delta and choices.
	graphCompletionEvent := &event.Event{
		Response: &model.Response{
			ID:     "graph-completion",
			Object: "graph.execution",
			Done:   true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Graph execution completed",
					},
				},
			},
		},
		StateDelta: map[string][]byte{
			"final_key": []byte("final_value"),
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "graph-event-id",
		Timestamp:    time.Now(),
	}

	eventCh <- graphCompletionEvent
	close(eventCh)

	return eventCh, nil
}

func (m *graphCompletionMockAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

// failingAgent returns an error from Run to cover error path in Runner.Run.
type failingAgent struct{ name string }

func (m *failingAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *failingAgent) SubAgents() []agent.Agent             { return nil }
func (m *failingAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *failingAgent) Tools() []tool.Tool                   { return nil }
func (m *failingAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, errors.New("run failed")
}

// completionNoticeAgent emits an event that requires completion; it pre-adds
// a notice channel so Runner can notify it. The test asserts the channel closes.
type completionNoticeAgent struct {
	name     string
	noticeCh chan any
}

func (m *completionNoticeAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *completionNoticeAgent) SubAgents() []agent.Agent             { return nil }
func (m *completionNoticeAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *completionNoticeAgent) Tools() []tool.Tool                   { return nil }
func (m *completionNoticeAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	// Prepare an event that requires completion and pre-create the notice channel.
	id := "need-complete-1"
	m.noticeCh = inv.AddNoticeChannel(ctx, agent.GetAppendEventNoticeKey(id))
	ch <- &event.Event{
		Response:           &model.Response{ID: id, Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}},
		ID:                 id,
		RequiresCompletion: true,
	}
	close(ch)
	return ch, nil
}

// panicAppendSessionService panics when AppendEvent is called to exercise
// the recover path inside processAgentEvents.
type panicAppendSessionService struct{ session.Service }

func (s *panicAppendSessionService) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, _ ...session.Option) error {
	panic("append failed")
}

// appendErrorSessionService returns error on AppendEvent to cover the error
// branch and to ensure EnqueueSummaryJob is not called afterward.
type appendErrorSessionService struct{ *mockSessionService }

func (s *appendErrorSessionService) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, _ ...session.Option) error {
	s.mockSessionService.appendEventCalls = append(s.mockSessionService.appendEventCalls, appendEventCall{sess, e, nil})
	return errors.New("append error")
}

// getSessionErrorService returns error on GetSession to cover error path in getOrCreateSession.
type getSessionErrorService struct{ *mockSessionService }

func (s *getSessionErrorService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return nil, errors.New("get session error")
}

// closeErrorSessionService returns error on Close to test error handling.
type closeErrorSessionService struct {
	session.Service
	closeErr    error
	closeCalled int
}

func (s *closeErrorSessionService) Close() error {
	s.closeCalled++
	return s.closeErr
}

func (s *closeErrorSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	return &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		Events:    []event.Event{},
		Summaries: map[string]*session.Summary{},
	}, nil
}

func (s *closeErrorSessionService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return nil, nil
}

func (s *closeErrorSessionService) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, options ...session.Option) error {
	return nil
}

func (s *closeErrorSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}

// noOpAgent emits one qualifying assistant message then closes.
type noOpAgent struct{ name string }

func (m *noOpAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *noOpAgent) SubAgents() []agent.Agent             { return nil }
func (m *noOpAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *noOpAgent) Tools() []tool.Tool                   { return nil }
func (m *noOpAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("hi")}}}}
	close(ch)
	return ch, nil
}

// graphDoneAgent emits a final graph.execution event with customizable state delta and choices.
type graphDoneAgent struct {
	name        string
	delta       map[string][]byte
	withChoices bool
}

func (m *graphDoneAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *graphDoneAgent) SubAgents() []agent.Agent             { return nil }
func (m *graphDoneAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *graphDoneAgent) Tools() []tool.Tool                   { return nil }
func (m *graphDoneAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ev := &event.Event{
		Response:   &model.Response{ID: "graph-done", Object: graph.ObjectTypeGraphExecution, Done: true},
		StateDelta: m.delta,
	}
	if m.withChoices {
		ev.Response.Choices = []model.Choice{{Index: 0, Message: model.NewAssistantMessage("final")}}
	}
	ch <- ev
	close(ch)
	return ch, nil
}

func TestNewRunner_DefaultSessionService(t *testing.T) {
	// No WithSessionService option -> should default to inmemory session service.
	r := NewRunner("app", &noOpAgent{name: "a"})
	rr := r.(*runner)
	require.NotNil(t, rr.sessionService)
}

func TestRunner_Run_AgentRunError(t *testing.T) {
	r := NewRunner("app", &failingAgent{name: "f"})
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("m"))
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestGetOrCreateSession_Existing(t *testing.T) {
	// Pre-create a session; getOrCreateSession should return it without creating a new one.
	svc := sessioninmemory.NewSessionService()
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	_, err := svc.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	ch, err := r.Run(context.Background(), key.UserID, key.SessionID, model.NewUserMessage("hi"))
	require.NoError(t, err)
	for range ch {
	}
}

func TestGetOrCreateSession_GetError(t *testing.T) {
	// Service that fails GetSession should make Run return the error immediately.
	svc := &getSessionErrorService{mockSessionService: &mockSessionService{}}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("m"))
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestProcessAgentEvents_PanicRecovery(t *testing.T) {
	// Use mock service that panics on append to exercise recover in the goroutine.
	base := &mockSessionService{}
	svc := &panicAppendSessionService{Service: base}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))

	// Empty message to avoid initial user append; only agent event will be processed and panic.
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	// Consume until closed; should not hang due to recover.
	for range ch {
	}
}

func TestHandleEventPersistence_AppendErrorSkipsSummarize(t *testing.T) {
	base := &mockSessionService{}
	svc := &appendErrorSessionService{mockSessionService: base}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	// Empty message avoids initial user append which would error out early.
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	for range ch {
	}
	// Append failed -> EnqueueSummaryJob should not be called.
	require.Len(t, base.enqueueSummaryJobCalls, 0)
}

func TestEmitRunnerCompletion_AppendErrorStillEmits(t *testing.T) {
	base := &mockSessionService{}
	svc := &appendErrorSessionService{mockSessionService: base}
	// Emit a graph completion so emitRunnerCompletion propagates state/choices as well.
	ag := &graphDoneAgent{name: "g", delta: map[string][]byte{"k": []byte("v")}, withChoices: true}
	r := NewRunner("app", ag, WithSessionService(svc))
	// Empty message avoids initial append error; ensures we reach completion emission.
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	var last *event.Event
	for e := range ch {
		last = e
	}
	require.NotNil(t, last)
	require.True(t, last.Done)
	require.Equal(t, model.ObjectTypeRunnerCompletion, last.Object)
	// Even though append failed internally, the completion event is still emitted.
}

func TestPropagateGraphCompletion_NilStateValue(t *testing.T) {
	// Call propagateGraphCompletion directly to cover the nil-value copy branch.
	rr := NewRunner("app", &noOpAgent{name: "a"}).(*runner)
	ev := event.NewResponseEvent("inv", "app", &model.Response{ID: "rc", Object: model.ObjectTypeRunnerCompletion, Done: true})
	delta := map[string][]byte{"nil": nil}
	rr.propagateGraphCompletion(ev, delta, nil)
	require.Contains(t, ev.StateDelta, "nil")
	require.Nil(t, ev.StateDelta["nil"]) // explicit nil copy branch covered
}

func TestProcessAgentEvents_NotifyCompletion(t *testing.T) {
	// Verify that RequiresCompletion results in NotifyCompletion closing the notice channel.
	ag := &completionNoticeAgent{name: "c"}
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("go"))
	require.NoError(t, err)
	// Drain events to allow processing.
	for range ch {
	}
	// Wait for notice channel to close; a closed channel receives immediately.
	select {
	case <-ag.noticeCh:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive completion notice in time")
	}
}

// nilEventAgent emits a nil event to exercise the skip branch.
type nilEventAgent struct{ name string }

func (m *nilEventAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *nilEventAgent) SubAgents() []agent.Agent             { return nil }
func (m *nilEventAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *nilEventAgent) Tools() []tool.Tool                   { return nil }
func (m *nilEventAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- nil
	close(ch)
	return ch, nil
}

func TestProcessAgentEvents_NilEventSkipped(t *testing.T) {
	r := NewRunner("app", &nilEventAgent{name: "n"}, WithSessionService(sessioninmemory.NewSessionService()))
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	// Expect only the runner completion event to arrive.
	var count int
	for range ch {
		count++
	}
	require.Equal(t, 1, count)
}

func TestRunner_Run_AppendUserEventError(t *testing.T) {
	// Non-empty message with append-error service should cause Run to return error.
	svc := &appendErrorSessionService{mockSessionService: &mockSessionService{}}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("hello"))
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestRunner_Run_SeedAppendError(t *testing.T) {
	// Append error should be surfaced when seeding history into an empty session.
	svc := &appendErrorSessionService{mockSessionService: &mockSessionService{}}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	seed := []model.Message{model.NewUserMessage("seed")}
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""), agent.WithMessages(seed))
	require.Error(t, err)
	require.Nil(t, ch)
}

// oneEventAgent emits a single valid event; used to cover EmitEvent error path when context is cancelled.
type oneEventAgent struct{ name string }

func (m *oneEventAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *oneEventAgent) SubAgents() []agent.Agent             { return nil }
func (m *oneEventAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *oneEventAgent) Tools() []tool.Tool                   { return nil }
func (m *oneEventAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	// Unbuffered channel so EmitEvent will block unless receiver is ready
	ch := make(chan *event.Event)
	go func() {
		ch <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("x")}}}}
		close(ch)
	}()
	return ch, nil
}

func TestProcessAgentEvents_EmitEventContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before running; EmitEvent should take ctx.Done() branch
	r := NewRunner("app", &oneEventAgent{name: "o"}, WithSessionService(sessioninmemory.NewSessionService()))
	ch, err := r.Run(ctx, "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	// Should close without emitting any event due to emit error path returning early.
	var got int
	for range ch {
		got++
	}
	require.Equal(t, 0, got)
}

func TestProcessAgentEvents_EmitEventErrorBranch_Direct(t *testing.T) {
	// Call processAgentEvents directly to deterministically exercise the emit error branch.
	rr := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(sessioninmemory.NewSessionService())).(*runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inv := agent.NewInvocation()
	sess, _ := rr.sessionService.CreateSession(context.Background(), session.Key{AppName: "app", UserID: "u", SessionID: "s"}, session.StateMap{})

	agentCh := make(chan *event.Event)
	processed := rr.processAgentEvents(ctx, sess, inv, agentCh)
	// Send one event, then close agentCh
	go func() {
		agentCh <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("x")}}}}
		close(agentCh)
	}()

	// Do not read from processed until goroutine has had a chance to hit emit; then drain.
	time.Sleep(50 * time.Millisecond)
	var n int
	for range processed {
		n++
	}
	require.Equal(t, 0, n)
}

func TestShouldAppendUserMessage_Cases(t *testing.T) {
	// message role is not user -> should append
	require.True(t, shouldAppendUserMessage(model.NewAssistantMessage("a"), []model.Message{model.NewUserMessage("u")}))
	// seed has no user -> should append
	require.True(t, shouldAppendUserMessage(model.NewUserMessage("u"), []model.Message{model.NewSystemMessage("s"), model.NewAssistantMessage("a")}))
}

func TestRunner_Close_OwnedSessionService(t *testing.T) {
	// Create runner without providing session service.
	// Runner should create and own the default inmemory session service.
	mockAgent := &mockAgent{name: "test-agent"}
	r := NewRunner("test-app", mockAgent)

	// Close should succeed.
	err := r.Close()
	require.NoError(t, err)

	// Close should be idempotent (safe to call multiple times).
	err = r.Close()
	require.NoError(t, err)
}

func TestRunner_Close_ProvidedSessionService(t *testing.T) {
	// Create a session service that we control.
	sessionService := sessioninmemory.NewSessionService()
	defer sessionService.Close()

	// Create runner with provided session service.
	mockAgent := &mockAgent{name: "test-agent"}
	r := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	// Close the runner.
	err := r.Close()
	require.NoError(t, err)

	// The session service should still be usable because runner didn't
	// close it (it was provided by user).
	// This is a simple check - in practice, you'd verify the service is
	// still functional.
	assert.NotNil(t, sessionService)
}

func TestRunner_Close_Idempotent(t *testing.T) {
	// Test that calling Close multiple times is safe.
	mockAgent := &mockAgent{name: "test-agent"}
	r := NewRunner("test-app", mockAgent)

	// Call Close multiple times.
	for i := 0; i < 5; i++ {
		err := r.Close()
		require.NoError(t, err, "Close call %d should succeed", i+1)
	}
}

func TestRunner_Close_SessionServiceError(t *testing.T) {
	const closeErrMsg = "session service close error"

	// Create a mock session service that fails on Close.
	errorSessionService := &closeErrorSessionService{
		closeErr: errors.New(closeErrMsg),
	}

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner directly and manually set it to own the error session service.
	// This simulates the case where the runner created a session service that
	// fails on Close.
	r := &runner{
		appName:             "test-app",
		agent:               mockAgent,
		sessionService:      errorSessionService,
		ownedSessionService: true, // Mark as owned to trigger Close in runner.Close().
	}

	// Close should return the error from session service.
	err := r.Close()
	require.Error(t, err)
	assert.Contains(t, err.Error(), closeErrMsg)

	// Verify Close was called.
	assert.Equal(t, 1, errorSessionService.closeCalled)

	// Close should still be idempotent, even on error.
	// Second call should not return error because closeOnce protects it.
	err = r.Close()
	require.NoError(t, err)
	assert.Equal(t, 1, errorSessionService.closeCalled, "Close should only be called once")
}
