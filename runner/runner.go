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
	"encoding/json"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/appid"
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

// WithMemoryService sets the memory service to use.
func WithMemoryService(service memory.Service) Option {
	return func(opts *Options) {
		opts.memoryService = service
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
	memoryService   memory.Service
	artifactService artifact.Service
}

// Options is the options for the Runner.
type Options struct {
	sessionService  session.Service
	memoryService   memory.Service
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
	// Register this runner's identity for observability fallback.
	appid.RegisterRunner(appName, agent.Info().Name)

	return &runner{
		appName:         appName,
		agent:           agent,
		sessionService:  options.sessionService,
		memoryService:   options.memoryService,
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
	// Resolve or create the session for this user and conversation.
	sessionKey := session.Key{
		AppName:   r.appName,
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := r.getOrCreateSession(ctx, sessionKey)
	if err != nil {
		return nil, err
	}

	// Build run options with defaults and construct the invocation.
	ro := agent.RunOptions{RequestID: uuid.NewString()}
	for _, opt := range runOpts {
		opt(&ro)
	}
	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(message),
		agent.WithInvocationAgent(r.agent),
		agent.WithInvocationRunOptions(ro),
		agent.WithInvocationMemoryService(r.memoryService),
		agent.WithInvocationArtifactService(r.artifactService),
		agent.WithInvocationEventFilterKey(r.appName),
	)

	// If caller provided a history via RunOptions and the session is empty,
	// persist that history into the session exactly once, so subsequent turns
	// and tool calls build on the same canonical transcript.
	if len(ro.Messages) > 0 && sess.GetEventCount() == 0 {
		for _, msg := range ro.Messages {
			author := r.agent.Info().Name
			if msg.Role == model.RoleUser {
				author = authorUser
			}
			m := msg
			seedEvt := event.NewResponseEvent(
				invocation.InvocationID,
				author,
				&model.Response{Done: false, Choices: []model.Choice{{Index: 0, Message: m}}},
			)
			agent.InjectIntoEvent(invocation, seedEvt)
			if err := r.sessionService.AppendEvent(ctx, sess, seedEvt); err != nil {
				return nil, err
			}
		}
	}

	// Append the incoming user message to the session if it has content.
	if message.Content != "" && shouldAppendUserMessage(message, ro.Messages) {
		evt := event.NewResponseEvent(
			invocation.InvocationID,
			authorUser,
			&model.Response{Done: false, Choices: []model.Choice{{Index: 0, Message: message}}},
		)
		agent.InjectIntoEvent(invocation, evt)
		if err := r.sessionService.AppendEvent(ctx, sess, evt); err != nil {
			return nil, err
		}
	}

	// Ensure the invocation can be accessed by downstream components (e.g., tools)
	// by embedding it into the context. This is necessary for tools like
	// transfer_to_agent that rely on agent.InvocationFromContext(ctx).
	ctx = agent.NewInvocationContext(ctx, invocation)

	// Run the agent and get the event channel.
	agentEventCh, err := r.agent.Run(ctx, invocation)
	if err != nil {
		invocation.CleanupNotice(ctx)
		return nil, err
	}

	// Process the agent events and emit them to the output channel.
	return r.processAgentEvents(ctx, sess, invocation, agentEventCh), nil
}

// getOrCreateSession returns an existing session or creates a new one.
func (r *runner) getOrCreateSession(
	ctx context.Context, key session.Key,
) (*session.Session, error) {
	sess, err := r.sessionService.GetSession(ctx, key)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		return sess, nil
	}
	return r.sessionService.CreateSession(ctx, key, session.StateMap{})
}

// processAgentEvents consumes agent events, persists to session, and emits.
func (r *runner) processAgentEvents(
	ctx context.Context,
	sess *session.Session,
	invocation *agent.Invocation,
	agentEventCh <-chan *event.Event,
) chan *event.Event {
	processedEventCh := make(chan *event.Event, cap(agentEventCh))
	// Capture the graph-level final snapshot if present so we can propagate it
	// to the terminal runner-completion event. This makes “final output”
	// discoverable from the very last event without requiring graph-specific
	// branching in user code.
	var finalStateDelta map[string][]byte
	var finalChoices []model.Choice
	// Start a goroutine to process and append events to session.
	go func() {
		defer func() {
			if rr := recover(); rr != nil {
				log.Errorf("panic in runner event loop: %v\n%s", rr, string(debug.Stack()))
			}
			close(processedEventCh)
			invocation.CleanupNotice(ctx)
		}()

		// Process all agent events.
		for agentEvent := range agentEventCh {
			if agentEvent == nil {
				log.Debug("agentEvent is nil.")
				continue
			}

			// Append qualifying events to session and trigger summarization.
			r.handleEventPersistence(ctx, sess, agentEvent)

			// Capture graph-level completion snapshot for final event.
			if agentEvent.Done && agentEvent.Object == graph.ObjectTypeGraphExecution {
				finalStateDelta, finalChoices = r.captureGraphCompletion(agentEvent)
			}

			// Notify completion if required.
			if agentEvent.RequiresCompletion {
				completionID := agent.GetAppendEventNoticeKey(agentEvent.ID)
				invocation.NotifyCompletion(ctx, completionID)
			}

			// Emit event to output channel.
			if err := event.EmitEvent(ctx, processedEventCh, agentEvent); err != nil {
				return
			}
		}

		// Emit final runner completion event.
		r.emitRunnerCompletion(ctx, invocation, sess, processedEventCh,
			finalStateDelta, finalChoices)
	}()
	return processedEventCh
}

// handleEventPersistence appends qualifying events to the session and triggers
// asynchronous summarization.
func (r *runner) handleEventPersistence(
	ctx context.Context,
	sess *session.Session,
	agentEvent *event.Event,
) {
	// Append event to session if it's complete (not partial).
	if !r.shouldPersistEvent(agentEvent) {
		return
	}

	if err := r.sessionService.AppendEvent(ctx, sess, agentEvent); err != nil {
		log.Errorf("Failed to append event to session: %v", err)
		return
	}

	// Trigger summarization immediately after appending a qualifying event.
	// Use EnqueueSummaryJob for true asynchronous processing.
	// Prefer filter-specific summarization to avoid scanning all filters.
	if err := r.sessionService.EnqueueSummaryJob(
		context.Background(), sess, agentEvent.FilterKey, false,
	); err != nil {
		log.Debugf("Auto summarize after append skipped or failed: %v.", err)
	}
	// Do not enqueue full-session summary here. The worker will cascade
	// a full-session summarization after a branch update when appropriate.
}

// shouldPersistEvent determines if an event should be persisted to the session.
// Events are persisted if they contain state deltas or are complete, valid
// responses.
func (r *runner) shouldPersistEvent(agentEvent *event.Event) bool {
	return len(agentEvent.StateDelta) > 0 ||
		(agentEvent.Response != nil && !agentEvent.IsPartial && agentEvent.IsValidContent())
}

// captureGraphCompletion captures the final state delta and choices from a
// graph execution completion event.
func (r *runner) captureGraphCompletion(
	agentEvent *event.Event,
) (map[string][]byte, []model.Choice) {
	// Shallow copy map (values are immutable []byte owned by event stream).
	finalStateDelta := make(map[string][]byte, len(agentEvent.StateDelta))
	for k, v := range agentEvent.StateDelta {
		// Copy bytes to avoid accidental mutation downstream.
		if v != nil {
			vv := make([]byte, len(v))
			copy(vv, v)
			finalStateDelta[k] = vv
		}
	}

	var finalChoices []model.Choice
	if agentEvent.Response != nil && len(agentEvent.Response.Choices) > 0 {
		finalChoices = agentEvent.Response.Choices
	}
	return finalStateDelta, finalChoices
}

// emitRunnerCompletion creates and emits the final runner completion event,
// optionally propagating graph-level completion data.
func (r *runner) emitRunnerCompletion(
	ctx context.Context,
	invocation *agent.Invocation,
	sess *session.Session,
	processedEventCh chan *event.Event,
	finalStateDelta map[string][]byte,
	finalChoices []model.Choice,
) {
	// Create runner completion event.
	runnerCompletionEvent := event.NewResponseEvent(
		invocation.InvocationID,
		r.appName,
		&model.Response{
			ID:        "runner-completion-" + uuid.New().String(),
			Object:    model.ObjectTypeRunnerCompletion,
			Created:   time.Now().Unix(),
			Done:      true,
			IsPartial: false,
		},
	)

	// Propagate graph-level completion data if available.
	if len(finalStateDelta) > 0 {
		r.propagateGraphCompletion(runnerCompletionEvent, finalStateDelta, finalChoices)
	}

	// Append runner completion event to session.
	if err := r.sessionService.AppendEvent(ctx, sess, runnerCompletionEvent); err != nil {
		log.Errorf("Failed to append runner completion event to session: %v", err)
	}

	// Send the runner completion event to output channel.
	agent.EmitEvent(ctx, invocation, processedEventCh, runnerCompletionEvent)
}

// propagateGraphCompletion propagates graph-level completion data (state delta
// and final choices) to the runner completion event.
func (r *runner) propagateGraphCompletion(
	runnerCompletionEvent *event.Event,
	finalStateDelta map[string][]byte,
	finalChoices []model.Choice,
) {
	// Initialize state delta map if needed.
	if runnerCompletionEvent.StateDelta == nil {
		runnerCompletionEvent.StateDelta = make(map[string][]byte, len(finalStateDelta))
	}

	// Copy state delta with byte ownership.
	for k, v := range finalStateDelta {
		if v != nil {
			vv := make([]byte, len(v))
			copy(vv, v)
			runnerCompletionEvent.StateDelta[k] = vv
		} else {
			runnerCompletionEvent.StateDelta[k] = nil
		}
	}

	// Optionally echo the final text as a non-streaming assistant message
	// if graph provided it in its completion.
	if runnerCompletionEvent.Response != nil &&
		len(runnerCompletionEvent.Response.Choices) == 0 &&
		len(finalChoices) > 0 {
		// Keep only content to avoid carrying tool deltas etc.
		// Use JSON marshal/unmarshal to deep-copy minimal fields safely.
		b, _ := json.Marshal(finalChoices)
		_ = json.Unmarshal(b, &runnerCompletionEvent.Response.Choices)
	}
}

// shouldAppendUserMessage checks if the incoming user message should be appended to the session.
func shouldAppendUserMessage(message model.Message, seed []model.Message) bool {
	if len(seed) == 0 {
		return true
	}
	if message.Role != model.RoleUser {
		return true
	}
	for i := len(seed) - 1; i >= 0; i-- {
		if seed[i].Role != model.RoleUser {
			continue
		}
		return !model.MessagesEqual(seed[i], message)
	}
	return true
}

// RunWithMessages is a convenience helper that lets callers pass a full
// conversation history ([]model.Message) directly. The messages seed the LLM
// request while the runner continues to merge in newer session events. It
// preserves backward compatibility by delegating to Runner.Run with an empty
// message and a RunOption that carries the conversation history.
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
