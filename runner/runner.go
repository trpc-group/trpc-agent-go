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
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
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

// WithAgent adds an agent to the runner registry for name-based lookup.
func WithAgent(name string, ag agent.Agent) Option {
	return func(opts *Options) {
		opts.agents[name] = ag
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

	// Close closes the runner and releases owned resources.
	// It's safe to call Close multiple times.
	// Only resources created by the runner (not provided by user) will be closed.
	Close() error
}

// runner runs agents.
type runner struct {
	appName          string
	defaultAgentName string
	agents           map[string]agent.Agent
	sessionService   session.Service
	memoryService    memory.Service
	artifactService  artifact.Service

	// Resource management fields.
	ownedSessionService bool      // Indicates if sessionService was created by this runner.
	closeOnce           sync.Once // Ensures Close is called only once.
}

// Options is the options for the Runner.
type Options struct {
	sessionService  session.Service
	memoryService   memory.Service
	artifactService artifact.Service
	agents          map[string]agent.Agent
}

// newOptions creates a new Options.
func newOptions(opt ...Option) Options {
	opts := Options{
		agents: make(map[string]agent.Agent),
	}
	for _, o := range opt {
		o(&opts)
	}
	return opts
}

// NewRunner creates a new Runner.
func NewRunner(appName string, ag agent.Agent, opts ...Option) Runner {
	options := newOptions(opts...)
	// Track if we created the session service.
	var ownedSessionService bool
	if options.sessionService == nil {
		options.sessionService = inmemory.NewSessionService()
		ownedSessionService = true
	}
	agents := options.agents
	agents[ag.Info().Name] = ag
	// Register the default agent for observability defaults.
	appid.RegisterRunner(appName, ag.Info().Name)
	// Register all runner identities for observability fallback.
	for _, a := range agents {
		appid.RegisterRunner(appName, a.Info().Name)
	}
	return &runner{
		appName:             appName,
		defaultAgentName:    ag.Info().Name,
		agents:              agents,
		sessionService:      options.sessionService,
		memoryService:       options.memoryService,
		artifactService:     options.artifactService,
		ownedSessionService: ownedSessionService,
	}
}

// Close closes the runner and cleans up owned resources.
// It's safe to call Close multiple times.
// Only resources created by this runner will be closed.
func (r *runner) Close() error {
	var closeErr error
	r.closeOnce.Do(func() {
		// Only close resources that we own (created by this runner).
		if r.ownedSessionService && r.sessionService != nil {
			if err := r.sessionService.Close(); err != nil {
				closeErr = err
				log.Errorf("close session service failed: %v", err)
			}
		}
	})
	return closeErr
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

	ag, err := r.selectAgent(ctx, ro)
	if err != nil {
		return nil, fmt.Errorf("select agent: %w", err)
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(message),
		agent.WithInvocationAgent(ag),
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
			author := ag.Info().Name
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

	// Create flush channel and attach flusher before agent.Run to ensure cloned invocations inherit it.
	flushChan := make(chan *flush.FlushRequest)
	flush.Attach(ctx, invocation, flushChan)
	appender.Attach(invocation, func(ctx context.Context, e *event.Event) error {
		if e == nil {
			return nil
		}
		return r.sessionService.AppendEvent(ctx, sess, e)
	})
	barrier.Enable(invocation)

	// Run the agent and get the event channel.
	agentEventCh, err := ag.Run(ctx, invocation)
	if err != nil {
		// Attempt to persist the error event so the session reflects the failure.
		errorEvent := event.NewErrorEvent(
			invocation.InvocationID,
			ag.Info().Name,
			model.ErrorTypeRunError,
			err.Error(),
		)
		// Populate content to ensure it is valid for persistence (and viewable by users).
		ensureErrorEventContent(errorEvent)

		if appendErr := r.sessionService.AppendEvent(ctx, sess, errorEvent); appendErr != nil {
			log.Errorf("failed to append agent run error event: %v", appendErr)
		}

		invocation.CleanupNotice(ctx)
		return nil, err
	}

	// Process the agent events and emit them to the output channel.
	return r.processAgentEvents(ctx, sess, invocation, agentEventCh, flushChan), nil
}

// resolveAgent decides which agent to use for this run.
func (r *runner) selectAgent(
	_ context.Context,
	ro agent.RunOptions,
) (agent.Agent, error) {
	if ro.Agent != nil {
		appid.RegisterRunner(r.appName, ro.Agent.Info().Name)
		return ro.Agent, nil
	}
	agentName := r.defaultAgentName
	if ro.AgentByName != "" {
		agentName = ro.AgentByName
	}
	if ag, ok := r.agents[agentName]; ok && ag != nil {
		return ag, nil
	}
	return nil, fmt.Errorf("runner: agent %q not found", agentName)
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

// eventLoopContext bundles all channels and state required by the event loop.
type eventLoopContext struct {
	sess             *session.Session
	invocation       *agent.Invocation
	agentEventCh     <-chan *event.Event
	flushChan        chan *flush.FlushRequest
	processedEventCh chan *event.Event
	finalStateDelta  map[string][]byte
	finalChoices     []model.Choice
	assistantContent map[string]struct{}
}

// processAgentEvents consumes agent events, persists to session, and emits.
func (r *runner) processAgentEvents(
	ctx context.Context,
	sess *session.Session,
	invocation *agent.Invocation,
	agentEventCh <-chan *event.Event,
	flushChan chan *flush.FlushRequest,
) chan *event.Event {
	processedEventCh := make(chan *event.Event, cap(agentEventCh))
	loop := &eventLoopContext{
		sess:             sess,
		invocation:       invocation,
		agentEventCh:     agentEventCh,
		flushChan:        flushChan,
		processedEventCh: processedEventCh,
		assistantContent: make(map[string]struct{}),
	}
	runCtx := agent.CloneContext(ctx)
	go r.runEventLoop(runCtx, loop)
	return processedEventCh
}

// runEventLoop drives the main event processing loop for a single invocation.
func (r *runner) runEventLoop(ctx context.Context, loop *eventLoopContext) {
	defer func() {
		if rr := recover(); rr != nil {
			log.Errorf("panic in runner event loop: %v\n%s", rr, string(debug.Stack()))
		}
		// Agent event stream completed.
		r.safeEmitRunnerCompletion(ctx, loop)
		// Disable further flush requests for this invocation.
		flush.Clear(loop.invocation)
		appender.Clear(loop.invocation)
		close(loop.processedEventCh)
		loop.invocation.CleanupNotice(ctx)
	}()
	for {
		select {
		case agentEvent, ok := <-loop.agentEventCh:
			if !ok {
				return
			}
			if err := r.processSingleAgentEvent(ctx, loop, agentEvent); err != nil {
				log.Errorf("process single agent event: %v", err)
				return
			}
		case req, ok := <-loop.flushChan:
			// Flush channel closed, disable further flush handling.
			if !ok {
				loop.flushChan = nil
				continue
			}
			if req == nil || req.ACK == nil {
				log.Errorf("flush request is nil or ACK is nil")
				continue
			}
			// Handle the flush request.
			if err := r.handleFlushRequest(ctx, loop, req); err != nil {
				log.Errorf("handle flush request: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

// processSingleAgentEvent handles a single agent event.
func (r *runner) processSingleAgentEvent(ctx context.Context, loop *eventLoopContext, agentEvent *event.Event) error {
	if agentEvent == nil {
		// Preserve existing behavior: skip nil events without failing the loop.
		log.Errorf("agentEvent is nil")
		return nil
	}

	r.recordAssistantContent(loop, agentEvent)

	// Append qualifying events to session and trigger summarization.
	r.handleEventPersistence(ctx, loop.sess, agentEvent)

	// Capture graph-level completion snapshot for final event.
	if isGraphCompletionEvent(agentEvent) {
		loop.finalStateDelta, loop.finalChoices = r.captureGraphCompletion(agentEvent)
	}

	// Notify completion if required.
	if agentEvent.RequiresCompletion {
		completionID := agent.GetAppendEventNoticeKey(agentEvent.ID)
		loop.invocation.NotifyCompletion(ctx, completionID)
	}

	// Emit event to output channel.
	if err := event.EmitEvent(ctx, loop.processedEventCh, agentEvent); err != nil {
		return fmt.Errorf("emit event to output channel: %w", err)
	}

	return nil
}

func (r *runner) recordAssistantContent(loop *eventLoopContext, e *event.Event) {
	if loop == nil || e == nil || e.Response == nil {
		return
	}
	if loop.invocation == nil {
		return
	}
	if loop.assistantContent == nil {
		loop.assistantContent = make(map[string]struct{})
	}
	if isGraphCompletionEvent(e) {
		return
	}
	if e.IsPartial || !e.IsValidContent() {
		return
	}
	for _, choice := range e.Response.Choices {
		msg := choice.Message
		if msg.Role != model.RoleAssistant {
			continue
		}
		if msg.Content == "" {
			continue
		}
		loop.assistantContent[msg.Content] = struct{}{}
	}
}

// safeEmitRunnerCompletion guards emitRunnerCompletion against panics from session services.
func (r *runner) safeEmitRunnerCompletion(ctx context.Context, loop *eventLoopContext) {
	defer func() {
		if rr := recover(); rr != nil {
			log.Errorf("panic emitting runner completion: %v\n%s", rr, string(debug.Stack()))
		}
	}()
	r.emitRunnerCompletion(ctx, loop)
}

// handleFlushRequest drains buffered agent events when a flush request arrives and closes the request's ACK channel
// once all events currently buffered in the agent event channel have been processed.
func (r *runner) handleFlushRequest(ctx context.Context, loop *eventLoopContext, req *flush.FlushRequest) error {
	defer close(req.ACK)
	for {
		select {
		case agentEvent, ok := <-loop.agentEventCh:
			if !ok {
				return nil
			}
			if err := r.processSingleAgentEvent(ctx, loop, agentEvent); err != nil {
				return fmt.Errorf("process single agent event: %w", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
}

// handleEventPersistence appends qualifying events to the session and triggers
// asynchronous summarization.
func (r *runner) handleEventPersistence(
	ctx context.Context,
	sess *session.Session,
	agentEvent *event.Event,
) {
	// Ensure error events have content so they are valid for persistence.
	ensureErrorEventContent(agentEvent)

	// Append event to session if it's complete (not partial).
	if !r.shouldPersistEvent(agentEvent) {
		return
	}

	persistEvent := agentEvent
	if isGraphCompletionEvent(agentEvent) {
		eventCopy := *agentEvent
		eventCopy.Response = agentEvent.Response.Clone()
		eventCopy.Response.Choices = nil
		persistEvent = &eventCopy
	}

	if err := r.sessionService.AppendEvent(
		ctx,
		sess,
		persistEvent,
	); err != nil {
		log.Errorf("Failed to append event to session: %v", err)
		return
	}

	// Trigger summarization only after final assistant responses.
	// Skip user messages, tool calls, and tool results to ensure summary
	// always contains complete Q&A pairs (including tool call round-trips).
	if agentEvent.IsUserMessage() ||
		agentEvent.IsToolCallResponse() ||
		agentEvent.IsToolResultResponse() ||
		!agentEvent.IsValidContent() {
		return
	}

	// Use EnqueueSummaryJob for true asynchronous processing.
	// Prefer filter-specific summarization to avoid scanning all filters.
	if err := r.sessionService.EnqueueSummaryJob(
		ctx, sess, agentEvent.FilterKey, false,
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

func isGraphCompletionEvent(agentEvent *event.Event) bool {
	if agentEvent == nil || agentEvent.Response == nil {
		return false
	}
	return agentEvent.Done &&
		agentEvent.Object == graph.ObjectTypeGraphExecution
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
func (r *runner) emitRunnerCompletion(ctx context.Context, loop *eventLoopContext) {
	// Create runner completion event.
	runnerCompletionEvent := event.NewResponseEvent(
		loop.invocation.InvocationID,
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
	if len(loop.finalStateDelta) > 0 {
		includeChoices := r.shouldIncludeFinalChoices(loop)
		r.propagateGraphCompletion(
			runnerCompletionEvent,
			loop.finalStateDelta,
			loop.finalChoices,
			includeChoices,
		)
	}

	// Append runner completion event to session.
	if err := r.sessionService.AppendEvent(ctx, loop.sess, runnerCompletionEvent); err != nil {
		log.Errorf("Failed to append runner completion event to session: %v", err)
	}

	// Send the runner completion event to output channel.
	agent.EmitEvent(ctx, loop.invocation, loop.processedEventCh, runnerCompletionEvent)
}

// propagateGraphCompletion propagates graph-level completion data (state delta
// and final choices) to the runner completion event.
func (r *runner) propagateGraphCompletion(
	runnerCompletionEvent *event.Event,
	finalStateDelta map[string][]byte,
	finalChoices []model.Choice,
	includeChoices bool,
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
	if includeChoices &&
		runnerCompletionEvent.Response != nil &&
		len(runnerCompletionEvent.Response.Choices) == 0 &&
		len(finalChoices) > 0 {
		// Keep only content to avoid carrying tool deltas etc.
		// Use JSON marshal/unmarshal to deep-copy minimal fields safely.
		b, _ := json.Marshal(finalChoices)
		_ = json.Unmarshal(b, &runnerCompletionEvent.Response.Choices)
	}
}

func (r *runner) shouldIncludeFinalChoices(loop *eventLoopContext) bool {
	if loop == nil {
		return true
	}
	if len(loop.finalChoices) == 0 {
		return false
	}
	if len(loop.assistantContent) == 0 {
		return true
	}
	for _, choice := range loop.finalChoices {
		msg := choice.Message
		if msg.Role != model.RoleAssistant {
			continue
		}
		if msg.Content == "" {
			continue
		}
		if _, ok := loop.assistantContent[msg.Content]; ok {
			return false
		}
	}
	return true
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

// ensureErrorEventContent ensures that error events have valid content.
// This is necessary because some models return error responses without content,
// which would otherwise be discarded by the session service.
func ensureErrorEventContent(e *event.Event) {
	if e == nil || e.Response == nil || e.Response.Error == nil {
		return
	}
	// If content is valid (non-empty), do nothing.
	if e.IsValidContent() {
		return
	}

	// Ensure Choices slice exists
	if len(e.Response.Choices) == 0 {
		e.Response.Choices = []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
			},
		}}
	}

	// Populate content if empty
	if e.Response.Choices[0].Message.Content == "" {
		e.Response.Choices[0].Message.Content = "An error occurred during execution. Please contact the service provider."
	}

	// Ensure FinishReason is set
	if e.Response.Choices[0].FinishReason == nil {
		reason := "error"
		e.Response.Choices[0].FinishReason = &reason
	}
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
