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
)

type knowledgeIndex struct {
	model        model.Model
	systemPrompt string
	*ToolKnowledge
}

const defaultSystemPromptWithToolKnowledge = "Your goal is to identify the most relevant tools for answering the user's query." +
	"Provide a natural-language description of the kind of tool needed (e.g., 'weather information', 'currency conversion', 'stock prices')."

func newToolKnowledgeSearcher(m model.Model, systemPrompt string, toolKnowledge *ToolKnowledge) *knowledgeIndex {
	if systemPrompt == "" {
		systemPrompt = defaultSystemPromptWithToolKnowledge
	}
	return &knowledgeIndex{
		model:         m,
		systemPrompt:  systemPrompt,
		ToolKnowledge: toolKnowledge,
	}
}

func (s *knowledgeIndex) rewriteQuery(ctx context.Context, query string) (string, error) {
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(s.systemPrompt),
			model.NewUserMessage(query),
		},
		GenerationConfig: model.GenerationConfig{Stream: false},
	}
	respCh, err := s.model.GenerateContent(ctx, req)
	if err != nil {
		return "", fmt.Errorf("RewriteQuery: selection model call failed: %w", err)
	}

	var final *model.Response
	for r := range respCh {
		if r == nil {
			continue
		}
		if r.Error != nil {
			return "", fmt.Errorf("ToolSearch: selection model returned error: %s", r.Error.Message)
		}
		if !r.IsPartial {
			final = r
		}
	}
	if final == nil || len(final.Choices) == 0 {
		return "", nil
	}

	content := strings.TrimSpace(final.Choices[0].Message.Content)
	if content == "" {
		content = strings.TrimSpace(final.Choices[0].Delta.Content)
	}
	if content == "" {
		return "", nil
	}
	return content, nil
}
