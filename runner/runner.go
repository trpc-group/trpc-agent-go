//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

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
	"trpc.group/trpc-go/trpc-agent-go/event"
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
	appName        string
	agent          agent.Agent
	sessionService session.Service
}

// Options is the options for the Runner.
type Options struct {
	sessionService session.Service
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
		appName:        appName,
		agent:          agent,
		sessionService: options.sessionService,
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
	ctx, span := trace.Tracer.Start(ctx, "invocation")
	defer span.End()

	// Get or create the session, and generate a new invocation ID.
	sess, invocationID, err := r.getOrCreateSession(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}

	// Append the incoming user message to the session if it has content.
	if err := r.appendUserEventIfAny(ctx, sess, invocationID, message); err != nil {
		return nil, err
	}

	// Create the agent invocation with provided run options.
	invocation := r.createInvocation(sess, invocationID, message, runOpts...)

	// Run the agent and get the event channel.
	agentEventCh, err := r.agent.Run(ctx, invocation)
	if err != nil {
		return nil, err
	}

	// Create a new channel for processed events.
	processedEventCh := make(chan *event.Event)
	// Start a goroutine to process and append events to session.
	go r.handleAgentEvents(ctx, sess, invocationID, agentEventCh, processedEventCh)
	return processedEventCh, nil
}

// getOrCreateSession fetches the session, creating it if missing, and returns invocation ID.
func (r *runner) getOrCreateSession(
	ctx context.Context,
	userID string,
	sessionID string,
) (*session.Session, string, error) {
	sessionKey := session.Key{
		AppName:   r.appName,
		UserID:    userID,
		SessionID: sessionID,
	}

	// Get session or create if it doesn't exist.
	sess, err := r.sessionService.GetSession(ctx, sessionKey)
	if err != nil {
		return nil, "", err
	}
	if sess == nil {
		if sess, err = r.sessionService.CreateSession(
			ctx, sessionKey, session.StateMap{},
		); err != nil {
			return nil, "", err
		}
	}
	// Generate invocation ID.
	invocationID := "invocation-" + uuid.New().String()
	return sess, invocationID, nil
}

// appendUserEventIfAny appends a user event to the session when message has content.
func (r *runner) appendUserEventIfAny(
	ctx context.Context,
	sess *session.Session,
	invocationID string,
	message model.Message,
) error {
	if message.Content == "" {
		return nil
	}
	userEvent := &event.Event{
		Response:     &model.Response{Done: false},
		InvocationID: invocationID,
		Author:       authorUser,
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		Branch:       "", // User events typically do not have branch constraints.
	}
	userEvent.Response.Choices = []model.Choice{{Index: 0, Message: message}}
	return r.sessionService.AppendEvent(ctx, sess, userEvent)
}

// createInvocation constructs an agent invocation.
func (r *runner) createInvocation(
	sess *session.Session,
	invocationID string,
	message model.Message,
	runOpts ...agent.RunOption,
) *agent.Invocation {
	var ro agent.RunOptions
	for _, opt := range runOpts {
		opt(&ro)
	}
	return &agent.Invocation{
		Agent:             r.agent,
		Session:           sess,
		InvocationID:      invocationID,
		EndInvocation:     false,
		Message:           message,
		RunOptions:        ro,
		EventCompletionCh: make(chan string),
	}
}

// handleAgentEvents processes agent events, persists them, emits completion, and triggers summarization.
func (r *runner) handleAgentEvents(
	ctx context.Context,
	sess *session.Session,
	invocationID string,
	agentEventCh <-chan *event.Event,
	processedEventCh chan<- *event.Event,
) {
	defer close(processedEventCh)

	for agentEvent := range agentEventCh {
		// Append event to session if it's complete (not partial) or has state delta.
		if r.shouldAppend(agentEvent) {
			if err := r.sessionService.AppendEvent(ctx, sess, agentEvent); err != nil {
				log.Errorf("Failed to append event to session: %v", err)
			}
		}
		// Forward the event to the output channel.
		select {
		case processedEventCh <- agentEvent:
		case <-ctx.Done():
			return
		}
	}

	// Emit final runner completion event after all agent events are processed.
	r.emitRunnerCompletion(ctx, sess, invocationID, processedEventCh)

	// After a turn completes, trigger summarization via the session service
	// asynchronously to avoid blocking the main thread.
	go func() {
		if err := r.sessionService.CreateSessionSummary(ctx, sess, false); err != nil {
			log.Warnf("Failed to summarize session: %v", err)
		}
	}()
}

// shouldAppend returns true when event should be persisted into the session.
func (r *runner) shouldAppend(ev *event.Event) bool {
	if ev == nil {
		return false
	}
	if ev.StateDelta != nil {
		return true
	}
	if ev.Response == nil {
		return false
	}
	return !ev.Response.IsPartial && ev.Response.Choices != nil
}

// emitRunnerCompletion appends and emits the runner completion event.
func (r *runner) emitRunnerCompletion(
	ctx context.Context,
	sess *session.Session,
	invocationID string,
	processedEventCh chan<- *event.Event,
) {
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
	if err := r.sessionService.AppendEvent(ctx, sess, runnerCompletionEvent); err != nil {
		log.Errorf("Failed to append runner completion event to session: %v", err)
	}
	select {
	case processedEventCh <- runnerCompletionEvent:
	case <-ctx.Done():
	}
}
