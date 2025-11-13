//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dify

import (
	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// StreamingRespHandler handles the streaming response content
// return the content will be added to the final aggregated content
type StreamingRespHandler func(resp *model.Response) (string, error)

// Option configures the DifyAgent
type Option func(*DifyAgent)

// WithBaseUrl sets the base URL of the Dify service
func WithBaseUrl(baseUrl string) Option {
	return func(a *DifyAgent) {
		a.baseUrl = baseUrl
	}
}

// WithMode sets the Dify service mode (chatflow or workflow).
// Default is ModeChatflow if not specified.
//
// Example:
//
//	WithMode(dify.ModeWorkflow)  // Use workflow mode
//	WithMode(dify.ModeChatflow)  // Use chatflow mode (default)
func WithMode(mode DifyMode) Option {
	return func(a *DifyAgent) {
		a.mode = mode
	}
}

// WithName sets the name of agent
func WithName(name string) Option {
	return func(a *DifyAgent) {
		a.name = name
	}
}

// WithDescription sets the agent description
func WithDescription(description string) Option {
	return func(a *DifyAgent) {
		a.description = description
	}
}

// WithCustomEventConverter adds a custom A2A event converter to the DifyAgent.
func WithCustomEventConverter(converter DifyEventConverter) Option {
	return func(a *DifyAgent) {
		a.eventConverter = converter
	}
}

// WithCustomRequestConverter adds a custom A2A message converter to the DifyAgent.
// This converter will be used to convert invocations to A2A protocol messages.
func WithCustomRequestConverter(converter DifyRequestConverter) Option {
	return func(a *DifyAgent) {
		a.requestConverter = converter
	}
}

// WithCustomWorkflowConverter adds a custom workflow request converter to the DifyAgent.
// This converter will be used when mode is set to ModeWorkflow.
func WithCustomWorkflowConverter(converter DifyWorkflowRequestConverter) Option {
	return func(a *DifyAgent) {
		a.workflowConverter = converter
	}
}

// WithStreamingChannelBufSize set the buf size of streaming protocol
func WithStreamingChannelBufSize(size int) Option {
	return func(a *DifyAgent) {
		a.streamingBufSize = size
	}
}

// WithStreamingRespHandler sets a handler function to process streaming responses.
func WithStreamingRespHandler(handler StreamingRespHandler) Option {
	return func(a *DifyAgent) {
		a.streamingRespHandler = handler
	}
}

// WithTransferStateKey sets the keys in session state to transfer to the A2A agent message by metadata
func WithTransferStateKey(key ...string) Option {
	return func(a *DifyAgent) {
		a.transferStateKey = append(a.transferStateKey, key...)
	}
}

// WithEnableStreaming explicitly controls whether to use streaming protocol.
// If not set (nil), the agent will use the streaming capability from the agent card.
// This option overrides the agent card's capability setting.
func WithEnableStreaming(enable bool) Option {
	return func(a *DifyAgent) {
		a.enableStreaming = &enable
	}
}

// WithAutoGenConversationName sets whether to auto-generate conversation names in Dify.
// This option is only applicable for chatflow mode.
func WithAutoGenConversationName(enable bool) Option {
	return func(a *DifyAgent) {
		a.autoGenConversationName = &enable
	}
}

// WithGetDifyClientFunc sets a custom function to create Dify client for each invocation.
// This is optional - if not provided, the agent will use a default client with base configuration.
//
// Use cases:
//   - Dynamic API keys per user/session (multi-tenant scenarios)
//   - Custom timeout settings based on request type or user tier
//   - Different Dify instances per organization
//   - Custom authentication headers or connection settings
//   - Load balancing across multiple Dify endpoints
//
// The function receives an *agent.Invocation parameter that provides access to:
//   - Session information (user ID, session ID)
//   - Runtime state (user preferences, context)
//   - Message content and metadata
//
// Example:
//
//	WithGetDifyClientFunc(func(inv *agent.Invocation) (*dify.Client, error) {
//	    apiSecret := getUserAPISecret(inv.Session.UserID)
//	    return dify.NewClientWithConfig(&dify.ClientConfig{
//	        Host:             "https://api.dify.ai/v1",
//	        DefaultAPISecret: apiSecret,
//	        Timeout:          30 * time.Second,
//	    }), nil
//	})
func WithGetDifyClientFunc(fn func(*agent.Invocation) (*dify.Client, error)) Option {
	return func(a *DifyAgent) {
		a.getDifyClientFunc = fn
	}
}
