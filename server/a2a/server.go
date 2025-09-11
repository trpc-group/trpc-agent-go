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
	"errors"
	"fmt"

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
	options := &options{}
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
		eventToA2AConverter = &defaultEventToA2AMessage{}
	}

	return &messageProcessor{
		runner:              runner,
		a2aToAgentConverter: a2aToAgentConverter,
		eventToA2AConverter: eventToA2AConverter,
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

	opts := []a2a.Option{
		a2a.WithAuthProvider(&defaultAuthProvider{}),
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
	runner              runner.Runner
	a2aToAgentConverter A2AMessageToAgentMessage
	eventToA2AConverter EventToA2AMessage
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

	// Convert A2A message to agent message
	agentMsg, err := m.a2aToAgentConverter.ConvertToAgentMessage(ctx, message)
	if err != nil {
		return nil, fmt.Errorf("failed to convert A2A message to agent message: %w", err)
	}

	runnerOpts := []agent.RunOption{
		agent.WithRuntimeState(message.Metadata),
	}

	if options.Streaming {
		return m.processStreamingMessage(ctx, userID, ctxID, agentMsg, handler, runnerOpts)
	}
	return m.processMessage(ctx, userID, ctxID, agentMsg, handler, runnerOpts)
}

func (m *messageProcessor) processStreamingMessage(
	ctx context.Context,
	userID string,
	ctxID string,
	agentMsg *model.Message,
	handler taskmanager.TaskHandler,
	runnerOpts []agent.RunOption,
) (*taskmanager.MessageProcessingResult, error) {
	if agentMsg == nil {
		return nil, errors.New("a2aserver: agent message is nil")
	}

	taskID, err := handler.BuildTask(nil, &ctxID)
	if err != nil {
		return nil, fmt.Errorf("failed to build task: %w", err)
	}

	subscriber, err := handler.SubscribeTask(&taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to task: %w", err)
	}

	// Run the agent and get streaming events
	agentMsgChan, err := m.runner.Run(ctx, userID, ctxID, *agentMsg, runnerOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to run agent: %w", err)
	}

	// Start processing in a goroutine
	go func() {
		m.processAgentStreamingEvents(ctx, agentMsgChan, subscriber)
	}()

	return &taskmanager.MessageProcessingResult{
		StreamingEvents: subscriber,
	}, nil
}

// processAgentStreamingEvents handles streaming events from the agent runner using tunnel for batch processing.
func (m *messageProcessor) processAgentStreamingEvents(
	ctx context.Context,
	agentMsgChan <-chan *event.Event,
	subscriber taskmanager.TaskSubscriber,
) {
	defer subscriber.Close()
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
		return m.processBatchStreamingEvents(ctx, batch, subscriber)
	}

	// run event tunnel
	tunnel := newEventTunnel(defaultBatchSize, defaultFlushInterval, produce, consume)
	if err := tunnel.Run(ctx); err != nil {
		log.Warnf("Event tunnel error: %v", err)
		return
	}
}

// processBatchStreamingEvents processes a batch of streaming events and sends them through msgChan.
func (m *messageProcessor) processBatchStreamingEvents(
	ctx context.Context,
	batch []*event.Event,
	subscriber taskmanager.TaskSubscriber,
) (bool, error) {
	hasFinalEvent := false
	for _, agentEvent := range batch {
		if agentEvent.Response == nil {
			continue
		}

		// Convert event to A2A message for streaming
		a2aMsg, err := m.eventToA2AConverter.ConvertStreamingToA2AMessage(ctx, agentEvent)
		if err != nil {
			log.Errorf("Failed to convert event to A2A message: %v", err)
			continue
		}
		if a2aMsg != nil {
			if err := subscriber.Send(protocol.StreamingMessageEvent{Result: a2aMsg}); err != nil {
				log.Errorf("Failed to send message event: %v", err)
			}
		}

		// Check if this is the final event
		if agentEvent.Response.Done {
			hasFinalEvent = true
			break
		}
	}

	// Return false to stop tunnel if we encountered the final event
	return !hasFinalEvent, nil
}

func (m *messageProcessor) processMessage(
	ctx context.Context,
	userID string,
	ctxID string,
	agentMsg *model.Message,
	handler taskmanager.TaskHandler,
	runnerOpts []agent.RunOption,
) (*taskmanager.MessageProcessingResult, error) {
	if agentMsg == nil {
		return nil, errors.New("a2aserver: agent message is nil")
	}

	agentMsgChan, err := m.runner.Run(ctx, userID, ctxID, *agentMsg, runnerOpts...)
	if err != nil {
		log.Errorf("failed to run agent: %v", err)
		return nil, err
	}

	// Collect and convert events to A2A message
	var allParts []protocol.Part
	for agentEvent := range agentMsgChan {
		a2aMsg, err := m.eventToA2AConverter.ConvertToA2AMessage(ctx, agentEvent)
		if err != nil {
			log.Errorf("failed to convert event to A2A message: %v", err)
			continue
		}
		if a2aMsg != nil {
			allParts = append(allParts, a2aMsg.Parts...)
		}
	}

	var a2aMsg *protocol.Message
	if len(allParts) > 0 {
		msg := protocol.NewMessage(protocol.MessageRoleAgent, allParts)
		a2aMsg = &msg
	} else {
		log.Warnf("no response from agent, return empty message")
		msg := protocol.NewMessage(protocol.MessageRoleAgent, allParts)
		a2aMsg = &msg
	}

	return &taskmanager.MessageProcessingResult{
		Result: a2aMsg,
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
