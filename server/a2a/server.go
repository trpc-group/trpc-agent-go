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
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
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

	if options.sessionService == nil {
		options.sessionService = inmemory.NewSessionService()
	}

	if options.agent == nil && options.runner == nil {
		return nil, errors.New("either agent (WithAgent) or runner (WithRunner) is required")
	}
	if options.agent != nil && options.runner != nil {
		return nil, errors.New("WithAgent and WithRunner cannot be used together; use WithAgentCard with WithRunner")
	}

	if options.agent == nil && options.agentCard == nil {
		return nil, errors.New("agent card (WithAgentCard) is required when using runner without agent")
	}

	// Host is only required if we need to build an agent card
	// If user provides a custom agent card, host is optional
	if options.agentCard == nil && options.host == "" {
		return nil, errors.New("host is required when agent card is not provided")
	}

	return buildA2AServer(options)
}

func buildAgentCard(options *options) (a2a.AgentCard, error) {
	if options.agentCard != nil {
		return *options.agentCard, nil
	}
	if options.agent == nil {
		return a2a.AgentCard{}, errors.New("agent is required when agent card is not provided")
	}
	info := options.agent.Info()
	return NewAgentCard(
		info.Name,
		info.Description,
		options.host,
		options.enableStreaming,
		WithCardTools(options.agent.Tools()...),
	)
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

func buildProcessor(
	agent agent.Agent,
	sessionService session.Service,
	serverIdentity string,
	options *options,
) (*messageProcessor, error) {
	if serverIdentity == "" {
		return nil, errors.New("agent card name is required")
	}

	procRunner := options.runner
	if procRunner == nil {
		if agent == nil {
			return nil, errors.New("agent is required when runner is not provided")
		}
		procRunner = runner.NewRunner(serverIdentity, agent, runner.WithSessionService(sessionService))
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
		}
	}

	return &messageProcessor{
		runner:               procRunner,
		a2aToAgentConverter:  a2aToAgentConverter,
		eventToA2AConverter:  eventToA2AConverter,
		errorHandler:         options.errorHandler,
		debugLogging:         options.debugLogging,
		adkCompatibility:     options.adkCompatibility,
		streamingEventType:   options.streamingEventType,
		structuredTaskErrors: options.structuredTaskErrors,
		agentName:            serverIdentity,
		runOptions:           options.runOptions,
	}, nil
}

func buildA2AServer(options *options) (*a2a.A2AServer, error) {
	agent := options.agent
	sessionService := options.sessionService

	agentCard, err := buildAgentCard(options)
	if err != nil {
		return nil, err
	}
	if agentCard.Name == "" {
		return nil, errors.New("agent card name is required")
	}

	var processor taskmanager.MessageProcessor
	if options.processorBuilder != nil {
		processor = options.processorBuilder(agent, sessionService)
	} else {
		processor, err = buildProcessor(agent, sessionService, agentCard.Name, options)
		if err != nil {
			return nil, fmt.Errorf("failed to build processor: %w", err)
		}
	}

	if options.processorHook != nil {
		processor = options.processorHook(processor)
	}

	// Create a task manager that wraps the session service
	var taskManager taskmanager.TaskManager
	if options.taskManagerBuilder != nil {
		taskManager = options.taskManagerBuilder(processor)
	} else {
		taskManager, err = taskmanager.NewMemoryTaskManager(processor)
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
	basePath := extractBasePath(ia2a.NormalizeURL(agentCard.URL))

	opts := []a2a.Option{
		a2a.WithAuthProvider(&defaultAuthProvider{userIDHeader: userIDHeader}),
		a2a.WithBasePath(basePath),
		a2a.WithMiddleWare(&traceContextMiddleware{}),
	}
	opts = append(opts, options.extraOptions...)
	a2aServer, err := a2a.NewA2AServer(agentCard, taskManager, opts...)
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
	streamingEventType   StreamingEventType
	structuredTaskErrors bool
	agentName            string
	runOptions           []agent.RunOption
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

	var parts []protocol.Part
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

// handleDefaultError provides a fallback error handling mechanism
func (m *messageProcessor) handleError(
	ctx context.Context,
	msg *protocol.Message,
	streaming bool,
	err error,
) (*taskmanager.MessageProcessingResult, error) {
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

	if streaming {
		subscriber := newSingleMsgSubscriber(errMsg)
		return &taskmanager.MessageProcessingResult{StreamingEvents: subscriber}, nil
	}

	return &taskmanager.MessageProcessingResult{Result: errMsg}, nil

}

func (m *messageProcessor) handleStreamingProcessingError(
	ctx context.Context,
	msg *protocol.Message,
	subscriber taskmanager.TaskSubscriber,
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

	errMsg, err := m.errorHandler(ctx, msg, err)
	if err != nil {
		log.WarnfContext(ctx, "handle streaming processing error: %v", err)
		return err
	}

	if err := subscriber.Send(protocol.StreamingMessageEvent{Result: errMsg}); err != nil {
		log.ErrorfContext(ctx, "failed to send error message: %v", err)
		return fmt.Errorf("failed to send error message: %w", err)
	}
	return nil
}

// ProcessMessage is the main entry point for processing messages.
func (m *messageProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
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
		return m.handleError(ctx, &message, options.Streaming, errors.New("a2aserver: user is nil"))
	}
	if message.ContextID == nil {
		// It should not reach here, if client transfers an empty ctx id, trpc-a2a-go will generate one
		log.WarnfContext(ctx, "a2aserver: context id not exists")
		return m.handleError(ctx, &message, options.Streaming, errors.New("context id not exists"))
	}

	ctxID := *message.ContextID

	// Get user ID from auth context, or generate from context ID if not available
	// This follows ADK pattern: use auth user if available, otherwise use A2A_USER_{context_id}
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
		return m.handleError(ctx, &message, options.Streaming, err)
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

	// Apply user-defined runOptions first, then merge A2A metadata into RuntimeState.
	// This avoids conflicts when user also sets WithRuntimeState in runOptions,
	// since WithRuntimeState uses overwrite semantics.
	runnerOpts := make([]agent.RunOption, 0, len(m.runOptions)+1)
	runnerOpts = append(runnerOpts, m.runOptions...)
	runnerOpts = append(runnerOpts, func(opts *agent.RunOptions) {
		if len(message.Metadata) == 0 {
			return
		}
		a2aState := buildRuntimeState(message.Metadata)
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

	if options.Streaming {
		return m.processStreamingMessage(ctx, userID, ctxID, &message, agentMsg, handler, runnerOpts)
	}
	return m.processMessage(ctx, userID, ctxID, &message, agentMsg, runnerOpts)
}

func (m *messageProcessor) processStreamingMessage(
	ctx context.Context,
	userID string,
	ctxID string,
	a2aMsg *protocol.Message,
	agentMsg *model.Message,
	handler taskmanager.TaskHandler,
	runnerOpts []agent.RunOption,
) (*taskmanager.MessageProcessingResult, error) {
	if agentMsg == nil {
		log.ErrorContext(
			ctx,
			"agent message is nil in streaming processing",
		)
		return m.handleError(ctx, a2aMsg, true, errors.New("a2aserver: agent message is nil"))
	}

	taskID, err := handler.BuildTask(nil, &ctxID)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"failed to build task for context %s: %v",
			ctxID,
			err,
		)
		return m.handleError(ctx, a2aMsg, true, err)
	}
	cleanupTask := func() {
		if err := handler.CleanTask(&taskID); err != nil {
			log.WarnfContext(
				ctx,
				"failed to clean task %s: %v",
				taskID,
				err,
			)
		}
	}

	subscriber, err := handler.SubscribeTask(&taskID)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"failed to subscribe to task %s: %v",
			taskID,
			err,
		)
		cleanupTask()
		return m.handleError(ctx, a2aMsg, true, err)
	}

	// Run the agent and get streaming events
	agentMsgChan, err := m.runner.Run(ctx, userID, ctxID, *agentMsg, runnerOpts...)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"failed to run agent for user %s, context %s: %v",
			userID,
			ctxID,
			err,
		)
		subscriber.Close()
		cleanupTask()
		return m.handleError(ctx, a2aMsg, true, err)
	}

	// Start processing in a goroutine with error recovery
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.ErrorfContext(
					ctx,
					"panic in streaming processing for task %s: %v",
					taskID,
					r,
				)
				// Send error to subscriber before closing
				if err := m.handleStreamingProcessingError(ctx, a2aMsg, subscriber,
					fmt.Errorf("streaming processing panic: %v", r)); err != nil {
					log.ErrorfContext(
						ctx,
						"failed to handle panic error: %v",
						err,
					)
				}
			}
		}()
		m.processAgentStreamingEvents(ctx, taskID, userID, ctxID, a2aMsg, agentMsgChan, subscriber, handler)
	}()

	return &taskmanager.MessageProcessingResult{
		StreamingEvents: subscriber,
	}, nil
}

// processAgentStreamingEvents handles streaming events from the agent runner using tunnel for batch processing.
func (m *messageProcessor) processAgentStreamingEvents(
	ctx context.Context,
	taskID string,
	userID string,
	sessionID string,
	a2aMsg *protocol.Message,
	agentMsgChan <-chan *event.Event,
	subscriber taskmanager.TaskSubscriber,
	handler taskmanager.TaskHandler,
) {
	defer func() {
		subscriber.Close()
		if err := handler.CleanTask(&taskID); err != nil {
			log.WarnfContext(
				ctx,
				"failed to clean task %s: %v",
				taskID,
				err,
			)
		}
	}()
	abortStreaming := func(err error) bool {
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
		if handleErr := m.handleStreamingProcessingError(ctx, a2aMsg, subscriber, err); handleErr != nil {
			log.ErrorfContext(
				ctx,
				"failed to handle streaming error: %v",
				handleErr,
			)
		}
		return true
	}
	produce := func() (*event.Event, bool) {
		select {
		case <-ctx.Done():
			return nil, false
		case agentEvent, ok := <-agentMsgChan:
			if !ok {
				return nil, false
			}
			return agentEvent, true
		}
	}

	// define consume function
	var terminalTaskError bool
	var finalStreamingMetadata map[string]any
	consume := func(batch []*event.Event) (bool, error) {
		return m.processBatchStreamingEvents(
			ctx,
			taskID,
			a2aMsg,
			batch,
			subscriber,
			&terminalTaskError,
			&finalStreamingMetadata,
		)
	}

	taskSubmitted := protocol.NewTaskStatusUpdateEvent(
		taskID, *a2aMsg.ContextID,
		protocol.TaskStatus{
			State:     protocol.TaskStateSubmitted,
			Timestamp: time.Now().Format(time.RFC3339),
		},
		false,
	)
	// Add ADK-compatible metadata if enabled
	m.addTaskMetadata(&taskSubmitted, userID, sessionID)
	if err := subscriber.Send(protocol.StreamingMessageEvent{Result: &taskSubmitted}); err != nil {
		log.ErrorfContext(
			ctx,
			"failed to send task submitted message: %v",
			err,
		)
		if abortStreaming(err) {
			return
		}
	}

	// run event tunnel
	tunnel := newEventTunnel(defaultBatchSize, defaultFlushInterval, produce, consume)
	if err := tunnel.Run(ctx); err != nil {
		log.WarnfContext(ctx, "Event transfer error: %v", err)
		if abortStreaming(err) {
			return
		}
	}
	if terminalTaskError {
		return
	}

	if m.streamingEventType != StreamingEventTypeMessage {
		finalArtifact := protocol.NewTaskArtifactUpdateEvent(
			taskID,
			*a2aMsg.ContextID,
			protocol.Artifact{Parts: []protocol.Part{}},
			true,
		)
		finalArtifact.Metadata = cloneStreamingMetadata(finalStreamingMetadata)
		if err := subscriber.Send(protocol.StreamingMessageEvent{
			Result: &finalArtifact,
		}); err != nil {
			log.ErrorfContext(
				ctx,
				"failed to send final artifact message: %v",
				err,
			)
			if abortStreaming(err) {
				return
			}
		}
	}

	taskCompleted := protocol.NewTaskStatusUpdateEvent(
		taskID, *a2aMsg.ContextID,
		protocol.TaskStatus{
			State:     protocol.TaskStateCompleted,
			Timestamp: time.Now().Format(time.RFC3339),
		},
		true,
	)
	if m.streamingEventType == StreamingEventTypeMessage {
		taskCompleted.Metadata = cloneStreamingMetadata(finalStreamingMetadata)
	}
	if err := subscriber.Send(protocol.StreamingMessageEvent{Result: &taskCompleted}); err != nil {
		log.ErrorfContext(
			ctx,
			"failed to send task completed message: %v",
			err,
		)
		if abortStreaming(err) {
			return
		}
	}
}

// processBatchStreamingEvents processes a batch of streaming events and sends them through msgChan.
func (m *messageProcessor) processBatchStreamingEvents(
	ctx context.Context,
	taskID string,
	a2aMsg *protocol.Message,
	batch []*event.Event,
	subscriber taskmanager.TaskSubscriber,
	terminalTaskError *bool,
	finalStreamingMetadata *map[string]any,
) (bool, error) {
	if len(batch) == 0 {
		log.DebugContext(ctx, "received empty batch, continuing")
		// continue processing
		return true, nil
	}

	for i, agentEvent := range batch {
		// Check context cancellation
		select {
		case <-ctx.Done():
			log.WarnfContext(ctx, "context cancelled during batch processing")
			return false, ctx.Err()
		default:
		}

		if m.debugLogging {
			agentEventJson, _ := json.Marshal(agentEvent)
			log.DebugfContext(
				ctx,
				"get agent event: req msg id: %s, event: %s",
				a2aMsg.MessageID,
				string(agentEventJson),
			)
		}

		if agentEvent == nil {
			log.WarnfContext(
				ctx,
				"received nil event at index %d, skipping",
				i,
			)
			continue
		}

		if agentEvent.Response == nil {
			log.DebugfContext(
				ctx,
				"received empty response event at index %d, "+
					"continuing, event: %v",
				i,
				agentEvent,
			)
			continue
		}

		if m.structuredTaskErrors &&
			isStructuredTaskErrorEvent(agentEvent) {
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
			if err := subscriber.Send(protocol.StreamingMessageEvent{
				Result: &statusEvent,
			}); err != nil {
				return false, fmt.Errorf(
					"failed to send task failure status: %w",
					err,
				)
			}
			if terminalTaskError != nil {
				*terminalTaskError = true
			}
			return false, nil
		}

		if isFinalStreamingEvent(agentEvent) {
			if finalStreamingMetadata != nil {
				*finalStreamingMetadata = buildFinalStreamingMetadata(agentEvent)
			}
			log.DebugfContext(
				ctx,
				"received final event, stopping batch processing "+
					"(eventID=%s)",
				agentEvent.ID,
			)
			return false, nil
		}

		// Convert event to A2A message for streaming
		convertedResult, err := m.eventToA2AConverter.ConvertStreamingToA2AMessage(
			ctx, agentEvent, EventToA2AStreamingOptions{CtxID: *a2aMsg.ContextID, TaskID: taskID},
		)
		if err != nil {
			return false, fmt.Errorf("failed to convert event to A2A message: %w", err)
		}

		if m.debugLogging {
			a2aMsgJson, _ := json.Marshal(convertedResult)
			log.DebugfContext(
				ctx,
				"converted agent event to A2A message: req msg id: %s, "+
					"event: %s",
				a2aMsg.MessageID,
				string(a2aMsgJson),
			)
		}

		// Send message if conversion successful
		if convertedResult != nil {
			if err := subscriber.Send(protocol.StreamingMessageEvent{Result: convertedResult}); err != nil {
				log.ErrorfContext(
					ctx,
					"failed to send streaming message event: %v",
					err,
				)
				return false, fmt.Errorf("failed to send streaming message event: %w", err)
			}
		}

	}

	// Continue processing - need more data
	return true, nil
}

func (m *messageProcessor) processMessage(
	ctx context.Context,
	userID string,
	ctxID string,
	a2aMsg *protocol.Message,
	agentMsg *model.Message,
	runnerOpts []agent.RunOption,
) (*taskmanager.MessageProcessingResult, error) {
	if agentMsg == nil {
		log.ErrorContext(
			ctx,
			"agent message is nil in non-streaming processing",
		)
		return nil, errors.New("a2aserver: agent message is nil")
	}

	agentMsgChan, err := m.runner.Run(ctx, userID, ctxID, *agentMsg, runnerOpts...)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"failed to run agent for user %s, context %s: %v",
			userID,
			ctxID,
			err,
		)
		return m.handleError(ctx, a2aMsg, false, err)
	}

	// Collect converted A2A messages
	var messages []protocol.Message
	var eventCount int

	for agentEvent := range agentMsgChan {
		eventCount++
		if m.structuredTaskErrors &&
			isStructuredTaskErrorEvent(agentEvent) {
			taskID := fmt.Sprintf(
				"task-%s-%d",
				a2aMsg.MessageID,
				time.Now().UnixNano(),
			)
			return &taskmanager.MessageProcessingResult{
				Result: buildStructuredFailureTask(
					taskID,
					ctxID,
					messages,
					agentEvent,
				),
			}, nil
		}
		if err := m.processUnaryEvent(
			ctx,
			a2aMsg,
			agentEvent,
			eventCount,
			&messages,
		); err != nil {
			return m.handleError(ctx, a2aMsg, false, err)
		}
	}

	log.DebugfContext(
		ctx,
		"processed %d events, collected %d messages",
		eventCount,
		len(messages),
	)

	if len(messages) == 0 {
		log.WarnfContext(
			ctx,
			"no response messages from agent after processing %d "+
				"events for message %s",
			eventCount,
			a2aMsg.MessageID,
		)
		return m.handleError(
			ctx,
			a2aMsg,
			false,
			errors.New(
				"no response messages from agent after processing "+
					"events",
			),
		)
	}

	return buildMessageProcessingResult(a2aMsg, ctxID, messages), nil
}

func (m *messageProcessor) processUnaryEvent(
	ctx context.Context,
	a2aMsg *protocol.Message,
	agentEvent *event.Event,
	eventCount int,
	messages *[]protocol.Message,
) error {
	select {
	case <-ctx.Done():
		log.WarnfContext(
			ctx,
			"context cancelled after processing %d events",
			eventCount,
		)
		return ctx.Err()
	default:
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

	if agentEvent == nil || agentEvent.Response == nil {
		log.WarnfContext(
			ctx,
			"received nil event or response at position %d, skipping",
			eventCount,
		)
		return nil
	}

	if shouldSkipRunnerCompletionEvent(agentEvent, *messages) {
		return nil
	}

	convertedResult, err := m.eventToA2AConverter.ConvertToA2AMessage(
		ctx,
		agentEvent,
		EventToA2AUnaryOptions{CtxID: *a2aMsg.ContextID},
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"failed to convert event %d to A2A message: %v",
			eventCount,
			err,
		)
		return err
	}

	if m.debugLogging {
		convertedMsgJSON, _ := json.Marshal(convertedResult)
		log.DebugfContext(
			ctx,
			"converted agent event to A2A message: req msg id: %s, "+
				"event: %s",
			a2aMsg.MessageID,
			string(convertedMsgJSON),
		)
	}

	appendConvertedUnaryResult(messages, convertedResult)
	return nil
}

func shouldSkipRunnerCompletionEvent(
	agentEvent *event.Event,
	messages []protocol.Message,
) bool {
	if !agentEvent.IsRunnerCompletion() || agentEvent.Response.IsValidContent() {
		return false
	}
	if len(agentEvent.StateDelta) == 0 {
		return true
	}
	// Keep runner-completion state delta, but merge it into the latest
	// converted message to avoid turning metadata-only completion into
	// the final artifact message.
	return mergeRunnerCompletionStateDeltaIntoLastMessage(
		messages,
		agentEvent.StateDelta,
	)
}

func appendConvertedUnaryResult(
	messages *[]protocol.Message,
	convertedResult any,
) {
	if convertedResult == nil {
		return
	}
	if msg, ok := convertedResult.(*protocol.Message); ok {
		*messages = append(*messages, *msg)
	}
	if task, ok := convertedResult.(*protocol.Task); ok {
		// Extract messages from task artifacts.
		for _, artifact := range task.Artifacts {
			artifactMsg := protocol.NewMessage(protocol.MessageRoleAgent, artifact.Parts)
			artifactMsg.Metadata = artifact.Metadata
			*messages = append(*messages, artifactMsg)
		}
	}
}

func buildMessageProcessingResult(
	a2aMsg *protocol.Message,
	ctxID string,
	messages []protocol.Message,
) *taskmanager.MessageProcessingResult {
	// If only one message, return it directly.
	if len(messages) == 1 {
		return &taskmanager.MessageProcessingResult{
			Result: &messages[0],
		}
	}

	// Multiple messages: return a Task with history containing
	// intermediate messages and the final message in artifacts.
	taskID := fmt.Sprintf("task-%s-%d", a2aMsg.MessageID, time.Now().UnixNano())
	task := protocol.NewTask(taskID, ctxID)
	task.Status = protocol.TaskStatus{
		State:     protocol.TaskStateCompleted,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	task.History = messages[:len(messages)-1]
	lastMsg := messages[len(messages)-1]
	task.Artifacts = []protocol.Artifact{{
		ArtifactID: lastMsg.MessageID,
		Parts:      lastMsg.Parts,
		Metadata:   lastMsg.Metadata,
	}}

	return &taskmanager.MessageProcessingResult{Result: task}
}

func mergeRunnerCompletionStateDeltaIntoLastMessage(
	messages []protocol.Message,
	stateDelta map[string][]byte,
) bool {
	if len(messages) == 0 || len(stateDelta) == 0 {
		return false
	}

	lastMsg := &messages[len(messages)-1]
	if lastMsg.Metadata == nil {
		lastMsg.Metadata = make(map[string]any, 1)
	}

	mergedStateDelta := make(map[string][]byte, len(stateDelta))
	if existingRaw, ok := lastMsg.Metadata[ia2a.MessageMetadataStateDeltaKey]; ok {
		existingStateDelta := DecodeStateDeltaMetadata(existingRaw)
		for key, value := range existingStateDelta {
			mergedStateDelta[key] = cloneStateDeltaBytes(value)
		}
	}
	for key, value := range stateDelta {
		mergedStateDelta[key] = cloneStateDeltaBytes(value)
	}

	encoded := EncodeStateDeltaMetadata(mergedStateDelta)
	if len(encoded) == 0 {
		return false
	}
	lastMsg.Metadata[ia2a.MessageMetadataStateDeltaKey] = encoded
	return true
}

func cloneStateDeltaBytes(raw []byte) []byte {
	if raw == nil {
		return nil
	}
	copied := make([]byte, len(raw))
	copy(copied, raw)
	return copied
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
