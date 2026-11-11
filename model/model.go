//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package model provides interfaces for working with LLMs.
package model

import "context"

// Model is the interface for all language models.
//
// Error Handling Strategy:
// This interface uses a dual-layer error handling approach:
//
// 1. Function-level errors (returned as `error`):
//   - System-level failures that prevent communication
//   - Examples: nil request, network issues, invalid parameters
//   - These prevent the channel from being created or used
//
// 2. Response-level errors (Response.Error field):
//   - API-level errors returned by the model service
//   - Examples: API rate limits, content filtering, model errors
//   - These are delivered through the response channel as structured errors
//
// Usage pattern:
//
//	responseChan, err := model.GenerateContent(ctx, request)
//	if err != nil {
//	    // Handle system-level errors (cannot communicate)
//	    return fmt.Errorf("failed to generate content: %w", err)
//	}
//
//	for response := range responseChan {
//	    if response.Error != nil {
//	        // Handle API-level errors (communication succeeded, but API returned error)
//	        return fmt.Errorf("API error: %s", response.Error.Message)
//	    }
//	    // Process successful response...
//	}
type Model interface {
	// GenerateContent generates content from the given request.
	//
	// Returns:
	// - A channel of Response objects for streaming results
	// - An error for system-level failures (prevents communication)
	//
	// The Response objects may contain their own Error field for API-level errors.
	GenerateContent(ctx context.Context, request *Request) (<-chan *Response, error)

	// Info returns basic information about the model.
	Info() Info
}

// ProviderDataProjector is an optional model capability for rebuilding message
// fields after an AfterModel callback edits native provider data. It does not
// change the Model interface or the behavior of messages without ProviderData.
// Wrappers around a model that implements this capability should forward it.
type ProviderDataProjector interface {
	// ProjectProviderData returns the display fields and tool calls represented
	// by data. It must not mutate data or perform model or tool execution. The
	// framework applies the projection before publishing the completed response.
	// Invalid records return an error before any local tools execute.
	ProjectProviderData(data *ProviderData) (Message, error)
}

// ProviderDataRequestProjector extends ProviderDataProjector when native output
// needs the originating request to resolve tool identities, such as namespace
// members registered under different framework tool names. Flows prefer this
// capability after AfterModel callbacks when the request is available.
// Wrappers should forward it when their candidates implement it.
type ProviderDataRequestProjector interface {
	ProviderDataProjector

	// ProjectProviderDataWithRequest rebuilds message fields using request's
	// tool bindings. It must not mutate either argument or execute tools or
	// model requests, and must support concurrent calls. A nil request has the
	// same behavior as ProjectProviderData.
	ProjectProviderDataWithRequest(request *Request, data *ProviderData) (Message, error)
}

// ProviderActionCompleter is an optional model capability for provider-native
// actions that a model delegates to the caller and that are not tool calls,
// such as a client-executed tool search. A model requests such actions by
// returning a completed response that carries ProviderData, has Done set to
// false, and has no Error.
//
// After the response is published, the LLM agent flow calls
// CompleteProviderActions with the request and the final response of that
// model call, before it executes the response's ordinary tool calls. Each
// returned message must be a tool message whose ToolID names the answered
// action and whose ProviderData belongs to the same protocol and model. The
// flow publishes and persists the messages in order and then requests another
// model step, so the next request carries them in history. A nil result
// means the response has no remaining provider action to complete. On resume,
// request.Messages includes saved native results so completed actions can be
// reused rather than repeated.
//
// An error stops the invocation without recording a result, and the action
// remains unanswered in history; a later run with agent.WithResume completes
// it again. A model that requests provider actions without implementing this
// interface, including a wrapper that does not forward it, stops the
// invocation with an error. Flows that cannot complete provider actions, such
// as Graph LLM nodes, report an error instead of treating the response as
// final. Models that never request provider actions need not implement this
// interface.
type ProviderActionCompleter interface {
	// CompleteProviderActions performs the actions requested by response and
	// returns their native results. It must not mutate request or response and
	// should stop when ctx is canceled. Like GenerateContent, it may be called
	// concurrently for different invocations.
	CompleteProviderActions(ctx context.Context, request *Request, response *Response) ([]Message, error)
}

// Seq is a callback-based sequence that yields values.
type Seq[T any] func(yield func(T) bool)

// IterModel is an optional extension of Model that streams responses in the caller goroutine.
// When implemented, flows may prefer this method to reduce goroutine and channel scheduling overhead for streaming use cases.
// Implementations should yield *Response values, including API-level and stream-level errors encoded in Response.Error.
// The returned error is reserved for failures that prevent creating the iterator.
type IterModel interface {
	Model
	GenerateContentIter(ctx context.Context, request *Request) (Seq[*Response], error)
}

// Info contains basic information about a Model.
type Info struct {
	// Name is the model identifier used by providers and registry lookup.
	Name string
	// ContextWindow is the model context window in tokens.
	// A non-positive value means the window is unknown for this model instance.
	ContextWindow int
	// ProviderDataProtocol identifies the protocol of provider data consumed by this
	// model. An empty value means ProviderData is unsupported. LLM flows and
	// Graph LLM nodes reject history from another protocol or configured model
	// before calling the adapter. Wrappers must preserve this value when
	// forwarding Info.
	ProviderDataProtocol string `json:"ProviderDataProtocol,omitempty"`
}
