//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package difyagent provides an agent that can communicate with dify workflow or chatflow.
package difyagent

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

// DifyAgent is an agent that communicates with a remote Dify service.
type DifyAgent struct {
	// options
	baseUrl          string // dify base url
	apiSecret        string // dify api secret
	name             string
	description      string
	eventConverter   DifyEventConverter   // Custom A2A event converters
	requestConverter DifyRequestConverter // Custom Dify request converter

	streamingBufSize        int                  // Buffer size for streaming responses
	streamingRespHandler    StreamingRespHandler // Handler for streaming responses
	transferStateKey        []string             // Keys in session state to transfer to the A2A agent message by metadata
	enableStreaming         *bool                // Explicitly set streaming mode; nil means use agent card capability
	autoGenConversationName *bool                // Whether to auto generate conversation name

	difyClient        *dify.Client
	getDifyClientFunc func(*agent.Invocation) (*dify.Client, error)
}

// New creates a new A2AAgent.
func New(opts ...Option) (*DifyAgent, error) {
	difyAgent := &DifyAgent{
		eventConverter:   &defaultDifyEventConverter{},
		streamingBufSize: defaultStreamingChannelSize,
	}

	for _, opt := range opts {
		opt(difyAgent)
	}

	// Validate that required fields are set
	if difyAgent.name == "" {
		return nil, fmt.Errorf("agent name is required")
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

// validateA2ARequestOptions validates that all A2A request options are of the correct type
func (r *DifyAgent) validateRequestOptions(invocation *agent.Invocation) error {
	// For Dify agent, we don't need to validate A2A client options
	// CustomAgentConfigs can contain any configuration for Dify requests
	return nil
}

// Run implements the Agent interface
func (r *DifyAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	cli, err := r.getDifyClient(invocation)
	if err != nil {
		return nil, err
	}
	r.difyClient = cli

	// Validate A2A request options early
	if err := r.validateRequestOptions(invocation); err != nil {
		return nil, err
	}

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
		req.Inputs = map[string]interface{}{}
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

// runStreaming handles streaming A2A communication
func (r *DifyAgent) runStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if r.eventConverter == nil {
		return nil, fmt.Errorf("event converter not set")
	}
	eventChan := make(chan *event.Event, r.streamingBufSize)
	go func() {
		defer close(eventChan)

		req, err := r.buildDifyRequest(ctx, invocation, true)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("failed to construct Dify request: %v", err))
			return
		}
		req.AutoGenerateName = r.autoGenConversationName
		streamChan, err := r.difyClient.API().ChatMessagesStream(ctx, req)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("Dify streaming request failed to %s: %v", r.baseUrl, err))
			return
		}

		var aggregatedContentBuilder strings.Builder
		for streamEvent := range streamChan {
			if err := agent.CheckContextCancelled(ctx); err != nil {
				return
			}

			evt := r.eventConverter.ConvertStreamingToEvent(streamEvent, r.name, invocation)
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
func (r *DifyAgent) runNonStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, defaultNonStreamingChannelSize)
	go func() {
		defer close(eventChan)

		// Construct Dify request from invocation
		req, err := r.buildDifyRequest(ctx, invocation, false)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("failed to construct Dify request: %v", err))
			return
		}
		// Dify requests don't use A2A request options
		result, err := r.difyClient.API().ChatMessages(ctx, req)
		if err != nil {
			r.sendErrorEvent(ctx, eventChan, invocation, fmt.Sprintf("Dify request failed to %s: %v", r.baseUrl, err))
			return
		}

		evt := r.eventConverter.ConvertToEvent(result, r.name, invocation)
		evt.Object = model.ObjectTypeChatCompletion
		agent.EmitEvent(ctx, invocation, eventChan, evt)
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
