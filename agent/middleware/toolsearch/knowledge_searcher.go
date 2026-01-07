//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type knowledgeSearcher struct {
	model         model.Model
	systemPrompt  string
	toolKnowledge *ToolKnowledge
}

const defaultSystemPromptWithToolKnowledge = "Your goal is to identify the most relevant tools for answering the user's query. " +
	"Provide a natural-language description of the kind of tool needed (e.g., 'weather information', 'currency conversion', 'stock prices')."

func newKnowledgeSearcher(m model.Model, systemPrompt string, toolKnowledge *ToolKnowledge) *knowledgeSearcher {
	if systemPrompt == "" {
		systemPrompt = defaultSystemPromptWithToolKnowledge
	}
	return &knowledgeSearcher{
		model:         m,
		systemPrompt:  systemPrompt,
		toolKnowledge: toolKnowledge,
	}
}

func (s *knowledgeSearcher) Search(ctx context.Context, candidates map[string]tool.Tool, query string, topK int) ([]string, error) {
	query, err := s.rewriteQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	if err := s.toolKnowledge.upsert(ctx, candidates); err != nil {
		return nil, err
	}
	return s.toolKnowledge.search(ctx, candidates, query, topK)
}

func (s *knowledgeSearcher) rewriteQuery(ctx context.Context, query string) (string, error) {
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(s.systemPrompt),
			model.NewUserMessage(query),
		},
		GenerationConfig: model.GenerationConfig{Stream: false},
	}
	respCh, err := s.model.GenerateContent(ctx, req)
	if err != nil {
		return "", fmt.Errorf("rewriting query: selection model call failed: %w", err)
	}

	var final *model.Response
	for r := range respCh {
		if r == nil {
			continue
		}
		if r.Error != nil {
			return "", fmt.Errorf("rewriting query: selection model returned error: %s", r.Error.Message)
		}
		if !r.IsPartial {
			final = r
		}
	}
	if final == nil || len(final.Choices) == 0 {
		return "", fmt.Errorf("rewriting query: selection model returned empty response")
	}

	content := strings.TrimSpace(final.Choices[0].Message.Content)
	if content == "" {
		content = strings.TrimSpace(final.Choices[0].Delta.Content)
	}
	if content == "" {
		return "", fmt.Errorf("rewriting query: selection model returned empty content")
	}
	return content, nil
}
