//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package a2a provides utilities for creating a2a servers.
package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"trpc.group/trpc-go/trpc-a2a-go/v2/auth"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/stateless"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// New creates a new a2a server.
func New(opts ...Option) (*a2a.A2AServer, error) {
	options := &options{
		errorHandler: defaultErrorHandler,
		// Enable ADK compatibility by default.
		adkCompatibility: true,
		// Default to ADK-style streaming: artifacts for content.
		streamingEventType: StreamingEventTypeTaskArtifactUpdate,
	}
	for _, opt := range opts {
		opt(options)
	}

	if options.runner == nil {
		return nil, errors.New("runner (WithRunner) is required")
	}
	if options.agentCard == nil {
		return nil, errors.New("agent card (WithAgentCard) is required")
	}

	return buildA2AServer(options)
}

// buildRuntimeState makes a shallow copy of message metadata for RuntimeState.
func buildRuntimeState(metadata map[string]any) map[string]any {
	runtimeState := make(map[string]any, len(metadata))
	for key, value := range metadata {
		runtimeState[key] = value
	}
	return runtimeState
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func buildProcessor(serverIdentity string, options *options) (*messageProcessor, error) {
	if serverIdentity == "" {
		return nil, errors.New("agent card name is required")
	}

	procRunner := options.runner
	if procRunner == nil {
		return nil, errors.New("runner is required")
	}

	// Use custom converters if provided, otherwise use defaults
	a2aToAgentConverter := options.a2aToAgentConverter
	if a2aToAgentConverter == nil {
		a2aToAgentConverter = &defaultA2AMessageToAgentMessage{}
	}

	eventToA2AConverter := options.eventToA2AConverter
	if eventToA2AConverter == nil {
		eventToA2AConverter = &defaultEventToA2AMessage{
			adkCompatibility:          options.adkCompatibility,
			graphEventObjectAllowlist: options.graphEventObjectAllowlist,
			streamingEventType:        options.streamingEventType,
			eventPartMappers:          options.eventPartMappers,
		}
	} else if len(options.eventPartMappers) > 0 {
		log.Warn("WithEventToA2APartMapper is ignored because WithEventToA2AConverter provided a custom converter")
	}

	return &messageProcessor{
		runner:               procRunner,
		a2aToAgentConverter:  a2aToAgentConverter,
		eventToA2AConverter:  eventToA2AConverter,
		errorHandler:         options.errorHandler,
		debugLogging:         options.debugLogging,
		adkCompatibility:     options.adkCompatibility,
		responseRewriter:     options.responseRewriter,
		streamingEventType:   options.streamingEventType,
		structuredTaskErrors: options.structuredTaskErrors,
		agentName:            serverIdentity,
		runOptions:           options.runOptions,
	}, nil
}

func buildA2AServer(options *options) (*a2a.A2AServer, error) {
	agentCard := *options.agentCard
	if agentCard.Name == "" {
		return nil, errors.New("agent card name is required")
	}

	var (
		processor taskmanager.MessageProcessor
		err       error
	)
	if options.processorBuilder != nil {
		processor = options.processorBuilder(options.runner)
	} else {
		processor, err = buildProcessor(agentCard.Name, options)
		if err != nil {
			return nil, fmt.Errorf("failed to build processor: %w", err)
		}
	}

	if options.processorHook != nil {
		processor = options.processorHook(processor)
	}

	// By default, A2A is only the transport: trpc-agent-go's session service owns
	// conversation context and the stateless manager retains no duplicate task
	// state. An explicit builder replaces the request-local manager when retained
	// A2A task state is required.
	var taskManager taskmanager.TaskManager
	if options.taskManagerBuilder != nil {
		taskManager, err = options.taskManagerBuilder(processor)
		if err != nil {
			return nil, fmt.Errorf("failed to create task manager: %w", err)
		}
	} else {
		taskManager, err = stateless.NewTaskManager(processor)
		if err != nil {
			return nil, fmt.Errorf("failed to create task manager: %w", err)
		}
	}

	// Set default UserID header if not configured
	userIDHeader := options.userIDHeader
	if userIDHeader == "" {
		userIDHeader = serverUserIDHeader
	}

	// Extract base path from agent card URL for request routing.
	// If the URL contains a path component (e.g., "http://example.com/api/v1"),
	// it will be extracted and used as the base path for routing incoming requests.
	basePath := extractBasePath(ia2a.NormalizeURL(agentCard.PrimaryURL()))

	opts := []a2a.Option{
		a2a.WithAuthProvider(&defaultAuthProvider{userIDHeader: userIDHeader}),
		a2a.WithBasePath(basePath),
		a2a.WithMiddleware(&traceContextMiddleware{}),
	}
	opts = append([]a2a.Option{a2a.WithAgentCard(agentCard)}, opts...)
	opts = append(opts, options.extraOptions...)
	a2aServer, err := a2a.NewA2AServer(taskManager, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create a2a server: %w", err)
	}
	return a2aServer, nil
}

// traceContextMiddleware extracts W3C Trace Context from HTTP headers and injects
// it into the request context. This enables distributed tracing across A2A
// agent boundaries.
type traceContextMiddleware struct{}

// Wrap implements the a2a.Middleware interface.
func (m *traceContextMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract trace context from HTTP headers using the global propagator
		propagator := otel.GetTextMapPropagator()
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		// Continue with the enriched context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractBasePath extracts the path component from a URL for request routing.
// It parses the URL and returns the path if the URL has a valid scheme.
//
// Examples:
//   - "http://example.com/api/v1" → "/api/v1"
//   - "https://example.com/docs" → "/docs"
//   - "grpc://service:9090/rpc" → "/rpc"
//   - "http://example.com" → "" (no path)
//   - "invalid-url" → "" (no scheme)
//
// The extracted path is used as the base path for routing incoming A2A requests.
func extractBasePath(urlStr string) string {
	if urlStr == "" {
		return ""
	}

	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}

	// Extract path if URL has a valid scheme
	if u.Scheme != "" {
		return u.Path
	}

	// No valid scheme, return empty string
	return ""
}

// messageProcessor is the message processor for the a2a server.
type messageProcessor struct {
	runner               runner.Runner
	a2aToAgentConverter  A2AMessageToAgentMessage
	eventToA2AConverter  EventToA2AMessage
	errorHandler         ErrorHandler
	debugLogging         bool
	adkCompatibility     bool
	responseRewriter     ResponseRewriter
	streamingEventType   StreamingEventType
	structuredTaskErrors bool
	agentName            string
	runOptions           []agent.RunOption
}

// taskOutputState tracks only the request-local information needed to frame
// converted runner events as one task lifecycle. Task persistence remains the
// selected TaskManager's responsibility.
type taskOutputState struct {
	seenArtifactIDs  map[string]struct{}
	fallbackArtifact string
	lastArtifactID   string
	finalMessage     *protocol.Message
	finalMetadata    map[string]any
	terminalError    bool
}

func isFinalStreamingEvent(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}

	// The only truly final event is runner.completion
	// This ensures we don't miss postprocessing events (code execution, etc.)
	return evt.IsRunnerCompletion()
}

func buildFinalStreamingMetadata(evt *event.Event) map[string]any {
	if evt == nil {
		return nil
	}

	var metadata map[string]any
	if responseID := finalStreamingResponseID(evt); responseID != "" {
		metadata = map[string]any{
			ia2a.MessageMetadataResponseIDKey: responseID,
		}
	}
	if evt.Response != nil && evt.Response.Error != nil {
		metadata = ia2a.WithResponseErrorMetadata(metadata, evt.Response.Error)
	}
	if stateDelta := ia2a.EncodeStateDeltaMetadata(evt.StateDelta); len(stateDelta) > 0 {
		if metadata == nil {
			metadata = make(map[string]any, 1)
		}
		metadata[ia2a.MessageMetadataStateDeltaKey] = stateDelta
	}
	return metadata
}

func finalStreamingResponseID(evt *event.Event) string {
	if evt == nil || len(evt.StateDelta) == 0 {
		return ""
	}
	raw, ok := evt.StateDelta[graph.StateKeyLastResponseID]
	if !ok || len(raw) == 0 {
		return ""
	}
	var responseID string
	if err := json.Unmarshal(raw, &responseID); err != nil {
		return ""
	}
	return responseID
}

func cloneStreamingMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func mergeMessageMetadata(message *protocol.Message, metadata map[string]any) {
	if message == nil || len(metadata) == 0 {
		return
	}
	if message.Metadata == nil {
		message.Metadata = make(map[string]any, len(metadata))
	}
	for key, value := range metadata {
		if key != ia2a.MessageMetadataStateDeltaKey {
			message.Metadata[key] = value
			continue
		}
		merged := DecodeStateDeltaMetadata(message.Metadata[key])
		if merged == nil {
			merged = make(map[string][]byte)
		}
		for deltaKey, deltaValue := range DecodeStateDeltaMetadata(value) {
			merged[deltaKey] = deltaValue
		}
		if encoded := EncodeStateDeltaMetadata(merged); len(encoded) > 0 {
			message.Metadata[key] = encoded
		}
	}
}

func isStructuredTaskErrorEvent(
	evt *event.Event,
) bool {
	return evt != nil && evt.IsTerminalError()
}

func taskErrorState(
	respErr *model.ResponseError,
) protocol.TaskState {
	if respErr != nil &&
		respErr.Type == agent.ErrorTypeStopAgentError {
		return protocol.TaskStateCanceled
	}
	return protocol.TaskStateFailed
}

func buildTaskErrorMetadata(
	agentEvent *event.Event,
) map[string]any {
	if agentEvent == nil || agentEvent.Response == nil {
		return nil
	}
	respErr := agentEvent.Response.Error
	if respErr == nil {
		return nil
	}

	metadata := ia2a.WithResponseErrorMetadata(nil, respErr)
	metadata[ia2a.MessageMetadataTaskStateKey] = string(
		taskErrorState(respErr),
	)
	if agentEvent.Response.ID != "" {
		metadata[ia2a.MessageMetadataResponseIDKey] =
			agentEvent.Response.ID
	}
	return metadata
}

func buildTaskErrorMessage(
	taskID string,
	ctxID string,
	agentEvent *event.Event,
	metadata map[string]any,
) *protocol.Message {
	if agentEvent == nil || agentEvent.Response == nil {
		return nil
	}
	respErr := agentEvent.Response.Error
	if respErr == nil {
		return nil
	}

	var parts []*protocol.Part
	if respErr.Message != "" {
		parts = append(parts, protocol.NewTextPart(respErr.Message))
	}
	msg := protocol.NewMessageWithContext(
		protocol.MessageRoleAgent,
		parts,
		&taskID,
		&ctxID,
	)
	msg.Metadata = cloneMetadata(metadata)
	if agentEvent.Response.ID != "" {
		msg.MessageID = agentEvent.Response.ID
	}
	return &msg
}

func buildStructuredFailureTask(
	taskID string,
	ctxID string,
	history []protocol.Message,
	agentEvent *event.Event,
) *protocol.Task {
	taskMetadata := buildTaskErrorMetadata(agentEvent)
	statusMsg := buildTaskErrorMessage(
		taskID,
		ctxID,
		agentEvent,
		taskMetadata,
	)
	task := protocol.NewTask(taskID, ctxID)
	task.History = history
	task.Status = protocol.TaskStatus{
		State:     taskErrorState(agentEvent.Response.Error),
		Message:   statusMsg,
		Timestamp: time.Now().Format(time.RFC3339),
	}
	task.Metadata = taskMetadata
	return task
}

// sendEvent rewrites one streaming event and delivers it on out. It reports an
// error only when ctx is canceled before the framework consumes the event; a
// rewritten-to-nil (dropped) event succeeds silently. The framework always
// drains out until it is closed, so a live send never leaks.
func (m *messageProcessor) sendEvent(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	result protocol.StreamEvent,
) error {
	rewritten := m.rewriteStreamingResult(ctx, result)
	if rewritten == nil {
		return nil
	}
	return sendPreparedEvent(ctx, out, rewritten)
}

// sendPreparedEvent delivers an event that has already been rewritten and
// normalized.
func sendPreparedEvent(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	result protocol.StreamEvent,
) error {
	select {
	case out <- result:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// replyError turns a setup failure into an outbound reply, preserving the former
// handleError behavior under the event-stream contract. A nil handler error
// yields a friendly agent message emitted on a short-lived channel: no task is
// created, so the framework derives that message as the unary result and streams
// it for message/stream. A non-nil handler error fails the round to start and is
// mapped to a JSON-RPC error.
func (m *messageProcessor) replyError(
	ctx context.Context,
	msg *protocol.Message,
	err error,
) (<-chan protocol.StreamEvent, error) {
	if m.debugLogging {
		log.DebugfContext(
			ctx,
			"handling error: req msg id: %s, error: %v",
			msg.MessageID,
			err,
		)
	}

	errMsg, handlerErr := m.errorHandler(ctx, msg, err)
	if handlerErr != nil {
		return nil, handlerErr
	}

	out := make(chan protocol.StreamEvent, 1)
	if rewritten := m.rewriteStreamingResult(ctx, errMsg); rewritten != nil {
		out <- rewritten
	}
	close(out)
	return out, nil
}

func (m *messageProcessor) handleStreamingProcessingError(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	msg *protocol.Message,
	err error,
) error {
	if m.debugLogging {
		msgJson, _ := json.Marshal(msg)
		log.DebugfContext(
			ctx,
			"handling error: req msg id: %s, error: %v, msg: %s",
			msg.MessageID,
			err,
			string(msgJson),
		)
	}

	errMsg, handlerErr := m.errorHandler(ctx, msg, err)
	if handlerErr != nil {
		log.WarnfContext(ctx, "handle streaming processing error: %v", handlerErr)
		return handlerErr
	}
	if err := m.sendEvent(ctx, out, errMsg); err != nil {
		log.ErrorfContext(ctx, "failed to send error message: %v", err)
		return fmt.Errorf("failed to send error message: %w", err)
	}
	return nil
}

// ProcessMessage is the main entry point for processing messages. It adapts the
// agent runner to the event-stream MessageProcessor contract and emits one task
// lifecycle (submitted -> converted agent output -> completed). The selected
// manager either keeps that Task request-local or retains it across requests,
// and derives unary and streaming results from the same events.
func (m *messageProcessor) ProcessMessage(
	ctx context.Context,
	ec *taskmanager.ExecContext,
) (<-chan protocol.StreamEvent, error) {
	message := ec.Message
	if m.debugLogging {
		msgJson, _ := json.Marshal(message)
		log.DebugfContext(
			ctx,
			"received A2A message: msg id: %s, message: %s",
			message.MessageID,
			string(msgJson),
		)
	}

	user, ok := ctx.Value(auth.AuthUserKey).(*auth.User)
	if !ok {
		log.WarnContext(ctx, "a2aserver: user is nil")
		return m.replyError(ctx, &message, errors.New("a2aserver: user is nil"))
	}

	// The framework guarantees a non-empty context ID (generating one when the
	// request omits it). Keep the message aligned so downstream converters and
	// emitted events observe the same context ID.
	ctxID := ec.ContextID
	message.ContextID = &ctxID

	// Get user ID from auth context, or generate from context ID if not available.
	// This follows ADK pattern: use auth user if available, otherwise use A2A_USER_{context_id}.
	userID := user.ID
	if userID == "" {
		userID = fmt.Sprintf("A2A_USER_%s", ctxID)
		log.DebugfContext(
			ctx,
			"UserID not set in auth context, using generated ID from context: %s",
			userID,
		)
	}

	// Convert A2A message to agent message
	agentMsg, err := m.a2aToAgentConverter.ConvertToAgentMessage(ctx, message)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"failed to convert A2A message to agent message: %v",
			err,
		)
		return m.replyError(ctx, &message, err)
	}
	if agentMsg == nil {
		log.ErrorContext(ctx, "agent message is nil after conversion")
		return m.replyError(ctx, &message, errors.New("a2aserver: agent message is nil"))
	}

	if m.debugLogging {
		agentMsgJson, _ := json.Marshal(agentMsg)
		log.DebugfContext(
			ctx,
			"converted A2A message to agent message: id: %s, message: %s",
			message.MessageID,
			string(agentMsgJson),
		)
	}

	agentMsgChan, err := m.runner.Run(ctx, userID, ctxID, *agentMsg, m.buildRunnerOptions(message)...)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"failed to run agent for user %s, context %s: %v",
			userID,
			ctxID,
			err,
		)
		return m.replyError(ctx, &message, err)
	}

	// Emit events from a goroutine: the framework starts draining the channel
	// only after ProcessMessage returns, so events must not be sent inline.
	// The processor emits one protocol event stream for both message/send and
	// message/stream. The selected TaskManager derives the requested wire shape.
	// Closing out ends the round.
	out := make(chan protocol.StreamEvent)
	go func() {
		defer close(out)
		m.streamAgentEvents(ctx, out, ec.TaskID, userID, ctxID, &message, agentMsgChan)
	}()
	return out, nil
}

// buildRunnerOptions applies user-defined run options first, then merges A2A
// message metadata into RuntimeState. Doing the merge last avoids conflicts when
// the user also sets WithRuntimeState in runOptions, since WithRuntimeState uses
// overwrite semantics.
func (m *messageProcessor) buildRunnerOptions(message protocol.Message) []agent.RunOption {
	runnerOpts := make([]agent.RunOption, 0, len(m.runOptions)+1)
	runnerOpts = append(runnerOpts, m.runOptions...)
	runnerOpts = append(runnerOpts, func(opts *agent.RunOptions) {
		if len(message.Metadata) == 0 {
			return
		}
		a2aState := buildRuntimeState(message.Metadata)
		// Overlay structured graph resume state (e.g. ResumeCommand) so that
		// it takes precedence over the raw flattened metadata keys.
		if resumeState := ia2a.GraphResumeStateFromMetadata(message.Metadata); len(resumeState) > 0 {
			for k, v := range resumeState {
				a2aState[k] = v
			}
		}
		if opts.RuntimeState == nil {
			opts.RuntimeState = a2aState
			return
		}
		// Copy existing state to avoid mutating the shared map from WithRunOptions.
		merged := make(map[string]any, len(opts.RuntimeState)+len(a2aState))
		for k, v := range opts.RuntimeState {
			merged[k] = v
		}
		for k, v := range a2aState {
			merged[k] = v
		}
		opts.RuntimeState = merged
	})
	return runnerOpts
}

// streamAgentEvents drives one agent run to completion and emits one task
// lifecycle. A stateless manager applies it to a request-local Task, while a
// retaining manager persists the same events. The caller closes out when this
// returns.
func (m *messageProcessor) streamAgentEvents(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	taskID string,
	userID string,
	sessionID string,
	a2aMsg *protocol.Message,
	agentMsgChan <-chan *event.Event,
) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(
				ctx,
				log.PanicPrefix+" panic in streaming processing for task %s: %v",
				taskID,
				r,
			)
			// Emit an error event before the caller closes out.
			if err := m.handleStreamingProcessingError(ctx, out, a2aMsg,
				fmt.Errorf("streaming processing panic: %v", r)); err != nil {
				log.ErrorfContext(ctx, "failed to handle panic error: %v", err)
			}
		}
	}()

	state := &taskOutputState{
		seenArtifactIDs:  make(map[string]struct{}),
		fallbackArtifact: protocol.GenerateArtifactID(),
	}

	err := m.sendTaskSubmittedEvent(ctx, out, taskID, userID, sessionID, a2aMsg)
	if m.abortStreamingOnError(ctx, out, a2aMsg, err) {
		return
	}

	err = m.consumeAgentEvents(ctx, out, taskID, a2aMsg, agentMsgChan, state)
	if m.abortStreamingOnError(ctx, out, a2aMsg, err) {
		return
	}

	if state.terminalError {
		return
	}

	ctxID := *a2aMsg.ContextID
	err = m.sendFinalArtifactEvent(ctx, out, taskID, ctxID, state)
	if m.abortStreamingOnError(ctx, out, a2aMsg, err) {
		return
	}

	err = m.sendTaskCompletedEvent(ctx, out, taskID, ctxID, state)
	m.abortStreamingOnError(ctx, out, a2aMsg, err)
}

// consumeAgentEvents forwards events until the runner closes the channel,
// processing reaches a terminal event, or the request context ends.
func (m *messageProcessor) consumeAgentEvents(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	taskID string,
	a2aMsg *protocol.Message,
	agentMsgChan <-chan *event.Event,
	state *taskOutputState,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case agentEvent, ok := <-agentMsgChan:
			if !ok {
				return nil
			}
			done, err := m.processStreamingEvent(ctx, out, taskID, a2aMsg, agentEvent, state)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
	}
}

func (m *messageProcessor) abortStreamingOnError(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	a2aMsg *protocol.Message,
	err error,
) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		log.DebugfContext(ctx, "streaming stopped before completion: %v", err)
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		log.WarnfContext(ctx, "streaming stopped before completion: %v", err)
		return true
	}
	if handleErr := m.handleStreamingProcessingError(ctx, out, a2aMsg, err); handleErr != nil {
		log.ErrorfContext(
			ctx,
			"failed to handle streaming error: %v",
			handleErr,
		)
	}
	return true
}

func (m *messageProcessor) sendTaskSubmittedEvent(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	taskID string,
	userID string,
	sessionID string,
	a2aMsg *protocol.Message,
) error {
	taskSubmitted := protocol.NewTaskStatusUpdateEvent(
		taskID, *a2aMsg.ContextID,
		protocol.TaskStatus{
			State:     protocol.TaskStateSubmitted,
			Timestamp: time.Now().Format(time.RFC3339),
		},
		false,
	)
	// Add ADK-compatible metadata if enabled.
	m.addTaskMetadata(&taskSubmitted, userID, sessionID)
	return m.sendEvent(ctx, out, &taskSubmitted)
}

func (m *messageProcessor) sendFinalArtifactEvent(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	taskID string,
	ctxID string,
	state *taskOutputState,
) error {
	if m.streamingEventType == StreamingEventTypeMessage {
		return nil
	}
	artifactID := state.lastArtifactID
	appendChunk := artifactID != ""
	if artifactID == "" {
		artifactID = state.fallbackArtifact
	}
	metadata := cloneStreamingMetadata(state.finalMetadata)
	finalArtifact := protocol.NewTaskArtifactUpdateEvent(
		taskID,
		ctxID,
		protocol.Artifact{
			ArtifactID: artifactID,
			Parts:      []*protocol.Part{},
			Metadata:   metadata,
		},
		true,
	)
	finalArtifact.Append = &appendChunk
	finalArtifact.Metadata = cloneStreamingMetadata(state.finalMetadata)
	return m.sendEvent(ctx, out, &finalArtifact)
}

func (m *messageProcessor) sendTaskCompletedEvent(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	taskID string,
	ctxID string,
	state *taskOutputState,
) error {
	if state.finalMessage != nil {
		mergeMessageMetadata(state.finalMessage, state.finalMetadata)
	}
	taskCompleted := protocol.NewTaskStatusUpdateEvent(
		taskID, ctxID,
		protocol.TaskStatus{
			State:     protocol.TaskStateCompleted,
			Message:   state.finalMessage,
			Timestamp: time.Now().Format(time.RFC3339),
		},
		true,
	)
	if m.streamingEventType == StreamingEventTypeMessage {
		taskCompleted.Metadata = cloneStreamingMetadata(state.finalMetadata)
	}
	return m.sendEvent(ctx, out, &taskCompleted)
}

// processStreamingEvent converts one agent event and emits it on out. The
// returned boolean reports whether event consumption is complete.
func (m *messageProcessor) processStreamingEvent(
	ctx context.Context,
	out chan<- protocol.StreamEvent,
	taskID string,
	a2aMsg *protocol.Message,
	agentEvent *event.Event,
	state *taskOutputState,
) (bool, error) {
	if agentEvent == nil {
		log.WarnContext(ctx, "received nil event, skipping")
		return false, nil
	}

	if m.debugLogging {
		agentEventJSON, _ := json.Marshal(agentEvent)
		log.DebugfContext(
			ctx,
			"get agent event: req msg id: %s, event: %s",
			a2aMsg.MessageID,
			string(agentEventJSON),
		)
	}

	if agentEvent.Response == nil {
		log.DebugfContext(
			ctx,
			"received empty response event, continuing, event: %v",
			agentEvent,
		)
		return false, nil
	}

	if m.structuredTaskErrors && isStructuredTaskErrorEvent(agentEvent) {
		task := buildStructuredFailureTask(
			taskID,
			*a2aMsg.ContextID,
			nil,
			agentEvent,
		)
		statusEvent := protocol.NewTaskStatusUpdateEvent(
			taskID,
			*a2aMsg.ContextID,
			task.Status,
			true,
		)
		statusEvent.Metadata = task.Metadata
		if err := m.sendEvent(ctx, out, &statusEvent); err != nil {
			return false, fmt.Errorf(
				"failed to send task failure status: %w",
				err,
			)
		}
		state.terminalError = true
		return true, nil
	}

	if isFinalStreamingEvent(agentEvent) {
		state.finalMetadata = buildFinalStreamingMetadata(agentEvent)
		log.DebugfContext(
			ctx,
			"received final event, stopping event processing (eventID=%s)",
			agentEvent.ID,
		)
		return true, nil
	}

	convertedResult, err := m.eventToA2AConverter.ConvertStreamingToA2AMessage(
		ctx, agentEvent, EventToA2AStreamingOptions{CtxID: *a2aMsg.ContextID, TaskID: taskID},
	)
	if err != nil {
		return false, fmt.Errorf("failed to convert event to A2A message: %w", err)
	}

	if m.debugLogging {
		a2aMsgJSON, _ := json.Marshal(convertedResult)
		log.DebugfContext(
			ctx,
			"converted agent event to A2A message: req msg id: %s, event: %s",
			a2aMsg.MessageID,
			string(a2aMsgJSON),
		)
	}

	// Rewrite first so task aggregation reflects exactly what is sent. This is
	// important for response redaction and event replacement: dropped or
	// rewritten content must not reappear in the final Task snapshot.
	if convertedResult == nil {
		return false, nil
	}
	prepareTaskOutputEvent(convertedResult, state)
	outbound := m.rewriteStreamingResult(ctx, convertedResult)
	if outbound == nil {
		return false, nil
	}
	prepareTaskOutputEvent(outbound, state)
	switch converted := outbound.(type) {
	case *protocol.TaskArtifactUpdateEvent:
		artifactID := converted.Artifact.ArtifactID
		state.seenArtifactIDs[artifactID] = struct{}{}
		state.lastArtifactID = artifactID
	case *protocol.Message:
		if state.finalMessage == nil {
			message := protocol.NewMessageWithContext(
				protocol.MessageRoleAgent,
				nil,
				&taskID,
				a2aMsg.ContextID,
			)
			state.finalMessage = &message
		}
		state.finalMessage.Parts = append(
			state.finalMessage.Parts,
			converted.Parts...,
		)
		mergeMessageMetadata(state.finalMessage, converted.Metadata)
	}
	if err := sendPreparedEvent(ctx, out, outbound); err != nil {
		log.ErrorfContext(
			ctx,
			"failed to send streaming message event: %v",
			err,
		)
		return false, fmt.Errorf("failed to send streaming message event: %w", err)
	}

	return false, nil
}

// prepareTaskOutputEvent fills request-local artifact framing without recording
// it in taskOutputState. State is updated only after rewriting succeeds.
func prepareTaskOutputEvent(
	result protocol.StreamEvent,
	state *taskOutputState,
) {
	artifact, ok := result.(*protocol.TaskArtifactUpdateEvent)
	if !ok || artifact == nil {
		return
	}
	artifactID := artifact.Artifact.ArtifactID
	if artifactID == "" {
		artifactID = state.fallbackArtifact
		artifact.Artifact.ArtifactID = artifactID
	}
	if artifact.Append == nil {
		_, seen := state.seenArtifactIDs[artifactID]
		artifact.Append = &seen
	}
}

// addTaskMetadata adds ADK-compatible metadata to task status update events.
// Only writes metadata when ADK compatibility is enabled, using ADK-prefixed keys
// (adk_app_name, adk_user_id, adk_session_id) for interoperability with ADK clients.
func (m *messageProcessor) addTaskMetadata(event *protocol.TaskStatusUpdateEvent, userID, sessionID string) {
	if !m.adkCompatibility {
		return
	}

	if event.Metadata == nil {
		event.Metadata = make(map[string]any)
	}

	event.Metadata[ia2a.GetADKMetadataKey("app_name")] = m.agentName
	event.Metadata[ia2a.GetADKMetadataKey("user_id")] = userID
	event.Metadata[ia2a.GetADKMetadataKey("session_id")] = sessionID
}

func (m *messageProcessor) rewriteStreamingResult(
	ctx context.Context,
	result protocol.StreamEvent,
) protocol.StreamEvent {
	if result == nil {
		return nil
	}
	if m.responseRewriter != nil {
		result = m.responseRewriter.RewriteStreaming(ctx, result)
	}
	if result == nil {
		return nil
	}
	return normalizeStreamingResult(result)
}

func normalizeStreamingResult(
	result protocol.StreamEvent,
) protocol.StreamEvent {
	switch v := result.(type) {
	case *protocol.Message:
		return normalizeProtocolMessage(v)
	case *protocol.TaskArtifactUpdateEvent:
		return normalizeTaskArtifactUpdateEvent(v)
	case *protocol.TaskStatusUpdateEvent:
		return normalizeTaskStatusUpdateEvent(v)
	default:
		return result
	}
}

func normalizeProtocolMessage(msg *protocol.Message) *protocol.Message {
	if msg == nil {
		return nil
	}
	msg.Metadata = normalizeMetadataMap(msg.Metadata)
	if len(msg.Parts) == 0 && !hasContentfulMetadata(msg.Metadata) {
		return nil
	}
	return msg
}

func normalizeTaskArtifactUpdateEvent(
	event *protocol.TaskArtifactUpdateEvent,
) *protocol.TaskArtifactUpdateEvent {
	if event == nil {
		return nil
	}
	event.Metadata = normalizeMetadataMap(event.Metadata)
	if normalized := normalizeArtifact(&event.Artifact); normalized != nil {
		event.Artifact = *normalized
		return event
	}
	if event.LastChunk != nil && *event.LastChunk {
		return event
	}
	if hasContentfulMetadata(event.Metadata) {
		return event
	}
	return nil
}

func normalizeTaskStatusUpdateEvent(
	event *protocol.TaskStatusUpdateEvent,
) *protocol.TaskStatusUpdateEvent {
	if event == nil {
		return nil
	}
	event.Metadata = normalizeMetadataMap(event.Metadata)
	if event.Status.Message != nil {
		event.Status.Message = normalizeProtocolMessage(event.Status.Message)
	}
	return event
}

func normalizeArtifact(artifact *protocol.Artifact) *protocol.Artifact {
	if artifact == nil {
		return nil
	}
	artifact.Metadata = normalizeMetadataMap(artifact.Metadata)
	if len(artifact.Parts) == 0 && !hasStructuredMetadata(artifact.Metadata) {
		return nil
	}
	return artifact
}

func normalizeMetadataMap(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}
