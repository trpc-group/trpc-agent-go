//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tiktoken provides a tiktoken-go based token counter implementation
// that is compatible with the root model.TokenCounter interface.
package tiktoken

import (
	"context"
	"fmt"

	"github.com/tiktoken-go/tokenizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Counter implements a tiktoken-based token counter compatible with model.TokenCounter.
// It uses a tokenizer.Codec to encode message text and counts tokens as the
// length of the returned token slice.
type Counter struct {
	encoding tokenizer.Codec
}

// New creates a tiktoken-based counter.
//
// Parameters:
//   - modelName: OpenAI model name (e.g., "gpt-4o"). The tokenizer is chosen with tokenizer.ForModel.
//     If the model is not supported, falls back to cl100k_base.
//
// Returns:
// - *Counter on success; error if codec initialization fails.
func New(modelName string) (*Counter, error) {
	enc, err := tokenizer.ForModel(tokenizer.Model(modelName))
	if err != nil {
		// Fallback to cl100k_base for broad compatibility.
		enc, err = tokenizer.Get(tokenizer.Cl100kBase)
		if err != nil {
			return nil, fmt.Errorf("failed to get fallback tokenizer: %w", err)
		}
	}
	return &Counter{encoding: enc}, nil
}

// CountTokens returns the token count for a single message using tiktoken-go.
// It encodes Message.Content, Message.ReasoningContent, and text ContentParts.
func (c *Counter) CountTokens(_ context.Context, message model.Message) (int, error) {
	total := 0

	if message.Content != "" {
		toks, _, err := c.encoding.Encode(message.Content)
		if err != nil {
			return 0, fmt.Errorf("encode content failed: %w", err)
		}
		total += len(toks)
	}

	if message.ReasoningContent != "" {
		toks, _, err := c.encoding.Encode(message.ReasoningContent)
		if err != nil {
			return 0, fmt.Errorf("encode reasoning failed: %w", err)
		}
		total += len(toks)
	}

	for _, part := range message.ContentParts {
		if part.Text != nil {
			toks, _, err := c.encoding.Encode(*part.Text)
			if err != nil {
				return 0, fmt.Errorf("encode text part failed: %w", err)
			}
			total += len(toks)
		}
	}

	return total, nil
}

// CountTokensRange returns the token count for a range of messages using tiktoken-go.
// This is more efficient than calling CountTokens multiple times.
func (c *Counter) CountTokensRange(ctx context.Context, messages []model.Message, start, end int) (int, error) {
	if start < 0 || end > len(messages) || start >= end {
		return 0, fmt.Errorf("invalid range: start=%d, end=%d, len=%d", start, end, len(messages))
	}

	total := 0
	for i := start; i < end; i++ {
		tokens, err := c.CountTokens(ctx, messages[i])
		if err != nil {
			return 0, fmt.Errorf("count tokens for message %d failed: %w", i, err)
		}
		total += tokens
	}
	return total, nil
}
