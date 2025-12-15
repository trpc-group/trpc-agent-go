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
	"net/url"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
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
		errorHandler:     defaultErrorHandler,
		adkCompatibility: true, // Enable ADK compatibility by default
	}
	for _, opt := range opts {
		opt(options)
	}

	if options.sessionService == nil {
		options.sessionService = inmemory.NewSessionService()
	}

	if options.agent == nil {
		return nil, errors.New("agent is required")
	}

	// Host is only required if we need to build an agent card
	// If user provides a custom agent card, host is optional
	if options.agentCard == nil && options.host == "" {
		return nil, errors.New("host is required when agent card is not provided")
	}

	return buildA2AServer(options)
}

func buildAgentCard(options *options) a2a.AgentCard {
	if options.agentCard != nil {
		return *options.agentCard
	}
	agent := options.agent
	desc := agent.Info().Description
	name := agent.Info().Name

	// Normalize the host to ensure it has a proper URL scheme
	url := ia2a.NormalizeURL(options.host)

	// Build skills from agent tools
	skills := buildSkillsFromTools(agent, name, desc)
	return a2a.AgentCard{
		Name:        name,
		Description: desc,
		URL:         url,
		Capabilities: a2a.AgentCapabilities{
			Streaming: &options.enableStreaming,
		},
		Skills:             skills,
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}
}

func buildProcessor(agent agent.Agent, sessionService session.Service, options *options) *messageProcessor {
	agentName := agent.Info().Name
	runner := runner.NewRunner(agentName, agent, runner.WithSessionService(sessionService))

	// Use custom converters if provided, otherwise use defaults
	a2aToAgentConverter := options.a2aToAgentConverter
	if a2aToAgentConverter == nil {
		a2aToAgentConverter = &defaultA2AMessageToAgentMessage{}
	}

	eventToA2AConverter := options.eventToA2AConverter
	if eventToA2AConverter == nil {
		eventToA2AConverter = &defaultEventToA2AMessage{adkCompatibility: options.adkCompatibility}
	}

	return &messageProcessor{
		runner:              runner,
		a2aToAgentConverter: a2aToAgentConverter,
		eventToA2AConverter: eventToA2AConverter,
		errorHandler:        options.errorHandler,
		debugLogging:        options.debugLogging,
		adkCompatibility:    options.adkCompatibility,
		agentName:           agentName,
	}
}

func buildA2AServer(options *options) (*a2a.A2AServer, error) {
	agent := options.agent
	sessionService := options.sessionService

	agentCard := buildAgentCard(options)

	var processor taskmanager.MessageProcessor
	if options.processorBuilder != nil {
		processor = options.processorBuilder(agent, sessionService)
	} else {
		processor = buildProcessor(agent, sessionService, options)
	}

	if options.processorHook != nil {
		processor = options.processorHook(processor)
	}

	// Create a task manager that wraps the session service
	var taskManager taskmanager.TaskManager
	var err error
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
	}
	opts = append(opts, options.extraOptions...)
	a2aServer, err := a2a.NewA2AServer(agentCard, taskManager, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create a2a server: %w", err)
	}
	return a2aServer, nil
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
	runner              runner.Runner
	a2aToAgentConverter A2AMessageToAgentMessage
	eventToA2AConverter EventToA2AMessage
	errorHandler        ErrorHandler
	debugLogging        bool
	adkCompatibility    bool
	agentName           string
}

func isFinalStreamingEvent(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}

	// The only truly final event is runner.completion
	// This ensures we don't miss postprocessing events (code execution, etc.)
	return evt.IsRunnerCompletion()
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

	runnerOpts := []agent.RunOption{
		agent.WithRuntimeState(message.Metadata),
	}

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

	subscriber, err := handler.SubscribeTask(&taskID)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"failed to subscribe to task %s: %v",
			taskID,
			err,
		)
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
	consume := func(batch []*event.Event) (bool, error) {
		return m.processBatchStreamingEvents(ctx, taskID, a2aMsg, batch, subscriber)
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
		m.handleStreamingProcessingError(ctx, a2aMsg, subscriber, err)
	}

	// run event tunnel
	tunnel := newEventTunnel(defaultBatchSize, defaultFlushInterval, produce, consume)
	if err := tunnel.Run(ctx); err != nil {
		log.WarnfContext(ctx, "Event transfer error: %v", err)
		m.handleStreamingProcessingError(ctx, a2aMsg, subscriber, err)
	}

	finalArtifact := protocol.NewTaskArtifactUpdateEvent(taskID, *a2aMsg.ContextID, protocol.Artifact{
		Parts: []protocol.Part{},
	}, true)
	if err := subscriber.Send(protocol.StreamingMessageEvent{Result: &finalArtifact}); err != nil {
		log.ErrorfContext(
			ctx,
			"failed to send final artifact message: %v",
			err,
		)
		m.handleStreamingProcessingError(ctx, a2aMsg, subscriber, err)
	}

	taskCompleted := protocol.NewTaskStatusUpdateEvent(
		taskID, *a2aMsg.ContextID,
		protocol.TaskStatus{
			State:     protocol.TaskStateCompleted,
			Timestamp: time.Now().Format(time.RFC3339),
		},
		true,
	)
	if err := subscriber.Send(protocol.StreamingMessageEvent{Result: &taskCompleted}); err != nil {
		log.ErrorfContext(
			ctx,
			"failed to send task completed message: %v",
			err,
		)
		m.handleStreamingProcessingError(ctx, a2aMsg, subscriber, err)
	}
}

// processBatchStreamingEvents processes a batch of streaming events and sends them through msgChan.
func (m *messageProcessor) processBatchStreamingEvents(
	ctx context.Context,
	taskID string,
	a2aMsg *protocol.Message,
	batch []*event.Event,
	subscriber taskmanager.TaskSubscriber,
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

		// Check if this is the final event - stop processing if done
		if isFinalStreamingEvent(agentEvent) {
			log.DebugfContext(
				ctx,
				"received final event, stopping batch processing "+
					"(eventID=%s)",
				agentEvent.ID,
			)
			return false, nil
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

		// Check context cancellation
		select {
		case <-ctx.Done():
			log.WarnfContext(
				ctx,
				"context cancelled after processing %d events",
				eventCount,
			)
			return m.handleError(ctx, a2aMsg, false, ctx.Err())
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

		if agentEvent == nil || agentEvent.Response == nil {
			log.WarnfContext(
				ctx,
				"received nil event or response at position %d, skipping",
				eventCount,
			)
			continue
		}

		convertedResult, err := m.eventToA2AConverter.ConvertToA2AMessage(
			ctx, agentEvent, EventToA2AUnaryOptions{CtxID: *a2aMsg.ContextID},
		)
		if err != nil {
			log.ErrorfContext(
				ctx,
				"failed to convert event %d to A2A message: %v",
				eventCount,
				err,
			)
			return m.handleError(ctx, a2aMsg, false, err)
		}

		if m.debugLogging {
			convertedMsgJson, _ := json.Marshal(convertedResult)
			log.DebugfContext(
				ctx,
				"converted agent event to A2A message: req msg id: %s, "+
					"event: %s",
				a2aMsg.MessageID,
				string(convertedMsgJson),
			)
		}

		if convertedResult != nil {
			if msg, ok := convertedResult.(*protocol.Message); ok {
				messages = append(messages, *msg)
			}
			if task, ok := convertedResult.(*protocol.Task); ok {
				// Extract messages from task artifacts.
				for _, artifact := range task.Artifacts {
					artifactMsg := protocol.NewMessage(
						protocol.MessageRoleAgent,
						artifact.Parts,
					)
					messages = append(messages, artifactMsg)
				}
			}
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

	// If only one message, return it directly
	// If multiple messages, return a Task with history
	if len(messages) == 1 {
		return &taskmanager.MessageProcessingResult{
			Result: &messages[0],
		}, nil
	}

	// Multiple messages: return Task with history containing intermediate messages
	// and final message in artifacts
	taskID := fmt.Sprintf("task-%s-%d", a2aMsg.MessageID, time.Now().UnixNano())
	task := protocol.NewTask(taskID, ctxID)
	task.Status = protocol.TaskStatus{
		State:     protocol.TaskStateCompleted,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// Put all messages except the last one in history
	if len(messages) > 1 {
		task.History = messages[:len(messages)-1]
	}

	// Put the last message in artifacts
	lastMsg := messages[len(messages)-1]
	task.Artifacts = []protocol.Artifact{{
		ArtifactID: lastMsg.MessageID,
		Parts:      lastMsg.Parts,
	}}

	return &taskmanager.MessageProcessingResult{
		Result: task,
	}, nil
}

// buildSkillsFromTools converts agent tools to AgentSkills
func buildSkillsFromTools(agent agent.Agent, agentName, agentDesc string) []a2a.AgentSkill {
	tools := agent.Tools()
	if len(tools) == 0 {
		// If no tools, create a default skill
		return []a2a.AgentSkill{
			{
				Name:        agentName,
				Description: &agentDesc,
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
				Tags:        []string{"default"},
			},
		}
	}

	skills := make([]a2a.AgentSkill, 0, len(tools)+1)

	// Add default agent skill
	skills = append(skills, a2a.AgentSkill{
		Name:        agentName,
		Description: &agentDesc,
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		Tags:        []string{"default"},
	})

	// Add tool-based skills
	for _, tool := range tools {
		decl := tool.Declaration()
		if decl != nil {
			skill := a2a.AgentSkill{
				Name:        decl.Name,
				Description: &decl.Description,
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
				Tags:        []string{"tool"},
			}
			skills = append(skills, skill)
		}
	}

	return skills
}

// addTaskMetadata adds ADK-compatible metadata to task status update events.
// When ADK compatibility mode is enabled, it adds metadata with "adk_" prefix
// (e.g., "adk_app_name", "adk_user_id", "adk_session_id") to match ADK Python implementation.
func (m *messageProcessor) addTaskMetadata(event *protocol.TaskStatusUpdateEvent, userID, sessionID string) {
	if !m.adkCompatibility {
		return
	}

	if event.Metadata == nil {
		event.Metadata = make(map[string]any)
	}

	// Add ADK-compatible metadata keys
	event.Metadata[ia2a.GetADKMetadataKey("app_name")] = m.agentName
	event.Metadata[ia2a.GetADKMetadataKey("user_id")] = userID
	event.Metadata[ia2a.GetADKMetadataKey("session_id")] = sessionID
}
