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
