//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

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

const (
	// AgentCardWellKnownPath is the standard path for agent card discovery
	AgentCardWellKnownPath = "/.well-known/agent.json"
	// A2AMetadataPrefix is the prefix for A2A-specific metadata
	A2AMetadataPrefix = "a2a:"
	// defaultFetchTimeout is the default timeout for fetching agent card
	defaultFetchTimeout = 30 * time.Second
)

// A2AAgent is an agent that communicates with a remote A2A agent via A2A protocol.
type A2AAgent struct {
	name        string
	description string

	// Agent card and resolution state
	agentCard *server.AgentCard
	agentURL  string

	// HTTP client configuration
	httpClient *http.Client

	// A2A client
	a2aClient *client.A2AClient
}

// New creates a new A2AAgent.
//
// The agent can be configured with:
// - A *server.AgentCard object
// - A URL string to A2A endpoint
func New(opts ...Option) (*A2AAgent, error) {
	agent := &A2AAgent{}

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

	a2aClientOpts := []client.Option{}
	if agent.httpClient != nil {
		a2aClientOpts = append(a2aClientOpts, client.WithHTTPClient(agent.httpClient))
	}
	// Initialize A2A client
	a2aClient, err := client.NewA2AClient(agent.agentCard.URL, a2aClientOpts...)
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

	return &agentCard, nil
}

// buildA2AParts converts event response to A2A parts
func (r *A2AAgent) buildA2AParts(ev *event.Event) []protocol.Part {
	var parts []protocol.Part

	if ev.Response == nil || len(ev.Response.Choices) == 0 {
		return parts
	}

	// Extract content from the first choice
	for _, choice := range ev.Response.Choices {
		var content string

		// Get content from either delta or message
		if choice.Delta.Content != "" {
			content = choice.Delta.Content
		} else if choice.Message.Content != "" {
			content = choice.Message.Content
		}

		if content != "" {
			parts = append(parts, protocol.NewTextPart(content))
		}
	}
	return parts
}

// buildA2AMessage constructs A2A message from session events
func (r *A2AAgent) buildA2AMessage(
	_ context.Context,
	invocation *agent.Invocation,
) (*protocol.Message, error) {
	var parts []protocol.Part

	parts = append(parts, protocol.NewTextPart(invocation.Message.Content))
	// Get recent events that are not from this agent
	events := invocation.Session.Events
	for i := len(events) - 1; i >= 0; i-- {
		ev := &events[i]
		if ev.Author == r.name {
			// Stop when we encounter our own message
			break
		}

		// Convert event content to A2A parts
		eventParts := r.buildA2AParts(ev)
		parts = append(eventParts, parts...) // Prepend to maintain order
	}

	if len(parts) == 0 {
		// If no content, create an empty text part
		parts = append(parts, protocol.NewTextPart(""))
	}

	message := protocol.NewMessage(protocol.MessageRoleUser, parts)
	return &message, nil
}

// buildRespEvent converts A2A response to tRPC event
func (r *A2AAgent) buildRespEvent(response *protocol.Message, invocation *agent.Invocation) *event.Event {
	if response == nil {
		return &event.Event{
			Author:       r.name,
			InvocationID: invocation.InvocationID,
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: ""}}}},
		}
	}

	// Extract text content from A2A response
	var content string
	for _, part := range response.Parts {
		if textPart, ok := part.(*protocol.TextPart); ok {
			content += textPart.Text
		}
	}

	// Create response event
	ev := &event.Event{
		Author:       r.name,
		InvocationID: invocation.InvocationID,
		Response: &model.Response{
			Choices:   []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: content}}},
			Done:      true,
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
		},
	}

	return ev
}

// sendErrorEvent sends an error event to the event channel
func (r *A2AAgent) sendErrorEvent(eventChan chan<- *event.Event, invocation *agent.Invocation, errorMessage string) {
	eventChan <- &event.Event{
		Author:       r.name,
		InvocationID: invocation.InvocationID,
		Response: &model.Response{
			Error: &model.ResponseError{
				Message: errorMessage,
			},
		},
	}
}

// Run implements the Agent interface
func (r *A2AAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if r.a2aClient == nil {
		return nil, fmt.Errorf("A2A client not initialized")
	}

	eventChan := make(chan *event.Event, 1)
	go func() {
		defer close(eventChan)

		// Construct A2A message from session
		a2aMessage, err := r.buildA2AMessage(ctx, invocation)
		if err != nil {
			r.sendErrorEvent(eventChan, invocation, fmt.Sprintf("failed to construct A2A message: %v", err))
			return
		}

		params := protocol.SendMessageParams{
			Message: *a2aMessage,
		}

		result, err := r.a2aClient.SendMessage(ctx, params)
		if err != nil {
			r.sendErrorEvent(eventChan, invocation, fmt.Sprintf("A2A request failed to %s: %v", r.agentCard.URL, err))
			return
		}

		// Convert response to event - result should have the message
		var responseMsg *protocol.Message
		switch v := result.Result.(type) {
		case *protocol.Message:
			responseMsg = v
		case *protocol.Task:
			// For tasks, we might want to handle them differently
			// For now, create a simple message indicating task was created
			responseMsg = &protocol.Message{
				Role:  protocol.MessageRoleAgent,
				Parts: []protocol.Part{protocol.NewTextPart(fmt.Sprintf("Task created: %s", v.ID))},
			}
		default:
			// Handle unknown response types
			responseMsg = &protocol.Message{
				Role:  protocol.MessageRoleAgent,
				Parts: []protocol.Part{protocol.NewTextPart("Received unknown response type")},
			}
		}

		event := r.buildRespEvent(responseMsg, invocation)
		eventChan <- event
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
