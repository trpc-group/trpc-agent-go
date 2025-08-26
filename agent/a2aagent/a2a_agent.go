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
	"trpc.group/trpc-go/trpc-agent-go/log"
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

// A2AEventConverter defines an interface for converting A2A protocol types to Event.
// This allows for custom conversion logic for different A2A response types.
//
// Example usage:
//
//	type CustomTaskConverter struct{}
//
//	func (c *CustomTaskConverter) CanHandle(a2aResult interface{}) bool {
//		_, ok := a2aResult.(*protocol.Task)
//		return ok
//	}
//
//	func (c *CustomTaskConverter) ConvertToEvent(a2aResult interface{}, invocation *agent.Invocation) *event.Event {
//		task, ok := a2aResult.(*protocol.Task)
//		if !ok {
//			return nil
//		}
//		// Custom conversion logic here
//		return &event.Event{...}
//	}
//
//	// Usage:
//	agent, err := New(
//		WithAgentURL("http://example.com"),
//		WithCustomEventConverter(&CustomTaskConverter{}),
//	)
type A2AEventConverter interface {
	// ConvertToEvent converts an A2A protocol type to an Event.
	// Returns nil if the converter cannot handle this type.
	ConvertToEvent(isStream bool, a2aResult interface{}, invocation *agent.Invocation) (*event.Event, error)
}

// EventA2AConverter defines an interface for converting invocations to A2A protocol messages.
// This allows for custom conversion logic when sending requests to A2A agents.
//
// Example usage:
//
//	type CustomA2AConverter struct{}
//
//	func (c *CustomA2AConverter) ConvertToA2AMessage(isStream bool, invocation *agent.Invocation) (*protocol.Message, error) {
//		// Custom conversion logic here
//		parts := []protocol.Part{
//			protocol.NewTextPart(fmt.Sprintf("Custom: %s", invocation.Message.Content)),
//		}
//		return &protocol.Message{
//			Role:  protocol.MessageRoleUser,
//			Parts: parts,
//		}, nil
//	}
//
//	// Usage:
//	agent, err := New(
//		WithAgentURL("http://example.com"),
//		WithCustomA2AConverter(&CustomA2AConverter{}),
//	)
type EventA2AConverter interface {
	// ConvertToA2AMessage converts an invocation to an A2A protocol Message.
	// This allows for custom conversion logic when sending requests to A2A agents.
	ConvertToA2AMessage(isStream bool, invocation *agent.Invocation) (*protocol.Message, error)
}

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

	// Streaming configuration
	forceNonStreaming bool // Force non-streaming mode even if agent supports streaming

	// customEventConverters holds custom A2A event converters
	customEventConverters A2AEventConverter

	// customA2AConverters holds custom A2A message converters for requests
	customA2AConverters EventA2AConverter
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
	// If URL is not set in the agent card, use the provided agent URL.
	if agentCard.URL == "" {
		agentCard.URL = agentURL
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
func (r *A2AAgent) buildA2AMessage(invocation *agent.Invocation, isStream bool) (*protocol.Message, error) {
	// Try custom A2A converter first
	if r.customA2AConverters != nil {
		message, err := r.customA2AConverters.ConvertToA2AMessage(isStream, invocation)
		if err != nil || message == nil {
			return nil, fmt.Errorf("custom A2A converter failed, msg:%v, err:%w", message, err)
		}
		return message, nil
	}

	// Fall back to default conversion logic
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

// convertTaskToMessage converts a Task to a Message
func (r *A2AAgent) convertTaskToMessage(task *protocol.Task) *protocol.Message {
	var parts []protocol.Part

	// Add task creation message with detailed information
	taskInfo := fmt.Sprintf("Task created: %s (Context: %s, Status: %s)", task.ID, task.ContextID, task.Status.State)
	if task.Status.Timestamp != "" {
		taskInfo += fmt.Sprintf(" at %s", task.Status.Timestamp)
	}
	parts = append(parts, protocol.NewTextPart(taskInfo))

	// Add task status message if available
	if task.Status.Message != nil {
		// Include the status message parts
		parts = append(parts, task.Status.Message.Parts...)
	}

	// Add artifacts if any
	for _, artifact := range task.Artifacts {
		artifactInfo := fmt.Sprintf("Artifact: %s", artifact.ArtifactID)
		if artifact.Name != nil {
			artifactInfo = fmt.Sprintf("Artifact: %s (%s)", *artifact.Name, artifact.ArtifactID)
		}
		if artifact.Description != nil {
			artifactInfo += fmt.Sprintf(" - %s", *artifact.Description)
		}
		parts = append(parts, protocol.NewTextPart(artifactInfo))
		parts = append(parts, artifact.Parts...)
	}

	return &protocol.Message{
		Role:  protocol.MessageRoleAgent,
		Parts: parts,
	}
}

// convertTaskStatusToMessage converts a TaskStatusUpdateEvent to a Message
func (r *A2AAgent) convertTaskStatusToMessage(event *protocol.TaskStatusUpdateEvent) *protocol.Message {
	var parts []protocol.Part

	// Add status update information
	statusText := fmt.Sprintf("Task %s status updated to: %s", event.TaskID, event.Status.State)
	if event.Status.Timestamp != "" {
		statusText += fmt.Sprintf(" at %s", event.Status.Timestamp)
	}
	parts = append(parts, protocol.NewTextPart(statusText))

	// Include status message if available
	if event.Status.Message != nil {
		parts = append(parts, event.Status.Message.Parts...)
	}

	return &protocol.Message{
		Role:  protocol.MessageRoleAgent,
		Parts: parts,
	}
}

// convertTaskArtifactToMessage converts a TaskArtifactUpdateEvent to a Message
func (r *A2AAgent) convertTaskArtifactToMessage(event *protocol.TaskArtifactUpdateEvent) *protocol.Message {
	var parts []protocol.Part

	// Add artifact information
	artifactName := "Artifact"
	if event.Artifact.Name != nil {
		artifactName = *event.Artifact.Name
	}

	artifactText := fmt.Sprintf("Artifact update: %s", artifactName)
	if event.Artifact.Description != nil {
		artifactText += fmt.Sprintf(" - %s", *event.Artifact.Description)
	}

	if event.LastChunk != nil && *event.LastChunk {
		artifactText += " (final)"
	} else if event.Append != nil && *event.Append {
		artifactText += " (streaming)"
	}

	parts = append(parts, protocol.NewTextPart(artifactText))

	// Include artifact content
	parts = append(parts, event.Artifact.Parts...)

	return &protocol.Message{
		Role:  protocol.MessageRoleAgent,
		Parts: parts,
	}
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

	// Convert A2A parts to model content parts
	var content string
	var contentParts []model.ContentPart

	for _, part := range response.Parts {
		switch part.GetKind() {
		case protocol.KindText:
			p, ok := part.(*protocol.TextPart)
			if !ok {
				log.Infof("unexpected part type: %T", part)
				continue
			}
			content += p.Text
			contentParts = append(contentParts, model.ContentPart{
				Type: model.ContentTypeText,
				Text: &p.Text,
			})
		case protocol.KindFile:
			f, ok := part.(*protocol.FilePart)
			if !ok {
				log.Infof("unexpected part type: %T", part)
				continue
			}
			// For FilePart, we'll add a text description since the structure is complex
			switch f.File.(type) {
			case *protocol.FileWithBytes:
				bytesFile := f.File.(*protocol.FileWithBytes)
				contentParts = append(contentParts, model.ContentPart{
					Type: model.ContentTypeFile,
					File: &model.File{
						Name:     *bytesFile.Name,
						Data:     []byte(bytesFile.Bytes),
						MimeType: *bytesFile.MimeType,
					},
				})
			case *protocol.FileWithURI:
				urlFile := f.File.(*protocol.FileWithURI)
				contentParts = append(contentParts, model.ContentPart{
					Type: model.ContentTypeFile,
					File: &model.File{
						Name:     *urlFile.Name,
						Data:     []byte(urlFile.URI),
						MimeType: *urlFile.MimeType,
					},
				})
			default:
				log.Infof("unexpected file type: %T", f.File)
			}
		case protocol.KindData:
			d, ok := part.(*protocol.DataPart)
			if !ok {
				log.Infof("unexpected part type: %T", part)
				continue
			}
			dataStr := fmt.Sprintf("%s", d.Data)
			contentParts = append(contentParts, model.ContentPart{
				Type: model.ContentTypeText,
				Text: &dataStr,
			})
		default:
			log.Infof("unexpected part type: %T", part)
		}
	}

	// Create message with both content and content parts
	message := model.Message{
		Role:         model.RoleAssistant,
		Content:      content,
		ContentParts: contentParts,
	}

	// Create response event with A2A metadata
	ev := &event.Event{
		Author:       r.name,
		InvocationID: invocation.InvocationID,
		Response: &model.Response{
			Choices:   []model.Choice{{Message: message}},
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

	// Determine if we should use streaming based on agent capabilities and configuration
	useStreaming := r.shouldUseStreaming()

	if useStreaming {
		return r.runStreaming(ctx, invocation)
	}
	return r.runNonStreaming(ctx, invocation)
}

// shouldUseStreaming determines whether to use streaming protocol
func (r *A2AAgent) shouldUseStreaming() bool {
	// If force non-streaming is enabled, always use non-streaming
	if r.forceNonStreaming {
		return false
	}

	// Check if agent card supports streaming
	if r.agentCard != nil && r.agentCard.Capabilities.Streaming != nil {
		return *r.agentCard.Capabilities.Streaming
	}

	// Default to non-streaming if capabilities are not specified
	return false
}

// runStreaming handles streaming A2A communication
func (r *A2AAgent) runStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 1024)
	go func() {
		defer close(eventChan)

		a2aMessage, err := r.buildA2AMessage(invocation, true)
		if err != nil {
			r.sendErrorEvent(eventChan, invocation, fmt.Sprintf("failed to construct A2A message: %v", err))
			return
		}

		params := protocol.SendMessageParams{
			Message: *a2aMessage,
		}

		// Use streaming API
		streamChan, err := r.a2aClient.StreamMessage(ctx, params)
		if err != nil {
			r.sendErrorEvent(eventChan, invocation, fmt.Sprintf("A2A streaming request failed to %s: %v", r.agentCard.URL, err))
			return
		}

		// Process streaming responses
		for streamEvent := range streamChan {
			select {
			case <-ctx.Done():
				r.sendErrorEvent(eventChan, invocation, "context cancelled")
				return
			default:
			}

			// Try custom event converters first
			var event *event.Event
			if r.customEventConverters != nil {
				event, err = r.customEventConverters.ConvertToEvent(true, streamEvent.Result, invocation)
				if err != nil {
					r.sendErrorEvent(eventChan, invocation, fmt.Sprintf("custom event converter failed: %v", err))
					return
				}
			}

			// Fall back to default conversion logic if no custom converter handled it
			if event == nil {
				var responseMsg *protocol.Message
				switch v := streamEvent.Result.(type) {
				case *protocol.Message:
					responseMsg = v
					event = r.buildRespEvent(responseMsg, invocation)
				case *protocol.Task:
					responseMsg = r.convertTaskToMessage(v)
					event = r.buildRespEvent(responseMsg, invocation)
				case *protocol.TaskStatusUpdateEvent:
					responseMsg = r.convertTaskStatusToMessage(v)
					event = r.buildRespEvent(responseMsg, invocation)
				case *protocol.TaskArtifactUpdateEvent:
					responseMsg = r.convertTaskArtifactToMessage(v)
					event = r.buildRespEvent(responseMsg, invocation)
				default:
					log.Infof("unexpected event type: %T", streamEvent.Result)
				}
			}

			// For streaming, mark as not done until the last message
			event.Response.Done = false
			eventChan <- event
		}

		// Send final event to indicate completion
		finalEvent := &event.Event{
			Author:       r.name,
			InvocationID: invocation.InvocationID,
			Response: &model.Response{
				Done:      true,
				Timestamp: time.Now(),
				Created:   time.Now().Unix(),
			},
		}
		eventChan <- finalEvent
	}()

	return eventChan, nil
}

// runNonStreaming handles non-streaming A2A communication
func (r *A2AAgent) runNonStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 1)
	go func() {
		defer close(eventChan)

		// Construct A2A message from session
		a2aMessage, err := r.buildA2AMessage(invocation, false)
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

		// Try custom event converters first
		var event *event.Event
		if r.customEventConverters != nil {
			event, err = r.customEventConverters.ConvertToEvent(false, result.Result, invocation)
			if err != nil {
				r.sendErrorEvent(eventChan, invocation, fmt.Sprintf("custom event converter failed: %v", err))
				return
			}
		}

		// Fall back to default conversion logic if no custom converter handled it
		if event == nil {
			var responseMsg *protocol.Message
			switch v := result.Result.(type) {
			case *protocol.Message:
				responseMsg = v
				event = r.buildRespEvent(responseMsg, invocation)
			case *protocol.Task:
				responseMsg = r.convertTaskToMessage(v)
				event = r.buildRespEvent(responseMsg, invocation)
			default:
				// Handle unknown response types
				responseMsg = &protocol.Message{
					Role:  protocol.MessageRoleAgent,
					Parts: []protocol.Part{protocol.NewTextPart("Received unknown response type")},
				}
				event = r.buildRespEvent(responseMsg, invocation)
			}
		}
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
