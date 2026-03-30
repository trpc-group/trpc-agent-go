//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package a2aagent provides an agent that can communicate with remote A2A agents.
package a2aagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultStreamingChannelSize    = 1024
	defaultNonStreamingChannelSize = 10
	defaultUserIDHeader            = "X-User-ID"
)

// A2AAgent is an agent that communicates with a remote A2A agent via A2A protocol.
type A2AAgent struct {
	// options
	name                 string
	description          string
	agentCard            *server.AgentCard      // Agent card and resolution state
	agentURL             string                 // URL of the remote A2A agent
	eventConverter       A2AEventConverter      // Custom A2A event converters
	a2aMessageConverter  InvocationA2AConverter // Custom A2A message converters for requests
	extraA2AOptions      []client.Option        // Additional A2A client options
	streamingBufSize     int                    // Buffer size for streaming responses
	streamingRespHandler StreamingRespHandler   // Handler for streaming responses
	transferStateKey     []string               // Keys in session state to transfer to the A2A agent message by metadata
	buildMessageHook     BuildMessageHook       // Hook called after A2A message is built but before it is sent
	userIDHeader         string                 // HTTP header name to send UserID to A2A server
	enableStreaming      *bool                  // Explicitly set streaming mode; nil means use agent card capability

	a2aClient *client.A2AClient
}

// New creates a new A2AAgent.
func New(opts ...Option) (*A2AAgent, error) {
	agent := &A2AAgent{
		eventConverter:      &defaultA2AEventConverter{},
		a2aMessageConverter: &defaultEventA2AConverter{},
		streamingBufSize:    defaultStreamingChannelSize,
	}

	for _, opt := range opts {
		opt(agent)
	}

	var agentURL string
	if agent.agentCard != nil {
		agentURL = agent.agentCard.URL
	} else if agent.agentURL != "" {
		agentURL = agent.agentURL
	} else {
		log.Info("agent card or agent card url not set")
	}

	// Normalize the URL to ensure it has a proper scheme
	agentURL = ia2a.NormalizeURL(agentURL)

	// Create A2A client first
	a2aClient, err := client.NewA2AClient(agentURL, agent.extraA2AOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create A2A client for %s: %w", agentURL, err)
	}
	agent.a2aClient = a2aClient

	// If agent card is not set, fetch it using A2A client's GetAgentCard method
	if agent.agentCard == nil {
		agentCard, err := a2aClient.GetAgentCard(context.Background(), "")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch agent card from %s: %w", agentURL, err)
		}

		// Set name and description from agent card if not already set
		if agent.name == "" {
			agent.name = agentCard.Name
		}
		if agent.description == "" {
			agent.description = agentCard.Description
		}

		if agentCard.URL == "" {
			agentCard.URL = agentURL
		} else {
			// Normalize the agent card URL to ensure it has a proper scheme
			agentCard.URL = ia2a.NormalizeURL(agentCard.URL)
		}

		// Rebuild a2a client if URL changed
		if agentCard.URL != agentURL {
			a2aClient, err := client.NewA2AClient(agentCard.URL, agent.extraA2AOptions...)
			if err != nil {
				return nil, fmt.Errorf("failed to create A2A client for %s: %w", agentCard.URL, err)
			}
			agent.a2aClient = a2aClient
		}

		agent.agentCard = agentCard
	}

	return agent, nil
}

// sendErrorEvent sends an error event to the event channel.
func (r *A2AAgent) sendErrorEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocation *agent.Invocation,
	err error,
) {
	respErr := model.ResponseErrorFromError(err, model.ErrorTypeRunError)
	agent.EmitEvent(ctx, invocation, eventChan, event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			Object: model.ObjectTypeError,
			Error:  respErr,
		}),
	))
}

// validateA2ARequestOptions validates that all A2A request options are of the correct type
func (r *A2AAgent) validateA2ARequestOptions(invocation *agent.Invocation) error {
	if invocation.RunOptions.A2ARequestOptions == nil {
		return nil
	}

	for i, opt := range invocation.RunOptions.A2ARequestOptions {
		if _, ok := opt.(client.RequestOption); !ok {
			return fmt.Errorf("A2ARequestOptions[%d] is not a valid client.RequestOption, got type %T", i, opt)
		}
	}
	return nil
}

func (r *A2AAgent) setupInvocation(invocation *agent.Invocation) {
	invocation.Agent = r
	invocation.AgentName = r.name
}

// Run implements the Agent interface
func (r *A2AAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	var err error
	if invocation != nil {
		r.setupInvocation(invocation)
	}
	useStreaming := r.shouldUseStreaming(invocation)
	ctx, span, startedSpan := itrace.StartSpan(
		ctx,
		invocation,
		fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, r.name),
	)
	if startedSpan {
		itelemetry.TraceBeforeInvokeAgent(
			span,
			invocation,
			r.description,
			"",
			&model.GenerationConfig{Stream: useStreaming},
		)
	}
	tracker := itelemetry.NewInvokeAgentTracker(ctx, invocation, useStreaming, &err)
	if r.a2aClient == nil {
		if startedSpan {
			span.SetStatus(codes.Error, "A2A client is nil")
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, fmt.Errorf("A2A client is nil")
	}
	// Validate A2A request options early
	if err := r.validateA2ARequestOptions(invocation); err != nil {
		if startedSpan {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, err
	}
	var (
		eventChan <-chan *event.Event
	)
	if useStreaming {
		eventChan, err = r.runStreaming(ctx, invocation)
	} else {
		eventChan, err = r.runNonStreaming(ctx, invocation)
	}
	if err != nil {
		if startedSpan {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, err
	}
	return r.wrapEventChannelWithTelemetry(ctx, invocation, eventChan, span, tracker, startedSpan), nil
}

// shouldUseStreaming determines whether to use streaming protocol.
//
// Priority:
//  1. Per-run override (agent.WithStream / invocation.RunOptions.Stream)
//  2. Agent option (WithEnableStreaming)
//  3. Agent card capability
//  4. Default false
func (r *A2AAgent) shouldUseStreaming(invocation *agent.Invocation) bool {
	// Per-run override.
	if invocation != nil && invocation.RunOptions.Stream != nil {
		return *invocation.RunOptions.Stream
	}

	// If explicitly set via option, use that value
	if r.enableStreaming != nil {
		return *r.enableStreaming
	}

	// Otherwise check if agent card supports streaming
	if r.agentCard != nil && r.agentCard.Capabilities.Streaming != nil {
		return *r.agentCard.Capabilities.Streaming
	}

	// Default to non-streaming if capabilities are not specified
	return false
}

// buildA2AMessage constructs A2A message from session events.
// It assembles a middleware chain around the base converter:
//
//	transferStateKey → user hook → base converter
//
// transferStateKey is the outermost layer so it always runs even if
// the user hook short-circuits (skips calling next).
func (r *A2AAgent) buildA2AMessage(invocation *agent.Invocation, isStream bool) (*protocol.Message, error) {
	if r.a2aMessageConverter == nil {
		return nil, fmt.Errorf("a2a message converter not set")
	}

	// Base converter function.
	convertFn := r.a2aMessageConverter.ConvertToA2AMessage

	// User hook layer wraps the base converter.
	if r.buildMessageHook != nil {
		convertFn = r.buildMessageHook(convertFn)
	}

	// Built-in layer (outermost): transfer state keys into message metadata.
	// Placed after hook so it always runs regardless of hook behavior.
	if len(r.transferStateKey) > 0 {
		convertFn = r.wrapWithTransferState(convertFn)
	}

	message, err := convertFn(isStream, r.name, invocation)
	if err != nil {
		return nil, fmt.Errorf("A2A message conversion failed: %w", err)
	}
	if message == nil {
		return nil, errors.New("A2A message conversion returned nil message")
	}
	return message, nil
}

// wrapWithTransferState returns a middleware that injects transferStateKey values
// from RuntimeState into the message metadata after calling next.
func (r *A2AAgent) wrapWithTransferState(next ConvertToA2AMessageFunc) ConvertToA2AMessageFunc {
	return func(isStream bool, agentName string, invocation *agent.Invocation) (*protocol.Message, error) {
		message, err := next(isStream, agentName, invocation)
		if err != nil {
			return nil, err
		}
		if message == nil {
			return nil, nil
		}
		if invocation.RunOptions.RuntimeState == nil {
			return message, nil
		}
		if message.Metadata == nil {
			message.Metadata = make(map[string]any)
		}
		for _, key := range r.transferStateKey {
			if value, ok := invocation.RunOptions.RuntimeState[key]; ok {
				message.Metadata[key] = value
			}
		}
		return message, nil
	}
}

// runStreaming handles streaming A2A communication
func (r *A2AAgent) runStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if r.eventConverter == nil {
		return nil, fmt.Errorf("event converter not set")
	}
	eventChan := make(chan *event.Event, r.streamingBufSize)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(eventChan)
		r.executeStreaming(ctx, invocation, eventChan)
	}(runCtx)
	return eventChan, nil
}

// executeStreaming executes the streaming A2A communication workflow.
func (r *A2AAgent) executeStreaming(ctx context.Context, invocation *agent.Invocation, eventChan chan<- *event.Event) {
	a2aMessage, err := r.buildA2AMessage(invocation, true)
	if err != nil {
		r.sendErrorEvent(
			ctx,
			eventChan,
			invocation,
			fmt.Errorf("failed to construct A2A message: %w", err),
		)
		return
	}

	requestOpts := r.buildRequestOptions(ctx, invocation)
	streamChan, err := r.a2aClient.StreamMessage(ctx, protocol.SendMessageParams{Message: *a2aMessage}, requestOpts...)
	if err != nil {
		r.sendErrorEvent(
			ctx,
			eventChan,
			invocation,
			fmt.Errorf(
				"A2A streaming request failed to %s: %w",
				r.agentCard.URL,
				err,
			),
		)
		return
	}

	streamResult := r.processStreamingEvents(
		ctx,
		invocation,
		eventChan,
		streamChan,
	)
	if streamResult.terminalError != nil {
		return
	}
	r.emitFinalEvent(
		ctx,
		invocation,
		eventChan,
		streamResult.responseID,
		streamResult.aggregatedContent,
	)
}

// buildRequestOptions constructs A2A request options from invocation.
func (r *A2AAgent) buildRequestOptions(ctx context.Context, invocation *agent.Invocation) []client.RequestOption {
	var requestOpts []client.RequestOption
	if invocation.RunOptions.A2ARequestOptions != nil {
		for _, opt := range invocation.RunOptions.A2ARequestOptions {
			requestOpts = append(requestOpts, opt.(client.RequestOption))
		}
	}
	// Add UserID header if session has UserID
	if invocation.Session != nil && invocation.Session.UserID != "" {
		userIDHeader := r.userIDHeader
		if userIDHeader == "" {
			userIDHeader = defaultUserIDHeader
		}
		requestOpts = append(requestOpts, client.WithRequestHeader(userIDHeader, invocation.Session.UserID))
	}
	// Propagate trace context via HTTP headers (W3C Trace Context).
	traceHeaders := extractTraceHeaders(ctx)
	for k, v := range traceHeaders {
		requestOpts = append(requestOpts, client.WithRequestHeader(k, v))
	}
	return requestOpts
}

type streamingEventResult struct {
	responseID        string
	aggregatedContent string
	terminalError     *model.ResponseError
}

// processStreamingEvents processes streaming events and aggregates content.
// Returns the response ID, aggregated content, and terminal error state.
func (r *A2AAgent) processStreamingEvents(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	streamChan <-chan protocol.StreamingMessageEvent,
) streamingEventResult {
	var result streamingEventResult
	var contentBuilder strings.Builder

	for streamEvent := range streamChan {
		if err := agent.CheckContextCancelled(ctx); err != nil {
			result.aggregatedContent = contentBuilder.String()
			return result
		}

		events, err := r.eventConverter.ConvertStreamingToEvents(streamEvent, r.name, invocation)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("custom event converter failed: %w", err),
			)
			result.aggregatedContent = contentBuilder.String()
			return result
		}

		for _, evt := range events {
			if evt == nil {
				continue
			}
			result.responseID, _ = r.aggregateEventContent(
				ctx,
				invocation,
				eventChan,
				evt,
				result.responseID,
				&contentBuilder,
			)
			agent.EmitEvent(ctx, invocation, eventChan, evt)
			if evt.Response != nil &&
				evt.Response.Error != nil &&
				evt.Response.Done {
				result.aggregatedContent = contentBuilder.String()
				result.terminalError = evt.Response.Error
				return result
			}
		}
	}
	result.aggregatedContent = contentBuilder.String()
	return result
}

// aggregateEventContent aggregates content from event delta.
// Returns updated responseID and whether an error occurred.
func (r *A2AAgent) aggregateEventContent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	evt *event.Event,
	responseID string,
	contentBuilder *strings.Builder,
) (string, bool) {
	if evt.Response == nil || evt.Response.Error != nil {
		return responseID, false
	}
	if len(evt.Response.Choices) == 0 {
		return responseID, false
	}

	if evt.Response.ID != "" {
		responseID = evt.Response.ID
	}

	if r.streamingRespHandler != nil {
		content, err := r.streamingRespHandler(evt.Response)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("streaming resp handler failed: %w", err),
			)
			return responseID, true
		}
		if content != "" {
			contentBuilder.WriteString(content)
		}
	} else if evt.Response.Choices[0].Delta.Content != "" {
		contentBuilder.WriteString(evt.Response.Choices[0].Delta.Content)
	}
	return responseID, false
}

// emitFinalEvent emits the final completion event.
func (r *A2AAgent) emitFinalEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	responseID string,
	aggregatedContent string,
) {
	agent.EmitEvent(ctx, invocation, eventChan, event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			ID:        responseID,
			Object:    model.ObjectTypeChatCompletion,
			Done:      true,
			IsPartial: false,
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: aggregatedContent,
				},
			}},
		}),
	))
}

// runNonStreaming handles non-streaming A2A communication
func (r *A2AAgent) runNonStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, defaultNonStreamingChannelSize)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(eventChan)

		// Construct A2A message from session
		a2aMessage, err := r.buildA2AMessage(invocation, false)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("failed to construct A2A message: %w", err),
			)
			return
		}

		params := protocol.SendMessageParams{
			Message: *a2aMessage,
		}
		requestOpts := r.buildRequestOptions(ctx, invocation)
		result, err := r.a2aClient.SendMessage(ctx, params, requestOpts...)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf(
					"A2A request failed to %s: %w",
					r.agentCard.URL,
					err,
				),
			)
			return
		}

		// Convert A2A response to multiple events
		msgResult := protocol.MessageResult{Result: result.Result}
		events, err := r.eventConverter.ConvertToEvents(msgResult, r.name, invocation)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("custom event converter failed: %w", err),
			)
			return
		}

		// Emit all events
		for _, evt := range events {
			agent.EmitEvent(ctx, invocation, eventChan, evt)
		}
	}(runCtx)
	return eventChan, nil
}

func (r *A2AAgent) wrapEventChannelWithTelemetry(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
	span sdktrace.Span,
	tracker *itelemetry.InvokeAgentTracker,
	startedSpan bool,
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, cap(originalChan))
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		var fullRespEvent *event.Event
		var responseErrorType string
		tokenUsage := &itelemetry.TokenUsage{}
		defer func() {
			if startedSpan && fullRespEvent != nil {
				log.DebugContext(ctx, "fullRespEvent is not ni")
				itelemetry.TraceAfterInvokeAgent(span, fullRespEvent, tokenUsage, tracker.FirstTokenTimeDuration())
			}
			tracker.SetResponseErrorType(responseErrorType)
			tracker.RecordMetrics()()
			if startedSpan {
				span.End()
			}
			close(wrappedChan)
		}()
		for evt := range originalChan {
			if evt != nil && evt.Response != nil {
				tracker.TrackResponse(evt.Response)
				if !evt.Response.IsPartial {
					if evt.Response.Usage != nil {
						tokenUsage.PromptTokens += evt.Response.Usage.PromptTokens
						tokenUsage.CompletionTokens += evt.Response.Usage.CompletionTokens
						tokenUsage.TotalTokens += evt.Response.Usage.TotalTokens
					}
					fullRespEvent = evt
				}
			}
			if evt != nil && evt.Error != nil {
				responseErrorType = evt.Error.Type
			}
			if err := event.EmitEvent(ctx, wrappedChan, evt); err != nil {
				return
			}
		}
	}(runCtx)

	return wrappedChan
}

// Tools implements the Agent interface
func (r *A2AAgent) Tools() []tool.Tool {
	// Remote A2A agents don't expose tools directly
	// Tools are handled by the remote agent
	return []tool.Tool{}
}

// Info implements the Agent interface
func (r *A2AAgent) Info() agent.Info {
	return agent.Info{
		Name:        r.name,
		Description: r.description,
	}
}

// SubAgents implements the Agent interface
func (r *A2AAgent) SubAgents() []agent.Agent {
	// Remote A2A agents don't have sub-agents in the local context
	return []agent.Agent{}
}

// FindSubAgent implements the Agent interface
func (r *A2AAgent) FindSubAgent(name string) agent.Agent {
	// Remote A2A agents don't have sub-agents in the local context
	return nil
}

// GetAgentCard returns the resolved agent card
func (r *A2AAgent) GetAgentCard() *server.AgentCard {
	return r.agentCard
}

// extractTraceHeaders extracts W3C Trace Context headers from ctx using the
// globally registered OpenTelemetry propagator. Returns a map of header
// key-value pairs (e.g. "traceparent" -> "00-..."). Returns nil when ctx
// carries no valid span context.
func extractTraceHeaders(ctx context.Context) map[string]string {
	propagator := otel.GetTextMapPropagator()
	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	return carrier
}
