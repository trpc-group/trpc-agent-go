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

import (
	"context"
	"iter"
)

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

// IterModel is an optional interface a Model can implement to stream responses
// in the caller goroutine using Go's iterator protocol.
//
// When implemented, flows may prefer this method to reduce goroutine and
// channel scheduling overhead for streaming use cases.
type IterModel interface {
	GenerateContentIter(ctx context.Context, request *Request) (iter.Seq2[*Response, error], error)
}

// Info contains basic information about a Model.
type Info struct {
	Name string
}
