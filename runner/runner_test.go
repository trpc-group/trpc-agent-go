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
	eventCh, err := runner.Run(ctx, userID, sessionID, message, agent.RunOptions{})
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

func TestRunner_SessionCreationWhenNotExists(t *testing.T) {
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
	eventCh, err := runner.Run(ctx, userID, sessionID, message, agent.RunOptions{})
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
	eventCh, err := runner.Run(ctx, userID, sessionID, emptyMessage, agent.RunOptions{})
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
