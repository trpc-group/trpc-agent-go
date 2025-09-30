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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var defaultStreamingChannelSize = 1024
var defaultNonStreamingChannelSize = 10

const (
	// AgentCardWellKnownPath is the standard path for agent card discovery
	AgentCardWellKnownPath = "/.well-known/agent.json"
	// defaultFetchTimeout is the default timeout for fetching agent card
	defaultFetchTimeout = 30 * time.Second
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
	transferStateKey     []string               // Keysa in session state to transfer to the A2A agent message by metadata

	httpClient *http.Client
	a2aClient  *client.A2AClient
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

	if agent.agentURL != "" && agent.agentCard == nil {
		agentCard, err := agent.resolveAgentCardFromURL()
		if err != nil {
			return nil, fmt.Errorf("failed to resolve agent card: %w", err)
		}
		agent.agentCard = agentCard
	}

	if agent.agentCard == nil {
		return nil, fmt.Errorf("agent card not set")
	}

	a2aClient, err := client.NewA2AClient(agent.agentCard.URL, agent.extraA2AOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create A2A client for %s: %w", agent.agentCard.URL, err)
	}
	agent.a2aClient = a2aClient
	return agent, nil
}

// resolveAgentCardFromURL fetches agent card from the well-known path
func (r *A2AAgent) resolveAgentCardFromURL() (*server.AgentCard, error) {
	agentURL := r.agentURL

	// Construct the agent card discovery URL
	agentCardURL := strings.TrimSuffix(agentURL, "/") + AgentCardWellKnownPath

	// Create HTTP client if not set
	httpClient := r.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultFetchTimeout}
	}

	// Fetch agent card from well-known path
	resp, err := httpClient.Get(agentCardURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch agent card from %s: %w", agentCardURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch agent card from %s: HTTP %d", agentCardURL, resp.StatusCode)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent card response: %w", err)
	}

	// Parse agent card JSON
	var agentCard server.AgentCard
	if err := json.Unmarshal(body, &agentCard); err != nil {
		return nil, fmt.Errorf("failed to parse agent card JSON: %w", err)
	}

	if r.name == "" {
		r.name = agentCard.Name
	}

	if r.description == "" {
		r.description = agentCard.Description
	}
	// If URL is not set in the agent card, use the provided agent URL.
	if agentCard.URL == "" {
		agentCard.URL = agentURL
	}
	return &agentCard, nil
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

// Run implements the Agent interface
func (r *A2AAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if r.a2aClient == nil {
		return nil, fmt.Errorf("A2A client not initialized")
	}
	useStreaming := r.shouldUseStreaming()
	if useStreaming {
		return r.runStreaming(ctx, invocation)
	}
	return r.runNonStreaming(ctx, invocation)
}

// shouldUseStreaming determines whether to use streaming protocol
func (r *A2AAgent) shouldUseStreaming() bool {
	// Check if agent card supports streaming
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
	go func() {
		defer close(eventChan)

		a2aMessage, err := r.buildA2AMessage(invocation, true)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("failed to construct A2A message: %v", err))
			return
		}
		params := protocol.SendMessageParams{
			Message: *a2aMessage,
		}
		streamChan, err := r.a2aClient.StreamMessage(ctx, params)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("A2A streaming request failed to %s: %v", r.agentCard.URL, err))
			return
		}

		var aggregatedContentBuilder strings.Builder
		for streamEvent := range streamChan {
			if err := agent.CheckContextCancelled(ctx); err != nil {
				return
			}

			evt, err := r.eventConverter.ConvertStreamingToEvent(streamEvent, r.name, invocation)
			if err != nil {
				r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("custom event converter failed: %v", err))
				return
			}

			// Aggregate content from delta
			if evt.Response != nil && len(evt.Response.Choices) > 0 {
				if r.streamingRespHandler != nil {
					content, err := r.streamingRespHandler(evt.Response)
					if err != nil {
						r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("streaming resp handler failed: %v", err))
						return
					}
					if content != "" {
						aggregatedContentBuilder.WriteString(content)
					}
				} else if evt.Response.Choices[0].Delta.Content != "" {
					aggregatedContentBuilder.WriteString(evt.Response.Choices[0].Delta.Content)
				}
			}

			agent.EmitEvent(ctx, invocation, eventChan, evt)
		}

		agent.EmitEvent(ctx, invocation, eventChan, event.New(
			invocation.InvocationID,
			r.name,
			event.WithResponse(&model.Response{
				Done:      true,
				IsPartial: false,
				Timestamp: time.Now(),
				Created:   time.Now().Unix(),
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: aggregatedContentBuilder.String(),
					},
				}},
			}),
		))
	}()
	return eventChan, nil
}

// runNonStreaming handles non-streaming A2A communication
func (r *A2AAgent) runNonStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, defaultNonStreamingChannelSize)
	go func() {
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
		result, err := r.a2aClient.SendMessage(ctx, params)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("A2A request failed to %s: %v", r.agentCard.URL, err))
			return
		}

		// Try custom event converters first
		msgResult := protocol.MessageResult{Result: result.Result}
		evt, err := r.eventConverter.ConvertToEvent(msgResult, r.name, invocation)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("custom event converter failed: %v", err))
			return
		}

		agent.EmitEvent(ctx, invocation, eventChan, evt)
	}()
	return eventChan, nil
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
