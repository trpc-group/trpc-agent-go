//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package graphagent provides a graph-based agent implementation.
package graphagent

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const invocationNilErrMsg = "invocation is nil"

// GraphAgent is an agent that executes a graph.
type GraphAgent struct {
	name              string
	description       string
	graph             *graph.Graph
	executor          *graph.Executor
	subAgents         []agent.Agent
	agentCallbacks    *agent.Callbacks
	initialState      graph.State
	channelBufferSize int
	options           Options
}

// New creates a new GraphAgent with the given graph and options.
func New(name string, g *graph.Graph, opts ...Option) (*GraphAgent, error) {
	// set default channel buffer size.
	options := defaultOptions

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	// Build executor options.
	// First, apply mapped options (ChannelBufferSize, MaxConcurrency, CheckpointSaver).
	var executorOpts []graph.ExecutorOption
	executorOpts = append(executorOpts,
		graph.WithChannelBufferSize(options.ChannelBufferSize))
	if options.MaxConcurrency != 0 {
		executorOpts = append(executorOpts,
			graph.WithMaxConcurrency(options.MaxConcurrency))
	}
	if options.CheckpointSaver != nil {
		executorOpts = append(executorOpts,
			graph.WithCheckpointSaver(options.CheckpointSaver))
	}
	if options.ExecutionEngine != "" {
		executorOpts = append(executorOpts,
			graph.WithExecutionEngine(options.ExecutionEngine))
	}

	// Then, append user-provided executor options.
	// These options are applied after the mapped options, so they can override
	// the mapped settings if needed.
	executorOpts = append(executorOpts, options.ExecutorOptions...)

	executor, err := graph.NewExecutor(g, executorOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create graph executor: %w", err)
	}

	return &GraphAgent{
		name:              name,
		description:       options.Description,
		graph:             g,
		executor:          executor,
		subAgents:         options.SubAgents,
		agentCallbacks:    options.AgentCallbacks,
		initialState:      options.InitialState,
		channelBufferSize: options.ChannelBufferSize,
		options:           options,
	}, nil
}

// Run executes the graph with the provided invocation.
func (ga *GraphAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New(invocationNilErrMsg)
	}
	// Setup invocation
	ga.setupInvocation(invocation)
	out := make(chan *event.Event, ga.eventChannelBufferSize(invocation))
	emitChan := out
	if shouldHideGraphAgentBarrierEvents(invocation) {
		hiddenChan := make(chan *event.Event, ga.eventChannelBufferSize(invocation))
		emitChan = hiddenChan
		forwardCtx := agent.CloneContext(ctx)
		go ga.forwardVisibleEvents(forwardCtx, invocation, hiddenChan, out)
	}
	runCtx := agent.CloneContext(ctx)
	if invocation.RunOptions.DisableTracing && ga.agentCallbacks == nil && !barrier.Enabled(invocation) {
		go ga.runWithoutBarrier(runCtx, invocation, emitChan)
		return out, nil
	}
	go ga.runWithBarrier(runCtx, invocation, emitChan)
	return out, nil
}

// eventChannelBufferSize returns the effective event channel buffer size for a run.
func (ga *GraphAgent) eventChannelBufferSize(invocation *agent.Invocation) int {
	if size := agent.GetEventChannelBufferSize(invocation); size > 0 {
		return size
	}
	return ga.channelBufferSize
}

// singleEventChannelBufferSize reserves one slot for immediate short-circuit responses.
func (ga *GraphAgent) singleEventChannelBufferSize(invocation *agent.Invocation) int {
	size := ga.eventChannelBufferSize(invocation)
	if size <= 0 {
		return 1
	}
	return size
}

func (ga *GraphAgent) runWithoutBarrier(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event) {
	initialState := ga.createInitialState(ctx, invocation)
	executeCtx := ctx
	suppressHiddenCompletion := shouldSuppressHiddenCompletion(ctx, invocation)
	if suppressHiddenCompletion {
		executeCtx = graph.WithGraphCompletionCapture(ctx)
	}
	innerChan, err := ga.executor.Execute(executeCtx, initialState, invocation)
	if err != nil {
		evt := event.NewErrorEvent(invocation.InvocationID, invocation.AgentName,
			model.ErrorTypeFlowError, err.Error())
		defer close(out)
		if emitErr := agent.EmitEvent(ctx, invocation, out, evt); emitErr != nil {
			log.Errorf("graphagent: emit error event failed: %v", emitErr)
		}
		return
	}
	defer close(out)
	terminalMessageFilter := newTerminalMessageFilter(invocation, ga.graph)
	_, _ = ga.forwardWrappedEvents(
		ctx,
		invocation,
		innerChan,
		suppressHiddenCompletion,
		func(evt *event.Event) error {
			if !terminalMessageFilter.Allows(evt) {
				return nil
			}
			return event.EmitEvent(ctx, out, evt)
		},
	)
}

func (ga *GraphAgent) forwardEventStream(ctx context.Context, innerChan <-chan *event.Event, out chan<- *event.Event) {
	defer close(out)
	for evt := range innerChan {
		if err := event.EmitEvent(ctx, out, evt); err != nil {
			log.Errorf("graphagent: emit event failed: %v.", err)
			return
		}
	}
}

// runWithBarrier emits a start barrier, waits for completion, then runs the graph with callbacks
// pipeline and forwards all events to the provided output channel.
func (ga *GraphAgent) runWithBarrier(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event) {
	var span oteltrace.Span
	stream := resolveGraphAgentStream(invocation)
	tracingEnabled := !invocation.RunOptions.DisableTracing
	if tracingEnabled {
		ctx, span = trace.Tracer.Start(ctx, fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, invocation.AgentName))
		itelemetry.TraceBeforeInvokeAgent(
			span,
			invocation,
			ga.description,
			"",
			&model.GenerationConfig{Stream: stream},
		)
	}
	defer close(out)
	var trackerErr error
	tracker := itelemetry.NewInvokeAgentTracker(ctx, invocation, stream, &trackerErr)
	tokenUsage := &itelemetry.TokenUsage{}
	var fullRespEvent *event.Event
	var operationErrorType string
	defer func() {
		if tracingEnabled && fullRespEvent != nil {
			itelemetry.TraceAfterInvokeAgent(
				span,
				fullRespEvent,
				tokenUsage,
				tracker.FirstTokenTimeDuration(),
				model.ErrorTypeFlowError,
			)
		}
		tracker.SetResponseErrorType(resolveGraphAgentErrorType(fullRespEvent, operationErrorType))
		tracker.RecordMetrics()()
		if tracingEnabled {
			span.End()
		}
	}()
	// Emit a barrier event and wait for completion in a dedicated goroutine so that the runner can append all prior
	// events before GraphAgent reads history.
	if err := ga.emitStartBarrierAndWait(ctx, invocation, out); err != nil {
		evt := event.NewErrorEvent(invocation.InvocationID, invocation.AgentName,
			model.ErrorTypeFlowError, err.Error())
		fullRespEvent = evt
		operationErrorType = itelemetry.ToErrorType(err, model.ErrorTypeFlowError)
		if tracingEnabled {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeFlowError)))
		}
		if emitErr := agent.EmitEvent(ctx, invocation, out, evt); emitErr != nil {
			log.Errorf("graphagent: emit error event failed: %v", emitErr)
		}
		return
	}
	innerChan, err := ga.runWithCallbacks(ctx, invocation)
	if err != nil {
		evt := event.NewErrorEvent(invocation.InvocationID, invocation.AgentName,
			model.ErrorTypeFlowError, err.Error())
		fullRespEvent = evt
		operationErrorType = itelemetry.ToErrorType(err, model.ErrorTypeFlowError)
		if tracingEnabled {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeFlowError)))
		}
		if emitErr := agent.EmitEvent(ctx, invocation, out, evt); emitErr != nil {
			log.Errorf("graphagent: emit error event failed: %v.", emitErr)
		}
		return
	}
	terminalMessageFilter := newTerminalMessageFilter(invocation, ga.graph)
	for evt := range innerChan {
		fullRespEvent = recordTraceEvent(tracker, tokenUsage, fullRespEvent, evt)
		if !terminalMessageFilter.Allows(evt) {
			continue
		}
		if err := event.EmitEvent(ctx, out, evt); err != nil {
			operationErrorType = itelemetry.ToErrorType(err, model.ErrorTypeFlowError)
			if tracingEnabled {
				span.SetStatus(codes.Error, err.Error())
				span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeFlowError)))
			}
			log.Errorf("graphagent: emit event failed: %v.", err)
			return
		}
	}
}

// resolveGraphAgentStream returns the effective streaming mode for GraphAgent.
// Graph-based executions default to streaming unless the caller explicitly
// overrides the run with agent.WithStream(false).
func resolveGraphAgentStream(invocation *agent.Invocation) bool {
	if invocation != nil && invocation.RunOptions.Stream != nil {
		return *invocation.RunOptions.Stream
	}
	return true
}

// resolveGraphAgentErrorType collapses the final GraphAgent metric error type.
// Transport or orchestration failures win because they indicate the invocation
// itself failed even if a response event had already been observed. Otherwise,
// use the final response event so after-agent callbacks can replace an earlier
// failure with a successful custom response.
func resolveGraphAgentErrorType(fullRespEvent *event.Event, operationErrorType string) string {
	if operationErrorType != "" {
		return operationErrorType
	}
	if fullRespEvent == nil || fullRespEvent.Response == nil || fullRespEvent.Response.Error == nil {
		return ""
	}
	return itelemetry.FormatResponseErrorLabel(
		fullRespEvent.Response.Error,
		model.ErrorTypeFlowError,
	)
}

func recordTraceEvent(
	tracker *itelemetry.InvokeAgentTracker,
	tokenUsage *itelemetry.TokenUsage,
	fullRespEvent *event.Event,
	evt *event.Event,
) *event.Event {
	if evt == nil || evt.Response == nil {
		return fullRespEvent
	}
	if tracker != nil {
		tracker.TrackResponse(evt.Response)
	}
	if evt.Response.IsPartial {
		return fullRespEvent
	}
	if !evt.IsError() && !evt.Response.IsValidContent() &&
		!(evt.Done && evt.Object == graph.ObjectTypeGraphExecution) {
		return fullRespEvent
	}
	if evt.Response.Usage != nil && tokenUsage != nil {
		tokenUsage.PromptTokens += evt.Response.Usage.PromptTokens
		tokenUsage.CompletionTokens += evt.Response.Usage.CompletionTokens
		tokenUsage.TotalTokens += evt.Response.Usage.TotalTokens
	}
	return evt
}

// emitStartBarrierAndWait emits a barrier event and waits until the runner has processed it,
// ensuring that all prior events have been appended to the session before GraphAgent reads history.
func (ga *GraphAgent) emitStartBarrierAndWait(ctx context.Context, invocation *agent.Invocation,
	ch chan<- *event.Event) error {
	// If graph barrier is not enabled, skip.
	if !barrier.Enabled(invocation) {
		return nil
	}
	barrier := event.New(invocation.InvocationID, invocation.AgentName,
		event.WithObject(graph.ObjectTypeGraphBarrier))
	barrier.RequiresCompletion = true
	completionID := agent.GetAppendEventNoticeKey(barrier.ID)
	if noticeCh := invocation.AddNoticeChannel(ctx, completionID); noticeCh == nil {
		return fmt.Errorf("add notice channel for %s", completionID)
	}
	if err := agent.EmitEvent(ctx, invocation, ch, barrier); err != nil {
		return fmt.Errorf("emit barrier event: %w", err)
	}
	timeout := llmflow.WaitEventTimeout(ctx)
	if err := invocation.AddNoticeChannelAndWait(ctx, completionID, timeout); err != nil {
		return fmt.Errorf("wait for barrier completion: %w", err)
	}
	return nil
}

// runWithCallbacks executes the GraphAgent flow: prepare initial state, run before-agent callbacks, execute the graph,
// and wrap with after-agent callbacks when present.
func (ga *GraphAgent) runWithCallbacks(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Execute the graph.
	if ga.agentCallbacks != nil {
		result, err := ga.agentCallbacks.RunBeforeAgent(ctx, &agent.BeforeAgentArgs{
			Invocation: invocation,
		})
		if err != nil {
			return nil, fmt.Errorf("before agent callback failed: %w", err)
		}
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		if result != nil && result.CustomResponse != nil {
			// Create a channel that returns the custom response and then closes.
			eventChan := make(chan *event.Event, ga.singleEventChannelBufferSize(invocation))
			// Create an event from the custom response.
			customevent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, result.CustomResponse)
			agent.EmitEvent(ctx, invocation, eventChan, customevent)
			close(eventChan)
			return eventChan, nil
		}
	}

	// Prepare initial state after callbacks so that any modifications
	// made by callbacks to the invocation (for example, RuntimeState,
	// Session, or Message) are visible to the graph execution.
	initialState := ga.createInitialState(ctx, invocation)

	// Execute the graph.
	executeCtx := ctx
	shouldWrapHiddenCompletion := shouldSuppressHiddenCompletion(ctx, invocation)
	if shouldWrapHiddenCompletion {
		executeCtx = graph.WithGraphCompletionCapture(ctx)
	}
	eventChan, err := ga.executor.Execute(executeCtx, initialState, invocation)
	if err != nil {
		return nil, err
	}
	if ga.agentCallbacks != nil ||
		shouldWrapHiddenCompletion ||
		invocation.RunOptions.GraphTerminalMessagesOnly {
		return ga.wrapEventChannel(
			ctx,
			invocation,
			eventChan,
			shouldWrapHiddenCompletion,
		), nil
	}
	return eventChan, nil
}

func (ga *GraphAgent) createInitialState(ctx context.Context, invocation *agent.Invocation) graph.State {
	var initialState graph.State

	if ga.initialState != nil {
		// Clone the base initial state to avoid modifying the original.
		initialState = ga.initialState.Clone()
	} else {
		initialState = make(graph.State)
	}

	// Merge runtime state from RunOptions if provided.
	if invocation.RunOptions.RuntimeState != nil {
		for key, value := range invocation.RunOptions.RuntimeState {
			initialState[key] = value
		}
	}

	// Seed messages from session events so multi‑turn runs share history.
	// This mirrors ContentRequestProcessor behavior used by non-graph flows.
	if invocation.Session != nil {
		// Build a temporary request to reuse the processor logic.
		req := &model.Request{}

		// Default processor: include (possibly overridden) + preserve same branch.
		contentOpts := []processor.ContentOption{
			processor.WithAddSessionSummary(ga.options.AddSessionSummary),
			processor.WithSessionSummaryInjectionMode(ga.options.SessionSummaryInjectionMode),
			processor.WithMaxHistoryRuns(ga.options.MaxHistoryRuns),
			processor.WithEnableContextCompaction(
				ga.options.EnableContextCompaction,
			),
			processor.WithContextCompactionKeepRecentRequests(
				ga.options.ContextCompactionKeepRecentRequests,
			),
			processor.WithContextCompactionToolResultMaxTokens(
				ga.options.ContextCompactionToolResultMaxTokens,
			),
			processor.WithContextCompactionOversizedToolResultMaxTokens(
				ga.options.ContextCompactionOversizedToolResultMaxTokens,
			),
			processor.WithContextCompactionTokenCounter(
				ga.options.ContextCompactionTokenCounter,
			),
			processor.WithPreserveSameBranch(true),
			processor.WithPreserveForeignMessages(
				ga.options.PreserveForeignMessages,
			),
			processor.WithTimelineFilterMode(ga.options.messageTimelineFilterMode),
			processor.WithBranchFilterMode(ga.options.messageBranchFilterMode),
			processor.WithEventMessageProjector(
				processor.EventMessageProjector(
					ga.options.EventMessageProjector,
				),
			),
		}
		if ga.options.ReasoningContentMode != "" {
			contentOpts = append(contentOpts,
				processor.WithReasoningContentMode(ga.options.ReasoningContentMode))
		}
		if ga.options.summaryFormatter != nil {
			contentOpts = append(contentOpts,
				processor.WithSummaryFormatter(ga.options.summaryFormatter))
		}
		p := processor.NewContentRequestProcessor(contentOpts...)
		// We only need messages side effect; no output channel needed.
		p.ProcessRequest(ctx, invocation, req, nil)
		if len(req.Messages) > 0 {
			initialState[graph.StateKeyMessages] = req.Messages
		}
	}

	// Add invocation message to state.
	// When resuming from checkpoint, only add user input if it's meaningful content
	// (not just a resume signal), following LangGraph's pattern.
	isResuming := invocation.RunOptions.RuntimeState != nil &&
		invocation.RunOptions.RuntimeState[graph.CfgKeyCheckpointID] != nil

	if invocation.Message.Content != "" && invocation.Message.Role == model.RoleUser {
		// If resuming and the message is just "resume", don't add it as input.
		// This allows pure checkpoint resumption without input interference.
		if isResuming && invocation.Message.Content == "resume" {
			// Skip adding user_input to preserve checkpoint state.
		} else {
			// Add user input for normal execution or resume with meaningful input.
			initialState[graph.StateKeyUserInput] = invocation.Message.Content
		}
	}
	// Add session context if available.
	if invocation.Session != nil {
		initialState[graph.StateKeySession] = invocation.Session
	}
	// Add parent agent to state so agent nodes can access sub-agents.
	initialState[graph.StateKeyParentAgent] = ga
	// Set checkpoint namespace if not already set.
	if ns, ok := initialState[graph.CfgKeyCheckpointNS].(string); !ok || ns == "" {
		initialState[graph.CfgKeyCheckpointNS] = ga.name
	}

	return initialState
}

func shouldSuppressHiddenCompletion(
	ctx context.Context,
	invocation *agent.Invocation,
) bool {
	return invocation != nil &&
		agent.IsGraphCompletionEventDisabled(invocation) &&
		!graph.ShouldCaptureGraphCompletion(ctx)
}

func shouldHideGraphAgentBarrierEvents(invocation *agent.Invocation) bool {
	return invocation != nil &&
		barrier.Enabled(invocation) &&
		agent.IsGraphExecutorEventsDisabled(invocation)
}

func shouldSuppressGraphAgentBarrierEvent(
	invocation *agent.Invocation,
	evt *event.Event,
) bool {
	return shouldHideGraphAgentBarrierEvents(invocation) &&
		evt != nil &&
		evt.Object == graph.ObjectTypeGraphBarrier
}

func (ga *GraphAgent) forwardVisibleEvents(
	ctx context.Context,
	invocation *agent.Invocation,
	src <-chan *event.Event,
	dst chan<- *event.Event,
) {
	defer close(dst)
	for evt := range src {
		if shouldSuppressGraphAgentBarrierEvent(invocation, evt) {
			if err := completeSuppressedGraphAgentBarrier(ctx, invocation, evt); err != nil {
				log.Errorf("graphagent: complete hidden barrier failed: %v", err)
				return
			}
			continue
		}
		if err := event.EmitEvent(ctx, dst, evt); err != nil {
			log.Errorf("graphagent: emit forwarded event failed: %v.", err)
			return
		}
	}
}

func completeSuppressedGraphAgentBarrier(
	ctx context.Context,
	invocation *agent.Invocation,
	evt *event.Event,
) error {
	if invocation == nil || evt == nil || !evt.RequiresCompletion {
		return nil
	}
	completionID := agent.GetAppendEventNoticeKey(evt.ID)
	return invocation.NotifyCompletion(ctx, completionID)
}

func (ga *GraphAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent and agent name.
	invocation.Agent = ga
	invocation.AgentName = ga.name
}

// Tools returns the list of tools available to this agent.
func (ga *GraphAgent) Tools() []tool.Tool { return nil }

// TimeTravel exposes checkpoint-based time travel helpers for this GraphAgent.
//
// It requires a checkpoint saver configured via graphagent.WithCheckpointSaver.
func (ga *GraphAgent) TimeTravel() (*graph.TimeTravel, error) {
	if ga == nil || ga.executor == nil {
		return nil, fmt.Errorf("graph executor is not configured")
	}
	return ga.executor.TimeTravel()
}

// Info returns the basic information about this agent.
func (ga *GraphAgent) Info() agent.Info {
	return agent.Info{
		Name:        ga.name,
		Description: ga.description,
	}
}

// SubAgents returns the list of sub-agents available to this agent.
func (ga *GraphAgent) SubAgents() []agent.Agent {
	return ga.subAgents
}

// FindSubAgent finds a sub-agent by name.
func (ga *GraphAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range ga.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

// wrapEventChannel wraps the event channel to apply after agent callbacks.
func (ga *GraphAgent) wrapEventChannel(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
	suppressHiddenCompletion bool,
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, ga.eventChannelBufferSize(invocation))
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(wrappedChan)
		fullRespEvent, ok := ga.forwardWrappedEvents(
			ctx,
			invocation,
			originalChan,
			suppressHiddenCompletion,
			func(evt *event.Event) error {
				return event.EmitEvent(ctx, wrappedChan, evt)
			},
		)
		if !ok {
			return
		}
		if ga.agentCallbacks == nil {
			return
		}

		// Collect error from the final response event so after-agent
		// callbacks can observe execution failures, matching LLMAgent
		// semantics.
		var agentErr error
		if fullRespEvent != nil && fullRespEvent.Response != nil &&
			fullRespEvent.Response.Error != nil {
			agentErr = fmt.Errorf(
				"%s: %s",
				fullRespEvent.Response.Error.Type,
				fullRespEvent.Response.Error.Message,
			)
		}

		// After all events are processed, run after agent callbacks
		result, err := ga.agentCallbacks.RunAfterAgent(ctx, &agent.AfterAgentArgs{
			Invocation:        invocation,
			Error:             agentErr,
			FullResponseEvent: fullRespEvent,
		})
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		var evt *event.Event
		if err != nil {
			// Send error event.
			evt = event.NewErrorEvent(
				invocation.InvocationID,
				invocation.AgentName,
				agent.ErrorTypeAgentCallbackError,
				err.Error(),
			)
		} else if result != nil && result.CustomResponse != nil {
			// Create an event from the custom response.
			evt = event.NewResponseEvent(
				invocation.InvocationID,
				invocation.AgentName,
				result.CustomResponse,
			)
		}

		agent.EmitEvent(ctx, invocation, wrappedChan, evt)
	}(runCtx)
	return wrappedChan
}

func (ga *GraphAgent) forwardWrappedEvents(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
	suppressHiddenCompletion bool,
	emit func(*event.Event) error,
) (*event.Event, bool) {
	var fullRespEvent *event.Event
	var emittedAssistantResponseIDs map[string]struct{}
	visibleCtx := graph.WithoutGraphCompletionCapture(ctx)
	for evt := range originalChan {
		outEvt := evt
		if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
			fullRespEvent = evt
		}
		shouldWrapCallbackCompletion := invocation != nil &&
			agent.IsGraphCompletionEventDisabled(invocation) &&
			graph.IsGraphCompletionEvent(evt)
		if shouldWrapCallbackCompletion {
			visibleEvent, callbackFullRespEvent, ok := graph.VisibleGraphCompletionEventsForForwardingWithAuthor(
				evt,
				emittedAssistantResponseIDs,
				invocation.AgentName,
			)
			if !ok {
				continue
			}
			if callbackFullRespEvent != nil &&
				callbackFullRespEvent.Response != nil &&
				!callbackFullRespEvent.Response.IsPartial {
				fullRespEvent = callbackFullRespEvent
			}
			if suppressHiddenCompletion &&
				graph.ShouldSuppressGraphCompletionEvent(
					visibleCtx,
					invocation,
					evt,
				) {
				outEvt = visibleEvent
			}
		}
		if err := emit(outEvt); err != nil {
			return nil, false
		}
		emittedAssistantResponseIDs = graph.RecordAssistantResponseID(
			emittedAssistantResponseIDs,
			outEvt,
		)
	}
	return fullRespEvent, true
}

// Executor returns the graph executor for direct access to checkpoint management.
func (ga *GraphAgent) Executor() *graph.Executor {
	return ga.executor
}
