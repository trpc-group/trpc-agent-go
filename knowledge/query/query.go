//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package query provides query enhancement and processing for knowledge systems.
package query

import "context"

// Enhancer enhances user queries for better search results.
type Enhancer interface {
	// EnhanceQuery improves a user query by expanding or rephrasing it.
	// Context includes conversation history for better understanding.
	EnhanceQuery(ctx context.Context, req *Request) (*Enhanced, error)
}

// Request represents a query enhancement request with context.
type Request struct {
	// Query is the user's current query text.
	Query string

	// History contains recent conversation messages for context.
	// Should be limited to last N messages to avoid overwhelming the enhancer.
	History []ConversationMessage

	// UserID can help with personalized query enhancement.
	UserID string

	// SessionID can help with session-specific context.
	SessionID string
}

// ConversationMessage represents a message in conversation history.
type ConversationMessage struct {
	// Role indicates if this is from user or assistant.
	Role string

	// Content is the message content.
	Content string

	// Timestamp when the message was sent.
	Timestamp int64
}

// Enhanced represents an enhanced search query.
type Enhanced struct {
	// Enhanced is the improved query text.
	Enhanced string

	// Keywords contains extracted key terms.
	Keywords []string
}
