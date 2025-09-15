//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package runner provides the core runner functionality.
package runner

import (
	"context"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
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

// WithArtifactService sets the artifact service to use.
func WithArtifactService(service artifact.Service) Option {
	return func(opts *Options) {
		opts.artifactService = service
	}
}

// Runner is the interface for running agents.
type Runner interface {
	Run(
		ctx context.Context,
		userID string,
		sessionID string,
		message model.Message,
		runOpts ...agent.RunOption,
	) (<-chan *event.Event, error)
}

// runner runs agents.
type runner struct {
	appName         string
	agent           agent.Agent
	sessionService  session.Service
	artifactService artifact.Service
}

// Options is the options for the Runner.
type Options struct {
	sessionService  session.Service
	artifactService artifact.Service
}

// NewRunner creates a new Runner.
func NewRunner(appName string, agent agent.Agent, opts ...Option) Runner {
	var options Options

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	if options.sessionService == nil {
		options.sessionService = inmemory.NewSessionService()
	}
	return &runner{
		appName:         appName,
		agent:           agent,
		sessionService:  options.sessionService,
		artifactService: options.artifactService,
	}
}

// Run runs the agent.
func (r *runner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {

	sessionKey := session.Key{
		AppName:   r.appName,
		UserID:    userID,
		SessionID: sessionID,
	}

	// Get session or create if it doesn't exist.
	sess, err := r.sessionService.GetSession(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		if sess, err = r.sessionService.CreateSession(
			ctx, sessionKey, session.StateMap{},
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
			Branch:       "", // User events typically don't have branch constraints
		}
		// Set the user message content in the response.
		userEvent.Response.Choices = []model.Choice{
			{
				Index:   0,
				Message: message,
			},
		}

		if err := r.sessionService.AppendEvent(ctx, sess, userEvent); err != nil {
			return nil, err
		}
	}

	// Create invocation.
	var ro agent.RunOptions
	for _, opt := range runOpts {
		opt(&ro)
	}
	invocation := agent.NewInvocation(
		agent.WithInvocationID(invocationID),
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(message),
		agent.WithInvocationAgent(r.agent),
		agent.WithInvocationRunOptions(ro),
		agent.WithInvocationArtifactService(r.artifactService),
	)

	// Ensure the invocation can be accessed by downstream components (e.g., tools)
	// by embedding it into the context. This is necessary for tools like
	// transfer_to_agent that rely on agent.InvocationFromContext(ctx).
	ctx = agent.NewInvocationContext(ctx, invocation)

	ctx, span := trace.Tracer.Start(ctx, itelemetry.SpanNameInvocation)
	defer span.End()
	// Run the agent and get the event channel.
	agentEventCh, err := r.agent.Run(ctx, invocation)
	if err != nil {
		invocation.CleanupNotice(ctx)
		return nil, err
	}

	// Create a new channel for processed events.
	processedEventCh := make(chan *event.Event)
	// Start a goroutine to process and append events to session.
	go func() {
		defer func() {
			close(processedEventCh)
			invocation.CleanupNotice(ctx)
		}()

		for agentEvent := range agentEventCh {
			// Append event to session if it's complete (not partial).
			if agentEvent.StateDelta != nil ||
				(agentEvent.Response != nil && !agentEvent.Response.IsPartial && agentEvent.Response.Choices != nil) {
				if err := r.sessionService.AppendEvent(ctx, sess, agentEvent); err != nil {
					log.Errorf("Failed to append event to session: %v", err)
				}
			}

			if agentEvent.RequiresCompletion {
				completionID := agent.AppendEventNoticeKeyPrefix + agentEvent.ID
				invocation.NotifyCompletion(ctx, completionID)
			}

			// Forward the event to the output channel.
			select {
			case processedEventCh <- agentEvent:
			case <-ctx.Done():
				return
			}
		}

		// Emit final runner completion event after all agent events are processed.
		runnerCompletionEvent := &event.Event{
			Response: &model.Response{
				ID:        "runner-completion-" + uuid.New().String(),
				Object:    model.ObjectTypeRunnerCompletion,
				Created:   time.Now().Unix(),
				Done:      true,
				IsPartial: false,
			},
			InvocationID: invocationID,
			Author:       r.appName,
			ID:           uuid.New().String(),
			Timestamp:    time.Now(),
		}

		// Append runner completion event to session.
		if err := r.sessionService.AppendEvent(
			ctx, sess, runnerCompletionEvent,
		); err != nil {
			log.Errorf("Failed to append runner completion event to session: %v", err)
		}

		// Send the runner completion event to output channel.
		select {
		case processedEventCh <- runnerCompletionEvent:
		case <-ctx.Done():
		}
	}()

	itelemetry.TraceRunner(span, r.appName, invocation, message)

	return processedEventCh, nil
}

// RunWithMessages is a convenience helper that lets callers pass a full
// conversation history ([]model.Message) directly, without relying on the
// session service. It preserves backward compatibility by delegating to the
// existing Runner.Run with an empty message and a RunOption that carries the
// conversation history.
func RunWithMessages(
	ctx context.Context,
	r Runner,
	userID string,
	sessionID string,
	messages []model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	runOpts = append(runOpts, agent.WithMessages(messages))
	// Derive the latest user message for invocation state compatibility
	// (e.g., used by GraphAgent to set initial user_input).
	var latestUser model.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser && (messages[i].Content != "" || len(messages[i].ContentParts) > 0) {
			latestUser = messages[i]
			break
		}
	}
	return r.Run(ctx, userID, sessionID, latestUser, runOpts...)
}
