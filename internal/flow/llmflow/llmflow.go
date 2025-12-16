//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llmflow provides an LLM-based flow implementation.
package llmflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// Timeout for event completion signaling.
	eventCompletionTimeout = 5 * time.Second

	// stateKeyToolsSnapshot is the invocation state key used to cache the
	// final tool list for a single Invocation. This ensures that the tool
	// set (including ToolSet-based tools and filters) stays stable for the
	// entire lifetime of an Invocation, even when underlying ToolSets are
	// dynamic.
	stateKeyToolsSnapshot = "llmflow:tools_snapshot"
)

// Options contains configuration options for creating a Flow.
type Options struct {
	ChannelBufferSize int // Buffer size for event channels (default: 256)
	ModelCallbacks    *model.Callbacks
}

// Flow provides the basic flow implementation.
type Flow struct {
	requestProcessors  []flow.RequestProcessor
	responseProcessors []flow.ResponseProcessor
	channelBufferSize  int
	modelCallbacks     *model.Callbacks
}

// New creates a new basic flow instance with the provided processors.
// Processors are immutable after creation.
func New(
	requestProcessors []flow.RequestProcessor,
	responseProcessors []flow.ResponseProcessor,
	opts Options,
) *Flow {
	return &Flow{
		requestProcessors:  requestProcessors,
		responseProcessors: responseProcessors,
		channelBufferSize:  opts.ChannelBufferSize,
		modelCallbacks:     opts.ModelCallbacks,
	}
}

// Run executes the flow in a loop until completion.
func (f *Flow) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, f.channelBufferSize) // Configurable buffered channel for events.

	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(eventChan)

		// Optionally resume from pending tool calls before starting a new
		// LLM cycle. This covers scenarios where the previous run stopped
		// after an assistant tool_call response but before tools executed.
		f.maybeResumePendingToolCalls(ctx, invocation, eventChan)

		for {
			// emit start event and wait for completion notice.
			if err := f.emitStartEventAndWait(ctx, invocation, eventChan); err != nil {
				return
			}

			// Run one step (one LLM call cycle).
			lastEvent, err := f.runOneStep(ctx, invocation, eventChan)
			if err != nil {
				// Treat context cancellation as graceful termination (common in streaming
				// pipelines where the client closes the stream after final event).
				if errors.Is(err, context.Canceled) {
					log.DebugfContext(
						ctx,
						"Flow context canceled for agent %s; exiting "+
							"without error",
						invocation.AgentName,
					)
					return
				}
				var errorEvent *event.Event
				if _, ok := agent.AsStopError(err); ok {
					errorEvent = event.NewErrorEvent(
						invocation.InvocationID,
						invocation.AgentName,
						agent.ErrorTypeStopAgentError,
						err.Error(),
					)
					log.ErrorfContext(
						ctx,
						"Flow step stopped for agent %s: %v",
						invocation.AgentName,
						err,
					)
				} else {
					// Send error event through channel instead of just logging.
					errorEvent = event.NewErrorEvent(
						invocation.InvocationID,
						invocation.AgentName,
						model.ErrorTypeFlowError,
						err.Error(),
					)
					log.ErrorfContext(
						ctx,
						"Flow step failed for agent %s: %v",
						invocation.AgentName,
						err,
					)
				}

				agent.EmitEvent(ctx, invocation, eventChan, errorEvent)
				return
			}

			// Exit conditions.
			// If no events were produced in this step, treat as terminal to avoid busy loop.
			// Also break when EndInvocation is set or a final response is observed.
			if lastEvent == nil || invocation.EndInvocation || lastEvent.IsFinalResponse() {
				break
			}
		}
	}(runCtx)

	return eventChan, nil
}

// maybeResumePendingToolCalls inspects the latest session events and, when
// RunOptions.Resume is enabled, executes any pending tool calls before the
// next LLM request. A pending tool call is defined as the latest persisted
// event being an assistant response that contains tool calls but no tool
// results after it.
func (f *Flow) maybeResumePendingToolCalls(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	if invocation == nil || !invocation.RunOptions.Resume {
		return
	}
	if invocation.Session == nil {
		return
	}

	invocation.Session.EventMu.RLock()
	events := invocation.Session.Events
	var lastResp *model.Response
	if len(events) > 0 {
		last := events[len(events)-1]
		if last.Response != nil && !last.IsPartial &&
			last.IsValidContent() && last.Response.IsToolCallResponse() {
			lastResp = last.Response
		}
	}
	invocation.Session.EventMu.RUnlock()

	if lastResp == nil {
		return
	}

	req := &model.Request{
		Tools: make(map[string]tool.Tool),
	}
	for _, t := range f.getFilteredTools(ctx, invocation) {
		req.Tools[t.Declaration().Name] = t
	}

	for _, rp := range f.responseProcessors {
		if toolRP, ok := rp.(*processor.FunctionCallResponseProcessor); ok {
			toolRP.ProcessResponse(ctx, invocation, req, lastResp, eventChan)
			break
		}
	}
}

func (f *Flow) emitStartEventAndWait(ctx context.Context, invocation *agent.Invocation,
	eventChan chan<- *event.Event) error {
	invocationID, agentName := "", ""
	if invocation != nil {
		invocationID = invocation.InvocationID
		agentName = invocation.AgentName
	}
	startEvent := event.New(invocationID, agentName)
	startEvent.RequiresCompletion = true
	agent.EmitEvent(ctx, invocation, eventChan, startEvent)

	// Wait for completion notice.
	// Ensure that the events of the previous agent or the previous step have been synchronized to the session.
	completionID := agent.GetAppendEventNoticeKey(startEvent.ID)
	err := invocation.AddNoticeChannelAndWait(ctx, completionID, eventCompletionTimeout)
	if errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// runOneStep executes one step of the flow (one LLM call cycle).
// Returns the last event generated, or nil if no events.
func (f *Flow) runOneStep(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	var lastEvent *event.Event

	// Initialize empty LLM request.
	llmRequest := &model.Request{
		Tools: make(map[string]tool.Tool), // Initialize tools map
	}

	// 1. Preprocess (prepare request).
	f.preprocess(ctx, invocation, llmRequest, eventChan)

	if invocation.EndInvocation {
		return lastEvent, nil
	}
	var span oteltrace.Span
	var modelName string
	if invocation.Model != nil {
		modelName = invocation.Model.Info().Name
	}
	_, span = trace.Tracer.Start(ctx, itelemetry.NewChatSpanName(modelName))
	defer span.End()

	// 2. Call LLM (get response channel).
	responseChan, err := f.callLLM(ctx, invocation, llmRequest)
	if err != nil {
		return nil, err
	}
	// 3. Process streaming responses.
	return f.processStreamingResponses(ctx, invocation, llmRequest, responseChan, eventChan, span)
}

// processStreamingResponses handles the streaming response processing logic.
func (f *Flow) processStreamingResponses(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	responseChan <-chan *model.Response,
	eventChan chan<- *event.Event,
	span oteltrace.Span,
) (lastEvent *event.Event, err error) {
	// Get or create timing info from invocation (only record first LLM call)
	timingInfo := invocation.GetOrCreateTimingInfo()

	// Create telemetry tracker and defer metrics recording
	tracker := itelemetry.NewChatMetricsTracker(ctx, invocation, llmRequest, timingInfo, &err)
	defer tracker.RecordMetrics()()

	for response := range responseChan {
		// Track response for telemetry (token usage and timing info)
		tracker.TrackResponse(response)

		// Attach timing info to response
		if response.Usage == nil {
			response.Usage = &model.Usage{}
		}
		// set timing info to response
		response.Usage.TimingInfo = timingInfo

		// Handle after model callbacks.
		customResp, err := f.handleAfterModelCallbacks(ctx, invocation, llmRequest, response, eventChan)
		if err != nil {
			return lastEvent, err
		}
		if customResp != nil {
			response = customResp
		}

		// 4. Create and send LLM response using the clean constructor.
		llmResponseEvent := f.createLLMResponseEvent(invocation, response, llmRequest)
		agent.EmitEvent(ctx, invocation, eventChan, llmResponseEvent)
		lastEvent = llmResponseEvent
		tracker.SetLastEvent(lastEvent)
		// 5. Check context cancellation.
		if err = agent.CheckContextCancelled(ctx); err != nil {
			return lastEvent, err
		}

		// 6. Postprocess response.
		f.postprocess(ctx, invocation, llmRequest, response, eventChan)
		if err := agent.CheckContextCancelled(ctx); err != nil {
			return lastEvent, err
		}

		itelemetry.TraceChat(span, invocation, llmRequest, response, llmResponseEvent.ID, tracker.FirstTokenTimeDuration())

	}

	return lastEvent, nil
}

// handleAfterModelCallbacks processes after model callbacks.
func (f *Flow) handleAfterModelCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	response *model.Response,
	eventChan chan<- *event.Event,
) (*model.Response, error) {
	ctx, customResp, err := f.runAfterModelCallbacks(ctx, llmRequest, response)
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return nil, err
		}

		log.ErrorfContext(
			ctx,
			"After model callback failed for agent %s: %v",
			invocation.AgentName,
			err,
		)
		agent.EmitEvent(ctx, invocation, eventChan, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			err.Error(),
		))
		return nil, err
	}
	return customResp, nil
}

// createLLMResponseEvent creates a new LLM response event.
func (f *Flow) createLLMResponseEvent(invocation *agent.Invocation, response *model.Response, llmRequest *model.Request) *event.Event {
	llmResponseEvent := event.New(invocation.InvocationID, invocation.AgentName, event.WithResponse(response))
	if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
		llmResponseEvent.LongRunningToolIDs = collectLongRunningToolIDs(response.Choices[0].Message.ToolCalls, llmRequest.Tools)
	}
	return llmResponseEvent
}

func collectLongRunningToolIDs(ToolCalls []model.ToolCall, tools map[string]tool.Tool) map[string]struct{} {
	longRunningToolIDs := make(map[string]struct{})
	for _, toolCall := range ToolCalls {
		t, ok := tools[toolCall.Function.Name]
		if !ok {
			continue
		}
		caller, ok := t.(function.LongRunner)
		if !ok {
			continue
		}
		if caller.LongRunning() {
			longRunningToolIDs[toolCall.ID] = struct{}{}
		}
	}
	return longRunningToolIDs
}

func (f *Flow) runAfterModelCallbacks(
	ctx context.Context,
	req *model.Request,
	response *model.Response,
) (context.Context, *model.Response, error) {
	if f.modelCallbacks == nil {
		return ctx, response, nil
	}

	// Convert response.Error to Go error for callback.
	var modelErr error
	if response != nil && response.Error != nil {
		modelErr = fmt.Errorf("%s: %s", response.Error.Type, response.Error.Message)
	}

	result, err := f.modelCallbacks.RunAfterModel(ctx, &model.AfterModelArgs{
		Request:  req,
		Response: response,
		Error:    modelErr,
	})
	if err != nil {
		return ctx, nil, err
	}
	// Use the context from result if provided for subsequent operations.
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		return ctx, result.CustomResponse, nil
	}
	return ctx, response, nil
}

// preprocess handles pre-LLM call preparation using request processors.
func (f *Flow) preprocess(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	eventChan chan<- *event.Event,
) {
	// Run request processors - they send events directly to the channel.
	for _, processor := range f.requestProcessors {
		processor.ProcessRequest(ctx, invocation, llmRequest, eventChan)
	}

	// Add tools to the request with optional filtering.
	if invocation.Agent != nil {
		tools := f.getFilteredTools(ctx, invocation)
		for _, t := range tools {
			llmRequest.Tools[t.Declaration().Name] = t
		}
	}
}

// UserToolsProvider is an optional interface that agents can implement to expose
// which tools were explicitly registered by the user (WithTools, WithToolSets)
// vs framework-added tools (Knowledge, SubAgents).
//
// User tools are subject to filtering via WithToolFilter.
// Framework tools are never filtered and always available to the agent.
type UserToolsProvider interface {
	UserTools() []tool.Tool
}

// ToolFilterProvider is an optional interface that agents can implement to provide
type ToolFilterProvider interface {
	FilterTools(ctx context.Context) []tool.Tool
}

// getFilteredTools returns the list of tools for this invocation after applying the filter.
//
// User tools (can be filtered):
//   - Tools registered via WithTools
//   - Tools registered via WithToolSets
//
// Framework tools (never filtered):
//   - transfer_to_agent (auto-added when SubAgents are configured)
//   - knowledge_search / agentic_knowledge_search (auto-added when Knowledge is configured)
//
// This method is called during the preprocess stage, before sending the request to the model.
func (f *Flow) getFilteredTools(ctx context.Context, invocation *agent.Invocation) []tool.Tool {
	if invocation == nil || invocation.Agent == nil {
		return nil
	}

	if cached, ok := agent.GetStateValue[[]tool.Tool](
		invocation,
		stateKeyToolsSnapshot,
	); ok && cached != nil {
		return cached
	}

	// Get all tools from the agent.
	allTools := invocation.Agent.Tools()
	if provider, ok := invocation.Agent.(ToolFilterProvider); ok {
		allTools = provider.FilterTools(ctx)
	}

	// If no filter is specified, return all tools for this invocation.
	if invocation.RunOptions.ToolFilter == nil {
		invocation.SetState(stateKeyToolsSnapshot, allTools)
		return allTools
	}

	// Get user tools (if the agent supports it).
	// User tools are those explicitly registered via WithTools and WithToolSets.
	// Framework tools (Knowledge, SubAgents) are never filtered.
	var userToolNames map[string]bool
	hasUserToolTracking := false
	if provider, ok := invocation.Agent.(UserToolsProvider); ok {
		userTools := provider.UserTools()
		hasUserToolTracking = true
		// Build a map for fast lookup.
		userToolNames = make(map[string]bool, len(userTools))
		for _, t := range userTools {
			userToolNames[t.Declaration().Name] = true
		}
	}

	// Apply the filter function to each tool.
	// Framework tools are never filtered.
	filtered := make([]tool.Tool, 0, len(allTools))
	for _, t := range allTools {
		toolName := t.Declaration().Name

		// Determine if this is a user tool or framework tool.
		isUserTool := !hasUserToolTracking || userToolNames[toolName]

		// Framework tools are always included (never filtered).
		if !isUserTool {
			filtered = append(filtered, t)
			continue
		}

		// User tool: apply the filter function.
		if invocation.RunOptions.ToolFilter(ctx, t) {
			filtered = append(filtered, t)
		}
	}

	invocation.SetState(stateKeyToolsSnapshot, filtered)

	return filtered
}

// callLLM performs the actual LLM call using core/model.
func (f *Flow) callLLM(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
) (<-chan *model.Response, error) {
	if invocation.Model == nil {
		return nil, errors.New("no model available for LLM call")
	}

	log.DebugfContext(
		ctx,
		"Calling LLM for agent %s",
		invocation.AgentName,
	)

	// Enforce optional per-invocation LLM call limit. When the limit is not
	// configured (<= 0), this is a no-op and preserves existing behavior.
	if err := invocation.IncLLMCallCount(); err != nil {
		log.Errorf("LLM call limit exceeded for agent %s: %v", invocation.AgentName, err)
		return nil, err
	}

	// Run before model callbacks if they exist.
	if f.modelCallbacks != nil {
		result, err := f.modelCallbacks.RunBeforeModel(ctx, &model.BeforeModelArgs{
			Request: llmRequest,
		})
		if err != nil {
			log.ErrorfContext(
				ctx,
				"Before model callback failed for agent %s: %v",
				invocation.AgentName,
				err,
			)
			return nil, err
		}
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		if result != nil && result.CustomResponse != nil {
			// Create a channel that returns the custom response and then closes.
			responseChan := make(chan *model.Response, 1)
			responseChan <- result.CustomResponse
			close(responseChan)
			return responseChan, nil
		}
	}

	// Call the model.
	responseChan, err := invocation.Model.GenerateContent(ctx, llmRequest)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"LLM call failed for agent %s: %v",
			invocation.AgentName,
			err,
		)
		return nil, err
	}

	return responseChan, nil
}

// postprocess handles post-LLM call processing using response processors.
func (f *Flow) postprocess(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	llmResponse *model.Response,
	eventChan chan<- *event.Event,
) {
	if llmResponse == nil {
		return
	}

	// Run response processors - they send events directly to the channel.
	for _, processor := range f.responseProcessors {
		processor.ProcessResponse(ctx, invocation, llmRequest, llmResponse, eventChan)
	}
}

// WaitEventTimeout returns the remaining time until the context deadline.
// If the context has no deadline, it returns the default event completion timeout.
func WaitEventTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return eventCompletionTimeout
}
