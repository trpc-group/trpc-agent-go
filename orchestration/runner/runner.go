// Package runner provides the core runner functionality.
package runner

import (
	"context"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session/inmemory"
)

// Author types for events.
const (
	authorUser = "user"
)

// Option is a function that configures a Runner.
type Option func(*Options)

// WithSessionService sets the session service to use.
func WithSessionService(service session.Service) Option {
	return func(opts *Options) {
		opts.sessionService = service
	}
}

// Runner runs agents.
type Runner struct {
	appName        string
	agent          agent.Agent
	sessionService session.Service
}

// Options is the options for the Runner.
type Options struct {
	sessionService session.Service
}

// New creates a new Runner.
func New(appName string, agent agent.Agent, opts ...Option) *Runner {
	var options Options

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	if options.sessionService == nil {
		options.sessionService = inmemory.NewSessionService()
	}
	return &Runner{
		appName:        appName,
		agent:          agent,
		sessionService: options.sessionService,
	}
}

// Run runs the agent.
func (r *Runner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts agent.RunOptions,
) (<-chan *event.Event, error) {
	sessionKey := session.Key{
		AppName:   r.appName,
		UserID:    userID,
		SessionID: sessionID,
	}

	// Get session or create if it doesn't exist.
	sess, err := r.sessionService.GetSession(ctx, sessionKey, &session.Options{})
	if err != nil {
		return nil, err
	}
	if sess == nil {
		if sess, err = r.sessionService.CreateSession(
			ctx, sessionKey, session.StateMap{},
			&session.Options{},
		); err != nil {
			return nil, err
		}
	}

	// Generate invocation ID.
	invocationID := "invocation-" + uuid.New().String()

	// Append the incoming user message to the session if it has content.
	if message.Content != "" {
		userEvent := &event.Event{
			Response:     &model.Response{Done: false},
			InvocationID: invocationID,
			Author:       authorUser,
			ID:           uuid.New().String(),
			Timestamp:    time.Now(),
		}
		// Set the user message content in the response.
		userEvent.Response.Choices = []model.Choice{
			{
				Index:   0,
				Message: message,
			},
		}

		if err := r.sessionService.AppendEvent(
			ctx, sess, userEvent, &session.Options{},
		); err != nil {
			return nil, err
		}
	}

	// Create invocation.
	eventCompletionCh := make(chan string)
	invocation := &agent.Invocation{
		Agent:             r.agent,
		Session:           sess,
		InvocationID:      invocationID,
		EndInvocation:     false,
		Message:           message,
		RunOptions:        opts,
		EventCompletionCh: eventCompletionCh,
	}

	// Run the agent and get the event channel.
	agentEventCh, err := r.agent.Run(ctx, invocation)
	if err != nil {
		return nil, err
	}

	// Create a new channel for processed events.
	processedEventCh := make(chan *event.Event)

	// Start a goroutine to process and append events to session.
	go func() {
		defer close(processedEventCh)

		for agentEvent := range agentEventCh {
			// Append event to session if it's complete (not partial).
			if agentEvent.Response != nil && !agentEvent.Response.IsPartial {
				if err := r.sessionService.AppendEvent(
					ctx, sess, agentEvent, &session.Options{},
				); err != nil {
					log.Errorf("Failed to append event to session: %v", err)
				}
			}

			if agentEvent.RequiresCompletion {
				eventCompletionCh <- agentEvent.CompletionID
			}

			// Forward the event to the output channel.
			select {
			case processedEventCh <- agentEvent:
			case <-ctx.Done():
				return
			}
		}
	}()

	return processedEventCh, nil
}
