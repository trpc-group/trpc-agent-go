// package a2a provides utilities for creating a2a servers.
package a2a

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// New creates a new a2a server.
func New(opts ...Option) (*a2a.A2AServer, error) {
	options := defaultOptions
	for _, opt := range opts {
		opt(options)
	}

	if options.sessionService == nil {
		options.sessionService = inmemory.NewSessionService()
	}

	if options.agent == nil {
		return nil, errors.New("agent is required")
	}

	if options.host == "" {
		return nil, errors.New("host is required")
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
	url := fmt.Sprintf("http://%s", options.host)
	return a2a.AgentCard{
		Name:        name,
		Description: desc,
		URL:         url,
		Capabilities: a2a.AgentCapabilities{
			Streaming: boolPtr(true),
		},
		Skills: []a2a.AgentSkill{
			{
				Name:        name,
				Description: &desc,
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
				Tags:        []string{"default"},
			},
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}
}

func buildProcessor(agent agent.Agent, sessionService session.Service) *messageProcessor {
	agentName := agent.Info().Name
	runner := runner.NewRunner(agentName, agent, runner.WithSessionService(sessionService))
	return &messageProcessor{
		runner: runner,
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
		processor = buildProcessor(agent, sessionService)
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

	opts := []a2a.Option{
		a2a.WithAuthProvider(&defautAuthProvider{}),
	}

	// if other AuthProvider is set, user info should be covered
	opts = append(opts, options.extraOptions...)
	a2aServer, err := a2a.NewA2AServer(agentCard, taskManager, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create a2a server: %w", err)
	}
	return a2aServer, nil
}

// messageProcessor is the message processor for the a2a server.
type messageProcessor struct {
	runner runner.Runner
}

// ProcessMessage is the main entry point for processing messages.
func (m *messageProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	user, ok := ctx.Value(auth.AuthUserKey).(*auth.User)
	if !ok {
		return nil, errors.New("userID is required")
	}
	if message.ContextID == nil {
		return nil, errors.New("context id not exists")
	}

	userID := user.ID
	ctxID := *message.ContextID
	if options.Streaming {
		return m.processStreamingMessage(ctx, userID, ctxID, message, options, handler)
	}
	return m.processMessage(ctx, userID, ctxID, message, options, handler)
}

func (m *messageProcessor) processStreamingMessage(
	ctx context.Context,
	userID string,
	ctxID string,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	text := extractTextFromMessage(message)
	if text == "" {
		errMsg := "input message must contain text."
		log.Errorf("Message processing failed: %s", errMsg)
		errorMessage := protocol.NewMessage(
			protocol.MessageRoleAgent,
			[]protocol.Part{protocol.NewTextPart(errMsg)},
		)
		return &taskmanager.MessageProcessingResult{
			Result: &errorMessage,
		}, nil
	}

	// For streaming processing, create a task and subscribe to it
	taskID, err := handler.BuildTask(nil, &ctxID)
	if err != nil {
		return nil, fmt.Errorf("failed to build task: %w", err)
	}

	// Subscribe to the task for streaming events
	subscriber, err := handler.SubscribeTask(&taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to task: %w", err)
	}

	// Start processing in a goroutine
	go func() {
		defer func() {
			if subscriber != nil {
				subscriber.Close()
			}
			handler.CleanTask(&taskID)
		}()

		if err = sendTaskUpdateEvent(handler, taskID, protocol.TaskStateSubmitted, ""); err != nil {
			log.Errorf("failed to update task state: %v", err)
			return
		}

		// Send task status update - working
		if err = sendTaskUpdateEvent(handler, taskID, protocol.TaskStateWorking, ""); err != nil {
			log.Errorf("failed to update task state: %v", err)
			return
		}

		// Run the agent and process streaming events
		agentMsg := model.NewUserMessage(text)
		agentMsgChan, err := m.runner.Run(ctx, userID, ctxID, agentMsg)
		if err != nil {
			log.Errorf("Failed to run agent: %v", err)
			if err = sendTaskUpdateEvent(handler, taskID, protocol.TaskStateFailed, ""); err != nil {
				log.Errorf("failed to update task state: %v", err)
			}
			return
		}

		// Process streaming events from agent
		m.processAgentStreamingEvent(ctx, taskID, handler, agentMsgChan)
	}()

	return &taskmanager.MessageProcessingResult{
		StreamingEvents: subscriber,
	}, nil
}

// processAgentStreamingEvent handles streaming events from the agent runner.
func (m *messageProcessor) processAgentStreamingEvent(
	ctx context.Context,
	taskID string,
	handler taskmanager.TaskHandler,
	agentMsgChan <-chan *event.Event,
) {
	// define produce function
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
		return m.processBatchEvents(taskID, handler, batch)
	}

	tunnel := newEventTunnel(defaultBatchSize, defaultFlushInterval, produce, consume)
	err := tunnel.Run(ctx)

	if err != nil {
		log.Warnf("Event tunnel error: %v", err)
		if sendErr := sendTaskUpdateEvent(handler, taskID, protocol.TaskStateFailed, ""); sendErr != nil {
			log.Errorf("Failed to send error state: %v", sendErr)
		}
		return
	}

	if err := sendTaskUpdateEvent(handler, taskID, protocol.TaskStateCompleted, ""); err != nil {
		log.Errorf("Failed to send completion state: %v", err)
	}
}

// processBatchEvents 批量处理事件
func (m *messageProcessor) processBatchEvents(taskID string, handler taskmanager.TaskHandler, batch []*event.Event) (bool, error) {
	var batchContent strings.Builder
	var hasFinalEvent bool
	var processErr error

	for _, agentEvent := range batch {
		if agentEvent.Response == nil {
			continue
		}
		if err := m.processSingleEvent(agentEvent, &batchContent); err != nil {
			processErr = err
			break
		}

		if agentEvent.Response.Done {
			hasFinalEvent = true
			break
		}
	}

	if batchContent.Len() > 0 {
		if err := sendTaskArtifactUpdateEvent(handler, taskID, batchContent.String(), hasFinalEvent); err != nil {
			return false, fmt.Errorf("failed to send batch artifact: %w", err)
		}
	}

	if hasFinalEvent {
		// return false to stop the tunnel
		return false, nil
	}

	if processErr != nil {
		// return false to stop the tunnel
		return false, fmt.Errorf("failed to process batch events: %w", processErr)
	}

	return true, nil
}

// processSingleEvent process single event
func (m *messageProcessor) processSingleEvent(agentEvent *event.Event, batchContent *strings.Builder) error {
	if agentEvent.Response.Error != nil {
		log.Infof("Agent error: %s", agentEvent.Response.Error.Message)
		return nil
	}

	if len(agentEvent.Response.Choices) > 0 {
		choice := agentEvent.Response.Choices[0]
		var content string

		// Smart content extraction: prefer Delta for streaming, Message for non-streaming
		if choice.Delta.Content != "" {
			// Streaming mode: use delta content
			content = choice.Delta.Content
		} else if choice.Message.Content != "" {
			// Non-streaming mode: use message content
			content = choice.Message.Content
		}

		if content != "" {
			batchContent.WriteString(content)
		}
	}
	return nil
}

func (m *messageProcessor) processMessage(
	ctx context.Context,
	userID string,
	ctxID string,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	text := extractTextFromMessage(message)
	if text == "" {
		message := protocol.NewMessage(protocol.MessageRoleUser, []protocol.Part{
			protocol.NewTextPart("input is empty!"),
		})
		return &taskmanager.MessageProcessingResult{
			Result: &message,
		}, nil
	}

	agentMsg := model.NewUserMessage(text)
	agentMsgChan, err := m.runner.Run(ctx, userID, ctxID, agentMsg)
	if err != nil {
		log.Errorf("failed to run agent: %v", err)
		return nil, err
	}

	content, err := processAgentResponse(agentMsgChan)
	if err != nil {
		log.Errorf("failed to process agent streaming response: %v", err)
		return nil, err
	}

	result := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart(content)})
	return &taskmanager.MessageProcessingResult{
		Result: &result,
	}, nil
}

// ExtractTextFromMessage extracts the text content from a message.
func extractTextFromMessage(message protocol.Message) string {
	for _, part := range message.Parts {
		if textPart, ok := part.(*protocol.TextPart); ok {
			return textPart.Text
		}
	}
	return ""
}

// ProcessStreamingResponse handles the streaming response with tool call visualization.
func processAgentResponse(eventChan <-chan *event.Event) (string, error) {
	var (
		fullContent string
	)

	for event := range eventChan {
		if event.Error != nil {
			log.Errorf("streaming process error: %v", event.Error)
			continue
		}

		// Detect and display tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				log.Debugf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					log.Debugf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			// Smart content extraction: prefer Delta for streaming, Message for non-streaming
			if choice.Delta.Content != "" {
				// Streaming mode: use delta content
				fullContent += choice.Delta.Content
			} else if choice.Message.Content != "" {
				// Non-streaming mode: use message content
				fullContent += choice.Message.Content
			}
		}
		if event.Done {
			break
		}
	}
	return fullContent, nil
}

func boolPtr(b bool) *bool {
	return &b
}

func sendTaskUpdateEvent(handler taskmanager.TaskHandler, taskID string, state protocol.TaskState, msg string) error {
	if len(msg) == 0 {
		return handler.UpdateTaskState(&taskID, state, nil)
	}

	return handler.UpdateTaskState(&taskID, state, &protocol.Message{
		Role: protocol.MessageRoleAgent,
		Parts: []protocol.Part{
			protocol.NewTextPart(msg),
		},
	})
}

func sendTaskArtifactUpdateEvent(handler taskmanager.TaskHandler, taskID string, content string, final bool) error {
	return handler.AddArtifact(&taskID, protocol.Artifact{Parts: []protocol.Part{protocol.NewTextPart(content)}}, final, false)
}
