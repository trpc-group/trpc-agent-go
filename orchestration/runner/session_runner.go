package runner

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/memory"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
)

// SessionRunner extends BaseRunner with session management capabilities.
type SessionRunner struct {
	*BaseRunner
	sessionManager session.Manager
}

// NewSessionRunner creates a new session-aware runner.
func NewSessionRunner(name string, a agent.Agent, config Config, sessionManager session.Manager) *SessionRunner {
	baseRunner := NewBaseRunner(name, a, config)

	// If no session manager provided, create an in-memory one
	if sessionManager == nil {
		sessionManager = session.NewMemoryManager(
			session.WithExpiration(config.SessionOptions.Expiration),
		)
	}

	return &SessionRunner{
		BaseRunner:     baseRunner,
		sessionManager: sessionManager,
	}
}

// RunWithSession executes an agent with the given input in the context of a session.
func (r *SessionRunner) RunWithSession(ctx context.Context, sessionID string, input message.Message) (*message.Message, error) {
	r.mu.RLock()
	agent := r.agent
	active := r.active
	r.mu.RUnlock()

	if !active {
		return nil, fmt.Errorf("runner %s is not active", r.name)
	}

	if agent == nil {
		return nil, fmt.Errorf("%w: runner %s has no agent", ErrAgentNotFound, r.name)
	}

	// Get or create session
	sess, err := r.sessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Add input message to session
	if err := sess.AddMessage(ctx, &input); err != nil {
		return nil, fmt.Errorf("failed to add message to session: %w", err)
	}

	// Get session messages for context
	messages, err := sess.GetMessages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}

	// Create a context with history for the agent
	inputWithContext := input
	if inputWithContext.Metadata == nil {
		inputWithContext.Metadata = make(map[string]interface{})
	}
	inputWithContext.Metadata["history"] = messages
	inputWithContext.Metadata["session_id"] = sessionID

	// Create a session context that wraps the Go context
	sessCtx := session.NewContext(ctx, sessionID, messages)

	// Create a timeout context if specified in config
	var runCtx context.Context
	var cancel context.CancelFunc
	if r.config.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(sessCtx, r.config.Timeout)
		defer cancel()
	} else {
		runCtx = sessCtx
	}

	// Process metrics and timing
	startTime := time.Now()
	defer func() {
		log.Debugf("Agent run with session completed. runner: %s, agent: %s, session: %s, duration_ms: %d",
			r.name,
			agent.Name(),
			sessionID,
			time.Since(startTime).Milliseconds(),
		)
	}()

	// Run the agent
	log.Debugf("Running agent with session. runner: %s, agent: %s, session: %s",
		r.name,
		agent.Name(),
		sessionID,
	)

	// Run agent with the context-enhanced input
	response, err := agent.Run(runCtx, &inputWithContext)
	if err != nil {
		return nil, err
	}

	// Add the response to the session
	if err := sess.AddMessage(ctx, response); err != nil {
		log.Warnf("Failed to add response to session. error: %v, session: %s",
			err,
			sessionID,
		)
	}

	return response, nil
}

// RunAsyncWithSession executes an agent with the given input in a session and returns a stream of events.
func (r *SessionRunner) RunAsyncWithSession(ctx context.Context, sessionID string, input message.Message) (<-chan *event.Event, error) {
	r.mu.RLock()
	agent := r.agent
	active := r.active
	r.mu.RUnlock()

	if !active {
		return nil, fmt.Errorf("runner %s is not active", r.name)
	}

	if agent == nil {
		return nil, fmt.Errorf("%w: runner %s has no agent", ErrAgentNotFound, r.name)
	}

	// Get or create session
	sess, err := r.sessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Add input message to session
	if err := sess.AddMessage(ctx, &input); err != nil {
		return nil, fmt.Errorf("failed to add message to session: %w", err)
	}

	// Get all messages from the session
	messages, err := sess.GetMessages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}

	// Create a context with history for the agent
	inputWithContext := input
	if inputWithContext.Metadata == nil {
		inputWithContext.Metadata = make(map[string]interface{})
	}
	inputWithContext.Metadata["history"] = messages
	inputWithContext.Metadata["session_id"] = sessionID

	// Create a session context that wraps the Go context
	sessCtx := session.NewContext(ctx, sessionID, messages)

	// Create an event channel
	eventCh := make(chan *event.Event, r.config.BufferSize)

	// Create a timeout context if specified in config
	var runCtx context.Context
	var cancel context.CancelFunc
	if r.config.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(sessCtx, r.config.Timeout)
	} else {
		runCtx = sessCtx
		cancel = func() {}
	}

	// Process metrics and timing
	startTime := time.Now()
	log.Debugf("Running agent asynchronously with session. runner: %s, agent: %s, session: %s, streaming: true",
		r.name,
		agent.Name(),
		sessionID,
	)

	// Start async processing
	go func() {
		defer func() {
			cancel()
			close(eventCh)

			// Log completion
			duration := time.Since(startTime)
			log.Debugf("Async run with session completed. runner: %s, agent: %s, session: %s, duration_ms: %d",
				r.name,
				agent.Name(),
				sessionID,
				duration.Milliseconds(),
			)
		}()

		// Send start event
		eventCh <- event.NewStreamStartEvent(sessionID)

		// Use streaming approach (assume all models support streaming)
		agentEventCh, err := agent.RunAsync(runCtx, &inputWithContext)
		if err != nil {
			eventCh <- event.NewErrorEvent(err, 500)
			return
		}

		var finalContent string
		var msgObj *message.Message

		for evt := range agentEventCh {
			// Forward all events from the agent
			eventCh <- evt

			// Handle different event types
			switch evt.Type {
			case event.TypeMessage:
				// If it's a message event, capture the message
				if msg, ok := evt.GetMetadata("message"); ok {
					if msgPtr, ok := msg.(*message.Message); ok {
						msgObj = msgPtr
						finalContent = msgPtr.Content
					}
				}
			case event.TypeStream:
				// If it's a stream event, just accumulate content (no need to create a new event)
				if content, ok := evt.GetMetadata("content"); ok {
					if contentStr, ok := content.(string); ok {
						finalContent += contentStr
					}
				}
			case event.TypeStreamChunk:
				// Stream chunk already has the right format, just update final content
				if content, ok := evt.GetMetadata("content"); ok {
					if contentStr, ok := content.(string); ok {
						finalContent += contentStr
					}
				}
			}
		}

		// Save the final response to the session
		if msgObj != nil {
			// We have a complete message object
			if err := sess.AddMessage(ctx, msgObj); err != nil {
				log.Warnf("Failed to add response to session. error: %v, session: %s",
					err,
					sessionID,
				)
			}
		} else if finalContent != "" {
			// We only have accumulated content, create a message
			respMsg := message.NewAssistantMessage(finalContent)
			if err := sess.AddMessage(ctx, respMsg); err != nil {
				log.Warnf("Failed to add response to session. error: %v, session: %s",
					err,
					sessionID,
				)
			}
		}

		// Send end event with the final content
		eventCh <- event.NewStreamEndEvent(finalContent)
	}()

	return eventCh, nil
}

// CreateSession creates a new session.
func (r *SessionRunner) CreateSession(ctx context.Context) (string, error) {
	sess, err := r.sessionManager.Create(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	return sess.ID(), nil
}

// GetSession retrieves a session by ID.
func (r *SessionRunner) GetSession(ctx context.Context, sessionID string) (memory.Session, error) {
	sess, err := r.sessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return sess, nil
}

// DeleteSession deletes a session.
func (r *SessionRunner) DeleteSession(ctx context.Context, sessionID string) error {
	return r.sessionManager.Delete(ctx, sessionID)
}

// ListSessions lists all session IDs.
func (r *SessionRunner) ListSessions(ctx context.Context) ([]string, error) {
	return r.sessionManager.ListIDs(ctx)
}
