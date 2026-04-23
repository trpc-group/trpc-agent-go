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
	"encoding/json"
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

// A2ADataPartToolResponse is a public tool response payload used by custom
// DataPart mappers.
type A2ADataPartToolResponse struct {
	ID      string
	Name    string
	Content string
}

// A2ADataPartMappingResult is the mapper-visible result holder used to enrich
// conversion output without depending on the converter's internal parseResult.
//
// Mappers receive a snapshot initialized from the current parse state. Changes
// are applied only when the mapper returns matched=true.
type A2ADataPartMappingResult struct {
	textContent            string
	reasoningContent       string
	toolCalls              []model.ToolCall
	toolResponses          []A2ADataPartToolResponse
	codeExecution          string
	codeExecutionResult    string
	eventExtensions        map[string]json.RawMessage
	textContentSet         bool
	reasoningContentSet    bool
	codeExecutionSet       bool
	codeExecutionResultSet bool
}

// GetTextContent returns the current text content snapshot.
func (r *A2ADataPartMappingResult) GetTextContent() string {
	if r == nil {
		return ""
	}
	return r.textContent
}

// SetTextContent overwrites text content when the mapper matches.
func (r *A2ADataPartMappingResult) SetTextContent(text string) {
	if r == nil {
		return
	}
	r.textContent = text
	r.textContentSet = true
}

// GetReasoningContent returns the current reasoning content snapshot.
func (r *A2ADataPartMappingResult) GetReasoningContent() string {
	if r == nil {
		return ""
	}
	return r.reasoningContent
}

// SetReasoningContent overwrites reasoning content when the mapper matches.
func (r *A2ADataPartMappingResult) SetReasoningContent(text string) {
	if r == nil {
		return
	}
	r.reasoningContent = text
	r.reasoningContentSet = true
}

// AppendToolCall appends a tool call when the mapper matches.
func (r *A2ADataPartMappingResult) AppendToolCall(call model.ToolCall) {
	if r == nil {
		return
	}
	r.toolCalls = append(r.toolCalls, call)
}

// AppendToolResponse appends a tool response when the mapper matches.
func (r *A2ADataPartMappingResult) AppendToolResponse(resp A2ADataPartToolResponse) {
	if r == nil {
		return
	}
	r.toolResponses = append(r.toolResponses, resp)
}

// GetCodeExecution returns the current executable code snapshot.
func (r *A2ADataPartMappingResult) GetCodeExecution() string {
	if r == nil {
		return ""
	}
	return r.codeExecution
}

// SetCodeExecution overwrites executable code when the mapper matches.
func (r *A2ADataPartMappingResult) SetCodeExecution(code string) {
	if r == nil {
		return
	}
	r.codeExecution = code
	r.codeExecutionSet = true
}

// GetCodeExecutionResult returns the current code execution result snapshot.
func (r *A2ADataPartMappingResult) GetCodeExecutionResult() string {
	if r == nil {
		return ""
	}
	return r.codeExecutionResult
}

// SetCodeExecutionResult overwrites code execution result when the mapper matches.
func (r *A2ADataPartMappingResult) SetCodeExecutionResult(result string) {
	if r == nil {
		return
	}
	r.codeExecutionResult = result
	r.codeExecutionResultSet = true
}

// SetEventExtension stores one serialized event extension when the mapper matches.
//
// This is useful for preserving custom A2A DataPart payloads through graph and
// server pipelines without forcing them into Message.Content.
func (r *A2ADataPartMappingResult) SetEventExtension(key string, value any) error {
	if r == nil || key == "" {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if r.eventExtensions == nil {
		r.eventExtensions = make(map[string]json.RawMessage)
	}
	r.eventExtensions[key] = cloneA2AExtensionRawMessage(raw)
	return nil
}

func cloneA2AExtensionRawMessage(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	return json.RawMessage(cloned)
}

func cloneA2AExtensions(
	extensions map[string]json.RawMessage,
) map[string]json.RawMessage {
	if len(extensions) == 0 {
		return nil
	}
	cloned := make(map[string]json.RawMessage, len(extensions))
	for key, raw := range extensions {
		cloned[key] = cloneA2AExtensionRawMessage(raw)
	}
	return cloned
}

// A2ADataPartMapper maps an inbound A2A DataPart into the default parser result.
//
// Built-in DataPart handling (function call/response, code execution) runs
// first. Mappers are invoked only when the DataPart is not consumed by the
// built-ins. Returning matched=true means this mapper consumed the part.
// Returning matched=false leaves the part ignored by the default converter.
type A2ADataPartMapper func(part *protocol.DataPart, result *A2ADataPartMappingResult) (
	matched bool,
	err error,
)

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

// WithA2ADataPartMapper registers a lightweight inbound DataPart mapper on the
// default A2A event converter.
//
// If WithCustomEventConverter provides a custom converter, this mapper is
// ignored.
func WithA2ADataPartMapper(mapper A2ADataPartMapper) Option {
	return func(a *A2AAgent) {
		if mapper == nil {
			return
		}
		a.dataPartMappers = append(a.dataPartMappers, mapper)
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

// WithTransferStateKey sets the keys in session state to transfer to the A2A agent message by metadata.
//
// Supported patterns:
//   - "*"         — transfer all keys from RuntimeState
//   - "prefix*"   — transfer keys with the given prefix (e.g. "user.*" or "user*")
//   - "*suffix"   — transfer keys with the given suffix (e.g. "*.id" or "*id")
//   - "exact_key" — transfer only the exact key
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
