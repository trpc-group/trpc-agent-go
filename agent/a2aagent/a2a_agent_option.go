//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"strings"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// StreamingRespHandler handles the streaming response content
// return the content will be added to the final aggregated content
type StreamingRespHandler func(resp *model.Response) (string, error)

// ConvertToA2AMessageFunc is the function signature for converting an invocation to an A2A protocol message.
type ConvertToA2AMessageFunc func(isStream bool, agentName string, invocation *agent.Invocation) (*protocol.Message, error)

// BuildMessageHook wraps the A2A message conversion with additional functionality.
// The hook receives the next converter function and returns a new converter function.
// Users can modify the invocation before calling next, modify the message after calling next,
// or completely replace the conversion logic by not calling next.
//
// This follows the same middleware pattern as server-side ProcessMessageHook.
type BuildMessageHook func(next ConvertToA2AMessageFunc) ConvertToA2AMessageFunc

// Option configures the A2AAgent
type Option func(*A2AAgent)

// WithName sets the name of agent
func WithName(name string) Option {
	return func(a *A2AAgent) {
		a.name = name
	}
}

// WithDescription sets the agent description
func WithDescription(description string) Option {
	return func(a *A2AAgent) {
		a.description = description
	}
}

// WithAgentCardURL set the agent card URL
func WithAgentCardURL(url string) Option {
	return func(a *A2AAgent) {
		a.agentURL = strings.TrimSpace(url)
	}
}

// WithAgentCard set the agent card
func WithAgentCard(agentCard *server.AgentCard) Option {
	return func(a *A2AAgent) {
		a.agentCard = agentCard
	}
}

// WithCustomEventConverter adds a custom A2A event converter to the A2AAgent.
func WithCustomEventConverter(converter A2AEventConverter) Option {
	return func(a *A2AAgent) {
		a.eventConverter = converter
	}
}

// WithCustomA2AConverter adds a custom A2A message converter to the A2AAgent.
// This converter will be used to convert invocations to A2A protocol messages.
func WithCustomA2AConverter(converter InvocationA2AConverter) Option {
	return func(a *A2AAgent) {
		a.a2aMessageConverter = converter
	}
}

// WithA2AClientExtraOptions adds extra options to the A2A client.
func WithA2AClientExtraOptions(opts ...client.Option) Option {
	return func(a *A2AAgent) {
		a.extraA2AOptions = append(a.extraA2AOptions, opts...)
	}
}

// WithStreamingChannelBufSize set the buf size of streaming protocol
func WithStreamingChannelBufSize(size int) Option {
	return func(a *A2AAgent) {
		if size < 0 {
			size = defaultStreamingChannelSize
		}
		a.streamingBufSize = size
	}
}

// WithStreamingRespHandler sets a handler function to process streaming responses.
func WithStreamingRespHandler(handler StreamingRespHandler) Option {
	return func(a *A2AAgent) {
		a.streamingRespHandler = handler
	}
}

// WithTransferStateKey sets the keys in session state to transfer to the A2A agent message by metadata
func WithTransferStateKey(key ...string) Option {
	return func(a *A2AAgent) {
		a.transferStateKey = append(a.transferStateKey, key...)
	}
}

// WithBuildMessageHook sets a hook to customize the A2A message conversion.
// The hook wraps the default converter (including transferStateKey processing) as a middleware,
// following the same pattern as server-side WithProcessMessageHook.
//
// Example - modify message after conversion:
//
//	a2aagent.WithBuildMessageHook(func(next a2aagent.ConvertToA2AMessageFunc) a2aagent.ConvertToA2AMessageFunc {
//	    return func(isStream bool, agentName string, inv *agent.Invocation) (*protocol.Message, error) {
//	        msg, err := next(isStream, agentName, inv)
//	        if err != nil {
//	            return nil, err
//	        }
//	        // inject custom metadata
//	        if msg.Metadata == nil {
//	            msg.Metadata = make(map[string]any)
//	        }
//	        msg.Metadata["custom_key"] = "custom_value"
//	        return msg, nil
//	    }
//	})
func WithBuildMessageHook(hook BuildMessageHook) Option {
	return func(a *A2AAgent) {
		a.buildMessageHook = hook
	}
}

// WithUserIDHeader sets the HTTP header name to send UserID to the A2A server.
// If not set, defaults to "X-User-ID".
// The UserID will be extracted from invocation.Session.UserID and sent via the specified header.
func WithUserIDHeader(header string) Option {
	return func(a *A2AAgent) {
		if header != "" {
			a.userIDHeader = header
		}
	}
}

// WithEnableStreaming explicitly controls whether to use streaming protocol.
// If not set (nil), the agent will use the streaming capability from the agent card.
// This option overrides the agent card's capability setting.
func WithEnableStreaming(enable bool) Option {
	return func(a *A2AAgent) {
		a.enableStreaming = &enable
	}
}
