//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package dify provides an agent that can communicate with dify workflow or chatflow.
package dify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultStreamingChannelSize    = 1024
	defaultNonStreamingChannelSize = 10
)

// DifyMode represents the Dify service mode
type DifyMode string

const (
	// ModeChatflow represents Dify chatflow mode (default)
	ModeChatflow DifyMode = "chatflow"
	// ModeWorkflow represents Dify workflow mode
	ModeWorkflow DifyMode = "workflow"
)

// DifyAgent is an agent that communicates with a remote Dify service.
type DifyAgent struct {
	// options
	baseUrl           string // dify base url
	apiSecret         string // dify api secret
	name              string
	description       string
	mode              DifyMode                     // Dify service mode: chatflow or workflow (default: chatflow)
	eventConverter    DifyEventConverter           // Custom event converters
	requestConverter  DifyRequestConverter         // Custom Dify chatflow request converter
	workflowConverter DifyWorkflowRequestConverter // Custom Dify workflow request converter

	streamingBufSize        int                  // Buffer size for streaming responses
	streamingRespHandler    StreamingRespHandler // Handler for streaming responses
	transferStateKey        []string             // Keys in session state to transfer to the A2A agent message by metadata
	enableStreaming         *bool                // Explicitly set streaming mode; nil means use agent card capability
	autoGenConversationName *bool                // Whether to auto generate conversation name

	difyClient        *dify.Client
	getDifyClientFunc func(*agent.Invocation) (*dify.Client, error)
}

// New creates a new DifyAgent.
func New(opts ...Option) (*DifyAgent, error) {
	difyAgent := &DifyAgent{
		eventConverter:    &defaultDifyEventConverter{},
		requestConverter:  &defaultEventDifyConverter{},
		workflowConverter: &defaultWorkflowRequestConverter{},
		streamingBufSize:  defaultStreamingChannelSize,
		mode:              ModeChatflow, // Default to chatflow mode
	}

	for _, opt := range opts {
		opt(difyAgent)
	}

	// Validate that required fields are set
	if difyAgent.name == "" {
		return nil, fmt.Errorf("agent name is required")
	}

	// Validate mode
	if difyAgent.mode != ModeChatflow && difyAgent.mode != ModeWorkflow {
		return nil, fmt.Errorf("invalid mode: %s, must be either 'chatflow' or 'workflow'", difyAgent.mode)
	}

	return difyAgent, nil
}

// sendErrorEvent sends an error event to the event channel
func (r *DifyAgent) sendErrorEvent(ctx context.Context, eventChan chan<- *event.Event,
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
func (r *DifyAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	cli, err := r.getDifyClient(invocation)
	if err != nil {
		return nil, err
	}
	r.difyClient = cli

	useStreaming := r.shouldUseStreaming()
	if useStreaming {
		return r.runStreaming(ctx, invocation)
	}
	return r.runNonStreaming(ctx, invocation)
}

// shouldUseStreaming determines whether to use streaming protocol
func (r *DifyAgent) shouldUseStreaming() bool {
	// If explicitly set via option, use that value
	if r.enableStreaming != nil {
		return *r.enableStreaming
	}
	// Default to non-streaming if capabilities are not specified
	return false
}

// buildDifyRequest constructs Dify request from invocation
func (r *DifyAgent) buildDifyRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	isStream bool,
) (*dify.ChatMessageRequest,
	error) {
	if r.requestConverter == nil {
		return nil, fmt.Errorf("request converter not set")
	}

	req, err := r.requestConverter.ConvertToDifyRequest(ctx, invocation, isStream)
	if err != nil {
		return nil, err
	}
	if req.Inputs == nil {
		req.Inputs = map[string]any{}
	}

	// Transfer additional state keys
	if len(r.transferStateKey) > 0 {
		for _, key := range r.transferStateKey {
			if value, ok := invocation.RunOptions.RuntimeState[key]; ok {
				req.Inputs[key] = value
			}
		}
	}

	return req, nil
}

// processStreamEvent processes a single stream event and returns the content to aggregate
func (r *DifyAgent) processStreamEvent(
	ctx context.Context,
	streamEvent dify.ChatMessageStreamChannelResponse,
	invocation *agent.Invocation,
) (*event.Event, string, error) {
	evt := r.eventConverter.ConvertStreamingToEvent(streamEvent, r.name, invocation)

	// Handle nil event (e.g., when Answer is empty)
	if evt == nil {
		return nil, "", nil
	}

	// Aggregate content from delta
	var content string
	if evt.Response != nil && len(evt.Response.Choices) > 0 {
		if r.streamingRespHandler != nil {
			var err error
			content, err = r.streamingRespHandler(evt.Response)
			if err != nil {
				return nil, "", fmt.Errorf("streaming resp handler failed: %v", err)
			}
		} else if evt.Response.Choices[0].Delta.Content != "" {
			content = evt.Response.Choices[0].Delta.Content
		}
	}

	return evt, content, nil
}

// buildStreamingRequest builds and sends streaming request to Dify chatflow
func (r *DifyAgent) buildStreamingRequest(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan dify.ChatMessageStreamChannelResponse, error) {
	req, err := r.buildDifyRequest(ctx, invocation, true)
	if err != nil {
		return nil, fmt.Errorf("failed to construct Dify request: %v", err)
	}
	req.AutoGenerateName = r.autoGenConversationName

	streamChan, err := r.difyClient.API().ChatMessagesStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("dify chatflow streaming request failed to %s: %v", r.baseUrl, err)
	}

	return streamChan, nil
}

// buildWorkflowStreamingRequest builds and sends streaming request to Dify workflow
func (r *DifyAgent) buildWorkflowStreamingRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) error {
	if r.workflowConverter == nil {
		return fmt.Errorf("workflow converter not set")
	}

	req, err := r.workflowConverter.ConvertToWorkflowRequest(ctx, invocation)
	if err != nil {
		return fmt.Errorf("failed to construct workflow request: %v", err)
	}

	// Transfer additional state keys
	if len(r.transferStateKey) > 0 {
		for _, key := range r.transferStateKey {
			if value, ok := invocation.RunOptions.RuntimeState[key]; ok {
				req.Inputs[key] = value
			}
		}
	}

	req.ResponseMode = "streaming"

	var aggregatedContentBuilder strings.Builder

	err = r.difyClient.API().RunStreamWorkflow(ctx, req, func(resp dify.StreamingResponse) {
		if err := agent.CheckContextCancelled(ctx); err != nil {
			return
		}

		// Convert workflow streaming response to event
		// Extract text from outputs
		var content string
		if resp.Data.Outputs != nil {
			// Try to get answer from common output fields
			if val, ok := resp.Data.Outputs["answer"]; ok {
				if strVal, ok := val.(string); ok {
					content = strVal
				}
			} else if val, ok := resp.Data.Outputs["text"]; ok {
				if strVal, ok := val.(string); ok {
					content = strVal
				}
			}
		}

		if content != "" {
			aggregatedContentBuilder.WriteString(content)

			message := model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			}

			evt := event.New(
				invocation.InvocationID,
				r.name,
				event.WithResponse(&model.Response{
					Object:    model.ObjectTypeChatCompletionChunk,
					Choices:   []model.Choice{{Delta: message}},
					Timestamp: time.Now(),
					Created:   time.Now().Unix(),
					IsPartial: true,
					Done:      false,
				}),
				event.WithObject(model.ObjectTypeChatCompletionChunk),
			)
			agent.EmitEvent(ctx, invocation, eventChan, evt)
		}
	})

	if err != nil {
		return fmt.Errorf("workflow streaming request failed to %s: %v", r.baseUrl, err)
	}

	// Send final aggregated event
	r.sendFinalStreamingEvent(ctx, eventChan, invocation, aggregatedContentBuilder.String())
	return nil
}

// sendFinalStreamingEvent sends the final aggregated event for streaming
func (r *DifyAgent) sendFinalStreamingEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocation *agent.Invocation,
	aggregatedContent string,
) {
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
					Content: aggregatedContent,
				},
			}},
		}),
	))
}

// runStreaming handles streaming communication
func (r *DifyAgent) runStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if r.eventConverter == nil {
		return nil, fmt.Errorf("event converter not set")
	}
	eventChan := make(chan *event.Event, r.streamingBufSize)

	go func() {
		defer close(eventChan)

		// Handle workflow and chatflow differently due to different SDK APIs
		if r.mode == ModeWorkflow {
			// Workflow uses callback-based streaming
			err := r.buildWorkflowStreamingRequest(ctx, invocation, eventChan)
			if err != nil {
				r.sendErrorEvent(ctx, eventChan, invocation, err.Error())
			}
			return
		}

		// Chatflow uses channel-based streaming
		streamChan, err := r.buildStreamingRequest(ctx, invocation)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, err.Error())
			return
		}

		var aggregatedContentBuilder strings.Builder
		for streamEvent := range streamChan {
			if err := agent.CheckContextCancelled(ctx); err != nil {
				return
			}

			evt, content, err := r.processStreamEvent(ctx, streamEvent, invocation)
			if err != nil {
				r.sendErrorEvent(ctx, eventChan, invocation, err.Error())
				return
			}

			// Skip nil events (empty responses)
			if evt == nil {
				continue
			}

			if content != "" {
				aggregatedContentBuilder.WriteString(content)
			}

			agent.EmitEvent(ctx, invocation, eventChan, evt)
		}

		r.sendFinalStreamingEvent(ctx, eventChan, invocation, aggregatedContentBuilder.String())
	}()
	return eventChan, nil
}

// executeNonStreamingRequest executes a non-streaming Dify request
func (r *DifyAgent) executeNonStreamingRequest(
	ctx context.Context,
	invocation *agent.Invocation,
) (*dify.ChatMessageResponse, error) {
	// Handle workflow mode
	if r.mode == ModeWorkflow {
		if r.workflowConverter == nil {
			return nil, fmt.Errorf("workflow converter not set")
		}

		req, err := r.workflowConverter.ConvertToWorkflowRequest(ctx, invocation)
		if err != nil {
			return nil, fmt.Errorf("failed to construct workflow request: %v", err)
		}

		// Transfer additional state keys
		if len(r.transferStateKey) > 0 {
			for _, key := range r.transferStateKey {
				if value, ok := invocation.RunOptions.RuntimeState[key]; ok {
					req.Inputs[key] = value
				}
			}
		}

		workflowResp, err := r.difyClient.API().RunWorkflow(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("dify workflow request failed to %s: %v", r.baseUrl, err)
		}

		// Convert WorkflowResponse to ChatMessageResponse
		// Extract answer from workflow outputs
		answer := ""
		if workflowResp.Data.Outputs != nil {
			// Try to get answer from common output fields
			if val, ok := workflowResp.Data.Outputs["answer"]; ok {
				if strVal, ok := val.(string); ok {
					answer = strVal
				}
			} else if val, ok := workflowResp.Data.Outputs["text"]; ok {
				if strVal, ok := val.(string); ok {
					answer = strVal
				}
			} else if val, ok := workflowResp.Data.Outputs["result"]; ok {
				if strVal, ok := val.(string); ok {
					answer = strVal
				}
			}
		}

		return &dify.ChatMessageResponse{
			Answer: answer,
			ID:     workflowResp.WorkflowRunID,
		}, nil
	}

	// Handle chatflow mode
	req, err := r.buildDifyRequest(ctx, invocation, false)
	if err != nil {
		return nil, fmt.Errorf("failed to construct Dify request: %v", err)
	}

	result, err := r.difyClient.API().ChatMessages(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("dify chatflow request failed to %s: %v", r.baseUrl, err)
	}

	return result, nil
}

// convertAndEmitNonStreamingEvent converts result to event and emits it
func (r *DifyAgent) convertAndEmitNonStreamingEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocation *agent.Invocation,
	result *dify.ChatMessageResponse,
) {
	evt := r.eventConverter.ConvertToEvent(result, r.name, invocation)
	evt.Object = model.ObjectTypeChatCompletion
	agent.EmitEvent(ctx, invocation, eventChan, evt)
}

// runNonStreaming handles non-streaming A2A communication
func (r *DifyAgent) runNonStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, defaultNonStreamingChannelSize)
	go func() {
		defer close(eventChan)

		result, err := r.executeNonStreamingRequest(ctx, invocation)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, err.Error())
			return
		}

		r.convertAndEmitNonStreamingEvent(ctx, eventChan, invocation, result)
	}()
	return eventChan, nil
}

// Tools implements the Agent interface
func (r *DifyAgent) Tools() []tool.Tool {
	// Remote A2A agents don't expose tools directly
	// Tools are handled by the remote agent
	return []tool.Tool{}
}

// Info implements the Agent interface
func (r *DifyAgent) Info() agent.Info {
	return agent.Info{
		Name:        r.name,
		Description: r.description,
	}
}

// SubAgents implements the Agent interface
func (r *DifyAgent) SubAgents() []agent.Agent {
	// Remote A2A agents don't have sub-agents in the local context
	return []agent.Agent{}
}

// FindSubAgent implements the Agent interface
func (r *DifyAgent) FindSubAgent(name string) agent.Agent {
	// Remote A2A agents don't have sub-agents in the local context
	return nil
}

func (r *DifyAgent) getDifyClient(
	invocation *agent.Invocation,
) (*dify.Client, error) {
	if r.getDifyClientFunc != nil {
		return r.getDifyClientFunc(invocation)
	}
	baseUrl := r.baseUrl
	return dify.NewClientWithConfig(&dify.ClientConfig{
		Host:             baseUrl,
		DefaultAPISecret: r.apiSecret,
		Timeout:          time.Hour,
	}), nil
}
