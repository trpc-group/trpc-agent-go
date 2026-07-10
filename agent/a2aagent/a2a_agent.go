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
	"container/list"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
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
	defaultStreamingChannelSize     = 1024
	defaultNonStreamingChannelSize  = 10
	defaultAnonymousClientCacheSize = 1024
	defaultUserIDHeader             = "X-User-ID"
)

// A2AAgent is an agent that communicates with a remote A2A agent via A2A protocol.
type A2AAgent struct {
	// options
	name                 string
	description          string
	agentCard            *server.AgentCard      // Agent card and resolution state
	agentURL             string                 // URL of the remote A2A agent
	eventConverter       A2AEventConverter      // Custom A2A event converters
	dataPartMappers      []A2ADataPartMapper    // Lightweight inbound DataPart mappers for default converter
	a2aMessageConverter  InvocationA2AConverter // Custom A2A message converters for requests
	extraA2AOptions      []client.Option        // Additional A2A client options
	streamingBufSize     int                    // Buffer size for streaming responses
	streamingRespHandler StreamingRespHandler   // Handler for streaming responses
	transferStateKey     []string               // Keys in session state to transfer to the A2A agent message by metadata
	buildMessageHook     BuildMessageHook       // Hook called after A2A message is built but before it is sent
	userIDHeader         string                 // HTTP header name to send UserID to A2A server
	enableStreaming      *bool                  // Explicitly set streaming mode; nil means use agent card capability

	a2aClient    *client.A2AClient
	a2aClientURL string

	anonymousClientsMu       sync.Mutex
	anonymousClients         map[anonymousClientScope]*list.Element
	anonymousClientsLRU      *list.List
	anonymousClientCacheSize int
}

type anonymousClientScope struct {
	appName           string
	sessionID         string
	createdAtUnixNano int64
}

type anonymousClientCacheEntry struct {
	scope  anonymousClientScope
	client *client.A2AClient
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

	if len(agent.dataPartMappers) > 0 {
		if converter, ok := agent.eventConverter.(*defaultA2AEventConverter); ok {
			for _, mapper := range agent.dataPartMappers {
				if mapper == nil {
					continue
				}
				converter.dataPartMappers = append(converter.dataPartMappers, mapper)
			}
		} else {
			log.Warn(
				"WithA2ADataPartMapper is ignored because WithCustomEventConverter provided a custom converter",
			)
		}
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
	agent.a2aClientURL = agentURL

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
			agent.a2aClientURL = agentCard.URL
		}

		agent.agentCard = agentCard
	}

	return agent, nil
}

var newCookieJar = cookiejar.New

func (r *A2AAgent) clientForInvocation(
	invocation *agent.Invocation,
) (*client.A2AClient, error) {
	if !needsAnonymousClient(invocation) || r.a2aClientURL == "" {
		return r.a2aClient, nil
	}

	scope, ok := anonymousScopeFromInvocation(invocation)
	if !ok {
		return r.newAnonymousClient()
	}

	r.anonymousClientsMu.Lock()
	defer r.anonymousClientsMu.Unlock()
	if element := r.anonymousClients[scope]; element != nil {
		r.anonymousClientsLRU.MoveToFront(element)
		return element.Value.(*anonymousClientCacheEntry).client, nil
	}
	a2aClient, err := r.newAnonymousClient()
	if err != nil {
		return nil, err
	}
	if r.anonymousClients == nil {
		r.anonymousClients = make(map[anonymousClientScope]*list.Element)
		r.anonymousClientsLRU = list.New()
	}
	element := r.anonymousClientsLRU.PushFront(&anonymousClientCacheEntry{
		scope:  scope,
		client: a2aClient,
	})
	r.anonymousClients[scope] = element

	cacheSize := r.anonymousClientCacheSize
	if cacheSize <= 0 {
		cacheSize = defaultAnonymousClientCacheSize
	}
	if len(r.anonymousClients) > cacheSize {
		oldest := r.anonymousClientsLRU.Back()
		r.anonymousClientsLRU.Remove(oldest)
		delete(
			r.anonymousClients,
			oldest.Value.(*anonymousClientCacheEntry).scope,
		)
	}
	return a2aClient, nil
}

func needsAnonymousClient(invocation *agent.Invocation) bool {
	return invocation == nil ||
		invocation.Session == nil ||
		strings.TrimSpace(invocation.Session.UserID) == ""
}

func anonymousScopeFromInvocation(
	invocation *agent.Invocation,
) (anonymousClientScope, bool) {
	if invocation == nil ||
		invocation.Session == nil {
		return anonymousClientScope{}, false
	}
	appName := strings.TrimSpace(invocation.Session.AppName)
	sessionID := strings.TrimSpace(invocation.Session.ID)
	if appName == "" ||
		sessionID == "" ||
		invocation.Session.CreatedAt.IsZero() {
		return anonymousClientScope{}, false
	}
	return anonymousClientScope{
		appName:           appName,
		sessionID:         sessionID,
		createdAtUnixNano: invocation.Session.CreatedAt.UnixNano(),
	}, true
}

func (r *A2AAgent) newAnonymousClient() (*client.A2AClient, error) {
	jar, err := newCookieJar(nil)
	if err != nil {
		return nil, fmt.Errorf("create anonymous cookie jar: %w", err)
	}
	opts := make([]client.Option, 0, len(r.extraA2AOptions)+1)
	opts = append(opts,
		client.WithHTTPClient(&http.Client{Jar: jar}),
	)
	opts = append(opts, r.extraA2AOptions...)
	a2aClient, err := client.NewA2AClient(r.a2aClientURL, opts...)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create session-scoped A2A client for %s: %w",
			r.a2aClientURL,
			err,
		)
	}
	return a2aClient, nil
}

// sendErrorEvent sends an error event to the event channel.
func (r *A2AAgent) sendErrorEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocation *agent.Invocation,
	err error,
) *model.ResponseError {
	respErr := model.ResponseErrorFromError(err, model.ErrorTypeRunError)
	agent.EmitEvent(ctx, invocation, eventChan, event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			Object: model.ObjectTypeError,
			Error:  respErr,
		}),
	))
	return respErr
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
	// Validate A2A request options early
	if err := r.validateA2ARequestOptions(invocation); err != nil {
		if startedSpan {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, err
	}
	a2aClient, err := r.clientForInvocation(invocation)
	if err != nil {
		if startedSpan {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, err
	}
	if a2aClient == nil {
		err = errors.New("A2A client is nil")
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
		eventChan, err = r.runStreamingWithClient(ctx, invocation, a2aClient)
	} else {
		eventChan, err = r.runNonStreamingWithClient(ctx, invocation, a2aClient)
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
//
// Supported patterns:
//   - "*"        — transfer all keys
//   - "prefix*"  — transfer keys with the given prefix (e.g. "user.*" or "user*")
//   - "*suffix"  — transfer keys with the given suffix (e.g. "*.id" or "*id")
//   - "exact"    — transfer only the exact key
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
		for _, pattern := range r.transferStateKey {
			matchStateKeys(pattern, invocation.RunOptions.RuntimeState, message.Metadata)
		}
		return message, nil
	}
}

// matchStateKeys copies keys from src to dst that match the given pattern.
func matchStateKeys(pattern string, src map[string]any, dst map[string]any) {
	switch {
	case pattern == "*":
		for k, v := range src {
			dst[k] = v
		}
	case strings.HasPrefix(pattern, "*"):
		suffix := pattern[1:]
		for k, v := range src {
			if strings.HasSuffix(k, suffix) {
				dst[k] = v
			}
		}
	case strings.HasSuffix(pattern, "*"):
		prefix := pattern[:len(pattern)-1]
		for k, v := range src {
			if strings.HasPrefix(k, prefix) {
				dst[k] = v
			}
		}
	default:
		if v, ok := src[pattern]; ok {
			dst[pattern] = v
		}
	}
}

// runStreaming handles streaming A2A communication
func (r *A2AAgent) runStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return r.runStreamingWithClient(ctx, invocation, r.a2aClient)
}

func (r *A2AAgent) runStreamingWithClient(
	ctx context.Context,
	invocation *agent.Invocation,
	a2aClient *client.A2AClient,
) (<-chan *event.Event, error) {
	if r.eventConverter == nil {
		return nil, fmt.Errorf("event converter not set")
	}
	eventChan := make(chan *event.Event, r.streamingBufSize)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(eventChan)
		r.executeStreaming(ctx, invocation, eventChan, a2aClient)
	}(runCtx)
	return eventChan, nil
}

// executeStreaming executes the streaming A2A communication workflow.
func (r *A2AAgent) executeStreaming(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	a2aClient *client.A2AClient,
) {
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
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	streamChan, err := a2aClient.StreamMessage(
		streamCtx,
		protocol.SendMessageParams{Message: *a2aMessage},
		requestOpts...,
	)
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
		streamCtx,
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
			currentResponseID := result.responseID
			if evt.Response != nil && evt.Response.ID != "" {
				currentResponseID = evt.Response.ID
			}
			if evt.Response != nil && !evt.Response.IsPartial {
				r.flushBufferedContent(
					ctx,
					invocation,
					eventChan,
					currentResponseID,
					evt.Timestamp,
					&contentBuilder,
				)
			}
			var terminalError *model.ResponseError
			result.responseID, terminalError = r.aggregateEventContent(
				ctx,
				invocation,
				eventChan,
				evt,
				result.responseID,
				&contentBuilder,
			)
			if terminalError != nil {
				result.aggregatedContent = contentBuilder.String()
				result.terminalError = terminalError
				return result
			}
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

// flushBufferedContent emits buffered streaming text as a complete assistant
// message before forwarding a non-partial event such as a tool call or tool
// response. This preserves the original turn order in session history.
func (r *A2AAgent) flushBufferedContent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	responseID string,
	anchorTimestamp time.Time,
	contentBuilder *strings.Builder,
) {
	if contentBuilder == nil || contentBuilder.Len() == 0 {
		return
	}

	content := contentBuilder.String()
	contentBuilder.Reset()

	flushTime := time.Now()
	if !anchorTimestamp.IsZero() {
		flushTime = anchorTimestamp.Add(-1 * time.Nanosecond)
	}

	evt := event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			ID:        responseID,
			Object:    model.ObjectTypeChatCompletion,
			Done:      false,
			IsPartial: false,
			Timestamp: flushTime,
			Created:   flushTime.Unix(),
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			}},
		}),
	)
	evt.Timestamp = flushTime
	agent.EmitEvent(ctx, invocation, eventChan, evt)
}

// aggregateEventContent aggregates content from event delta.
// Returns updated responseID and any terminal error that occurred.
func (r *A2AAgent) aggregateEventContent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	evt *event.Event,
	responseID string,
	contentBuilder *strings.Builder,
) (string, *model.ResponseError) {
	if evt.Response == nil || evt.Response.Error != nil {
		return responseID, nil
	}
	if len(evt.Response.Choices) == 0 {
		return responseID, nil
	}

	if evt.Response.ID != "" {
		responseID = evt.Response.ID
	}

	if r.streamingRespHandler != nil {
		content, err := r.streamingRespHandler(evt.Response)
		if err != nil {
			respErr := r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("streaming resp handler failed: %w", err),
			)
			return responseID, respErr
		}
		if content != "" {
			contentBuilder.WriteString(content)
		}
	} else if evt.Response.Choices[0].Delta.Content != "" {
		contentBuilder.WriteString(evt.Response.Choices[0].Delta.Content)
	}
	return responseID, nil
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
	return r.runNonStreamingWithClient(ctx, invocation, r.a2aClient)
}

func (r *A2AAgent) runNonStreamingWithClient(
	ctx context.Context,
	invocation *agent.Invocation,
	a2aClient *client.A2AClient,
) (<-chan *event.Event, error) {
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
		result, err := a2aClient.SendMessage(ctx, params, requestOpts...)
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
			if fullRespEvent != nil && fullRespEvent.Response != nil {
				responseErrorType = ""
				if fullRespEvent.Response.Error != nil {
					responseErrorType = itelemetry.FormatResponseErrorLabel(
						fullRespEvent.Response.Error,
						model.ErrorTypeRunError,
					)
				}
			}
			if startedSpan && fullRespEvent != nil {
				log.DebugContext(ctx, "fullRespEvent is not ni")
				itelemetry.TraceAfterInvokeAgent(
					span,
					fullRespEvent,
					tokenUsage,
					tracker.FirstTokenTimeDuration(),
					model.ErrorTypeRunError,
				)
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
				responseErrorType = itelemetry.FormatResponseErrorLabel(
					evt.Error,
					model.ErrorTypeRunError,
				)
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
