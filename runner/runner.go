//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package runner provides the core runner functionality.
package runner

import (
	"context"
	"fmt"
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
		// Variadic run options placeholder for future extension.
		runOpts ...agent.RunOptions,
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
	runOpts ...agent.RunOptions,
) (<-chan *event.Event, error) {
	ctx, span := trace.Tracer.Start(ctx, fmt.Sprintf("invocation"))
	defer span.End()

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
	eventCompletionCh := make(chan string)
	var ro agent.RunOptions
	if len(runOpts) > 0 {
		ro = runOpts[0]
	}
	invocation := &agent.Invocation{
		Agent:             r.agent,
		Session:           sess,
		InvocationID:      invocationID,
		EndInvocation:     false,
		Message:           message,
		RunOptions:        ro,
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
				if err := r.sessionService.AppendEvent(ctx, sess, agentEvent); err != nil {
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

	return processedEventCh, nil
}
