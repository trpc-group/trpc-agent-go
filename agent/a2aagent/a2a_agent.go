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
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
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

// sendErrorEvent sends an error event to the event channel
func (r *A2AAgent) sendErrorEvent(ctx context.Context, eventChan chan<- *event.Event,
	invocation *agent.Invocation, errorMessage string) {
	agent.EmitEvent(ctx, invocation, eventChan, event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			Error: &model.ResponseError{
				Message: errorMessage,
			},
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

// Run implements the Agent interface
func (r *A2AAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ctx, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, r.name))
	itelemetry.TraceBeforeInvokeAgent(span, invocation, r.description, "", nil)

	if r.a2aClient == nil {
		span.SetStatus(codes.Error, "A2A client is nil")
		span.SetAttributes(attribute.String(itelemetry.KeyErrorType, itelemetry.ValueDefaultErrorType))
		span.End()
		return nil, fmt.Errorf("A2A client is nil")
	}

	// Validate A2A request options early
	if err := r.validateA2ARequestOptions(invocation); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(itelemetry.KeyErrorType, itelemetry.ValueDefaultErrorType))
		span.End()
		return nil, err
	}

	useStreaming := r.shouldUseStreaming()
	var (
		eventChan <-chan *event.Event
		err       error
	)
	if useStreaming {
		eventChan, err = r.runStreaming(ctx, invocation)
	} else {
		eventChan, err = r.runNonStreaming(ctx, invocation)
	}
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(itelemetry.KeyErrorType, itelemetry.ValueDefaultErrorType))
		span.End()
		return nil, err
	}

	return r.wrapEventChannelWithTelemetry(ctx, invocation, eventChan, span), nil
}

// shouldUseStreaming determines whether to use streaming protocol
func (r *A2AAgent) shouldUseStreaming() bool {
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

// buildA2AMessage constructs A2A message from session events
func (r *A2AAgent) buildA2AMessage(invocation *agent.Invocation, isStream bool) (*protocol.Message, error) {
	if r.a2aMessageConverter == nil {
		return nil, fmt.Errorf("a2a message converter not set")
	}
	message, err := r.a2aMessageConverter.ConvertToA2AMessage(isStream, r.name, invocation)
	if err != nil || message == nil {
		return nil, fmt.Errorf("custom A2A converter failed, msg:%v, err:%w", message, err)
	}

	if len(r.transferStateKey) > 0 {
		if message.Metadata == nil {
			message.Metadata = make(map[string]any)
		}
		for _, key := range r.transferStateKey {
			if value, ok := invocation.RunOptions.RuntimeState[key]; ok {
				message.Metadata[key] = value
			}
		}
	}
	return message, nil
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
		r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("failed to construct A2A message: %v", err))
		return
	}

	requestOpts := r.buildRequestOptions(invocation)
	streamChan, err := r.a2aClient.StreamMessage(ctx, protocol.SendMessageParams{Message: *a2aMessage}, requestOpts...)
	if err != nil {
		r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("A2A streaming request failed to %s: %v", r.agentCard.URL, err))
		return
	}

	responseID, aggregatedContent := r.processStreamingEvents(ctx, invocation, eventChan, streamChan)
	r.emitFinalEvent(ctx, invocation, eventChan, responseID, aggregatedContent)
}

// buildRequestOptions constructs A2A request options from invocation.
func (r *A2AAgent) buildRequestOptions(invocation *agent.Invocation) []client.RequestOption {
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
	return requestOpts
}

// processStreamingEvents processes streaming events and aggregates content.
// Returns the response ID and aggregated content.
func (r *A2AAgent) processStreamingEvents(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	streamChan <-chan protocol.StreamingMessageEvent,
) (responseID string, aggregatedContent string) {
	var contentBuilder strings.Builder

	for streamEvent := range streamChan {
		if err := agent.CheckContextCancelled(ctx); err != nil {
			return responseID, contentBuilder.String()
		}

		events, err := r.eventConverter.ConvertStreamingToEvents(streamEvent, r.name, invocation)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("custom event converter failed: %v", err))
			return responseID, contentBuilder.String()
		}

		for _, evt := range events {
			if evt == nil {
				continue
			}
			responseID, _ = r.aggregateEventContent(ctx, invocation, eventChan, evt, responseID, &contentBuilder)
			agent.EmitEvent(ctx, invocation, eventChan, evt)
		}
	}
	return responseID, contentBuilder.String()
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
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return responseID, false
	}

	if evt.Response.ID != "" {
		responseID = evt.Response.ID
	}

	if r.streamingRespHandler != nil {
		content, err := r.streamingRespHandler(evt.Response)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("streaming resp handler failed: %v", err))
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
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("failed to construct A2A message: %v", err))
			return
		}

		params := protocol.SendMessageParams{
			Message: *a2aMessage,
		}
		// Extract A2A request options from invocation
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
		result, err := r.a2aClient.SendMessage(ctx, params, requestOpts...)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("A2A request failed to %s: %v", r.agentCard.URL, err))
			return
		}

		// Convert A2A response to multiple events
		msgResult := protocol.MessageResult{Result: result.Result}
		events, err := r.eventConverter.ConvertToEvents(msgResult, r.name, invocation)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("custom event converter failed: %v", err))
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
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, cap(originalChan))

	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		var fullRespEvent *event.Event
		tokenUsage := &itelemetry.TokenUsage{}
		defer func() {
			if fullRespEvent != nil {
				log.Debug("fullRespEvent is not ni")
				itelemetry.TraceAfterInvokeAgent(span, fullRespEvent, tokenUsage)
			}
			span.End()
			close(wrappedChan)
		}()

		for evt := range originalChan {
			if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
				if evt.Response.Usage != nil {
					tokenUsage.PromptTokens += evt.Response.Usage.PromptTokens
					tokenUsage.CompletionTokens += evt.Response.Usage.CompletionTokens
					tokenUsage.TotalTokens += evt.Response.Usage.TotalTokens
				}
				fullRespEvent = evt
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
