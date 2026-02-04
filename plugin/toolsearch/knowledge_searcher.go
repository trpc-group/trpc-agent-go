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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
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

func (s *knowledgeSearcher) Search(ctx context.Context, candidates map[string]tool.Tool, query string, topK int) (newCtx context.Context, selectedTools []string, err error) {
	newCtx, query, usage, err := s.rewriteQuery(ctx, query)
	if err != nil {
		return ctx, nil, err
	}
	defer func() {
		newCtx = SetToolSearchUsage(newCtx, usage)
	}()
	upsertUsage, err := s.toolKnowledge.upsert(newCtx, candidates)
	if err != nil {
		return ctx, nil, err
	}
	usage.CompletionTokens += upsertUsage.CompletionTokens
	usage.PromptTokens += upsertUsage.PromptTokens
	usage.TotalTokens += upsertUsage.TotalTokens

	newCtx, selectedTools, searchUsage, err := s.toolKnowledge.search(newCtx, candidates, query, topK)
	if err != nil {
		return newCtx, nil, err
	}
	usage.CompletionTokens += searchUsage.CompletionTokens
	usage.PromptTokens += searchUsage.PromptTokens
	usage.TotalTokens += searchUsage.TotalTokens
	return newCtx, selectedTools, err
}

func (s *knowledgeSearcher) rewriteQuery(ctx context.Context, query string) (context.Context, string, *model.Usage, error) {
	var err error

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(s.systemPrompt),
			model.NewUserMessage(query),
		},
		GenerationConfig: model.GenerationConfig{Stream: false},
	}
	_, span := trace.Tracer.Start(ctx, itelemetry.NewChatSpanName(s.model.Info().Name))
	defer span.End()
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		invocation = agent.NewInvocation()
	}
	timingInfo := invocation.GetOrCreateTimingInfo()
	tracker := itelemetry.NewChatMetricsTracker(ctx, invocation, req, timingInfo, nil, &err)
	defer tracker.RecordMetrics()()
	respCh, err := s.model.GenerateContent(ctx, req)
	if err != nil {
		return ctx, "", nil, fmt.Errorf("rewriting query: selection model call failed: %w", err)
	}

	var final *model.Response
	for r := range respCh {
		if r == nil {
			continue
		}
		if r.Error != nil {
			return ctx, "", nil, fmt.Errorf("rewriting query: selection model returned error: %s", r.Error.Message)
		}
		if !r.IsPartial {
			final = r
		}
	}
	if final == nil || len(final.Choices) == 0 {
		return ctx, "", nil, fmt.Errorf("rewriting query: selection model returned empty response")
	}

	content := strings.TrimSpace(final.Choices[0].Message.Content)
	if content == "" {
		content = strings.TrimSpace(final.Choices[0].Delta.Content)
	}
	if content == "" {
		return ctx, "", nil, fmt.Errorf("rewriting query: selection model returned empty content")
	}
	tracker.TrackResponse(final)
	if final.Usage == nil {
		final.Usage = &model.Usage{}
	}
	final.Usage.TimingInfo = timingInfo
	itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{
		Invocation:       invocation,
		Request:          req,
		Response:         final,
		EventID:          "",
		TimeToFirstToken: tracker.FirstTokenTimeDuration(),
	})

	return ctx, content, final.Usage, nil
}
