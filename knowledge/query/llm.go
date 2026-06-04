//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package query

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const defaultSystemPrompt = `Given a chat history and the latest user question, rewrite the question into a standalone search query optimized for vector database retrieval.

Rules:
- Output ONLY the rewritten query, nothing else.
- Resolve any references (e.g. "it", "that", "the above") using conversation context.
- Remove conversational noise, greetings, and formatting instructions.
- Keep the query concise and focused on key concepts.
- If the question is already a clear, standalone query, return it as is.`

// LLMEnhancer uses a language model to rewrite queries for better retrieval.
type LLMEnhancer struct {
	model        model.Model
	systemPrompt string
}

// LLMEnhancerOption configures an LLMEnhancer.
type LLMEnhancerOption func(*LLMEnhancer)

// WithSystemPrompt overrides the default system prompt.
func WithSystemPrompt(prompt string) LLMEnhancerOption {
	return func(e *LLMEnhancer) {
		e.systemPrompt = prompt
	}
}

// NewLLMEnhancer creates a query enhancer that uses an LLM to rewrite queries.
func NewLLMEnhancer(m model.Model, opts ...LLMEnhancerOption) *LLMEnhancer {
	e := &LLMEnhancer{
		model:        m,
		systemPrompt: defaultSystemPrompt,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// EnhanceQuery rewrites the query using the LLM with conversation context.
func (e *LLMEnhancer) EnhanceQuery(ctx context.Context, req *Request) (*Enhanced, error) {
	if req == nil || req.Query == "" {
		return &Enhanced{}, nil
	}

	messages := []model.Message{
		model.NewSystemMessage(e.systemPrompt),
	}

	for _, h := range req.History {
		switch h.Role {
		case "user":
			messages = append(messages, model.NewUserMessage(h.Content))
		case "assistant":
			messages = append(messages, model.NewAssistantMessage(h.Content))
		}
	}

	messages = append(messages, model.NewUserMessage(req.Query))

	ch, err := e.model.GenerateContent(ctx, &model.Request{
		Messages: messages,
	})
	if err != nil {
		return nil, fmt.Errorf("query enhance LLM call failed: %w", err)
	}

	var result strings.Builder
	for resp := range ch {
		if resp.Error != nil {
			return nil, fmt.Errorf("query enhance LLM error: %s", resp.Error.Message)
		}
		for _, choice := range resp.Choices {
			if choice.Message.Content != "" {
				result.WriteString(choice.Message.Content)
			}
			if choice.Delta.Content != "" {
				result.WriteString(choice.Delta.Content)
			}
		}
	}

	enhanced := strings.TrimSpace(result.String())
	if enhanced == "" {
		enhanced = req.Query
	}

	return &Enhanced{
		Enhanced: enhanced,
	}, nil
}
